package filesystem

import (
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

type zipReaderCache struct {
	cache *expirable.LRU[string, *zipReader]
}

func newZipReaderCache(size int, ttl time.Duration) *zipReaderCache {
	c := &zipReaderCache{}

	c.cache = expirable.NewLRU(size, func(_ string, zr *zipReader) {
		zr.Release()
	}, ttl)

	return c
}

func (c *zipReaderCache) Archive(archive string) (*zipReader, error) {
	zr, ok := c.cache.Get(archive)
	if !ok {
		var err error

		zr, err = newZipReader(archive)
		if err != nil {
			return nil, fuse.ToErrno(syscall.EINVAL)
		}

		zr.refCount.Add(1) // for cache
		c.cache.Add(archive, zr)
	}

	zr.refCount.Add(1) // for caller

	return zr, nil
}

func (c *zipReaderCache) Entry(archive, path string) (*zipReader, *zipFileReader, error) {
	zr, ok := c.cache.Get(archive)
	if !ok {
		var err error

		zr, err = newZipReader(archive)
		if err != nil {
			return nil, nil, fuse.ToErrno(syscall.EINVAL)
		}

		zr.refCount.Add(1) // for cache
		c.cache.Add(archive, zr)
	}

	m := zipMetricStart() // metadata read
	defer zipMetricEnd(m)

	for _, f := range zr.File {
		if f.Name == path {
			fr, err := newZipFileReader(f)
			if err != nil {
				return nil, nil, fuse.ToErrno(syscall.EINVAL)
			}

			zr.refCount.Add(1) // for caller

			return zr, fr, nil
		}
	}

	return nil, nil, fuse.ToErrno(syscall.ENOENT)
}
