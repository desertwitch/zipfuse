package filesystem

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bazil.org/fuse"
	"github.com/klauspost/compress/zip"
)

// zipMetric is a single measurement of a ZIP operation.
type zipMetric struct {
	fsys      *FS
	isExtract bool
	startTime time.Time
	readBytes int64
}

// newZipMetric returns a pointer to a new [zipMetric] for a single
// measurement of a ZIP operation. The time is set to time.Now(),
// and the measurement fields can be mutated as required. You must
// call Done() on the [zipMetric] when the measurement is finished.
func newZipMetric(fsys *FS, isExtract bool) *zipMetric {
	return &zipMetric{
		fsys:      fsys,
		startTime: time.Now(),
		isExtract: isExtract,
		readBytes: 0,
	}
}

// Done closes the single measurement of a ZIP operation and adds the
// field values to the filesystem metrics, so ensures saving of the metrics.
func (m *zipMetric) Done() {
	if m.isExtract {
		m.fsys.Metrics.TotalExtractTime.Add(time.Since(m.startTime).Nanoseconds())
		m.fsys.Metrics.TotalExtractCount.Add(1)
		m.fsys.Metrics.TotalExtractBytes.Add(m.readBytes)
	} else {
		m.fsys.Metrics.TotalMetadataReadTime.Add(time.Since(m.startTime).Nanoseconds())
		m.fsys.Metrics.TotalMetadataReadCount.Add(1)
	}
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

// toFuseErr inspects an error chain for a [syscall.Errno] and returns it
// when found, otherwise trying for the next best fit to return as Errno.
// If no compatible error can be approximated, it defaults to [syscall.EIO].
func toFuseErr(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fuse.ToErrno(errno)
	}
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
