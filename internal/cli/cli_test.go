package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/testutil"
)

func testInfo() BuildInfo {
	return BuildInfo{Version: "0.0.0-test", Commit: "abc", Date: "today"}
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = RunWithIO(args, testInfo(), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func writeTempConfig(t *testing.T, socket string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if socket == "" {
		socket = filepath.Join(dir, "no.sock")
	}
	content := strings.Join([]string{
		"version: 1",
		"source_dirs: []",
		"control_socket: " + socket,
		"state_db: " + filepath.Join(dir, "state.db"),
		"mount_root: " + filepath.Join(dir, "mounts"),
		"index_dir: " + filepath.Join(dir, "indexes"),
		"overlay_dir: " + filepath.Join(dir, "overlays"),
		"hooks_dir: " + filepath.Join(dir, "hooks"),
		"pid_file: " + filepath.Join(dir, "mw.pid"),
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersion(t *testing.T) {
	code, out, err := runCLI(t, "version")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr=%s", code, err)
	}
	if !strings.Contains(out, "0.0.0-test") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestHelp(t *testing.T) {
	code, out, err := runCLI(t, "help")
	if code != ExitOK {
		t.Fatalf("exit %d stderr=%s", code, err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("missing usage: %q", out)
	}
	for _, cmd := range []string{
		"serve", "doctor", "config", "status", "metrics", "rescan",
		"retry", "mount", "unmount", "purge", "hooks", "reload", "version",
	} {
		if !strings.Contains(out, cmd) {
			t.Fatalf("help should list %q: %q", cmd, out)
		}
	}
}

func TestUnknown(t *testing.T) {
	code, _, _ := runCLI(t, "nope")
	if code != ExitUsage {
		t.Fatalf("want exit %d, got %d", ExitUsage, code)
	}
}

func TestEmptyArgs(t *testing.T) {
	code, _, _ := runCLI(t)
	if code != ExitUsage {
		t.Fatalf("want exit %d, got %d", ExitUsage, code)
	}
}

func TestServeHelp(t *testing.T) {
	code, out, errBuf := runCLI(t, "serve", "-h")
	if code != ExitOK {
		t.Fatalf("exit %d stderr=%s", code, errBuf)
	}
	help := out + errBuf
	if !strings.Contains(help, "once") && !strings.Contains(help, "config") {
		t.Fatalf("expected serve flags in help: %q", help)
	}
}

func TestServeUnknownFlag(t *testing.T) {
	code, _, errBuf := runCLI(t, "serve", "--not-a-flag")
	if code != ExitUsage {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitUsage, code, errBuf)
	}
}

func TestServeBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errBuf := runCLI(t, "serve", "--config", bad)
	if code != ExitError {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitError, code, errBuf)
	}
	if !strings.Contains(errBuf, "version") && !strings.Contains(errBuf, "config") {
		t.Fatalf("expected config error, got %q", errBuf)
	}
}

func TestDoctorJSONWithTempConfig(t *testing.T) {
	// Minimal valid config under temp dirs — no FUSE required for doctor library.
	cfgPath := writeTempConfig(t, "")
	code, out, errBuf := runCLI(t, "doctor", "--config", cfgPath, "--json")
	if code != ExitOK && code != ExitError {
		t.Fatalf("unexpected exit %d stderr=%s", code, errBuf)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json: %v out=%q stderr=%s", err, out, errBuf)
	}
	if _, ok := data["ok"]; !ok {
		t.Fatalf("missing ok: %v", data)
	}
	checks, ok := data["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("expected checks: %v", data)
	}
	// Config was loadable — report should mention path or include config check.
	foundConfigCheck := false
	for _, c := range checks {
		m, _ := c.(map[string]any)
		if m["name"] == "config" {
			foundConfigCheck = true
			break
		}
	}
	if !foundConfigCheck {
		t.Fatalf("expected config check in doctor report: %v", data)
	}
}

func TestDoctorMissingConfigStillRuns(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-config.yaml")
	code, out, errBuf := runCLI(t, "doctor", "--config", missing, "--json")
	if code != ExitOK && code != ExitError {
		t.Fatalf("unexpected exit %d stderr=%s", code, errBuf)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json: %v out=%q stderr=%s", err, out, errBuf)
	}
	// Missing file → no config_path (null/absent) but checks still run.
	if cp, ok := data["config_path"]; ok && cp != nil && cp != "" {
		t.Fatalf("expected empty config_path for missing file, got %v", cp)
	}
	checks, _ := data["checks"].([]any)
	if len(checks) == 0 {
		t.Fatal("expected host/binary checks without config")
	}
}

func TestConfigShowLocal(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, out, errBuf := runCLI(t, "config", "show", "--local", "--config", cfgPath)
	if code != ExitOK {
		t.Fatalf("exit %d stderr=%s", code, errBuf)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json: %v out=%q", err, out)
	}
	if data["config"] == nil {
		t.Fatalf("missing config key: %v", data)
	}
	cfgMap, _ := data["config"].(map[string]any)
	if cfgMap["control_socket"] == nil {
		t.Fatalf("expected control_socket in snapshot: %v", cfgMap)
	}
	if data["hot_reload_keys"] == nil {
		t.Fatalf("expected hot_reload_keys: %v", data)
	}
}

func TestConfigSetDryRunOffline(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	patch := `{"poll_interval_seconds": 15}`
	code, out, errBuf := runCLI(t, "config", "set", "--config", cfgPath, "--patch", "--json", patch, "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit %d stderr=%s out=%s", code, errBuf, out)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json: %v out=%q", err, out)
	}
	if data["valid"] != true {
		t.Fatalf("expected valid=true: %v", data)
	}
	if data["apply"] != false {
		t.Fatalf("expected apply=false for dry-run: %v", data)
	}
	if data["written"] != false {
		t.Fatalf("expected written=false: %v", data)
	}
	// File should be unchanged.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalSeconds != 60 {
		t.Fatalf("dry-run mutated poll_interval: %v", cfg.PollIntervalSeconds)
	}
}

func TestStatusServiceUnavailable(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, _, errBuf := runCLI(t, "status", "--config", cfgPath)
	if code != ExitServiceUnavailable {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitServiceUnavailable, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
	}
}

func TestStatusServiceUnavailableSocketOverride(t *testing.T) {
	// --socket alone (no readable config needed if path is absolute).
	sock := filepath.Join(t.TempDir(), "missing.sock")
	code, _, errBuf := runCLI(t, "status", "--socket", sock, "--json")
	if code != ExitServiceUnavailable {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitServiceUnavailable, code, errBuf)
	}
}

