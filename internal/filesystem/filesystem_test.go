package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/stretchr/testify/require"
)

func testFS(t *testing.T, out io.Writer) (string, *FS) {
	t.Helper()

	tmp := t.TempDir()
	rbf := logging.NewRingBuffer(10, out)
	fsys, err := NewFS(tmp, nil, rbf)
	require.NoError(t, err)

	t.Cleanup(func() {
		fsys.HaltPurgeCache()
		fsys.Cleanup()
	})

	return tmp, fsys
}

// Expectation: RootDir should be returned as a [realDirNode].
func Test_FS_Root_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	node, err := fsys.Root()
	require.NoError(t, err)

	dn, ok := node.(*realDirNode)
	require.True(t, ok)

	require.Equal(t, uint64(1), dn.inode)
	require.Equal(t, dn.path, fsys.RootDir)
	require.NotZero(t, dn.mtime)
}

// Expectation: Two FS over the same root should produce identical results,
// for both FlatMode = false and FlatMode = true.
func Test_FS_Deterministic_Success(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	tnow := time.Now()

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "dir"), 0o777))
	_ = createTestZip(t, tmpDir, "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "a.txt", ModTime: tnow, Content: []byte("A")},
		{Path: "sub/", ModTime: tnow, Content: nil},
		{Path: "sub/b.txt", ModTime: tnow, Content: []byte("B")},
	})

	type entry struct {
		attrInode uint64
		dirInode  uint64
		hasDirent bool
	}

	collect := func(zpfs *FS) ([]string, map[string]entry) {
		var paths []string
		entries := make(map[string]entry)

		err := zpfs.Walk(t.Context(), func(path string, d *fuse.Dirent, _ fs.Node, a fuse.Attr) error {
			paths = append(paths, path)
			e := entry{attrInode: a.Inode}
			if d != nil {
				e.dirInode = d.Inode
				e.hasDirent = true
				require.Equal(t, e.attrInode, e.dirInode)
			}
			entries[path] = e

			return nil
		})
		require.NoError(t, err)

		return paths, entries
	}

	for _, mode := range []bool{false, true} {
		t.Run("FlatMode="+strconv.FormatBool(mode), func(t *testing.T) {
			t.Parallel()

			fs1, err := NewFS(tmpDir, nil, logging.NewRingBuffer(10, io.Discard))
			require.NoError(t, err)
			fs1.Options.FlatMode = mode
			fs2, err := NewFS(tmpDir, nil, logging.NewRingBuffer(10, io.Discard))
			require.NoError(t, err)
			fs2.Options.FlatMode = mode

			paths1, entries1 := collect(fs1)
			paths2, entries2 := collect(fs2)

			require.Equal(t, paths1, paths2)

			for _, p := range paths1 {
				e1 := entries1[p]
				e2 := entries2[p]

				require.Equal(t, e1.attrInode, e2.attrInode, "attr inode mismatch at %q", p)

				if e1.hasDirent || e2.hasDirent {
					require.True(t, e1.hasDirent && e2.hasDirent, "dirent presence mismatch at %q", p)
					require.Equal(t, e1.dirInode, e2.dirInode, "dirent inode mismatch at %q", p)
				}
			}
		})
	}
}

// Expectation: A panic should occur when GenerateInode is called.
func Test_FS_GenerateInode_Panic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		require.NotNil(t, r, "GenerateInode must panic")
	}()

	_, fsys := testFS(t, io.Discard)

	fsys.GenerateInode(0, "")
}

