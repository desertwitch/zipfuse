package main

const (
	helpTextLong = `%s (%s) - FUSE mount helper

This program is a helper for the mount/fstab mechanism.
It is normally located in /sbin or another directory
searched by mount(8) for filesystem helpers, and is
not intended to be invoked directly by the end users.

Usage:
  %s source mountpoint [-o key[=value],key[=value],...]

For running the filesystem as another (e.g. unprivileged) user:
  %s source mountpoint -o setuid=USER[,key[=value],...]

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
Filesystem events are printed to %q (if it is writeable).`

	helpErrNotFound = `mount.zipfuse error: zipfuse not found within $PATH dirs.
Perhaps you installed it into some non-standard directory?
Some operating systems also mangle the environment variable.
Do try to pass "xbin=/full/path/to/binary" as a mount option.`

	helpErrMountTimeout = `mount.zipfuse error: mount did not appear within %d seconds.
You can raise this timeout by passing "xtim=SECS" as a mount option.
But beware default timeouts usually suffice and indicate error conditions.
So first do try checking %q for more (error) information.`
)
