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
	dash := testDashboard(t, io.Discard)

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

			dash.fsys.Metrics.TotalFDCacheHits.Store(tt.hits)
			dash.fsys.Metrics.TotalFDCacheMisses.Store(tt.misses)

			got := dash.totalFDCacheRatio()
			require.Equal(t, tt.wantStr, got)
		})
	}
}

// Expectation: enabledOrDisabled should produce the correct string.
func Test_enabledOrDisabled_Success(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Enabled", enabledOrDisabled(true))
	require.Equal(t, "Disabled", enabledOrDisabled(false))
}
