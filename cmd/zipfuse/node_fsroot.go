package main

import (
	"time"

	"bazil.org/fuse/fs"
)

var (
	_ fs.FS               = (*zipFS)(nil)
	_ fs.FSInodeGenerator = (*zipFS)(nil)
)

type zipFS struct {
	RootDir string
}

func (zfs *zipFS) Root() (fs.Node, error) {
	return &realDirNode{
		Inode:    1,
		Path:     zfs.RootDir,
		Modified: time.Now(),
	}, nil
}

func (zfs *zipFS) GenerateInode(_ uint64, _ string) uint64 {
	// zipfuse handles inodes itself, so a dynamic inode generation within the
	// FUSE library (being the fallback on encountering zero inodes) is a core
	// violation of this very design principle. So a panic should reveal where
	// internal inode handling does not produce an inode and needs fixing up.
	panic("unhandled zero inode triggered an illegal dynamic generation")
}
