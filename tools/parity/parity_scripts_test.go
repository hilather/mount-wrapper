// Package parity validates inventory scripts are present and parse cleanly.
// Run: go test ./tools/parity/
package parity_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// tools/parity → repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestParityScripts_bashSyntax(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tools", "parity")
	scripts := []string{"cli_surface.sh", "gen_config_keys.sh", "socket_ops.sh", "run_all.sh"}
	for _, name := range scripts {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing script %s: %v", name, err)
		}
		cmd := exec.Command("bash", "-n", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash -n %s: %v\n%s", name, err, out)
		}
	}
}

// macOS GHA uses bash 3.2; nested heredoc-in-$() historically broke bash -n.
// When docker is available, re-check cli_surface.sh under bash:3.2.
func TestParityScripts_bash32SyntaxIfDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	root := repoRoot(t)
	path := filepath.Join(root, "tools", "parity", "cli_surface.sh")
	cmd := exec.Command("docker", "run", "--rm", "-v", path+":/s:ro", "bash:3.2", "bash", "-n", "/s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash:3.2 bash -n cli_surface.sh: %v\n%s", err, out)
	}
}

func TestListKeys_runs(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "run", "./tools/parity/cmd/listkeys")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("HOME")+"/.local/go/bin:"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run listkeys: %v\n%s", err, out)
	}
	if len(out) < 20 {
		t.Fatalf("listkeys output too short: %q", out)
	}
	// Expect a known public key line.
	if !containsLine(string(out), "source_dirs") {
		t.Fatalf("listkeys missing source_dirs:\n%s", out)
	}
}

func containsLine(s, want string) bool {
	for _, line := range splitLines(s) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
