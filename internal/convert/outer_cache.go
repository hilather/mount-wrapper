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

// EnsureNonsolidCachedCopy builds or reuses a non-solid cached copy for
// outer/all nonsolid scope (minimal Go parity with ensure_nonsolid_cached_copy).
//
// Behavior:
//   - nonsolid off / wrong scope / non-.7z → return source unchanged
//   - encrypted (7z l -slt markers) → clear error (encrypted 7z not supported)
//   - not solid (CLI Solid != +) → return source (no cache copy needed)
//   - solid → extract + `7z a -t7z -ms=off` into cache; return cache path
//   - cache hit when dest exists and lists as non-solid / non-encrypted
//
// Residual vs ratarmountcore: CLI extract+repack only (no stream repack / py7zr
// folder walk); no flock; nested members stay as embedded .7z files (outer
// solid block only). Stream-flatten remains deferred.
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

	out, _ := list(bin, []string{"l", "-slt", source}, "")
	if Parse7zListEncrypted(out) {
		return "", convertErrorf("outer_cache", "%s: %s", Encrypted7zMessage, source)
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

	// Cache hit: existing non-solid, non-encrypted dest.
	if dstSt, err := os.Stat(dest); err == nil && dstSt.Mode().IsRegular() && dstSt.Size() > 0 {
		dout, _ := list(bin, []string{"l", "-slt", dest}, "")
		if strings.TrimSpace(dout) != "" && !Parse7zListEncrypted(dout) && !Parse7zListIsSolid(dout) {
			return dest, nil
		}
	}

	// Free-space gate: source + extracted (~source) + output (~source).
	peak := EstimateFlattenPeakDiskBytes(st.Size())
	if err := CheckFlattenSpace(cacheDir, peak, int64(p.OverheadBytes), int64(p.MinFreeBytes)); err != nil {
		return "", err
	}

	started := time.Now()
	if err := populateOuterNonsolidCache(source, dest, bin, run); err != nil {
		return "", err
	}
	dstSt, err := os.Stat(dest)
	if err != nil || !dstSt.Mode().IsRegular() || dstSt.Size() <= 0 {
		_ = os.Remove(dest)
		return "", convertErrorf("outer_cache", "cache populate produced no output: %s", dest)
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
	return dest, nil
}

// populateOuterNonsolidCache extracts source and rebuilds a non-solid 7z at dest.
// Does not expand nested *.7z members (outer solid block only).
func populateOuterNonsolidCache(source, dest, bin string, run Run7zFunc) error {
	partial := dest + ".nonsolid.partial"
	workDir := partial + ".work"
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
