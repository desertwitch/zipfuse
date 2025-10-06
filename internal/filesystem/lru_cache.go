package filesystem

import (
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

// zipReaderCache is a [expirable.LRU] cache for [zipReader] pointers.
// It allows reusing opened ZIP files until TTL- or capacity-based eviction.
type zipReaderCache struct {
	sync.Mutex

	fsys  *FS
	cache *expirable.LRU[string, *zipReader]
}

// newZipReaderCache establishes a new [zipReaderCache] for a [FS].
func newZipReaderCache(fs *FS, size int, ttl time.Duration) *zipReaderCache {
	c := &zipReaderCache{fsys: fs}

	c.cache = expirable.NewLRU(size, func(_ string, zr *zipReader) {
		_ = zr.Release()
	}, ttl)

	return c
}

// Archive returns a [zipReader] from the cache (adding a new one if needed).
// The [zipReader] needs to be Release()d after use, ensure that this is called.
func (c *zipReaderCache) Archive(archive string) (*zipReader, error) {
	if c.fsys.Options.CacheDisabled.Load() {
		zr, err := newZipReader(c.fsys, archive)
		if err != nil {
			return nil, fuse.ToErrno(syscall.EINVAL)
		}

		// No need to Acquire() here, newZipFileReader() returns with a
		// caller ref (which would be for the cache), which we transfer
		// to our caller here instead (for lack of cache being enabled).
		return zr, nil
	}

	// While both the cache library and the [zipReader] are thread-safe,
	// the cache library does not evict overwritten entries via callback.
	// For this reason, FD that are overwritten are never Release()d and
	// just get garbage collected eventually, leading to metrics issues.
	// Locking prevents two [zipReader] racing for insertion into cache.
	c.Lock()
	defer c.Unlock()

	zr, ok := c.cache.Get(archive)
	if !ok {
		var err error

		zr, err = newZipReader(c.fsys, archive)
		if err != nil {
			return nil, fuse.ToErrno(syscall.EINVAL)
		}

		c.cache.Add(archive, zr)
		c.fsys.Metrics.TotalLruMisses.Add(1)
	} else {
		c.fsys.Metrics.TotalLruHits.Add(1)
	}

	// Cache holds one reference, add another for the caller.
	zr.Acquire()

	return zr, nil
}

// Entry returns a [zipFileReader] for a specific "path" within a ZIP "archive",
// fetching from the cache the [zipReader] (or adding a new one if needed). The
// underlying [zipReader] is also returned and needs to be Release()d after use.
func (c *zipReaderCache) Entry(archive, path string) (*zipReader, *zipFileReader, error) {
	if c.fsys.Options.CacheDisabled.Load() {
		m := newZipMetric(c.fsys, false)
		defer m.Done()

		zr, err := newZipReader(c.fsys, archive)
		if err != nil {
			return nil, nil, fuse.ToErrno(syscall.EINVAL)
		}

		for _, f := range zr.File {
			if f.Name == path {
				fr, err := newZipFileReader(c.fsys, f)
				if err != nil {
					return nil, nil, fuse.ToErrno(syscall.EINVAL)
				}

				// No need to Acquire() here, newZipFileReader() returns with a
				// caller ref (which would be for the cache), which we transfer
				// to our caller here instead (for lack of cache being enabled).
				return zr, fr, nil
			}
		}

		return nil, nil, fuse.ToErrno(syscall.ENOENT)
	}

	// While both the cache library and the [zipReader] are thread-safe,
	// the cache library does not evict overwritten entries via callback.
	// For this reason, FD that are overwritten are never Release()d and
	// just get garbage collected eventually, leading to metrics issues.
	// Locking prevents two [zipReader] racing for insertion into cache.
	c.Lock()
	defer c.Unlock()

	m := newZipMetric(c.fsys, false)
	defer m.Done()

	zr, ok := c.cache.Get(archive)
	if !ok {
		var err error

		zr, err = newZipReader(c.fsys, archive)
		if err != nil {
			return nil, nil, fuse.ToErrno(syscall.EINVAL)
		}

		c.cache.Add(archive, zr)
		c.fsys.Metrics.TotalLruMisses.Add(1)
	} else {
		c.fsys.Metrics.TotalLruHits.Add(1)
	}

	for _, f := range zr.File {
		if f.Name == path {
			fr, err := newZipFileReader(c.fsys, f)
			if err != nil {
				return nil, nil, fuse.ToErrno(syscall.EINVAL)
			}

			// Cache holds one reference, add another for the caller.
			zr.Acquire()

			return zr, fr, nil
		}
	}

	return nil, nil, fuse.ToErrno(syscall.ENOENT)
}
