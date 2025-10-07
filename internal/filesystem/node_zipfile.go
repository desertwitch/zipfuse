package filesystem

import (
	"context"
	"errors"
	"io"
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var _ fs.Node = (*zipBaseFileNode)(nil)

// zipBaseFileNode is a file within a ZIP archive of the mirrored filesystem.
// It is presented as a regular file in our filesystem and unpacked on demand.
//
// To be embedded into either [zipInMemoryFileNode] or [zipDiskStreamFileNode],
// depending on [Options.StreamingThreshold] as set by arguments or at runtime.
type zipBaseFileNode struct {
	fsys    *FS       // Pointer to our filesystem.
	inode   uint64    // Inode within our filesystem.
	archive string    // Path of the underlying ZIP archive (= parent).
	path    string    // Path of the file inside the underlying ZIP file.
	size    uint64    // Size of the file inside the underlying ZIP file.
	mtime   time.Time // Modified time of the file inside the underlying ZIP file.
}

func (z *zipBaseFileNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = fileBasePerm
	a.Inode = z.inode

	a.Size = z.size

	a.Atime = z.mtime
	a.Ctime = z.mtime
	a.Mtime = z.mtime

	return nil
}

var (
	_ fs.Node            = (*zipInMemoryFileNode)(nil)
	_ fs.NodeOpener      = (*zipInMemoryFileNode)(nil)
	_ fs.HandleReadAller = (*zipInMemoryFileNode)(nil)
)

// zipInMemoryFileNode is a [zipBaseFileNode] that implements only the
// [fs.HandleReadAller] for one-shot reading the entire file into memory.
type zipInMemoryFileNode struct {
	*zipBaseFileNode
}

func (z *zipInMemoryFileNode) Open(_ context.Context, _ *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	// We consider a ZIP to be immutable if it exists, so we don't invalidate here.
	resp.Flags |= fuse.OpenKeepCache

	return z, nil
}

func (z *zipInMemoryFileNode) ReadAll(_ context.Context) ([]byte, error) {
	m := newZipMetric(z.fsys, true)
	defer m.Done()

	zr, fr, err := z.fsys.fdcache.Entry(z.archive, z.path)
	if err != nil {
		z.fsys.rbuf.Printf("Error: %q->ReadAll->%q: ZIP Error: %v\n", z.archive, z.path, err)

		return nil, z.fsys.fsError(toFuseErr(syscall.EINVAL))
	}
	defer zr.Release() //nolint:errcheck
	defer fr.Close()

	data, err := io.ReadAll(fr)
	if err != nil {
		z.fsys.rbuf.Printf("Error: %q->ReadAll->%q: IO Error: %v\n", z.archive, z.path, err)

		return nil, z.fsys.fsError(toFuseErr(syscall.EIO))
	}

	m.readBytes = int64(len(data))

	return data, nil
}

var (
	_ fs.Node       = (*zipDiskStreamFileNode)(nil)
	_ fs.NodeOpener = (*zipDiskStreamFileNode)(nil)
)

// zipDiskStreamFileNode is a [zipBaseFileNode] that opens to a
// [zipDiskStreamFileHandle] for streaming from a ZIP-contained file.
type zipDiskStreamFileNode struct {
	*zipBaseFileNode
}

func (z *zipDiskStreamFileNode) Open(_ context.Context, _ *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	zr, fr, err := z.fsys.fdcache.Entry(z.archive, z.path)
	if err != nil {
		z.fsys.rbuf.Printf("Error: %q->Open->%q: ZIP Error: %v\n", z.archive, z.path, err)

		return nil, z.fsys.fsError(toFuseErr(syscall.EINVAL))
	}

	// We consider a ZIP to be immutable if it exists, so we don't invalidate here.
	resp.Flags |= fuse.OpenKeepCache

	return &zipDiskStreamFileHandle{
		fsys:    z.fsys,
		archive: z.archive,
		path:    z.path,
		zr:      zr,
		fr:      fr,
		offset:  0,
	}, nil
}

var (
	_ fs.HandleReader   = (*zipDiskStreamFileHandle)(nil)
	_ fs.HandleReleaser = (*zipDiskStreamFileHandle)(nil)
)

// zipDiskStreamFileHandle is a [fs.Handle] returned when opening a
// [zipDiskStreamFileNode]. It implements [fs.HandleReader] to allow for
// reading bytes from a ZIP-contained file as part of a [fuse.ReadRequest].
// The implemented [fs.HandleReleaser] ensures appropriate cleanup afterwards.
type zipDiskStreamFileHandle struct {
	sync.Mutex

	fsys    *FS
	archive string
	path    string

	zr     *zipReader
	fr     *zipFileReader
	offset int64
}

func (h *zipDiskStreamFileHandle) Read(_ context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	h.Lock()
	defer h.Unlock()

	m := newZipMetric(h.fsys, true)
	defer m.Done()

	if req.Offset != h.offset {
		n, err := h.fr.ForwardTo(req.Offset)
		h.offset = n
		switch {
		case errors.Is(err, errNonSeekableRewind):
			f := h.fr.f      // Save first the [zip.File] for re-use
			_ = h.fr.Close() // Close now the failed [zipFileReader]

			// Reopening the entry will start with offset zero, so the
			// pseudo-seek should always succeed even if it's a rewind.
			// Re-use of the [zipReader] and [zip.File] saves overhead.
			rc, err := newZipFileReader(h.fsys, f)
			if err != nil {
				h.fsys.rbuf.Printf("Error: %q->Read->%q: ZIP Error: %v\n", h.archive, h.path, err)

				return h.fsys.fsError(toFuseErr(syscall.EINVAL))
			}
			h.fr = rc
			h.offset = 0
			h.fsys.Metrics.TotalReopenedEntries.Add(1)

			// Retry the forward... if it fails again, return the error.
			n, err = h.fr.ForwardTo(req.Offset)
			h.offset = n
			if err != nil {
				h.fsys.rbuf.Printf("Error: %q->Read->%q: Seek Error: %v\n", h.archive, h.path, err)

				return h.fsys.fsError(toFuseErr(syscall.EIO))
			}

		case err != nil:
			h.fsys.rbuf.Printf("Error: %q->Read->%q: Seek Error: %v\n", h.archive, h.path, err)

			return h.fsys.fsError(toFuseErr(syscall.EIO))
		}
	}

	rawBuf := h.fsys.bufpool.Get()
	pBuf, ok := rawBuf.(*[]byte)
	if !ok {
		panic("zipDiskStreamFileHandle: received unexpected type from bufPool")
	}
	buf := *pBuf

	if cap(buf) < req.Size {
		// Put back the pointer first, we won't use it.
		*pBuf = (*pBuf)[:h.fsys.Options.PoolBufferSize]
		h.fsys.bufpool.Put(pBuf)

		buf = make([]byte, req.Size) // will be GC'ed.
	} else {
		defer func() {
			*pBuf = (*pBuf)[:h.fsys.Options.PoolBufferSize]
			h.fsys.bufpool.Put(pBuf)
		}()
	}

	buf = buf[:req.Size]

	n, err := io.ReadFull(h.fr, buf)
	h.offset += int64(n)
	m.readBytes = int64(n)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		h.fsys.rbuf.Printf("Error: %q->Read->%q: IO Error: %v\n", h.archive, h.path, err)

		return h.fsys.fsError(toFuseErr(syscall.EIO))
	}

	// The kernel owns the data buffer, so we hand it a copy of ours here.
	resp.Data = append([]byte(nil), buf[:n]...)

	return nil
}

func (h *zipDiskStreamFileHandle) Release(_ context.Context, _ *fuse.ReleaseRequest) error {
	h.Lock()
	defer h.Unlock()

	if h.fr != nil {
		_ = h.fr.Close()
		h.fr = nil
	}
	if h.zr != nil {
		_ = h.zr.Release()
		h.zr = nil
	}

	return nil
}
