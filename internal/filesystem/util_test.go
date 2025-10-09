package filesystem

import (
	"encoding/binary"
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

			f := createTestZipFilePtr(t, tt.fileName)
			if tt.isDirAttr {
				f.SetMode(0o755 | os.ModeDir)
			}

			got := isDir(f, normalizeZipPath(0, f, true))
			require.Equal(t, tt.want, got)
		})
	}
}

// Expectation: flatEntryName should flatten paths correctly and append index.
func Test_flatEntryName_Success(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		index    int
		input    string
		expected string
		valid    bool
	}{
		{0, "dir/file.txt", "file(0).txt", true},
		{5, "a/b/c/test.log", "test(5).log", true},
		{10, "file.txt", "file(10).txt", true},
		{99, "deep/nested/path/document.pdf", "document(99).pdf", true},
	}

	for _, tc := range testCases {
		result, valid := flatEntryName(tc.index, tc.input)
		require.Equal(t, tc.valid, valid)
		if valid {
			require.Equal(t, tc.expected, result)
		}
	}
}

// Expectation: flatEntryName should preserve file extensions.
func Test_flatEntryName_PreserveExtension_Success(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		index int
		input string
		ext   string
	}{
		{1, "dir/file.txt", ".txt"},
		{2, "path/to/document.pdf", ".pdf"},
		{3, "nested/script.sh", ".sh"},
		{4, "archive.tar.gz", ".gz"},
	}

	for _, tc := range testCases {
		result, valid := flatEntryName(tc.index, tc.input)
		require.True(t, valid)
		require.Equal(t, filepath.Ext(result), tc.ext)
	}
}

// Expectation: flatEntryName should generate different names for different indices.
func Test_flatEntryName_UniqueIndices_Success(t *testing.T) {
	t.Parallel()

	path := "dir/file.txt"
	name1, valid1 := flatEntryName(1, path)
	require.True(t, valid1)

	name2, valid2 := flatEntryName(2, path)
	require.True(t, valid2)

	require.NotEqual(t, name1, name2)
	require.Equal(t, "file(1).txt", name1)
	require.Equal(t, "file(2).txt", name2)
}

// Expectation: flatEntryName should generate consistent names for the same input.
func Test_flatEntryName_Deterministic_Success(t *testing.T) {
	t.Parallel()

	path := "some/deep/path/file.txt"
	index := 42

	name1, valid1 := flatEntryName(index, path)
	require.True(t, valid1)

	name2, valid2 := flatEntryName(index, path)
	require.True(t, valid2)

	require.Equal(t, name1, name2)
}

// Expectation: flatEntryName should handle files without extensions.
func Test_flatEntryName_NoExtension_Success(t *testing.T) {
	t.Parallel()

	name, valid := flatEntryName(5, "dir/README")
	require.True(t, valid)
	require.Equal(t, "README(5)", name)
	require.Empty(t, filepath.Ext(name))
}

// Expectation: flatEntryName should handle dotfiles.
func Test_flatEntryName_Dotfile_Success(t *testing.T) {
	t.Parallel()

	name, valid := flatEntryName(3, "dir/.gitignore")
	require.True(t, valid)
	require.Equal(t, "(3).gitignore", name)
}

// Expectation: flatEntryName should handle paths with multiple dots.
func Test_flatEntryName_MultipleDots_Success(t *testing.T) {
	t.Parallel()

	name, valid := flatEntryName(7, "dir/archive.tar.gz")
	require.True(t, valid)
	require.Equal(t, "archive.tar(7).gz", name)
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
		_, valid := flatEntryName(0, tc)
		require.False(t, valid)
	}
}

// Expectation: normalizeZipPath should handle valid UTF-8 paths correctly.
func Test_normalizeZipPath_ValidUTF8_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"foo//bar/baz.txt", "foo/bar/baz.txt"},
		{"/leading/slash.txt", "leading/slash.txt"},
		{"normal/path.txt", "normal/path.txt"},
		{"////file.txt", "file.txt"},
		{"привет.txt", "привет.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			f := createTestZipFilePtr(t, tt.in)
			got := normalizeZipPath(0, f, true)
			require.Equal(t, tt.want, got)
		})
	}
}