// Expectation: Walk should visit all file and directory nodes in the tree.
func Test_FS_Walk_Success(t *testing.T) {
	t.Parallel()

	tmpDir, fsys := testFS(t, io.Discard)
	tnow := time.Now()

	dir1 := filepath.Join(tmpDir, "dir1", "dir")
	require.NoError(t, os.MkdirAll(dir1, 0o777))

	dir2 := filepath.Join(tmpDir, "dir2", "dir")
	require.NoError(t, os.MkdirAll(dir2, 0o777))

	dir3 := filepath.Join(tmpDir, "dir3")
	require.NoError(t, os.MkdirAll(dir3, 0o777))

	zipPath := createTestZip(t, filepath.Join(tmpDir, "dir1", "dir"), "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "file.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/images/", ModTime: tnow, Content: nil},
		{Path: "docs/images/logo.png", ModTime: tnow, Content: []byte("image")},
	})
	require.FileExists(t, zipPath)

	zipPath2 := createTestZip(t, filepath.Join(tmpDir, "dir2", "dir"), "test.zip", []struct {
		Path    string
		ModTime time.Time
		Content []byte
	}{
		{Path: "file.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/", ModTime: tnow, Content: nil},
		{Path: "docs/a.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/b.txt", ModTime: tnow, Content: []byte("test content")},
		{Path: "docs/images/", ModTime: tnow, Content: nil},
		{Path: "docs/images/logo.png", ModTime: tnow, Content: []byte("image")},
	})
	require.FileExists(t, zipPath2)

	visited := make(map[string]bool)

	err := fsys.Walk(t.Context(), func(path string, d *fuse.Dirent, n fs.Node, a fuse.Attr) error {
		require.NotEmpty(t, path)
		require.NotNil(t, n)
		require.NotNil(t, a)

		if path != "/" {
			require.NotNil(t, d)
		}

		visited[path] = true

		return nil
	})
	require.NoError(t, err)

	require.Contains(t, visited, "/")

	require.Contains(t, visited, "/dir1")
	require.Contains(t, visited, "/dir2")
	require.Contains(t, visited, "/dir3")

	require.Contains(t, visited, "/dir1/dir")
	require.Contains(t, visited, "/dir2/dir")

	require.Contains(t, visited, "/dir1/dir/test")
	require.Contains(t, visited, "/dir2/dir/test")

	require.Contains(t, visited, "/dir1/dir/test/file.txt")
	require.Contains(t, visited, "/dir1/dir/test/docs")
	require.Contains(t, visited, "/dir1/dir/test/docs/a.txt")
	require.Contains(t, visited, "/dir1/dir/test/docs/b.txt")
	require.Contains(t, visited, "/dir1/dir/test/docs/images")
	require.Contains(t, visited, "/dir1/dir/test/docs/images/logo.png")

	require.Contains(t, visited, "/dir2/dir/test/file.txt")
	require.Contains(t, visited, "/dir2/dir/test/docs")
	require.Contains(t, visited, "/dir2/dir/test/docs/a.txt")
	require.Contains(t, visited, "/dir2/dir/test/docs/b.txt")
	require.Contains(t, visited, "/dir2/dir/test/docs/images")
	require.Contains(t, visited, "/dir2/dir/test/docs/images/logo.png")

	require.Len(t, visited, 20)
}

// Expectation: Walk should propagate errors returned by the callback.
func Test_FS_Walk_CallbackError_Error(t *testing.T) {
	t.Parallel()

	tmpDir, fsys := testFS(t, io.Discard)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("x"), 0o644))

	testErr := errors.New("simulated error")

	err := fsys.Walk(t.Context(), func(_ string, _ *fuse.Dirent, _ fs.Node, _ fuse.Attr) error {
		return testErr
	})
	require.ErrorIs(t, err, testErr)
}

// Expectation: Walk should respect a context cancellation and report the correct error.
func Test_FS_Walk_ContextError_Error(t *testing.T) {
	t.Parallel()
	tmpDir, fsys := testFS(t, io.Discard)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("x"), 0o644))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := fsys.Walk(ctx, func(_ string, _ *fuse.Dirent, _ fs.Node, _ fuse.Attr) error {
		t.Fatal("walk should not begin when context is cancelled")

		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

// Expectation: An error should be returned as-is and counted in the metrics.
func Test_FS_fsError_Success(t *testing.T) {
	t.Parallel()
	_, fsys := testFS(t, io.Discard)

	customErr := errors.New("test error")
	err := fsys.fsError(customErr)

	require.ErrorIs(t, err, customErr)
	require.Equal(t, int64(1), fsys.Metrics.Errors.Load())
}
