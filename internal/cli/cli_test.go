package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/platform"
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
		"retry", "mount", "unmount", "purge", "hooks", "reload", "stop", "version",
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

func TestDoctorFixSystemdDryRun(t *testing.T) {
	// Real CLI: --fix-systemd --dry-run must not write the default drop-in path
	// (we cannot safely point DropinPath from CLI). Use a writable temp config
	// and assert preview content appears without creating the system drop-in.
	// Override is not available on CLI, so we only check that:
	// 1) exit is ok/error from host checks (not usage)
	// 2) output contains dry-run + unit markers
	// 3) default system path was not created under a fake root — skip if root
	//    could write system paths; instead use --json details via library path
	//    covered in doctor package. Here we verify flag wiring + stdout notes.
	cfgPath := writeTempConfig(t, "")
	// Ensure default drop-in is not present / not writable by this test user
	// in normal CI; we only assert CLI surfaces dry-run content.
	defaultDropin := "/etc/systemd/system/mount-wrapper.service.d/sources.conf"
	before, _ := os.Stat(defaultDropin)

	code, out, errBuf := runCLI(t, "doctor", "--config", cfgPath, "--fix-systemd", "--dry-run")
	if code != ExitOK && code != ExitError {
		t.Fatalf("unexpected exit %d stderr=%s out=%s", code, errBuf, out)
	}
	combined := out + errBuf
	if !strings.Contains(combined, "dry-run") {
		t.Fatalf("expected dry-run in output: out=%q stderr=%q", out, errBuf)
	}
	if !strings.Contains(out, "[Service]") && !strings.Contains(out, "Generated by mount-wrapper doctor") {
		// Text format puts unit in notes; accept either marker.
		t.Fatalf("expected systemd unit preview in stdout: %q", out)
	}
	if !strings.Contains(out, "fix_systemd") {
		t.Fatalf("expected fix_systemd check in report: %q", out)
	}

	after, err := os.Stat(defaultDropin)
	if before == nil && err == nil {
		// File appeared during dry-run — fail hard.
		t.Fatalf("dry-run created default drop-in %s", defaultDropin)
	}
	if before != nil && after != nil && after.ModTime().After(before.ModTime()) {
		t.Fatalf("dry-run modified default drop-in %s", defaultDropin)
	}
}

func TestDoctorFixSystemdDryRunJSON(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, out, errBuf := runCLI(t, "doctor", "--config", cfgPath, "--fix-systemd", "--dry-run", "--json")
	if code != ExitOK && code != ExitError {
		t.Fatalf("unexpected exit %d stderr=%s", code, errBuf)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json: %v out=%q stderr=%s", err, out, errBuf)
	}
	fixes, _ := data["fixes_applied"].([]any)
	if len(fixes) != 0 {
		t.Fatalf("dry-run must not report fixes_applied: %v", fixes)
	}
	notes, _ := data["notes"].([]any)
	foundDry := false
	foundUnit := false
	for _, n := range notes {
		s, _ := n.(string)
		if strings.Contains(s, "dry-run") {
			foundDry = true
		}
		if strings.Contains(s, "[Service]") || strings.Contains(s, "Generated by mount-wrapper doctor") {
			foundUnit = true
		}
	}
	if !foundDry || !foundUnit {
		t.Fatalf("notes want dry-run + unit: %v", notes)
	}
	// Locate fix_systemd check details.content
	checks, _ := data["checks"].([]any)
	var fix map[string]any
	for _, c := range checks {
		m, _ := c.(map[string]any)
		if m["name"] == "fix_systemd" {
			fix = m
			break
		}
	}
	if fix == nil {
		t.Fatalf("missing fix_systemd check: %v", data)
	}
	if fix["ok"] != true {
		t.Fatalf("fix_systemd not ok: %v", fix)
	}
	details, _ := fix["details"].(map[string]any)
	if details["dry_run"] != true {
		t.Fatalf("details.dry_run: %v", details)
	}
	content, _ := details["content"].(string)
	if !strings.Contains(content, "[Service]") {
		t.Fatalf("details.content missing unit: %q", content)
	}
}

