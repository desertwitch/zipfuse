package filesystem

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"github.com/stretchr/testify/require"
)

// Expectation: newZipReaderCache should create a cache with correct initial state.
func Test_newZipReaderCache_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	require.NotNil(t, cache)
	require.Equal(t, fsys, cache.fsys)
	require.NotNil(t, cache.cache)
}

// Expectation: zipReaderCache.Archive should return a new zipReader for uncached archive.
func Test_zipReaderCache_Archive_Uncached_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, err := cache.Archive(zipPath)

	require.NoError(t, err)
	require.NotNil(t, zr)
	require.Equal(t, int32(2), zr.refCount.Load()) // Cache ref + caller ref

	err = zr.Release()
	require.NoError(t, err)

	err = zr.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache.Archive should return cached zipReader for subsequent calls.
func Test_zipReaderCache_Archive_Cached_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr1, err := cache.Archive(zipPath)
	require.NoError(t, err)
	require.NotNil(t, zr1)

	zr2, err := cache.Archive(zipPath)
	require.NoError(t, err)
	require.NotNil(t, zr2)

	// Should be the same zipReader instance
	require.Equal(t, zr1, zr2)
	require.Equal(t, int32(3), zr1.refCount.Load()) // Cache ref + 2 caller refs

	err = zr1.Release()
	require.NoError(t, err)
	err = zr2.Release()
	require.NoError(t, err)

	err = zr1.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache.Archive should return error for non-existent archive.
func Test_zipReaderCache_Archive_NotExist_Error(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, err := cache.Archive("/nonexistent/archive.zip")
	require.Nil(t, zr)
	require.Error(t, err)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: zipReaderCache.Archive should return error for invalid archive.
func Test_zipReaderCache_Archive_InvalidZip_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	invalidPath := filepath.Join(tmpDir, "invalid.zip")
	err := os.WriteFile(invalidPath, []byte("not a zip file"), 0o644)
	require.NoError(t, err)

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, err := cache.Archive(invalidPath)
	require.Nil(t, zr)
	require.Error(t, err)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: zipReaderCache.Entry should return zipReader and zipFileReader for valid entry.
func Test_zipReaderCache_Entry_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, fr, err := cache.Entry(zipPath, "test.txt")
	require.NoError(t, err)
	require.NotNil(t, zr)
	require.NotNil(t, fr)
	require.Equal(t, int32(2), zr.refCount.Load()) // Cache ref + caller ref

	data, err := io.ReadAll(fr)
	require.NoError(t, err)
	require.Equal(t, content, data)

	err = fr.Close()
	require.NoError(t, err)
	err = zr.Release()
	require.NoError(t, err)

	err = zr.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache.Entry should return cached zipReader for subsequent calls.
func Test_zipReaderCache_Entry_Cached_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr1, fr1, err := cache.Entry(zipPath, "test.txt")
	require.NoError(t, err)
	require.NotNil(t, zr1)
	require.NotNil(t, fr1)

	zr2, fr2, err := cache.Entry(zipPath, "test.txt")
	require.NoError(t, err)
	require.NotNil(t, zr2)
	require.NotNil(t, fr2)

	require.Equal(t, zr1, zr2)                      // Should be the same zipReader instance
	require.Equal(t, int32(3), zr1.refCount.Load()) // Cache ref + 2 caller refs

	err = fr1.Close()
	require.NoError(t, err)
	err = fr2.Close()
	require.NoError(t, err)
	err = zr1.Release()
	require.NoError(t, err)
	err = zr2.Release()
	require.NoError(t, err)

	err = zr1.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache.Entry should return error for non-existent entry.
func Test_zipReaderCache_Entry_NotExist_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, fr, err := cache.Entry(zipPath, "nonexistent.txt")
	require.Nil(t, zr)
	require.Nil(t, fr)
	require.Error(t, err)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: zipReaderCache.Entry should return error for non-existent archive.
func Test_zipReaderCache_Entry_ArchiveNotExist_Error(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, fr, err := cache.Entry("/nonexistent/archive.zip", "test.txt")
	require.Nil(t, zr)
	require.Nil(t, fr)
	require.Error(t, err)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: zipReaderCache.Entry should return error for invalid archive.
func Test_zipReaderCache_Entry_InvalidZip_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	invalidPath := filepath.Join(tmpDir, "invalid.zip")
	err := os.WriteFile(invalidPath, []byte("not a zip file"), 0o644)
	require.NoError(t, err)

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr, fr, err := cache.Entry(invalidPath, "test.txt")
	require.Nil(t, zr)
	require.Nil(t, fr)
	require.Error(t, err)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: zipReaderCache.Entry should update metadata metrics.
func Test_zipReaderCache_Entry_Metrics_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	initialMetadataCount := fsys.Metrics.TotalMetadataReadCount.Load()

	zr, fr, err := cache.Entry(zipPath, "test.txt")
	require.NoError(t, err)
	require.NotNil(t, zr)
	require.NotNil(t, fr)

	require.Equal(t, initialMetadataCount+1, fsys.Metrics.TotalMetadataReadCount.Load())

	err = fr.Close()
	require.NoError(t, err)
	err = zr.Release()
	require.NoError(t, err)

	err = zr.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache should handle multiple entries in the same archive.
func Test_zipReaderCache_Entry_MultipleEntries_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content1 := []byte("content 1")
	content2 := []byte("content 2")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "file1.txt", ModTime: tnow, Content: content1},
		{Path: "file2.txt", ModTime: tnow, Content: content2},
	})

	cache := newZipReaderCache(fsys, 10, 5*time.Minute)

	zr1, fr1, err := cache.Entry(zipPath, "file1.txt")
	require.NoError(t, err)
	require.NotNil(t, zr1)
	require.NotNil(t, fr1)

	zr2, fr2, err := cache.Entry(zipPath, "file2.txt")
	require.NoError(t, err)
	require.NotNil(t, zr2)
	require.NotNil(t, fr2)

	require.Equal(t, zr1, zr2) // Should be the same zipReader instance

	data1, err := io.ReadAll(fr1)
	require.NoError(t, err)
	require.Equal(t, content1, data1)

	data2, err := io.ReadAll(fr2)
	require.NoError(t, err)
	require.Equal(t, content2, data2)

	err = fr1.Close()
	require.NoError(t, err)
	err = fr2.Close()
	require.NoError(t, err)
	err = zr1.Release()
	require.NoError(t, err)
	err = zr2.Release()
	require.NoError(t, err)

	err = zr1.Release() // cleanup cache ref
	require.NoError(t, err)
}

