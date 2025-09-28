package main

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"text/template"
	"time"

	"github.com/desertwitch/zipfuse/assets"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/mux"
)

// serveMetrics serves the metrics dashboard as part of a [http.Server].
func serveMetrics(addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: diagnosticsMux()}

	go func() {
		logPrintf("serving metrics+pprof on %s\n", addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logPrintf("HTTP error: %v\n", err)
		}
	}()

	return srv
}

// diagnosticsMux describes the routes served by the metrics dashboard.
func diagnosticsMux() *mux.Router {
	mux := mux.NewRouter()

	mux.HandleFunc("/", metricsHandler)
	mux.HandleFunc("/gc", gcHandler)
	mux.HandleFunc("/reset-metrics", resetMetricsHandler)
	mux.HandleFunc("/threshold/{value}", thresholdHandler)

	mux.HandleFunc("/zipfuse.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(assets.Logo)
	})

	mux.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	return mux
}

//nolint:funlen
func metricsHandler(w http.ResponseWriter, _ *http.Request) {
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
		AppVersion:          Version,
		LogBufferSize:       logBufferLinesMax,
		OpenZips:            openZips.Load(),
		OpenedZips:          openedZips.Load(),
		ClosedZips:          closedZips.Load(),
		StreamingThreshold:  humanize.Bytes(streamingThreshold.Load()),
		AllocBytes:          humanize.Bytes(m.Alloc),
		TotalAlloc:          humanize.Bytes(m.TotalAlloc),
		SysBytes:            humanize.Bytes(m.Sys),
		NumGC:               m.NumGC,
		AvgMetadataReadTime: time.Duration(totalMetadataReadTime.Load() / max(1, totalMetadataReadCount.Load())).String(),
		TotalMetadatas:      totalMetadataReadCount.Load(),
		AvgExtractTime:      time.Duration(totalExtractTime.Load() / max(1, totalExtractCount.Load())).String(),
		AvgExtractSpeed: func() string {
			bytes := totalExtractBytes.Load()
			ns := totalExtractTime.Load()

			if ns == 0 {
				return "0 B/s"
			}

			bps := float64(bytes) / (float64(ns) / 1e9) //nolint:mnd

			return humanize.Bytes(uint64(bps)) + "/s"
		}(),
		TotalExtracts: totalExtractCount.Load(),
		TotalExtractBytes: func() string {
			bytes := totalExtractBytes.Load()

			if bytes < 0 {
				return humanize.Bytes(0)
			}

			return humanize.Bytes(uint64(bytes))
		}(),
		Logs: strings.Join(logs.lines(), "\n"),
	}

	tmpl := template.Must(template.New("metrics").Parse(`
        <html><head><title>ZipFUSE ({{.AppVersion}})</title></head><body>
        <img width=150 src="/zipfuse.png"> <b>{{.AppVersion}} / Diagnostics Server</b><br><br>
        <b>
            <a href="/debug/pprof/" target="_blank">Show Profiling</a> / 
            <a href="/reset-metrics" target="_blank">Reset Metrics</a> / 
            <a href="/gc" target="_blank">Force GC</a>
        </b>
        <ul>
            <li>ZIP handles:                     {{.OpenZips}}</li>
            <li>Total ZIP opens:                 {{.OpenedZips}}</li>
            <li>Total ZIP closes:                {{.ClosedZips}}</li>
            <li>Streaming threshold:             {{.StreamingThreshold}}</li>
            <br>
            <li>Current heap alloc:              {{.AllocBytes}}</li>
            <li>Total heap alloc:                {{.TotalAlloc}}</li>
            <li>OS memory obtained:              {{.SysBytes}}</li>
            <li>GC cycles run:                   {{.NumGC}}</li>
            <br>
            <li>Total metadata reads:            {{.TotalMetadatas}}</li>
            <li>Avg metadata read time:          {{.AvgMetadataReadTime}}</li>
            <br>
            <li>Total file extracts:             {{.TotalExtracts}}</li>
            <li>Total byte extracts:             {{.TotalExtractBytes}}</li>
            <li>Avg file extract time:           {{.AvgExtractTime}}</li>
            <li>Avg file extract speed:          {{.AvgExtractSpeed}}</li>
        </ul>
        <h3>In-Memory Ring Buffer ({{.LogBufferSize}} lines):</h3>
        <pre>{{.Logs}}</pre>
        </body></html>
    `))

	_ = tmpl.Execute(w, data)
}

func resetMetricsHandler(w http.ResponseWriter, _ *http.Request) {
	totalMetadataReadTime.Store(0)
	totalMetadataReadCount.Store(0)
	totalExtractTime.Store(0)
	totalExtractCount.Store(0)
	totalExtractBytes.Store(0)
	openedZips.Store(0)
	closedZips.Store(0)

	logPrintln("Metrics reset via /reset-metrics.")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintln(w, "Metrics reset.")
}

func gcHandler(w http.ResponseWriter, _ *http.Request) {
	runtime.GC()
	debug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	logPrintf("GC forced via /gc, current heap: %s.\n", humanize.Bytes(m.Alloc))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "GC forced, current heap: %s.\n", humanize.Bytes(m.Alloc))
}

func thresholdHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	valStr, ok := vars["value"]
	if !ok {
		http.Error(w, "Missing value", http.StatusBadRequest)

		return
	}

	val, err := humanize.ParseBytes(valStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid threshold: %v", err), http.StatusBadRequest)

		return
	}
	streamingThreshold.Store(val)

	logPrintf("Streaming threshold set via /threshold: %s.\n", humanize.Bytes(val))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "Streaming threshold set: %s.\n", humanize.Bytes(val))
}
