package convert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

func TestCacheKeyForSource_Stable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	k1, err := convert.CacheKeyForSource(src)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := convert.CacheKeyForSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 || len(k1) != 64 {
		t.Fatalf("key=%q / %q", k1, k2)
	}
	// Size change → different key.
	if err := os.WriteFile(src, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	k3, err := convert.CacheKeyForSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if k3 == k1 {
		t.Fatal("size change should change key")
	}
}

func TestNonsolidCacheDestPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "c")
	got := convert.NonsolidCacheDestPath(cache, src)
	key, _ := convert.CacheKeyForSource(src)
	want := filepath.Join(cache, key+".7z")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Missing file → basename fallback.
	got2 := convert.NonsolidCacheDestPath(cache, "/no/such/file.7z")
	if got2 != filepath.Join(cache, "file.7z") {
		t.Fatalf("fallback=%q", got2)
	}
}

func TestEnsureNonsolidCachedCopy_Gates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Disabled
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid": false,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{})
	if err != nil || got != src {
		t.Fatalf("disabled got=%q err=%v", got, err)
	}

	// Nested scope — no outer cache
	cfg, err = config.FromMap(map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_scope":    "nested",
		"convert_7z_cache_dir": filepath.Join(dir, "cache"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err = convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{})
	if err != nil || got != src {
		t.Fatalf("nested got=%q err=%v", got, err)
	}
}

func TestEnsureNonsolidCachedCopy_NonSolidReturnsSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": cache,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	list := func(string, []string, string) (string, error) {
		return sampleNonSolidList, nil
	}
	var ran bool
	run := func(string, []string, string) error {
		ran = true
		return nil
	}
	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z: list,
		Run7z:  run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("want source, got %q", got)
	}
	if ran {
		t.Fatal("7z convert should not run for non-solid")
	}
}

func TestEnsureNonsolidCachedCopy_EncryptedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "all",
		"convert_7z_cache_dir": filepath.Join(dir, "cache"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	list := func(string, []string, string) (string, error) {
		return sampleEncryptedMember, nil
	}
	_, err = convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{List7z: list})
	if err == nil {
		t.Fatal("expected encrypted error")
	}
	if !strings.Contains(err.Error(), convert.Encrypted7zMessage) {
		t.Fatalf("err=%v", err)
	}
}

func TestEnsureNonsolidCachedCopy_SolidPopulate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "solid.7z")
	if err := os.WriteFile(src, []byte("solid-payload-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": cache,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// List: first call source (solid), later dest (non-solid) for cache-hit path.
	listCalls := 0
	list := func(_ string, args []string, _ string) (string, error) {
		listCalls++
		// Last arg is archive path.
		path := ""
		if len(args) > 0 {
			path = args[len(args)-1]
		}
		if strings.Contains(path, "cache") || strings.HasSuffix(path, ".7z") && path != src {
			// After populate, dest should look non-solid.
			if st, err := os.Stat(path); err == nil && st.Size() > 0 {
				return sampleNonSolidList, nil
			}
		}
		return sampleSolidList, nil
	}

	run := func(bin string, args []string, cwd string) error {
		// Simulate extract (x) and create (a).
		joined := strings.Join(args, " ")
		if len(args) > 0 && args[0] == "a" {
			// Dest is args after flags; BuildFlattenCreateCmd: a -t7z -ms=off -y dest *
			var dest string
			for _, a := range args {
				if strings.HasSuffix(a, ".partial") || strings.Contains(a, "nonsolid.partial") {
					dest = a
					break
				}
				if strings.HasSuffix(a, ".7z") && !strings.HasPrefix(a, "-") {
					dest = a
				}
			}
			if dest == "" {
				// Find first non-flag .7z / partial path
				for _, a := range args {
					if strings.HasPrefix(a, "-") || a == "*" {
						continue
					}
					if strings.Contains(a, "partial") || strings.HasSuffix(a, ".7z") {
						dest = a
						break
					}
				}
			}
			if dest == "" {
				t.Fatalf("create args=%v", args)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dest, []byte("nonsolid-cached"), 0o644)
		}
		if len(args) > 0 && args[0] == "x" {
			// Extract: create a dummy file in -o work dir.
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					work := strings.TrimPrefix(a, "-o")
					work = strings.TrimSuffix(work, string(filepath.Separator))
					if err := os.MkdirAll(work, 0o755); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(work, "file.txt"), []byte("hi"), 0o644)
				}
			}
		}
		_ = bin
		_ = cwd
		_ = joined
		return nil
	}

	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         run,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := convert.NonsolidCacheDestPath(cache, src)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	st, err := os.Stat(got)
	if err != nil || st.Size() == 0 {
		t.Fatalf("dest missing: %v", err)
	}
	if convert.ReadConvertMetadata(got) == nil {
		t.Fatal("expected convert metadata on cached copy")
	}
	if meta := convert.ReadConvertMetadata(got); meta.Method != convert.MethodOuterNonsolidCLI {
		t.Fatalf("method=%q", meta.Method)
	}

	// Cache hit — second call should not re-run create with empty dest.
	listCalls = 0
	got2, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z: list,
		Run7z: func(string, []string, string) error {
			t.Fatal("should not re-convert on cache hit")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != got {
		t.Fatalf("cache hit path %q vs %q", got2, got)
	}
	if listCalls < 1 {
		t.Fatal("expected list for hit check")
	}
}

func TestEnsureNonsolidCachedCopy_FixtureListings(t *testing.T) {
	t.Parallel()
	// Offline: encrypted fixture listings refuse; solid fixture is solid.
	encWP := readNestedListing(t, "encrypted-wrong-password.l-slt.txt")
	if !convert.Parse7zListEncrypted(encWP) {
		t.Fatal("wrong-password fixture")
	}
	if convert.Parse7zListNeedsFlatten(encWP) {
		t.Fatal("encrypted fixture must not need flatten")
	}
	encMem := readNestedListing(t, "encrypted-member.l-slt.txt")
	if !convert.Parse7zListEncrypted(encMem) {
		t.Fatal("member fixture encrypted")
	}
	if convert.Parse7zListNeedsFlatten(encMem) {
		t.Fatal("encrypted solid must not auto-flatten")
	}
	if !convert.Parse7zListIsSolid(encMem) {
		t.Fatal("member fixture is solid (but encrypted)")
	}
	solid := readNestedListing(t, "solid-mini.l-slt.txt")
	if !convert.Parse7zListIsSolid(solid) {
		t.Fatal("solid-mini")
	}
	if convert.Parse7zListEncrypted(solid) {
		t.Fatal("solid-mini not encrypted")
	}
	nested := readNestedListing(t, "SUP-36264-nested-mini.l-slt.txt")
	if convert.Parse7zListIsSolid(nested) {
		t.Fatal("nested mini outer is non-solid")
	}
	if !convert.Parse7zListNeedsFlatten(nested) {
		t.Fatal("nested still needs flatten")
	}
}

func TestNonsolidCacheParamsFromConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_bin":            "/usr/bin/7z",
		"convert_7z_overhead_bytes": 42,
		"convert_7z_cache_dir":      "/c",
		"min_free_bytes":            7,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := convert.NonsolidCacheParamsFromConfig(cfg, convert.ResolveOptions{
		IsExecutable: alwaysExec, SearchPathDisabled: true,
	})
	if p.OverheadBytes != 42 || p.MinFreeBytes != 7 || p.CacheDir != "/c" {
		t.Fatalf("%+v", p)
	}
	if p.SevenZipBin != "/usr/bin/7z" {
		t.Fatalf("bin=%q", p.SevenZipBin)
	}
}
