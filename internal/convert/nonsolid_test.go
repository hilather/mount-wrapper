package convert_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestScopeHelpers(t *testing.T) {
	t.Parallel()
	if !convert.ScopeIsNested(config.Convert7zScopeNested) {
		t.Fatal("nested")
	}
	if !convert.ScopeIsOuter(config.Convert7zScopeOuter) {
		t.Fatal("outer")
	}
	if !convert.ScopeIsFlatten(config.Convert7zScopeFlatten) {
		t.Fatal("flatten")
	}
	if !convert.ScopeIsAll(config.Convert7zScopeAll) {
		t.Fatal("all")
	}
	if !convert.ScopeUsesChildEnv(config.Convert7zScopeNested) {
		t.Fatal("nested env")
	}
	if convert.ScopeUsesChildEnv(config.Convert7zScopeFlatten) {
		t.Fatal("flatten no child env")
	}
	if !convert.ScopeUsesOuterCache(config.Convert7zScopeAll) {
		t.Fatal("all cache")
	}
	if convert.ScopeUsesOuterCache(config.Convert7zScopeNested) {
		t.Fatal("nested no outer cache")
	}
}

func TestApplyNonsolidEnv(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":        true,
		"convert_7z_scope":           "nested",
		"convert_7z_overhead_bytes":  1234,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": "/bin"}
	convert.ApplyNonsolidEnv(env, cfg)
	if env[convert.Env7zNonsolid] != "1" {
		t.Fatalf("env=%v", env)
	}
	if env[convert.Env7zNonsolidOverheadBytes] != "1234" {
		t.Fatalf("overhead=%s", env[convert.Env7zNonsolidOverheadBytes])
	}

	// Flatten is no-op
	cfg.Convert7zScope = config.Convert7zScopeFlatten
	env2 := map[string]string{}
	convert.ApplyNonsolidEnv(env2, cfg)
	if _, ok := env2[convert.Env7zNonsolid]; ok {
		t.Fatal("flatten should not set env")
	}

	// Disabled
	cfg.Convert7zNonsolid = false
	cfg.Convert7zScope = config.Convert7zScopeNested
	env3 := map[string]string{}
	convert.ApplyNonsolidEnv(env3, cfg)
	if len(env3) != 0 {
		t.Fatal("disabled")
	}
}

func TestApplyNonsolidEnvSlice(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "outer",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	out := convert.ApplyNonsolidEnvSlice([]string{"A=1"}, cfg)
	found := false
	for _, e := range out {
		if e == convert.Env7zNonsolid+"=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("out=%v", out)
	}
}

func TestShouldFlattenConvert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(archive, []byte("solid"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "flatten",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// No probe → false
	if convert.ShouldFlattenConvert(cfg, archive, nil) {
		t.Fatal("nil probe")
	}
	// Probe true
	if !convert.ShouldFlattenConvert(cfg, archive, func(string) bool { return true }) {
		t.Fatal("expected true")
	}
	// Probe false
	if convert.ShouldFlattenConvert(cfg, archive, func(string) bool { return false }) {
		t.Fatal("probe false")
	}
	// Metadata present
	if _, err := convert.WriteConvertMetadata(archive, convert.BuildConvertMetadata(1, 1, "flatten", nil)); err != nil {
		t.Fatal(err)
	}
	if convert.ShouldFlattenConvert(cfg, archive, func(string) bool { return true }) {
		t.Fatal("has metadata")
	}
	// Wrong scope
	cfg.Convert7zScope = config.Convert7zScopeNested
	os.Remove(convert.MetadataPath(archive))
	if convert.ShouldFlattenConvert(cfg, archive, func(string) bool { return true }) {
		t.Fatal("scope")
	}
}

func TestDefaultNonsolidCacheDir(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_cache_dir": "/var/cache/mw/ns",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := convert.DefaultNonsolidCacheDir(cfg); got != "/var/cache/mw/ns" {
		t.Fatalf("got %q", got)
	}
	cfg2, _ := config.FromMap(map[string]any{}, "")
	if got := convert.DefaultNonsolidCacheDir(cfg2); got == "" {
		t.Fatal("empty cache")
	}
}

func TestResolveMountArchivePath(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": "/cache/ns",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Missing source → basename fallback under cache.
	srcMissing := "/data/a.7z"
	got := convert.ResolveMountArchivePath(cfg, srcMissing)
	want := filepath.Join("/cache/ns", "a.7z")
	if got != want {
		t.Fatalf("missing source got %q want %q", got, want)
	}

	// Existing source → content-keyed path.
	dir := t.TempDir()
	src := filepath.Join(dir, "solid.7z")
	if err := os.WriteFile(src, []byte("solid-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Convert7zCacheDir = filepath.Join(dir, "cache")
	got = convert.ResolveMountArchivePath(cfg, src)
	key, err := convert.CacheKeyForSource(src)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(cfg.Convert7zCacheDir, key+".7z")
	if got != want {
		t.Fatalf("keyed got %q want %q", got, want)
	}

	cfg.Convert7zScope = config.Convert7zScopeFlatten
	if convert.ResolveMountArchivePath(cfg, src) != src {
		t.Fatal("flatten keeps path")
	}
	cfg.Convert7zScope = config.Convert7zScopeNested
	if convert.ResolveMountArchivePath(cfg, src) != src {
		t.Fatal("nested keeps path")
	}
}

func TestResolveSevenZipBin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "7z")
	writeFakeBin(t, p)
	got := convert.ResolveSevenZipBin(p, convert.ResolveOptions{
		IsExecutable:       alwaysExec,
		SearchPathDisabled: true,
	})
	if got != p {
		t.Fatalf("got %q", got)
	}
	// Empty → default name
	got = convert.ResolveSevenZipBin("", convert.ResolveOptions{
		SearchPathDisabled: true,
	})
	if got != "7z" {
		t.Fatalf("got %q", got)
	}
}

func TestFlattenParamsFromConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_bin":                           "/usr/bin/7z",
		"convert_7z_overhead_bytes":                99,
		"convert_7z_flatten_extract_buffer_bytes":  1000,
		"convert_7z_inner_prefix_strip":            "prefix/",
		"convert_7z_flatten_exclude":               []any{"*.tmp"},
		"convert_7z_cache_dir":                     "/c",
		"min_free_bytes":                           50,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := convert.FlattenParamsFromConfig(cfg, convert.ResolveOptions{
		IsExecutable: alwaysExec, SearchPathDisabled: true,
	})
	if p.OverheadBytes != 99 || p.ExtractBufferBytes != 1000 {
		t.Fatalf("%+v", p)
	}
	if p.InnerPrefixStrip != "prefix/" {
		t.Fatalf("strip=%q", p.InnerPrefixStrip)
	}
	if len(p.ExcludePatterns) != 1 || p.ExcludePatterns[0] != "*.tmp" {
		t.Fatalf("exclude=%v", p.ExcludePatterns)
	}
	if p.CacheDir != "/c" {
		t.Fatalf("cache=%q", p.CacheDir)
	}
	if p.MinFreeBytes != 50 {
		t.Fatalf("min_free=%d", p.MinFreeBytes)
	}
}
