package webgui

import (
	"time"

	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/dustin/go-humanize"
)

func avgMetadataReadTime() string {
	return time.Duration(filesystem.TotalMetadataReadTime.Load() / max(1, filesystem.TotalMetadataReadCount.Load())).String()
}

func avgExtractTime() string {
	return time.Duration(filesystem.TotalExtractTime.Load() / max(1, filesystem.TotalExtractCount.Load())).String()
}

func avgExtractSpeed() string {
	bytes := filesystem.TotalExtractBytes.Load()
	ns := filesystem.TotalExtractTime.Load()

	if ns == 0 {
		return "0 B/s"
	}

	bps := float64(bytes) / (float64(ns) / 1e9) //nolint:mnd

	return humanize.Bytes(uint64(bps)) + "/s"
}

func totalExtractBytes() string {
	bytes := filesystem.TotalExtractBytes.Load()

	if bytes < 0 {
		return humanize.Bytes(0)
	}

	return humanize.Bytes(uint64(bytes))
}
