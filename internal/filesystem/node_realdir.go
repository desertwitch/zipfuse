package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var (
	_ fs.Node               = (*realDirNode)(nil)
	_ fs.HandleReadDirAller = (*realDirNode)(nil)
	_ fs.NodeStringLookuper = (*realDirNode)(nil)
)

// realDirNode is an actual regular directory of the mirrored filesystem.
// It is presented also as a regular directory within our filesystem, however
// only contained regular directories and ZIP archives are processed further.
type realDirNode struct {
	fsys  *FS       // Pointer to our filesystem.
	inode uint64    // Inode within our filesystem.
	path  string    // Path of the underlying regular directory.
	mtime time.Time // Modified time of the underlying regular directory.
}

func (d *realDirNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | dirBasePerm
	a.Inode = d.inode

	a.Atime = d.mtime
	a.Ctime = d.mtime
	a.Mtime = d.mtime

	return nil
}

func (d *realDirNode) ReadDirAll(_ context.Context) ([]fuse.Dirent, error) {
	seen := make(map[string]bool)
	resp := make([]fuse.Dirent, 0)

	entries, err := os.ReadDir(d.path)
	if err != nil {
		d.fsys.rbuf.Printf("Error: %q->ReadDirAll: %v\n", d.path, err)

		return nil, toFuseErr(err)
	}

	dirs := make([]os.DirEntry, 0)
	zips := make([]os.DirEntry, 0)

	for _, e := range entries {
		switch {
		case e.IsDir():
			dirs = append(dirs, e)
		case strings.HasSuffix(e.Name(), ".zip"):
			zips = append(zips, e)
		default:
			continue
		}
	}

	for _, de := range dirs {
		name := de.Name()

		if seen[name] {
			continue
		}
		seen[name] = true

		resp = append(resp, fuse.Dirent{
			Name:  name,
			Type:  fuse.DT_Dir,
			Inode: fs.GenerateDynamicInode(d.inode, name),
		})
	}

	for _, ze := range zips {
		name := strings.TrimSuffix(ze.Name(), ".zip")

		if seen[name] {
			continue
		}
		seen[name] = true

		resp = append(resp, fuse.Dirent{
			Name:  name,
			Type:  fuse.DT_Dir,
			Inode: fs.GenerateDynamicInode(d.inode, name),
		})
	}

	slices.SortFunc(resp, func(a, b fuse.Dirent) int {
		return strings.Compare(a.Name, b.Name)
	})

	return resp, nil
}

func (d *realDirNode) Lookup(_ context.Context, name string) (fs.Node, error) {
	path := filepath.Join(d.path, name)

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return &realDirNode{
			fsys:  d.fsys,
			path:  path,
			mtime: info.ModTime(),
			inode: fs.GenerateDynamicInode(d.inode, name),
		}, nil
	}

	zipPath := path + ".zip"
	if info, err := os.Stat(zipPath); err == nil && !info.IsDir() {
		return &zipDirNode{
			fsys:  d.fsys,
			path:  zipPath,
			mtime: info.ModTime(),
			inode: fs.GenerateDynamicInode(d.inode, name),
		}, nil
	}

	return nil, toFuseErr(syscall.ENOENT)
}
