// Package webserver implements the diagnostics server.
package webserver

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	templateFS    embed.FS
	indexTemplate = template.Must(template.ParseFS(templateFS, "templates/index.html"))

	// errInvalidArgument is for an invalid constructor argument.
	errInvalidArgument = errors.New("invalid argument")
)

// FSDashboard is the implementation of the filesystem dashboard.
type FSDashboard struct {
	version string
	fsys    *filesystem.FS
	rbuf    *logging.RingBuffer
}

// NewFSDashboard returns a pointer to a new [FSDashboard].
func NewFSDashboard(fsys *filesystem.FS, rbuf *logging.RingBuffer, version string) (*FSDashboard, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: need filesystem", errInvalidArgument)
	}
	if rbuf == nil {
		return nil, fmt.Errorf("%w: need ring buffer", errInvalidArgument)
	}

	return &FSDashboard{
		version: version,
		fsys:    fsys,
		rbuf:    rbuf,
	}, nil
}

// Serve serves the diagnostics dashboard as part of a [http.Server].
func (d *FSDashboard) Serve(addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: d.dashboardMux()}

	go func() {
		defer func() {
			r := recover()
			if r != nil {
				fmt.Fprintf(os.Stderr, "(webserver) PANIC: %v\n", r)
				debug.PrintStack()
			}
		}()
		d.rbuf.Printf("serving dashboard on %s\n", addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.rbuf.Printf("HTTP error: %v\n", err)
		}
	}()

	return srv
}

func (d *FSDashboard) dashboardMux() *mux.Router {
	mux := mux.NewRouter()

	mux.HandleFunc("/", d.dashboardHandler)
	mux.HandleFunc("/metrics.json", d.metricsHandler)
	mux.HandleFunc("/gc", d.gcHandler)
	mux.HandleFunc("/reset", d.resetMetricsHandler)

	mux.HandleFunc("/set/fd-cache-bypass/{value}",
		d.booleanHandler("FD cache bypass", &d.fsys.Options.FDCacheBypass))
	mux.HandleFunc("/set/must-crc32/{value}",
		d.booleanHandler("Forced integrity checking", &d.fsys.Options.MustCRC32))
	mux.HandleFunc("/set/stream-threshold/{value}", d.thresholdHandler)

	mux.HandleFunc("/zipfuse.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(assets.Logo)
	})
	// mux.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	return mux
}

type fsDashboardData struct {
	AllocBytes          string   `json:"allocBytes"`
	AvgExtractSpeed     string   `json:"avgExtractSpeed"`
	AvgExtractTime      string   `json:"avgExtractTime"`
	AvgMetadataReadTime string   `json:"avgMetadataReadTime"`
	FDCacheBypass       string   `json:"fdCacheBypass"`
	FDCacheSize         int      `json:"fdCacheSize"`
	FDCacheTTL          string   `json:"fdCacheTtl"`
	FDLimit             int      `json:"fdLimit"`
	FlatMode            string   `json:"flatMode"`
	ForceUnicode        string   `json:"forceUnicode"`
	Logs                []string `json:"logs"`
	MustCRC32           string   `json:"mustCrc32"`
	NumGC               uint32   `json:"numGc"`
	OpenZips            int64    `json:"openZips"`
	RingBufferSize      int      `json:"ringBufferSize"`
	StreamingThreshold  string   `json:"streamingThreshold"`
	StreamPoolHitAvg    string   `json:"streamPoolHitAvg"`
	StreamPoolHitRatio  string   `json:"streamPoolHitRatio"`
	StreamPoolHits      int64    `json:"streamPoolHits"`
	StreamPoolMissAvg   string   `json:"streamPoolMissAvg"`
	StreamPoolMisses    int64    `json:"streamPoolMisses"`
	StreamPoolSize      string   `json:"streamPoolSize"`
	StrictCache         string   `json:"strictCache"`
	SysBytes            string   `json:"sysBytes"`
	TotalAlloc          string   `json:"totalAlloc"`
	TotalClosedZips     int64    `json:"totalClosedZips"`
	TotalErrors         int64    `json:"totalErrors"`
	TotalExtractBytes   string   `json:"totalExtractBytes"`
	TotalExtracts       int64    `json:"totalExtracts"`
	TotalFDCacheHits    int64    `json:"totalFdCacheHits"`
	TotalFDCacheMisses  int64    `json:"totalFdCacheMisses"`
	TotalFDCacheRatio   string   `json:"totalFdCacheRatio"`
	TotalMetadatas      int64    `json:"totalMetadatas"`
	TotalOpenedZips     int64    `json:"totalOpenedZips"`
	TotalStreamRewinds  int64    `json:"totalStreamRewinds"`
	Uptime              string   `json:"uptime"`
	Version             string   `json:"version"`
}

