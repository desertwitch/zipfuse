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
	Prefix   string    // Prefix within the underlying ZIP archive.
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

func (z *zipDirNode) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	if Options.FlatMode {
		return z.readDirAllFlat(ctx)
	}

	return z.readDirAllNested(ctx)
}

func (z *zipDirNode) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if Options.FlatMode {
		return z.lookupFlat(ctx, name)
	}

	return z.lookupNested(ctx, name)
}

func (z *zipDirNode) readDirAllFlat(_ context.Context) ([]fuse.Dirent, error) {
	seen := make(map[string]bool)
	resp := make([]fuse.Dirent, 0)

	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->ReadDirAll: ZIP Error: %v\n", z.Path, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	for _, f := range zr.File {
		normalizedPath := normalizeZipPath(f.Name)

		if isDir(f, normalizedPath) {
			continue
		}

		name, ok := flatEntryName(normalizedPath)
		if !ok || name == "" || seen[name] {
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
		return strings.Compare(a.Name, b.Name) // only [fuse.DT_File]
	})

	return resp, nil
}

func (z *zipDirNode) lookupFlat(_ context.Context, name string) (fs.Node, error) {
	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->Lookup->%q: ZIP Error: %v\n", z.Path, name, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	for _, f := range zr.File {
		normalizedPath := normalizeZipPath(f.Name)

		// Dirent is already normalized and flat, needs checking against that:
		flatName, ok := flatEntryName(normalizedPath)
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

		if f.UncompressedSize64 <= Options.StreamingThreshold.Load() {
			return &zipInMemoryFileNode{base}, nil
		}

		return &zipDiskStreamFileNode{base}, nil
	}

	return nil, fuse.ToErrno(syscall.ENOENT)
}

func (z *zipDirNode) readDirAllNested(_ context.Context) ([]fuse.Dirent, error) {
	resp := []fuse.Dirent{}
	seen := map[string]bool{}

	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->ReadDirAll: ZIP error: %v\n", z.Path, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	for _, f := range zr.File {
		normalizedPath := normalizeZipPath(f.Name)

		// Prefix is already normalized, needs checking against that:
		if !strings.HasPrefix(normalizedPath, z.Prefix) {
			continue
		}

		relPath := strings.TrimPrefix(normalizedPath, z.Prefix)
		parts := strings.SplitN(relPath, "/", 2) //nolint:mnd

		name := parts[0]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		if len(parts) == 1 && !isDir(f, normalizedPath) {
			resp = append(resp, fuse.Dirent{
				Name:  name,
				Type:  fuse.DT_File,
				Inode: fs.GenerateDynamicInode(z.Inode, name),
			})
		} else { // Can be explicit or implicit (dir/, dir/file.txt):
			resp = append(resp, fuse.Dirent{
				Name:  name,
				Type:  fuse.DT_Dir,
				Inode: fs.GenerateDynamicInode(z.Inode, name),
			})
		}
	}

	slices.SortFunc(resp, func(a, b fuse.Dirent) int {
		if a.Type == b.Type {
			return strings.Compare(a.Name, b.Name)
		}
		if a.Type == fuse.DT_Dir {
			return -1
		}

		return 1
	})

	return resp, nil
}

func (z *zipDirNode) lookupNested(_ context.Context, name string) (fs.Node, error) {
	zr, err := newZipReader(z.Path, false)
	if err != nil {
		logging.Printf("%q->Lookup->%q: ZIP error: %v\n", z.Path, name, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer zr.Close(0)

	fullPath := z.Prefix + name

	for _, f := range zr.File {
		normalizedPath := normalizeZipPath(f.Name)

		// Dirent is already normalized, needs checking against that:
		if normalizedPath == fullPath && !isDir(f, normalizedPath) {
			base := &zipBaseFileNode{
				Archive:  z.Path,
				Path:     f.Name,
				Inode:    fs.GenerateDynamicInode(z.Inode, name),
				Size:     f.UncompressedSize64,
				Modified: f.Modified,
			}

			if f.UncompressedSize64 <= Options.StreamingThreshold.Load() {
				return &zipInMemoryFileNode{base}, nil
			}

			return &zipDiskStreamFileNode{base}, nil
		}

		// Can be explicit or implicit (dir/, dir/file.txt):
		if strings.HasPrefix(normalizedPath, fullPath+"/") {
			return &zipDirNode{
				Path:     z.Path,
				Prefix:   fullPath + "/",
				Inode:    fs.GenerateDynamicInode(z.Inode, name),
				Modified: z.Modified,
			}, nil
		}
	}

	return nil, fuse.ToErrno(syscall.ENOENT)
}
