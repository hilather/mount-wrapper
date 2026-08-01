package convert_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
)

// nonsolidCachedPayload is large enough to pass FlattenMinOKSize for small sources.
var nonsolidCachedPayload = bytes.Repeat([]byte("nonsolid-cached\n"), 20) // 320 bytes

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
	// Lock path is sibling {cacheKey}.lock (Python parity).
	lock := convert.NonsolidCacheLockPath(got)
	if lock != filepath.Join(cache, key+".lock") {
		t.Fatalf("lock=%q want %s.lock", lock, key)
	}
	// Missing file → basename fallback.
	got2 := convert.NonsolidCacheDestPath(cache, "/no/such/file.7z")
	if got2 != filepath.Join(cache, "file.7z") {
		t.Fatalf("fallback=%q", got2)
	}
	if convert.NonsolidCacheLockPath(got2) != filepath.Join(cache, "file.lock") {
		t.Fatalf("fallback lock=%q", convert.NonsolidCacheLockPath(got2))
	}
	// Partial / work helpers + lock→dest inverse for cleaner hygiene.
	if convert.NonsolidPartialPath(got) != got+convert.NonsolidPartialSuffix {
		t.Fatalf("partial=%q", convert.NonsolidPartialPath(got))
	}
	if convert.NonsolidPartialWorkPath(got) != got+convert.NonsolidPartialWorkSuffix {
		t.Fatalf("work=%q", convert.NonsolidPartialWorkPath(got))
	}
	if convert.NonsolidCacheDestFromLockPath(lock) != got {
		t.Fatalf("dest from lock=%q want %q", convert.NonsolidCacheDestFromLockPath(lock), got)
	}
}

// solidPopulateRun is a shared fake 7z runner for outer-cache populate tests.
func solidPopulateRun(t *testing.T, creates *atomic.Int32, createDelay time.Duration) convert.Run7zFunc {
	t.Helper()
	return func(bin string, args []string, cwd string) error {
		_ = bin
		_ = cwd
		if len(args) > 0 && args[0] == "a" {
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
			if creates != nil {
				creates.Add(1)
			}
			if createDelay > 0 {
				time.Sleep(createDelay)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			// Must satisfy FlattenMinOKSize for typical small test sources (floor 200).
			return os.WriteFile(dest, nonsolidCachedPayload, 0o644)
		}
		if len(args) > 0 && args[0] == "x" {
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
		return nil
	}
}

// solidListForCache returns solid for source and non-solid for populated dest.
func solidListForCache(src string) convert.List7zFunc {
	return func(_ string, args []string, _ string) (string, error) {
		path := ""
		if len(args) > 0 {
			path = args[len(args)-1]
		}
		if path != src {
			if st, err := os.Stat(path); err == nil && st.Size() > 0 {
				return sampleNonSolidList, nil
			}
		}
		return sampleSolidList, nil
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

func TestEnsureNonsolidCachedCopy_ListFailClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": filepath.Join(dir, "cache"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		list    convert.List7zFunc
		wantSub string
	}{
		{
			name: "list error",
			list: func(string, []string, string) (string, error) {
				return "", errors.New("7z: cannot open file as archive")
			},
			wantSub: "7z list failed",
		},
		{
			name: "empty listing",
			list: func(string, []string, string) (string, error) {
				return "  \n\t", nil
			},
			wantSub: "7z list empty",
		},
		{
			name: "list error with encryption phrase",
			list: func(string, []string, string) (string, error) {
				return "", errors.New("ERROR: Wrong password : a.7z")
			},
			wantSub: convert.Encrypted7zMessage,
		},
		{
			name: "partial output plus list error still fails closed",
			list: func(string, []string, string) (string, error) {
				// Non-empty garbage must not silent-passthrough when list fails.
				return "Path = a.7z\n", errors.New("exit status 2")
			},
			wantSub: "7z list failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ran bool
			run := func(string, []string, string) error {
				ran = true
				return nil
			}
			got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
				List7z: tc.list,
				Run7z:  run,
			})
			if err == nil {
				t.Fatalf("expected error, got path %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err=%v want substring %q", err, tc.wantSub)
			}
			if ran {
				t.Fatal("must not populate on list fail/empty")
			}
			if got != "" {
				t.Fatalf("path should be empty on error, got %q", got)
			}
		})
	}
}

