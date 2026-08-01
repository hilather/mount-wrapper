package mounter_test

import (
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
)

func TestNormalizeMountBackend_aliases(t *testing.T) {
	t.Parallel()
	// Python aliases are rejected.
	for _, a := range []string{"python", "Python", "ratarmount", "py", "cpython", "RATARMOUNT"} {
		if _, err := mounter.NormalizeMountBackend(a); err == nil {
			t.Fatalf("expected error for python alias %q", a)
		}
		if mounter.IsRustBackend(a) {
			t.Fatalf("IsRustBackend should be false for %q", a)
		}
	}
	rust := []string{"rust", "Rust", "ratarmount-rs", "ratarmount_rs", "rs", "native", ""}
	for _, a := range rust {
		got, err := mounter.NormalizeMountBackend(a)
		if err != nil || got != mounter.BackendRust {
			t.Fatalf("alias %q: got %q err=%v want rust", a, got, err)
		}
		if !mounter.IsRustBackend(a) {
			t.Fatalf("IsRustBackend wrong for %q", a)
		}
	}
	if _, err := mounter.NormalizeMountBackend("java"); err == nil {
		t.Fatal("expected error for java")
	}
}

func TestDefaultRatarmountBin(t *testing.T) {
	t.Parallel()
	if got := mounter.DefaultRatarmountBin("rust"); got != config.DefaultRustRatarmountBin {
		t.Fatalf("rust default=%q", got)
	}
	// Invalid still returns rust binary name for messaging.
	if got := mounter.DefaultRatarmountBin("python"); got != config.DefaultRustRatarmountBin {
		t.Fatalf("invalid backend default=%q", got)
	}
}

func TestBackendLabel(t *testing.T) {
	t.Parallel()
	if got := mounter.BackendLabel("rust"); got == "" || got == "rust" {
		t.Fatalf("label=%q", got)
	}
}

func TestResolveRatarmountBin(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	explicit := filepath.Join(tmp, "my-rm")
	mustWriteExec(t, explicit)

	whichMap := map[string]string{}
	which := func(name string) string { return whichMap[name] }

	// Explicit wins.
	got, err := mounter.ResolveRatarmountBin("rust", explicit, mounter.ResolveOptions{
		Which: which, IsExecutable: func(path string) bool { return path == explicit },
	})
	if err != nil || got != explicit {
		t.Fatalf("explicit: got %q err=%v", got, err)
	}

	// Python backend rejected.
	if _, err := mounter.ResolveRatarmountBin("python", explicit, mounter.ResolveOptions{}); err == nil {
		t.Fatal("expected python backend error")
	}

	// Default name executable via PATH.
	whichMap["ratarmount-rs"] = filepath.Join(tmp, "from-path")
	isExecPath := func(path string) bool {
		return path == filepath.Join(tmp, "from-path") || path == filepath.Join(tmp, "sibling")
	}
	got, err = mounter.ResolveRatarmountBin("rust", "", mounter.ResolveOptions{
		Which: which, IsExecutable: isExecPath,
	})
	if err != nil || got != filepath.Join(tmp, "from-path") {
		t.Fatalf("path resolve: got %q err=%v", got, err)
	}

	// Sibling release when PATH empty.
	delete(whichMap, "ratarmount-rs")
	sibling := filepath.Join(tmp, "sibling")
	got, err = mounter.ResolveRatarmountBin("rust", "", mounter.ResolveOptions{
		Which:              which,
		IsExecutable:       isExecPath,
		SiblingRustRelease: sibling,
	})
	if err != nil || got != sibling {
		t.Fatalf("sibling: got %q err=%v", got, err)
	}

	// Fallback to default name when nothing found.
	got, err = mounter.ResolveRatarmountBin("rust", "", mounter.ResolveOptions{
		Which:        func(string) string { return "" },
		IsExecutable: func(string) bool { return false },
	})
	if err != nil || got != config.DefaultRustRatarmountBin {
		t.Fatalf("fallback: got %q err=%v", got, err)
	}

	// Invalid backend.
	if _, err := mounter.ResolveRatarmountBin("java", "", mounter.ResolveOptions{}); err == nil {
		t.Fatal("expected invalid backend error")
	}

	// SearchPath disabled skips PATH.
	whichMap["ratarmount-rs"] = filepath.Join(tmp, "from-path")
	got, err = mounter.ResolveRatarmountBin("rust", "", mounter.ResolveOptions{
		Which:              which,
		IsExecutable:       func(string) bool { return false },
		SearchPathDisabled: true,
	})
	if err != nil || got != config.DefaultRustRatarmountBin {
		t.Fatalf("search disabled: got %q err=%v", got, err)
	}
}

func mustWriteExec(t *testing.T, path string) {
	t.Helper()
	if err := writeFileMode(path, "#!/bin/sh\n", 0o755); err != nil {
		t.Fatal(err)
	}
}
