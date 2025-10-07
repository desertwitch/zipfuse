package main

const (
	helpTextUse = "zipfuse <root-dir> <mountpoint>"

	helpTextShort = "a read-only FUSE filesystem for browsing of ZIP files"

	helpTextLong = `zipfuse is a FUSE filesystem that shows ZIP files as flattened, browseable
directories - it unpacks, streams and serves files straight from memory (RAM).

When mounted, the following OS signals are observed at runtime:
- SIGTERM/SIGINT for gracefully unmounting the FS
- SIGUSR1 for forcing a garbage collection run within Go
- SIGUSR2 for printing a stack trace to standard error (stderr)

When enabled, the diagnostics dashboard exposes the following routes:
- "/" for filesystem dashboard and event ring-buffer
- "/gc" for forcing of a garbage collection (within Go)
- "/reset" for resetting the filesystem metrics at runtime
- "/set/must-crc32/<bool>" for adapting forced integrity checking
- "/set/fd-cache-bypass/<bool>" for bypassing the file descriptor cache
- "/set/stream-threshold/<string>" for adapting of the streaming threshold`
)
