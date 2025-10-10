//nolint:mnd
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

	bps := float64(bytes) / (float64(ns) / 1e9)

	return humanize.IBytes(uint64(bps)) + "/s"
}

func (d *FSDashboard) totalExtractBytes() string {
	bytes := d.fsys.Metrics.TotalExtractBytes.Load()

	if bytes < 0 {
		return humanize.IBytes(0)
	}

	return humanize.IBytes(uint64(bytes))
}

func (d *FSDashboard) totalFDCacheRatio() string {
	hits := d.fsys.Metrics.TotalFDCacheHits.Load()
	misses := d.fsys.Metrics.TotalFDCacheMisses.Load()
	total := hits + misses

	if total == 0 {
		return "0.00%"
	}

	perc := (float64(hits) / float64(total)) * 100

	return fmt.Sprintf("%.2f%%", perc)
}

func (d *FSDashboard) streamPoolHitRatio() string {
	hits := d.fsys.Metrics.TotalStreamPoolHits.Load()
	misses := d.fsys.Metrics.TotalStreamPoolMisses.Load()
	total := hits + misses

	if total == 0 {
		return "0.00%"
	}

	perc := (float64(hits) / float64(total)) * 100

	return fmt.Sprintf("%.2f%%", perc)
}

func (d *FSDashboard) streamPoolHitAvgSize() string {
	hits := d.fsys.Metrics.TotalStreamPoolHits.Load()
	hitBytes := d.fsys.Metrics.TotalStreamPoolHitBytes.Load()

	if hits == 0 {
		return "0 B"
	}

	avg := hitBytes / hits

	return humanize.IBytes(uint64(avg))
}

func (d *FSDashboard) streamPoolMissAvgSize() string {
	misses := d.fsys.Metrics.TotalStreamPoolMisses.Load()
	missBytes := d.fsys.Metrics.TotalStreamPoolMissBytes.Load()

	if misses == 0 {
		return "0 B"
	}

	avg := missBytes / misses

	return humanize.IBytes(uint64(avg))
}

func enabledOrDisabled(v bool) string {
	if v {
		return "Enabled"
	}

	return "Disabled"
}
