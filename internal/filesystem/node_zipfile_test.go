package filesystem

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/stretchr/testify/require"
)

// Expectation: Attr should fill in the [fuse.Attr] with the correct values.
func Test_zipBaseFileNode_Attr_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipBaseFileNode{
		fsys:    fsys,
		inode:   fs.GenerateDynamicInode(1, "test.txt"),
		archive: "",
		path:    "",
		size:    1024,
		mtime:   tnow,
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
	t.Parallel()

	for _, mode := range []bool{true, false} {
		t.Run("StrictCache="+strconv.FormatBool(mode), func(t *testing.T) {
			t.Parallel()
			tmpDir, fsys := testFS(t, io.Discard)
			tnow := time.Now()

			fsys.Options.StrictCache = mode

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
					fsys:    fsys,
					inode:   fs.GenerateDynamicInode(1, "test.txt"),
					archive: zipPath,
					path:    "test.txt",
					size:    uint64(len(content)),
					mtime:   tnow,
				},
			}

			resp := &fuse.OpenResponse{}
			handle, err := node.Open(t.Context(), &fuse.OpenRequest{}, resp)
			require.NoError(t, err)

			inmemHandle, ok := handle.(*zipInMemoryFileNode)
			require.True(t, ok)
			require.Equal(t, node, inmemHandle, "handle should be the same as the original node")

			if fsys.Options.StrictCache {
				require.Zero(t, resp.Flags&fuse.OpenKeepCache, "OpenKeepCache flag should not be set")
			} else {
				require.NotZero(t, resp.Flags&fuse.OpenKeepCache, "OpenKeepCache flag should be set")
			}
		})
	}
}

// Expectation: ReadAll should return the complete content of the underlying file.
func Test_zipInMemoryFileNode_ReadAll_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "dir/test.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, content, data)
}

// Expectation: ReadAll should handle empty files correctly.
func Test_zipInMemoryFileNode_ReadAll_EmptyFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "dir/empty.txt",
			size:    0,
			mtime:   tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Empty(t, data)
}

// Expectation: ReadAll should return EINVAL for a missing file.
func Test_zipInMemoryFileNode_ReadAll_FileNotFound_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "dir/missing.txt",
			size:    0,
			mtime:   tnow,
		},
	}

	data, err := node.ReadAll(t.Context())
	require.Nil(t, data)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: ReadAll should return EINVAL for an invalid archive.
func Test_zipInMemoryFileNode_ReadAll_InvalidArchive_Error(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipInMemoryFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: "/nonexistent/archive.zip",
			path:    "dir/test.txt",
			size:    100,
			mtime:   tnow,
		},
	}

	data, err := node.ReadAll(context.Background())
	require.Nil(t, data)
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Open should set the caching flag and return a zipDiskStreamFileHandle.
func Test_zipDiskStreamFileNode_Open_Success(t *testing.T) {
	t.Parallel()

	for _, mode := range []bool{true, false} {
		t.Run("StrictCache="+strconv.FormatBool(mode), func(t *testing.T) {
			t.Parallel()
			tmpDir, fsys := testFS(t, io.Discard)
			tnow := time.Now()

			fsys.Options.StrictCache = mode

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
					fsys:    fsys,
					inode:   fs.GenerateDynamicInode(1, "stream.txt"),
					archive: zipPath,
					path:    "stream.txt",
					size:    uint64(len(content)),
					mtime:   tnow,
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

			if fsys.Options.StrictCache {
				require.Zero(t, resp.Flags&fuse.OpenKeepCache, "OpenKeepCache flag should not be set")
			} else {
				require.NotZero(t, resp.Flags&fuse.OpenKeepCache, "OpenKeepCache flag should be set")
			}
		})
	}
}

// Expectation: Read should return EINVAL for a missing file.
func Test_zipDiskStreamFileNode_Open_FileNotFound_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "missing.txt",
			size:    0,
			mtime:   tnow,
		},
	}

	_, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Read should return EINVAL for an invalid archive.
