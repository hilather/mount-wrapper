package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSeedConfigScript exercises packaging/scripts/seed-config.sh under MW_ROOT
// without root. Never runs create-user.sh.
func TestSeedConfigScript(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	script := filepath.Join(root, "packaging/scripts/seed-config.sh")
	exampleSrc := filepath.Join(root, "packaging/examples/config.yaml.example")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("seed-config.sh: %v", err)
	}
	if _, err := os.Stat(exampleSrc); err != nil {
		t.Fatalf("config.yaml.example: %v", err)
	}

	runSeed := func(t *testing.T, mwRoot string) {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "MW_ROOT="+mwRoot)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("seed-config.sh: %v\n%s", err, out)
		}
	}

	t.Run("seeds_when_missing", func(t *testing.T) {
		mw := t.TempDir()
		share := filepath.Join(mw, "usr/share/mount-wrapper")
		if err := os.MkdirAll(share, 0o755); err != nil {
			t.Fatal(err)
		}
		exBody, err := os.ReadFile(exampleSrc)
		if err != nil {
			t.Fatal(err)
		}
		exPath := filepath.Join(share, "config.yaml.example")
		if err := os.WriteFile(exPath, exBody, 0o644); err != nil {
			t.Fatal(err)
		}

		runSeed(t, mw)

		dest := filepath.Join(mw, "etc/mount-wrapper/config.yaml")
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("expected seeded config: %v", err)
		}
		if string(got) != string(exBody) {
			t.Fatalf("seeded content mismatch (len %d vs %d)", len(got), len(exBody))
		}
		info, err := os.Stat(dest)
		if err != nil {
			t.Fatal(err)
		}
		// Mode is best-effort 0640 when install/chmod works.
		mode := info.Mode().Perm()
		if mode&0o077 != 0 && mode != 0o644 {
			// Allow 0640 (desired) or 0644 (if chmod skipped on some FS); fail only if world-writable.
			if mode&0o002 != 0 {
				t.Errorf("seeded config world-writable: %04o", mode)
			}
		}
	})

	t.Run("never_overwrites_existing", func(t *testing.T) {
		mw := t.TempDir()
		share := filepath.Join(mw, "usr/share/mount-wrapper")
		etc := filepath.Join(mw, "etc/mount-wrapper")
		if err := os.MkdirAll(share, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(etc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(share, "config.yaml.example"), []byte("example-body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(etc, "config.yaml")
		const operator = "operator-custom-config\n"
		if err := os.WriteFile(dest, []byte(operator), 0o600); err != nil {
			t.Fatal(err)
		}

		runSeed(t, mw)

		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != operator {
			t.Fatalf("config was overwritten: got %q", got)
		}
	})

	t.Run("missing_example_is_noop", func(t *testing.T) {
		mw := t.TempDir()
		// No usr/share example — script must exit 0 and leave no dest.
		runSeed(t, mw)
		dest := filepath.Join(mw, "etc/mount-wrapper/config.yaml")
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("expected no config when example missing, err=%v", err)
		}
	})

	t.Run("idempotent_second_run", func(t *testing.T) {
		mw := t.TempDir()
		share := filepath.Join(mw, "usr/share/mount-wrapper")
		if err := os.MkdirAll(share, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(share, "config.yaml.example"), []byte("v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runSeed(t, mw)
		// Change example; second seed must not replace existing dest.
		if err := os.WriteFile(filepath.Join(share, "config.yaml.example"), []byte("v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runSeed(t, mw)
		got, err := os.ReadFile(filepath.Join(mw, "etc/mount-wrapper/config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "v1\n" {
			t.Fatalf("second run changed config: %q", got)
		}
	})
}

// TestSeedConfigScriptSyntax ensures bash -n on seed + postinstall.
func TestSeedConfigScriptSyntax(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, rel := range []string{
		"packaging/scripts/seed-config.sh",
		"packaging/scripts/nfpm-postinstall.sh",
	} {
		path := filepath.Join(root, rel)
		cmd := exec.Command("bash", "-n", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("bash -n %s: %v\n%s", rel, err, out)
		}
	}
}

// Guard: postinstall must not hard-require root-only paths when only seed is tested.
func TestPostinstallMentionsSeedNotOverwrite(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	body, err := os.ReadFile(filepath.Join(root, "packaging/scripts/nfpm-postinstall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, needle := range []string{
		"seed-config.sh",
		"create-user.sh",
		// Comment contract: never overwrite operator config.
		"never overwrites",
	} {
		if !strings.Contains(strings.ToLower(s), strings.ToLower(needle)) {
			t.Errorf("nfpm-postinstall.sh missing %q", needle)
		}
	}
}
