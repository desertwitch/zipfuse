<div align="left">
    <img alt="Logo" src="assets/zipfuse.png" width="150">
    <br><br>
    <a href="https://codecov.io/gh/desertwitch/zipfuse" target="_blank"><img src="https://codecov.io/gh/desertwitch/zipfuse/graph/badge.svg?token=SENW4W2GQL"/></a>
    <a href="https://github.com/desertwitch/zipfuse/releases" target="_blank"><img alt="Release" src="https://img.shields.io/github/release/desertwitch/zipfuse.svg"></a>
    <a href="https://go.dev/"><img alt="Go Version" src="https://img.shields.io/badge/go-%3E%3D%201.25.1-%23007d9c" target="_blank"></a>
    <a href="https://pkg.go.dev/github.com/desertwitch/zipfuse" target="_blank"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/desertwitch/zipfuse.svg"></a>
    <a href="https://goreportcard.com/report/github.com/desertwitch/zipfuse" target="_blank"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/desertwitch/zipfuse"></a>
    <a href="./LICENSE" target="_blank"><img alt="License" src="https://img.shields.io/github/license/desertwitch/zipfuse"></a>
    <br>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml" target="_blank"><img alt="Lint" src="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golang-tests.yml" target="_blank"><img alt="Tests" src="https://github.com/desertwitch/zipfuse/actions/workflows/golang-tests.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/zipfuse-cli.yml" target="_blank"><img alt="Integration CLI" src="https://github.com/desertwitch/zipfuse/actions/workflows/zipfuse-cli.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/zipfuse-fstab.yml" target="_blank"><img alt="Integration Fstab" src="https://github.com/desertwitch/zipfuse/actions/workflows/zipfuse-fstab.yml/badge.svg"></a>
</div><br>

>**Note: This software is under active development.**  
> CLI arguments and documentation may change until a stable (v1.0.0) release.

<img alt="Example" src="assets/example.gif" width="650"><br>

# ZipFUSE Filesystem

`zipfuse` is a read-only FUSE filesystem that mirrors another filesystem, but
exposing only its contained ZIP archives as files and folders. It handles
in-memory enumeration, chunked streaming and on-the-fly extraction - so that
consumers remain entirely unaware of an archive being involved. It includes a
HTTP webserver for a responsive diagnostics dashboard and runtime configurables.

The filesystem strives to remain simple and purpose-driven, while also utilizing
caching both in userspace and on the kernel side for improved performance. In
contrast to similar filesystems, it does not mount single ZIP archives, but
handles any ZIP archives contained within a filesystem without re-mounting.

While initially developed entirely for a personal need and being [used with
photo albums](./examples/zipgallery), it is organically growing into a far more
general-purpose direction, so that it can be useful for other applications also.

## Building from source

To build from source, a `Makefile` is included with the project's source code.
Running `make all` will compile the application and pull in any necessary
dependencies. `make check` runs the test suite and static analysis tools.

The Makefile assumes a **Go installation (1.25.1+)** as a prerequisite.

```bash
git clone https://github.com/desertwitch/zipfuse.git
cd zipfuse
make all
./zipfuse --help
```

Running `make all` produces two binaries:
- `zipfuse` - binary of the FUSE filesystem
- `mount.zipfuse` - binary of the FUSE mount helper

The latter is needed only for mounting with `mount(8)` or `/etc/fstab`.  

## Installing the filesystem

You will need to ensure that you have FUSE (`libfuse`, `fuse3`...) installed on
the system that you are planning to use `zipfuse` on. The **only hard dependency**
of `zipfuse` **is the `fusermount3` binary**, so ensure it exists in your `$PATH`.

The recommended location to install FUSE filesystems to can differ between Linux
distributions. Most important is that you install the binaries to a location
that is covered in your `$PATH` environment variable. A common and relatively
portable solution would be installing the `zipfuse` binary into `/bin` and the
`mount.zipfuse` binary into `/sbin` on your system. You have to ensure that the
files have the appropriate permissions set for users intending to execute them,
specifically the executable bit needs to be set on both binaries (`chmod +x`).

As can be derived from the recommended paths above, the `zipfuse` binary itself
does not need elevated permissions. In contrast, the `mount.zipfuse` is usually
executed by the system as `root` (when processing `/etc/fstab`), but will (when
configured to do so) execute the filesystem binary as a given unprivileged user.

## Mounting the filesystem
### Mounting with command-line or `systemd` service (recommended):

The `zipfuse` filesystem binary runs as a foreground process and is ideal for
`systemd` wrapping, or use directly from command-line as either a foreground or
background (paired with `nohup` and/or `&`) process. For continous usage,
integration into the larger `systemd` framework is recommended and preferable.

**For mounting using the command-line:**
```
zipfuse <source> <mountpoint> [flags]
```

`<source>` is the root of the underlying filesystem to expose.  
`<mountpoint>` is the mountpoint where the FUSE filesystem will appear.

**For mounting using a `systemd` service unit:**
```ini
[Unit]
Description=ZipFUSE

[Service]
Type=simple
ExecStart=/usr/local/bin/zipfuse /home/alice/zips /home/alice/zipfuse --webserver :8000
Restart=on-failure
RestartSec=5
TimeoutStartSec=30
TimeoutStopSec=30
KillSignal=SIGTERM
User=alice
Group=alice

[Install]
WantedBy=multi-user.target
```

**It is not recommended to use a `.mount` unit over a `.service` unit.**  
The reason is that a `.mount` unit would again rely on the FUSE mount helper.  
For more complex orchestration with `systemd`, see also inside the [examples](./examples) folder.

