package filesystem

import (
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
	zr, err := newZipReader("/nonexistent/path.zip", true)
	require.Nil(t, zr)
	require.Error(t, err)
}

// Expectation: newZipReader should return an error for an invalid ZIP file.
func Test_newZipReader_InvalidZip_Error(t *testing.T) {
	tmpDir := t.TempDir()

	invalidPath := filepath.Join(tmpDir, "invalid.zip")
	err := os.WriteFile(invalidPath, []byte("not a zip file"), 0o644)
	require.NoError(t, err)

	zr, err := newZipReader(invalidPath, true)
	require.Nil(t, zr)
	require.Error(t, err)
}

// Expectation: zipReader should track metrics correctly on Close for extract operations.
func Test_zipReader_Close_Extract_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test content for extraction")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	Metrics.OpenZips.Store(0)
	Metrics.TotalOpenedZips.Store(0)
	Metrics.TotalClosedZips.Store(0)
	Metrics.TotalExtractCount.Store(0)
	Metrics.TotalExtractBytes.Store(0)
	Metrics.TotalExtractTime.Store(0)

	zr, err := newZipReader(zipPath, true)
	require.NoError(t, err)
	require.NotNil(t, zr)

	require.Equal(t, int64(1), Metrics.OpenZips.Load())
	require.Equal(t, int64(1), Metrics.TotalOpenedZips.Load())

	var bytesRead int
	for _, f := range zr.File {
		if f.Name == "test.txt" {
			rc, err := f.Open()
			require.NoError(t, err)
			n, err := io.Copy(io.Discard, rc)
			require.NoError(t, err)
			rc.Close()
			bytesRead = int(n)
		}
	}

	require.Equal(t, len(content), bytesRead)

	err = zr.Close(bytesRead)
	require.NoError(t, err)

	require.Equal(t, int64(0), Metrics.OpenZips.Load())
	require.Equal(t, int64(1), Metrics.TotalClosedZips.Load())
	require.Equal(t, int64(1), Metrics.TotalExtractCount.Load())
	require.Equal(t, int64(bytesRead), Metrics.TotalExtractBytes.Load())
	require.Equal(t, int64(len(content)), Metrics.TotalExtractBytes.Load())
	require.Positive(t, Metrics.TotalExtractTime.Load(), int64(0))
}

// Expectation: zipReader should track metrics correctly on Close for metadata operations.
func Test_zipReader_Close_Metadata_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test content for metadata")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
		{Path: "other.txt", ModTime: tnow, Content: []byte("other")},
	})

	Metrics.OpenZips.Store(0)
	Metrics.TotalOpenedZips.Store(0)
	Metrics.TotalClosedZips.Store(0)
	Metrics.TotalMetadataReadCount.Store(0)
	Metrics.TotalMetadataReadTime.Store(0)

	zr, err := newZipReader(zipPath, false)
	require.NoError(t, err)
	require.NotNil(t, zr)

	require.Equal(t, int64(1), Metrics.OpenZips.Load())
	require.Equal(t, int64(1), Metrics.TotalOpenedZips.Load())

	fileCount := len(zr.File)
	require.Equal(t, 2, fileCount)

	err = zr.Close(0)
	require.NoError(t, err)

	require.Equal(t, int64(0), Metrics.OpenZips.Load())
	require.Equal(t, int64(1), Metrics.TotalClosedZips.Load())
	require.Equal(t, int64(1), Metrics.TotalMetadataReadCount.Load())
	require.Positive(t, Metrics.TotalMetadataReadTime.Load(), int64(0))
}

// Expectation: flatEntryName should flatten paths correctly and produce hashes.
func Test_flatEntryName_Success(t *testing.T) {
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
	name1, valid1 := flatEntryName("dir1/file.txt")
	require.True(t, valid1)

	name2, valid2 := flatEntryName("dir2/file.txt")
	require.True(t, valid2)

	// Same filename but different paths should have different hashes
	require.NotEqual(t, name1, name2)
}

// Expectation: flatEntryName should generate consistent hashes for the same path.
func Test_flatEntryName_DeterministicHashes_Success(t *testing.T) {
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
	name, valid := flatEntryName("dir/README")
	require.True(t, valid)
	require.Contains(t, name, "README_")
	require.Empty(t, filepath.Ext(name))
	require.Greater(t, len(name), len("README_"))
}

// Expectation: flatEntryName should handle paths with multiple dots.
func Test_flatEntryName_MultipleDots_Success(t *testing.T) {
	name, valid := flatEntryName("dir/archive.tar.gz")
	require.True(t, valid)
	require.Contains(t, name, "archive.tar_")
	require.Equal(t, ".gz", filepath.Ext(name))
	require.Greater(t, len(name), len("archive.tar_"))
}

// Expectation: flatEntryName should return false for invalid paths.
func Test_flatEntryName_InvalidPaths_Error(t *testing.T) {
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
	err := toFuseErr(os.ErrNotExist)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: toFuseErr should convert os.ErrPermission to EACCES.
func Test_toFuseErr_Permission_Success(t *testing.T) {
	err := toFuseErr(os.ErrPermission)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EACCES))
}

// Expectation: toFuseErr should convert other errors to EIO.
func Test_toFuseErr_Other_Success(t *testing.T) {
	customErr := syscall.EINVAL
	err := toFuseErr(customErr)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EIO))
}

// Expectation: toFuseErr should handle wrapped os.ErrNotExist.
func Test_toFuseErr_WrappedNotExist_Success(t *testing.T) {
	tmpDir := t.TempDir()

	_, osErr := os.Open(filepath.Join(tmpDir, "nonexistent.txt"))
	require.Error(t, osErr)

	err := toFuseErr(osErr)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: The function should behave according to the table expectations.
func Test_isDir_Success(t *testing.T) {
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
			got := normalizeZipPath(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}
