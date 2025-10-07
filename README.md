<div align="left">
    <img alt="Logo" src="assets/zipfuse.png" width="150">
    <br><br>
    <img src="https://img.shields.io/badge/.zip-%E2%99%A5_FUSE-red">
    <a href="https://github.com/desertwitch/zipfuse/tags" target="_blank"><img alt="Release" src="https://img.shields.io/github/tag/desertwitch/zipfuse.svg"></a>
    <a href="https://go.dev/"><img alt="Go Version" src="https://img.shields.io/badge/go-%3E%3D%201.25.1-%23007d9c" target="_blank"></a>
    <a href="https://pkg.go.dev/github.com/desertwitch/zipfuse" target="_blank"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/desertwitch/zipfuse.svg"></a>
    <a href="https://goreportcard.com/report/github.com/desertwitch/zipfuse" target="_blank"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/desertwitch/zipfuse"></a>
    <a href="./LICENSE" target="_blank"><img alt="License" src="https://img.shields.io/github/license/desertwitch/zipfuse"></a>
    <br>
    <a href="https://codecov.io/gh/desertwitch/zipfuse" target="_blank"><img src="https://codecov.io/gh/desertwitch/zipfuse/graph/badge.svg?token=SENW4W2GQL"/></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml" target="_blank"><img alt="Lint" src="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golang-tests.yml" target="_blank"><img alt="Tests" src="https://github.com/desertwitch/zipfuse/actions/workflows/golang-tests.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build.yml" target="_blank"><img alt="Build" src="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build.yml/badge.svg"></a>
</div>

## ZipFUSE Filesystem

`zipfuse` is a read-only FUSE filesystem that mirrors another filesystem, but
exposing only its contained `.zip` archives as files and folders. It handles
in-memory enumeration, chunked streaming and on-the-fly extraction - so that
consumers remain entirely unaware of an archive being involved. It includes a
HTTP webserver for a responsive diagnostics dashboard and runtime configurables.

The filesystem strives to remain simple and purpose-driven, while also utilizing
caching both in userspace and on the kernel side for improved performance. In
contrast to similar filesystems, it does not mount single `.zip` archives, but
handles any `.zip` archives contained within a filesystem without re-mounting.

While initially developed entirely for a personal need and being [used with
photo albums](./examples/zipgallery), it is organically growing into a far more
general-purpose direction, so that it can be useful for other applications also.

### Building from source:

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

The [examples](./examples) folder contains possible integration examples.  
**Pre-compiled static binaries are planned to be offered starting v1.0.0.**

### Runtime routes and signals handling:

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
