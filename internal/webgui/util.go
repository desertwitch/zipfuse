package webgui

import (
	"time"

	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/dustin/go-humanize"
)

func avgMetadataReadTime() string {
	return time.Duration(filesystem.Metrics.TotalMetadataReadTime.Load() / max(1, filesystem.Metrics.TotalMetadataReadCount.Load())).String()
}

func avgExtractTime() string {
	return time.Duration(filesystem.Metrics.TotalExtractTime.Load() / max(1, filesystem.Metrics.TotalExtractCount.Load())).String()
}

func avgExtractSpeed() string {
	bytes := filesystem.Metrics.TotalExtractBytes.Load()
	ns := filesystem.Metrics.TotalExtractTime.Load()

	if ns == 0 {
		return "0 B/s"
	}

	bps := float64(bytes) / (float64(ns) / 1e9) //nolint:mnd

	return humanize.Bytes(uint64(bps)) + "/s"
}

func totalExtractBytes() string {
	bytes := filesystem.Metrics.TotalExtractBytes.Load()

	if bytes < 0 {
		return humanize.Bytes(0)
	}

	return humanize.Bytes(uint64(bytes))
}

func enabledOrDisabled(v bool) string {
	if v {
		return "Enabled"
	}

	return "Disabled"
}
