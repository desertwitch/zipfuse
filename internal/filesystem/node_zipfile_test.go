package filesystem

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

// Expectation: Open should set the caching flag and return the node itself as the handle.
func Test_zipInMemoryFileNode_Open_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test content for in-memory node")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "test.txt", ModTime: tnow, Content: content},
	})

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    fs.GenerateDynamicInode(1, "test.txt"),
			Archive:  zipPath,
			Path:     "test.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	resp := &fuse.OpenResponse{}
	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, resp)
	require.NoError(t, err)

	inmemHandle, ok := handle.(*zipInMemoryFileNode)
	require.True(t, ok)
	require.Equal(t, node, inmemHandle, "handle should be the same as the original node")
	require.NotZero(t, resp.Flags&fuse.OpenKeepCache)
}

// Expectation: ReadAll should return the complete content of the underlying file.
func Test_zipInMemoryFileNode_ReadAll_Success(t *testing.T) {
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

// Expectation: Open should set the caching flag and return a zipDiskStreamFileHandle.
func Test_zipDiskStreamFileNode_Open_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("test content for disk stream node")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "stream.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    fs.GenerateDynamicInode(1, "stream.txt"),
			Archive:  zipPath,
			Path:     "stream.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	resp := &fuse.OpenResponse{}
	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, resp)
	require.NoError(t, err)

	streamHandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)
	require.NotNil(t, streamHandle)

	defer func() {
		err = streamHandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	require.NotZero(t, resp.Flags&fuse.OpenKeepCache)
}

// Expectation: Read should return ENOENT for a missing file.
func Test_zipDiskStreamFileNode_Open_FileNotFound_Error(t *testing.T) {
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

	_, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.ErrorIs(t, err, fuse.ToErrno(syscall.ENOENT))
}

// Expectation: Read should return EINVAL for an invalid archive.
func Test_zipDiskStreamFileNode_Open_InvalidArchive_Error(t *testing.T) {
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

	_, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Read should return the requested bytes from the specified offset.
func Test_zipDiskStreamFileHandle_Read_Success(t *testing.T) {
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

	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.NoError(t, err)

	fhandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)

	defer func() {
		err = fhandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	req := &fuse.ReadRequest{
		Offset: 10,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[10:20], resp.Data)
}

// Expectation: Read should handle empty files correctly.
func Test_zipDiskStreamFileHandle_Read_EmptyFile_Success(t *testing.T) {
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

	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.NoError(t, err)

	fhandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)

	defer func() {
		err = fhandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   10,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(context.Background(), req, resp)
	require.NoError(t, err)
	require.Empty(t, resp.Data)
}

// Expectation: Read should handle reading from offset 0.
func Test_zipDiskStreamFileHandle_Read_OffsetZero_Success(t *testing.T) {
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

	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.NoError(t, err)

	fhandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)

	defer func() {
		err = fhandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	req := &fuse.ReadRequest{
		Offset: 0,
		Size:   11,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[:11], resp.Data)
}

// Expectation: Read should handle reading beyond EOF gracefully.
func Test_zipDiskStreamFileHandle_Read_BeyondEOF_Success(t *testing.T) {
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

	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.NoError(t, err)

	fhandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)

	defer func() {
		err = fhandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	req := &fuse.ReadRequest{
		Offset: 3,
		Size:   100,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content[3:], resp.Data)
}

// Expectation: Reading backwards on a non-seekable should trigger a rewind.
func Test_zipDiskStreamFileHandle_Read_NoSeekRewind_Success(t *testing.T) {
	Options.MustCRC32.Store(true)
	defer Options.MustCRC32.Store(false)

	tmpDir := t.TempDir()
	tnow := time.Now()

	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "dir/", ModTime: tnow, Content: nil},
		{Path: "dir/rewind.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			Inode:    0,
			Archive:  zipPath,
			Path:     "dir/rewind.txt",
			Size:     uint64(len(content)),
			Modified: tnow,
		},
	}

	handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.NoError(t, err)

	fhandle, ok := handle.(*zipDiskStreamFileHandle)
	require.True(t, ok)

	defer func() {
		err = fhandle.Release(t.Context(), &fuse.ReleaseRequest{})
		require.NoError(t, err)
	}()

	// First read at offset 5
	req1 := &fuse.ReadRequest{
		Offset: 5,
		Size:   10,
	}
	resp1 := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req1, resp1)
	require.NoError(t, err)
	require.Equal(t, content[5:15], resp1.Data)

	initialReopenCount := Metrics.TotalReopenedZips.Load()

	// Second read at offset 1 (backwards) - should trigger rewind
	req2 := &fuse.ReadRequest{
		Offset: 1,
		Size:   10,
	}
	resp2 := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req2, resp2)
	require.NoError(t, err)
	require.Equal(t, content[1:11], resp2.Data)

	finalReopenCount := Metrics.TotalReopenedZips.Load()
	require.Equal(t, initialReopenCount+1, finalReopenCount)
}
