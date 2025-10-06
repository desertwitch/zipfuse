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
  - "/reset" for resetting the filesystem metrics at runtime
  - "/set/checkall/<bool>" for adapting forced integrity checking
  - "/set/threshold/<string>" for adapting of the streaming threshold
*/
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"github.com/desertwitch/zipfuse/internal/webserver"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

const (
	stackTraceBufferSize = 1 << 24
	ringBufferSize       = 500
)

// Version is the program version (filled in from the Makefile).
var Version string

type cliOptions struct {
	allowOther         bool
	dashboardAddress   string
	dryRun             bool
	flatMode           bool
	lruDisable         bool
	lruSize            int
	lruTTL             time.Duration
	mountDir           string
	mustCRC32          bool
	rootDir            string
	streamThreshold    uint64
	streamThresholdRaw string
	fuseVerbose        bool
}

//nolint:mnd
func rootCmd() *cobra.Command {
	var opts cliOptions

	cmd := &cobra.Command{
		Use:     helpTextUse,
		Short:   helpTextShort,
		Long:    helpTextLong,
		Version: Version,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			var err error

			opts.streamThreshold, err = humanize.ParseBytes(opts.streamThresholdRaw)
			if err != nil {
				return fmt.Errorf("failed to parse memsize: %w", err)
			}
			opts.rootDir = args[0]
			opts.mountDir = args[1]

			return run(opts)
		},
	}
	cmd.PersistentFlags().BoolP("version", "", false, "version for zipfuse") // removes -v shorthand

	cmd.Flags().BoolVarP(&opts.allowOther, "allowother", "a", true, "Allow other users to access the filesystem")
	cmd.Flags().BoolVarP(&opts.dryRun, "dryrun", "d", false, "Do not mount, but print all would-be inodes and paths to standard output (stdout)")
	cmd.Flags().BoolVarP(&opts.flatMode, "flatten", "f", false, "Flatten ZIP-contained subdirectories and their files into one directory per ZIP")
	cmd.Flags().BoolVarP(&opts.mustCRC32, "checkall", "c", false, "Force integrity verification on non-compressed ZIP files (at performance cost)")
	cmd.Flags().BoolVar(&opts.lruDisable, "lrudisable", false, "Disable the LRU cache and re-open file descriptors on every request (beware FD limits)")
	cmd.Flags().DurationVar(&opts.lruTTL, "lrutime", 60*time.Second, "Max time before LRU cache evicts unused file descriptors (beware FD limits)")
	cmd.Flags().IntVar(&opts.lruSize, "lrusize", 60, "Max total number of file descriptors in the LRU cache (beware FD limits)")
	cmd.Flags().StringVarP(&opts.dashboardAddress, "webaddr", "w", "", "Address to serve the diagnostics dashboard on (e.g. :8000; but disabled when empty)")
	cmd.Flags().StringVarP(&opts.streamThresholdRaw, "memsize", "m", "10M", "Size cutoff for loading a file fully into RAM (streaming instead)")
	cmd.Flags().BoolVarP(&opts.fuseVerbose, "verbose", "v", false, "Print any verbose FUSE communication and diagnostics to standard error (stderr)")

	return cmd
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func walkFsAndExit(fsys *filesystem.FS) {
	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			log.Println("Signal received, cancelling the filesystem walk...")
			cancel()
		}
	}()

	err := fsys.Walk(ctx, func(path string, _ *fuse.Dirent, _ fs.Node, attr fuse.Attr) error {
		fmt.Fprintf(os.Stdout, "%d:%s\n", attr.Inode, path)

		return nil
	})
	if err == nil {
		os.Exit(0)
	}

	for {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			// Return the deepest error, and not the whole chain.
			// The node-produced error messages will show the details.
			log.Fatalf("fs walk error: %v", err)
		}
		err = unwrapped
	}
}

func setupSignalHandlers(unmountDir string, rbuf *logging.RingBuffer) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			rbuf.Println("Signal received, unmounting the filesystem...")

			if err := fuse.Unmount(unmountDir); err != nil {
				rbuf.Printf("Unmount error: %v (try again later)\n", err)

				continue
			}

			return
		}
	}()

	sig1 := make(chan os.Signal, 1)
	signal.Notify(sig1, syscall.SIGUSR1)
	go func() {
		for range sig1 {
			rbuf.Println("Signal received, forcing garbage collection...")
			runtime.GC()
			debug.FreeOSMemory()
		}
	}()

	sig2 := make(chan os.Signal, 1)
	signal.Notify(sig2, syscall.SIGUSR2)
	go func() {
		for range sig2 {
			rbuf.Println("Signal received, printing stacktrace (to stderr)...")
			buf := make([]byte, stackTraceBufferSize)
			stacklen := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:stacklen])
		}
	}()
}

func run(opts cliOptions) error {
	rbuf := logging.NewRingBuffer(ringBufferSize, os.Stderr)

	fopts := &filesystem.Options{
		CacheSize: opts.lruSize,
		CacheTTL:  opts.lruTTL,
		FlatMode:  opts.flatMode,
	}
	fopts.CacheDisabled.Store(opts.lruDisable)
	fopts.MustCRC32.Store(opts.mustCRC32)
	fopts.StreamingThreshold.Store(opts.streamThreshold)

	fsys := filesystem.NewFS(opts.rootDir, fopts, rbuf)
	if opts.dryRun {
		walkFsAndExit(fsys)
	}

	mountOpts := []fuse.MountOption{fuse.FSName("zipfuse"), fuse.ReadOnly()}
	if opts.allowOther {
		mountOpts = append(mountOpts, fuse.AllowOther())
	}

	c, err := fuse.Mount(opts.mountDir, mountOpts...)
	if err != nil {
		return fmt.Errorf("fs mount error: %w", err)
	}
	defer c.Close()
	defer fuse.Unmount(opts.mountDir) //nolint:errcheck

	setupSignalHandlers(opts.mountDir, rbuf)

	var wg sync.WaitGroup
	errChan := make(chan error, 1)
	wg.Go(func() {
		defer close(errChan)

		var config *fs.Config
		if opts.fuseVerbose {
			config = &fs.Config{
				Debug: func(msg interface{}) {
					fmt.Fprintf(os.Stderr, "%s", msg)
				},
			}
		}

		srv := fs.New(c, config)
		if err := srv.Serve(fsys); err != nil {
			errChan <- fmt.Errorf("fs serve error: %w", err)
		}
	})

	if opts.dashboardAddress != "" {
		dash := webserver.NewFSDashboard(fsys, rbuf, Version)
		srv := dash.Serve(opts.dashboardAddress)
		defer srv.Close()
	}

	wg.Wait()

	return <-errChan
}
