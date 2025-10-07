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

// Expectation: newZipReader should return an error for a non-existent file.
func Test_newZipReader_NotExist_Error(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)
	zr, err := newZipReader(fsys, "/nonexistent/path.zip")
	require.Nil(t, zr)
	require.Error(t, err)
}

// Expectation: newZipReader should return an error for an invalid ZIP file.
func Test_newZipReader_InvalidZip_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	invalidPath := filepath.Join(tmpDir, "invalid.zip")
	err := os.WriteFile(invalidPath, []byte("not a zip file"), 0o644)
	require.NoError(t, err)

	zr, err := newZipReader(fsys, invalidPath)
	require.Nil(t, zr)
	require.Error(t, err)
}

// Expectation: zipReader should track open/close metrics correctly.
func Test_zipReader_Metrics_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content for extraction")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := newZipReader(fsys, zipPath)
	require.NoError(t, err)
	require.NotNil(t, zr)

	require.Equal(t, int64(1), fsys.Metrics.OpenZips.Load())
	require.Equal(t, int64(1), fsys.Metrics.TotalOpenedZips.Load())

	for _, f := range zr.File {
		if f.Name == "test.txt" {
			rc, err := f.Open()
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, rc)
			require.NoError(t, err)
			rc.Close()
		}
	}

	err = zr.Release()
	require.NoError(t, err)

	require.Equal(t, int64(0), fsys.Metrics.OpenZips.Load())
	require.Equal(t, int64(1), fsys.Metrics.TotalClosedZips.Load())
}

// Expectation: zipReader should track internal reference count correctly.
func Test_zipReader_ReferenceCount_Success(t *testing.T) {
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

	zr, err := newZipReader(fsys, zipPath)
	require.NoError(t, err)
	require.NotNil(t, zr)

	require.Equal(t, int32(1), zr.refCount.Load())
	zr.Acquire()
	require.Equal(t, int32(2), zr.refCount.Load())
	err = zr.Release()
	require.NoError(t, err)
	require.Equal(t, int32(1), zr.refCount.Load())

	for _, f := range zr.File {
		if f.Name == "test.txt" {
			rc, err := f.Open()
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, rc)
			require.NoError(t, err)
			rc.Close()
		}
	}

	err = zr.Release()
	require.NoError(t, err)
	require.Zero(t, zr.refCount.Load())
}

// Expectation: A direct Close() call on a zipReader should panic.
func Test_zipReader_DirectClose_Panic(t *testing.T) {
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

	zr, err := newZipReader(fsys, zipPath)
	require.NoError(t, err)
	require.NotNil(t, zr)

	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic, got nil")
		_ = zr.Release()
	}()

	_ = zr.Close()
}

// Expectation: newZipFileReader should successfully open a stored (uncompressed) file.
func Test_newZipFileReader_Store_Success(t *testing.T) {
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

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	require.Len(t, zr.File, 1)

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	require.NotNil(t, fr)
	defer fr.Close()

	data, err := io.ReadAll(fr)
	require.NoError(t, err)
	require.Equal(t, content, data)
}

// Expectation: newZipFileReader should successfully open a compressed file.
func Test_newZipFileReader_Deflate_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	zipPath := filepath.Join(tmpDir, "test.zip")
	f, err := os.Create(zipPath)
	require.NoError(t, err)

	zw := zip.NewWriter(f)
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "compressed.txt",
		Method: zip.Deflate,
	})
	require.NoError(t, err)

	content := []byte("compressed test content")
	_, err = w.Write(content)
	require.NoError(t, err)

	err = zw.Close()
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	require.NotNil(t, fr)
	defer fr.Close()

	data, err := io.ReadAll(fr)
	require.NoError(t, err)
	require.Equal(t, content, data)
}

// Expectation: zipFileReader.Read should correctly track position.
func Test_zipFileReader_Read_Position_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("0123456789")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	require.Equal(t, int64(0), fr.Position())

	buf := make([]byte, 3)
	n, err := fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, int64(3), fr.Position())
	require.Equal(t, []byte("012"), buf)

	n, err = fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, int64(6), fr.Position())
	require.Equal(t, []byte("345"), buf)
}

// Expectation: zipFileReader should handle empty files.
func Test_zipFileReader_Read_EmptyFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "empty.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	require.Equal(t, int64(0), fr.Position())

	data, err := io.ReadAll(fr)
	require.NoError(t, err)
	require.Empty(t, data)
	require.Equal(t, int64(0), fr.Position())
}

// Expectation: zipFileReader.Read should handle EOF correctly.
func Test_zipFileReader_Read_EOF_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("short")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	data, err := io.ReadAll(fr)
	require.NoError(t, err)
	require.Equal(t, content, data)
	require.Equal(t, int64(len(content)), fr.Position())

	buf := make([]byte, 10)
	n, err := fr.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
}

