package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/stretchr/testify/require"
)

// Expectation: Attr should fill in the [fuse.Attr] with the correct values.
func Test_zipBaseFileNode_Attr_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tnow := time.Now()

	node := &zipBaseFileNode{
		Inode:    fs.GenerateDynamicInode(1, "test.txt"),
		Archive:  "",
		Path:     "",
		Size:     1024,
		Modified: tnow,
	}

	attr := fuse.Attr{}
	err := node.Attr(t.Context(), &attr)
	require.NoError(t, err)

	require.Equal(t, fs.GenerateDynamicInode(1, "test.txt"), attr.Inode)
	require.Equal(t, os.FileMode(fileBasePerm), attr.Mode)
	require.Equal(t, uint64(1024), attr.Size)
	require.Equal(t, tnow, attr.Atime)
	require.Equal(t, tnow, attr.Ctime)
	require.Equal(t, tnow, attr.Mtime)
}

// Expectation: ReadAll should return the complete content of the underlying file.
func Test_zipInMemoryFileNode_ReadAll_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test file content for in-memory reading")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/test.txt", ModTime: tnow, Content: content},
	})

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "dir/test.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, content, data)
}

// Expectation: ReadAll should handle empty files correctly.
func Test_zipInMemoryFileNode_ReadAll_EmptyFile_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/empty.txt", ModTime: tnow, Content: []byte{}},
	})

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "dir/empty.txt",
			Size:     0,
			Modified: tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Empty(t, data)
}

// Expectation: ReadAll should return ENOENT for a missing file.
func Test_zipInMemoryFileNode_ReadAll_FileNotFound_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/other.txt", ModTime: tnow, Content: []byte("other content")},
	})

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "dir/missing.txt",
			Size:     0,
			Modified: tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.Nil(t, data)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: ReadAll should return EINVAL for an invalid archive.
func Test_zipInMemoryFileNode_ReadAll_InvalidArchive_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tnow := time.Now()

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  "/nonexistent/archive.zip",
			Path:     "dir/test.txt",
			Size:     100,
			Modified: tnow,
		},
	}

	data, err := node.ReadAll(context.Background())
	require.Nil(t, data)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Read should return the requested bytes from the specified offset.
func Test_zipDiskStreamFileNode_Read_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/stream.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "dir/stream.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 10,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[10:20], resp.Data)
}

// Expectation: Read should handle empty files correctly.
func Test_zipDiskStreamFileNode_Read_EmptyFile_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "empty.txt", ModTime: tnow, Content: []byte{}},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    fs.GenerateDynamicInode(1, "empty.txt"),
			Archive:  zipPath,
			Path:     "empty.txt",
			Size:     0,
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(context.Background(), req, resp)
	require.NoError(t, err)
	require.Empty(t, resp.Data)
}

// Expectation: Read should handle reading from offset 0.
func Test_zipDiskStreamFileNode_Read_OffsetZero_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test content at start")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "file.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "file.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   11,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[:11], resp.Data)
}

// Expectation: Read should handle reading beyond EOF gracefully.
func Test_zipDiskStreamFileNode_Read_BeyondEOF_Success(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("short")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "short.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "short.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 3,
		Size:   100,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[3:], resp.Data)
}

// Expectation: Read should return ENOENT for a missing file.
func Test_zipDiskStreamFileNode_Read_FileNotFound_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tmpDir := t.TempDir()
	tnow := time.Now()

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "exists.txt", ModTime: tnow, Content: []byte("content")},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "missing.txt",
			Size:     0,
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(t.Context(), req, resp)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Read should return EINVAL for an invalid archive.
func Test_zipDiskStreamFileNode_Read_InvalidArchive_Error(t *testing.T) {
	logs = newLogBuffer(5)
	tnow := time.Now()

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  "/nonexistent/archive.zip",
			Path:     "test.txt",
			Size:     100,
			Modified: tnow,
		},
	}

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err := node.Read(t.Context(), req, resp)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}
