/*
zipfuse is a FUSE filesystem that shows ZIP files as flattened, browseable
directories - it unpacks, streams and serves files straight from memory (RAM).
*/
package main

import (
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

const (
	logBufferLinesMax = 500
	stackTraceBuffer  = 1 << 24

	fileBasePerm = 0o444 // RO
	dirBasePerm  = 0o555 // RO
)

var (
	// Version is the program version (filled in from the Makefile).
	Version string

	logs               *logBuffer
	streamingThreshold atomic.Uint64

	openZips   atomic.Int64
	openedZips atomic.Int64
	closedZips atomic.Int64

	totalMetadataReadTime  atomic.Int64
	totalMetadataReadCount atomic.Int64

	totalExtractTime  atomic.Int64
	totalExtractCount atomic.Int64
	totalExtractBytes atomic.Int64
)

func main() {
	var exitCode int
	var wg sync.WaitGroup

	defer func() { os.Exit(exitCode) }()

	logs = newLogBuffer(logBufferLinesMax)
	logPrintf("zipfuse %s\n", Version)
	root, mount := parseArgsOrExit(os.Args)

	c, err := fuse.Mount(mount, fuse.ReadOnly(), fuse.AllowOther(), fuse.FSName("zipfuse"))
	if err != nil {
		logPrintf("Mount error: %v\n", err)
		exitCode = 1

		return
	}
	defer c.Close()
	defer fuse.Unmount(mount) //nolint:errcheck

	wg.Go(func() {
		if err := fs.Serve(c, &zipFS{root}); err != nil {
			logPrintf("FS serve error: %v\n", err)
			exitCode = 1
		}
	})

	srv := serveMetrics(":8000")
	defer srv.Close()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			logPrintln("Signal received, unmounting the filesystem...")

			if err := fuse.Unmount(mount); err != nil {
				logPrintf("Unmount error: %v (try again later)\n", err)

				continue
			}

			return
		}
	}()

	sig2 := make(chan os.Signal, 1)
	signal.Notify(sig2, syscall.SIGUSR1)
	go func() {
		for range sig2 {
			logPrintln("Signal received, printing stacktrace to standard error (stderr)...")
			buf := make([]byte, stackTraceBuffer)
			stacklen := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:stacklen])
		}
	}()

	wg.Wait()
}
