package config_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression: package-musl-release.sh names archives like goreleaser extras
// and updates SHA256SUMS without needing docker (fake bins under BIN_DIR).
func TestPackageMuslRelease_tarballNamesAndSums(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	script := filepath.Join(root, "scripts/package-musl-release.sh")

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	outDir := filepath.Join(tmp, "dist")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pretend metadata.json version matches what goreleaser would write.
	meta := `{"project_name":"mount-wrapper","version":"v9.9.9-test","tag":"v9.9.9"}`
	if err := os.WriteFile(filepath.Join(outDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing primary checksum line; musl append must preserve it.
	if err := os.WriteFile(filepath.Join(outDir, "SHA256SUMS"), []byte(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  mount-wrapper_v9.9.9-test_linux_amd64.tar.gz\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, arch := range []string{"amd64", "arm64"} {
		p := filepath.Join(binDir, "mount-wrapper-linux-"+arch+"-musl")
		// Not a real binary; packaging only copies + tars.
		if err := os.WriteFile(p, []byte("fake-musl-"+arch+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"BIN_DIR="+binDir,
		"OUT_DIR="+outDir,
		"ARCHS=amd64,arm64",
		"REQUIRE_ALL=1",
		"UPDATE_SUMS=1",
		// Leave VERSION empty so metadata.json is used.
		"VERSION=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("package-musl-release.sh: %v\n%s", err, out)
	}

	wantNames := []string{
		"mount-wrapper_v9.9.9-test_linux_amd64_musl.tar.gz",
		"mount-wrapper_v9.9.9-test_linux_arm64_musl.tar.gz",
	}
	for _, name := range wantNames {
		p := filepath.Join(outDir, name)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %s: %v\n%s", name, err, out)
		}
		if st.Size() < 20 {
			t.Fatalf("%s too small: %d", name, st.Size())
		}
		// Archive must contain the binary as mount-wrapper.
		if !tarHasMember(t, p, "mount-wrapper") {
			t.Errorf("%s missing mount-wrapper member", name)
		}
		if !tarHasMember(t, p, "MUSL.txt") {
			t.Errorf("%s missing MUSL.txt", name)
		}
	}

	sumsBody, err := os.ReadFile(filepath.Join(outDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	sums := string(sumsBody)
	if !strings.Contains(sums, "mount-wrapper_v9.9.9-test_linux_amd64.tar.gz") {
		t.Fatalf("SHA256SUMS lost primary entry:\n%s", sums)
	}
	for _, name := range wantNames {
		if !strings.Contains(sums, name) {
			t.Fatalf("SHA256SUMS missing %s:\n%s", name, sums)
		}
	}

	// Re-run must be idempotent (no duplicate musl lines).
	cmd2 := exec.Command("bash", script)
	cmd2.Dir = root
	cmd2.Env = cmd.Env
	if out2, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("second package-musl-release.sh: %v\n%s", err, out2)
	}
	sums2, err := os.ReadFile(filepath.Join(outDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(sums2), "_musl.tar.gz"); c != 2 {
		t.Fatalf("expected 2 musl lines after re-run, got %d:\n%s", c, sums2)
	}
}

func tarHasMember(t *testing.T, path, want string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == want || strings.HasSuffix(name, "/"+want) {
			return true
		}
	}
}
