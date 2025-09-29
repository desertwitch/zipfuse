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

// flatEntryName flattens a path into just the filename.
// Name collisions are avoided by appending 8 digits of its SHA-1 hash.
func flatEntryName(zipEntryName string) (string, bool) {
	cleanedEntryName := filepath.Clean(filepath.ToSlash(zipEntryName))

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

	return nameWithoutExt + "_" + hash[:8] + ext, true
}

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
