package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/klauspost/compress/zip"
)

// zipReader is a metrics-aware [zip.ReadCloser].
type zipReader struct {
	*zip.ReadCloser

	startTime time.Time
	isExtract bool
}

func (z *zipReader) Close(readBytes int) error {
	openZips.Add(-1)
	closedZips.Add(1)

	if z.isExtract {
		totalExtractTime.Add(time.Since(z.startTime).Nanoseconds())
		totalExtractCount.Add(1)
		totalExtractBytes.Add(int64(readBytes))
	} else {
		totalMetadataReadTime.Add(time.Since(z.startTime).Nanoseconds())
		totalMetadataReadCount.Add(1)
	}

	return z.ReadCloser.Close() //nolint:wrapcheck
}

func newZipReader(path string, isExtract bool) (*zipReader, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	openZips.Add(1)
	openedZips.Add(1)

	return &zipReader{
		ReadCloser: zr,
		startTime:  time.Now(),
		isExtract:  isExtract,
	}, nil
}

func parseArgsOrExit() (root string, mount string) { //nolint:nonamedreturns
	if len(os.Args) < 4 { //nolint:mnd
		logPrintf("Usage: %s <root-dir> <mountpoint> <streaming-threshold>\n", os.Args[0])
		os.Exit(1)
	}

	root, mount = os.Args[1], os.Args[2]
	threshold, err := humanize.ParseBytes(os.Args[3])

	if root == "" || mount == "" || threshold <= 0 || err != nil {
		logPrintf("Usage: %s <root-dir> <mountpoint> <streaming-threshold>\n", os.Args[0])
		if err != nil {
			logPrintf("Error: %v", err)
		}
		os.Exit(1)
	}

	streamingThreshold.Store(threshold)

	return root, mount
}

// flatEntryName flattens a path into just the filename.
// Name collisions are avoided by appending 8 digits of its SHA-1 hash.
func flatEntryName(zipEntryName string) (string, bool) {
	cleanedEntryName := filepath.Clean(filepath.ToSlash(zipEntryName))

	baseName := filepath.Base(cleanedEntryName)
	if baseName == "." || strings.HasPrefix(baseName, "..") || baseName == "/" {
		return baseName, false
	}

	h := sha1.New()
	h.Write([]byte(cleanedEntryName))
	hash := hex.EncodeToString(h.Sum(nil))

	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	return nameWithoutExt + "_" + hash[:8] + ext, true
}
