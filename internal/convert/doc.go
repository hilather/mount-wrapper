// Package convert implements the convert pipeline library:
// archiveconverter solid→non-solid, built-in 7z nonsolid/flatten helpers,
// and ZIP→stored 7z repack predicates, cmd construction, and process runners.
//
// Runners (RunZipRepack, RunFlattenConvert) exec 7z with an injectable
// Run7zFunc for tests. Serve-loop job orchestration lives in mounter.Engine.
//
// Metadata sidecar: *.tarmount-convert.json next to the converted archive
// (parity with tarmount-wsl sevenzip_convert_metadata).
//
// Parity sources: tarmount-wsl converter.py, sevenzip_nonsolid.py,
// zip_repack.py, sevenzip_convert_metadata.py; flatten CLI path from
// ratarmountcore.nonsolid_convert (cli_flatten_7z_nonsolid).
//
// Residual gaps vs upstream:
//   - Best-effort CLI solid/nested/encrypted probe (7z l -slt heuristics:
//     Solid=+, nested member *.7z, Encrypted=+/Wrong password) via
//     CLIFlattenNeeded / DefaultFlattenNeeded — not a full ratarmountcore/py7zr
//     solid-folder parser. Conservative: false on uncertainty / missing 7z /
//     encryption. Inject FlattenNeededFunc to override.
//   - Flatten runner is best-effort CLI extract→expand nested *.7z→repack
//     (-ms=off); encrypted archives refused with Encrypted7zMessage; no
//     stream-flatten path or post-rebuild nested-header validation.
//   - Outer nonsolid cache: EnsureNonsolidCachedCopy (CLI extract + a -ms=off)
//     for solid outer/all; exclusive flock on `{cacheKey}.lock` around re-check
//     + populate; fail-closed on 7z list error/empty (no silent non-solid
//     passthrough); post-populate FlattenMinOKSize floor (under-floor dest
//     removed); post-populate re-list must pass nonsolidCacheHit (still-solid /
//     list fail / encrypted → remove dest + clear error; no stream-flatten);
//     extract/create stderr encryption → Encrypted7zMessage;
//     leftover *.nonsolid.partial / *.work cleaned before populate.
//     Cache hit (fast path + under-lock re-check) without convert sidecar:
//     best-effort size-only metadata (source/dest Stat sizes, method
//     outer-nonsolid-cli, duration omitted); no thrash-rewrite when sidecar
//     exists. Path helpers (NonsolidPartialPath / LockPath / DestFromLockPath)
//     are shared with cleaner.PruneNonsolidCache hygiene under the cache dir.
//     Path prediction via ResolveMountArchivePath / NonsolidCacheDestPath.
//     No stream-repack / stream-flatten; nested members not expanded in outer
//     cache (child env still handles nested when scope allows).
//   - Doctor availability reporting is separate (internal/doctor).
package convert
