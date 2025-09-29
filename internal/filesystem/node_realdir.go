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
	"github.com/desertwitch/zipfuse/internal/logging"
)

var (
	_ fs.Node               = (*realDirNode)(nil)
	_ fs.HandleReadDirAller = (*realDirNode)(nil)
	_ fs.NodeStringLookuper = (*realDirNode)(nil)
)

// realDirNode is an actual regular directory within the mirrored filesystem.
// It is presented also as a regular directory within our filesystem, however
// only contained regular directories and ZIP archives are processed further.
type realDirNode struct {
	Inode    uint64    // Inode within our filesystem.
	Path     string    // Path of the actual regular directory.
	Modified time.Time // Modified time of the actual regular directory.
}

func (d *realDirNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | dirBasePerm
	a.Inode = d.Inode

	a.Atime = d.Modified
	a.Ctime = d.Modified
	a.Mtime = d.Modified

	return nil
}

func (d *realDirNode) ReadDirAll(_ context.Context) ([]fuse.Dirent, error) {
	seen := make(map[string]bool)
	resp := make([]fuse.Dirent, 0)

	entries, err := os.ReadDir(d.Path)
	if err != nil {
		logging.Printf("Error: %q->ReadDirAll: %v\n", d.Path, err)

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
			Inode: fs.GenerateDynamicInode(d.Inode, name),
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
			Inode: fs.GenerateDynamicInode(d.Inode, name),
		})
	}

	slices.SortFunc(resp, func(a, b fuse.Dirent) int {
		return strings.Compare(a.Name, b.Name)
	})

	return resp, nil
}

func (d *realDirNode) Lookup(_ context.Context, name string) (fs.Node, error) {
	path := filepath.Join(d.Path, name)

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return &realDirNode{
			Path:     path,
			Modified: info.ModTime(),
			Inode:    fs.GenerateDynamicInode(d.Inode, name),
		}, nil
	}

	zipPath := path + ".zip"
	if info, err := os.Stat(zipPath); err == nil && !info.IsDir() {
		return &zipDirNode{
			Path:     zipPath,
			Modified: info.ModTime(),
			Inode:    fs.GenerateDynamicInode(d.Inode, name),
		}, nil
	}

	return nil, fuse.ToErrno(syscall.ENOENT)
}
