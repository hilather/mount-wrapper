package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
)

// MethodOuterNonsolidCLI is the convert metadata method for outer-scope
// solid→non-solid cache populate via the 7z CLI.
const MethodOuterNonsolidCLI = "outer-nonsolid-cli"

// NonsolidPartialSuffix is appended to a cache dest for in-flight populate
// output (`{dest}.nonsolid.partial`). Shared with cleaner hygiene.
const NonsolidPartialSuffix = ".nonsolid.partial"

// NonsolidPartialWorkSuffix is the extract work directory suffix for outer
// cache populate (`{dest}.nonsolid.partial.work`).
const NonsolidPartialWorkSuffix = ".nonsolid.partial.work"

// NonsolidCacheParams holds knobs for EnsureNonsolidCachedCopy.
type NonsolidCacheParams struct {
	SevenZipBin   string
	OverheadBytes int
	MinFreeBytes  int
	// CacheDir overrides DefaultNonsolidCacheDir when non-empty.
	CacheDir string
	// Run7z optional process runner; nil uses DefaultRun7z.
	Run7z Run7zFunc
	// List7z optional list runner; nil uses DefaultList7z.
	List7z List7zFunc
}

// NonsolidCacheParamsFromConfig builds NonsolidCacheParams from cfg.
func NonsolidCacheParamsFromConfig(cfg *config.Config, opts ResolveOptions) NonsolidCacheParams {
	p := NonsolidCacheParams{
		SevenZipBin:   "7z",
		OverheadBytes: 64 * 1024 * 1024,
	}
	if cfg != nil {
		p.SevenZipBin = EffectiveSevenZipBin(cfg, opts)
		p.OverheadBytes = cfg.Convert7zOverheadBytes
		p.MinFreeBytes = cfg.MinFreeBytes
		p.CacheDir = DefaultNonsolidCacheDir(cfg)
	}
	return p
}

// CacheKeyForSource returns a stable hex key for source (path + size + mtime_ns).
// Parity with ratarmountcore.nonsolid_convert.cache_key_for_source.
func CacheKeyForSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", convertErrorf("outer_cache", "empty source path")
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		abs = source
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(abs))
	_, _ = h.Write([]byte(fmt.Sprintf("%d", st.Size())))
	_, _ = h.Write([]byte(fmt.Sprintf("%d", st.ModTime().UnixNano())))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NonsolidCacheDestPath returns `{cacheDir}/{cacheKey}.7z` when source can be
// stated, else `{cacheDir}/{basename}` (best-effort path-only fallback).
func NonsolidCacheDestPath(cacheDir, source string) string {
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "mount-wrapper", "non-solid-7z")
	}
	key, err := CacheKeyForSource(source)
	if err != nil || key == "" {
		base := filepath.Base(source)
		if base == "" || base == "." || base == string(filepath.Separator) {
			base = "archive.7z"
		}
		return filepath.Join(cacheDir, base)
	}
	return filepath.Join(cacheDir, key+".7z")
}

// NonsolidCacheLockPath returns the exclusive flock path for a cache dest
// (parity with ratarmountcore: `{cacheKey}.lock` next to `{cacheKey}.7z`).
func NonsolidCacheLockPath(dest string) string {
	if strings.HasSuffix(dest, ".7z") {
		return strings.TrimSuffix(dest, ".7z") + ".lock"
	}
	return dest + ".lock"
}

// NonsolidCacheDestFromLockPath returns the sibling `.7z` dest for a `{key}.lock`
// path (inverse of NonsolidCacheLockPath for content-keyed `.7z` dests).
// Empty lockPath or non-`.lock` suffix returns "".
func NonsolidCacheDestFromLockPath(lockPath string) string {
	lockPath = strings.TrimSpace(lockPath)
	if lockPath == "" || !strings.HasSuffix(lockPath, ".lock") {
		return ""
	}
	base := strings.TrimSuffix(lockPath, ".lock")
	if base == "" {
		return ""
	}
	return base + ".7z"
}

