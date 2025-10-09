package filesystem

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/stretchr/testify/require"
)

// Expectation: Attr should fill in the [fuse.Attr] with the correct values.
func Test_zipDirNode_Attr_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipDirNode{
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  "",
		mtime: tnow,
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

// Expectation: Open should set the caching flags and return the node itself as the handle.
func Test_zipDirNode_Open_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("test content in directory")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/file.txt", ModTime: tnow, Content: content},
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test.zip"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	resp := &fuse.OpenResponse{}
	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, resp)
	require.NoError(t, err)

	require.NotZero(t, resp.Flags&fuse.OpenKeepCache, "OpenKeepCache flag should be set")
	require.NotZero(t, resp.Flags&fuse.OpenCacheDir, "OpenCacheDir flag should be set")

	dirHandle, ok := handle.(*zipDirNode)
	require.True(t, ok, "handle should be a *zipDirNode")
	require.Equal(t, node, dirHandle, "handle should be the same as the original node")
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations (flat mode).
func Test_zipDirNode_readDirAllFlat_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 3)

	name, ok := flatEntryName(1, "dir/a.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), ent[0].Inode)
	require.Equal(t, name, ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)

	name, ok = flatEntryName(2, "dir/b.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), ent[1].Inode)
	require.Equal(t, name, ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)

	name, ok = flatEntryName(3, "dir/b.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), ent[2].Inode)
	require.Equal(t, name, ent[2].Name)
	require.Equal(t, fuse.DT_File, ent[2].Type)
}

// Expectation: Leading slashes in ZIP entries should be handled in flat mode.
func Test_zipDirNode_readDirAllFlat_LeadingSlash_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "/file.txt", ModTime: tnow, Content: []byte("test")},    // malformed
		{Path: "//normal.txt", ModTime: tnow, Content: []byte("test")}, // malformed
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	name, ok := flatEntryName(0, normalizeZipPath(0, createZipFilePtr(t, "/file.txt"), fsys.Options.ForceUnicode))
	require.True(t, ok)
	require.Equal(t, name, ent[0].Name)
	require.NotContains(t, name, "/")
	require.Equal(t, fuse.DT_File, ent[0].Type)

	name, ok = flatEntryName(1, normalizeZipPath(1, createZipFilePtr(t, "//normal.txt"), fsys.Options.ForceUnicode))
	require.True(t, ok)
	require.Equal(t, name, ent[1].Name)
	require.NotContains(t, name, "/")
	require.Equal(t, fuse.DT_File, ent[1].Type)
}

// Expectation: EINVAL should be returned upon accessing an invalid ZIP file (flat mode).
func Test_zipDirNode_readDirAllFlat_InvalidArchive_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipDirNode{
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  tmpDir + "_notexist.zip", // missing
		mtime: tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations (nested mode - root).
func Test_zipDirNode_readDirAllNested_Root_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "docs/",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "docs/",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 1)

	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)
}

// Expectation: Mixed explicit and implicit should work in nested mode.
func Test_zipDirNode_readDirAllNested_MixedDirectories_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "docs/",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "file.txt", ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)

	require.Equal(t, "normal.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
}

// Expectation: Double slashes in ZIP entries should be handled in nested mode.
func Test_zipDirNode_readDirAllNested_DoubleSlash_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "/file.txt", ModTime: tnow, Content: []byte("test")},       // malformed
		{Path: "dir//normal.txt", ModTime: tnow, Content: []byte("test")}, // malformed
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "dir/",
		mtime:  tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 1)

	require.Equal(t, "normal.txt", ent[0].Name)
	require.NotContains(t, ent[0].Name, "/")
	require.Equal(t, fuse.DT_File, ent[0].Type)
}

// Expectation: Duplicate prefixed entries in ReadDirAll should be deduplicated in nested mode.
func Test_zipDirNode_readDirAllNested_Deduplication_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   tmpDir + "_notexist.zip", // missing
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned lookup nodes should meet the expectations (flat mode).
func Test_zipDirNode_lookupFlat_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  zipPath,
		mtime: tnow,
	}

	name, ok := flatEntryName(1, "dir/a.txt")
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), mn.inode)
	require.Equal(t, "dir/a.txt", mn.path)
	require.WithinDuration(t, tnow, mn.mtime, time.Second)

	name, ok = flatEntryName(2, "dir/b.txt")
	require.True(t, ok)
	lk, err = node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), dn.inode)
	require.Equal(t, "dir/b.txt", dn.path)
	require.WithinDuration(t, tnow, dn.mtime, time.Second)
}

// Expectation: A lookup on a non-existing entry should return ENOENT (flat mode).
func Test_zipDirNode_lookupFlat_EntryNotExist_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  zipPath,
		mtime: tnow,
	}

	name, ok := flatEntryName(0, "dir/c.txt") // missing
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: A lookup on an invalid backing archive should return EINVAL (flat mode).
func Test_zipDirNode_lookupFlat_InvalidArchive_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

	tnow := time.Now()

	node := &zipDirNode{
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  tmpDir + "_noexist.zip", // missing
		mtime: tnow,
	}

	name, ok := flatEntryName(0, "dir/c.txt")
	require.True(t, ok)
	lk, err := node.lookupFlat(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned lookup nodes should meet the expectations (nested mode - file).
func Test_zipDirNode_lookupNested_File_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	lk, err := node.lookupNested(t.Context(), "readme.txt")
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, "readme.txt"), mn.inode)
	require.Equal(t, "readme.txt", mn.path)
	require.WithinDuration(t, tnow, mn.mtime, time.Second)

	_, err = node.lookupNested(t.Context(), "a.txt")
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: The returned lookup nodes should meet the expectations (nested mode - directory).
func Test_zipDirNode_lookupNested_Directory_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, zipPath, dn.path)
	require.Equal(t, "docs/", dn.prefix)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, "docs"), dn.inode)
	require.Equal(t, tnow, dn.mtime) // Should use archive's timestamp

	_, err = node.lookupNested(t.Context(), "a.txt")
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Directory timestamp should use archive time, not file time in nested mode.
func Test_zipDirNode_lookupNested_Timestamps_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  archiveTime,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)

	// Directory should use archive time, not file time
	require.Equal(t, archiveTime, dn.mtime)
}

