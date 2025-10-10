package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	"github.com/desertwitch/zipfuse/internal/filesystem"
	"github.com/desertwitch/zipfuse/internal/logging"
	"golang.org/x/sys/unix"
)

//nolint:mnd,err113,nonamedreturns
func fdLimits() (fsLimit int, cacheLimit int, err error) {
	var rlim unix.Rlimit

	if e := unix.Getrlimit(unix.RLIMIT_NOFILE, &rlim); e != nil {
		return 0, 0, fmt.Errorf("failed to get rlimit: %w", e)
	}

	if rlim.Cur == unix.RLIM_INFINITY {
		rlim.Cur = 1 << 20
	}

	if rlim.Cur == 0 {
		return 0, 0, fmt.Errorf("got invalid rlimit: %d", rlim.Cur)
	}

	if rlim.Cur > math.MaxInt {
		return 0, 0, fmt.Errorf("rlimit too large: %d", rlim.Cur)
	}

	osLimit := int(rlim.Cur)
	fsLimit = osLimit / 2             // 50% of OS limit
	cacheLimit = (fsLimit * 70) / 100 // 70% of FS limit

	if fsLimit < 1 || cacheLimit < 1 {
		return 0, 0, fmt.Errorf("calculations too small (soft=%d)", osLimit)
	}

	return fsLimit, cacheLimit, nil
}

func setupSignalHandlers(fsys *filesystem.FS, rbuf *logging.RingBuffer, mountDir string) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer recoverSignalsPanic()
		for range sig {
			rbuf.Println("Signal received, unmounting the filesystem...")

			errs := make(chan error, 1)
			fsys.PrepareUnmount(errs)
			if err := fuse.Unmount(mountDir); err != nil {
				errs <- err
				close(errs)

				rbuf.Printf("Unmount error: %v (try again later)\n", err)

				continue
			}
			close(errs)

			return
		}
	}()

	sig1 := make(chan os.Signal, 1)
	signal.Notify(sig1, syscall.SIGUSR1)
	go func() {
		defer recoverSignalsPanic()
		for range sig1 {
			rbuf.Println("Signal received, forcing garbage collection...")
			runtime.GC()
			debug.FreeOSMemory()
		}
	}()

	sig2 := make(chan os.Signal, 1)
	signal.Notify(sig2, syscall.SIGUSR2)
	go func() {
		defer recoverSignalsPanic()
		for range sig2 {
			rbuf.Println("Signal received, printing stacktrace to standard error...")
			buf := make([]byte, stackTraceBufferSize)
			stacklen := runtime.Stack(buf, true)
			os.Stderr.Write(buf[:stacklen])
		}
	}()
}

func dryWalkFS(fsys *filesystem.FS) error {
	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer recoverSignalsPanic()
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

func recoverSignalsPanic() {
	r := recover()
	if r != nil {
		fmt.Fprintf(os.Stderr, "(signals) PANIC: %v\n", r)
		debug.PrintStack()
	}
}
