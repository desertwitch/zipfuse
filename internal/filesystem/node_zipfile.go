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
	"github.com/desertwitch/zipfuse/internal/logging"
)

var _ fs.Node = (*zipBaseFileNode)(nil)

// zipBaseFileNode is a file within a ZIP archive of the mirrored filesystem.
// It is presented as a regular file in our filesystem and unpacked on demand.
//
// To be embedded into either [zipInMemoryFileNode] or [zipDiskStreamFileNode],
// depending on which [StreamingThreshold] was set by CLI argument or at runtime.
type zipBaseFileNode struct {
	Inode    uint64    // Inode within our filesystem.
	Archive  string    // Path of the underlying ZIP archive (= parent).
	Path     string    // Path of the file inside the underlying ZIP file.
	Size     uint64    // Size of the file inside the underlying ZIP file.
	Modified time.Time // Modified time of the file inside the underlying ZIP file.
}

func (z *zipBaseFileNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Mode = fileBasePerm
	a.Inode = z.Inode

	a.Size = z.Size

	a.Atime = z.Modified
	a.Ctime = z.Modified
	a.Mtime = z.Modified

	return nil
}

var (
	_ fs.Node            = (*zipInMemoryFileNode)(nil)
	_ fs.NodeOpener      = (*zipInMemoryFileNode)(nil)
	_ fs.HandleReadAller = (*zipInMemoryFileNode)(nil)
)

// zipInMemoryFileNode is a [zipBaseFileNode] that implements only the
// [fs.HandleReadAller] for reading the entire file contents into memory.
type zipInMemoryFileNode struct {
	*zipBaseFileNode
}

func (z *zipInMemoryFileNode) Open(_ context.Context, _ *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	// We consider a ZIP to be immutable if it exists, so we don't invalidate here.
	resp.Flags |= fuse.OpenKeepCache

	return z, nil
}

func (z *zipInMemoryFileNode) ReadAll(_ context.Context) ([]byte, error) {
	bytesRead := 0

	zr, fr, err := openZipEntry(z.Archive, z.Path)
	if err != nil {
		logging.Printf("Error: %q->ReadAll->%q: ZIP Error: %v\n", z.Archive, z.Path, err)

		return nil, fuse.ToErrno(err)
	}
	defer func() {
		fr.Close()
		zr.Close(bytesRead)
	}()

	data, err := io.ReadAll(fr)
	if err != nil {
		logging.Printf("Error: %q->ReadAll->%q: IO Error: %v\n", z.Archive, z.Path, err)

		return nil, fuse.ToErrno(syscall.EIO)
	}
	bytesRead = len(data)

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
	zr, fr, err := openZipEntry(z.Archive, z.Path)
	if err != nil {
		logging.Printf("Error: %q->Open->%q: ZIP Error: %v\n", z.Archive, z.Path, err)

		return nil, fuse.ToErrno(err)
	}

	// We consider a ZIP to be immutable if it exists, so we don't invalidate here.
	resp.Flags |= fuse.OpenKeepCache

	return &zipDiskStreamFileHandle{
		archive:   z.Archive,
		path:      z.Path,
		zr:        zr,
		fr:        fr,
		offset:    0,
		bytesRead: 0,
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

	archive string
	path    string

	zr        *zipReader
	fr        *zipFileReader
	offset    int64
	bytesRead int
}

func (h *zipDiskStreamFileHandle) Read(_ context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	h.Lock()
	defer h.Unlock()

	if req.Offset != h.offset {
		n, err := h.fr.ForwardTo(req.Offset)
		h.offset = n
		switch {
		case errors.Is(err, ErrNonSeekableRewind):
			_ = h.fr.Close()
			_ = h.zr.Close(h.bytesRead)

			// Reopening the entry will start with offset zero, so the
			// pseudo-seek should always succeed even if it's a rewind.
			zr, rc, err := openZipEntry(h.archive, h.path)
			if err != nil {
				logging.Printf("Error: %q->Read->%q: ZIP Error: %v\n", h.archive, h.path, err)

				return fuse.ToErrno(syscall.EINVAL)
			}

			h.zr = zr
			h.fr = rc
			h.offset = 0
			h.bytesRead = 0
			Metrics.TotalReopenedZips.Add(1)

			// Retry the forward... if it fails again, return an error.
			n, err = h.fr.ForwardTo(req.Offset)
			h.offset = n
			if err != nil {
				logging.Printf("Error: %q->Read->%q: Seek Error: %v\n", h.archive, h.path, err)

				return fuse.ToErrno(syscall.EIO)
			}

		case err != nil:
			logging.Printf("Error: %q->Read->%q: Seek Error: %v\n", h.archive, h.path, err)

			return fuse.ToErrno(syscall.EIO)
		}
	}

	buf := make([]byte, req.Size)

	n, err := h.fr.Read(buf)
	h.bytesRead += n
	h.offset += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		logging.Printf("Error: %q->Read->%q: IO Error: %v\n", h.archive, h.path, err)

		return fuse.ToErrno(syscall.EIO)
	}

	resp.Data = buf[:n]

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
		_ = h.zr.Close(h.bytesRead)
		h.zr = nil
	}

	return nil
}
