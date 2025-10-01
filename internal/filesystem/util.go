package filesystem

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/klauspost/compress/zip"
)

// zipReader is a metrics-aware [zip.ReadCloser].
type zipReader struct {
	*zip.ReadCloser

	startTime time.Time
	isExtract bool
}

// Close closes the [zip.ReadCloser] and records the bytes read.
func (z *zipReader) Close(readBytes int) error {
	OpenZips.Add(-1)
	TotalClosedZips.Add(1)

	if z.isExtract {
		TotalExtractTime.Add(time.Since(z.startTime).Nanoseconds())
		TotalExtractCount.Add(1)
		TotalExtractBytes.Add(int64(readBytes))
	} else {
		TotalMetadataReadTime.Add(time.Since(z.startTime).Nanoseconds())
		TotalMetadataReadCount.Add(1)
	}

	return z.ReadCloser.Close() //nolint:wrapcheck
}

// newZipReader returns a pointer to a new [zipReader] for given path.
// Argument isExtract separates metadata reading and extraction metrics.
func newZipReader(path string, isExtract bool) (*zipReader, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	OpenZips.Add(1)
	TotalOpenedZips.Add(1)

	return &zipReader{
		ReadCloser: zr,
		startTime:  time.Now(),
		isExtract:  isExtract,
	}, nil
}

// flatEntryName flattens a normalized path to a filename, discarding structure.
// Any name collisions are avoided via appending [hashDigits] of its SHA-1 hash.
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

	return nameWithoutExt + "_" + hash[:hashDigits] + ext, true
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
