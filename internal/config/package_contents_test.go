package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requiredTarMembers must match scripts/smoke-package-contents.sh
// REQUIRED_TAR_MEMBERS and .goreleaser.yaml archives.files packaging layout.
var requiredTarMembers = []string{
	"mount-wrapper",
	"packaging/systemd/mount-wrapper.service",
	"packaging/examples/config.yaml.example",
	"packaging/scripts/seed-config.sh",
	"packaging/scripts/create-user.sh",
	"packaging/man/mount-wrapper.1",
}

// TestPackageContentsInventory runs scripts/smoke-package-contents.sh when
// nfpm and dpkg-deb are on PATH (default CI path for .deb layout). Soft-skips
// otherwise so developers without packaging tools keep make test green.
//
// Documented skip: tools not installed (see scripts/smoke-package-contents.sh).
// CI smoke.yml package-contents job installs nfpm and sets REQUIRE_TOOLS=1.
//
// Always-on tar path (no nfpm): TestPackageTarInventory.
func TestPackageContentsInventory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping package contents smoke in -short (needs nfpm package)")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	script := filepath.Join(root, "scripts/smoke-package-contents.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("smoke-package-contents.sh: %v", err)
	}

	// Match scripts/smoke-package-contents.sh: prefer local Go install bins.
	pathEnv := os.Getenv("PATH")
	home, _ := os.UserHomeDir()
	extra := []string{
		filepath.Join(home, ".local/go/bin"),
		filepath.Join(home, "go/bin"),
	}
	if gp := os.Getenv("GOPATH"); gp != "" {
		extra = append(extra, filepath.Join(gp, "bin"))
	}
	pathEnv = strings.Join(append(extra, pathEnv), string(os.PathListSeparator))

	// Temporarily extend PATH for LookPath (restored via defer).
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", pathEnv)
	defer func() { _ = os.Setenv("PATH", oldPath) }()

	nfpm, errNfpm := exec.LookPath("nfpm")
	dpkg, errDpkg := exec.LookPath("dpkg-deb")
	if errNfpm != nil || errDpkg != nil {
		t.Skipf("package contents smoke needs nfpm+dpkg-deb on PATH (nfpm=%v dpkg-deb=%v); see scripts/smoke-package-contents.sh",
			errNfpm, errDpkg)
	}
	t.Logf("using nfpm=%s dpkg-deb=%s", nfpm, dpkg)

	// Prefer existing binary; only --build when missing (keeps test faster).
	args := []string{script}
	if _, err := os.Stat(filepath.Join(root, "bin/mount-wrapper")); err != nil {
		args = append(args, "--build")
	}

	cmd := exec.Command("bash", args...)
	cmd.Dir = root
	// Force hard failure if tools vanish mid-run; we already gated LookPath.
	cmd.Env = append(os.Environ(), "PATH="+pathEnv, "REQUIRE_TOOLS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke-package-contents.sh: %v\n%s", err, out)
	}
	s := string(out)
	// Sanity: required paths reported OK (script also exits non-zero on miss).
	for _, needle := range []string{
		"./usr/bin/mount-wrapper",
		"./lib/systemd/system/mount-wrapper.service",
		"./usr/share/mount-wrapper/config.yaml.example",
		"./usr/share/mount-wrapper/seed-config.sh",
		"./usr/share/mount-wrapper/create-user.sh",
		"./usr/share/man/man1/mount-wrapper.1",
		"smoke-package-contents OK",
	} {
		if !strings.Contains(s, needle) {
			t.Errorf("script output missing %q\n%s", needle, s)
		}
	}
}

