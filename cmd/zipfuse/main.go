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
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/desertwitch/zipfuse/internal/webgui"
	"github.com/dustin/go-humanize"
)

const (
	stackTraceBuffer = 1 << 24
)

// Version is the program version (filled in from the Makefile).
var Version string

func main() {
	var exitCode int
	var wg sync.WaitGroup

	defer func() { os.Exit(exitCode) }()

	logging.Printf("zipfuse %s\n", Version)
	root, mount := parseArgsOrExit(os.Args)

	c, err := fuse.Mount(mount, fuse.ReadOnly(), fuse.AllowOther(), fuse.FSName("zipfuse"))
	if err != nil {
		logging.Printf("Mount error: %v\n", err)
		exitCode = 1

		return
	}
	defer c.Close()
	defer fuse.Unmount(mount) //nolint:errcheck

	wg.Go(func() {
		if err := fs.Serve(c, &filesystem.FS{RootDir: root}); err != nil {
			logging.Printf("FS serve error: %v\n", err)
			exitCode = 1
		}
	})

	webgui.AppVersion = Version
	srv := webgui.Serve(":8000")
	defer srv.Close()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			logging.Println("Signal received, unmounting the filesystem...")

			if err := fuse.Unmount(mount); err != nil {
				logging.Printf("Unmount error: %v (try again later)\n", err)

				continue
			}

			return
		}
	}()

	sig2 := make(chan os.Signal, 1)
	signal.Notify(sig2, syscall.SIGUSR1)
	go func() {
		for range sig2 {
			logging.Println("Signal received, printing stacktrace to standard error (stderr)...")
			buf := make([]byte, stackTraceBuffer)
			stacklen := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:stacklen])
		}
	}()

	wg.Wait()
}

func parseArgsOrExit(args []string) (root string, mount string) { //nolint:nonamedreturns
	if len(args) < 4 { //nolint:mnd
		logging.Printf("Usage: %s <root-dir> <mountpoint> <streaming-threshold>\n", args[0])
		os.Exit(1)
	}

	root, mount = args[1], args[2]
	threshold, err := humanize.ParseBytes(args[3])

	if root == "" || mount == "" || threshold <= 0 || err != nil {
		logging.Printf("Usage: %s <root-dir> <mountpoint> <streaming-threshold>\n", args[0])
		if err != nil {
			logging.Printf("Error: %v", err)
		}
		os.Exit(1)
	}

	filesystem.StreamingThreshold.Store(threshold)

	return root, mount
}