func TestDoctorHelpListsDryRun(t *testing.T) {
	code, out, errBuf := runCLI(t, "doctor", "-h")
	if code != ExitOK {
		t.Fatalf("exit %d stderr=%s", code, errBuf)
	}
	help := out + errBuf
	if !strings.Contains(help, "dry-run") {
		t.Fatalf("doctor -h should list --dry-run: %q", help)
	}
	if !strings.Contains(help, "fix-systemd") {
		t.Fatalf("doctor -h should list --fix-systemd: %q", help)
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

func TestHandleControlError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"permission_denied", &ControlError{Message: "not authorized", Code: "PERMISSION_DENIED"}, ExitPermission},
		{"unavailable", &ControlError{Message: "dial: connection refused", Code: "UNAVAILABLE"}, ExitServiceUnavailable},
		{"other_control", &ControlError{Message: "bad request", Code: "ERROR"}, ExitError},
		{"plain", errors.New("not a control error"), ExitError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := handleControlError(&stderr, tc.err)
			if got != tc.want {
				t.Fatalf("got exit %d want %d stderr=%q", got, tc.want, stderr.String())
			}
			if tc.err == nil {
				if stderr.Len() != 0 {
					t.Fatalf("nil err should not write stderr: %q", stderr.String())
				}
				return
			}
			if !strings.Contains(stderr.String(), "error:") {
				t.Fatalf("expected error: prefix, got %q", stderr.String())
			}
		})
	}
}

// startAuthDenyControlServer runs a short Unix control server that always
// rejects peer auth (mirrors control.TestServerAuthDeny).
func startAuthDenyControlServer(t *testing.T) (sock string, cleanup func()) {
	t.Helper()
	sock = testutil.ShortUnixSocketPath(t, "cli-auth-deny.sock")
	falseVal := false
	srv := control.NewServer(sock, func(map[string]any) map[string]any {
		return control.OKResponse(map[string]any{"should": "not-reach"})
	}, false)
	srv.PeerCredentials = func(net.Conn) (platform.PeerCreds, bool) {
		return platform.PeerCreds{UID: 99999, GID: 99999, PID: 1}, true
	}
	srv.UserInGroup = func(int, string) bool { return false }
	srv.AllowUnauthEnv = &falseVal
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

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
	// Allow listener to accept.
	time.Sleep(20 * time.Millisecond)

	cleanup = func() {
		close(stop)
		wg.Wait()
		_ = srv.Close()
	}
	return sock, cleanup
}

func TestStatusPermissionDenied(t *testing.T) {
	sock, cleanup := startAuthDenyControlServer(t)
	defer cleanup()

	code, _, errBuf := runCLI(t, "status", "--socket", sock)
	if code != ExitPermission {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitPermission, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
	}
}

func TestReloadPermissionDenied(t *testing.T) {
	sock, cleanup := startAuthDenyControlServer(t)
	defer cleanup()

	code, _, errBuf := runCLI(t, "reload", "--socket", sock)
	if code != ExitPermission {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitPermission, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
	}
}

