package filesystem

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/klauspost/compress/zip"
)

var (
	_ io.ReadCloser = (*zipFileReader)(nil)

	// errNonSeekableRewind occurs when an attempt is made to rewind a non-seekable file.
	errNonSeekableRewind = errors.New("cannot rewind non-seekable file")
)

type zipMetric struct {
	isExtract bool
	startTime time.Time
	readBytes int64
}

// zipReader is a metrics-aware [zip.ReadCloser].
type zipReader struct {
	*zip.ReadCloser

	fsys     *FS
	refCount atomic.Int32
}

// newZipReader returns a pointer to a new [zipReader] for given path.
// Argument isExtract separates metadata reading and extraction metrics.
func newZipReader(fsys *FS, path string) (*zipReader, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	fsys.Metrics.OpenZips.Add(1)
	fsys.Metrics.TotalOpenedZips.Add(1)

	return &zipReader{
		fsys:       fsys,
		ReadCloser: zr,
	}, nil
}

func zipMetricStart() zipMetric {
	return zipMetric{
		startTime: time.Now(),
		isExtract: false,
		readBytes: 0,
	}
}

func zipMetricEnd(fsys *FS, m zipMetric) {
	if m.isExtract {
		if m.readBytes > 0 { // We do not count Open calls without extraction.
			fsys.Metrics.TotalExtractTime.Add(time.Since(m.startTime).Nanoseconds())
			fsys.Metrics.TotalExtractCount.Add(1)
			fsys.Metrics.TotalExtractBytes.Add(m.readBytes)
		}
	} else {
		fsys.Metrics.TotalMetadataReadTime.Add(time.Since(m.startTime).Nanoseconds())
		fsys.Metrics.TotalMetadataReadCount.Add(1)
	}
}

func (zr *zipReader) Release() {
	if zr.refCount.Add(-1) <= 0 {
		zr.Close()
	}
}

// Close closes the [zip.ReadCloser] and records the bytes read.
func (zr *zipReader) Close() error {
	zr.fsys.Metrics.OpenZips.Add(-1)
	zr.fsys.Metrics.TotalClosedZips.Add(1)

	return zr.ReadCloser.Close() //nolint:wrapcheck
}

// zipFileReader opens a [zip.File] for reading and forward seeking.
// Depending on compression and runtime options, the seeking is implemented
// either by actual seeking (type assertion) or reading bytes to [io.Discard].
type zipFileReader struct {
	r   io.Reader
	pos int64
}

// newZipFileReader opens a [zip.File] and returns a new [zipFileReader].
// An error is returned if the [zip.File] cannot be opened.
// You should ensure that Close will always be called after use.
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

	return &zipFileReader{r: r}, nil
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
// In case of [io.ReadCloser], do not use it anymore after calling Close on it.
func (fr *zipFileReader) Reader() io.Reader {
	return fr.r
}

// Position is the position of the underlying [io.Reader] of [zipFileReader].
func (fr *zipFileReader) Position() int64 {
	return fr.pos
}

// Close facilitiates the closing of the reader after use.
func (fr *zipFileReader) Close() error {
	if closer, ok := fr.r.(io.ReadCloser); ok {
		return closer.Close() //nolint:wrapcheck
	}

	return nil
}

// flatEntryName flattens a normalized path to a filename, discarding structure.
// Any name collisions are avoided via appending [flattenHashDigits] of its SHA-1 hash.
func flatEntryName(normalizedPath string) (string, bool) {
	cleanedEntryName := filepath.Clean(normalizedPath)

	if strings.HasPrefix(cleanedEntryName, "..") {
		return cleanedEntryName, false
	}

	baseName := filepath.Base(cleanedEntryName)
	if baseName == "." || baseName == ".." || baseName == "/" {
		return baseName, false
	}

	h := sha1.New()
	h.Write([]byte(cleanedEntryName))
	hash := hex.EncodeToString(h.Sum(nil))

	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	return nameWithoutExt + "_" + hash[:flattenHashDigits] + ext, true
}

// toFuseErr converts an error into either ENOENT, EACCES or EIO.
// When the error is not convertable, a generic EIO is chosen instead.
func toFuseErr(err error) error {
	switch {
	case os.IsNotExist(err):
		return fuse.ToErrno(syscall.ENOENT)

	case os.IsPermission(err):
		return fuse.ToErrno(syscall.EACCES)

	default:
		return fuse.ToErrno(syscall.EIO)
	}
}

// isDir checks if [zip.File] is a directory either by mode or normalized path.
func isDir(f *zip.File, normalizedPath string) bool {
	return f.FileInfo().IsDir() || strings.HasSuffix(normalizedPath, "/")
}

// normalizeZipPath ensures ZIP paths use slashes and removes malformations.
func normalizeZipPath(path string) string {
	path = filepath.ToSlash(path)

	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}

	path = strings.TrimPrefix(path, "/")

	return path
}
