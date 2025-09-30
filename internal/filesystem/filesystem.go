// Package filesystem implements the FUSE filesystem.
package filesystem

import (
	"sync/atomic"
	"time"

	"bazil.org/fuse/fs"
)

const (
	fileBasePerm = 0o444 // RO
	dirBasePerm  = 0o555 // RO
	hashDigits   = 8     // for [flatEntryName]
)

var (
	_ fs.FS               = (*FS)(nil)
	_ fs.FSInodeGenerator = (*FS)(nil)

	// StreamingThreshold when files are no longer fully loaded into RAM,
	// but rather streamed in chunks (amount as requested by the kernel).
	StreamingThreshold atomic.Uint64

	// OpenZips is the amount of currently open ZIP files.
	OpenZips atomic.Int64

	// TotalOpenedZips is the amount of opened ZIP files.
	TotalOpenedZips atomic.Int64

	// TotalClosedZips is the amount of closed ZIP files.
	TotalClosedZips atomic.Int64

	// TotalMetadataReadTime is time spent reading metadata from ZIP files.
	TotalMetadataReadTime atomic.Int64

	// TotalMetadataReadCount is the amount of metadata reads from ZIP files.
	TotalMetadataReadCount atomic.Int64

	// TotalExtractTime is time spent extracting data from ZIP files.
	TotalExtractTime atomic.Int64

	// TotalExtractCount is the amount of extractions from ZIP files.
	TotalExtractCount atomic.Int64

	// TotalExtractBytes is the amount of bytes extracted from ZIP files.
	TotalExtractBytes atomic.Int64
)

// FS is the core implementation of the zipfuse filesystem.
type FS struct {
	RootDir string
}

// Root returns the topmost [fs.Node] of the filesystem.
func (zpfs *FS) Root() (fs.Node, error) {
	return &realDirNode{
		Inode:    1,
		Path:     zpfs.RootDir,
		Modified: time.Now(),
	}, nil
}

// GenerateInode implements [fs.FSInodeGenerator] to prevent dynamic
// inode generation by the fallback method inside of the FUSE library.
//
// [FS] handles inodes internally, so dynamic inode generation within the
// FUSE library (being the fallback on encountering zero inodes) is a core
// violation of this very design principle. Calls to this method will panic,
// revealing where internal inode handling does not produce the valid inode.
func (zpfs *FS) GenerateInode(_ uint64, _ string) uint64 {
	panic("unhandled zero inode triggered an illegal dynamic generation")
}
