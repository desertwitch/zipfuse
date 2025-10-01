package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/klauspost/compress/zip"
	"github.com/stretchr/testify/require"
)

// createTestZip creates a zip file for testing with the given paths and modification times.
// Each path can be a file (no trailing slash) or directory (with trailing slash).
// Returns the path to the created zip file.
func createTestZip(t *testing.T, tmpDir string, tmpName string, entries []struct { //nolint:unparam
	Path    string
	ModTime time.Time
	Content []byte // optional, only for files (can be nil)
},
) string {
	t.Helper()

	tmpFile, err := os.Create(filepath.Join(tmpDir, tmpName))
	require.NoError(t, err)
	defer tmpFile.Close()

	zw := zip.NewWriter(tmpFile)
	defer zw.Close()

	for _, entry := range entries {
		header := &zip.FileHeader{
			Name:     entry.Path,
			Method:   zip.Store,
			Modified: entry.ModTime,
		}

		if strings.HasSuffix(entry.Path, "/") {
			header.SetMode(os.ModeDir | 0o755)
		} else {
			header.SetMode(0o644)
		}

		w, err := zw.CreateHeader(header)
		require.NoError(t, err)

		if len(entry.Content) > 0 && !strings.HasSuffix(entry.Path, "/") {
			_, err = w.Write(entry.Content)
			require.NoError(t, err)
		}
	}

	err = zw.Close()
	require.NoError(t, err)

	err = tmpFile.Close()
	require.NoError(t, err)

	return tmpFile.Name()
}

// Expectation: Attr should fill in the [fuse.Attr] with the correct values.
func Test_zipDirNode_Attr_Success(t *testing.T) {
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     "",
		Modified: tnow,
	}

	attr := fuse.Attr{}
	err := node.Attr(t.Context(), &attr)
	require.NoError(t, err)

	require.Equal(t, fs.GenerateDynamicInode(1, "test"), attr.Inode)
	require.Equal(t, os.ModeDir|dirBasePerm, attr.Mode)
	require.Equal(t, tnow, attr.Atime)
	require.Equal(t, tnow, attr.Ctime)
	require.Equal(t, tnow, attr.Mtime)
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations (flat mode).
func Test_zipDirNode_readDirAllFlat_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/a.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "dir/b.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "dir/b.txt", ModTime: tnow, Content: []byte("test content")}, // duplicate
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	name, ok := flatEntryName("dir/a.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), ent[0].Inode)
	require.Equal(t, name, ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)

	name, ok = flatEntryName("dir/b.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), ent[1].Inode)
	require.Equal(t, name, ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
}

// Expectation: EINVAL should be returned upon accessing an invalid ZIP file (flat mode).
func Test_zipDirNode_readDirAllFlat_InvalidArchive_Error(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     tmpDir + "_notexist.zip", // missing
		Modified: tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations (nested mode - root).
func Test_zipDirNode_readDirAllNested_Root_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "readme.txt", ModTime: tnow, Content: []byte("root file")},
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/images/logo.png", ModTime: tnow, Content: []byte("image")},
		{Path: "src/main.go", ModTime: tnow, Content: []byte("code")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 3)

	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	require.Equal(t, "src", ent[1].Name)
	require.Equal(t, fuse.DT_Dir, ent[1].Type)

	require.Equal(t, "readme.txt", ent[2].Name)
	require.Equal(t, fuse.DT_File, ent[2].Type)
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations (nested mode - subdir).
func Test_zipDirNode_readDirAllNested_Subdir_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "ignore.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/images/", ModTime: tnow, Content: nil},
		{Path: "docs/images/logo.png", ModTime: tnow, Content: []byte("image")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "docs/",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 3)

	require.Equal(t, "images", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	require.Equal(t, "a.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)

	require.Equal(t, "b.txt", ent[2].Name)
	require.Equal(t, fuse.DT_File, ent[2].Type)
}

// Expectation: Empty names should be skipped in nested mode.
func Test_zipDirNode_readDirAllNested_EmptyName_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/", ModTime: tnow, Content: nil}, // explicit
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "docs/",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)

	// Should not have an empty-named entry for docs/ itself
	for _, e := range ent {
		require.NotEmpty(t, e.Name)
	}
}

// Expectation: Implicit directories should be detected in nested mode.
func Test_zipDirNode_readDirAllNested_ImplicitDirectory_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 1)

	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)
}

