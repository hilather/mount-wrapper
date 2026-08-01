package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// TestHomebrewFormulaSketchContent guards the ship-ready formula sketch:
// GoReleaser darwin archive names, ratarmount-rs-only policy, macOS path layout.
// Does not run brew.
func TestHomebrewFormulaSketchContent(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "packaging/homebrew/mount-wrapper.rb.example"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	needles := []string{
		"class MountWrapper < Formula",
		`version "0.1.3"`,
		"mount-wrapper_#{version}_darwin_arm64.tar.gz",
		"mount-wrapper_#{version}_darwin_amd64.tar.gz",
		"releases/download/v#{version}/",
		"REPLACE_ME_DARWIN_ARM64",
		"REPLACE_ME_DARWIN_AMD64",
		"macFUSE",
		"Application Support/mount-wrapper",
		"Library/Caches/mount-wrapper/run",
		"ratarmount-rs",
		"mount_backend: rust",
		"brew install --formula",
		"update-homebrew-formula.sh",
		// Engines not bundled.
		"NOT bundled",
	}
	for _, n := range needles {
		if !strings.Contains(s, n) {
			t.Errorf("formula missing %q", n)
		}
	}

	// Must not recommend Python ratarmount (backend is rust-only since 0.1.1).
	forbidden := []string{
		"Python ratarmount on PATH",
		"or Python ratarmount",
		"pip install ratarmount",
		"depends_on \"python",
		"depends_on \"ratarmount\"",
	}
	for _, n := range forbidden {
		if strings.Contains(s, n) {
			t.Errorf("formula must not mention Python ratarmount / pip engine: found %q", n)
		}
	}
	// Caveats may say "Python ratarmount is not supported" — that is allowed.
	// Ensure we never list python as an install option in desc.
	if strings.Contains(s, `desc "`) {
		// desc line only
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, `desc "`) && strings.Contains(strings.ToLower(line), "python") {
				t.Errorf("desc must not mention Python: %s", line)
			}
		}
	}
}

// TestUpdateHomebrewFormulaScript rewrites a formula copy from fixture SHA256SUMS.
func TestUpdateHomebrewFormulaScript(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts/update-homebrew-formula.sh")
	formulaSrc := filepath.Join(root, "packaging/homebrew/mount-wrapper.rb.example")
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	formulaCopy := filepath.Join(tmp, "mount-wrapper.rb")
	srcBody, err := os.ReadFile(formulaSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(formulaCopy, srcBody, 0o644); err != nil {
		t.Fatal(err)
	}

	const (
		ver     = "0.1.1"
		armHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		amdHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	sumsPath := filepath.Join(tmp, "SHA256SUMS")
	sums := strings.Join([]string{
		// Extra noise lines
		"# comment",
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  mount-wrapper_" + ver + "_linux_amd64.tar.gz",
		armHash + "  mount-wrapper_" + ver + "_darwin_arm64.tar.gz",
		amdHash + " *mount-wrapper_" + ver + "_darwin_amd64.tar.gz",
		"",
	}, "\n")
	if err := os.WriteFile(sumsPath, []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(tmp, "out.rb")
	cmd := exec.Command("bash", script, ver, sumsPath, outPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"FORMULA="+formulaCopy,
		// Clear env overrides that might point at real dist/
		"VERSION=",
		"SHA256SUMS=",
		"OUT=",
		"DRY_RUN=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update-homebrew-formula.sh: %v\n%s", err, out)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out: %v\n%s", err, out)
	}
	gs := string(got)
	for _, n := range []string{
		`version "` + ver + `"`,
		`sha256 "` + armHash + `"`,
		`sha256 "` + amdHash + `"`,
		"darwin_arm64.tar.gz",
		"darwin_amd64.tar.gz",
	} {
		if !strings.Contains(gs, n) {
			t.Errorf("rewritten formula missing %q\n---\n%s\n---\n%s", n, gs, out)
		}
	}
	if strings.Contains(gs, "REPLACE_ME_DARWIN_ARM64") || strings.Contains(gs, "REPLACE_ME_DARWIN_AMD64") {
		t.Errorf("placeholders left in rewritten formula:\n%s", gs)
	}

	// Idempotent re-run with OUT=same path must keep digests.
	cmd2 := exec.Command("bash", script, ver, sumsPath, outPath)
	cmd2.Dir = root
	cmd2.Env = append(os.Environ(), "FORMULA="+outPath, "VERSION=", "SHA256SUMS=", "OUT=", "DRY_RUN=0")
	if out2, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out2)
	}
	got2, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got2), `sha256 "`+armHash+`"`) || !strings.Contains(string(got2), `sha256 "`+amdHash+`"`) {
		t.Fatalf("second run lost digests:\n%s", got2)
	}
}

