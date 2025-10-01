// Package filesystem implements the filesystem.
package filesystem

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

const (
	fileBasePerm = 0o444 // RO
	dirBasePerm  = 0o555 // RO
	hashDigits   = 8     // [flatEntryName]
)

var (
	_ fs.FS               = (*FS)(nil)
	_ fs.FSInodeGenerator = (*FS)(nil)

	// Options is a pointer to the filesystem [FSOptions].
	// As there is ever only one filesystem per program, keeping it as global
	// variable is an acceptable trade-off over passing around [FS] pointers.
	//
	// Beware that any contained non-atomic variables should not be modified
	// after the filesystem was mounted and are also not considered thread-safe.
	Options = &FSOptions{}

	// Metrics is a pointer to the filesystem [FSMetrics].
	// As there is ever only one filesystem per program, keeping it as global
	// variable is an acceptable trade-off over passing around [FS] pointers.
	//
	// Beware that any contained non-atomic variables should not be modified
	// after the filesystem was mounted and are also not considered thread-safe.
	Metrics = &FSMetrics{}
)

// FSOptions contains all settings for the operation of the filesystem.
type FSOptions struct {
	// FlatMode controls if ZIP-contained subdirectories and files
	// should be flattened with [flatEntryName] for shallow directories.
	// This variable should no longer be modified when the FS is mounted.
	FlatMode bool

	// MustCRC32 controls if ZIP-contained uncompressed files must still run
	// through the integrity verification algorithm (CRC32), which is slower.
	MustCRC32 atomic.Bool

	// StreamingThreshold when files are no longer fully loaded into RAM,
	// but rather streamed in chunks (amount as requested by the kernel).
	StreamingThreshold atomic.Uint64
}

// FSMetrics contains all metrics which are collected within the filesystem.
type FSMetrics struct {
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
}

// FS is the core implementation of the filesystem.
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

// WalkFunc gets called on each visited [fs.Node] as part of a [FS.Walk].
// Do note that as the root directory is synthetic, the [fuse.Dirent] will be nil.
// All paths provided to the callback will be relative to the filesystem root dir.
type WalkFunc func(path string, dirent *fuse.Dirent, node fs.Node, attr fuse.Attr) error

// Walk constructs and walks the [FS] in-memory, calling walkFn on each visited [fs.Node].
func (zpfs *FS) Walk(ctx context.Context, walkFn WalkFunc) error {
	root, err := zpfs.Root()
	if err != nil {
		return fmt.Errorf("failed to get fs root: %w", err)
	}

	return zpfs.walkNode(ctx, "/", nil, root, walkFn)
}

// walkNode handles walking of a [fs.Node] within the [FS].
func (zpfs *FS) walkNode(ctx context.Context, path string, dirent *fuse.Dirent, node fs.Node, walkFn WalkFunc) error {
	var attr fuse.Attr

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error: %w", err)
	}

	if err := node.Attr(ctx, &attr); err != nil {
		return fmt.Errorf("attr error at %q: %w", path, err)
	}

	if err := walkFn(path, dirent, node, attr); err != nil {
		return fmt.Errorf("walkFn error at %q: %w", path, err)
	}

	if readDirNode, ok := node.(fs.HandleReadDirAller); ok {
		dirents, err := readDirNode.ReadDirAll(ctx)
		if err != nil {
			return fmt.Errorf("readdirall error at %q: %w", path, err)
		}

		if lookupNode, ok := node.(fs.NodeStringLookuper); ok {
			for _, de := range dirents {
				childPath := path
				if path != "/" {
					childPath += "/"
				}
				childPath += de.Name

				childNode, err := lookupNode.Lookup(ctx, de.Name)
				if err != nil {
					return fmt.Errorf("lookup error for %q at %q: %w", de.Name, path, err)
				}

				if err := zpfs.walkNode(ctx, childPath, &de, childNode, walkFn); err != nil {
					return fmt.Errorf("walkFn error at %q: %w", childPath, err)
				}
			}
		}
	}

	return nil
}
