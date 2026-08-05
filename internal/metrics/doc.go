// Package metrics computes archive/index/extracted sizes and space-saved formulas.
//
// Pure formulas (always available without FUSE or indexes):
//
//	space_saved_bytes            = max(0, extracted − index)
//	space_saved_vs_archive_bytes = max(0, extracted − archive − index)
//	convert_size_delta_bytes     = archive_size − convert_source_size
//
// Extracted logical size (primary extracted_size_bytes used by space_saved):
//
//   - Deep leaf when the ratarmount index fully describes nested content
//     (flattened recursiondepth rows; expanded nested archive blobs are not
//     double-counted with their leaves).
//   - Shallow when nested archive members remain opaque in the index (packed
//     size only) — metrics do not invent deep sizes from those blobs; quality
//     fields (extracted_nesting, opaque_nested_*) flag incompleteness.
//   - Mount walk for live mounts when index deep is incomplete, or when
//     prefer_mount / index missing (existing fallback; capped by MaxFiles/Timeout).
//
// Offline expansion of opaque zip/7z members without FUSE is intentionally out
// of scope. File and index inspection is interface-injected (SizeProvider,
// ExtractedSizeProvider / IndexAnalyzer) so unit tests use fakes;
// FSSizeProvider and DefaultExtractedProvider talk to the real filesystem and
// modernc.org/sqlite.
//
// CollectorConfig + QueryOptions model cache TTL, no_cache, and prefer_mount for the
// MetricsCollector interface used by control/API layers.
//
// The collector TTL cache is dual-keyed by (archive_id, prefer_mount) so an
// index-first warm entry cannot be returned for a prefer_mount query (and vice
// versa); both variants keep independent TTLs until Invalidate. Cache Get/Put/
// Invalidate are concurrency-safe (sync.RWMutex).
package metrics
