package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPackageContentsInventory runs scripts/smoke-package-contents.sh when
// nfpm and dpkg-deb are on PATH (default CI path for .deb layout). Soft-skips
// otherwise so developers without packaging tools keep make test green.
//
// Documented skip: tools not installed (see scripts/smoke-package-contents.sh).
// CI smoke.yml package-contents job installs nfpm and sets REQUIRE_TOOLS=1.
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
