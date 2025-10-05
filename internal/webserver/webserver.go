// Package webserver implements the diagnostics server.
package webserver

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
	//go:embed templates/*.html
	templateFS embed.FS

	indexTemplate = template.Must(template.ParseFS(templateFS, "templates/index.html"))
)

// FSDashboard is the principal implementation for the filesystem dashboard.
type FSDashboard struct {
	version string
	fsys    *filesystem.FS
	rbuf    *logging.RingBuffer
}

// NewFSDashboard returns a pointer to a new [FSDashboard].
func NewFSDashboard(fsys *filesystem.FS, rbuf *logging.RingBuffer, version string) *FSDashboard {
	return &FSDashboard{
		version: version,
		fsys:    fsys,
		rbuf:    rbuf,
	}
}

type fsDashboardData struct {
	Version             string   `json:"version"`
	RingBufferSize      int      `json:"ringBufferSize"`
	OpenZips            int64    `json:"openZips"`
	OpenedZips          int64    `json:"openedZips"`
	ClosedZips          int64    `json:"closedZips"`
	ReopenedEntries     int64    `json:"reopenedEntries"`
	CacheSize           int      `json:"cacheSize"`
	CacheTTL            string   `json:"cacheTtl"`
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

// Serve serves the diagnostics dashboard as part of a [http.Server].
func (d *FSDashboard) Serve(addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: d.dashboardMux()}

	go func() {
		d.rbuf.Printf("serving dashboard on %s\n", addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.rbuf.Printf("HTTP error: %v\n", err)
		}
	}()

	return srv
}

// dashboardMux describes the routes served by the diagnostics dashboard.
func (d *FSDashboard) dashboardMux() *mux.Router {
	mux := mux.NewRouter()

	mux.HandleFunc("/", d.dashboardHandler)
	mux.HandleFunc("/metrics.json", d.metricsHandler)
	mux.HandleFunc("/gc", d.gcHandler)
	mux.HandleFunc("/reset", d.resetMetricsHandler)
	mux.HandleFunc("/set/checkall/{value}", d.mustCRC32Handler)
	mux.HandleFunc("/set/threshold/{value}", d.thresholdHandler)

	mux.HandleFunc("/zipfuse.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(assets.Logo)
	})

	return mux
}

func (d *FSDashboard) collectMetrics() fsDashboardData {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	lines := d.rbuf.Lines()
	slices.Reverse(lines)

	return fsDashboardData{
		Version:             d.version,
		RingBufferSize:      d.rbuf.Size(),
		OpenZips:            d.fsys.Metrics.OpenZips.Load(),
		OpenedZips:          d.fsys.Metrics.TotalOpenedZips.Load(),
		ClosedZips:          d.fsys.Metrics.TotalClosedZips.Load(),
		ReopenedEntries:     d.fsys.Metrics.TotalReopenedEntries.Load(),
		CacheSize:           d.fsys.Options.CacheSize,
		CacheTTL:            d.fsys.Options.CacheTTL.String(),
		FlatMode:            enabledOrDisabled(d.fsys.Options.FlatMode),
		MustCRC32:           enabledOrDisabled(d.fsys.Options.MustCRC32.Load()),
		StreamingThreshold:  humanize.Bytes(d.fsys.Options.StreamingThreshold.Load()),
		AllocBytes:          humanize.Bytes(m.Alloc),
		TotalAlloc:          humanize.Bytes(m.TotalAlloc),
		SysBytes:            humanize.Bytes(m.Sys),
		NumGC:               m.NumGC,
		AvgMetadataReadTime: d.avgMetadataReadTime(),
		TotalMetadatas:      d.fsys.Metrics.TotalMetadataReadCount.Load(),
		AvgExtractTime:      d.avgExtractTime(),
		AvgExtractSpeed:     d.avgExtractSpeed(),
		TotalExtracts:       d.fsys.Metrics.TotalExtractCount.Load(),
		TotalExtractBytes:   d.totalExtractBytes(),
		Logs:                lines,
	}
}

func (d *FSDashboard) dashboardHandler(w http.ResponseWriter, _ *http.Request) {
	data := d.collectMetrics()

	if err := indexTemplate.Execute(w, data); err != nil {
		d.rbuf.Printf("HTTP template execution error: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (d *FSDashboard) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	data := d.collectMetrics()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (d *FSDashboard) gcHandler(w http.ResponseWriter, _ *http.Request) {
	runtime.GC()
	debug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	d.rbuf.Printf("GC forced via API, current heap: %s.\n", humanize.Bytes(m.Alloc))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "GC forced, current heap: %s.\n", humanize.Bytes(m.Alloc))
}

func (d *FSDashboard) resetMetricsHandler(w http.ResponseWriter, _ *http.Request) {
	d.fsys.Metrics.TotalMetadataReadTime.Store(0)
	d.fsys.Metrics.TotalMetadataReadCount.Store(0)
	d.fsys.Metrics.TotalExtractTime.Store(0)
	d.fsys.Metrics.TotalExtractCount.Store(0)
	d.fsys.Metrics.TotalExtractBytes.Store(0)
	d.fsys.Metrics.TotalOpenedZips.Store(0)
	d.fsys.Metrics.TotalClosedZips.Store(0)

	d.rbuf.Println("Metrics reset via API.")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Metrics reset.")
}

func (d *FSDashboard) mustCRC32Handler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	val, err := strconv.ParseBool(vars["value"])
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid boolean value: %v", err), http.StatusBadRequest)

		return
	}
	d.fsys.Options.MustCRC32.Store(val)

	d.rbuf.Printf("Forced integrity checking set via API: %t.\n", val)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Forced integrity checking set: %t.\n", val)
}

func (d *FSDashboard) thresholdHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	val, err := humanize.ParseBytes(vars["value"])
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid string value: %v", err), http.StatusBadRequest)

		return
	}
	d.fsys.Options.StreamingThreshold.Store(val)

	d.rbuf.Printf("Streaming threshold set via API: %s.\n", humanize.Bytes(val))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Streaming threshold set: %s.\n", humanize.Bytes(val))
}
