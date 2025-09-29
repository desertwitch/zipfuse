package main

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var _ fs.Node = (*zipBaseFileNode)(nil)

// zipBaseFileNode is a file within a ZIP archive of the mirrored filesystem.
// It is presented as a regular file in our filesystem, for in-memory unpacking.
//
// To be embedded into either [zipInMemoryFileNode] or [zipDiskStreamFileNode],
// depending on which [streamingThreshold] was set by CLI argument or at runtime.
type zipBaseFileNode struct {
	Inode    uint64    // Inode within our filesystem.
	Archive  string    // Path of the actual ZIP archive (= parent).
	Path     string    // Path of the actual file inside the ZIP file.
	Size     uint64    // Size of the actual file inside the ZIP file.
	Modified time.Time // Modified time of the actual file inside the ZIP file.
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
	_ fs.HandleReadAller = (*zipInMemoryFileNode)(nil)
)

// zipInMemoryFileNode is a [zipBaseFileNode] that implements only the
// [fs.HandleReadAller] for reading the entire file contents into memory.
type zipInMemoryFileNode struct {
	*zipBaseFileNode
}

func (z *zipInMemoryFileNode) ReadAll(_ context.Context) ([]byte, error) {
	bytesRead := 0

	zr, err := newZipReader(z.Archive, true)
	if err != nil {
		logPrintf("Error: %q->ReadAll->%q: ZIP Error: %v\n", z.Archive, z.Path, err)

		return nil, fuse.ToErrno(syscall.EINVAL)
	}
	defer func() {
		zr.Close(bytesRead)
	}()

	for _, f := range zr.File {
		if f.Name == z.Path {
			rc, err := f.Open()
			if err != nil {
				logPrintf("Error: %q->ReadAll->%q: Open Error: %v\n", z.Archive, z.Path, err)

				return nil, fuse.ToErrno(syscall.EIO)
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				logPrintf("Error: %q->Readall->%q: IO Error: %v\n", z.Archive, z.Path, err)

				return nil, fuse.ToErrno(syscall.EIO)
			}
			bytesRead = len(data)

			return data, nil
		}
	}

	return nil, fuse.ToErrno(syscall.ENOENT)
}

var (
	_ fs.Node         = (*zipDiskStreamFileNode)(nil)
	_ fs.HandleReader = (*zipDiskStreamFileNode)(nil)
)

// zipDiskStreamFileNode is a [zipBaseFileNode] that implements only the
// [fs.HandleReader] for streaming the kernel requested bytes from the file.
type zipDiskStreamFileNode struct {
	*zipBaseFileNode
}

func (z *zipDiskStreamFileNode) Read(_ context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	bytesRead := 0

	zr, err := newZipReader(z.Archive, true)
	if err != nil {
		logPrintf("Error: %q->Read->%q: ZIP Error: %v\n", z.Archive, z.Path, err)

		return fuse.ToErrno(syscall.EINVAL)
	}
	defer func() {
		zr.Close(bytesRead)
	}()

	for _, f := range zr.File {
		if f.Name == z.Path { //nolint:nestif
			rc, err := f.Open()
			if err != nil {
				logPrintf("Error: %q->Read->%q: Open Error: %v\n", z.Archive, z.Path, err)

				return fuse.ToErrno(syscall.EIO)
			}
			defer rc.Close()

			if seeker, ok := rc.(io.Seeker); ok {
				if _, err := seeker.Seek(req.Offset, io.SeekStart); err != nil {
					logPrintf("Error: %q->Read->%q: Seek Error: %v\n", z.Archive, z.Path, err)

					return fuse.ToErrno(syscall.EIO)
				}
			} else {
				_, err = io.CopyN(io.Discard, rc, req.Offset)
				if err != nil {
					logPrintf("Error: %q->Read->%q: CopyN Error: %v\n", z.Archive, z.Path, err)

					return fuse.ToErrno(syscall.EIO)
				}
			}

			buf := make([]byte, req.Size)

			n, err := io.ReadFull(rc, buf)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				logPrintf("Error: %q->Read->%q: IO Error: %v\n", z.Archive, z.Path, err)

				return fuse.ToErrno(syscall.EIO)
			}

			resp.Data = buf[:n]
			bytesRead = len(resp.Data)

			return nil
		}
	}

	return fuse.ToErrno(syscall.ENOENT)
}