// Expectation: Directory and file lookup should work at multiple nesting levels.
func Test_zipDirNode_lookupNested_DeepStructure_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	lk, err := rootNode.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	docsNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/", docsNode.prefix)
	require.Equal(t, fs.GenerateDynamicInode(rootNode.inode, "docs"), docsNode.inode)

	lk, err = docsNode.lookupNested(t.Context(), "images")
	require.NoError(t, err)
	imagesNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/", imagesNode.prefix)
	require.Equal(t, fs.GenerateDynamicInode(docsNode.inode, "images"), imagesNode.inode)

	lk, err = imagesNode.lookupNested(t.Context(), "icons")
	require.NoError(t, err)
	iconsNode, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/", iconsNode.prefix)
	require.Equal(t, fs.GenerateDynamicInode(imagesNode.inode, "icons"), iconsNode.inode)

	lk, err = iconsNode.lookupNested(t.Context(), "favicon.ico")
	require.NoError(t, err)
	faviconNode, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/favicon.ico", faviconNode.path)
	require.Equal(t, fs.GenerateDynamicInode(iconsNode.inode, "favicon.ico"), faviconNode.inode)

	lk, err = iconsNode.lookupNested(t.Context(), "favicon-large.ico")
	require.NoError(t, err)
	faviconLargeNode, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, "docs/images/icons/favicon-large.ico", faviconLargeNode.path)
	require.Equal(t, fs.GenerateDynamicInode(iconsNode.inode, "favicon-large.ico"), faviconLargeNode.inode)
}

// Expectation: Looking up implicit directories should work in nested mode.
func Test_zipDirNode_lookupNested_ImplicitDirectory_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "docs/", dn.prefix)
}

// Expectation: Prefix matching should be exact (not substring) in nested mode.
func Test_zipDirNode_lookupNested_ExactPrefixMatch_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
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
	require.Equal(t, "docs/", dn.prefix)

	lk, err = node.lookupNested(t.Context(), "documentation")
	require.NoError(t, err)
	dn, ok = lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, "documentation/", dn.prefix)
}

// Expectation: A lookup on a non-existing entry should return ENOENT (nested mode).
func Test_zipDirNode_lookupNested_EntryNotExist_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	lk, err := node.lookupNested(t.Context(), "a.txt")
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: A lookup on an invalid backing archive should return EINVAL (nested mode).
func Test_zipDirNode_lookupNested_InvalidArchive_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   tmpDir + "_noexist.zip", // missing
		prefix: "",
		mtime:  tnow,
	}

	lk, err := node.lookupNested(t.Context(), "docs")
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Inodes should remain deterministic and equal across calls (flat mode).
func Test_zipDirNode_DeterministicInodes_Flat_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

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
		fsys:  fsys,
		inode: fs.GenerateDynamicInode(1, "test"),
		path:  zipPath,
		mtime: tnow,
	}

	ent, err := node.readDirAllFlat(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	name, ok := flatEntryName(1, "dir/a.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), ent[0].Inode)
	require.Equal(t, name, ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)
	lk, err := node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, ent[0].Inode, mn.inode)
	attr := fuse.Attr{}
	err = mn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, mn.inode, attr.Inode)

	name, ok = flatEntryName(2, "dir/b.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, name), ent[1].Inode)
	require.Equal(t, name, ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
	lk, err = node.lookupFlat(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, dn.inode)
	attr = fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, dn.inode, attr.Inode)
}

// Expectation: Inodes should remain deterministic and equal across calls (nested mode).
func Test_zipDirNode_DeterministicInodes_Nested_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.StreamingThreshold.Store(1)

	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "readme.txt", ModTime: tnow, Content: []byte{}},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test")},
	})

	node := &zipDirNode{
		fsys:   fsys,
		inode:  fs.GenerateDynamicInode(1, "test"),
		path:   zipPath,
		prefix: "",
		mtime:  tnow,
	}

	ent, err := node.readDirAllNested(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	require.Equal(t, "docs", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, "docs"), ent[0].Inode)

	lk, err := node.lookupNested(t.Context(), "docs")
	require.NoError(t, err)
	dn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, ent[0].Inode, dn.inode)
	attr := fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, dn.inode, attr.Inode)

	require.Equal(t, "readme.txt", ent[1].Name)
	require.Equal(t, fuse.DT_File, ent[1].Type)
	require.Equal(t, fs.GenerateDynamicInode(node.inode, "readme.txt"), ent[1].Inode)

	lk, err = node.lookupNested(t.Context(), "readme.txt")
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, mn.inode)
	attr = fuse.Attr{}
	err = mn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, mn.inode, attr.Inode)
}
