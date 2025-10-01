/*
zipfuse is a tailored, read-only FUSE filesystem that exposes any directories
and .zip archives of an underlying filesystem as both regular directories and
files. This means it internally handles in-memory unpacking, streaming and
serving .zip archives and all their contained files, so that consumers need
not know or care about .zip archive mechanics. It includes a HTTP dashboard
for basic filesystem metrics and controlling operations and runtime behavior.

The following signals are observed and handled by the filesystem:
  - SIGTERM or SIGINT (CTRL+C) gracefully unmounts the filesystem
  - SIGUSR1 forces a garbage collection (within Go)
  - SIGUSR2 dumps a diagnostic stacktrace to standard error (stderr)

When enabled, the diagnostics server exposes the following routes over HTTP:
  - "/" for filesystem dashboard and event ring-buffer
  - "/gc" for forcing of a garbage collection (within Go)
  - "/reset-metrics" for resetting the FS metrics at runtime
  - "/threshold/<value>" for adapting of the streaming threshold
*/
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/desertwitch/zipfuse/internal/webgui"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

const (
	stackTraceBuffer = 1 << 24
)

// Version is the program version (filled in from the Makefile).
var Version string

type programOpts struct {
	flatMode         bool
	rootDir          string
	mountDir         string
	streamThreshold  uint64
	dashboardAddress string
}

func rootCmd() *cobra.Command {
	var argFlatMode bool
	var argThreshold string
	var argDashAddress string

	cmd := &cobra.Command{
		Use:   "zipfuse <root-dir> <mountpoint>",
		Short: "a read-only FUSE filesystem for browsing of ZIP files",
		Long: `zipfuse is a FUSE filesystem that shows ZIP files as flattened, browseable
directories - it unpacks, streams and serves files straight from memory (RAM).

When mounted, the following OS signals are observed at runtime:
- SIGTERM/SIGINT for gracefully unmounting the FS
- SIGUSR1 for forcing a garbage collection run within Go
- SIGUSR2 for printing a stack trace to standard error (stderr)

When enabled, the diagnostics dashboard exposes the following routes:
- "/" for filesystem dashboard and event ring-buffer
- "/gc" for forcing of a garbage collection (within Go)
- "/reset-metrics" for resetting the FS metrics at runtime
- "/threshold/<value>" for adapting of the streaming threshold`,
		Version: Version,
		Args:    cobra.ExactArgs(2), //nolint:mnd
		RunE: func(_ *cobra.Command, args []string) error {
			numThreshold, err := humanize.ParseBytes(argThreshold)
			if err != nil {
				return fmt.Errorf("failed to parse threshold: %w", err)
			}

			return run(programOpts{
				flatMode:         argFlatMode,
				rootDir:          args[0],
				mountDir:         args[1],
				streamThreshold:  numThreshold,
				dashboardAddress: argDashAddress,
			})
		},
	}
	cmd.Flags().BoolVarP(&argFlatMode, "flat", "f", false, "Flatten ZIP-contained subdirectories and their files into one directory per ZIP")
	cmd.Flags().StringVarP(&argThreshold, "memsize", "m", "200M", "Size cutoff for loading a file fully into RAM (streaming instead)")
	cmd.Flags().StringVarP(&argDashAddress, "webaddr", "w", "", "Address to serve the diagnostics dashboard on (e.g. :8000; but disabled when empty)")

	return cmd
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func run(opts programOpts) error {
	filesystem.FlatMode = opts.flatMode
	filesystem.StreamingThreshold.Store(opts.streamThreshold)

	c, err := fuse.Mount(opts.mountDir, fuse.ReadOnly(), fuse.AllowOther(), fuse.FSName("zipfuse"))
	if err != nil {
		return fmt.Errorf("fs mount error: %w", err)
	}
	defer c.Close()
	defer fuse.Unmount(opts.mountDir) //nolint:errcheck

	var wg sync.WaitGroup
	errChan := make(chan error, 1)
	wg.Go(func() {
		defer close(errChan)
		if err := fs.Serve(c, &filesystem.FS{RootDir: opts.rootDir}); err != nil {
			errChan <- fmt.Errorf("fs serve error: %w", err)
		}
	})

	if opts.dashboardAddress != "" {
		webgui.Version = Version
		srv := webgui.Serve(opts.dashboardAddress)
		defer srv.Close()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			logging.Println("Signal received, unmounting the filesystem...")

			if err := fuse.Unmount(opts.mountDir); err != nil {
				logging.Printf("Unmount error: %v (try again later)\n", err)

				continue
			}

			return
		}
	}()

	sig1 := make(chan os.Signal, 1)
	signal.Notify(sig1, syscall.SIGUSR1)
	go func() {
		for range sig1 {
			logging.Println("Signal received, forcing garbage collection...")
			runtime.GC()
			debug.FreeOSMemory()
		}
	}()

	sig2 := make(chan os.Signal, 1)
	signal.Notify(sig2, syscall.SIGUSR2)
	go func() {
		for range sig2 {
			logging.Println("Signal received, printing stacktrace (to stderr)...")
			buf := make([]byte, stackTraceBuffer)
			stacklen := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:stacklen])
		}
	}()

	wg.Wait()

	return <-errChan
}