func TestRescanServiceUnavailable(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, _, errBuf := runCLI(t, "rescan", "--config", cfgPath)
	if code != ExitServiceUnavailable {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitServiceUnavailable, code, errBuf)
	}
}

func TestReloadServiceUnavailable(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, _, errBuf := runCLI(t, "reload", "--config", cfgPath)
	if code != ExitServiceUnavailable {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitServiceUnavailable, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
	}
}

func TestReloadSuccessHumanMessage(t *testing.T) {
	sock := testutil.ShortUnixSocketPath(t, "cli-reload.sock")
	srv := control.NewServer(sock, func(req map[string]any) map[string]any {
		if req["op"] != "reload" {
			return control.ErrResponse("unexpected op", "ERROR")
		}
		return control.OKResponse(map[string]any{"reload": "scheduled"})
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				srv.ServeReady()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()

	// Allow listener to accept.
	time.Sleep(20 * time.Millisecond)

	code, out, errBuf := runCLI(t, "reload", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitOK, code, errBuf)
	}
	if !strings.Contains(out, "reload scheduled") {
		t.Fatalf("expected human success line, got %q", out)
	}
	if strings.Contains(out, "{") {
		t.Fatalf("reload should not dump JSON ack by default: %q", out)
	}
}

func TestUnmountUsage(t *testing.T) {
	code, _, errBuf := runCLI(t, "unmount")
	if code != ExitUsage {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitUsage, code, errBuf)
	}
}

func TestPurgeRequiresYes(t *testing.T) {
	code, _, errBuf := runCLI(t, "purge", "some-id")
	if code != ExitUsage {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitUsage, code, errBuf)
	}
	if !strings.Contains(errBuf, "--yes") {
		t.Fatalf("expected --yes message: %q", errBuf)
	}
}

