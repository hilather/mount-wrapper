package convert

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// Env keys for nested/outer nonsolid conversion via ratarmount child processes.
// Parity with sevenzip_nonsolid.apply_nonsolid_env (Python TARMOUNT_* names —
// the ratarmount engines still read TARMOUNT_7Z_NONSOLID; mount-wrapper hooks
// use MOUNT_WRAPPER_* only).
const (
	Env7zNonsolid              = "TARMOUNT_7Z_NONSOLID"
	Env7zNonsolidOverheadBytes = "TARMOUNT_7Z_NONSOLID_OVERHEAD_BYTES"
)

// FlattenNeededFunc reports whether archivePath needs flatten conversion
// (solid / nested 7z). Injectable because full 7z structure probes live in
// ratarmountcore upstream and are not reimplemented here.
type FlattenNeededFunc func(archivePath string) bool

// DefaultNonsolidCacheDir returns convert_7z_cache_dir or
// `$HOME/.cache/mount-wrapper/non-solid-7z` (renamed product path; Python used
// tarmount-wsl in the cache segment).
func DefaultNonsolidCacheDir(cfg *config.Config) string {
	if cfg != nil {
		if d := strings.TrimSpace(cfg.Convert7zCacheDir); d != "" {
			return d
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "mount-wrapper", "non-solid-7z")
	}
	return filepath.Join(home, ".cache", "mount-wrapper", "non-solid-7z")
}

// ResolveSevenZipBin locates the 7z CLI.
//
// Priority:
//  1. Explicit non-empty configured path (returned even if missing)
//  2. PATH search for configured bare name / "7z"
//  3. "7z" even if missing (caller/doctor report)
func ResolveSevenZipBin(configured string, opts ResolveOptions) string {
	which := whichOf(opts)
	isExec := execOf(opts)

	if configured = strings.TrimSpace(configured); configured != "" {
		if candidate := resolveIfExecutable(configured, which, isExec); candidate != "" {
			return candidate
		}
		// Bare name: try PATH.
		if !strings.Contains(configured, string(filepath.Separator)) {
			if p := which(configured); p != "" {
				return p
			}
		}
		return configured
	}
	if !opts.SearchPathDisabled {
		if p := which("7z"); p != "" {
			return p
		}
	}
	return "7z"
}

// EffectiveSevenZipBin returns cfg.Convert7zBin resolved via ResolveSevenZipBin.
func EffectiveSevenZipBin(cfg *config.Config, opts ResolveOptions) string {
	var configured string
	if cfg != nil {
		configured = cfg.Convert7zBin
	}
	return ResolveSevenZipBin(configured, opts)
}

// SevenZipAvailable reports whether the resolved 7z binary is executable.
func SevenZipAvailable(cfg *config.Config, opts ResolveOptions) bool {
	bin := EffectiveSevenZipBin(cfg, opts)
	return bin != "" && execOf(opts)(bin)
}

// Scope helpers for convert_7z_scope values.
func ScopeIsNested(scope string) bool  { return scope == config.Convert7zScopeNested }
func ScopeIsOuter(scope string) bool   { return scope == config.Convert7zScopeOuter }
func ScopeIsFlatten(scope string) bool { return scope == config.Convert7zScopeFlatten }
func ScopeIsAll(scope string) bool     { return scope == config.Convert7zScopeAll }

// ScopeUsesChildEnv reports whether nonsolid is applied via child env
// (nested/outer/all — not flatten, which is pre-mount in-place).
func ScopeUsesChildEnv(scope string) bool {
	return ScopeIsNested(scope) || ScopeIsOuter(scope) || ScopeIsAll(scope)
}

// ScopeUsesOuterCache reports whether outer nonsolid cached copies are used
// (outer or all; not nested-only or flatten).
func ScopeUsesOuterCache(scope string) bool {
	return ScopeIsOuter(scope) || ScopeIsAll(scope)
}

// ApplyNonsolidEnv mutates env map for ratarmount children when nonsolid
// nested/outer conversion is enabled. Flatten scope is a no-op (pre-mount).
// Parity with sevenzip_nonsolid.apply_nonsolid_env.
func ApplyNonsolidEnv(env map[string]string, cfg *config.Config) {
	if env == nil || cfg == nil || !cfg.Convert7zNonsolid {
		return
	}
	if ScopeIsFlatten(cfg.Convert7zScope) {
		return
	}
	env[Env7zNonsolid] = "1"
	env[Env7zNonsolidOverheadBytes] = strconv.Itoa(cfg.Convert7zOverheadBytes)
}

