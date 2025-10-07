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
  - "/set/must-crc32/<bool>" for adapting forced integrity checking
  - "/set/fd-cache-bypass/<bool>" for bypassing the file descriptor cache
  - "/set/stream-threshold/<string>" for adapting of the streaming threshold
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
	"golang.org/x/sys/unix"
)

const (
	stackTraceBufferSize = 1 << 24
	ringBufferSize       = 500
)

var (
	// Version is the program version (filled in from the Makefile).
	Version string

	// errInvalidArgument for an invalid CLI argument/value provided.
	errInvalidArgument = errors.New("invalid argument")
)

type cliOptions struct {
	allowOther         bool
	dryRun             bool
	fdCacheBypass      bool
	fdCacheSize        int
	fdCacheTTL         time.Duration
	fdLimit            int
	flatMode           bool
	fuseVerbose        bool
	mountDir           string
	mustCRC32          bool
	poolBufferSize     uint64
	poolBufferSizeRaw  string
	rootDir            string
	streamThreshold    uint64
	streamThresholdRaw string
	webserverAddr      string
}

//nolint:mnd
func fdLimit() (fsLimit int, cacheLimit int, e error) {
	var rlim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &rlim); err != nil {
		return 0, 0, fmt.Errorf("failed to get rlimit: %w", e)
	}

	osLimit := int(rlim.Cur)
	if osLimit <= 0 {
		return 0, 0, fmt.Errorf("invalid os limit: %d", osLimit)
	}

	fsLimit = osLimit / 2                     // 50% of OS limit
	cacheLimit = int(float64(fsLimit) * 0.70) // 70% of FS limit

	if fsLimit < 1 || cacheLimit < 1 {
		return 0, 0, fmt.Errorf("calculated values too small (soft=%d)", osLimit)
	}

	return fsLimit, cacheLimit, nil
}

//nolint:mnd
func rootCmd() *cobra.Command {
	var opts cliOptions

	fsLimit, cacheLimit, err := fdLimit()
	if err != nil {
		fsLimit = filesystem.DefaultOptions().FDLimit
		cacheLimit = filesystem.DefaultOptions().FDCacheSize

		fmt.Fprintf(os.Stderr, "Error: Failed to get OS file descriptor limit: %v\n", err)
		fmt.Fprintln(os.Stderr, "Using fallback as defaults, tune with --fd-limit and --fd-cache-size.")
	}

	cmd := &cobra.Command{
		Use:     helpTextUse,
		Short:   helpTextShort,
		Long:    helpTextLong,
		Version: Version,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if opts.fdLimit <= opts.fdCacheSize {
				return fmt.Errorf("%w: fd-limit cannot be <= fd-cache-size", errInvalidArgument)
			}
			opts.streamThreshold, err = humanize.ParseBytes(opts.streamThresholdRaw)
			if err != nil {
				return fmt.Errorf("%w: failed to parse memsize: %w", errInvalidArgument, err)
			}
			opts.poolBufferSize, err = humanize.ParseBytes(opts.poolBufferSizeRaw)
			if err != nil {
				return fmt.Errorf("%w: failed to parse poolsize: %w", errInvalidArgument, err)
			}
			opts.rootDir = args[0]
			opts.mountDir = args[1]

			return run(opts)
		},
	}
	cmd.PersistentFlags().BoolP("version", "", false, "version for zipfuse") // removes -v shorthand

	cmd.Flags().BoolVarP(&opts.fdCacheBypass, "fd-cache-bypass", "b", false, "Bypass the FD cache; (re-)opens and closes file descriptors on every request")
	cmd.Flags().BoolVarP(&opts.allowOther, "allow-other", "a", true, "Allow other users to access the filesystem")
	cmd.Flags().BoolVarP(&opts.dryRun, "dry-run", "d", false, "Do not mount, but print all would-be inodes and paths to standard output (stdout)")
	cmd.Flags().BoolVarP(&opts.flatMode, "flatten-zips", "f", false, "Flatten ZIP-contained subdirectories and their files into one directory per ZIP")
	cmd.Flags().BoolVarP(&opts.fuseVerbose, "verbose", "v", false, "Print all verbose FUSE communication and diagnostics to standard error (stderr)")
	cmd.Flags().BoolVarP(&opts.mustCRC32, "must-crc32", "m", false, "Force integrity verification on non-compressed ZIP files also (at performance cost)")
	cmd.Flags().DurationVarP(&opts.fdCacheTTL, "fd-cache-ttl", "t", 60*time.Second, "Time-to-live before FD cache evicts unused open file descriptors")
	cmd.Flags().IntVarP(&opts.fdCacheSize, "fd-cache-size", "c", cacheLimit, "Max number of open file descriptors in the FD cache (must be < fd-limit)")
	cmd.Flags().IntVarP(&opts.fdLimit, "fd-limit", "l", fsLimit, "Limit of total open file descriptors (> fd-cache-size; beware OS limits)")
	cmd.Flags().StringVarP(&opts.poolBufferSizeRaw, "pool-buffer-size", "p", "128KiB", "Buffer size for the file read buffer pool (beware this multiplies)")
	cmd.Flags().StringVarP(&opts.streamThresholdRaw, "stream-threshold", "s", "10MiB", "Size cutoff for loading a file fully into RAM (streaming instead)")
	cmd.Flags().StringVarP(&opts.webserverAddr, "webserver", "w", "", "Address to serve the diagnostics dashboard on (e.g. :8000; but disabled when empty)")

	return cmd
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func dryWalkFS(fsys *filesystem.FS) error {
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
		return nil
	}

	for {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			// Return the deepest error, and not the whole chain.
			// The node-produced error messages will show the details.
			return fmt.Errorf("fs walk error: %w", err)
		}
		err = unwrapped
	}
}

func setupSignalHandlers(fsys *filesystem.FS, unmountDir string, rbuf *logging.RingBuffer) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sig {
			rbuf.Println("Signal received, unmounting the filesystem...")

			v := fsys.HaltPurgeCache()
			if err := fuse.Unmount(unmountDir); err != nil {
				fsys.Options.FDCacheBypass.Store(v)
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
		FDCacheSize:    opts.fdCacheSize,
		FDCacheTTL:     opts.fdCacheTTL,
		FDLimit:        opts.fdLimit,
		FlatMode:       opts.flatMode,
		PoolBufferSize: int(opts.poolBufferSize),
	}
	fopts.FDCacheBypass.Store(opts.fdCacheBypass)
	fopts.MustCRC32.Store(opts.mustCRC32)
	fopts.StreamingThreshold.Store(opts.streamThreshold)

	fsys, err := filesystem.NewFS(opts.rootDir, fopts, rbuf)
	if err != nil {
		return fmt.Errorf("failed to establish fs: %w", err)
	}
	defer fsys.Cleanup()

	if opts.dryRun {
		return dryWalkFS(fsys)
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
	defer fsys.HaltPurgeCache()

	setupSignalHandlers(fsys, opts.mountDir, rbuf)

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

	if opts.webserverAddr != "" {
		dash, err := webserver.NewFSDashboard(fsys, rbuf, Version)
		if err != nil {
			return fmt.Errorf("failed to establish dashboard: %w", err)
		}

		srv := dash.Serve(opts.webserverAddr)
		defer srv.Close()
	}

	wg.Wait()

	return <-errChan
}
