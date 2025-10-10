package webserver

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// Expectation: avgMetadataReadTime should calculate correctly.
func Test_avgMetadataReadTime_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalMetadataReadTime.Store(1_000_000_000)
	dash.fsys.Metrics.TotalMetadataReadCount.Store(10)

	result := dash.avgMetadataReadTime()
	require.Contains(t, result, "100ms")
}

// Expectation: avgMetadataReadTime should handle zero count.
func Test_avgMetadataReadTime_ZeroCount_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalMetadataReadTime.Store(1000)
	dash.fsys.Metrics.TotalMetadataReadCount.Store(0)

	result := dash.avgMetadataReadTime()
	require.NotEmpty(t, result)
}

// Expectation: avgExtractTime should calculate correctly.
func Test_avgExtractTime_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractTime.Store(2_000_000_000)
	dash.fsys.Metrics.TotalExtractCount.Store(20)

	result := dash.avgExtractTime()
	require.Contains(t, result, "100ms")
}

// Expectation: avgExtractTime should handle zero count.
func Test_avgExtractTime_ZeroCount_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractTime.Store(1000)
	dash.fsys.Metrics.TotalExtractCount.Store(0)

	result := dash.avgExtractTime()
	require.NotEmpty(t, result)
}

// Expectation: avgExtractSpeed should calculate bytes per second correctly.
func Test_avgExtractSpeed_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractBytes.Store(1 * 1024 * 1024)
	dash.fsys.Metrics.TotalExtractTime.Store(1_000_000_000)

	result := dash.avgExtractSpeed()
	require.Contains(t, result, "/s")
	require.Contains(t, result, "MiB")
}

// Expectation: avgExtractSpeed should handle zero time.
func Test_avgExtractSpeed_ZeroTime_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractBytes.Store(1000)
	dash.fsys.Metrics.TotalExtractTime.Store(0)

	result := dash.avgExtractSpeed()
	require.Equal(t, "0 B/s", result)
}

// Expectation: totalExtractBytes should format bytes correctly.
func Test_totalExtractBytes_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractBytes.Store(500 * 1024 * 1024)

	result := dash.totalExtractBytes()
	require.Contains(t, result, "500 MiB")
}

// Expectation: totalExtractBytes should handle negative values.
func Test_totalExtractBytes_Negative_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalExtractBytes.Store(-100)

	result := dash.totalExtractBytes()
	require.Equal(t, "0 B", result)
}

// Expectation: totalFDCacheRatio should return correct percentage string.
func Test_totalFDCacheRatio_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hits    int64
		misses  int64
		wantStr string
	}{
		{
			name:    "NoHitsNoMisses",
			hits:    0,
			misses:  0,
			wantStr: "0.00%",
		},
		{
			name:    "OnlyHits",
			hits:    10,
			misses:  0,
			wantStr: "100.00%",
		},
		{
			name:    "OnlyMisses",
			hits:    0,
			misses:  5,
			wantStr: "0.00%",
		},
		{
			name:    "EqualHitsAndMisses",
			hits:    5,
			misses:  5,
			wantStr: "50.00%",
		},
		{
			name:    "MoreHits",
			hits:    8,
			misses:  2,
			wantStr: "80.00%",
		},
		{
			name:    "MoreMisses",
			hits:    2,
			misses:  8,
			wantStr: "20.00%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dash := testDashboard(t, io.Discard)

			dash.fsys.Metrics.TotalFDCacheHits.Store(tt.hits)
			dash.fsys.Metrics.TotalFDCacheMisses.Store(tt.misses)

			got := dash.totalFDCacheRatio()
			require.Equal(t, tt.wantStr, got)
		})
	}
}

// Expectation: streamPoolHitRatio should return correct percentage string.
func Test_streamPoolHitRatio_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hits    int64
		misses  int64
		wantStr string
	}{
		{
			name:    "NoHitsNoMisses",
			hits:    0,
			misses:  0,
			wantStr: "0.00%",
		},
		{
			name:    "OnlyHits",
			hits:    10,
			misses:  0,
			wantStr: "100.00%",
		},
		{
			name:    "OnlyMisses",
			hits:    0,
			misses:  5,
			wantStr: "0.00%",
		},
		{
			name:    "EqualHitsAndMisses",
			hits:    5,
			misses:  5,
			wantStr: "50.00%",
		},
		{
			name:    "MoreHits",
			hits:    8,
			misses:  2,
			wantStr: "80.00%",
		},
		{
			name:    "MoreMisses",
			hits:    2,
			misses:  8,
			wantStr: "20.00%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dash := testDashboard(t, io.Discard)
			dash.fsys.Metrics.TotalStreamPoolHits.Store(tt.hits)
			dash.fsys.Metrics.TotalStreamPoolMisses.Store(tt.misses)

			got := dash.streamPoolHitRatio()
			require.Equal(t, tt.wantStr, got)
		})
	}
}

// Expectation: streamPoolMissAvgSize should return average size or 0 B.
func Test_streamPoolMissAvgSize_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	// No misses
	dash.fsys.Metrics.TotalStreamPoolMisses.Store(0)
	dash.fsys.Metrics.TotalStreamPoolMissBytes.Store(1024)
	require.Equal(t, "0 B", dash.streamPoolMissAvgSize())

	// With misses
	dash.fsys.Metrics.TotalStreamPoolMisses.Store(2)
	dash.fsys.Metrics.TotalStreamPoolMissBytes.Store(2048)
	require.Equal(t, "1.0 KiB", dash.streamPoolMissAvgSize())
}

// Expectation: streamPoolUtilization should return utilization or 0.0%.
func Test_streamPoolUtilization_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	// No hits
	dash.fsys.Metrics.TotalStreamPoolHits.Store(0)
	dash.fsys.Metrics.TotalStreamPoolHitBytes.Store(1024)
	dash.fsys.Options.StreamPoolSize = 128 * 1024
	require.Equal(t, "0.0%", dash.streamPoolUtilization())

	// With hits and usage
	dash.fsys.Metrics.TotalStreamPoolHits.Store(2)
	dash.fsys.Metrics.TotalStreamPoolHitBytes.Store(64 * 1024) // 64 KiB requested total
	dash.fsys.Options.StreamPoolSize = 128 * 1024
	got := dash.streamPoolUtilization()
	require.Equal(t, "25.0%", got) // 64 KiB / (2*128 KiB) = 0.25
}

// Expectation: enabledOrDisabled should produce the correct string.
func Test_enabledOrDisabled_Success(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Enabled", enabledOrDisabled(true))
	require.Equal(t, "Disabled", enabledOrDisabled(false))
}