// NonsolidPartialPath returns the in-flight partial output path for dest.
func NonsolidPartialPath(dest string) string {
	if dest == "" {
		return ""
	}
	return dest + NonsolidPartialSuffix
}

// NonsolidPartialWorkPath returns the extract work directory for dest.
func NonsolidPartialWorkPath(dest string) string {
	if dest == "" {
		return ""
	}
	return dest + NonsolidPartialWorkSuffix
}

// nonsolidCacheHit reports whether dest is a usable non-solid, non-encrypted
// cached copy (size > 0 and 7z l -slt lists non-solid / non-encrypted).
func nonsolidCacheHit(dest, bin string, list List7zFunc) bool {
	dstSt, err := os.Stat(dest)
	if err != nil || !dstSt.Mode().IsRegular() || dstSt.Size() <= 0 {
		return false
	}
	dout, _ := list(bin, []string{"l", "-slt", dest}, "")
	return strings.TrimSpace(dout) != "" && !Parse7zListEncrypted(dout) && !Parse7zListIsSolid(dout)
}

// writeNonsolidCacheHitMetadata best-effort writes a size-only convert sidecar
// next to dest when a cache hit is reused without an existing sidecar (e.g.
// dest copied in without metadata). original = source Stat size, converted =
// dest Stat size, method = MethodOuterNonsolidCLI, duration omitted (never
// invent duration on a hit). No-op when a readable sidecar already exists.
func writeNonsolidCacheHitMetadata(source, dest string) {
	if HasConvertMetadata(dest) {
		return
	}
	st, err := os.Stat(source)
	if err != nil || !st.Mode().IsRegular() {
		return
	}
	dstSt, err := os.Stat(dest)
	if err != nil || !dstSt.Mode().IsRegular() || dstSt.Size() <= 0 {
		return
	}
	meta := BuildConvertMetadata(st.Size(), dstSt.Size(), MethodOuterNonsolidCLI, nil)
	if _, err := WriteConvertMetadata(dest, meta); err != nil {
		// Non-fatal: mount path remains valid without sidecar.
		_ = err
	}
}

// withNonsolidCacheLock opens dest's sibling .lock and holds a blocking
// exclusive flock for the duration of fn (Python fcntl.flock LOCK_EX).
func withNonsolidCacheLock(dest string, fn func() error) error {
	lockPath := NonsolidCacheLockPath(dest)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return convertErrorf("outer_cache", "create lock dir: %v", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return convertErrorf("outer_cache", "open lock %s: %v", lockPath, err)
	}
	defer func() { _ = f.Close() }()
	if err := flockExclusive(f); err != nil {
		return convertErrorf("outer_cache", "lock %s: %v", lockPath, err)
	}
	defer func() { _ = flockUnlock(f) }()
	return fn()
}

