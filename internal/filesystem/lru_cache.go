package filesystem

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// zipReaderCache implements a [ttlcache.Cache] for [zipReader] pointers.
// It allows reusing opened ZIP files until TTL- or capacity-based eviction.
// With [Options.FDCacheBypass] enabled, it facilitates direct FD pass-through.
type zipReaderCache struct {
	sync.Mutex

	fsys  *FS
	cache *ttlcache.Cache[string, *zipReader]
}

// newZipReaderCache establishes a new [zipReaderCache] for a [FS].
// Once done with the cache, ensure calling HaltAndPurge() and Destroy().
func newZipReaderCache(fs *FS, size int, ttl time.Duration) *zipReaderCache {
	c := &zipReaderCache{fsys: fs}

	c.cache = ttlcache.New(
		ttlcache.WithTTL[string, *zipReader](ttl),
		ttlcache.WithCapacity[string, *zipReader](uint64(size)),
	)

	c.cache.OnEviction(func(_ context.Context, _ ttlcache.EvictionReason, item *ttlcache.Item[string, *zipReader]) {
		if v := item.Value(); v != nil {
			// We need to lock here to prevent races with Archive().
			c.Lock()
			defer c.Unlock()

			_ = v.Release()
		}
	})

	go c.cache.Start()

	return c
}

// Archive returns a [zipReader] from cache or direct (when uncached/on bypass).
// The [zipReader] needs to be Release()d after use, ensure that this is called.
func (c *zipReaderCache) Archive(archive string) (*zipReader, error) {
	if c.fsys.Options.FDCacheBypass.Load() {
		zr, err := newZipReader(c.fsys, archive)
		if err != nil {
			return nil, fmt.Errorf("ZIP failure: %w", err)
		}

		// No need to Acquire() here, newZipReader() returns with a
		// caller ref (which would be for the cache), which we transfer
		// to our caller here instead (for lack of cache being enabled).
		return zr, nil
	}

	c.Lock()
	if item := c.cache.Get(archive); item != nil && item.Value() != nil {
		existing := item.Value()
		existing.Acquire() // for caller
		c.fsys.Metrics.TotalFDCacheHits.Add(1)
		c.Unlock()

		return existing, nil
	}
	c.Unlock()

	// Outside of the lock, as it may block on the FD semaphore.
	zr, err := newZipReader(c.fsys, archive)
	if err != nil {
		return nil, fmt.Errorf("ZIP failure: %w", err)
	}

	c.Lock()
	defer c.Unlock()

	if item := c.cache.Get(archive); item != nil && item.Value() != nil {
		// Another call beat us to inserting the item into the cache.
		_ = zr.Release()         // release our ref (= closes our creation)
		existing := item.Value() // use the existing cached reader instead
		existing.Acquire()       // for caller
		c.fsys.Metrics.TotalFDCacheHits.Add(1)

		return existing, nil
	}

	c.cache.Set(archive, zr, ttlcache.DefaultTTL)
	zr.Acquire() // for caller
	c.fsys.Metrics.TotalFDCacheMisses.Add(1)

	return zr, nil
}

// Entry returns a [zipFileReader] for a specific "path" within a ZIP "archive",
// fetching from cache or direct (when uncached/on bypass) the [zipReader]. The
// underlying [zipReader] is also returned and needs to be Release()d after use.
func (c *zipReaderCache) Entry(archive, path string) (*zipReader, *zipFileReader, error) {
	m := newZipMetric(c.fsys, false)
	defer m.Done()

	if c.fsys.Options.FDCacheBypass.Load() {
		zr, err := newZipReader(c.fsys, archive)
		if err != nil {
			return nil, nil, fmt.Errorf("ZIP failure: %w", err)
		}

		for _, f := range zr.File {
			if f.Name == path {
				fr, err := newZipFileReader(c.fsys, f)
				if err != nil {
					return nil, nil, fmt.Errorf("ZIP file failure: %w", err)
				}

				// No need to Acquire() here, newZipReader() returns with a
				// caller ref (which would be for the cache), which we transfer
				// to our caller here instead (for lack of cache being enabled).
				return zr, fr, nil
			}
		}

		return nil, nil, fmt.Errorf("%w: %s", os.ErrNotExist, path)
	}

	// We do not need to lock here, as Archive() internally locks and
	// returns [zipReader] with an Acquire()d ref for us (as the caller).
	zr, err := c.Archive(archive)
	if err != nil {
		return nil, nil, err
	}

	for _, f := range zr.File {
		if f.Name == path {
			fr, err := newZipFileReader(c.fsys, f)
			if err != nil {
				_ = zr.Release() // release our ref

				return nil, nil, fmt.Errorf("ZIP file failure: %w", err)
			}

			// No need to Acquire() here, Archive() returns with a caller ref,
			// which was for us (as caller) and transfer to our caller instead.
			return zr, fr, nil
		}
	}

	_ = zr.Release() // release our ref

	return nil, nil, fmt.Errorf("%w: %s", os.ErrNotExist, path)
}

// HaltAndPurge prepares the file descriptor cache for unmount,
// turning on FD cache bypass and deleting all items from the cache.
// It takes an error channel for checking if the upstream unmounting
// has failed, in which case it will restore the previous FD cache bypass
// setting and resume the cache to its normal operation (as user-configured).
func (c *zipReaderCache) HaltAndPurge(errs <-chan error) {
	v := c.fsys.Options.FDCacheBypass.Load()

	c.fsys.Options.FDCacheBypass.Store(true)
	c.cache.DeleteAll()

	go func() {
		if err := <-errs; err != nil {
			c.fsys.Options.FDCacheBypass.Store(v)
		}
	}()
}

// Destroy stops the goroutines associated with the file descriptor cache.
// The cache cannot be re-used after the calling of this no-return function.
func (c *zipReaderCache) Destroy() {
	c.cache.Stop()
}