// ApplyNonsolidEnvSlice returns a copy of base with nonsolid env keys applied.
// Base is typically os.Environ()-style KEY=VAL pairs.
func ApplyNonsolidEnvSlice(base []string, cfg *config.Config) []string {
	if cfg == nil || !cfg.Convert7zNonsolid || ScopeIsFlatten(cfg.Convert7zScope) {
		return append([]string(nil), base...)
	}
	m := envSliceToMap(base)
	ApplyNonsolidEnv(m, cfg)
	return envMapToSlice(m)
}

// ShouldFlattenConvert reports whether in-place flatten should run before mount.
//
// When needsFlatten is nil, returns false for the structure probe. Production
// service wires DefaultFlattenNeeded (best-effort 7z l -slt) when nonsolid +
// flatten scope; inject a custom FlattenNeededFunc to override. Other gates
// match Python should_flatten_convert.
func ShouldFlattenConvert(cfg *config.Config, archivePath string, needsFlatten FlattenNeededFunc) bool {
	if cfg == nil || !cfg.Convert7zNonsolid || !ScopeIsFlatten(cfg.Convert7zScope) {
		return false
	}
	if !IsSevenzPath(archivePath) {
		return false
	}
	st, err := os.Stat(archivePath)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if HasConvertMetadata(archivePath) {
		return false
	}
	if needsFlatten == nil {
		return false
	}
	return needsFlatten(archivePath)
}

// NonsolidFlattenParams holds parameters for flatten convert (space / tool knobs).
type NonsolidFlattenParams struct {
	SevenZipBin        string
	OverheadBytes      int
	MinFreeBytes       int
	ExtractBufferBytes int
	InnerPrefixStrip   string
	ExcludePatterns    []string
	CacheDir           string
	// Run7z optional process runner; nil uses DefaultRun7z.
	Run7z Run7zFunc
}

// FlattenParamsFromConfig builds NonsolidFlattenParams from cfg.
func FlattenParamsFromConfig(cfg *config.Config, opts ResolveOptions) NonsolidFlattenParams {
	p := NonsolidFlattenParams{
		SevenZipBin:        "7z",
		OverheadBytes:      64 * 1024 * 1024,
		ExtractBufferBytes: 10 * 1024 * 1024 * 1024,
	}
	if cfg != nil {
		p.SevenZipBin = EffectiveSevenZipBin(cfg, opts)
		p.OverheadBytes = cfg.Convert7zOverheadBytes
		p.MinFreeBytes = cfg.MinFreeBytes
		p.ExtractBufferBytes = cfg.Convert7zFlattenExtractBuffer
		p.InnerPrefixStrip = cfg.Convert7zInnerPrefixStrip
		if len(cfg.Convert7zFlattenExclude) > 0 {
			p.ExcludePatterns = append([]string(nil), cfg.Convert7zFlattenExclude...)
		}
		p.CacheDir = DefaultNonsolidCacheDir(cfg)
	}
	return p
}

// ResolveMountArchivePath returns the archive path ratarmount should mount.
//
// For outer/all scopes with nonsolid enabled, returns the path under the
// nonsolid cache dir (`{cache}/{basename}`) without creating the cached copy
// (creation is deferred to a runner that calls ratarmountcore / 7z).
// Flatten and nested scopes return the original path.
//
// Gap: Python ensure_nonsolid_cached_copy performs the conversion; this helper
// only computes the expected cache path when outer cache is active.
func ResolveMountArchivePath(cfg *config.Config, archivePath string) string {
	if cfg == nil || !cfg.Convert7zNonsolid || !IsSevenzPath(archivePath) {
		return archivePath
	}
	if ScopeIsFlatten(cfg.Convert7zScope) {
		return archivePath
	}
	if !ScopeUsesOuterCache(cfg.Convert7zScope) {
		return archivePath
	}
	cache := DefaultNonsolidCacheDir(cfg)
	return filepath.Join(cache, filepath.Base(archivePath))
}

func envSliceToMap(base []string) map[string]string {
	m := make(map[string]string, len(base))
	for _, e := range base {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		} else if e != "" {
			m[e] = ""
		}
	}
	return m
}

func envMapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