// TestPackageTarInventory always runs under make test (no nfpm/dpkg-deb).
// Builds a minimal synthetic tar.gz matching GoReleaser relative layout and
// asserts REQUIRED_TAR_MEMBERS via smoke-package-contents.sh PACKAGE_TAR= +
// --tar-only (SKIP_DEB=1).
func TestPackageTarInventory(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	script := filepath.Join(root, "scripts/smoke-package-contents.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("smoke-package-contents.sh: %v", err)
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Fatalf("tar required for package tar inventory: %v", err)
	}

	// Repo files that must appear in primary release tarballs (relative layout).
	repoCopies := []string{
		"packaging/systemd/mount-wrapper.service",
		"packaging/examples/config.yaml.example",
		"packaging/scripts/seed-config.sh",
		"packaging/scripts/create-user.sh",
		"packaging/man/mount-wrapper.1",
	}
	for _, rel := range repoCopies {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("fixture source missing %s: %v", rel, err)
		}
	}

	t.Run("complete", func(t *testing.T) {
		tarball := buildSyntheticReleaseTar(t, root, repoCopies, true)
		out := runPackageContentsTar(t, root, script, tarball, true)
		s := string(out)
		for _, mem := range requiredTarMembers {
			// Script prints "OK  tar <member>" on success.
			if !strings.Contains(s, "OK  tar "+mem) && !strings.Contains(s, mem) {
				t.Errorf("script output missing tar member %q\n%s", mem, s)
			}
		}
		if !strings.Contains(s, "tar inventory OK") {
			t.Errorf("expected tar inventory OK\n%s", s)
		}
		if !strings.Contains(s, "smoke-package-contents OK") {
			t.Errorf("expected smoke-package-contents OK\n%s", s)
		}
		// Must not require nfpm for this path.
		if strings.Contains(s, "nfpm package") {
			t.Errorf("PACKAGE_TAR --tar-only must not run nfpm deb path\n%s", s)
		}
	})

	// Incomplete archive must fail (regression: assert members are enforced).
	t.Run("missing_seed_config", func(t *testing.T) {
		incomplete := make([]string, 0, len(repoCopies)-1)
		for _, rel := range repoCopies {
			if strings.HasSuffix(rel, "seed-config.sh") {
				continue
			}
			incomplete = append(incomplete, rel)
		}
		tarball := buildSyntheticReleaseTar(t, root, incomplete, true)
		out := runPackageContentsTar(t, root, script, tarball, false)
		s := string(out)
		if !strings.Contains(s, "MISSING required member in tar: packaging/scripts/seed-config.sh") &&
			!strings.Contains(s, "seed-config.sh") {
			t.Errorf("expected missing seed-config failure, got:\n%s", s)
		}
		if strings.Contains(s, "smoke-package-contents OK") {
			t.Errorf("incomplete tar must not report OK\n%s", s)
		}
	})
}

// buildSyntheticReleaseTar stages a stub binary plus packaging files and
// creates a gzip tar with GoReleaser-style relative members (no top-level dir).
func buildSyntheticReleaseTar(t *testing.T, root string, repoFiles []string, withBinary bool) string {
	t.Helper()
	stage := t.TempDir()
	if withBinary {
		// Stub binary name must match REQUIRED_TAR_MEMBERS "mount-wrapper".
		if err := os.WriteFile(filepath.Join(stage, "mount-wrapper"), []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range repoFiles {
		src := filepath.Join(root, rel)
		dst := filepath.Join(stage, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outDir := t.TempDir()
	tarball := filepath.Join(outDir, "mount-wrapper_synthetic_linux_amd64.tar.gz")
	// tar -C stage so members are relative (mount-wrapper, packaging/…).
	cmd := exec.Command("tar", "-czf", tarball, "-C", stage, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar czf: %v\n%s", err, out)
	}
	return tarball
}

func runPackageContentsTar(t *testing.T, root, script, tarball string, wantOK bool) []byte {
	t.Helper()
	cmd := exec.Command("bash", script, "--tar-only")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PACKAGE_TAR="+tarball,
		"SKIP_DEB=1",
		// Never soft-skip on missing tools for this always-on path.
		"REQUIRE_TOOLS=0",
	)
	out, err := cmd.CombinedOutput()
	if wantOK {
		if err != nil {
			t.Fatalf("smoke-package-contents.sh PACKAGE_TAR (want OK): %v\n%s", err, out)
		}
	} else if err == nil {
		t.Fatalf("smoke-package-contents.sh PACKAGE_TAR expected failure, got success:\n%s", out)
	}
	return out
}
