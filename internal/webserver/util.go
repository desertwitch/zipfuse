package webserver

import (
	"fmt"
	"time"

	"github.com/dustin/go-humanize"
)

func (d *FSDashboard) avgMetadataReadTime() string {
	return time.Duration(d.fsys.Metrics.TotalMetadataReadTime.Load() / max(1, d.fsys.Metrics.TotalMetadataReadCount.Load())).String()
}

func (d *FSDashboard) avgExtractTime() string {
	return time.Duration(d.fsys.Metrics.TotalExtractTime.Load() / max(1, d.fsys.Metrics.TotalExtractCount.Load())).String()
}

func (d *FSDashboard) avgExtractSpeed() string {
	bytes := d.fsys.Metrics.TotalExtractBytes.Load()
	ns := d.fsys.Metrics.TotalExtractTime.Load()

	if ns == 0 {
		return "0 B/s"
	}

	bps := float64(bytes) / (float64(ns) / 1e9) //nolint:mnd

	return humanize.Bytes(uint64(bps)) + "/s"
}

func (d *FSDashboard) totalExtractBytes() string {
	bytes := d.fsys.Metrics.TotalExtractBytes.Load()

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

//nolint:mnd
func (d *FSDashboard) totalLruRatio() string {
	hits := d.fsys.Metrics.TotalLruHits.Load()
	misses := d.fsys.Metrics.TotalLruMisses.Load()
	total := hits + misses

	if total == 0 {
		return "0.00%"
	}

	perc := (float64(hits) / float64(total)) * 100

	return fmt.Sprintf("%.2f%%", perc)
}