// Expectation: zipReaderCache should evict entries when cache size is exceeded.
func Test_zipReaderCache_Eviction_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")

	zipPath1 := createTestZip(t, tmpDir, "test1.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zipPath2 := createTestZip(t, tmpDir, "test2.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zipPath3 := createTestZip(t, tmpDir, "test3.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	// Create cache with size 2
	cache := newZipReaderCache(fsys, 2, 5*time.Minute)

	// Add first archive
	zr1, err := cache.Archive(zipPath1)
	require.NoError(t, err)
	require.Equal(t, int64(1), fsys.Metrics.TotalOpenedZips.Load())
	err = zr1.Release()
	require.NoError(t, err)

	// Add second archive
	zr2, err := cache.Archive(zipPath2)
	require.NoError(t, err)
	require.Equal(t, int64(2), fsys.Metrics.TotalOpenedZips.Load())
	err = zr2.Release()
	require.NoError(t, err)

	// Add third archive (should evict first)
	zr3, err := cache.Archive(zipPath3)
	require.NoError(t, err)
	require.Equal(t, int64(3), fsys.Metrics.TotalOpenedZips.Load())
	require.Equal(t, int64(1), fsys.Metrics.TotalClosedZips.Load())
	err = zr3.Release()
	require.NoError(t, err)

	err = zr2.Release() // cleanup cache ref
	require.NoError(t, err)

	err = zr3.Release() // cleanup cache ref
	require.NoError(t, err)

	err = zr1.Release() // should already be closed on size-caused eviction
	require.ErrorContains(t, err, "already closed")
}

// Expectation: zipReaderCache should expire entries after TTL.
func Test_zipReaderCache_Expiration_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	cache := newZipReaderCache(fsys, 10, 100*time.Millisecond)

	zr1, err := cache.Archive(zipPath)
	require.NoError(t, err)
	require.Equal(t, int64(1), fsys.Metrics.TotalOpenedZips.Load())
	err = zr1.Release()
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond) // wait until TTL

	// Access again should create new zipReader
	zr2, err := cache.Archive(zipPath)
	require.NoError(t, err)
	require.Equal(t, int64(2), fsys.Metrics.TotalOpenedZips.Load())
	require.Equal(t, int64(1), fsys.Metrics.TotalClosedZips.Load())
	err = zr2.Release()
	require.NoError(t, err)

	require.NotEqual(t, zr1, zr2) // should not be the same zipReader

	err = zr2.Release() // cleanup cache ref
	require.NoError(t, err)

	err = zr1.Release() // should already be closed on TTL-caused eviction
	require.ErrorContains(t, err, "already closed")
}
