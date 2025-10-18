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
)

var (
	_ fs.Node               = (*zipDirNode)(nil)
	_ fs.NodeOpener         = (*zipDirNode)(nil)
	_ fs.HandleReadDirAller = (*zipDirNode)(nil)
	_ fs.NodeStringLookuper = (*zipDirNode)(nil)
)

// zipDirNode is a ZIP archive file of the mirrored filesystem.
// It is now presented as a regular directory within our filesystem.
// When enabled, contained structures are flattened (by [flatEntryName]).
// Archive contents are presented as regular entries and unpacked on-the-fly.
type zipDirNode struct {
	fsys   *FS       // Pointer to our filesystem.
	inode  uint64    // Inode within our filesystem.
	path   string    // Path of the underlying ZIP archive.
	prefix string    // Prefix within the underlying ZIP archive.
	mtime  time.Time // Modified time of the underlying ZIP archive.
}

func (z *zipDirNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | dirBasePerm
	a.Inode = z.inode

	a.Atime = z.mtime
	a.Ctime = z.mtime
	a.Mtime = z.mtime

	return nil
}

func (z *zipDirNode) Open(_ context.Context, _ *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if !z.fsys.Options.StrictCache {
		resp.Flags |= fuse.OpenKeepCache | fuse.OpenCacheDir
	}

	return z, nil
}

func (z *zipDirNode) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	if z.fsys.Options.FlatMode {
		return z.readDirAllFlat(ctx)
	}

	return z.readDirAllNested(ctx)
}

func (z *zipDirNode) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if z.fsys.Options.FlatMode {
		return z.lookupFlat(ctx, name)
	}

	return z.lookupNested(ctx, name)
}

func (z *zipDirNode) readDirAllFlat(_ context.Context) ([]fuse.Dirent, error) {
	m := newZipMetric(z.fsys, false)
	defer m.Done()

	seen := make(map[string]bool)
	resp := make([]fuse.Dirent, 0)

	zr, err := z.fsys.fdcache.Archive(z.path)
	if err != nil {
		z.fsys.rbuf.Printf("%q->ReadDirAll: ZIP Error: %v\n", z.path, err)

		return nil, z.fsys.countError(toFuseErr(syscall.EINVAL))
	}
	defer zr.Release() //nolint:errcheck

	for i, f := range zr.File {
		normalizedPath := zipEntryNormalize(i, f, m.fsys.Options.ForceUnicode)

		if isDir(f, normalizedPath) {
			continue
		}

		name, ok := flatEntryName(i, normalizedPath)
		if !ok || name == "" || seen[name] {
			z.fsys.rbuf.Printf("Skipped: %q->ReadDirAll: %q -> %q (duplicate or invalid sanitized name)\n", z.path, f.Name, name)

			continue
		}
		seen[name] = true

		resp = append(resp, fuse.Dirent{
			Name:  name,
			Type:  fuse.DT_File,
			Inode: fs.GenerateDynamicInode(z.inode, name),
		})
	}

	slices.SortFunc(resp, func(a, b fuse.Dirent) int {
		return strings.Compare(a.Name, b.Name) // only [fuse.DT_File]
	})

	return resp, nil
}

func (z *zipDirNode) lookupFlat(_ context.Context, name string) (fs.Node, error) {
	m := newZipMetric(z.fsys, false)
	defer m.Done()

	zr, err := z.fsys.fdcache.Archive(z.path)
	if err != nil {
		z.fsys.rbuf.Printf("%q->Lookup->%q: ZIP Error: %v\n", z.path, name, err)

		return nil, z.fsys.countError(toFuseErr(syscall.EINVAL))
	}
	defer zr.Release() //nolint:errcheck

	for i, f := range zr.File {
		normalizedPath := zipEntryNormalize(i, f, m.fsys.Options.ForceUnicode)

		// Dirent is already normalized and flat, needs checking against that:
		flatName, ok := flatEntryName(i, normalizedPath)
		if !ok || flatName != name {
			continue
		}

		base := &zipBaseFileNode{
			fsys:    z.fsys,
			archive: z.path,
			path:    f.Name,
			inode:   fs.GenerateDynamicInode(z.inode, name),
			size:    f.UncompressedSize64,
			mtime:   f.Modified,
		}

		if f.UncompressedSize64 <= z.fsys.Options.StreamingThreshold.Load() {
			return &zipInMemoryFileNode{base}, nil
		}

		return &zipDiskStreamFileNode{base}, nil
	}

	return nil, toFuseErr(syscall.ENOENT)
}

func (z *zipDirNode) readDirAllNested(_ context.Context) ([]fuse.Dirent, error) {
	m := newZipMetric(z.fsys, false)
	defer m.Done()

	resp := []fuse.Dirent{}
	seen := map[string]bool{}

	zr, err := z.fsys.fdcache.Archive(z.path)
	if err != nil {
		z.fsys.rbuf.Printf("%q->ReadDirAll: ZIP error: %v\n", z.path, err)

		return nil, z.fsys.countError(toFuseErr(syscall.EINVAL))
	}
	defer zr.Release() //nolint:errcheck

	for i, f := range zr.File {
		normalizedPath := zipEntryNormalize(i, f, m.fsys.Options.ForceUnicode)

		// Prefix is already normalized, needs checking against that:
		if !strings.HasPrefix(normalizedPath, z.prefix) {
			continue
		}

		relPath := strings.TrimPrefix(normalizedPath, z.prefix)
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
				Inode: fs.GenerateDynamicInode(z.inode, name),
			})
		} else { // Can be explicit or implicit (dir/, dir/file.txt):
			resp = append(resp, fuse.Dirent{
				Name:  name,
				Type:  fuse.DT_Dir,
				Inode: fs.GenerateDynamicInode(z.inode, name),
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
	m := newZipMetric(z.fsys, false)
	defer m.Done()

	zr, err := z.fsys.fdcache.Archive(z.path)
	if err != nil {
		z.fsys.rbuf.Printf("%q->Lookup->%q: ZIP error: %v\n", z.path, name, err)

		return nil, z.fsys.countError(toFuseErr(syscall.EINVAL))
	}
	defer zr.Release() //nolint:errcheck

	fullPath := z.prefix + name

	for i, f := range zr.File {
		normalizedPath := zipEntryNormalize(i, f, m.fsys.Options.ForceUnicode)

		// Dirent is already normalized, needs checking against that:
		if normalizedPath == fullPath && !isDir(f, normalizedPath) {
			base := &zipBaseFileNode{
				fsys:    z.fsys,
				archive: z.path,
				path:    f.Name,
				inode:   fs.GenerateDynamicInode(z.inode, name),
				size:    f.UncompressedSize64,
				mtime:   f.Modified,
			}

			if f.UncompressedSize64 <= z.fsys.Options.StreamingThreshold.Load() {
				return &zipInMemoryFileNode{base}, nil
			}

			return &zipDiskStreamFileNode{base}, nil
		}

		// A directory can be explicit or implicit (dir/, dir/file.txt). So in
		// order to keep things deterministic and to account for any implicit
		// directories, we assign the modified time of the archive itself for
		// [zipDirNode] of subdirectories within archives, for the time being.
		if strings.HasPrefix(normalizedPath, fullPath+"/") {
			return &zipDirNode{
				fsys:   z.fsys,
				path:   z.path,
				prefix: fullPath + "/",
				inode:  fs.GenerateDynamicInode(z.inode, name),
				mtime:  z.mtime,
			}, nil
		}
	}

	return nil, toFuseErr(syscall.ENOENT)
}
