/*
mount.zipfuse - FUSE mount helper

This program is a helper for the mount/fstab mechanism.
It is normally located in /sbin or another directory
searched by mount(8) for filesystem helpers, and is
not intended to be invoked directly by the end users.

Usage:
  mount.zipfuse source mountpoint [-o key[=value],key[=value],...]

For running the filesystem as another (e.g. unprivileged) user:
  mount.zipfuse source mountpoint -o setuid=USER[,key[=value],...]

Example (fstab entry):
  /mnt/zips   /mnt/zipfuse   zipfuse   allow_other,webserver=:8000   0  0

Additional mount options to control mount helper behavior itself:
  setuid=USER (as username or UID; overrides executing user)
  xbin=/full/path/to/zipfuse/binary (overrides filesystem binary)
  xlog=/full/path/to/writeable/logfile (overrides filesystem logfile)
  xtim=SECS (numeric and in seconds; overrides filesystem mount timeout)

Filesystem-specific options need to be adapted into this format:
  --webserver :8000 --strict-cache => webserver=:8000,strict_cache

Note that FUSE mount helper events are printed to standard error (stderr).
Filesystem events are printed to "/var/log/zipfuse.log" (if it is writeable).
*/
//nolint:mnd,err113
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultType    = "zipfuse"
	defaultLogfile = "/var/log/zipfuse.log"
	defaultTimeout = 20 * time.Second
)

var (
	// Version is the program version (filled in from the Makefile).
	Version string

	// allowedKeys is a map of known arguments to the ZipFUSE program.
	allowedKeys = map[string]struct{}{
		"fd-cache-bypass":  {},
		"force-unicode":    {},
		"must-crc32":       {},
		"strict-cache":     {},
		"allow-other":      {},
		"dry-run":          {},
		"flatten-zips":     {},
		"verbose":          {},
		"fd-cache-ttl":     {},
		"fd-cache-size":    {},
		"fd-limit":         {},
		"ring-buffer-size": {},
		"stream-pool-size": {},
		"stream-threshold": {},
		"webserver":        {},
	}
)

// mountHelper is the principal implementation of the FUSE mount helper.
type mountHelper struct {
	Program    string
	Binary     string
	Type       string
	Source     string
	Mountpoint string
	Options    map[string]string
	Setuid     string
	Logfile    string
	Timeout    time.Duration
}

// newMountHelper parses arguments and returns a new [mountHelper] on success.
func newMountHelper(args []string) (*mountHelper, error) {
	mh := &mountHelper{
		Program:    args[0],
		Source:     args[1],
		Type:       defaultType,
		Mountpoint: args[2],
		Options:    make(map[string]string),
		Logfile:    defaultLogfile,
		Timeout:    defaultTimeout,
	}

	if mh.Source == "" {
		return nil, errors.New("no source argument was given")
	}
	if mh.Mountpoint == "" {
		return nil, errors.New("no mountpoint argument was given")
	}

	basename := filepath.Base(mh.Program)
	if after, ok := strings.CutPrefix(basename, "mount.fuse."); ok {
		mh.Type = after
	} else if after0, ok0 := strings.CutPrefix(basename, "mount.fuseblk."); ok0 {
		mh.Type = after0
	}

	err := mh.parseOptions(args[3:])
	if err != nil {
		return nil, fmt.Errorf("failed to parse options: %w", err)
	}

	if mh.Type == "" {
		err := mh.deriveTypeFromSource()
		if err != nil {
			return nil, fmt.Errorf("failed to derive fs type: %w", err)
		}
	}

	return mh, nil
}

// parseOptions parses the mount options from the provided argument slice.
func (mh *mountHelper) parseOptions(args []string) error {
	for i := 0; i < len(args); i++ { //nolint:intrange
		arg := args[i]

		if arg == "-v" || arg == "-o" {
			continue
		}

		if arg == "-t" {
			err := mh.deriveTypeFromArg(&i, args)
			if err != nil {
				return fmt.Errorf("failed to derive type: %w", err)
			}

			continue
		}

		for opt := range strings.SplitSeq(arg, ",") {
			if opt == "" {
				continue
			}
			opt = strings.ReplaceAll(opt, "_", "-")
			opt = strings.TrimPrefix(opt, "--")

			if strings.Contains(opt, "=") { // key=value
				parts := strings.SplitN(opt, "=", 2)
				key := parts[0]
				val := parts[1]

				_, ok := allowedKeys[key]

				switch {
				case key == "xbin":
					mh.Binary = val

				case key == "xlog":
					mh.Logfile = val

				case key == "xtim":
					secs, err := strconv.Atoi(val)
					if err != nil {
						return fmt.Errorf("failed to parse %q value %q: %w", key, val, err)
					}
					if secs <= 0 {
						return fmt.Errorf("failed to use %q value %q: must be > 0", key, val)
					}
					mh.Timeout = time.Duration(secs) * time.Second

				case key == "setuid":
					mh.Setuid = val

				case ok:
					mh.Options[key] = val
				}
			} else { // key
				if _, ok := allowedKeys[opt]; ok {
					mh.Options[opt] = ""
				}
			}
		}
	}

	return nil
}

// deriveTypeFromArg tries to deduct the filesystem type from a "-t" argument.
func (mh *mountHelper) deriveTypeFromArg(i *int, args []string) error {
	*i++
	if *i >= len(args) {
		return errors.New("missing type value to argument \"-t\"")
	}
	t := args[*i]
	if after, ok := strings.CutPrefix(t, "fuse."); ok {
		t = after
	} else if after0, ok0 := strings.CutPrefix(t, "fuseblk."); ok0 {
		t = after0
	}
	if t == "" {
		return errors.New("empty type value to argument \"-t\"")
	}
	mh.Type = t

	return nil
}

// deriveTypeFromSource tries to deduct the filesystem type from the source.
func (mh *mountHelper) deriveTypeFromSource() error {
	parts := strings.SplitN(mh.Source, "#", 2)

	if len(parts) > 1 {
		mh.Type = parts[0]
		mh.Source = parts[1]
	} else {
		return errors.New("source argument is not in format \"type#source\"")
	}

	if mh.Type == "" {
		return errors.New("empty type value before '#' in source argument")
	}
	if mh.Source == "" {
		return errors.New("empty source value after '#' in source argument")
	}

	return nil
}

func main() {
	if len(os.Args) < 3 {
		progName := filepath.Base(os.Args[0])
		fmt.Fprintf(os.Stderr, helpTextLong+"\n",
			progName, Version, progName, progName, defaultLogfile)
		os.Exit(1)
	}

	helper, err := newMountHelper(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount.zipfuse error: %v\n", err)
		os.Exit(1)
	}

	err = helper.Execute()
	if err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			fmt.Fprintln(os.Stderr, helpErrNotFound)

		case errors.Is(err, errMountTimeout):
			fmt.Fprintf(os.Stderr, helpErrMountTimeout+"\n",
				int(helper.Timeout.Seconds()), helper.Logfile)

		default:
			fmt.Fprintf(os.Stderr, "mount.zipfuse error: %v\n", err)
		}

		os.Exit(1)
	}
}
