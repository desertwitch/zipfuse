package filesystem

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

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

// zipEntryNormalize ensures ZIP paths use slashes and removes malformations.
// It also handles non-unicode paths, trying to get the unicode representation
// or instead falling back to a generation using ZIP file index and/or hashing.
func zipEntryNormalize(index int, f *zip.File, forceUnicode bool) string {
	var path string
	var isUnicode bool

	if utf8.ValidString(f.Name) {
		path = f.Name
		isUnicode = true
	} else if p, ok := zipEntryUnicodeFromExtra(f); ok {
		path = p
		isUnicode = true
	} else {
		path = f.Name
		isUnicode = false
	}

	path = filepath.ToSlash(path)

	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}

	path = strings.TrimPrefix(path, "/")

	if !isUnicode && forceUnicode {
		// We do this here because the function relies on clean "/".
		path = zipEntryUnicodeFallback(index, path)
	}

	return path
}

// zipEntryUnicodeFromExtra tries to parse the Extra field of a [zip.File]
// for the Unicode path name field which is located with header ID 0x7075.
//
//nolint:mnd
func zipEntryUnicodeFromExtra(f *zip.File) (string, bool) {
	extra := f.Extra

	i := 0
	for i+4 <= len(extra) {
		headerID := binary.LittleEndian.Uint16(extra[i:])
		dataSize := binary.LittleEndian.Uint16(extra[i+2:])
		i += 4
		if i+int(dataSize) > len(extra) {
			break
		}

		data := extra[i : i+int(dataSize)]
		i += int(dataSize)

		if headerID == 0x7075 {
			if len(data) < 5 {
				continue
			}

			ubuf := data[5:]
			if utf8.Valid(ubuf) {
				return string(ubuf), true
			}
		}
	}

	return "", false
}

// zipEntryUnicodeFallback tries to salvage as much UTF8 of the original ZIP path
// as possible, fallback to generation using archive-internal index and hashing.
func zipEntryUnicodeFallback(index int, normalizedPath string) string {
	parts := strings.Split(normalizedPath, "/")
	converted := make([]string, 0, len(parts))

	for i, part := range parts {
		if part == "" || utf8.ValidString(part) {
			converted = append(converted, part)
		} else {
			if i == len(parts)-1 { // File
				ext := filepath.Ext(part)
				if ext != "" && !utf8.ValidString(ext) {
					ext = ""
				}
				converted = append(converted, fmt.Sprintf("noutf8_file(%d)%s", index, ext))
			} else { // Directory
				hash := fmt.Sprintf("%x", sha1.Sum([]byte(part)))[:8]
				converted = append(converted, "noutf8_dir("+hash+")")
			}
		}
	}

	return strings.Join(converted, "/")
}

// flatEntryName flattens a normalized path to a filename, discarding structure.
// Path collisions are avoided via appending of the index to the filename base.
func flatEntryName(index int, normalizedPath string) (string, bool) {
	cleanedEntryName := filepath.Clean(normalizedPath)

	if strings.HasPrefix(cleanedEntryName, "..") {
		return cleanedEntryName, false
	}

	baseName := filepath.Base(cleanedEntryName)
	if baseName == "." || baseName == ".." || baseName == "/" {
		return baseName, false
	}

	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	return fmt.Sprintf("%s(%d)%s", nameWithoutExt, index, ext), true
}