// Expectation: normalizeZipPath should normalize path separators.
func Test_normalizeZipPath_Separators_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE, 0xFD}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"double slashes", "dir//file.txt", "dir/file.txt"},
		{"triple slashes", "dir///file.txt", "dir/file.txt"},
		{"leading slash", "/dir/file.txt", "dir/file.txt"},
		{"multiple leading slashes", "///dir/file.txt", "dir/file.txt"},
		{"non-unicode", "valid//dir///" + string(corruptBytes) + ".txt", "valid/dir/noutf8_file(0).txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := createTestZipFilePtr(t, tt.in)
			got := normalizeZipPath(0, f, true)
			require.Equal(t, tt.want, got)
		})
	}
}

// Expectation: normalizeZipPath should use Unicode Extra Field when present and UTF-8 is invalid.
func Test_normalizeZipPath_UnicodeExtraField_Success(t *testing.T) {
	t.Parallel()

	invalidUTF8 := []byte{0xFF, 0xFE, 0xFD}

	// Unicode Extra Field (0x7075)
	unicodePath := "正しいファイル名.txt"
	extra := make([]byte, 0)
	extra = append(extra, 0x75, 0x70)

	// Data size
	dataSize := uint16(1 + 4 + len(unicodePath)) // version + crc32 + name
	extra = binary.LittleEndian.AppendUint16(extra, dataSize)

	// Version
	extra = append(extra, 0x01)

	// CRC32 (dummy for test)
	extra = binary.LittleEndian.AppendUint32(extra, 0)

	// UTF-8 name
	extra = append(extra, []byte(unicodePath)...)

	f := &zip.File{
		FileHeader: zip.FileHeader{
			Name:  string(invalidUTF8) + ".txt",
			Extra: extra,
		},
	}

	got := normalizeZipPath(0, f, true)
	require.Equal(t, unicodePath, got)
}

// Expectation: normalizeZipPath should fall back when UTF-8 invalid and no Unicode Extra Field.
func Test_normalizeZipPath_Fallback_Success(t *testing.T) {
	t.Parallel()

	invalidUTF8 := []byte{0xFF, 0xFE, 0xFD}
	f := createTestZipFilePtr(t, "dir/"+string(invalidUTF8)+".txt")

	got := normalizeZipPath(42, f, true)
	require.Equal(t, "dir/noutf8_file(42).txt", got)
}

// Expectation: normalizeZipPath should not fall back when UTF-8 invalid and no Unicode Extra Field.
func Test_normalizeZipPath_NoFallback_Success(t *testing.T) {
	t.Parallel()

	invalidUTF8 := []byte{0xFF, 0xFE, 0xFD}
	f := createTestZipFilePtr(t, "dir/"+string(invalidUTF8)+".txt")

	got := normalizeZipPath(42, f, false)
	require.Equal(t, "dir/\xff\xfe\xfd.txt", got)
}

// Expectation: zipUnicodePathFromExtra should extract Unicode path from Extra Field.
func Test_zipUnicodePathFromExtra_Success(t *testing.T) {
	t.Parallel()

	unicodePath := "日本語ファイル.txt"
	extra := make([]byte, 0)
	extra = append(extra, 0x75, 0x70) // Header ID
	dataSize := uint16(1 + 4 + len(unicodePath))
	extra = binary.LittleEndian.AppendUint16(extra, dataSize)
	extra = append(extra, 0x01)                        // Version
	extra = binary.LittleEndian.AppendUint32(extra, 0) // CRC32
	extra = append(extra, []byte(unicodePath)...)

	f := &zip.File{
		FileHeader: zip.FileHeader{
			Extra: extra,
		},
	}

	path, ok := zipUnicodePathFromExtra(f)
	require.True(t, ok)
	require.Equal(t, unicodePath, path)
}