func TestStopPermissionDenied(t *testing.T) {
	sock, cleanup := startAuthDenyControlServer(t)
	defer cleanup()

	code, _, errBuf := runCLI(t, "stop", "--socket", sock)
	if code != ExitPermission {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitPermission, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
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

func TestStopServiceUnavailable(t *testing.T) {
	cfgPath := writeTempConfig(t, "")
	code, _, errBuf := runCLI(t, "stop", "--config", cfgPath)
	if code != ExitServiceUnavailable {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitServiceUnavailable, code, errBuf)
	}
	if !strings.Contains(errBuf, "error:") {
		t.Fatalf("expected error message, got %q", errBuf)
	}
}

// startScheduledOKServer runs a control server that answers the given op with
// {"<op>":"scheduled"}. Returns socket path and a cleanup func.
func startScheduledOKServer(t *testing.T, op string) (sock string, cleanup func()) {
	t.Helper()
	sock = testutil.ShortUnixSocketPath(t, "cli-"+op+".sock")
	wantOp := op
	srv := control.NewServer(sock, func(req map[string]any) map[string]any {
		if req["op"] != wantOp {
			return control.ErrResponse("unexpected op", "ERROR")
		}
		return control.OKResponse(map[string]any{wantOp: "scheduled"})
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				srv.ServeReady()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	// Allow listener to accept.
	time.Sleep(20 * time.Millisecond)

	cleanup = func() {
		close(stopCh)
		wg.Wait()
		_ = srv.Close()
	}
	return sock, cleanup
}

// startReloadOKServer runs a control server that answers op=reload with the
// standard scheduled ack. Returns socket path and a cleanup func.
func startReloadOKServer(t *testing.T) (sock string, cleanup func()) {
	return startScheduledOKServer(t, "reload")
}

func startStopOKServer(t *testing.T) (sock string, cleanup func()) {
	return startScheduledOKServer(t, "stop")
}

func TestReloadSuccessHumanMessage(t *testing.T) {
	sock, cleanup := startReloadOKServer(t)
	defer cleanup()

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

func TestStopSuccessHumanMessage(t *testing.T) {
	sock, cleanup := startStopOKServer(t)
	defer cleanup()

	code, out, errBuf := runCLI(t, "stop", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitOK, code, errBuf)
	}
	if !strings.Contains(out, "stop scheduled") {
		t.Fatalf("expected human success line, got %q", out)
	}
	if strings.Contains(out, "{") {
		t.Fatalf("stop should not dump JSON ack by default: %q", out)
	}
}

func TestHooksRerunCLI(t *testing.T) {
	sock := testutil.ShortUnixSocketPath(t, "cli-hooks-run.sock")
	var lastReq map[string]any
	srv := control.NewServer(sock, func(req map[string]any) map[string]any {
		lastReq = req
		if req["op"] != "hooks_run" {
			return control.ErrResponse("unexpected op", "ERROR")
		}
		return control.OKResponse(map[string]any{
			"archive_id":   req["archive_id"],
			"ran":          true,
			"hooks_status": "success",
			"force":        req["force"],
		})
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

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
		_ = srv.Close()
	}()
	time.Sleep(20 * time.Millisecond)

	// Flags before positional (Go flag.Parse stops at first non-flag).
	code, out, errBuf := runCLI(t, "hooks", "rerun", "--force", "--socket", sock, "arch-1")
	if code != ExitOK {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitOK, code, errBuf)
	}
	if !strings.Contains(out, "hooks ran") || !strings.Contains(out, "arch-1") {
		t.Fatalf("human output: %q", out)
	}
	if lastReq == nil || lastReq["force"] != true || lastReq["archive_id"] != "arch-1" {
		t.Fatalf("request fields: %+v", lastReq)
	}

	code, out, errBuf = runCLI(t, "hooks", "rerun", "--json", "--socket", sock, "arch-2")
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"archive_id"`) || !strings.Contains(out, "arch-2") {
		t.Fatalf("json output: %q", out)
	}
	if lastReq["force"] != false {
		t.Fatalf("default force should be false: %+v", lastReq)
	}
}

func TestFormatHooksRerunHuman(t *testing.T) {
	got := formatHooksRerunHuman(map[string]any{
		"archive_id":     "abc",
		"ran":            false,
		"hooks_status":   "success",
		"force":          false,
		"skipped_reason": "hooks_status=success is terminal or not eligible",
	})
	if !strings.Contains(got, "hooks skipped") || !strings.Contains(got, "abc") {
		t.Fatalf("got %q", got)
	}
	got = formatHooksRerunHuman(map[string]any{
		"archive_id":   "abc",
		"ran":          true,
		"hooks_status": "success",
		"force":        true,
	})
	if !strings.Contains(got, "hooks ran") || !strings.Contains(got, "force=true") {
		t.Fatalf("got %q", got)
	}
}

func TestReloadSuccessJSON(t *testing.T) {
	sock, cleanup := startReloadOKServer(t)
	defer cleanup()

	code, out, errBuf := runCLI(t, "reload", "--socket", sock, "--json")
	if code != ExitOK {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitOK, code, errBuf)
	}
	if strings.Contains(out, "reload scheduled\n") && !strings.Contains(out, "{") {
		t.Fatalf("expected JSON, got human line: %q", out)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("stdout not parseable JSON: %v out=%q", err, out)
	}
	if data["reload"] != "scheduled" {
		t.Fatalf("want reload=scheduled, got %v", data)
	}
}

func TestStopSuccessJSON(t *testing.T) {
	sock, cleanup := startStopOKServer(t)
	defer cleanup()

	code, out, errBuf := runCLI(t, "stop", "--socket", sock, "--json")
	if code != ExitOK {
		t.Fatalf("want exit %d, got %d stderr=%s", ExitOK, code, errBuf)
	}
	if strings.Contains(out, "stop scheduled\n") && !strings.Contains(out, "{") {
		t.Fatalf("expected JSON, got human line: %q", out)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("stdout not parseable JSON: %v out=%q", err, out)
	}
	if data["stop"] != "scheduled" {
		t.Fatalf("want stop=scheduled, got %v", data)
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
		{[]string{"--help"}, ExitOK, "reload flags"},
		{[]string{"-h"}, ExitOK, "serve"},
		{[]string{"version"}, ExitOK, "0.0.0-test"},
		{[]string{"doctor", "-h"}, ExitOK, "json"},
		{[]string{"status", "-h"}, ExitOK, "sizes"},
		{[]string{"config", "help"}, ExitOK, "show"},
		{[]string{"hooks", "help"}, ExitOK, "list"},
		{[]string{"hooks", "help"}, ExitOK, "rerun"},
		{[]string{"hooks", "rerun"}, ExitUsage, "ARCHIVE_ID"},
		{[]string{"hooks", "rerun", "-h"}, ExitOK, "force"},
		{[]string{"metrics", "-h"}, ExitOK, "no-cache"},
		{[]string{"metrics", "-h"}, ExitOK, "json"},
		{[]string{"reload", "-h"}, ExitOK, "json"},
		{[]string{"reload", "--help"}, ExitOK, "socket"},
		{[]string{"reload", "extra-arg"}, ExitUsage, "unexpected"},
		{[]string{"reload", "--not-a-flag"}, ExitUsage, ""},
		{[]string{"stop", "-h"}, ExitOK, "json"},
		{[]string{"stop", "--help"}, ExitOK, "socket"},
		{[]string{"stop", "extra-arg"}, ExitUsage, "unexpected"},
		{[]string{"stop", "--not-a-flag"}, ExitUsage, ""},
		{[]string{"retry"}, ExitUsage, "ARCHIVE_ID"},
		{[]string{"mount"}, ExitUsage, "PATH"},
		{[]string{"unmount", "--all", "x"}, ExitUsage, "--all"},
		{[]string{"purge", "id", "--yes", "--not-a-flag"}, ExitUsage, ""},
		{[]string{"rescan", "-h"}, ExitOK, "json"},
		{[]string{"retry", "-h"}, ExitOK, "json"},
		{[]string{"mount", "-h"}, ExitOK, "json"},
		{[]string{"unmount", "-h"}, ExitOK, "json"},
		{[]string{"purge", "-h"}, ExitOK, "json"},
		{[]string{"hooks", "list", "-h"}, ExitOK, "json"},
		{[]string{"hooks", "status", "-h"}, ExitOK, "json"},
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

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KiB"},
		{1536, "1.50 KiB"},
		{1024 * 1024, "1.00 MiB"},
		{10 * 1024, "10.0 KiB"},
		{100 * 1024, "100 KiB"},
		{5 * 1024 * 1024 * 1024, "5.00 GiB"},
		{-1, "—"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d)=%q want %q", tc.n, got, tc.want)
		}
	}
	if formatBytesNullable(nil) != "—" {
		t.Fatal("nil → —")
	}
	if formatBytesNullable(float64(1024)) != "1.00 KiB" {
		t.Fatalf("float64: %q", formatBytesNullable(float64(1024)))
	}
}

func TestFormatMetricsHumanSummaryAndRows(t *testing.T) {
	data := map[string]any{
		"summary": map[string]any{
			"archive_count":                   float64(2),
			"archives_with_extracted_size":    float64(2),
			"archives_with_convert_metadata":  float64(1),
			"total_archive_size_bytes":        float64(1024),
			"total_index_size_bytes":          float64(100),
			"total_extracted_size_bytes":      float64(4096),
			"total_space_saved_bytes":         float64(3996),
			"total_convert_source_size_bytes": float64(2048),
			"total_convert_size_delta_bytes":  float64(-1024),
			"max_convert_duration_seconds":    float64(12.4),
			"archives_with_convert_duration":  float64(1),
		},
		"metrics": []any{
			map[string]any{
				"archive_id":                   "bbbbbbbb-2222",
				"archive_basename":             "b.tar",
				"status":                       "mounted",
				"archive_size_bytes":           float64(500),
				"index_size_bytes":             float64(40),
				"extracted_size_bytes":         float64(2000),
				"space_saved_bytes":            float64(1960),
				"space_saved_vs_archive_bytes": float64(1460),
				"extracted_source":             "index",
			},
			map[string]any{
				"archive_id":                "aaaaaaaa-1111",
				"archive_basename":          "a.tar",
				"status":                    "mounted",
				"archive_size_bytes":        float64(524),
				"index_size_bytes":          float64(60),
				"extracted_size_bytes":      float64(2096),
				"space_saved_bytes":         float64(2036),
				"extracted_source":          "mount",
				"convert_source_size_bytes": float64(2048),
				"convert_size_delta_bytes":  float64(-1524),
				"convert_duration_seconds":  float64(12.4),
			},
		},
	}
	text := formatMetricsHuman(data)
	for _, want := range []string{
		"mount-wrapper metrics",
		"summary: archives=2",
		"with_extracted=2",
		"archive total:",
		"1.00 KiB",
		"space saved:",
		"convert source:",
		"convert delta:",
		"-1.00 KiB",
		"convert duration max: 12s",
		"archives:",
		// Sorted by basename: a.tar before b.tar
		"a.tar",
		"b.tar",
		"extracted=2.05 KiB (mount)",
		"extracted=1.95 KiB (index)",
		"space_saved=",
		"convert source=",
		"delta=-1.49 KiB",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	// a.tar should appear before b.tar
	ia := strings.Index(text, "a.tar")
	ib := strings.Index(text, "b.tar")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("expected a.tar before b.tar: ia=%d ib=%d\n%s", ia, ib, text)
	}
}

func TestFormatMetricsHumanSingleArchive(t *testing.T) {
	data := map[string]any{
		"metrics": map[string]any{
			"archive_id":           "abcdef12-zzzz",
			"archive_basename":     "solo.tar",
			"status":               "indexing",
			"archive_size_bytes":   float64(2048),
			"index_size_bytes":     nil,
			"extracted_size_bytes": nil,
			"error":                "index incomplete",
		},
	}
	text := formatMetricsHuman(data)
	for _, want := range []string{
		"mount-wrapper metrics",
		"solo.tar",
		"abcdef12",
		"archive=2.00 KiB",
		"index=—",
		"extracted=—",
		"err=index incomplete",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "summary:") {
		t.Fatalf("single-archive should not invent summary:\n%s", text)
	}
}

func TestFormatMetricsHumanEmpty(t *testing.T) {
	if !strings.Contains(formatMetricsHuman(nil), "empty") {
		t.Fatal("nil")
	}
	text := formatMetricsHuman(map[string]any{
		"summary": map[string]any{"archive_count": float64(0)},
		"metrics": []any{},
	})
	if !strings.Contains(text, "archives: (none)") {
		t.Fatalf("empty list: %q", text)
	}
}

func TestFormatStatusOutputWithSizesAppendix(t *testing.T) {
	data := map[string]any{
		"version": "1.0.0",
		"pid":     float64(7),
		"counts": map[string]any{
			"mounted": float64(1),
		},
		"mounted": float64(1),
		"archives": []any{
			map[string]any{
				"archive_id":       "aabbccdd-eeee",
				"archive_basename": "x.tar",
				"archive_path":     "/data/x.tar",
				"status":           "mounted",
				"hooks_status":     "success",
				"metrics": map[string]any{
					"archive_id":           "aabbccdd-eeee",
					"archive_basename":     "x.tar",
					"status":               "mounted",
					"archive_size_bytes":   float64(1024),
					"index_size_bytes":     float64(10),
					"extracted_size_bytes": float64(5000),
					"space_saved_bytes":    float64(4990),
					"extracted_source":     "index",
				},
			},
		},
		"metrics_summary": map[string]any{
			"archive_count":                  float64(1),
			"archives_with_extracted_size":   float64(1),
			"archives_with_convert_metadata": float64(0),
			"total_archive_size_bytes":       float64(1024),
			"total_index_size_bytes":         float64(10),
			"total_extracted_size_bytes":     float64(5000),
			"total_space_saved_bytes":        float64(4990),
		},
		"low_disk": false,
	}
	text := formatStatusOutput(data)
	// Human status header still present.
	if !strings.Contains(text, "mount-wrapper 1.0.0") {
		t.Fatalf("status header: %q", text)
	}
	if !strings.Contains(text, "mounted=1") {
		t.Fatalf("counts: %q", text)
	}
	// Sizes appendix (not raw JSON).
	for _, want := range []string{
		"sizes:",
		"summary: archives=1",
		"per-archive:",
		"x.tar",
		"space_saved=",
		"extracted=4.88 KiB (index)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"metrics_summary"`) || strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("should not dump JSON:\n%s", text)
	}
}

func TestFormatStatusOutputWithoutSizesOmitsAppendix(t *testing.T) {
	data := map[string]any{
		"version":  "1.0.0",
		"pid":      float64(1),
		"counts":   map[string]any{"mounted": float64(0)},
		"archives": []any{},
	}
	text := formatStatusOutput(data)
	if strings.Contains(text, "sizes:") {
		t.Fatalf("unexpected sizes appendix:\n%s", text)
	}
}

func TestMetricsHelpListsJSON(t *testing.T) {
	code, out, errBuf := runCLI(t, "metrics", "-h")
	if code != ExitOK {
		t.Fatalf("exit %d err=%s", code, errBuf)
	}
	help := out + errBuf
	for _, want := range []string{"json", "no-cache", "prefer-mount"} {
		if !strings.Contains(help, want) {
			t.Fatalf("metrics -h missing %q: %q", want, help)
		}
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

// startOKDataServer answers op with OKResponse(data). Returns socket + cleanup.
func startOKDataServer(t *testing.T, op string, data map[string]any) (sock string, cleanup func()) {
	t.Helper()
	sock = testutil.ShortUnixSocketPath(t, "cli-"+op+".sock")
	wantOp := op
	payload := data
	srv := control.NewServer(sock, func(req map[string]any) map[string]any {
		if req["op"] != wantOp {
			return control.ErrResponse("unexpected op", "ERROR")
		}
		return control.OKResponse(payload)
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				srv.ServeReady()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	cleanup = func() {
		close(stopCh)
		wg.Wait()
		_ = srv.Close()
	}
	return sock, cleanup
}

func TestFormatRescanHuman(t *testing.T) {
	got := formatRescanHuman(map[string]any{
		"seen":            float64(10),
		"inserted":        float64(2),
		"reappeared":      float64(1),
		"content_changed": float64(0),
		"absent":          float64(3),
		"stable":          float64(8),
		"duration_ms":     float64(42),
		"assume_stable":   true,
		"errors":          []any{"path/x: permission denied"},
	})
	for _, want := range []string{
		"rescan:",
		"seen=10",
		"inserted=2",
		"reappeared=1",
		"absent=3",
		"stable=8",
		"duration_ms=42",
		"assume_stable=true",
		"error: path/x: permission denied",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "{") {
		t.Fatalf("should not be JSON: %q", got)
	}
	if !strings.Contains(formatRescanHuman(map[string]any{"error": "boom"}), "rescan failed: boom") {
		t.Fatal("error field")
	}
	if formatRescanHuman(nil) != "rescan: ok\n" {
		t.Fatalf("nil: %q", formatRescanHuman(nil))
	}
}

func TestFormatRetryHuman(t *testing.T) {
	got := formatRetryHuman(map[string]any{
		"archive_id":     "arch-1",
		"status":         "discovered",
		"mount_attempts": float64(0),
	})
	if !strings.Contains(got, "retry archive_id=arch-1") || !strings.Contains(got, "status=discovered") ||
		!strings.Contains(got, "mount_attempts=0") {
		t.Fatalf("got %q", got)
	}
}

func TestFormatMountHuman(t *testing.T) {
	queued := formatMountHuman(map[string]any{
		"archive_id": "a1",
		"status":     "discovered",
		"queued":     true,
	})
	if !strings.Contains(queued, "mount queued") || !strings.Contains(queued, "a1") {
		t.Fatalf("queued: %q", queued)
	}
	started := formatMountHuman(map[string]any{
		"archive_id": "a1",
		"status":     "mounting",
		"pid":        float64(99),
		"mount_path": "/mnt/a",
	})
	for _, want := range []string{"mount started", "status=mounting", "pid=99", "mount_path=/mnt/a"} {
		if !strings.Contains(started, want) {
			t.Fatalf("missing %q in %q", want, started)
		}
	}
}

func TestFormatUnmountHuman(t *testing.T) {
	single := formatUnmountHuman(map[string]any{
		"archive_id": "x1",
		"status":     "absent",
	})
	if !strings.Contains(single, "unmounted archive_id=x1") || !strings.Contains(single, "status=absent") {
		t.Fatalf("single: %q", single)
	}
	all := formatUnmountHuman(map[string]any{
		"unmounted": []any{
			map[string]any{"archive_id": "ok1", "status": "absent"},
			map[string]any{"archive_id": "bad1", "error": "busy"},
		},
	})
	for _, want := range []string{
		"unmount --all: 2 archive(s)",
		"unmounted archive_id=ok1",
		"error archive_id=bad1: busy",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in %q", want, all)
		}
	}
	empty := formatUnmountHuman(map[string]any{"unmounted": []any{}})
	if !strings.Contains(empty, "0 archive(s)") || !strings.Contains(empty, "(none unmounted)") {
		t.Fatalf("empty all: %q", empty)
	}
}

func TestFormatPurgeHuman(t *testing.T) {
	got := formatPurgeHuman(map[string]any{
		"archive_id":     "p1",
		"index_deleted":  true,
		"overlay_action": "quarantine",
		"mount_cleaned":  false,
	})
	for _, want := range []string{
		"purged archive_id=p1",
		"index_deleted=true",
		"overlay_action=quarantine",
		"mount_cleaned=false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestFormatHooksListHuman(t *testing.T) {
	got := formatHooksListHuman(map[string]any{
		"hooks": []any{
			map[string]any{"name": "20-second", "path": "/h/20-second"},
			map[string]any{"name": "10-first", "path": "/h/10-first"},
		},
	})
	if !strings.Contains(got, "hooks (2):") {
		t.Fatalf("header: %q", got)
	}
	// Sorted by name: 10-first before 20-second
	i10 := strings.Index(got, "10-first")
	i20 := strings.Index(got, "20-second")
	if i10 < 0 || i20 < 0 || i10 > i20 {
		t.Fatalf("sort order: %q", got)
	}
	if formatHooksListHuman(map[string]any{"hooks": []any{}}) != "hooks: (none)\n" {
		t.Fatal("empty list")
	}
}

func TestFormatHooksStatusHuman(t *testing.T) {
	got := formatHooksStatusHuman(map[string]any{
		"archive_id":   "arch-z",
		"hooks_status": "success",
		"hooks": []any{
			map[string]any{
				"hook_name":      "notify",
				"status":         "success",
				"attempts":       float64(1),
				"last_exit_code": float64(0),
			},
			map[string]any{
				"hook_name":  "failing",
				"status":     "failed",
				"attempts":   float64(3),
				"last_error": "exit 1",
			},
		},
	})
	for _, want := range []string{
		"hooks status archive_id=arch-z",
		"hooks_status=success",
		"[success] notify attempts=1 exit=0",
		"[failed] failing attempts=3 err=exit 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	empty := formatHooksStatusHuman(map[string]any{
		"archive_id":   "x",
		"hooks_status": "none",
		"hooks":        []any{},
	})
	if !strings.Contains(empty, "(no hook rows)") {
		t.Fatalf("empty rows: %q", empty)
	}
}

func TestRescanCLIHumanAndJSON(t *testing.T) {
	payload := map[string]any{
		"seen": float64(5), "inserted": float64(1), "reappeared": float64(0),
		"content_changed": float64(0), "absent": float64(0), "stable": float64(4),
		"duration_ms": float64(12), "assume_stable": true,
	}
	sock, cleanup := startOKDataServer(t, "rescan", payload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "rescan", "--assume-stable", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "rescan:") || !strings.Contains(out, "seen=5") || !strings.Contains(out, "assume_stable=true") {
		t.Fatalf("human: %q", out)
	}
	if strings.Contains(out, "{") {
		t.Fatalf("default should not dump JSON: %q", out)
	}

	code, out, errBuf = runCLI(t, "rescan", "--json", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("json parse: %v out=%q", err, out)
	}
	if data["seen"] != float64(5) {
		t.Fatalf("seen=%v", data["seen"])
	}
}

func TestRetryCLIHumanAndJSON(t *testing.T) {
	payload := map[string]any{
		"archive_id": "id-1", "status": "discovered", "mount_attempts": float64(0),
	}
	sock, cleanup := startOKDataServer(t, "retry", payload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "retry", "--socket", sock, "id-1")
	if code != ExitOK {
		t.Fatalf("human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "retry archive_id=id-1") || strings.Contains(out, "{") {
		t.Fatalf("human: %q", out)
	}

	code, out, errBuf = runCLI(t, "retry", "--json", "--socket", sock, "id-1")
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"archive_id"`) {
		t.Fatalf("json: %q", out)
	}
}

func TestMountCLIHumanAndJSON(t *testing.T) {
	payload := map[string]any{
		"archive_id": "m1", "status": "indexing", "pid": float64(7), "mount_path": "/m/m1",
	}
	sock, cleanup := startOKDataServer(t, "mount", payload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "mount", "--socket", sock, "/tmp/a.tar")
	if code != ExitOK {
		t.Fatalf("human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "mount started") || !strings.Contains(out, "m1") || strings.Contains(out, "{") {
		t.Fatalf("human: %q", out)
	}

	code, out, errBuf = runCLI(t, "mount", "--json", "--socket", sock, "/tmp/a.tar")
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"mount_path"`) {
		t.Fatalf("json: %q", out)
	}
}

func TestUnmountCLIHumanAndJSON(t *testing.T) {
	payload := map[string]any{
		"unmounted": []any{
			map[string]any{"archive_id": "u1", "status": "absent"},
		},
	}
	sock, cleanup := startOKDataServer(t, "unmount", payload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "unmount", "--all", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "unmount --all") || !strings.Contains(out, "u1") || strings.Contains(out, "{") {
		t.Fatalf("human: %q", out)
	}

	code, out, errBuf = runCLI(t, "unmount", "--all", "--json", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"unmounted"`) {
		t.Fatalf("json: %q", out)
	}
}

func TestPurgeCLIHumanAndJSON(t *testing.T) {
	payload := map[string]any{
		"archive_id": "p1", "index_deleted": true, "overlay_action": "delete", "mount_cleaned": true,
	}
	sock, cleanup := startOKDataServer(t, "purge", payload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "purge", "--yes", "--socket", sock, "p1")
	if code != ExitOK {
		t.Fatalf("human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "purged archive_id=p1") || strings.Contains(out, "{") {
		t.Fatalf("human: %q", out)
	}

	code, out, errBuf = runCLI(t, "purge", "--yes", "--json", "--socket", sock, "p1")
	if code != ExitOK {
		t.Fatalf("json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"index_deleted"`) {
		t.Fatalf("json: %q", out)
	}
}

func TestHooksListAndStatusCLIHumanAndJSON(t *testing.T) {
	listPayload := map[string]any{
		"hooks": []any{map[string]any{"name": "h1", "path": "/hooks/h1"}},
	}
	sock, cleanup := startOKDataServer(t, "hooks_list", listPayload)
	defer cleanup()

	code, out, errBuf := runCLI(t, "hooks", "list", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("list human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "hooks (1):") || !strings.Contains(out, "h1") || strings.Contains(out, "{") {
		t.Fatalf("list human: %q", out)
	}
	code, out, errBuf = runCLI(t, "hooks", "list", "--json", "--socket", sock)
	if code != ExitOK {
		t.Fatalf("list json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"hooks"`) {
		t.Fatalf("list json: %q", out)
	}

	statusPayload := map[string]any{
		"archive_id": "a1", "hooks_status": "pending",
		"hooks": []any{map[string]any{"hook_name": "h1", "status": "pending", "attempts": float64(0)}},
	}
	sock2, cleanup2 := startOKDataServer(t, "hooks_status", statusPayload)
	defer cleanup2()

	code, out, errBuf = runCLI(t, "hooks", "status", "--socket", sock2, "a1")
	if code != ExitOK {
		t.Fatalf("status human exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, "hooks status archive_id=a1") || !strings.Contains(out, "pending") ||
		strings.Contains(out, "{") {
		t.Fatalf("status human: %q", out)
	}
	code, out, errBuf = runCLI(t, "hooks", "status", "--json", "--socket", sock2, "a1")
	if code != ExitOK {
		t.Fatalf("status json exit %d stderr=%s", code, errBuf)
	}
	if !strings.Contains(out, `"hooks_status"`) {
		t.Fatalf("status json: %q", out)
	}
}
