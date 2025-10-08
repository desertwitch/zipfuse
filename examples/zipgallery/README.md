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

Some users may specifically want the `--flatten-zips` argument when mounting
their filesystem, so it does not waste resources in recreating structures from
within `.zip` archives, but rather flattens any such structures so that only
files remain within one shallow virtual directory per `.zip` archive. In order
to avoid filename collisions, a deterministic portion of an `SHA-1` hash is then
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
