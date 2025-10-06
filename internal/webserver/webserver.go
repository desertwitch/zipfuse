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
	"sync/atomic"
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

	errMissingArgument = errors.New("missing argument")
)

// FSDashboard is the implementation for the filesystem dashboard.
type FSDashboard struct {
	version string
	fsys    *filesystem.FS
	rbuf    *logging.RingBuffer
}

// NewFSDashboard returns a pointer to a new [FSDashboard].
func NewFSDashboard(fsys *filesystem.FS, rbuf *logging.RingBuffer, version string) (*FSDashboard, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: need filesystem", errMissingArgument)
	}
	if rbuf == nil {
		return nil, fmt.Errorf("%w: need ring buffer", errMissingArgument)
	}

	return &FSDashboard{
		version: version,
		fsys:    fsys,
		rbuf:    rbuf,
	}, nil
}

type fsDashboardData struct {
	AllocBytes          string   `json:"allocBytes"`
	AvgExtractSpeed     string   `json:"avgExtractSpeed"`
	AvgExtractTime      string   `json:"avgExtractTime"`
	AvgMetadataReadTime string   `json:"avgMetadataReadTime"`
	CacheEnabled        string   `json:"cacheEnabled"`
	CacheSize           int      `json:"cacheSize"`
	CacheTTL            string   `json:"cacheTtl"`
	ClosedZips          int64    `json:"closedZips"`
	FlatMode            string   `json:"flatMode"`
	Logs                []string `json:"logs"`
	MustCRC32           string   `json:"mustCrc32"`
	NumGC               uint32   `json:"numGc"`
	OpenedZips          int64    `json:"openedZips"`
	OpenZips            int64    `json:"openZips"`
	ReopenedEntries     int64    `json:"reopenedEntries"`
	RingBufferSize      int      `json:"ringBufferSize"`
	StreamingThreshold  string   `json:"streamingThreshold"`
	SysBytes            string   `json:"sysBytes"`
	TotalAlloc          string   `json:"totalAlloc"`
	TotalExtractBytes   string   `json:"totalExtractBytes"`
	TotalExtracts       int64    `json:"totalExtracts"`
	TotalLruHits        int64    `json:"totalLruHits"`
	TotalLruMisses      int64    `json:"totalLruMisses"`
	TotalLruRatio       string   `json:"totalLruRatio"`
	TotalMetadatas      int64    `json:"totalMetadatas"`
	Version             string   `json:"version"`
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

	mux.HandleFunc("/set/cache/{value}",
		d.booleanHandler("LRU cache enabled", &d.fsys.Options.CacheDisabled))
	mux.HandleFunc("/set/checkall/{value}",
		d.booleanHandler("Forced integrity checking", &d.fsys.Options.MustCRC32))
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
		AllocBytes:          humanize.Bytes(m.Alloc),
		AvgExtractSpeed:     d.avgExtractSpeed(),
		AvgExtractTime:      d.avgExtractTime(),
		AvgMetadataReadTime: d.avgMetadataReadTime(),
		CacheEnabled:        enabledOrDisabled(!d.fsys.Options.CacheDisabled.Load()),
		CacheSize:           d.fsys.Options.CacheSize,
		CacheTTL:            d.fsys.Options.CacheTTL.String(),
		ClosedZips:          d.fsys.Metrics.TotalClosedZips.Load(),
		FlatMode:            enabledOrDisabled(d.fsys.Options.FlatMode),
		Logs:                lines,
		MustCRC32:           enabledOrDisabled(d.fsys.Options.MustCRC32.Load()),
		NumGC:               m.NumGC,
		OpenedZips:          d.fsys.Metrics.TotalOpenedZips.Load(),
		OpenZips:            d.fsys.Metrics.OpenZips.Load(),
		ReopenedEntries:     d.fsys.Metrics.TotalReopenedEntries.Load(),
		RingBufferSize:      d.rbuf.Size(),
		StreamingThreshold:  humanize.Bytes(d.fsys.Options.StreamingThreshold.Load()),
		SysBytes:            humanize.Bytes(m.Sys),
		TotalAlloc:          humanize.Bytes(m.TotalAlloc),
		TotalExtractBytes:   d.totalExtractBytes(),
		TotalExtracts:       d.fsys.Metrics.TotalExtractCount.Load(),
		TotalLruHits:        d.fsys.Metrics.TotalLruHits.Load(),
		TotalLruMisses:      d.fsys.Metrics.TotalLruMisses.Load(),
		TotalLruRatio:       d.totalLruRatio(),
		TotalMetadatas:      d.fsys.Metrics.TotalMetadataReadCount.Load(),
		Version:             d.version,
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
	d.fsys.Metrics.TotalClosedZips.Store(0)
	d.fsys.Metrics.TotalExtractBytes.Store(0)
	d.fsys.Metrics.TotalExtractCount.Store(0)
	d.fsys.Metrics.TotalExtractTime.Store(0)
	d.fsys.Metrics.TotalLruHits.Store(0)
	d.fsys.Metrics.TotalLruMisses.Store(0)
	d.fsys.Metrics.TotalMetadataReadCount.Store(0)
	d.fsys.Metrics.TotalMetadataReadTime.Store(0)
	d.fsys.Metrics.TotalOpenedZips.Store(0)
	d.fsys.Metrics.TotalReopenedEntries.Store(0)

	d.rbuf.Println("Metrics reset via API.")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Metrics reset.")
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

func (d *FSDashboard) booleanHandler(descr string, target *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		val, err := strconv.ParseBool(vars["value"])
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid boolean value: %v", err), http.StatusBadRequest)

			return
		}
		target.Store(val)

		d.rbuf.Printf("%s set via API: %t.\n", descr, val)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s set: %t.\n", descr, val)
	}
}