// Expectation: zipUnicodePathFromExtra should return false when no Unicode Extra Field present.
func Test_zipUnicodePathFromExtra_NotFound_Error(t *testing.T) {
	t.Parallel()

	f := &zip.File{
		FileHeader: zip.FileHeader{
			Extra: []byte{},
		},
	}

	_, ok := zipUnicodePathFromExtra(f)
	require.False(t, ok)
}

// Expectation: zipUnicodePathFromExtra should handle malformed Extra Field.
func Test_zipUnicodePathFromExtra_Malformed_Error(t *testing.T) {
	t.Parallel()

	// Malformed: size extends beyond actual data
	extra := []byte{0x75, 0x70, 0xFF, 0xFF}

	f := &zip.File{
		FileHeader: zip.FileHeader{
			Extra: extra,
		},
	}

	_, ok := zipUnicodePathFromExtra(f)
	require.False(t, ok)
}

// Expectation: zipPathUnicodeFallback should preserve valid UTF-8 components.
func Test_zipPathUnicodeFallback_ValidUTF8Components_Success(t *testing.T) {
	t.Parallel()

	path := "valid/utf8/path.txt"
	result := zipPathUnicodeFallback(5, path)
	require.Equal(t, "valid/utf8/path.txt", result)
}

// Expectation: zipPathUnicodeFallback should generate fallback names for corrupt filenames.
func Test_zipPathUnicodeFallback_CorruptFilename_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE, 0xFD}
	path := "valid/dir/" + string(corruptBytes) + ".txt"

	got := zipPathUnicodeFallback(42, path)
	require.Equal(t, "valid/dir/noutf8_file(42).txt", got)
}

// Expectation: zipPathUnicodeFallback should generate fallback names for corrupt directories.
func Test_zipPathUnicodeFallback_CorruptDirectory_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE}
	path := string(corruptBytes) + "/file.txt"

	result := zipPathUnicodeFallback(10, path)
	require.Contains(t, result, "noutf8_dir_")
	require.Contains(t, result, "/file.txt")
}

// Expectation: zipPathUnicodeFallback should generate equal fallback names when called twice.
func Test_zipPathUnicodeFallback_DeterministicDirectory_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE}
	path := string(corruptBytes) + "/"

	result1 := zipPathUnicodeFallback(10, path)
	result2 := zipPathUnicodeFallback(10, path)

	require.Equal(t, result1, result2)
}

// Expectation: zipPathUnicodeFallback should preserve non-corrupt extensions.
func Test_zipPathUnicodeFallback_Extension_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE}
	path := "dir/" + string(corruptBytes) + ".jpg"

	result := zipPathUnicodeFallback(7, path)
	require.Equal(t, "dir/noutf8_file(7).jpg", result)
}

// Expectation: zipPathUnicodeFallback should clear suspicious extensions.
func Test_zipPathUnicodeFallback_SuspiciousExtension_Success(t *testing.T) {
	t.Parallel()

	corruptBytes := []byte{0xFF, 0xFE}
	path := "dir/" + string(corruptBytes) + "." + string(corruptBytes)

	result := zipPathUnicodeFallback(7, path)
	require.Equal(t, "dir/noutf8_file(7)", result)
}

// Expectation: zipPathUnicodeFallback should handle mixed valid and invalid components.
func Test_zipPathUnicodeFallback_MixedComponents_Success(t *testing.T) {
	t.Parallel()

	corruptDir := []byte{0xFF, 0xFE}
	corruptFile := []byte{0xFD, 0xFC}
	path := "valid/" + string(corruptDir) + "/subdir/" + string(corruptFile) + ".log"

	result := zipPathUnicodeFallback(15, path)
	require.Contains(t, result, "valid/")
	require.Contains(t, result, "noutf8_dir_")
	require.Contains(t, result, "/subdir/")
	require.Contains(t, result, "file(15)")
	require.Contains(t, result, ".log")
}