// TestUpdateHomebrewFormulaScript_envVersionKeepsPositionalSums ensures that
// when VERSION is already set, the first positional arg is SHA256SUMS (not version).
func TestUpdateHomebrewFormulaScript_envVersionKeepsPositionalSums(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts/update-homebrew-formula.sh")
	formulaSrc := filepath.Join(root, "packaging/homebrew/mount-wrapper.rb.example")

	tmp := t.TempDir()
	formulaCopy := filepath.Join(tmp, "mount-wrapper.rb")
	srcBody, err := os.ReadFile(formulaSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(formulaCopy, srcBody, 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		ver     = "0.1.1"
		armHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		amdHash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	sumsPath := filepath.Join(tmp, "SHA256SUMS")
	sums := armHash + "  mount-wrapper_" + ver + "_darwin_arm64.tar.gz\n" +
		amdHash + "  mount-wrapper_" + ver + "_darwin_amd64.tar.gz\n"
	if err := os.WriteFile(sumsPath, []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(tmp, "out.rb")

	// VERSION via env; first positional is sums (regression for accidental shift).
	cmd := exec.Command("bash", script, sumsPath, outPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"FORMULA="+formulaCopy,
		"VERSION="+ver,
		"SHA256SUMS=", // empty → allow positional sums
		"OUT=",
		"DRY_RUN=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("update-homebrew-formula.sh: %v\n%s", err, out)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	gs := string(got)
	if !strings.Contains(gs, `sha256 "`+armHash+`"`) || !strings.Contains(gs, `sha256 "`+amdHash+`"`) {
		t.Fatalf("env VERSION + positional sums failed:\n%s\n---\n%s", gs, out)
	}
}

// TestUpdateHomebrewFormulaScript_missingDigest fails closed when an arch is absent.
func TestUpdateHomebrewFormulaScript_missingDigest(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts/update-homebrew-formula.sh")
	formulaSrc := filepath.Join(root, "packaging/homebrew/mount-wrapper.rb.example")

	tmp := t.TempDir()
	formulaCopy := filepath.Join(tmp, "mount-wrapper.rb")
	srcBody, err := os.ReadFile(formulaSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(formulaCopy, srcBody, 0o644); err != nil {
		t.Fatal(err)
	}
	sumsPath := filepath.Join(tmp, "SHA256SUMS")
	// Only arm64 — amd64 missing must fail.
	if err := os.WriteFile(sumsPath, []byte(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  mount-wrapper_0.1.1_darwin_arm64.tar.gz\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", script, "0.1.1", sumsPath, filepath.Join(tmp, "out.rb"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "FORMULA="+formulaCopy, "VERSION=", "SHA256SUMS=", "OUT=", "DRY_RUN=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure for missing amd64 digest, got:\n%s", out)
	}
	if !strings.Contains(string(out), "darwin_amd64") {
		t.Errorf("error should mention missing amd64 archive:\n%s", out)
	}
}

// TestUpdateHomebrewFormulaScriptSyntax ensures bash -n.
func TestUpdateHomebrewFormulaScriptSyntax(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "scripts/update-homebrew-formula.sh")
	cmd := exec.Command("bash", "-n", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}
