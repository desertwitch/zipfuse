/*
mount.zipfuse - FUSE mount helper

This program is a helper for the mount/fstab mechanism.
It is normally located in /sbin or another directory
searched by mount(8) for filesystem helpers, and is
not intended to be invoked directly by end users.

Usage:
  mount.zipfuse source mountpoint [-o key[=value],key[=value],...]

For running the filesystem as another (e.g. unprivileged) user:
  mount.zipfuse source mountpoint -o setuid=USER[,key[=value],...]

Example (fstab entry):
  /mnt/zips   /mnt/zipfuse   zipfuse   allow_other,webserver=:8000   0  0

Filesystem-specific options need to be adapted into this format:
  --webserver :8000 --strict-cache => webserver=:8000,strict_cache

Mount helper events are logged to standard error (stderr).
Filesystem events are logged to '/var/log/zipfuse.log' (if writeable).
*/
//nolint:mnd,err113
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	mountTimeout = 20 * time.Second
	mountLog     = "/var/log/zipfuse.log"
)

var (
	Version string

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

type MountHelper struct {
	Program    string
	Type       string
	Source     string
	Mountpoint string
	Options    map[string]string
	Setuid     string
}

func NewMountHelper(args []string) (*MountHelper, error) {
	mh := &MountHelper{
		Program:    args[0],
		Source:     args[1],
		Type:       "zipfuse",
		Mountpoint: args[2],
		Options:    make(map[string]string),
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

func (mh *MountHelper) parseOptions(args []string) error {
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

		for _, opt := range strings.Split(arg, ",") {
			if opt == "" {
				continue
			}
			opt = strings.ReplaceAll(opt, "_", "-")
			opt = strings.TrimPrefix(opt, "--")

			if strings.Contains(opt, "=") { // key=value
				parts := strings.SplitN(opt, "=", 2)
				key := parts[0]
				val := parts[1]

				if key == "setuid" {
					mh.Setuid = val
				} else if _, ok := allowedKeys[key]; ok {
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

func (mh *MountHelper) deriveTypeFromArg(i *int, args []string) error {
	*i++
	if *i >= len(args) {
		return errors.New("missing value to argument '-t'")
	}
	t := args[*i]
	if after, ok := strings.CutPrefix(t, "fuse."); ok {
		t = after
	} else if after0, ok0 := strings.CutPrefix(t, "fuseblk."); ok0 {
		t = after0
	}
	if t == "" {
		return errors.New("missing value to argument '-t'")
	}
	mh.Type = t

	return nil
}

func (mh *MountHelper) deriveTypeFromSource() error {
	parts := strings.SplitN(mh.Source, "#", 2) //nolint:mnd

	if len(parts) > 1 {
		mh.Type = parts[0]
		mh.Source = parts[1]
	} else {
		return errors.New("source argument is not in format 'type#source'")
	}

	if mh.Type == "" {
		return errors.New("empty type before '#' in source argument")
	}
	if mh.Source == "" {
		return errors.New("empty source after '#' in source argument")
	}

	return nil
}

func main() {
	if len(os.Args) < 3 {
		progName := filepath.Base(os.Args[0])
		fmt.Fprintf(os.Stderr, `%s (%s) - FUSE mount helper

This program is a helper for the mount/fstab mechanism.
It is normally located in /sbin or another directory
searched by mount(8) for filesystem helpers, and is
not intended to be invoked directly by end users.

Usage:
  %s source mountpoint [-o key[=value],key[=value],...]

For running the filesystem as another (e.g. unprivileged) user:
  %s source mountpoint -o setuid=USER[,key[=value],...]

Example (fstab entry):
  /mnt/zips   /mnt/zipfuse   zipfuse   allow_other,webserver=:8000   0  0

Filesystem-specific options need to be adapted into this format:
  --webserver :8000 --strict-cache => webserver=:8000,strict_cache

Mount helper events are logged to standard error (stderr).
Filesystem events are logged to '%s' (if writeable).
`, progName, Version, progName, progName, mountLog)
		os.Exit(1)
	}
	helper, err := NewMountHelper(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	err = helper.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
