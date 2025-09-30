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

// Expectation: The returned [fuse.Dirent] slice should meet the expectations.
func Test_zipDirNode_ReadDirAll_Success(t *testing.T) {
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
		Modified: tnow,
	}

	ent, err := node.ReadDirAll(t.Context())
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

// Expectation: EINVAL should be returned upon accessing an invalid ZIP file.
func Test_zipDirNode_ReadDirAll_InvalidArchive_Error(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	node := &zipDirNode{
		Inode:    fs.GenerateDynamicInode(1, "test"),
		Path:     tmpDir + "_notexist.zip", // missing
		Modified: tnow,
	}

	ent, err := node.ReadDirAll(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: The returned lookup nodes should meet the expectations.
func Test_zipDirNode_Lookup_Success(t *testing.T) {
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
	lk, err := node.Lookup(t.Context(), name)
	require.NoError(t, err)
	mn, ok := lk.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), mn.Inode)
	require.Equal(t, "dir/a.txt", mn.Path)
	require.WithinDuration(t, tnow, mn.Modified, time.Second)

	name, ok = flatEntryName("dir/b.txt")
	require.True(t, ok)
	lk, err = node.Lookup(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), dn.Inode)
	require.Equal(t, "dir/b.txt", dn.Path)
	require.WithinDuration(t, tnow, dn.Modified, time.Second)
}

// Expectation: A lookup on an invalid backing archive should return EINVAL.
func Test_zipDirNode_Lookup_InvalidArchive_Error(t *testing.T) {
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
	lk, err := node.Lookup(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: A lookup on a non-existing entry should return ENOENT.
func Test_zipDirNode_Lookup_EntryNotExist_Error(t *testing.T) {
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
	lk, err := node.Lookup(t.Context(), name)
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Inodes should remain deterministic and equal across calls.
func Test_zipDirNode_DeterministicInodes_Success(t *testing.T) {
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

	ent, err := node.ReadDirAll(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 2)

	name, ok := flatEntryName("dir/a.txt")
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, name), ent[0].Inode)
	require.Equal(t, name, ent[0].Name)
	require.Equal(t, fuse.DT_File, ent[0].Type)
	lk, err := node.Lookup(t.Context(), name)
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
	lk, err = node.Lookup(t.Context(), name)
	require.NoError(t, err)
	dn, ok := lk.(*zipDiskStreamFileNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, dn.Inode)
	attr = fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, dn.Inode, attr.Inode)
}
