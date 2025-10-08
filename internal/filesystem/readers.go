package filesystem

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/klauspost/compress/zip"
)

// zipReader is a thread-safe, metrics-aware [zip.ReadCloser].
//
// It allows for multiple files to be read concurrently, while
// keeping open the archive, and internally tracking reference count.
type zipReader struct {
	*zip.ReadCloser

	fsys     *FS
	refCount atomic.Int32
}

// newZipReader returns a pointer to a new [zipReader] for given path.
//
// It increases the atomic reference count by one upon returning the new
// pointer. Once done, you need to call Release() to close the reference.
// When re-using the [zipReader], ensure to always Acquire() and Release().
//
// A new [zipReader] is always returned with a reference count of one.
// This means that one-shot calls only need to call Release() after use.
func newZipReader(fsys *FS, path string) (*zipReader, error) {
	fsys.fdlimit <- struct{}{}

	rc, err := zip.OpenReader(path)
	if err != nil {
		<-fsys.fdlimit

		return nil, err //nolint:wrapcheck
	}

	fsys.Metrics.OpenZips.Add(1)
	fsys.Metrics.TotalOpenedZips.Add(1)

	zr := &zipReader{
		ReadCloser: rc,
		fsys:       fsys,
	}
	zr.Acquire() // for caller

	return zr, nil
}

// Acquire increases the reference count by one, it should be
// called every time a [zipReader] is re-used more than once.
//
// Upon creation of a [zipReader], the reference count is one,
// so single one-shot use does not need to Acquire(), and only
// ensure a Release() call once [zipReader] is done being used.
func (zr *zipReader) Acquire() {
	zr.refCount.Add(1)
}

// Release decreases the reference count by one and closes the
// [zipReader] if the new reference count is exactly at zero (0).
func (zr *zipReader) Release() error {
	if zr.refCount.Add(-1) == 0 {
		return zr.closeReader()
	}

	return nil
}

// Close is not supported and will always panic when being used.
// You must use Release() instead, which internally calls Close().
func (zr *zipReader) Close() error {
	panic("unsupported direct close of zipReader, use Release() instead")
}

// closeReader instantly closes the [zip.ReadCloser].
// You must use Release() instead, which internally calls closeReader().
func (zr *zipReader) closeReader() error {
	defer func() {
		<-zr.fsys.fdlimit
	}()

	zr.fsys.Metrics.OpenZips.Add(-1)
	zr.fsys.Metrics.TotalClosedZips.Add(1)

	return zr.ReadCloser.Close() //nolint:wrapcheck
}

var (
	_ io.ReadCloser = (*zipFileReader)(nil)

	// errNonSeekableRewind occurs when an attempt is made to rewind a non-seekable file.
	errNonSeekableRewind = errors.New("cannot rewind non-seekable file")
)

// zipFileReader opens a [zip.File] for reading and forward seeking.
// Depending on compression and runtime options, the seeking is implemented
// either by actual seeking (type assertion) or reading bytes to [io.Discard].
//
// It is not thread-safe, but you can use the contained [zip.File] pointer to
// establish a new [zipFileReader], if needing to open one file concurrently.
type zipFileReader struct {
	f   *zip.File
	r   io.Reader
	pos int64
}

// newZipFileReader opens a [zip.File] and returns a new [zipFileReader].
// You must ensure that Close() will always be called after use is complete.
func newZipFileReader(fsys *FS, f *zip.File) (*zipFileReader, error) {
	var r io.Reader
	var err error

	if f.Method == zip.Store && !fsys.Options.MustCRC32.Load() {
		r, err = f.OpenRaw()
	} else {
		r, err = f.Open()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}

	return &zipFileReader{r: r, f: f}, nil
}

// Read facilitates reading of a fixed amount of bytes.
// It returns the number of bytes that were read and an error.
func (fr *zipFileReader) Read(p []byte) (int, error) {
	n, err := fr.r.Read(p)
	fr.pos += int64(n)

	return n, err //nolint:wrapcheck
}

// ForwardTo advances the reader position to the specified offset.
// It returns the offset of the internal reader position and an error.
// [errNonSeekableRewind] is returned upon rewinding a non-seekable file.
func (fr *zipFileReader) ForwardTo(offset int64) (int64, error) {
	if offset == fr.pos {
		return fr.pos, nil
	}

	if seeker, ok := fr.r.(io.Seeker); ok {
		n, err := seeker.Seek(offset, io.SeekStart)
		fr.pos = n
		if err != nil {
			return fr.pos, fmt.Errorf("failed to seek: %w", err)
		}

		return fr.pos, nil
	}

	if offset < fr.pos {
		return fr.pos, fmt.Errorf("%w (want %d, current %d)", errNonSeekableRewind, offset, fr.pos)
	}

	n, err := io.CopyN(io.Discard, fr.r, offset-fr.pos)
	fr.pos += n
	if err != nil && !errors.Is(err, io.EOF) {
		return fr.pos, fmt.Errorf("failed to discard: %w", err)
	}

	return fr.pos, nil
}

// Reader returns the underlying [io.Reader] of the [zipFileReader].
// You will need to type assert this to [io.ReadCloser] or [io.SectionReader].
// In case of [io.ReadCloser], do not use anymore after call of Close() on it.
func (fr *zipFileReader) Reader() io.Reader {
	return fr.r
}

// Position is the position of the underlying [io.Reader] of [zipFileReader].
func (fr *zipFileReader) Position() int64 {
	return fr.pos
}

// Close facilitiates the closing of the reader after use.
// In case the underlying [io.Reader] is a [io.SectionReader],
// it is a no-op and will return nil without closing anything.
func (fr *zipFileReader) Close() error {
	if closer, ok := fr.r.(io.ReadCloser); ok {
		return closer.Close() //nolint:wrapcheck
	}

	return nil
}
