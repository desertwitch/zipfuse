package webserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

func testDashboard(t *testing.T, out io.Writer) *FSDashboard {
	t.Helper()

	tmp := t.TempDir()
	rbf := logging.NewRingBuffer(10, out)

	return NewFSDashboard(filesystem.NewFS(tmp, nil, rbf), rbf, "gotests")
}

// Expectation: Serve should return a valid HTTP server pointer.
func Test_Serve_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	srv := dash.Serve("127.0.0.1:0")
	require.NotNil(t, srv)
	require.NotEmpty(t, srv.Addr)

	defer srv.Close()
}

// Expectation: dashboardMux should register all expected routes.
func Test_dashboardMux_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	router := dash.dashboardMux()

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
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.version = "test-version"
	dash.rbuf.Println("test log entry")

	dash.fsys.Metrics.OpenZips.Store(5)
	dash.fsys.Metrics.TotalOpenedZips.Store(100)
	dash.fsys.Metrics.TotalClosedZips.Store(95)
	dash.fsys.Options.StreamingThreshold.Store(200_000_000)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	dash.dashboardHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "test-version")
	require.Contains(t, body, "test log entry")
	require.Contains(t, body, "200 MB")
}

// Expectation: metricsHandler should return JSON with current metrics.
func Test_metricsHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.version = "test-metrics-version"
	dash.rbuf.Println("metrics test log entry")

	dash.fsys.Metrics.OpenZips.Store(7)
	dash.fsys.Metrics.TotalOpenedZips.Store(123)
	dash.fsys.Metrics.TotalClosedZips.Store(120)
	dash.fsys.Options.StreamingThreshold.Store(42_000_000)

	req := httptest.NewRequest(http.MethodGet, "/metrics.json", nil)
	w := httptest.NewRecorder()

	dash.metricsHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "test-metrics-version")
	require.Contains(t, body, "metrics test log entry")
	require.Contains(t, body, "42 MB")
}

// Expectation: gcHandler should force GC and return success message.
func Test_gcHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	req := httptest.NewRequest(http.MethodGet, "/gc", nil)
	w := httptest.NewRecorder()

	dash.gcHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "GC forced")
	require.Contains(t, body, "current heap")

	logs := dash.rbuf.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "GC forced")
}

// Expectation: resetMetricsHandler should reset all metrics to zero.
func Test_resetMetricsHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Metrics.TotalMetadataReadTime.Store(1000)
	dash.fsys.Metrics.TotalMetadataReadCount.Store(10)
	dash.fsys.Metrics.TotalExtractTime.Store(2000)
	dash.fsys.Metrics.TotalExtractCount.Store(20)
	dash.fsys.Metrics.TotalExtractBytes.Store(3000)
	dash.fsys.Metrics.TotalOpenedZips.Store(30)
	dash.fsys.Metrics.TotalClosedZips.Store(40)

	req := httptest.NewRequest(http.MethodGet, "/reset", nil)
	w := httptest.NewRecorder()

	dash.resetMetricsHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Metrics reset")

	require.Zero(t, dash.fsys.Metrics.TotalMetadataReadTime.Load())
	require.Zero(t, dash.fsys.Metrics.TotalMetadataReadCount.Load())
	require.Zero(t, dash.fsys.Metrics.TotalExtractTime.Load())
	require.Zero(t, dash.fsys.Metrics.TotalExtractCount.Load())
	require.Zero(t, dash.fsys.Metrics.TotalExtractBytes.Load())
	require.Zero(t, dash.fsys.Metrics.TotalOpenedZips.Load())
	require.Zero(t, dash.fsys.Metrics.TotalClosedZips.Load())

	logs := dash.rbuf.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Metrics reset")
}

// Expectation: thresholdHandler should update threshold with valid input.
func Test_thresholdHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold/500MB", nil)
	w := httptest.NewRecorder()

	router := dash.dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Streaming threshold set")
	require.Contains(t, body, "500 MB")

	require.Equal(t, uint64(500_000_000), dash.fsys.Options.StreamingThreshold.Load())

	logs := dash.rbuf.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Streaming threshold set")
}

// Expectation: thresholdHandler should return error for invalid threshold.
func Test_thresholdHandler_InvalidThreshold_Error(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Options.StreamingThreshold.Store(100)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold/invalid", nil)
	w := httptest.NewRecorder()

	router := dash.dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "Invalid")

	require.Equal(t, uint64(100), dash.fsys.Options.StreamingThreshold.Load())
}

// Expectation: thresholdHandler should return error for empty threshold value.
func Test_thresholdHandler_EmptyThreshold_Error(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	dash.fsys.Options.StreamingThreshold.Store(100)

	req := httptest.NewRequest(http.MethodGet, "/set/threshold", nil)
	w := httptest.NewRecorder()

	router := dash.dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, uint64(100), dash.fsys.Options.StreamingThreshold.Load())
}

// Expectation: thresholdHandler should handle various threshold formats.
func Test_thresholdHandler_VariousFormats_Success(t *testing.T) {
	t.Parallel()

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
		dash := testDashboard(t, io.Discard)

		dash.fsys.Options.StreamingThreshold.Store(0)

		req := httptest.NewRequest(http.MethodGet, "/set/threshold/"+tc.input, nil)
		w := httptest.NewRecorder()

		router := dash.dashboardMux()
		router.ServeHTTP(w, req)

		resp := w.Result()
		resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, tc.expected, dash.fsys.Options.StreamingThreshold.Load())
	}
}

// Expectation: booleanHandler should update the target atomic.Bool with valid input.
func Test_booleanHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	handler := dash.booleanHandler("Forced integrity checking", &dash.fsys.Options.MustCRC32)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall/true", nil)
	req = mux.SetURLVars(req, map[string]string{"value": "true"})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := w.Body.String()
	require.Contains(t, body, "Forced integrity checking")
	require.Contains(t, body, "true")

	require.True(t, dash.fsys.Options.MustCRC32.Load())

	logs := dash.rbuf.Lines()
	require.NotEmpty(t, logs)
	require.Contains(t, strings.Join(logs, " "), "Forced integrity checking")
}

// Expectation: booleanHandler should return error for invalid boolean.
func Test_booleanHandler_InvalidBoolean_Error(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	handler := dash.booleanHandler("Forced integrity checking", &dash.fsys.Options.MustCRC32)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall/x", nil)
	req = mux.SetURLVars(req, map[string]string{"value": "x"})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := w.Body.String()
	require.Contains(t, body, "Invalid boolean value")

	require.False(t, dash.fsys.Options.MustCRC32.Load())
}

// Expectation: booleanHandler should return error for missing value.
func Test_booleanHandler_EmptyBoolean_Error(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	handler := dash.booleanHandler("Forced integrity checking", &dash.fsys.Options.MustCRC32)

	req := httptest.NewRequest(http.MethodGet, "/set/checkall", nil)
	req = mux.SetURLVars(req, map[string]string{}) // no "value"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.False(t, dash.fsys.Options.MustCRC32.Load())
}

// Expectation: Logo endpoint should serve PNG image.
func Test_logoHandler_Success(t *testing.T) {
	t.Parallel()
	dash := testDashboard(t, io.Discard)

	req := httptest.NewRequest(http.MethodGet, "/zipfuse.png", nil)
	w := httptest.NewRecorder()

	router := dash.dashboardMux()
	router.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	require.NotEmpty(t, w.Body.Bytes())
}
