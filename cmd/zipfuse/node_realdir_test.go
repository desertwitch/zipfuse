package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/stretchr/testify/require"
)

// Expectation: Attr should fill in the [fuse.Attr] with the correct values.
func Test_realDirNode_Attr_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tnow := time.Now()

	node := &realDirNode{
		Inode:    1,
		Path:     "",
		Modified: tnow,
	}

	attr := fuse.Attr{}
	err := node.Attr(t.Context(), &attr)
	require.NoError(t, err)

	require.Equal(t, uint64(1), attr.Inode)
	require.Equal(t, os.ModeDir|dirBasePerm, attr.Mode)
	require.Equal(t, tnow, attr.Atime)
	require.Equal(t, tnow, attr.Ctime)
	require.Equal(t, tnow, attr.Mtime)
}

// Expectation: The returned [fuse.Dirent] slice should meet the expectations.
func Test_realDirNode_ReadDirAll_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()

	_, err := os.Create(filepath.Join(tmpDir, "file1"))
	require.NoError(t, err)

	_, err = os.Create(filepath.Join(tmpDir, "file2.zip"))
	require.NoError(t, err)

	_, err = os.Create(filepath.Join(tmpDir, "file3.zip"))
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir1"), dirBasePerm)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir2"), dirBasePerm)
	require.NoError(t, err)

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	ent, err := node.ReadDirAll(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 4)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir1"), ent[0].Inode)
	require.Equal(t, "dir1", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir2"), ent[1].Inode)
	require.Equal(t, "dir2", ent[1].Name)
	require.Equal(t, fuse.DT_Dir, ent[1].Type)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "file2"), ent[2].Inode)
	require.Equal(t, "file2", ent[2].Name)
	require.Equal(t, fuse.DT_Dir, ent[2].Type)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "file3"), ent[3].Inode)
	require.Equal(t, "file3", ent[3].Name)
	require.Equal(t, fuse.DT_Dir, ent[3].Type)
}

// Expectation: ENOENT should be returned upon accessing an invalid directory.
func Test_realDirNode_ReadDirAll_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir() + "_notexist"

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	ent, err := node.ReadDirAll(t.Context())
	require.Nil(t, ent)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: The returned lookup nodes should meet the expectations.
func Test_realDirNode_Lookup_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()

	_, err := os.Create(filepath.Join(tmpDir, "file1"))
	require.NoError(t, err)

	_, err = os.Create(filepath.Join(tmpDir, "file2.zip"))
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir1"), dirBasePerm)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir2"), dirBasePerm)
	require.NoError(t, err)

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	lk, err := node.Lookup(t.Context(), "file2")
	require.NoError(t, err)
	zn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "file2"), zn.Inode)
	require.Equal(t, filepath.Join(tmpDir, "file2.zip"), zn.Path)
	info, err := os.Stat(filepath.Join(tmpDir, "file2.zip"))
	require.NoError(t, err)
	require.WithinDuration(t, info.ModTime(), zn.Modified, time.Second)

	lk, err = node.Lookup(t.Context(), "dir1")
	require.NoError(t, err)
	dn, ok := lk.(*realDirNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir1"), dn.Inode)
	require.Equal(t, filepath.Join(tmpDir, "dir1"), dn.Path)
	info, err = os.Stat(filepath.Join(tmpDir, "dir1"))
	require.NoError(t, err)
	require.WithinDuration(t, info.ModTime(), dn.Modified, time.Second)

	lk, err = node.Lookup(t.Context(), "dir2")
	require.NoError(t, err)
	dn, ok = lk.(*realDirNode)
	require.True(t, ok)
	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir2"), dn.Inode)
	require.Equal(t, filepath.Join(tmpDir, "dir2"), dn.Path)
	info, err = os.Stat(filepath.Join(tmpDir, "dir2"))
	require.NoError(t, err)
	require.WithinDuration(t, info.ModTime(), dn.Modified, time.Second)
}

// Expectation: When a real directory and ZIP would result in the same name
// for the directory entry slice, the real directory should always be preferred.
func Test_realDirNode_Lookup_CollidingEntry_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()

	_, err := os.Create(filepath.Join(tmpDir, "file.zip")) // should be ignored
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "file"), dirBasePerm)
	require.NoError(t, err)

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	lk, err := node.Lookup(t.Context(), "file")
	require.NoError(t, err)
	_, ok := lk.(*realDirNode) // should be a realDirNode
	require.True(t, ok)
}

// Expectation: A lookup on a non-existing entry should return ENOENT.
func Test_realDirNode_Lookup_EntryNotExist_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	lk, err := node.Lookup(t.Context(), "notexist") // missing
	require.Nil(t, lk)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Inodes should remain deterministic and equal across calls.
func Test_realDirNode_DeterministicInodes_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()

	_, err := os.Create(filepath.Join(tmpDir, "file1"))
	require.NoError(t, err)

	_, err = os.Create(filepath.Join(tmpDir, "file2.zip"))
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir1"), dirBasePerm)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir2"), dirBasePerm)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(tmpDir, "dir3"), dirBasePerm)
	require.NoError(t, err)

	node := &realDirNode{
		Inode:    1,
		Path:     tmpDir,
		Modified: time.Now(),
	}

	ent, err := node.ReadDirAll(t.Context())
	require.NoError(t, err)
	require.Len(t, ent, 4)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir1"), ent[0].Inode)
	require.Equal(t, "dir1", ent[0].Name)
	require.Equal(t, fuse.DT_Dir, ent[0].Type)

	lk, err := node.Lookup(t.Context(), "dir1")
	require.NoError(t, err)
	dn, ok := lk.(*realDirNode)
	require.True(t, ok)
	require.Equal(t, ent[0].Inode, dn.Inode)
	attr := fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, attr.Inode, dn.Inode)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir2"), ent[1].Inode)
	require.Equal(t, "dir2", ent[1].Name)
	require.Equal(t, fuse.DT_Dir, ent[1].Type)

	lk, err = node.Lookup(t.Context(), "dir2")
	require.NoError(t, err)
	dn, ok = lk.(*realDirNode)
	require.True(t, ok)
	require.Equal(t, ent[1].Inode, dn.Inode)
	attr = fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, attr.Inode, dn.Inode)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "dir3"), ent[2].Inode)
	require.Equal(t, "dir3", ent[2].Name)
	require.Equal(t, fuse.DT_Dir, ent[2].Type)

	lk, err = node.Lookup(t.Context(), "dir3")
	require.NoError(t, err)
	dn, ok = lk.(*realDirNode)
	require.True(t, ok)
	require.Equal(t, ent[2].Inode, dn.Inode)
	attr = fuse.Attr{}
	err = dn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, attr.Inode, dn.Inode)

	require.Equal(t, fs.GenerateDynamicInode(node.Inode, "file2"), ent[3].Inode)
	require.Equal(t, "file2", ent[3].Name)
	require.Equal(t, fuse.DT_Dir, ent[3].Type)

	lk, err = node.Lookup(t.Context(), "file2")
	require.NoError(t, err)
	zn, ok := lk.(*zipDirNode)
	require.True(t, ok)
	require.Equal(t, ent[3].Inode, zn.Inode)
	attr = fuse.Attr{}
	err = zn.Attr(t.Context(), &attr)
	require.NoError(t, err)
	require.Equal(t, attr.Inode, zn.Inode)
}
