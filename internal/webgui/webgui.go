// Package webgui implements the diagnostics dashboard for the filesystem.
package webgui

import (
	"embed"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"text/template"

	"github.com/desertwitch/zipfuse/assets"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/mux"
)

var (
	// AppVersion is the version of the program, as displayed in the dashboard.
	AppVersion string

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
	mux.HandleFunc("/gc", gcHandler)
	mux.HandleFunc("/reset-metrics", resetMetricsHandler)
	mux.HandleFunc("/threshold/{value}", thresholdHandler)

	mux.HandleFunc("/zipfuse.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(assets.Logo)
	})

	return mux
}

func dashboardHandler(w http.ResponseWriter, _ *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	data := struct {
		AppVersion          string
		LogBufferSize       int
		OpenZips            int64
		OpenedZips          int64
		ClosedZips          int64
		StreamingThreshold  string
		AllocBytes          string
		TotalAlloc          string
		SysBytes            string
		NumGC               uint32
		AvgMetadataReadTime string
		TotalMetadatas      int64
		AvgExtractTime      string
		AvgExtractSpeed     string
		TotalExtracts       int64
		TotalExtractBytes   string
		Logs                string
	}{
		AppVersion:          AppVersion,
		LogBufferSize:       logging.Buffer.Size(),
		OpenZips:            filesystem.OpenZips.Load(),
		OpenedZips:          filesystem.TotalOpenedZips.Load(),
		ClosedZips:          filesystem.TotalClosedZips.Load(),
		StreamingThreshold:  humanize.Bytes(filesystem.StreamingThreshold.Load()),
		AllocBytes:          humanize.Bytes(m.Alloc),
		TotalAlloc:          humanize.Bytes(m.TotalAlloc),
		SysBytes:            humanize.Bytes(m.Sys),
		NumGC:               m.NumGC,
		AvgMetadataReadTime: avgMetadataReadTime(),
		TotalMetadatas:      filesystem.TotalMetadataReadCount.Load(),
		AvgExtractTime:      avgExtractTime(),
		AvgExtractSpeed:     avgExtractSpeed(),
		TotalExtracts:       filesystem.TotalExtractCount.Load(),
		TotalExtractBytes:   totalExtractBytes(),
		Logs:                strings.Join(logging.Buffer.Lines(), "\n"),
	}

	if err := indexTemplate.Execute(w, data); err != nil {
		logging.Printf("HTTP template execution error: %v\n", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func gcHandler(w http.ResponseWriter, _ *http.Request) {
	runtime.GC()
	debug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	logging.Printf("GC forced via /gc, current heap: %s.\n", humanize.Bytes(m.Alloc))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "GC forced, current heap: %s.\n", humanize.Bytes(m.Alloc))
}

func resetMetricsHandler(w http.ResponseWriter, _ *http.Request) {
	filesystem.TotalMetadataReadTime.Store(0)
	filesystem.TotalMetadataReadCount.Store(0)
	filesystem.TotalExtractTime.Store(0)
	filesystem.TotalExtractCount.Store(0)
	filesystem.TotalExtractBytes.Store(0)
	filesystem.TotalOpenedZips.Store(0)
	filesystem.TotalClosedZips.Store(0)

	logging.Println("Metrics reset via /reset-metrics.")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Metrics reset.")
}

func thresholdHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	val, err := humanize.ParseBytes(vars["value"])
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid threshold: %v", err), http.StatusBadRequest)

		return
	}
	filesystem.StreamingThreshold.Store(val)

	logging.Printf("Streaming threshold set via /threshold: %s.\n", humanize.Bytes(val))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Streaming threshold set: %s.\n", humanize.Bytes(val))
}