func TestHelpParseTable(t *testing.T) {
	cases := []struct {
		args     []string
		wantCode int
		wantOut  string // substring in stdout or stderr
	}{
		{[]string{"help"}, ExitOK, "doctor"},
		{[]string{"--help"}, ExitOK, "status"},
		{[]string{"-h"}, ExitOK, "serve"},
		{[]string{"version"}, ExitOK, "0.0.0-test"},
		{[]string{"doctor", "-h"}, ExitOK, "json"},
		{[]string{"status", "-h"}, ExitOK, "sizes"},
		{[]string{"config", "help"}, ExitOK, "show"},
		{[]string{"hooks", "help"}, ExitOK, "list"},
		{[]string{"metrics", "-h"}, ExitOK, "no-cache"},
		{[]string{"reload", "-h"}, ExitOK, "config"},
		{[]string{"reload", "--help"}, ExitOK, "socket"},
		{[]string{"reload", "extra-arg"}, ExitUsage, "unexpected"},
		{[]string{"reload", "--not-a-flag"}, ExitUsage, ""},
		{[]string{"retry"}, ExitUsage, "ARCHIVE_ID"},
		{[]string{"mount"}, ExitUsage, "PATH"},
		{[]string{"unmount", "--all", "x"}, ExitUsage, "--all"},
		{[]string{"purge", "id", "--yes", "--not-a-flag"}, ExitUsage, ""},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			code, out, errBuf := runCLI(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("args=%v want %d got %d out=%q err=%q", tc.args, tc.wantCode, code, out, errBuf)
			}
			if tc.wantOut != "" {
				combined := out + errBuf
				if !strings.Contains(combined, tc.wantOut) {
					t.Fatalf("args=%v missing %q in %q", tc.args, tc.wantOut, combined)
				}
			}
		})
	}
}

func TestFormatStatusHuman(t *testing.T) {
	text := formatStatusHuman(map[string]any{
		"version": "1.2.3",
		"pid":     float64(42),
		"counts": map[string]any{
			"mounted":    float64(2),
			"discovered": float64(1),
		},
		"archives": []any{
			map[string]any{
				"archive_id":   "abcdef12-zzzz",
				"status":       "mounted",
				"archive_path": "/tmp/a.tar",
				"mount_path":   "/mnt/a",
			},
		},
		"low_disk": false,
	})
	if !strings.Contains(text, "mount-wrapper 1.2.3") {
		t.Fatalf("header: %q", text)
	}
	if !strings.Contains(text, "mounted=2") {
		t.Fatalf("counts: %q", text)
	}
	if !strings.Contains(text, "/tmp/a.tar") {
		t.Fatalf("archive: %q", text)
	}
}

func TestControlClientUnavailable(t *testing.T) {
	c := newControlClient(filepath.Join(t.TempDir(), "no.sock"))
	_, err := c.RequestOK("status", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*ControlError)
	if !ok {
		t.Fatalf("want ControlError, got %T %v", err, err)
	}
	if ce.Code != "UNAVAILABLE" {
		t.Fatalf("code=%s", ce.Code)
	}
}

func TestResolveConfigPathDefault(t *testing.T) {
	// Smoke: ResolveConfigPath("") is non-empty platform path.
	p := config.ResolveConfigPath("")
	if p == "" {
		t.Fatal("empty default config path")
	}
	// Explicit wins.
	if got := config.ResolveConfigPath("/tmp/x.yaml"); got != "/tmp/x.yaml" {
		t.Fatalf("got %s", got)
	}
	// Darwin helper is distinct from Linux constant.
	darwin := config.DefaultConfigPathFor("darwin")
	linux := config.DefaultConfigPathFor("linux")
	if linux != config.DefaultConfigPath {
		t.Fatalf("linux default=%s", linux)
	}
	if !strings.Contains(darwin, "Application Support") && !strings.Contains(darwin, "mount-wrapper") {
		t.Fatalf("darwin default unexpected: %s", darwin)
	}
}