// EnsureNonsolidCachedCopy builds or reuses a non-solid cached copy for
// outer/all nonsolid scope (minimal Go parity with ensure_nonsolid_cached_copy).
//
// Behavior:
//   - nonsolid off / wrong scope / non-.7z → return source unchanged
//   - 7z list fail / empty listing → clear error (fail closed; do not treat as
//     non-solid and silent-passthrough the solid candidate)
//   - encrypted (7z l -slt markers, or extract/create stderr) → Encrypted7zMessage
//   - list succeeds and Solid != + → return source (no cache copy needed)
//   - solid → extract + `7z a -t7z -ms=off` into cache; return cache path
//   - cache hit when dest exists and lists as non-solid / non-encrypted; on hit
//     without a convert sidecar, best-effort write size-only metadata
//     (original=source size, converted=dest size, method outer-nonsolid-cli;
//     duration omitted — never invent duration on a hit)
//   - concurrent populates of the same dest serialize on `{cacheKey}.lock`
//     (re-check hit inside exclusive flock before free-space + populate)
//   - post-populate size floor via FlattenMinOKSize; under-floor dest removed
//   - post-populate re-list of dest must pass nonsolidCacheHit (non-solid /
//     non-encrypted listing); still-solid, empty/failed list, or encrypted →
//     remove dest and return a clear error (no stream-flatten fallback)
//
// Residual vs ratarmountcore: CLI extract+repack only (no stream repack / py7zr
// folder walk); nested members stay as embedded .7z files (outer solid block
// only). Stream-flatten remains deferred.
func EnsureNonsolidCachedCopy(cfg *config.Config, source string, p NonsolidCacheParams) (string, error) {
	source = filepath.Clean(strings.TrimSpace(source))
	if cfg == nil || !cfg.Convert7zNonsolid || !IsSevenzPath(source) {
		return source, nil
	}
	if !ScopeUsesOuterCache(cfg.Convert7zScope) {
		return source, nil
	}
	st, err := os.Stat(source)
	if err != nil || !st.Mode().IsRegular() {
		return "", convertErrorf("outer_cache", "archive not found: %s", source)
	}

	bin := strings.TrimSpace(p.SevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	list := list7zOf(p.List7z)
	run := run7zOf(p.Run7z)

	out, listErr := list(bin, []string{"l", "-slt", source}, "")
	// Prefer encryption markers from listing or list-error text before fail-closed.
	if Parse7zListEncrypted(out) || (listErr != nil && Parse7zListEncrypted(listErr.Error())) {
		return "", convertErrorf("outer_cache", "%s: %s", Encrypted7zMessage, source)
	}
	// Fail closed: only treat as non-solid when list succeeds with usable text
	// and Solid != +. Empty or failed list must not silent-passthrough.
	if listErr != nil {
		return "", convertErrorf("outer_cache", "7z list failed for %s: %v", source, listErr)
	}
	if strings.TrimSpace(out) == "" {
		return "", convertErrorf("outer_cache", "7z list empty for solid-scope candidate: %s", source)
	}
	// Outer cache is for solid outer archives only. Nested-only archives keep
	// the original path; child env TARMOUNT_7Z_NONSOLID handles nested load.
	if !Parse7zListIsSolid(out) {
		return source, nil
	}

	cacheDir := strings.TrimSpace(p.CacheDir)
	if cacheDir == "" {
		cacheDir = DefaultNonsolidCacheDir(cfg)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", convertErrorf("outer_cache", "create cache dir: %v", err)
	}
	dest := NonsolidCacheDestPath(cacheDir, source)

	// Fast path: cache hit without taking the flock.
	if nonsolidCacheHit(dest, bin, list) {
		writeNonsolidCacheHitMetadata(source, dest)
		return dest, nil
	}

	// Critical section: exclusive flock on {cacheKey}.lock, re-check hit, then
	// free-space gate + populate (parity with ensure_nonsolid_cached_copy).
	var outPath string
	err = withNonsolidCacheLock(dest, func() error {
		if nonsolidCacheHit(dest, bin, list) {
			writeNonsolidCacheHitMetadata(source, dest)
			outPath = dest
			return nil
		}

		// Free-space gate: source + extracted (~source) + output (~source).
		peak := EstimateFlattenPeakDiskBytes(st.Size())
		if err := CheckFlattenSpace(cacheDir, peak, int64(p.OverheadBytes), int64(p.MinFreeBytes)); err != nil {
			return err
		}

		started := time.Now()
		if err := populateOuterNonsolidCache(source, dest, bin, run); err != nil {
			return wrapOuterCacheRunError(err, source)
		}
		dstSt, err := os.Stat(dest)
		if err != nil || !dstSt.Mode().IsRegular() || dstSt.Size() <= 0 {
			_ = os.Remove(dest)
			return convertErrorf("outer_cache", "cache populate produced no output: %s", dest)
		}
		// Size floor (shared with flatten CLI path). Reject under-floor dest.
		minOK := FlattenMinOKSize(st.Size())
		if dstSt.Size() < minOK {
			_ = os.Remove(dest)
			return convertErrorf("outer_cache",
				"cache populate output too small: %d bytes (source=%d, minimum=%d)",
				dstSt.Size(), st.Size(), minOK)
		}
		// Post-populate solid verify: re-list dest and require non-solid /
		// non-encrypted (same hit predicate as cache reuse). Still-solid or
		// list fail must not leave a bad cache entry (no stream-flatten).
		if !nonsolidCacheHit(dest, bin, list) {
			_ = os.Remove(dest)
			return convertErrorf("outer_cache",
				"cache populate still solid or unlistable as non-solid: %s", dest)
		}
		// Best-effort metadata sidecar next to the cached copy.
		dur := time.Since(started).Seconds()
		if dur < 0 {
			dur = 0
		}
		d := float64(int(dur*1000)) / 1000
		meta := BuildConvertMetadata(st.Size(), dstSt.Size(), MethodOuterNonsolidCLI, &d)
		if _, err := WriteConvertMetadata(dest, meta); err != nil {
			// Non-fatal: mount path is still valid.
			_ = err
		}
		outPath = dest
		return nil
	})
	if err != nil {
		return "", err
	}
	return outPath, nil
}

// wrapOuterCacheRunError surfaces Encrypted7zMessage when a 7z extract/create
// failure's combined stderr/stdout (embedded in the error text by DefaultRun7z)
// indicates encryption; otherwise returns err unchanged.
func wrapOuterCacheRunError(err error, source string) error {
	if err == nil {
		return nil
	}
	if Parse7zListEncrypted(err.Error()) {
		return convertErrorf("outer_cache", "%s: %s", Encrypted7zMessage, source)
	}
	return err
}

// populateOuterNonsolidCache extracts source and rebuilds a non-solid 7z at dest.
// Does not expand nested *.7z members (outer solid block only).
// Cleans leftover *.nonsolid.partial / *.work before work and on return.
func populateOuterNonsolidCache(source, dest, bin string, run Run7zFunc) error {
	partial := NonsolidPartialPath(dest)
	workDir := NonsolidPartialWorkPath(dest)
	// Always drop leftovers from a prior crashed populate before starting.
	_ = os.Remove(partial)
	_ = os.RemoveAll(workDir)
	defer func() {
		_ = os.Remove(partial)
		_ = os.RemoveAll(workDir)
	}()

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return convertErrorf("outer_cache", "create work dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return convertErrorf("outer_cache", "create dest dir: %v", err)
	}

	srcAbs, err := filepath.Abs(source)
	if err != nil {
		srcAbs = source
	}
	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		workAbs = workDir
	}
	partialAbs, err := filepath.Abs(partial)
	if err != nil {
		partialAbs = partial
	}

	extractArgv := BuildFlattenExtractCmd(bin, srcAbs, workAbs, nil)
	if err := run(extractArgv[0], extractArgv[1:], ""); err != nil {
		return err
	}
	createArgv := BuildFlattenCreateCmd(bin, partialAbs)
	if err := run(createArgv[0], createArgv[1:], workAbs); err != nil {
		return err
	}

	pst, err := os.Stat(partial)
	if err != nil || !pst.Mode().IsRegular() {
		return convertErrorf("outer_cache", "partial output missing: %s", partial)
	}
	// Atomic-ish replace onto dest.
	_ = os.Remove(dest)
	if err := os.Rename(partial, dest); err != nil {
		// Cross-device fallback: stream copy then remove partial.
		if cerr := copyFile(partial, dest); cerr != nil {
			return convertErrorf("outer_cache", "rename partial: %v (copy: %v)", err, cerr)
		}
		_ = os.Remove(partial)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
