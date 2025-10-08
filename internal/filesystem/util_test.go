package filesystem

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"github.com/klauspost/compress/zip"
	"github.com/stretchr/testify/require"
)

// Expectation: newZipMetric should create a zipMetric with correct initial values for extract operation.
func Test_newZipMetric_Extract_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	before := time.Now()
	zm := newZipMetric(fsys, true)
	after := time.Now()

	require.NotNil(t, zm)
	require.Equal(t, fsys, zm.fsys)
	require.True(t, zm.isExtract)
	require.GreaterOrEqual(t, zm.startTime.UnixNano(), before.UnixNano())
	require.LessOrEqual(t, zm.startTime.UnixNano(), after.UnixNano())
	require.Equal(t, int64(0), zm.readBytes)
}

// Expectation: newZipMetric should create a zipMetric with correct initial values for metadata operation.
func Test_newZipMetric_Metadata_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	before := time.Now()
	zm := newZipMetric(fsys, false)
	after := time.Now()

	require.NotNil(t, zm)
	require.Equal(t, fsys, zm.fsys)
	require.False(t, zm.isExtract)
	require.GreaterOrEqual(t, zm.startTime.UnixNano(), before.UnixNano())
	require.LessOrEqual(t, zm.startTime.UnixNano(), after.UnixNano())
	require.Equal(t, int64(0), zm.readBytes)
}

// Expectation: zipMetric.Done should update extract metrics correctly.
func Test_zipMetric_Done_Extract_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialExtractTime := fsys.Metrics.TotalExtractTime.Load()
	initialExtractCount := fsys.Metrics.TotalExtractCount.Load()
	initialExtractBytes := fsys.Metrics.TotalExtractBytes.Load()

	zm := newZipMetric(fsys, true)
	zm.readBytes = 1024

	time.Sleep(10 * time.Millisecond)
	zm.Done()

	require.Greater(t, fsys.Metrics.TotalExtractTime.Load(), initialExtractTime)
	require.Equal(t, initialExtractCount+1, fsys.Metrics.TotalExtractCount.Load())
	require.Equal(t, initialExtractBytes+1024, fsys.Metrics.TotalExtractBytes.Load())
}

// Expectation: zipMetric.Done should update metadata metrics correctly.
func Test_zipMetric_Done_Metadata_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialMetadataTime := fsys.Metrics.TotalMetadataReadTime.Load()
	initialMetadataCount := fsys.Metrics.TotalMetadataReadCount.Load()

	zm := newZipMetric(fsys, false)
	zm.readBytes = 512 // Should be ignored for metadata operations

	time.Sleep(10 * time.Millisecond)
	zm.Done()

	require.Greater(t, fsys.Metrics.TotalMetadataReadTime.Load(), initialMetadataTime)
	require.Equal(t, initialMetadataCount+1, fsys.Metrics.TotalMetadataReadCount.Load())
}

// Expectation: zipMetric.Done should not update extract metrics when isExtract is false.
func Test_zipMetric_Done_Metadata_NoExtractUpdate_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialExtractTime := fsys.Metrics.TotalExtractTime.Load()
	initialExtractCount := fsys.Metrics.TotalExtractCount.Load()
	initialExtractBytes := fsys.Metrics.TotalExtractBytes.Load()

	zm := newZipMetric(fsys, false)
	zm.readBytes = 2048
	zm.Done()

	require.Equal(t, initialExtractTime, fsys.Metrics.TotalExtractTime.Load())
	require.Equal(t, initialExtractCount, fsys.Metrics.TotalExtractCount.Load())
	require.Equal(t, initialExtractBytes, fsys.Metrics.TotalExtractBytes.Load())
}

// Expectation: zipMetric.Done should not update metadata metrics when isExtract is true.
func Test_zipMetric_Done_Extract_NoMetadataUpdate_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialMetadataTime := fsys.Metrics.TotalMetadataReadTime.Load()
	initialMetadataCount := fsys.Metrics.TotalMetadataReadCount.Load()

	zm := newZipMetric(fsys, true)
	zm.readBytes = 2048
	zm.Done()

	require.Equal(t, initialMetadataTime, fsys.Metrics.TotalMetadataReadTime.Load())
	require.Equal(t, initialMetadataCount, fsys.Metrics.TotalMetadataReadCount.Load())
}

// Expectation: zipMetric should track zero bytes read correctly.
func Test_zipMetric_Done_ZeroBytes_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialExtractBytes := fsys.Metrics.TotalExtractBytes.Load()

	zm := newZipMetric(fsys, true)
	zm.readBytes = 0
	zm.Done()

	require.Equal(t, initialExtractBytes, fsys.Metrics.TotalExtractBytes.Load())
}

// Expectation: zipMetric should track large byte counts correctly.
func Test_zipMetric_Done_LargeBytes_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialExtractBytes := fsys.Metrics.TotalExtractBytes.Load()

	zm := newZipMetric(fsys, true)
	zm.readBytes = 1024 * 1024 * 100 // 100MB
	zm.Done()

	require.Equal(t, initialExtractBytes+1024*1024*100, fsys.Metrics.TotalExtractBytes.Load())
}

// Expectation: Multiple zipMetric operations should accumulate correctly.
func Test_zipMetric_Done_Multiple_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	initialExtractCount := fsys.Metrics.TotalExtractCount.Load()
	initialExtractBytes := fsys.Metrics.TotalExtractBytes.Load()

	zm1 := newZipMetric(fsys, true)
	zm1.readBytes = 512
	zm1.Done()

	zm2 := newZipMetric(fsys, true)
	zm2.readBytes = 1024
	zm2.Done()

	zm3 := newZipMetric(fsys, true)
	zm3.readBytes = 256
	zm3.Done()

	require.Equal(t, initialExtractCount+3, fsys.Metrics.TotalExtractCount.Load())
	require.Equal(t, initialExtractBytes+1792, fsys.Metrics.TotalExtractBytes.Load())
}