// Expectation: Mixed explicit and implicit should work in nested mode.
func Test_zipDirNode_readDirAllNested_MixedDirectories_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "explicit/", ModTime: tnow, Content: nil},
		{Path: "explicit/file.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "implicit/file.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "explicit", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	require.Equal(t, "implicit", ent[1].Name)
	require.Equal(t, fuse.DT_Dir, ent[1].Type)
}

func Test_zipDirNode_readDirAllNested_PrefixFiltering_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "readme.txt", ModTime: tnow, Content: []byte("root")},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "src/main.go", ModTime: tnow, Content: []byte("code")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "docs/",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "a.txt", ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)

	require.Equal(t, "b.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
}

// Expectation: Leading slashes in ZIP entries should be handled in nested mode.
func Test_zipDirNode_readDirAllNested_LeadingSlash_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "/file.txt", ModTime: tnow, Content: []byte("test")}, // malformed
		{Path: "normal.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "file.txt", ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)

	require.Equal(t, "normal.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
}

// Expectation: Duplicate prefixed entries in ReadDirAll should be deduplicated in nested mode.
func Test_zipDirNode_readDirAllNested_Deduplication_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "docs/c.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)

	// Should only have one "docs" entry, not three
	require.Len(t, ent, 1)
	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)
}

// Expectation: Files and directories with similar names should not conflict in nested mode.
func Test_zipDirNode_readDirAllNested_SimilarNames_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test", ModTime: tnow, Content: []byte("file")},
		{Path: "test-dir/a.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "testing/b.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 3)

	require.Equal(t, "test-dir", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	require.Equal(t, "testing", ent[1].Name)
	require.Equal(t, fuse.DT_Dir, ent[1].Type)

	require.Equal(t, "test", ent[2].Name)
	require.Equal(t, fuse.DT_File, ent[2].Type)
}

// Expectation: EINVAL should be returned upon accessing an invalid ZIP file (nested mode).
func Test_zipDirNode_readDirAllNested_InvalidArchive_Error(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     tmpDir + "_notexist.zip", // missing
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned lookup nodes should meet the expectations (flat mode).
func Test_zipDirNode_lookupFlat_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/a.txt", ModTime: tnow, Content: []byte{}},
		{Path: "dir/b.txt", ModTime: tnow, Content: []byte("test content")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Modified: tnow,
	}

	name, ok := flatEntryName("dir/a.txt")
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), mn.Inode)
	require.Equal(t, "dir/a.txt", mn.Path)
	require.WithinDuration(t, tnow, mn.Modified, time.Second)

	name, ok = flatEntryName("dir/b.txt")
	require.True(t, ok)
	lk, err = node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), dn.Inode)
	require.Equal(t, "dir/b.txt", dn.Path)
	require.WithinDuration(t, tnow, dn.Modified, time.Second)
}

// Expectation: A lookup on a non-existing entry should return ENOENT (flat mode).
func Test_zipDirNode_lookupFlat_EntryNotExist_Error(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/a.txt", ModTime: tnow, Content: []byte{}},
		{Path: "dir/b.txt", ModTime: tnow, Content: []byte("test content")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Modified: tnow,
	}

	name, ok := flatEntryName("dir/c.txt") // missing
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: A lookup on an invalid backing archive should return EINVAL (flat mode).
func Test_zipDirNode_lookupFlat_InvalidArchive_Error(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     tmpDir + "_noexist.zip", // missing
		Modified: tnow,
	}

	name, ok := flatEntryName("dir/c.txt")
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned lookup nodes should meet the expectations (nested mode - file).
func Test_zipDirNode_lookupNested_File_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "readme.txt", ModTime: tnow, Content: []byte{}},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test content")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := node.lookupNested(t.Context(), "readme.txt")
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "readme.txt"), mn.Inode)
	require.Equal(t, "readme.txt", mn.Path)
	require.WithinDuration(t, tnow, mn.Modified, time.Second)

	_, err = node.lookupNested(t.Context(), "a.txt")
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: The returned lookup nodes should meet the expectations (nested mode - directory).
func Test_zipDirNode_lookupNested_Directory_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, zipPath, dn.Path)
	require.Equal(t, "docs/", dn.Prefix)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "docs"), dn.Inode)
	require.Equal(t, tnow, dn.Modified) // Should use archive's timestamp

	_, err = node.lookupNested(t.Context(), "a.txt")
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Directory timestamp should use archive time, not file time in nested mode.
func Test_zipDirNode_lookupNested_Timestamps_Success(t *testing.T) {
	tmpDir := t.TempDir()
	archiveTime := time.Now()

	fileTime := archiveTime.Add(-24 * time.Hour)
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/file.txt", ModTime: fileTime, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: archiveTime,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)

	// Directory should use archive time, not file time
	require.Equal(t, archiveTime, dn.Modified)
}

