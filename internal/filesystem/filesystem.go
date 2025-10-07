// Package filesystem implements the filesystem.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/logging"
)

const (
	fileBasePerm      = 0o444 // RO
	dirBasePerm       = 0o555 // RO
	flattenHashDigits = 8     // [flatEntryName]

	defaultCacheSize          = 60
	defaultCacheTTL           = 60 * time.Second
	defaultFlatMode           = false
	defaultMustCRC32          = false
	defaultPoolBufferSize     = 128 * 1024       // 10KiB
	defaultStreamingThreshold = 10 * 1024 * 1024 // 10MiB
)

var (
	_ fs.FS               = (*FS)(nil)
	_ fs.FSInodeGenerator = (*FS)(nil)

	errMissingArgument = errors.New("missing argument")
)

// Options contains all settings for the operation of the filesystem.
// All non-atomic fields can no longer be modified at runtime (once mounted).
type Options struct {
	// FDCacheBypass circumvents the LRU cache for ZIP file descriptors.
	// When enabled at runtime, in-flight descriptors will close after TTL.
	FDCacheBypass atomic.Bool

	// FDCacheSize is the size of the LRU cache for ZIP file descriptors.
	// Beware the operating system file descriptor limit when changing this.
	FDCacheSize int

	// FDCacheTTL is the time-to-live for each ZIP file descriptor in the LRU.
	// If a file descriptor is no longer used, it will be evicted after TTL.
	FDCacheTTL time.Duration

	// PoolBufferSize is the buffer size for the file read buffer pool.
	PoolBufferSize int

	// FlatMode controls if ZIP-contained subdirectories and files
	// should be flattened with [flatEntryName] for shallow directories.
	FlatMode bool

	// MustCRC32 controls if ZIP-contained uncompressed files must still run
	// through the integrity verification algorithm (CRC32), which is slower.
	MustCRC32 atomic.Bool

	// StreamingThreshold when files are no longer fully loaded into RAM,
	// but rather streamed in chunks (amount as requested by the kernel).
	StreamingThreshold atomic.Uint64
}

// DefaultOptions returns a pointer to [Options] with the default values.
func DefaultOptions() *Options {
	opts := &Options{
		FDCacheSize:    defaultCacheSize,
		FDCacheTTL:     defaultCacheTTL,
		FlatMode:       defaultFlatMode,
		PoolBufferSize: defaultPoolBufferSize,
	}
	opts.FDCacheBypass.Store(false)
	opts.MustCRC32.Store(defaultMustCRC32)
	opts.StreamingThreshold.Store(defaultStreamingThreshold)

	return opts
}

// Metrics contains all metrics which are collected within the filesystem.
type Metrics struct {
	// OpenZips is the amount of currently open ZIP files.
	OpenZips atomic.Int64

	// TotalOpenedZips is the amount of opened ZIP files.
	TotalOpenedZips atomic.Int64

	// TotalClosedZips is the amount of closed ZIP files.
	TotalClosedZips atomic.Int64

	// TotalReopenedEntries is the amount of reopened ZIP entries (rewinds).
	TotalReopenedEntries atomic.Int64

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

	// TotalFDCacheHits is the amount of cache-hits for the LRU cache.
	TotalFDCacheHits atomic.Int64

	// TotalFDCacheMisses is the amount of cache-misses for the LRU cache
	TotalFDCacheMisses atomic.Int64
}

// FS is the core implementation of the filesystem.
type FS struct {
	RootDir string

	Options *Options
	Metrics *Metrics

	fdcache *zipReaderCache
	bufpool sync.Pool

	rbuf *logging.RingBuffer
}

// NewFS returns a pointer to a new [FS].
// You must call Cleanup() once all work is complete.
func NewFS(rootDir string, opts *Options, rbuf *logging.RingBuffer) (*FS, error) {
	if rbuf == nil {
		return nil, fmt.Errorf("%w: need a ring buffer", errMissingArgument)
	}
	if rootDir == "" {
		return nil, fmt.Errorf("%w: need a root dir", errMissingArgument)
	}
	if _, err := os.Stat(rootDir); err != nil {
		return nil, fmt.Errorf("failed to stat root dir: %w", err)
	}
	if opts == nil {
		opts = DefaultOptions()
	}

	fsys := &FS{
		RootDir: rootDir,
		Options: opts,
		Metrics: &Metrics{},
		rbuf:    rbuf,
	}
	fsys.fdcache = newZipReaderCache(fsys, opts.FDCacheSize, opts.FDCacheTTL)
	fsys.bufpool = sync.Pool{
		New: func() any {
			b := make([]byte, opts.PoolBufferSize)

			return &b
		},
	}

	return fsys, nil
}

// Cleanup does filesystem cleanup and blocks until done.
func (fsys *FS) Cleanup() {
	fsys.fdcache.cache.Stop()
}

// Root returns the entry-point [fs.Node] of the filesystem.
func (fsys *FS) Root() (fs.Node, error) {
	return &realDirNode{
		fsys:  fsys,
		inode: 1,
		path:  fsys.RootDir,
		mtime: time.Now(),
	}, nil
}

// GenerateInode implements [fs.FSInodeGenerator] to prevent dynamic
// inode generation by the fallback method inside of the FUSE library.
//
// [FS] handles inodes internally, so dynamic inode generation within the
// FUSE library (being the fallback on encountering zero inodes) is a core
// violation of this very design principle. Calls to this method will panic,
// revealing where internal inode handling does not produce the valid inode.
func (fsys *FS) GenerateInode(_ uint64, _ string) uint64 {
	panic("unhandled zero inode triggered an illegal dynamic generation")
}

// WalkFunc gets called on each visited [fs.Node] as part of a [FS.Walk].
// Do note that as the root directory is synthetic, the [fuse.Dirent] will be nil.
// All paths provided to the callback will be relative to the filesystem root dir.
type WalkFunc func(path string, dirent *fuse.Dirent, node fs.Node, attr fuse.Attr) error

// Walk constructs and walks the [FS] in-memory, calling walkFn on each visited [fs.Node].
func (fsys *FS) Walk(ctx context.Context, walkFn WalkFunc) error {
	root, err := fsys.Root()
	if err != nil {
		return fmt.Errorf("failed to get fs root: %w", err)
	}

	return fsys.walkNode(ctx, "/", nil, root, walkFn)
}

// walkNode handles walking of a [fs.Node] within the [FS].
func (fsys *FS) walkNode(ctx context.Context, path string, dirent *fuse.Dirent, node fs.Node, walkFn WalkFunc) error {
	var attr fuse.Attr

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error: %w", err)
	}

	if err := node.Attr(ctx, &attr); err != nil {
		return fmt.Errorf("attr error at %q: %w", path, err)
	}

	if err := walkFn(path, dirent, node, attr); err != nil {
		return fmt.Errorf("walkfn error at %q: %w", path, err)
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

				if err := fsys.walkNode(ctx, childPath, &de, childNode, walkFn); err != nil {
					return fmt.Errorf("walkfn error at %q: %w", childPath, err)
				}
			}
		}
	}

	return nil
}