func Test_zipDiskStreamFileNode_Open_InvalidArchive_Error(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: "/nonexistent/archive.zip",
			path:    "test.txt",
			size:    100,
			mtime:   tnow,
		},
	}

	_, err := node.Open(t.Context(), &fuse.OpenRequest{}, &fuse.OpenResponse{})
	require.ErrorIs(t, err, fuse.ToErrno(syscall.EINVAL))
}

// Expectation: Read should return the requested bytes from the specified offset.
func Test_zipDiskStreamFileHandle_Read_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "dir/stream.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   fs.GenerateDynamicInode(1, "empty.txt"),
			archive: zipPath,
			path:    "empty.txt",
			size:    0,
			mtime:   tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "file.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "short.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	fsys.Options.MustCRC32.Store(true)

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
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "dir/rewind.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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

	initialReopenCount := fsys.Metrics.TotalReopenedEntries.Load()

	// Second read at offset 1 (backwards) - should trigger rewind
	req2 := &fuse.ReadRequest{
		Offset: 1,
		Size:   10,
	}
	resp2 := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req2, resp2)
	require.NoError(t, err)
	require.Equal(t, content[1:11], resp2.Data)

	finalReopenCount := fsys.Metrics.TotalReopenedEntries.Load()
	require.Equal(t, initialReopenCount+1, finalReopenCount)
}

// Expectation: Multiple concurrent reads on the same file handle should not
// race or corrupt data, including when read operations require seeking (or
// pseudo-seeking on non-seekables) in a sequential/non-sequential manner.
func Test_zipDiskStreamFileHandle_Read_Concurrent_Success(t *testing.T) {
	t.Parallel()

	fn := func(MustCRC32 bool) {
		tmpDir, fsys := testFS(t, io.Discard)
		fsys.Options.MustCRC32.Store(MustCRC32)

		tnow := time.Now()

		contentLen := 1024 * 10
		content := make([]byte, contentLen)
		for i := range contentLen {
			content[i] = byte(i % 256)
		}

		zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
			Path    string
			ModTime time.Time
			Content []byte
		}{
			{Path: "dir/concurrent_seek_test.txt", ModTime: tnow, Content: content},
		})

		node := &zipDiskStreamFileNode{
			zipBaseFileNode: &zipBaseFileNode{
				fsys:    fsys,
				inode:   0,
				archive: zipPath,
				path:    "dir/concurrent_seek_test.txt",
				size:    uint64(contentLen),
				mtime:   tnow,
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

		readSize := 1024

		reads := []struct {
			Offset int64
			Size   int
		}{
			{Offset: 5000, Size: readSize},                   // Initial read, deep into the file
			{Offset: 5000 + int64(readSize), Size: readSize}, // Sequential read, deeper into the file
			{Offset: 1000, Size: readSize},                   // Backward seek (should trigger a rewind)
			{Offset: 8000, Size: readSize},                   // Forward seek
			{Offset: 2000, Size: readSize},                   // Backward seek (should trigger a rewind)
			{Offset: 0, Size: readSize},                      // Read from the very start (backward/rewind)
			{Offset: 9216, Size: readSize},                   // Read near the end (forward)
			{Offset: int64(contentLen), Size: readSize},      // Read beyond the end of file (EOF)
		}

		numReaders := len(reads)
		errChan := make(chan error, numReaders)

		var wg sync.WaitGroup

		for i, r := range reads {
			wg.Go(func() {
				req := &fuse.ReadRequest{
					Offset: r.Offset,
					Size:   r.Size,
				}
				resp := &fuse.ReadResponse{}

				err := fhandle.Read(context.Background(), req, resp)
				if err != nil {
					errChan <- fmt.Errorf("read %d (offset %d) failed: %w", i, r.Offset, err)

					return
				}

				endOffset := min(int(r.Offset)+r.Size, len(content))
				expectedData := content[r.Offset:endOffset]

				if !bytes.Equal(expectedData, resp.Data) {
					errChan <- fmt.Errorf("read %d data mismatch at offset %d: expected length %d, got %d",
						i, r.Offset, len(expectedData), len(resp.Data))

					return
				}

				errChan <- nil
			})
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			require.NoError(t, err)
		}
	}

	for _, mode := range []bool{true, false} {
		t.Run("MustCRC32="+strconv.FormatBool(mode), func(t *testing.T) {
			t.Parallel()
			fn(mode)
		})
	}
}

