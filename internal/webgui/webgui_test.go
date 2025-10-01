package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/stretchr/testify/require"
)

// Expectation: Serve should return a valid HTTP server pointer.
func Test_Serve_Success(t *testing.T) {
	srv := Serve("127.0.0.1:0")
	require.NotNil(t, srv)
	require.NotEmpty(t, srv.Addr)

	defer srv.Close()
}

// Expectation: dashboardMux should register all expected routes.
func Test_dashboardMux_Success(t *testing.T) {
	router := dashboardMux()

	testCases := []struct {
		path   string
		method string
	}{
		{"/", http.MethodGet},
		{"/gc", http.MethodGet},
		{"/reset", http.MethodGet},
		{"/set/checkall/false", http.MethodGet},
		{"/set/threshold/100MB", http.MethodGet},
		{"/zipfuse.png", http.MethodGet},
	}

	for _, tc := range testCases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		require.NotEqual(t, http.StatusNotFound, w.Code, "Route %s should exist", tc.path)
	}
}

// Expectation: dashboardHandler should render the dashboard with correct data.
func Test_dashboardHandler_Success(t *testing.T) {
	logging.Buffer.Reset()

	Version = "test-version"
	logging.Println("test log entry")

	filesystem.Metrics.OpenZips.Store(5)
	filesystem.Metrics.TotalOpenedZips.Store(100)
	filesystem.Metrics.TotalClosedZips.Store(95)
	filesystem.Options.StreamingThreshold.Store(200_000_000)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	dashboardHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "test-version")
	require.Contains(t, body, "test log entry")
	require.Contains(t, body, "200 MB")
}

// Expectation: gcHandler should force GC and return success message.
func Test_gcHandler_Success(t *testing.T) {
	logging.Buffer.Reset()

	req := httptest.NewRequest(http.MethodGet, "/gc", nil)
	w := httptest.NewRecorder()

	gcHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "GC forced")
	require.Contains(t, body, "current heap")

	logs := logging.Buffer.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "GC forced")
}

// Expectation: resetMetricsHandler should reset all metrics to zero.
func Test_resetMetricsHandler_Success(t *testing.T) {
	logging.Buffer.Reset()

	filesystem.Metrics.TotalMetadataReadTime.Store(1000)
	filesystem.Metrics.TotalMetadataReadCount.Store(10)
	filesystem.Metrics.TotalExtractTime.Store(2000)
	filesystem.Metrics.TotalExtractCount.Store(20)
	filesystem.Metrics.TotalExtractBytes.Store(3000)
	filesystem.Metrics.TotalOpenedZips.Store(30)
	filesystem.Metrics.TotalClosedZips.Store(40)

	req := httptest.NewRequest(http.MethodGet, "/reset", nil)
	w := httptest.NewRecorder()

	resetMetricsHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Metrics reset")

	require.Zero(t, filesystem.Metrics.TotalMetadataReadTime.Load())
	require.Zero(t, filesystem.Metrics.TotalMetadataReadCount.Load())
	require.Zero(t, filesystem.Metrics.TotalExtractTime.Load())
	require.Zero(t, filesystem.Metrics.TotalExtractCount.Load())
	require.Zero(t, filesystem.Metrics.TotalExtractBytes.Load())
	require.Zero(t, filesystem.Metrics.TotalOpenedZips.Load())
	require.Zero(t, filesystem.Metrics.TotalClosedZips.Load())

	logs := logging.Buffer.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Metrics reset")
}

// Expectation: mustCRC32Handler should update MustCRC32 with valid input.
func Test_mustCRC32Handler_Success(t *testing.T) {
	defer filesystem.Options.MustCRC32.Store(false)

	logging.Buffer.Reset()
	filesystem.Options.StreamingThreshold.Store(0)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall/true", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Forced integrity checking")
	require.Contains(t, body, "true")

	require.True(t, filesystem.Options.MustCRC32.Load())

	logs := logging.Buffer.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Forced integrity checking")
}

// Expectation: mustCRC32Handler should return error for invalid boolean.
func Test_mustCRC32Handler_InvalidBoolean_Error(t *testing.T) {
	logging.Buffer.Reset()
	filesystem.Options.MustCRC32.Store(false)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall/x", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "Invalid")

	require.False(t, filesystem.Options.MustCRC32.Load())
}

// Expectation: mustCRC32Handler should return error for empty value.
func Test_mustCRC32Handler_EmptyBoolean_Error(t *testing.T) {
	logging.Buffer.Reset()
	filesystem.Options.MustCRC32.Store(false)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.False(t, filesystem.Options.MustCRC32.Load())
}

// Expectation: thresholdHandler should update threshold with valid input.
func Test_thresholdHandler_Success(t *testing.T) {
	logging.Buffer.Reset()
	filesystem.Options.StreamingThreshold.Store(0)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold/500MB", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Streaming threshold set")
	require.Contains(t, body, "500 MB")

	require.Equal(t, uint64(500_000_000), filesystem.Options.StreamingThreshold.Load())

	logs := logging.Buffer.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Streaming threshold set")
}

// Expectation: thresholdHandler should return error for invalid threshold.
func Test_thresholdHandler_InvalidThreshold_Error(t *testing.T) {
	logging.Buffer.Reset()
	filesystem.Options.StreamingThreshold.Store(100)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold/invalid", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "Invalid")

	require.Equal(t, uint64(100), filesystem.Options.StreamingThreshold.Load())
}

// Expectation: thresholdHandler should return error for empty threshold value.
func Test_thresholdHandler_EmptyThreshold_Error(t *testing.T) {
	logging.Buffer.Reset()
	filesystem.Options.StreamingThreshold.Store(100)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, uint64(100), filesystem.Options.StreamingThreshold.Load())
}

// Expectation: thresholdHandler should handle various threshold formats.
func Test_thresholdHandler_VariousFormats_Success(t *testing.T) {
	testCases := []struct {
		input    string
		expected uint64
	}{
		{"1KB", 1000},
		{"1MB", 1_000_000},
		{"1GB", 1_000_000_000},
		{"100M", 100_000_000},
		{"1024", 1024},
		{"1M", 1_000_000},
	}

	for _, tc := range testCases {
		logging.Buffer.Reset()
		filesystem.Options.StreamingThreshold.Store(0)

		req := httptest.NewRequest(http.MethodGet, "/set/threshold/"+tc.input, nil)
		w := httptest.NewRecorder()

		router := dashboardMux()
		router.ServeHTTP(w, req)

		resp := w.Result()
		resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, tc.expected, filesystem.Options.StreamingThreshold.Load())
	}
}

// Expectation: Logo endpoint should serve PNG image.
func Test_logoHandler_Success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/zipfuse.png", nil)
	w := httptest.NewRecorder()

	router := dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	require.NotEmpty(t, w.Body.Bytes())
}
