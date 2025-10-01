package webgui

import (
	"testing"

	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/stretchr/testify/require"
)

// Expectation: avgMetadataReadTime should calculate correctly.
func Test_avgMetadataReadTime_Success(t *testing.T) {
	filesystem.Metrics.TotalMetadataReadTime.Store(1_000_000_000)
	filesystem.Metrics.TotalMetadataReadCount.Store(10)

	result := avgMetadataReadTime()
	require.Contains(t, result, "100ms")
}

// Expectation: avgMetadataReadTime should handle zero count.
func Test_avgMetadataReadTime_ZeroCount_Success(t *testing.T) {
	filesystem.Metrics.TotalMetadataReadTime.Store(1000)
	filesystem.Metrics.TotalMetadataReadCount.Store(0)

	result := avgMetadataReadTime()
	require.NotEmpty(t, result)
}

// Expectation: avgExtractTime should calculate correctly.
func Test_avgExtractTime_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractTime.Store(2_000_000_000)
	filesystem.Metrics.TotalExtractCount.Store(20)

	result := avgExtractTime()
	require.Contains(t, result, "100ms")
}

// Expectation: avgExtractTime should handle zero count.
func Test_avgExtractTime_ZeroCount_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractTime.Store(1000)
	filesystem.Metrics.TotalExtractCount.Store(0)

	result := avgExtractTime()
	require.NotEmpty(t, result)
}

// Expectation: avgExtractSpeed should calculate bytes per second correctly.
func Test_avgExtractSpeed_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractBytes.Store(1_000_000)
	filesystem.Metrics.TotalExtractTime.Store(1_000_000_000)

	result := avgExtractSpeed()
	require.Contains(t, result, "/s")
	require.Contains(t, result, "MB")
}

// Expectation: avgExtractSpeed should handle zero time.
func Test_avgExtractSpeed_ZeroTime_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractBytes.Store(1000)
	filesystem.Metrics.TotalExtractTime.Store(0)

	result := avgExtractSpeed()
	require.Equal(t, "0 B/s", result)
}

// Expectation: totalExtractBytes should format bytes correctly.
func Test_totalExtractBytes_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractBytes.Store(500_000_000)

	result := totalExtractBytes()
	require.Contains(t, result, "500 MB")
}

// Expectation: totalExtractBytes should handle negative values.
func Test_totalExtractBytes_Negative_Success(t *testing.T) {
	filesystem.Metrics.TotalExtractBytes.Store(-100)

	result := totalExtractBytes()
	require.Equal(t, "0 B", result)
}