// Expectation: Read should use buffer from pool when size is within pool capacity.
func Test_zipDiskStreamFileHandle_Read_Pool_UseBuffer_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := []byte("small content that fits in pool buffer")
	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "small.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "small.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
		Size:   len(content),
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content, resp.Data)
}

// Expectation: Read should allocate new buffer when requested size exceeds pool capacity.
func Test_zipDiskStreamFileHandle_Read_Pool_AllocLargeBuffer_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	// Content to force allocation
	largeSize := fsys.Options.StreamPoolSize + 1024
	content := make([]byte, largeSize)
	for i := range content {
		content[i] = byte(i % 256)
	}

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "large.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "large.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
		Size:   largeSize,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content, resp.Data)
}

// Expectation: Multiple reads with pool-sized buffers should reuse pool buffers.
func Test_zipDiskStreamFileHandle_Read_Pool_BufferReuse_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	// Content that can be read in pool-sized chunks
	totalSize := fsys.Options.StreamPoolSize * 3
	content := make([]byte, totalSize)
	for i := range content {
		content[i] = byte(i % 256)
	}

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "chunked.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "chunked.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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

	for i := range 3 {
		offset := int64(i * fsys.Options.StreamPoolSize)
		req := &fuse.ReadRequest{
			Offset: offset,
			Size:   fsys.Options.StreamPoolSize,
		}
		resp := &fuse.ReadResponse{}

		err = fhandle.Read(t.Context(), req, resp)
		require.NoError(t, err)
		require.Equal(t, content[offset:offset+int64(fsys.Options.StreamPoolSize)], resp.Data)
	}
}

// Expectation: Read at pool buffer size boundary should work correctly.
func Test_zipDiskStreamFileHandle_Read_Pool_BufferBoundary_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	content := make([]byte, fsys.Options.StreamPoolSize)
	for i := range content {
		content[i] = byte(i % 256)
	}

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "boundary.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "boundary.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
		Size:   fsys.Options.StreamPoolSize,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content, resp.Data)
}

// Expectation: Read with size just over pool capacity should allocate new buffer.
func Test_zipDiskStreamFileHandle_Read_Pool_OverCapacity_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	oversize := fsys.Options.StreamPoolSize + 1
	content := make([]byte, oversize)
	for i := range content {
		content[i] = byte(i % 256)
	}

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "oversize.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "oversize.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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
		Size:   oversize,
	}
	resp := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req, resp)
	require.NoError(t, err)
	require.Equal(t, content, resp.Data)
}

// Expectation: Small read followed by large read should handle buffer allocation correctly.
func Test_zipDiskStreamFileHandle_Read_Pool_SmallThenLarge_Success(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	totalSize := fsys.Options.StreamPoolSize * 2
	content := make([]byte, totalSize)
	for i := range content {
		content[i] = byte(i % 256)
	}

	zipPath := createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "mixed.txt", ModTime: tnow, Content: content},
	})

	node := &zipDiskStreamFileNode{
		zipBaseFileNode: &zipBaseFileNode{
			fsys:    fsys,
			inode:   0,
			archive: zipPath,
			path:    "mixed.txt",
			size:    uint64(len(content)),
			mtime:   tnow,
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

	// Small read first (uses pool)
	req1 := &fuse.ReadRequest{
		Offset: 0,
		Size:   1024,
	}
	resp1 := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req1, resp1)
	require.NoError(t, err)
	require.Equal(t, content[:1024], resp1.Data)

	// Large read second (allocates)
	req2 := &fuse.ReadRequest{
		Offset: 1024,
		Size:   fsys.Options.StreamPoolSize + 512,
	}
	resp2 := &fuse.ReadResponse{}

	err = fhandle.Read(t.Context(), req2, resp2)
	require.NoError(t, err)
	require.Equal(t, content[1024:1024+fsys.Options.StreamPoolSize+512], resp2.Data)
}
