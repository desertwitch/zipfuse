package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

var errItemValueWasNil = errors.New("cache returned item or value was nil")

// zipReaderCache is a [expirable.LRU] cache for [zipReader] pointers.
// It allows reusing opened ZIP files until TTL- or capacity-based eviction.
type zipReaderCache struct {
	sync.Mutex

	fsys  *FS
	cache *ttlcache.Cache[string, *zipReader]
}

// newZipReaderCache establishes a new [zipReaderCache] for a [FS].
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

// Archive returns a [zipReader] from the cache (adding a new one if needed).
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

	// We need to lock here to prevent races with the eviction callback.
	// In high pressure situations, entries could get capacity-evicted
	// before reaching Acquire(), despite the thread-safe library call.
	c.Lock()

	var err error
	item, ok := c.cache.GetOrSetFunc(archive, func() *zipReader {
		rc, zerr := newZipReader(c.fsys, archive)
		if zerr != nil {
			err = zerr
		}

		return rc
	})
	if err != nil {
		c.Unlock()

		return nil, fmt.Errorf("ZIP failure: %w", err)
	}
	if item == nil || item.Value() == nil {
		c.Unlock()

		return nil, errItemValueWasNil
	}
	zr := item.Value()
	zr.Acquire() // Cache holds one ref, add another for caller.

	c.Unlock()

	if ok {
		c.fsys.Metrics.TotalFDCacheHits.Add(1)
	} else {
		c.fsys.Metrics.TotalFDCacheMisses.Add(1)
	}

	return zr, nil
}

// Entry returns a [zipFileReader] for a specific "path" within a ZIP "archive",
// fetching from the cache the [zipReader] (or adding a new one if needed). The
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
