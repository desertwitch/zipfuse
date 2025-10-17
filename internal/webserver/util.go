//nolint:mnd
package webserver

import (
	"fmt"
	"time"

	"github.com/dustin/go-humanize"
)

// avgMetadataReadTime returns a string of the average metadata read time.
func (d *FSDashboard) avgMetadataReadTime() string {
	return time.Duration(d.fsys.Metrics.TotalMetadataReadTime.Load() / max(1, d.fsys.Metrics.TotalMetadataReadCount.Load())).String()
}

// avgExtractTime returns a string of the average extraction time.
func (d *FSDashboard) avgExtractTime() string {
	return time.Duration(d.fsys.Metrics.TotalExtractTime.Load() / max(1, d.fsys.Metrics.TotalExtractCount.Load())).String()
}

// avgExtractSpeed returns a string of the average extraction throughput.
func (d *FSDashboard) avgExtractSpeed() string {
	bytes := d.fsys.Metrics.TotalExtractBytes.Load()
	ns := d.fsys.Metrics.TotalExtractTime.Load()

	if ns == 0 {
		return "0 B/s"
	}

	bps := float64(bytes) / (float64(ns) / 1e9)

	return humanize.IBytes(uint64(bps)) + "/s"
}

// totalExtractBytes returns a string of the total extracted bytes.
func (d *FSDashboard) totalExtractBytes() string {
	bytes := d.fsys.Metrics.TotalExtractBytes.Load()

	if bytes < 0 {
		return humanize.IBytes(0)
	}

	return humanize.IBytes(uint64(bytes))
}

// totalFDCacheRatio returns a string of the FD cache hit/miss ratio.
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

// streamPoolHitRatio returns a string of the stream pool hit/miss ratio.
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

// streamPoolHitAvgSize returns a string of the average stream pool hit size.
func (d *FSDashboard) streamPoolHitAvgSize() string {
	hits := d.fsys.Metrics.TotalStreamPoolHits.Load()
	hitBytes := d.fsys.Metrics.TotalStreamPoolHitBytes.Load()

	if hits == 0 {
		return "0 B"
	}

	avg := hitBytes / hits

	return humanize.IBytes(uint64(avg))
}

// streamPoolMissAvgSize returns a string of the average stream pool miss size.
func (d *FSDashboard) streamPoolMissAvgSize() string {
	misses := d.fsys.Metrics.TotalStreamPoolMisses.Load()
	missBytes := d.fsys.Metrics.TotalStreamPoolMissBytes.Load()

	if misses == 0 {
		return "0 B"
	}

	avg := missBytes / misses

	return humanize.IBytes(uint64(avg))
}

// enabledOrDisabled returns string "Enabled" or "Disabled" based on a boolean.
func enabledOrDisabled(v bool) string {
	if v {
		return "Enabled"
	}

	return "Disabled"
}
