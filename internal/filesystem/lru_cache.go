package filesystem

import (
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type zipReaderCache struct {
	sync.Mutex

	fsys  *FS
	cache *expirable.LRU[string, *zipReader]
}

func newZipReaderCache(fs *FS, size int, ttl time.Duration) *zipReaderCache {
	c := &zipReaderCache{fsys: fs}

	c.cache = expirable.NewLRU(size, func(_ string, zr *zipReader) {
		_ = zr.Release()
	}, ttl)

	return c
}

func (c *zipReaderCache) Archive(archive string) (*zipReader, error) {
	if c.fsys.Options.CacheDisabled.Load() {
		zr, err := newZipReader(c.fsys, archive)
		if err != nil {
			return nil, fuse.ToErrno(syscall.EINVAL)
		}

		return zr, nil
	}

	// Cache must be enabled from here on downwards.
	// We need locking to avoid concurrent insertion of [zipReaders].

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
	}

	// Cache holds one reference, add another for the caller.
	zr.Acquire()

	return zr, nil
}

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

				return zr, fr, nil
			}
		}

		return nil, nil, fuse.ToErrno(syscall.ENOENT)
	}

	// Cache must be enabled from here on downwards.
	// We need locking to avoid concurrent insertion of [zipReaders].

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
