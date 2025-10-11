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
	fileBasePerm = 0o444 // RO
	dirBasePerm  = 0o555 // RO

	defaultFDCacheBypass      = false
	defaultFDCacheSize        = 256
	defaultFDCacheTTL         = 60 * time.Second
	defaultFDLimit            = 512
	defaultFlatMode           = false
	defaultForceUnicode       = true
	defaultMustCRC32          = false
	defaultStreamingThreshold = 1 * 1024 * 1024 // 1MiB
	defaultStreamPoolSize     = 128 * 1024      // 128KiB
	defaultStrictCache        = false
)

var (
	_ fs.FS               = (*FS)(nil)
	_ fs.FSInodeGenerator = (*FS)(nil)

	// errInvalidArgument is for an invalid constructor argument.
	errInvalidArgument = errors.New("invalid argument")
)

// Options contains all settings for the operation of the filesystem.
// All non-atomic fields can no longer be modified at runtime (once mounted).
type Options struct {
	// FDLimit is the absolute limit on open file descriptors at any time.
	// It must be larger than [Options.FDCacheSize], but beware the OS limits.
	FDLimit int

	// FDCacheBypass circumvents the cache for ZIP file descriptors.
	// When enabled at runtime, in-flight descriptors will close after TTL.
	FDCacheBypass atomic.Bool

	// FDCacheSize is the size of the cache for ZIP file descriptors.
	// It must be smaller than [Options.FDLimit], otherwise may cause deadlock.
	FDCacheSize int

	// FDCacheTTL is the time-to-live for ZIP file descriptors in the cache.
	// If a file descriptor is no longer in use, it will be evicted after TTL.
	FDCacheTTL time.Duration

	// StreamPoolSize is the buffer size for the streamed read buffer pool.
	// This value multiplies with concurrency; a common read size makes sense,
	// in particular one that aligns well with page size/FUSE readahead setting.
	StreamPoolSize int

	// StrictCache controls if ZIP files/contents should be treated as
	// immutable for caching decisions (and invalidation of cached content).
	// If disabled, ZIPs are considered immutable (non-changing) for caching.
	StrictCache bool

	// ForceUnicode controls if unicode should be enforced for all ZIP paths.
	// Beware: If disabled, non-compliant ZIPs may end up with garbled paths.
	ForceUnicode bool

	// FlatMode controls if ZIP-contained subdirectories and files
	// should be flattened with [flatEntryName] into shallow directories.
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
		FDCacheSize:    defaultFDCacheSize,
		FDCacheTTL:     defaultFDCacheTTL,
		FDLimit:        defaultFDLimit,
		FlatMode:       defaultFlatMode,
		ForceUnicode:   defaultForceUnicode,
		StreamPoolSize: defaultStreamPoolSize,
		StrictCache:    defaultStrictCache,
	}
	opts.FDCacheBypass.Store(defaultFDCacheBypass)
	opts.MustCRC32.Store(defaultMustCRC32)
	opts.StreamingThreshold.Store(defaultStreamingThreshold)

	return opts
}

// Metrics contains all metrics which are collected within the filesystem.
type Metrics struct {
	// Errors is the amount of errors that have occurred.
	Errors atomic.Int64

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

	// TotalFDCacheHits is the amount of cache-hits for the FD cache.
	TotalFDCacheHits atomic.Int64

	// TotalFDCacheMisses is the amount of cache-misses for the FD cache
	TotalFDCacheMisses atomic.Int64

	// TotalStreamPoolHits is the amount of times pool buffers were used.
	TotalStreamPoolHits atomic.Int64

	// TotalStreamPoolMisses is the amount of times buffers were allocated.
	TotalStreamPoolMisses atomic.Int64

	// TotalStreamPoolHitBytes is the bytes actually used from pool buffers.
	TotalStreamPoolHitBytes atomic.Int64

	// TotalStreamPoolMissBytes is the bytes newly allocated outside the pool.
	TotalStreamPoolMissBytes atomic.Int64
}

// FS is the core implementation of the filesystem.
type FS struct {
	RootDir   string
	MountTime time.Time

	Options *Options
	Metrics *Metrics

	fdlimit chan struct{}
	fdcache *zipReaderCache
	bufpool sync.Pool

	rbuf *logging.RingBuffer
}

// NewFS returns a pointer to a new [FS].
// You must call PrepareUnmount() before unmount, Destroy() after unmount.
func NewFS(rootDir string, opts *Options, rbuf *logging.RingBuffer) (*FS, error) {
	if rbuf == nil {
		return nil, fmt.Errorf("%w: need a non-nil rbuf", errInvalidArgument)
	}
	if rootDir == "" {
		return nil, fmt.Errorf("%w: need a non-empty rootDir", errInvalidArgument)
	}
	if _, err := os.Stat(rootDir); err != nil {
		return nil, fmt.Errorf("%w: failed to stat rootDir: %w", errInvalidArgument, err)
	}
	if opts == nil {
		opts = DefaultOptions()
	}
	if opts.FDLimit <= opts.FDCacheSize {
		return nil, fmt.Errorf("%w: fd limit cannot be <= fd cache size (%d/%d)",
			errInvalidArgument, opts.FDLimit, opts.FDCacheSize)
	}

	fsys := &FS{
		RootDir: rootDir,
		Options: opts,
		Metrics: &Metrics{},
		rbuf:    rbuf,
	}

	fsys.fdlimit = make(chan struct{}, opts.FDLimit)
	fsys.fdcache = newZipReaderCache(fsys, opts.FDCacheSize, opts.FDCacheTTL)

	fsys.bufpool = sync.Pool{
		New: func() any {
			b := make([]byte, opts.StreamPoolSize)

			return &b
		},
	}

	return fsys, nil
}

// PrepareUnmount does pre-unmount FS cleanup.
// It takes an error channel for checking if unmount was successful.
// In case of an unmount failure, it restores the FS to working state.
func (fsys *FS) PrepareUnmount(unmountErr <-chan error) {
	fsys.fdcache.HaltAndPurge(unmountErr)
}

// Destroy does post-unmount FS cleanup and blocks until done.
// You should not use the filesystem after calling of this function.
func (fsys *FS) Destroy() {
	fsys.fdcache.Destroy()
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
// All paths provided to the callback will be relative to the filesystem Root() node.
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

// countError adds to the error count within the filesystem.
// It returns the received error back to the caller unchanged.
// This allows for convenient use of the method in return calls.
func (fsys *FS) countError(err error) error {
	fsys.Metrics.Errors.Add(1)

	return err
}