func TestEnsureNonsolidCachedCopy_SizeFloorRejectsUnderfloorDest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Source large enough that floor is source/2 (>= 200).
	srcPayload := bytes.Repeat([]byte("S"), 1000)
	src := filepath.Join(dir, "solid.7z")
	if err := os.WriteFile(src, srcPayload, 0o644); err != nil {
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
	minOK := convert.FlattenMinOKSize(int64(len(srcPayload)))
	if minOK != 500 {
		t.Fatalf("minOK=%d want 500", minOK)
	}

	list := solidListForCache(src)
	// Write under-floor partial (tiny) so post-populate size floor rejects dest.
	run := func(_ string, args []string, _ string) error {
		if len(args) > 0 && args[0] == "a" {
			var dest string
			for _, a := range args {
				if strings.Contains(a, "nonsolid.partial") || (strings.HasSuffix(a, ".7z") && !strings.HasPrefix(a, "-")) {
					dest = a
					if strings.Contains(a, "nonsolid.partial") {
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
			return os.WriteFile(dest, []byte("tiny"), 0o644)
		}
		if len(args) > 0 && args[0] == "x" {
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
		return nil
	}

	wantDest := convert.NonsolidCacheDestPath(cache, src)
	_, err = convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         run,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err == nil {
		t.Fatal("expected under-floor error")
	}
	if !strings.Contains(err.Error(), "too small") {
		t.Fatalf("err=%v", err)
	}
	if _, stErr := os.Stat(wantDest); stErr == nil {
		t.Fatalf("under-floor dest must be removed: %s", wantDest)
	}
}

func TestEnsureNonsolidCachedCopy_CleansLeftoverPartialAndWork(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "solid.7z")
	if err := os.WriteFile(src, []byte("solid-payload-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": cache,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	dest := convert.NonsolidCacheDestPath(cache, src)
	partial := convert.NonsolidPartialPath(dest)
	work := convert.NonsolidPartialWorkPath(dest)
	// Simulate leftovers from a prior crashed populate.
	if err := os.WriteFile(partial, []byte("stale-partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "old", "x"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	var creates atomic.Int32
	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        solidListForCache(src),
		Run7z:         solidPopulateRun(t, &creates, 0),
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != dest {
		t.Fatalf("got %q want %q", got, dest)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("partial leftover must be cleaned: %v", err)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("work leftover must be cleaned: %v", err)
	}
	// Dest must be the new payload, not the stale partial.
	b, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(b, nonsolidCachedPayload) {
		t.Fatalf("dest content wrong len=%d err=%v", len(b), err)
	}
}

func TestEnsureNonsolidCachedCopy_EncryptedExtractFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "enc.7z")
	if err := os.WriteFile(src, []byte("solid-looking"), 0o644); err != nil {
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
	// List says solid (not encrypted) — encryption only surfaces mid-extract.
	list := solidListForCache(src)
	run := func(_ string, args []string, _ string) error {
		if len(args) > 0 && args[0] == "x" {
			return fmt.Errorf("7z failed: 7z x -y: ERROR: Wrong password : %s", src)
		}
		return nil
	}
	_, err = convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         run,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err == nil {
		t.Fatal("expected encrypted extract error")
	}
	if !strings.Contains(err.Error(), convert.Encrypted7zMessage) {
		t.Fatalf("err=%v", err)
	}
	// Non-encryption extract failures should pass through.
	runPlain := func(_ string, args []string, _ string) error {
		if len(args) > 0 && args[0] == "x" {
			return errors.New("7z failed: disk full")
		}
		return nil
	}
	_, err = convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         runPlain,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err == nil {
		t.Fatal("expected plain extract error")
	}
	if strings.Contains(err.Error(), convert.Encrypted7zMessage) {
		t.Fatalf("must not mislabel plain error: %v", err)
	}
	if !strings.Contains(err.Error(), "disk full") {
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

	var creates atomic.Int32
	listCalls := 0
	list := solidListForCache(src)
	listCounting := func(bin string, args []string, cwd string) (string, error) {
		listCalls++
		return list(bin, args, cwd)
	}
	run := solidPopulateRun(t, &creates, 0)

	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        listCounting,
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
	// Lock file is created beside dest for exclusive flock.
	lockPath := convert.NonsolidCacheLockPath(want)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file %s: %v", lockPath, err)
	}
	if creates.Load() != 1 {
		t.Fatalf("creates=%d want 1", creates.Load())
	}

	// Cache hit — second call should not re-run create with empty dest.
	listCalls = 0
	got2, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z: listCounting,
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
	if creates.Load() != 1 {
		t.Fatalf("cache hit must not re-create; creates=%d", creates.Load())
	}
}

func TestEnsureNonsolidCachedCopy_ConcurrentPopulateOnce(t *testing.T) {
	t.Parallel()
	// filelock_other.go stubs flock; concurrent serialization is Unix-only.
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
	default:
		t.Skip("blocking exclusive flock required")
	}
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

	var creates atomic.Int32
	// Slow create so both callers miss the pre-lock fast path.
	run := solidPopulateRun(t, &creates, 80*time.Millisecond)
	list := solidListForCache(src)
	params := convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         run,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	}

	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	paths := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = convert.EnsureNonsolidCachedCopy(cfg, src, params)
		}(i)
	}
	wg.Wait()

	want := convert.NonsolidCacheDestPath(cache, src)
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if paths[i] != want {
			t.Fatalf("goroutine %d path=%q want %q", i, paths[i], want)
		}
	}
	if creates.Load() != 1 {
		t.Fatalf("expected single populate under flock, creates=%d", creates.Load())
	}
	st, err := os.Stat(want)
	if err != nil || st.Size() == 0 {
		t.Fatalf("dest: %v", err)
	}
}

func TestEnsureNonsolidCachedCopy_CorruptDestRepopulates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "solid.7z")
	if err := os.WriteFile(src, []byte("solid-payload-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromMap(map[string]any{
		"convert_7z_nonsolid":  true,
		"convert_7z_scope":     "outer",
		"convert_7z_cache_dir": cache,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	dest := convert.NonsolidCacheDestPath(cache, src)
	// Corrupt/solid-looking dest must not count as a hit.
	if err := os.WriteFile(dest, []byte("corrupt-solid-looking"), 0o644); err != nil {
		t.Fatal(err)
	}

	var creates atomic.Int32
	list := func(_ string, args []string, _ string) (string, error) {
		path := ""
		if len(args) > 0 {
			path = args[len(args)-1]
		}
		if path == src {
			return sampleSolidList, nil
		}
		// Before repopulate: existing dest still lists solid → miss.
		// After create writes nonsolid payload, treat as non-solid.
		if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, nonsolidCachedPayload) {
			return sampleNonSolidList, nil
		}
		return sampleSolidList, nil
	}
	run := solidPopulateRun(t, &creates, 0)

	got, err := convert.EnsureNonsolidCachedCopy(cfg, src, convert.NonsolidCacheParams{
		List7z:        list,
		Run7z:         run,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != dest {
		t.Fatalf("got %q want %q", got, dest)
	}
	if creates.Load() != 1 {
		t.Fatalf("expected repopulate creates=1 got %d", creates.Load())
	}
	b, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(b, nonsolidCachedPayload) {
		t.Fatalf("dest content len=%d err=%v", len(b), err)
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
