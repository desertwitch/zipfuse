package filesystem

import (
	"context"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/logging"
)

var (
	_ fs.Node               = (*zipDirNode)(nil)
	_ fs.HandleReadDirAller = (*zipDirNode)(nil)
	_ fs.NodeStringLookuper = (*zipDirNode)(nil)
)

// zipDirNode is a ZIP archive file of the mirrored filesystem.
// It is now presented as a regular directory within our filesystem.
// All structures contained in the archive are flattened (by [flatEntryName])
// and presented as regular files (to be unpacked into memory when requested).
type zipDirNode struct {
	Inode    uint64    // Inode within our filesystem.
	Path     string    // Path of the underlying ZIP archive.
	Modified time.Time // Modified time of the underlying ZIP archive.
}

func (z *zipDirNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | dirBasePerm
	a.Inode = z.Inode

	a.Atime = z.Modified
	a.Ctime = z.Modified
	a.Mtime = z.Modified

	return nil
}

func (z *zipDirNode) ReadDirAll(_ context.Context) ([]fuse.Dirent, error) {
	seen := make(map[string]bool)
	resp := make([]fuse.Dirent, 0)

	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->ReadDirAll: ZIP Error: %v\n", z.Path, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		name, ok := flatEntryName(f.Name)
		if !ok || seen[name] {
			logging.Printf("Skipped: %q->ReadDirAll: %q -> %q (duplicate or invalid sanitized name)\n", z.Path, f.Name, name)

			continue
		}
		seen[name] = true

		resp = append(resp, fuse.Dirent{
			Name:  name,
			Type:  fuse.DT_File,
			Inode: fs.GenerateDynamicInode(z.Inode, name),
		})
	}

	slices.SortFunc(resp, func(a, b fuse.Dirent) int {
		return strings.Compare(a.Name, b.Name)
	})

	return resp, nil
}

func (z *zipDirNode) Lookup(_ context.Context, name string) (fs.Node, error) {
	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->Lookup->%q: ZIP Error: %v\n", z.Path, name, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	for _, f := range zr.File {
		// Dirent is already flat, so needs checking against a flat name:
		flatName, ok := flatEntryName(f.Name)
		if !ok || flatName != name {
			continue
		}

		base := &zipBaseFileNode{
			Archive:  z.Path,
			Path:     f.Name,
			Inode:    fs.GenerateDynamicInode(z.Inode, name),
			Size:     f.UncompressedSize64,
			Modified: f.Modified,
		}

		if f.UncompressedSize64 <= StreamingThreshold.Load() {
			return &zipInMemoryFileNode{base}, nil
		}

		return &zipDiskStreamFileNode{base}, nil
	}

	return nil, fuse.ToErrno(syscall.ENOENT)
}