// Expectation: zipFileReader.ForwardTo should return early when already at target offset.
func Test_zipFileReader_ForwardTo_AlreadyAtOffset_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("test content")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), pos)
	require.Equal(t, int64(0), fr.Position())
}

// Expectation: zipFileReader.ForwardTo should seek forward by discarding bytes.
func Test_zipFileReader_ForwardTo_Discard_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("0123456789")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(5)
	require.NoError(t, err)
	require.Equal(t, int64(5), pos)
	require.Equal(t, int64(5), fr.Position())

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []byte("5"), buf)
}

// Expectation: zipFileReader.ForwardTo should work with multiple forward discards.
func Test_zipFileReader_ForwardTo_Discard_Multiple_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("0123456789ABCDEFGHIJ")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(5)
	require.NoError(t, err)
	require.Equal(t, int64(5), pos)

	pos, err = fr.ForwardTo(10)
	require.NoError(t, err)
	require.Equal(t, int64(10), pos)

	pos, err = fr.ForwardTo(15)
	require.NoError(t, err)
	require.Equal(t, int64(15), pos)

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []byte("F"), buf)
}

// Expectation: zipFileReader.ForwardTo should handle discarding to end of file.
func Test_zipFileReader_ForwardTo_Discard_EndOfFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("test")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(int64(len(content)))
	require.NoError(t, err)
	require.Equal(t, int64(len(content)), pos)

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
}

// Expectation: zipFileReader.ForwardTo should handle discarding beyond EOF gracefully.
func Test_zipFileReader_ForwardTo_Discard_BeyondEOF_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("short")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(1000)
	require.NoError(t, err)

	require.Equal(t, int64(len(content)), pos)
	require.Equal(t, int64(len(content)), fr.Position())
}

// Expectation: zipFileReader.ForwardTo should error when seeking backward on non-seekable reader.
func Test_zipFileReader_ForwardTo_Discard_NonSeekableRewind_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

	tnow := time.Now()
	content := []byte("0123456789")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(io.ReadCloser)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	_, err = fr.ForwardTo(5)
	require.NoError(t, err)

	pos, err := fr.ForwardTo(2)
	require.Error(t, err)
	require.ErrorIs(t, err, errNonSeekableRewind)
	require.Equal(t, int64(5), pos)
}

// Expectation: zipFileReader.ForwardTo should seek forward by actual seeking.
func Test_zipFileReader_ForwardTo_Seek_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("0123456789")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(5)
	require.NoError(t, err)
	require.Equal(t, int64(5), pos)
	require.Equal(t, int64(5), fr.Position())

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []byte("5"), buf)
}

// Expectation: zipFileReader.ForwardTo should work with multiple forward seeks.
func Test_zipFileReader_ForwardTo_Seek_Multiple_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("0123456789ABCDEFGHIJ")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(5)
	require.NoError(t, err)
	require.Equal(t, int64(5), pos)

	pos, err = fr.ForwardTo(10)
	require.NoError(t, err)
	require.Equal(t, int64(10), pos)

	pos, err = fr.ForwardTo(15)
	require.NoError(t, err)
	require.Equal(t, int64(15), pos)

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []byte("F"), buf)
}

// Expectation: zipFileReader.ForwardTo should handle seeking to end of file.
func Test_zipFileReader_ForwardTo_Seek_EndOfFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("test")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(int64(len(content)))
	require.NoError(t, err)
	require.Equal(t, int64(len(content)), pos)

	buf := make([]byte, 1)
	n, err := fr.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
}

// Expectation: zipFileReader.ForwardTo should handle seeking beyond EOF gracefully.
func Test_zipFileReader_ForwardTo_Seek_BeyondEOF_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	tnow := time.Now()
	content := []byte("short")

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	defer fr.Close()

	pos, err := fr.ForwardTo(1000)
	require.NoError(t, err)

	require.Equal(t, int64(1000), pos)
	require.Equal(t, int64(1000), fr.pos)
}

// Expectation: zipFileReader.Close should close underlying ReadCloser.
func Test_zipFileReader_Close_Success(t *testing.T) {
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

	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()

	fr, err := newZipFileReader(fsys, zr.File[0])
	_, ok := fr.Reader().(*io.SectionReader)
	require.True(t, ok)
	require.NoError(t, err)
	require.NotNil(t, fr)

	err = fr.Close()
	require.NoError(t, err)
}

// Expectation: zipFileReader.Close should handle non-ReadCloser gracefully.
func Test_zipFileReader_Close_NonCloser_Success(t *testing.T) {
	t.Parallel()

	fr := &zipFileReader{
		r:   &io.SectionReader{},
		pos: 0,
	}

	err := fr.Close()
	require.NoError(t, err)
}

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
