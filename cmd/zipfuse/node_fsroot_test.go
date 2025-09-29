package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Expectation: RootDir should be returned as a [realDirNode].
func Test_zipFS_Root_Success(t *testing.T) {
	t.Parallel()

	zfs := &zipFS{
		RootDir: t.TempDir(),
	}

	node, err := zfs.Root()
	require.NoError(t, err)

	dn, ok := node.(*realDirNode)
	require.True(t, ok)

	require.Equal(t, uint64(1), dn.Inode)
	require.Equal(t, dn.Path, zfs.RootDir)
	require.NotZero(t, dn.Modified)
}

// Expectation: A panic should occur when GenerateInode is called.
func Test_zipFS_GenerateInode_Panic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		require.NotNil(t, r, "GenerateInode must panic")
	}()

	zfs := &zipFS{
		RootDir: t.TempDir(),
	}

	zfs.GenerateInode(0, "")
}
