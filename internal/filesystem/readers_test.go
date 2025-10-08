package filesystem

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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
