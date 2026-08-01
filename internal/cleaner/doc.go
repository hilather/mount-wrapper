// Package cleaner purges absent archives after grace and manages overlay quarantine.
//
// # Grace purge
//
// After an archive is marked absent (scanner / reconcile), rows remain until
// cleanup_after elapses from removed_at. Cleaner.PurgeAbsentPastGrace lists
// those rows via state.ListAbsentPastGrace and runs PurgeArchive on each.
//
// # Overlay policy (config overlay_cleanup)
//
//   - quarantine (default): move overlay under overlay_dir/.quarantine/<id>-<ts>
//   - delete: remove the overlay tree
//   - retain: leave the overlay directory in place
//
// Quarantine entries are age-pruned (quarantine_retain_for) and optionally
// size-capped (quarantine_max_bytes, oldest first).
//
// # Admin purge
//
// Cleaner.PurgeArchive is the immediate purge path (control plane / CLI).
// It unmounts when an Unmounter is configured, deletes the index file (when
// under index_dir), applies overlay policy (when under overlay_dir), removes
// an unused mount directory (when under mount_root), then DELETEs the DB row.
//
// # Reappear vs cleaner
//
// Scanner.Reappear transitions absent → discovered and clears removed_at while
// keeping overlay_path / index_path. Cleaner only grace-purges rows that are
// still absent with removed_at past grace, so a reappear before grace means
// the cleaner never sees the row. Admin purge is explicit and may still purge
// a rediscovered archive if invoked by id. Cleaner does not fight the scanner:
// it never re-marks absent or re-touches reappeared rows.
//
// # Outer nonsolid cache hygiene
//
// Cleaner.PruneNonsolidCache (part of Run) maintains convert_7z_cache_dir
// (or DefaultNonsolidCacheDir): always strip leftover *.nonsolid.partial and
// *.nonsolid.partial.work; remove stale *.lock when the sibling *.7z is gone
// (skip when flock is held); age-prune orphaned *.7z (and
// *.tarmount-convert.json / .lock siblings) older than cleanup_after.
// No separate config key — reuses cleanup_after. Deletes only under the
// resolved cache root; LivePaths entries are skipped for age prune.
//
// # Path safety
//
// Filesystem deletes/moves are refused unless the target resolves under the
// configured root (index_dir, overlay_dir, mount_root, overlay_dir/.quarantine,
// or convert_7z_cache_dir / default nonsolid cache).
// Paths outside those roots are left on disk; purge may still remove the DB row.
//
// This package is a library; the serve loop / control plane wire it in later.
package cleaner
