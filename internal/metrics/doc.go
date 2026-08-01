// Package metrics computes archive/index/extracted sizes and space-saved formulas.
//
// Pure formulas (always available without FUSE or indexes):
//
//	space_saved_bytes            = max(0, extracted − index)
//	space_saved_vs_archive_bytes = max(0, extracted − archive − index)
//	convert_size_delta_bytes     = archive_size − convert_source_size
//
// Extracted logical size prefers a ratarmount SQLite index files table (directories
// excluded via mode & S_IFDIR). Mount-point walk is a slow fallback controlled by
// ComputeOptions / QueryOptions (prefer_mount, mount_walk).
//
// File and index inspection is interface-injected (SizeProvider, ExtractedSizeProvider)
// so unit tests use fakes; FSSizeProvider and DefaultExtractedProvider talk to the
// real filesystem and modernc.org/sqlite.
//
// CollectorConfig + QueryOptions model cache TTL, no_cache, and prefer_mount for the
// MetricsCollector interface used by control/API layers (wiring deferred).
package metrics