// Expectation: Directory and file lookup should work at multiple nesting levels.
func Test_zipDirNode_lookupNested_DeepStructure_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/images/icons/favicon.ico", ModTime: tnow, Content: []byte{}},
		{Path: "docs/images/icons/favicon-large.ico", ModTime: tnow, Content: []byte("longer content")},
	})

	rootNode := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := rootNode.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	docsNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/", docsNode.Prefix)
	require.Equal(t, fs.GenerateDynamicInode(rootNode.Inode, "docs"), docsNode.Inode)

	lk, err = docsNode.lookupNested(t.Context(), "images")
	require.NoError(t, err)
	imagesNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/", imagesNode.Prefix)
	require.Equal(t, fs.GenerateDynamicInode(docsNode.Inode, "images"), imagesNode.Inode)

	lk, err = imagesNode.lookupNested(t.Context(), "icons")
	require.NoError(t, err)
	iconsNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/", iconsNode.Prefix)
	require.Equal(t, fs.GenerateDynamicInode(imagesNode.Inode, "icons"), iconsNode.Inode)

	lk, err = iconsNode.lookupNested(t.Context(), "favicon.ico")
	require.NoError(t, err)
	faviconNode, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/favicon.ico", faviconNode.Path)
	require.Equal(t, fs.GenerateDynamicInode(iconsNode.Inode, "favicon.ico"), faviconNode.Inode)

	lk, err = iconsNode.lookupNested(t.Context(), "favicon-large.ico")
	require.NoError(t, err)
	faviconLargeNode, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/favicon-large.ico", faviconLargeNode.Path)
	require.Equal(t, fs.GenerateDynamicInode(iconsNode.Inode, "favicon-large.ico"), faviconLargeNode.Inode)
}

// Expectation: Looking up implicit directories should work in nested mode.
func Test_zipDirNode_lookupNested_ImplicitDirectory_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/", dn.Prefix)
}

// Expectation: Prefix matching should be exact (not substring) in nested mode.
func Test_zipDirNode_lookupNested_ExactPrefixMatch_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "doc", ModTime: tnow, Content: []byte{}},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
		{Path: "documentation/b.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	_, err := node.lookupNested(t.Context(), "do")
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))

	lk, err := node.lookupNested(t.Context(), "doc")
	require.NoError(t, err)
	_, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)

	lk, err = node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/", dn.Prefix)

	lk, err = node.lookupNested(t.Context(), "documentation")
	require.NoError(t, err)
	dn, ok = lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "documentation/", dn.Prefix)
}

// Expectation: A lookup on a non-existing entry should return ENOENT (nested mode).
func Test_zipDirNode_lookupNested_EntryNotExist_Error(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := node.lookupNested(t.Context(), "a.txt")
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: A lookup on an invalid backing archive should return EINVAL (nested mode).
func Test_zipDirNode_lookupNested_InvalidArchive_Error(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     tmpDir + "_noexist.zip", // missing
		Prefix:   "",
		Modified: tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Inodes should remain deterministic and equal across calls (flat mode).
func Test_zipDirNode_DeterministicInodes_Flat_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/a.txt", ModTime: tnow, Content: []byte{}},
		{Path: "dir/b.txt", ModTime: tnow, Content: []byte("test content")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Modified: tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	name, ok := flatEntryName("dir/a.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), ent[0].Inode)
	require.Equal(t, name, ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)
	lk, err := node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, ent[0].Inode, mn.Inode)
	attr := fuse.Attr{}
	err = mn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, mn.Inode, attr.Inode)

	name, ok = flatEntryName("dir/b.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), ent[1].Inode)
	require.Equal(t, name, ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
	lk, err = node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, dn.Inode)
	attr = fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, dn.Inode, attr.Inode)
}

// Expectation: Inodes should remain deterministic and equal across calls (nested mode).
func Test_zipDirNode_DeterministicInodes_Nested_Success(t *testing.T) {
	StreamingThreshold.Store(1)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "readme.txt", ModTime: tnow, Content: []byte{}},
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     zipPath,
		Prefix:   "",
		Modified: tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "docs"), ent[0].Inode)

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, ent[0].Inode, dn.Inode)
	attr := fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, dn.Inode, attr.Inode)

	require.Equal(t, "readme.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "readme.txt"), ent[1].Inode)

	lk, err = node.lookupNested(t.Context(), "readme.txt")
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, mn.Inode)
	attr = fuse.Attr{}
	err = mn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, mn.Inode, attr.Inode)
}
