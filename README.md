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

`zipfuse` is a tailored, read-only FUSE filesystem that exposes any directories
and `.zip` archives of an underlying filesystem as both regular directories and
files. This means it internally handles in-memory unpacking, streaming and
serving `.zip` archives and all their contained files, so that consumers need
not know or care about `.zip` archive mechanics. It includes a HTTP dashboard
for basic filesystem metrics and controlling operations and runtime behavior.

The filesystem is realized in Go and strives to remain as simple as possible,
while also ideally fully leveraging kernel-side caching for better performance.
In contrast to similar filesystems, it does not mount single `.zip` files, but
instead gracefully handles any `.zip` archives contained in another filesystem.

```bash
make all
mkdir /mnt/zipfuse
./zipfuse /mnt/albums /mnt/zipfuse --memsize 10M --webaddr :8000
```

In the example above, the `.zip` archives are contained in `/mnt/albums` and the
target mount is at `/mnt/zipfuse`. `10M` describes the streaming threshold, at
which individual `.zip`-contained files are no longer entirely loaded in memory
but streamed to the kernel in chunks instead (bytes as requested by the kernel).

The diagnostics server was configured on `:8000`, exposing the routes:
- `/` for filesystem dashboard and event ring-buffer
- `/gc` for forcing of a garbage collection (within Go)
- `/reset` for resetting the filesystem metrics at runtime
- `/set/checkall/<bool>` for adapting forced integrity checking
- `/set/threshold/<string>` for adapting of the streaming threshold

The following signals are observed and handled by the filesystem:
- `SIGTERM` or `SIGINT` (CTRL+C) gracefully unmounts the filesystem
- `SIGUSR1` forces a garbage collection (within Go)
- `SIGUSR2` dumps a diagnostic stacktrace to standard error (`stderr`)

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

In the example above the target is started immediately and also at system boot.

### Path Flattening Mode

Some users may specifically want the `--flatten` argument when mounting their
filesystem, so it does not waste resources in recreating structures from within
`.zip` archives, but rather flattens any such structures so that only files
remain within one shallow virtual directory per `.zip` archive. In order to
avoid filename collisions, a deterministic portion of an `SHA-1` hash is then
appended to every one of these files (8 digits to also avoid hash collisions):

```
/mnt/albums/test.zip/dir1/file.txt -> /mnt/zipfuse/test/file_V3321D81.txt
/mnt/albums/test.zip/dir2/file.txt -> /mnt/zipfuse/test/file_A8371A86.txt
```

While this may seem unusual at first, one could assume all to process `.zip`
archives as shallow and containing few files. Flattening would help to reduce
unnecessary directory traversals for processing consumer software. For a photo
gallery software to treat every `.zip` archive as a shallow album and not
trigger additional creations of subalbums could also be one factor. In the end,
photo filenames themselves rarely matter, as long as order and sorting remains.

## Security, Contributions, and License

Security is not a priority for this **personal purpose-driven project**. It is
running in self-hosted, appropriately secured environments so that it does not
have to be. Stability and long-term, hands-off operation are however paramount
to it, due to the very multi-layered nature. Feel free to fork this project as
needed, or open issues and pull requests if you notice any glaring issues - but
please do approach any such with the perspective of it being a mere tool for a
tailored, specific purpose. **All code is licensed under the MIT license.**
