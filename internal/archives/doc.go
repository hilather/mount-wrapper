// Package archives handles optional relocate of sources onto Linux filesystem
// paths and path classification (archives_dir / converter output).
//
// Relocate (move_archives_to_linux):
//   - ArchiveFilePath — permanent path under archives_dir with basename collision
//     suffix `--{archive_id[:8]}`
//   - ShouldRelocate — whether a record should move before first index
//   - CheckRelocateSpace — free-space gate (archive + min_free + overhead)
//   - RelocateArchive — rename/move source + convert metadata sidecar
//   - RemoveSupersededSource — delete DrvFs original after convert/relocate
//
// Convert sidecar path: convert.MetadataPath (suffix .tarmount-convert.json).
//
// Parity source: tarmount-wsl archives.py.
package archives