**The above are the recommended and modern approaches for almost all use cases.**

---

### Mounting with `mount(8)` and `/etc/fstab`:

For users not able to use `systemd`, a FUSE mount helper is provided, so the
filesystem can be used with `mount(8)` or also `/etc/fstab` entry. This usually
**requires putting the `mount.zipfuse` binary into `/sbin`** or another location
that the `mount(8)` program examines for the filesystem helper binaries.

**For mounting using the `mount(8)` program:**
```
sudo mount -t zipfuse /home/alice/zips /home/alice/zipfuse -o setuid=alice,allow_other,webserver=:8000
```

**For mounting using an entry in the `/etc/fstab` file:**
```
# <file system>   <mount point>   <type>   <options>   <dump>   <pass>
/home/alice/zips   /home/alice/zipfuse   zipfuse   setuid=alice,allow_other,webserver=:8000   0  0
```

**Additional mount options to control mount helper behavior itself:**
```
setuid=USER (as username or UID; overrides executing user)
xbin=/full/path/to/zipfuse/binary (overrides filesystem binary)
xlog=/full/path/to/writeable/logfile (overrides filesystem logfile)
xtim=SECS (numeric and in seconds; overrides filesystem mount timeout)
```

**As you can see, program options (read more below) need format conversion:**  
`--allow-other --webserver :8000` is turning into `allow_other,webserver=:8000`

Note that FUSE mount helper events are printed to standard error (`stderr`).  
Any filesystem events are printed to `/var/log/zipfuse.log` (if it is writeable).

## Program options and configurables

```
zipfuse <source> <mountpoint> [flags]
```

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| --allow-other `<bool>` | -a | false | Allow other system users to access the mounted filesystem. |
| --dry-run `<bool>` | -d | false | Do not mount; instead print all would-be inodes and paths to standard output. |
| --fd-cache-bypass `<bool>` | (none) | false | Disable file descriptor caching; open/close a new file descriptor on every single request. |
| --fd-cache-size `<int>` | (none) | (70% of `fd-limit`) | Maximum open file descriptors to retain in cache (for more performant re-accessing). |
| --fd-cache-ttl `<duration>` | (none) | 60s | Time-to-live before evicting cached file descriptors (that are not in use). |
| --fd-limit `<int>` | (none) | (50% of OS soft limit) | Maximum total open file descriptors at any given time (must be > `fd-cache-size`). |
| --flatten-zips `<bool>` | -f | false | Flatten ZIP-contained subdirectories into one directory per ZIP archive. |
| --force-unicode `<bool>` | (none) | true | Unicode (or fallback to synthetic generated) paths for ZIPs; disabling garbles non-compliant ZIPs when trying to be interpreted as unicode. |
| --must-crc32 `<bool>` | (none) | false | Force integrity verification for non-compressed ZIP archives (slower). |
| --ring-buffer-size `<int>` | (none) | 500 | Lines of the in-memory event ring-buffer (as served in the diagnostics dashboard). |
| --stream-pool-size `<size>` | (none) | 128KiB | Buffer size for the streamed read buffer pool (multiplies with concurrency). |
| --stream-threshold `<size>` | -s | 1MiB | Files larger than this are streamed in chunks, instead of fully loaded into RAM. |
| --strict-cache `<bool>` | (none) | false | Do not treat ZIP files/contents as immutable (non-changing) for caching decisions. |
| --verbose `<bool>` | -v | false | Print all FUSE communication and diagnostics to standard error. |
| --version | (none) | false | Print the program version to standard output. |
| --webserver `<addr>` | -w | (empty) | Address for the diagnostics dashboard (e.g. `:8000`). If unset, the webserver is disabled. |

Size parameters accept human-readable formats like `1024`, `128KB`, `128KiB`, `10MB`, or `10MiB`.  
Duration parameters accept Go duration formats like `30s`, `5m`, `1h`, or combined values like `1h30m`.

### Examples:

Mount `/home/alice/zips` onto `/home/alice/zipfuse` and serve dashboard on port 8080:

    zipfuse /home/alice/zips /home/alice/zipfuse --webserver :8080

Dry-run to inspect would-be inodes and files without actual mounting:

    zipfuse /home/alice/zips /home/alice/zipfuse --dry-run

## Runtime routes and signals handling

When enabled, the diagnostics server exposes the following routes:
- `/` for filesystem dashboard and event ring-buffer
- `/gc` for forcing of a garbage collection (within Go)
- `/reset` for resetting the filesystem metrics at runtime
- `/set/must-crc32/<bool>` for adapting forced integrity checking
- `/set/fd-cache-bypass/<bool>` for bypassing the file descriptor cache
- `/set/stream-threshold/<string>` for adapting of the streaming threshold

The following signals are observed and handled by the filesystem:
- `SIGTERM` or `SIGINT` (CTRL+C) gracefully unmounts the filesystem
- `SIGUSR1` forces a garbage collection (within Go)
- `SIGUSR2` dumps a diagnostic stacktrace to standard error (`stderr`)

## Security, Contributions, and License

The filesystem is read-only, purpose-built and assumes more or less static
content being served for a few consuming applications. While it may well be
possible it works for larger-scale operations or in more complex environments,
it was not built for such and should always be used with appropriate cautions.

The webserver is disabled by default. When enabled, it is unsecured and assumes
an otherwise appropriately secured environment (a modern reverse proxy,
firewall, ...) to prevent any unauthorized access to the runtime configurables.

Feel free to fork this project as needed, or open issues and pull requests if
you notice issues or otherwise wish to add features - but please do approach
them with perspective of it originally being a personal, small-scale project.

**All code is licensed under the MIT license.**
