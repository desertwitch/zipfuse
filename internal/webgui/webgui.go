// Package webgui implements the diagnostics server.
package webgui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"text/template"

	"github.com/desertwitch/zipfuse/assets"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/mux"
)

var (
	// Version is the version of the diagnostics server.
	Version string

	//go:embed templates/*.html
	templateFS embed.FS

	indexTemplate = template.Must(template.ParseFS(templateFS, "templates/index.html"))
)

// Serve serves the diagnostics dashboard as part of a [http.Server].
func Serve(addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: dashboardMux()}

	go func() {
		logging.Printf("serving dashboard on %s\n", addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Printf("HTTP error: %v\n", err)
		}
	}()

	return srv
}

// dashboardMux describes the routes served by the diagnostics dashboard.
func dashboardMux() *mux.Router {
	mux := mux.NewRouter()

	mux.HandleFunc("/", dashboardHandler)
	mux.HandleFunc("/metrics.json", metricsHandler)
	mux.HandleFunc("/gc", gcHandler)
	mux.HandleFunc("/reset", resetMetricsHandler)
	mux.HandleFunc("/set/checkall/{value}", mustCRC32Handler)
	mux.HandleFunc("/set/threshold/{value}", thresholdHandler)

	mux.HandleFunc("/zipfuse.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(assets.Logo)
	})

	return mux
}

type dashboardData struct {
	Version             string   `json:"version"`
	RingBufferSize      int      `json:"ringBufferSize"`
	OpenZips            int64    `json:"openZips"`
	OpenedZips          int64    `json:"openedZips"`
	ClosedZips          int64    `json:"closedZips"`
	FlatMode            string   `json:"flatMode"`
	MustCRC32           string   `json:"mustCrc32"`
	StreamingThreshold  string   `json:"streamingThreshold"`
	AllocBytes          string   `json:"allocBytes"`
	TotalAlloc          string   `json:"totalAlloc"`
	SysBytes            string   `json:"sysBytes"`
	NumGC               uint32   `json:"numGc"`
	AvgMetadataReadTime string   `json:"avgMetadataReadTime"`
	TotalMetadatas      int64    `json:"totalMetadatas"`
	AvgExtractTime      string   `json:"avgExtractTime"`
	AvgExtractSpeed     string   `json:"avgExtractSpeed"`
	TotalExtracts       int64    `json:"totalExtracts"`
	TotalExtractBytes   string   `json:"totalExtractBytes"`
	Logs                []string `json:"logs"`
}

func collectMetrics() dashboardData {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	logs := logging.Buffer.Lines()
	slices.Reverse(logs)

	return dashboardData{
		Version:             Version,
		RingBufferSize:      logging.Buffer.Size(),
		OpenZips:            filesystem.Metrics.OpenZips.Load(),
		OpenedZips:          filesystem.Metrics.TotalOpenedZips.Load(),
		ClosedZips:          filesystem.Metrics.TotalClosedZips.Load(),
		FlatMode:            enabledOrDisabled(filesystem.Options.FlatMode),
		MustCRC32:           enabledOrDisabled(filesystem.Options.MustCRC32.Load()),
		StreamingThreshold:  humanize.Bytes(filesystem.Options.StreamingThreshold.Load()),
		AllocBytes:          humanize.Bytes(m.Alloc),
		TotalAlloc:          humanize.Bytes(m.TotalAlloc),
		SysBytes:            humanize.Bytes(m.Sys),
		NumGC:               m.NumGC,
		AvgMetadataReadTime: avgMetadataReadTime(),
		TotalMetadatas:      filesystem.Metrics.TotalMetadataReadCount.Load(),
		AvgExtractTime:      avgExtractTime(),
		AvgExtractSpeed:     avgExtractSpeed(),
		TotalExtracts:       filesystem.Metrics.TotalExtractCount.Load(),
		TotalExtractBytes:   totalExtractBytes(),
		Logs:                logs,
	}
}

func dashboardHandler(w http.ResponseWriter, _ *http.Request) {
	data := collectMetrics()

	if err := indexTemplate.Execute(w, data); err != nil {
		logging.Printf("HTTP template execution error: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	data := collectMetrics()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func gcHandler(w http.ResponseWriter, _ *http.Request) {
	runtime.GC()
	debug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	logging.Printf("GC forced via API, current heap: %s.\n", humanize.Bytes(m.Alloc))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "GC forced, current heap: %s.\n", humanize.Bytes(m.Alloc))
}

func resetMetricsHandler(w http.ResponseWriter, _ *http.Request) {
	filesystem.Metrics.TotalMetadataReadTime.Store(0)
	filesystem.Metrics.TotalMetadataReadCount.Store(0)
	filesystem.Metrics.TotalExtractTime.Store(0)
	filesystem.Metrics.TotalExtractCount.Store(0)
	filesystem.Metrics.TotalExtractBytes.Store(0)
	filesystem.Metrics.TotalOpenedZips.Store(0)
	filesystem.Metrics.TotalClosedZips.Store(0)

	logging.Println("Metrics reset via API.")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Metrics reset.")
}

func mustCRC32Handler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	val, err := strconv.ParseBool(vars["value"])
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid boolean value: %v", err), http.StatusBadRequest)

		return
	}
	filesystem.Options.MustCRC32.Store(val)

	logging.Printf("Forced integrity checking set via API: %t.\n", val)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Forced integrity checking set: %t.\n", val)
}

func thresholdHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	val, err := humanize.ParseBytes(vars["value"])
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid string value: %v", err), http.StatusBadRequest)

		return
	}
	filesystem.Options.StreamingThreshold.Store(val)

	logging.Printf("Streaming threshold set via API: %s.\n", humanize.Bytes(val))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Streaming threshold set: %s.\n", humanize.Bytes(val))
}