// Expectation: flatEntryName should flatten paths correctly and produce hashes.
func Test_flatEntryName_Success(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input    string
		expected string
		valid    bool
	}{
		{"dir/file.txt", "file_", true},
		{"a/b/c/test.log", "test_", true},
		{"file.txt", "file_", true},
		{"deep/nested/path/document.pdf", "document_", true},
	}

	for _, tc := range testCases {
		result, valid := flatEntryName(tc.input)
		require.Equal(t, tc.valid, valid)
		if valid {
			require.Contains(t, result, tc.expected)
			require.Greater(t, len(result), len(tc.expected))
		}
	}
}

// Expectation: flatEntryName should preserve file extensions.
func Test_flatEntryName_PreserveExtension_Success(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input string
		ext   string
	}{
		{"dir/file.txt", ".txt"},
		{"path/to/document.pdf", ".pdf"},
		{"nested/script.sh", ".sh"},
		{"archive.tar.gz", ".gz"},
	}

	for _, tc := range testCases {
		result, valid := flatEntryName(tc.input)
		require.True(t, valid)
		require.Equal(t, filepath.Ext(result), tc.ext)
	}
}

// Expectation: flatEntryName should generate different hashes for different paths.
func Test_flatEntryName_UniqueHashes_Success(t *testing.T) {
	t.Parallel()

	name1, valid1 := flatEntryName("dir1/file.txt")
	require.True(t, valid1)

	name2, valid2 := flatEntryName("dir2/file.txt")
	require.True(t, valid2)

	// Same filename but different paths should have different hashes
	require.NotEqual(t, name1, name2)
}

// Expectation: flatEntryName should generate consistent hashes for the same path.
func Test_flatEntryName_DeterministicHashes_Success(t *testing.T) {
	t.Parallel()

	path := "some/deep/path/file.txt"

	name1, valid1 := flatEntryName(path)
	require.True(t, valid1)

	name2, valid2 := flatEntryName(path)
	require.True(t, valid2)

	// Same path should always generate the same hash
	require.Equal(t, name1, name2)
}

// Expectation: flatEntryName should handle files without extensions.
func Test_flatEntryName_NoExtension_Success(t *testing.T) {
	t.Parallel()

	name, valid := flatEntryName("dir/README")
	require.True(t, valid)
	require.Contains(t, name, "README_")
	require.Empty(t, filepath.Ext(name))
	require.Greater(t, len(name), len("README_"))
}

// Expectation: flatEntryName should handle paths with multiple dots.
func Test_flatEntryName_MultipleDots_Success(t *testing.T) {
	t.Parallel()

	name, valid := flatEntryName("dir/archive.tar.gz")
	require.True(t, valid)
	require.Contains(t, name, "archive.tar_")
	require.Equal(t, ".gz", filepath.Ext(name))
	require.Greater(t, len(name), len("archive.tar_"))
}

// Expectation: flatEntryName should return false for invalid paths.
func Test_flatEntryName_InvalidPaths_Error(t *testing.T) {
	t.Parallel()

	testCases := []string{
		".",
		"..",
		"../file.txt",
		"/",
	}

	for _, tc := range testCases {
		_, valid := flatEntryName(tc)
		require.False(t, valid)
	}
}

// Expectation: toFuseErr should convert os.ErrNotExist to ENOENT.
func Test_toFuseErr_NotExist_Success(t *testing.T) {
	t.Parallel()

	err := toFuseErr(os.ErrNotExist)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: toFuseErr should convert os.ErrPermission to EACCES.
func Test_toFuseErr_Permission_Success(t *testing.T) {
	t.Parallel()

	err := toFuseErr(os.ErrPermission)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EACCES))
}

// Expectation: toFuseErr should convert other errors to EIO.
func Test_toFuseErr_Other_Success(t *testing.T) {
	t.Parallel()

	customErr := errors.New("test")
	err := toFuseErr(customErr)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EIO))
}

// Expectation: toFuseErr should handle wrapped os.ErrNotExist.
func Test_toFuseErr_WrappedNotExist_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	_, osErr := os.Open(filepath.Join(tmpDir, "nonexistent.txt"))
	require.Error(t, osErr)

	err := toFuseErr(osErr)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: The function should behave according to the table expectations.
func Test_isDir_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fileName  string
		isDirAttr bool
		want      bool
	}{
		{"file with dir attribute", "folder/", true, true},
		{"file with suffix slash", "fake.txt/", false, true},
		{"regular file", "file.txt", false, false},
		{"regular file in subdir", "folder/file.txt", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &zip.File{
				FileHeader: zip.FileHeader{
					Name: tt.fileName,
				},
			}

			if tt.isDirAttr {
				f.SetMode(0o755 | os.ModeDir)
			}

			got := isDir(f, normalizeZipPath(tt.fileName))
			require.Equal(t, tt.want, got)
		})
	}
}

// Expectation: The function should behave according to the table expectations.
func Test_normalizeZipPath_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"foo//bar/baz.txt", "foo/bar/baz.txt"},
		{"/leading/slash.txt", "leading/slash.txt"},
		{"normal/path.txt", "normal/path.txt"},
		{"////file.txt", "file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := normalizeZipPath(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}
