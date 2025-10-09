/*
zipfuse is a read-only FUSE filesystem that mirrors another filesystem, but
exposing only its contained ZIP archives as files and folders. It handles
in-memory enumeration, chunked streaming and on-the-fly extraction - so that
consumers remain entirely unaware of an archive being involved. It includes a
HTTP webserver for a responsive diagnostics dashboard and runtime configurables.

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
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
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
)

var (
	// Version is the program version (filled in from the Makefile).
	Version string

	// errInvalidArgument is for an invalid CLI argument/value provided.
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
	forceUnicode       bool
	fuseVerbose        bool
	mountDir           string
	mustCRC32          bool
	poolBufferSize     uint64
	poolBufferSizeRaw  string
	ringBufferSize     int
	rootDir            string
	streamThreshold    uint64
	streamThresholdRaw string
	webserverAddr      string
}

//nolint:mnd
func rootCmd() *cobra.Command {
	var opts cliOptions

	fsLimit, cacheLimit, err := fdLimits()
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
				return fmt.Errorf("%w: failed to parse --stream-threshold: %w", errInvalidArgument, err)
			}
			opts.poolBufferSize, err = humanize.ParseBytes(opts.poolBufferSizeRaw)
			if err != nil {
				return fmt.Errorf("%w: failed to parse --pool-buffer-size: %w", errInvalidArgument, err)
			}
			opts.rootDir = args[0]
			opts.mountDir = args[1]

			return run(opts)
		},
	}
	cmd.PersistentFlags().BoolP("version", "", false, "version for zipfuse") // removes -v shorthand

	cmd.Flags().BoolVarP(&opts.allowOther, "allow-other", "a", true, "Allow other users to access the filesystem")
	cmd.Flags().BoolVarP(&opts.dryRun, "dry-run", "d", false, "Do not mount, but print all would-be inodes and paths to standard output (stdout)")
	cmd.Flags().BoolVarP(&opts.fdCacheBypass, "fd-cache-bypass", "b", false, "Bypass the FD cache; (re-)opens and closes file descriptors on every request")
	cmd.Flags().BoolVarP(&opts.flatMode, "flatten-zips", "f", false, "Flatten ZIP-contained subdirectories and their files into one directory per ZIP")
	cmd.Flags().BoolVarP(&opts.forceUnicode, "force-unicode", "u", true, "Unicode (or generated) paths for ZIPs; disabling garbles non-compliant ZIPs")
	cmd.Flags().BoolVarP(&opts.fuseVerbose, "verbose", "v", false, "Print all verbose FUSE communication and diagnostics to standard error (stderr)")
	cmd.Flags().BoolVarP(&opts.mustCRC32, "must-crc32", "m", false, "Force integrity verification on non-compressed ZIP files also (at performance cost)")
	cmd.Flags().DurationVarP(&opts.fdCacheTTL, "fd-cache-ttl", "t", 60*time.Second, "Time-to-live before FD cache evicts unused open file descriptors")
	cmd.Flags().IntVarP(&opts.fdCacheSize, "fd-cache-size", "c", cacheLimit, "Max number of open file descriptors in the FD cache (must be < fd-limit)")
	cmd.Flags().IntVarP(&opts.fdLimit, "fd-limit", "l", fsLimit, "Limit of total open file descriptors (> fd-cache-size; beware OS limits)")
	cmd.Flags().IntVarP(&opts.ringBufferSize, "ring-buffer-size", "r", 500, "Buffer size for the event ring-buffer (displayed in diagnostics dashboard)")
	cmd.Flags().StringVarP(&opts.poolBufferSizeRaw, "pool-buffer-size", "p", "128KiB", "Buffer size for the file read buffer pool (beware this multiplies)")
	cmd.Flags().StringVarP(&opts.streamThresholdRaw, "stream-threshold", "s", "10MiB", "Size cutoff for loading a file fully into RAM (streaming instead)")
	cmd.Flags().StringVarP(&opts.webserverAddr, "webserver", "w", "", "Address to serve the diagnostics dashboard on (e.g. :8000; but disabled when empty)")

	return cmd
}

func setupFilesystem(opts cliOptions, rbuf *logging.RingBuffer) (*filesystem.FS, error) {
	fopts := &filesystem.Options{
		FDCacheSize:    opts.fdCacheSize,
		FDCacheTTL:     opts.fdCacheTTL,
		FDLimit:        opts.fdLimit,
		FlatMode:       opts.flatMode,
		ForceUnicode:   opts.forceUnicode,
		PoolBufferSize: int(opts.poolBufferSize),
	}
	fopts.FDCacheBypass.Store(opts.fdCacheBypass)
	fopts.MustCRC32.Store(opts.mustCRC32)
	fopts.StreamingThreshold.Store(opts.streamThreshold)

	fsys, err := filesystem.NewFS(opts.rootDir, fopts, rbuf)
	if err != nil {
		return nil, fmt.Errorf("fs error: %w", err)
	}

	return fsys, nil
}

func mountFilesystem(opts cliOptions) (*fuse.Conn, error) {
	mountOpts := []fuse.MountOption{fuse.FSName("zipfuse"), fuse.ReadOnly()}
	if opts.allowOther {
		mountOpts = append(mountOpts, fuse.AllowOther())
	}

	conn, err := fuse.Mount(opts.mountDir, mountOpts...)
	if err != nil {
		return nil, fmt.Errorf("fuse error: %w", err)
	}

	return conn, nil
}

func serveFilesystem(conn *fuse.Conn, fsys *filesystem.FS, verbose bool) (*sync.WaitGroup, <-chan error) {
	var wg sync.WaitGroup
	errChan := make(chan error, 1)

	wg.Go(func() {
		defer close(errChan)

		var config *fs.Config
		if verbose {
			config = &fs.Config{
				Debug: func(msg interface{}) {
					fmt.Fprintf(os.Stderr, "%s", msg)
				},
			}
		}

		srv := fs.New(conn, config)
		if err := srv.Serve(fsys); err != nil {
			errChan <- fmt.Errorf("failed to serve fs: %w", err)
		}
	})

	return &wg, errChan
}

func serveDashboard(fsys *filesystem.FS, rbuf *logging.RingBuffer, addr string) (*http.Server, error) {
	dashboard, err := webserver.NewFSDashboard(fsys, rbuf, Version)
	if err != nil {
		return nil, fmt.Errorf("dashboard error: %w", err)
	}

	return dashboard.Serve(addr), nil
}

func cleanupMount(conn *fuse.Conn, fsys *filesystem.FS, mountDir string) {
	defer conn.Close()
	defer fuse.Unmount(mountDir) //nolint:errcheck
	noErr := make(chan error, 1)
	fsys.PrepareUnmount(noErr)
	close(noErr)
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func run(opts cliOptions) error {
	rbuf := logging.NewRingBuffer(opts.ringBufferSize, os.Stderr)

	fsys, err := setupFilesystem(opts, rbuf)
	if err != nil {
		return fmt.Errorf("failed to setup fs: %w", err)
	}
	defer fsys.Destroy()

	if opts.dryRun {
		return dryWalkFS(fsys)
	}

	conn, err := mountFilesystem(opts)
	if err != nil {
		return fmt.Errorf("failed to mount fs: %w", err)
	}
	defer cleanupMount(conn, fsys, opts.mountDir)

	setupSignalHandlers(fsys, opts.mountDir, rbuf)
	wg, errChan := serveFilesystem(conn, fsys, opts.fuseVerbose)

	if opts.webserverAddr != "" {
		srv, err := serveDashboard(fsys, rbuf, opts.webserverAddr)
		if err != nil {
			return fmt.Errorf("failed to setup webserver: %w", err)
		}
		defer srv.Close()
	}

	wg.Wait()

	return <-errChan
}
