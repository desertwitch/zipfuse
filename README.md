<div align="left">
    <img alt="Logo" src="assets/zipfuse.png" width="150">
    <br><br>
    <a href="https://github.com/desertwitch/zipfuse/tags"><img alt="Release" src="https://img.shields.io/github/tag/desertwitch/zipfuse.svg"></a>
    <a href="https://go.dev/"><img alt="Go Version" src="https://img.shields.io/badge/go-%3E%3D%201.25.1-%23007d9c"></a>
    <a href="https://pkg.go.dev/github.com/desertwitch/zipfuse"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/desertwitch/zipfuse.svg"></a>
    <a href="https://goreportcard.com/report/github.com/desertwitch/zipfuse"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/desertwitch/zipfuse"></a>
    <a href="./LICENSE"><img alt="License" src="https://img.shields.io/github/license/desertwitch/zipfuse"></a>
    <br>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml"><img alt="Lint" src="https://github.com/desertwitch/zipfuse/actions/workflows/golangci-lint.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build.yml"><img alt="Build" src="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build.yml/badge.svg"></a>
    <a href="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build-debug.yml"><img alt="Build Debug" src="https://github.com/desertwitch/zipfuse/actions/workflows/golang-build-debug.yml/badge.svg"></a>
</div>

## ZipFUSE Filesystem

`zipfuse` is a tailored, read-only FUSE filesystem that exposes both directories
and `.zip` archives of an underlying filesystem as regular directories and
files. This means it internally handles in-memory unpacking, streaming and
serving `.zip` archives and all their contained files, so that consumers need
not know or care about the archive mechanics at all. It includes a HTTP endpoint
for basic filesystem metrics and controlling operations and behavior at runtime.

The filesystem is realized in Go and kept as simple and stateless as possible,
while also fully leveraging kernel caching with deterministic design choices.
In contrast to similar filesystems, it does not mount single `.zip` files, but
instead gracefully handles any `.zip` archives contained in another filesystem.

Paths within the `.zip` archives are converted into flat structures for
convenience and reducing complexity for the processing consumer software, with
collisions avoided by appending 8-digit `SHA-1` hash portions to all filenames.

As static binary, it can experimentally run on most Linux distributions without
any major dependencies, however it is only meant to be used in environments that
are sufficiently secured (itself being not security-centric but purpose-built).

```bash
make all
mkdir /mnt/zipfuse
./zipfuse /mnt/albums /mnt/zipfuse 200M
```

In the example above, the `.zip` archives are contained in `/mnt/albums` and the
target mount is at `/mnt/zipfuse`. `200M` describes the streaming threshold, at
which individual `.zip`-contained files are no longer entirely loaded in memory
but streamed to the kernel in chunks instead (bytes as requested by the kernel).

The following signals are observed and handled by the filesystem:
- `SIGTERM` or `SIGINT` (CTRL+C) gracefully unmounts the filesystem
- `SIGUSR1` forces a garbage collection (within Go)
- `SIGUSR2` dumps a diagnostic stacktrace to standard error (`stderr`)

The diagnostics endpoint is exposed on `:8000` with the following routes:
- `/` for filesystem dashboard and event ring-buffer
- `/gc` for forcing of a garbage collection (within Go)
- `/threshold/500MB` for adapting of the streaming threshold

## ZipGallery Project

`zipgallery` is a `systemd` stack to realize a data storage setup where any
self-hosted gallery software may be used in combination with a photo collection
where every individual album is contained within `.zip` archives. The gallery
software itself should not need to be able to handle archives, so that this is
not at all a limiting factor in the choice of the software that is being used.

This aims to allow for the user to efficiently organize their photo albums
without having to deal with any magnitude of individual files on data storage
(outside archives). The weight of dealing with any individual files is relieved
from the underlying filesystems and off-loaded instead to `zipfuse`, for the
eventual photo viewing. This increases performance on the backing filesystem,
while also allowing for choosing gallery software based on performance metrics
rather than archive handling capabilities.

The project is realized with layered `systemd` approach consisting of:
- a CIFS mount to a remote Unraid OS share containing `.zip` albums
- `zipfuse` exposing `.zip` archives as regular directories and files
- `zipweb` handling the gallery software container (`PiGallery2`) lifetime 
- `zipgallery` as a `systemd` target to glue all individual services together

The `systemd` files need adapting to one's required setup and paths, with the
defaults tied to a basic setup for personal needs (`PiGallery2` at `:42800`).

```bash
sudo cp systemd/* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now zipgallery.target
```

In the example above the target is started immediately and at system boot.

## Security, Contributions, and License

Security is not a priority for this personal purpose-driven project. It is
running in self-hosted, appropriately secured environments so that it does not
have to be. Stability and long-term, hands-off operation are however paramount
to it, due to its very multi-layered nature. Feel free to fork this project as
needed, or open issues and pull requests if you notice any glaring issues - but
please do approach any such with the perspective of it being just a tool for a
tailored, specific purpose. All code is licensed under the MIT license.