func (d *FSDashboard) collectMetrics() fsDashboardData {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	lines := d.rbuf.Lines()
	slices.Reverse(lines)

	return fsDashboardData{
		AllocBytes:          humanize.IBytes(m.Alloc),
		AvgExtractSpeed:     d.avgExtractSpeed(),
		AvgExtractTime:      d.avgExtractTime(),
		AvgMetadataReadTime: d.avgMetadataReadTime(),
		FDCacheBypass:       enabledOrDisabled(d.fsys.Options.FDCacheBypass.Load()),
		FDCacheSize:         d.fsys.Options.FDCacheSize,
		FDCacheTTL:          d.fsys.Options.FDCacheTTL.String(),
		FDLimit:             d.fsys.Options.FDLimit,
		FlatMode:            enabledOrDisabled(d.fsys.Options.FlatMode),
		ForceUnicode:        enabledOrDisabled(d.fsys.Options.ForceUnicode),
		Logs:                lines,
		MustCRC32:           enabledOrDisabled(d.fsys.Options.MustCRC32.Load()),
		NumGC:               m.NumGC,
		OpenZips:            d.fsys.Metrics.OpenZips.Load(),
		RingBufferSize:      d.rbuf.Size(),
		StreamingThreshold:  humanize.IBytes(d.fsys.Options.StreamingThreshold.Load()),
		StreamPoolHitAvg:    d.streamPoolHitAvgSize(),
		StreamPoolHitRatio:  d.streamPoolHitRatio(),
		StreamPoolHits:      d.fsys.Metrics.TotalStreamPoolHits.Load(),
		StreamPoolMissAvg:   d.streamPoolMissAvgSize(),
		StreamPoolMisses:    d.fsys.Metrics.TotalStreamPoolMisses.Load(),
		StreamPoolSize:      humanize.IBytes(uint64(d.fsys.Options.StreamPoolSize)),
		StrictCache:         enabledOrDisabled(d.fsys.Options.StrictCache),
		SysBytes:            humanize.IBytes(m.Sys),
		TotalAlloc:          humanize.IBytes(m.TotalAlloc),
		TotalClosedZips:     d.fsys.Metrics.TotalClosedZips.Load(),
		TotalErrors:         d.fsys.Metrics.Errors.Load(),
		TotalExtractBytes:   d.totalExtractBytes(),
		TotalExtracts:       d.fsys.Metrics.TotalExtractCount.Load(),
		TotalFDCacheHits:    d.fsys.Metrics.TotalFDCacheHits.Load(),
		TotalFDCacheMisses:  d.fsys.Metrics.TotalFDCacheMisses.Load(),
		TotalFDCacheRatio:   d.totalFDCacheRatio(),
		TotalMetadatas:      d.fsys.Metrics.TotalMetadataReadCount.Load(),
		TotalOpenedZips:     d.fsys.Metrics.TotalOpenedZips.Load(),
		TotalStreamRewinds:  d.fsys.Metrics.TotalStreamRewinds.Load(),
		Uptime:              humanize.Time(d.fsys.MountTime),
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

	d.rbuf.Printf("GC forced via API, current heap: %s.\n", humanize.IBytes(m.Alloc))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "GC forced, current heap: %s.\n", humanize.IBytes(m.Alloc))
}

func (d *FSDashboard) resetMetricsHandler(w http.ResponseWriter, _ *http.Request) {
	d.fsys.Metrics.Errors.Store(0)
	d.fsys.Metrics.TotalOpenedZips.Store(0)
	d.fsys.Metrics.TotalClosedZips.Store(0)
	d.fsys.Metrics.TotalStreamRewinds.Store(0)
	d.fsys.Metrics.TotalMetadataReadTime.Store(0)
	d.fsys.Metrics.TotalMetadataReadCount.Store(0)
	d.fsys.Metrics.TotalExtractTime.Store(0)
	d.fsys.Metrics.TotalExtractCount.Store(0)
	d.fsys.Metrics.TotalExtractBytes.Store(0)
	d.fsys.Metrics.TotalFDCacheHits.Store(0)
	d.fsys.Metrics.TotalFDCacheMisses.Store(0)
	d.fsys.Metrics.TotalStreamPoolHits.Store(0)
	d.fsys.Metrics.TotalStreamPoolMisses.Store(0)
	d.fsys.Metrics.TotalStreamPoolHitBytes.Store(0)
	d.fsys.Metrics.TotalStreamPoolMissBytes.Store(0)

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

	d.rbuf.Printf("Streaming threshold set via API: %s.\n", humanize.IBytes(val))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Streaming threshold set: %s.\n", humanize.IBytes(val))
}

func (d *FSDashboard) booleanHandler(desc string, target *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		val, err := strconv.ParseBool(vars["value"])
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid boolean value: %v", err), http.StatusBadRequest)

			return
		}
		target.Store(val)

		d.rbuf.Printf("%s set via API: %t.\n", desc, val)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s set: %t.\n", desc, val)
	}
}
