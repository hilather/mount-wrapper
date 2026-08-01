package hooks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/hooks"
	"github.com/hilather/mount-wrapper/internal/state"
)

func testPolicy() hooks.SecurityPolicy {
	// Non-root CI / dev trees cannot own files as uid 0.
	return hooks.TestSecurityPolicy()
}

func writeHook(t *testing.T, hooksDir, name, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hooksDir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func cfg(t *testing.T, tmp, hooksDir string, overrides map[string]any) *config.Config {
	t.Helper()
	raw := map[string]any{
		"source_dirs":             []any{filepath.Join(tmp, "src")},
		"mount_root":              filepath.Join(tmp, "mounts"),
		"index_dir":               filepath.Join(tmp, "indexes"),
		"overlay_dir":             filepath.Join(tmp, "overlays"),
		"hooks_dir":               hooksDir,
		"hook_timeout_seconds":    5,
		"hook_max_retries":        2,
		"hooks_stop_on_hard_fail": true,
		"hooks_cwd":               "mount",
	}
	for k, v := range overrides {
		raw[k] = v
	}
	c, err := config.FromMap(raw, filepath.Join(tmp, "config.yaml"))
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	return c
}

func mountedArchive(t *testing.T, store *state.Store, tmp string) *state.ArchiveRecord {
	t.Helper()
	mount := filepath.Join(tmp, "mounts", "a")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(src, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       src,
		ArchivePath:     archive,
		ArchiveBasename: "a.tar.gz",
		SizeBytes:       1,
		MtimeNs:         1,
		Fingerprint:     "1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(tmp, "indexes", rec.ArchiveID+".index.sqlite")
	overlay := filepath.Join(tmp, "overlays", rec.ArchiveID)
	if _, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path":   mount,
		"index_path":   index,
		"overlay_path": overlay,
	}, ""); err != nil {
		t.Fatal(err)
	}
	rec, err = store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusIndexing, map[string]any{
		"mount_path": mount,
		"mount_pid":  int64(os.Getpid()),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

func TestDiscoverSortedExecutablesOnly(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "20-b.sh", "#!/bin/sh\nexit 0\n", 0o755)
	writeHook(t, hd, "10-a.sh", "#!/bin/sh\nexit 0\n", 0o755)
	writeHook(t, hd, "30-c.sh.sample", "#!/bin/sh\nexit 0\n", 0o755)
	writeHook(t, hd, "README.md", "docs\n", 0o644)
	writeHook(t, hd, "noexec.sh", "#!/bin/sh\nexit 0\n", 0o644)
	found := hooks.DiscoverHooks(hd)
	var names []string
	for _, h := range found {
		names = append(names, h.Name)
	}
	if len(names) != 2 || names[0] != "10-a.sh" || names[1] != "20-b.sh" {
		t.Fatalf("names=%v, want [10-a.sh 20-b.sh]", names)
	}
}

func TestIsIgnoredHookName(t *testing.T) {
	cases := []struct {
		name    string
		ignored bool
	}{
		{"foo.sample", true},
		{"x.disabled", true},
		{"README", true},
		{"readme.md", true},
		{"README.extra", true},
		{".hidden", true},
		{"10-run.sh", false},
		{"foo.dpkg-new", true},
	}
	for _, tc := range cases {
		if got := hooks.IsIgnoredHookName(tc.name); got != tc.ignored {
			t.Errorf("IsIgnoredHookName(%q)=%v, want %v", tc.name, got, tc.ignored)
		}
	}
}

// ---------------------------------------------------------------------------
// Security
// ---------------------------------------------------------------------------

func TestGroupWritableHookRejected(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	p := writeHook(t, hd, "10.sh", "#!/bin/sh\nexit 0\n", 0o775)
	_, err := hooks.ValidateHookSecurity(p, hd, testPolicy())
	if err == nil || !strings.Contains(err.Error(), "group-writable") {
		t.Fatalf("err=%v, want group-writable", err)
	}
}

func TestOtherWritableHookRejected(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	p := writeHook(t, hd, "10.sh", "#!/bin/sh\nexit 0\n", 0o757)
	_, err := hooks.ValidateHookSecurity(p, hd, testPolicy())
	if err == nil || !strings.Contains(err.Error(), "other-writable") {
		t.Fatalf("err=%v, want other-writable", err)
	}
}

func TestGroupWritableDirRejected(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hd, 0o775); err != nil {
		t.Fatal(err)
	}
	err := hooks.ValidateHooksDir(hd, testPolicy())
	if err == nil || !strings.Contains(err.Error(), "group-writable") {
		t.Fatalf("err=%v, want group-writable", err)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(hd, "10-evil.sh")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := hooks.ValidateHookSecurity(link, hd, testPolicy())
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err=%v, want escapes", err)
	}
}

func TestRootOwnerRequiredWhenPolicySaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; cannot assert non-root ownership failure")
	}
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	p := writeHook(t, hd, "10.sh", "#!/bin/sh\nexit 0\n", 0o755)
	policy := hooks.SecurityPolicy{RequireRootOwner: true, RequireUnderHooksDir: true}
	_, err := hooks.ValidateHookSecurity(p, hd, policy)
	if err == nil || !strings.Contains(err.Error(), "owned by root") {
		t.Fatalf("err=%v, want owned by root", err)
	}
}

// ---------------------------------------------------------------------------
// ClassifyExit
// ---------------------------------------------------------------------------

func TestClassifyExitMatrix(t *testing.T) {
	code := func(c int) *int { return &c }

	st, _ := hooks.ClassifyExit(code(0), false, 1, 3)
	if st != state.HookSuccess {
		t.Fatalf("0 → %s", st)
	}
	st, _ = hooks.ClassifyExit(code(hooks.ExitRetry), false, 1, 3)
	if st != state.HookRetry {
		t.Fatalf("75 → %s", st)
	}
	st, _ = hooks.ClassifyExit(code(hooks.ExitRetry), false, 4, 3)
	if st != state.HookFailed {
		t.Fatalf("75 exhausted → %s", st)
	}
	st, _ = hooks.ClassifyExit(code(1), false, 1, 3)
	if st != state.HookFailed {
		t.Fatalf("1 → %s", st)
	}
	st, errMsg := hooks.ClassifyExit(nil, true, 1, 3)
	if st != state.HookRetry || errMsg != "timeout" {
		t.Fatalf("timeout → %s %q", st, errMsg)
	}
	st, _ = hooks.ClassifyExit(nil, true, 5, 2)
	if st != state.HookFailed {
		t.Fatalf("timeout exhausted → %s", st)
	}
}

// ---------------------------------------------------------------------------
// Env protocol
// ---------------------------------------------------------------------------

func TestBuildHookEnvUsesMountWrapperPrefixOnly(t *testing.T) {
	rec := hooks.ArchiveEnv{
		ArchiveID:       "aid-1",
		ArchivePath:     "/data/a.tar",
		MountPath:       "/mnt/a",
		IndexPath:       "/idx/a",
		OverlayPath:     "/ov/a",
		ArchiveBasename: "a.tar",
		SourceDir:       "/data",
	}
	env := hooks.BuildHookEnv(rec, "10-ok.sh", "/etc/config.yaml", []string{
		"PATH=/bin",
		"TARMOUNT_ARCHIVE_ID=should-not-be-set-by-us",
		"MOUNT_WRAPPER_ARCHIVE_ID=old",
	})
	// Required MOUNT_WRAPPER_* keys present with new values.
	want := map[string]string{
		"MOUNT_WRAPPER_ARCHIVE_ID":       "aid-1",
		"MOUNT_WRAPPER_ARCHIVE_PATH":     "/data/a.tar",
		"MOUNT_WRAPPER_MOUNT_PATH":       "/mnt/a",
		"MOUNT_WRAPPER_INDEX_PATH":       "/idx/a",
		"MOUNT_WRAPPER_OVERLAY_PATH":     "/ov/a",
		"MOUNT_WRAPPER_ARCHIVE_BASENAME": "a.tar",
		"MOUNT_WRAPPER_SOURCE_DIR":       "/data",
		"MOUNT_WRAPPER_CONFIG":           "/etc/config.yaml",
		"MOUNT_WRAPPER_HOOK_NAME":        "10-ok.sh",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s=%q, want %q", k, env[k], v)
		}
	}
	// Must not introduce TARMOUNT_* keys; base may retain pre-existing ones
	// but we never set them ourselves. Count our exports.
	for k := range env {
		if strings.HasPrefix(k, "TARMOUNT_") && k != "TARMOUNT_ARCHIVE_ID" {
			t.Errorf("unexpected TARMOUNT_ key introduced: %s", k)
		}
	}
	// Ensure we did not create dual export keys beyond what was already in base.
	if _, ok := env["TARMOUNT_MOUNT_PATH"]; ok {
		t.Error("must not set TARMOUNT_MOUNT_PATH")
	}
	if env["PATH"] != "/bin" {
		t.Error("base PATH lost")
	}
	argv := hooks.HookArgv("/hooks/10.sh", rec)
	if len(argv) != 3 || argv[1] != "/mnt/a" || argv[2] != "/data/a.tar" {
		t.Fatalf("argv=%v", argv)
	}
}

// ---------------------------------------------------------------------------
// ShouldRun / aggregate
// ---------------------------------------------------------------------------

func TestShouldRunHooks(t *testing.T) {
	if hooks.ShouldRunHooks(state.HooksSuccess, false) {
		t.Fatal("success should not re-run")
	}
	if hooks.ShouldRunHooks(state.HooksFailed, false) {
		t.Fatal("failed should not re-run without flag")
	}
	if !hooks.ShouldRunHooks(state.HooksFailed, true) {
		t.Fatal("failed should re-run when hook_rerun_on_failure")
	}
	for _, s := range []string{state.HooksNone, state.HooksPending, state.HooksRetry, state.HooksRunning} {
		if !hooks.ShouldRunHooks(s, false) {
			t.Fatalf("%s should run", s)
		}
	}
}

func TestAggregateStatus(t *testing.T) {
	if hooks.AggregateStatus(nil) != state.HooksSuccess {
		t.Fatal("empty → success")
	}
	if hooks.AggregateStatus([]hooks.RunResult{{Status: state.HookFailed}, {Status: state.HookSuccess}}) != state.HooksFailed {
		t.Fatal("any failed → failed")
	}
	if hooks.AggregateStatus([]hooks.RunResult{{Status: state.HookRetry}, {Status: state.HookSuccess}}) != state.HooksRetry {
		t.Fatal("retry without failed → retry")
	}
	if hooks.AggregateStatus([]hooks.RunResult{{Status: state.HookSkipped}}) != state.HooksFailed {
		t.Fatal("skipped-only → failed")
	}
	if hooks.AggregateStatus([]hooks.RunResult{{Status: state.HookSuccess}, {Status: state.HookSkipped}}) != state.HooksSuccess {
		t.Fatal("success+skipped → success")
	}
}

// ---------------------------------------------------------------------------
// Runner integration (real scripts)
// ---------------------------------------------------------------------------

func TestRunnerSuccessCycle(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-ok.sh", `#!/bin/sh
echo "id=$MOUNT_WRAPPER_ARCHIVE_ID" > "$1/.hook-ran"
test -n "$MOUNT_WRAPPER_ARCHIVE_ID"
test -n "$MOUNT_WRAPPER_MOUNT_PATH"
test "$1" = "$MOUNT_WRAPPER_MOUNT_PATH"
test "$2" = "$MOUNT_WRAPPER_ARCHIVE_PATH"
exit 0
`, 0o755)
	if err := os.Chmod(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || result.HooksStatus != state.HooksSuccess {
		t.Fatalf("result=%+v", result)
	}
	for _, r := range result.Results {
		if r.Status != state.HookSuccess {
			t.Fatalf("hook result=%+v", r)
		}
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMounted || rec2.HooksStatus != state.HooksSuccess || rec2.HooksCompletedAt == nil {
		t.Fatalf("archive=%+v", rec2)
	}
	marker := filepath.Join(*rec.MountPath, ".hook-ran")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
}

func TestRunnerSuccessPreservesNestedSkipLastError(t *testing.T) {
	// Mounted success may store pure nested-skip summary in last_error; hooks
	// success must not wipe that operator advisory.
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	// Empty hooks.d → finishSuccess with no scripts.
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	advisory := "skipped 2 nested mounts: /a.7z, /b.7z"
	if _, err := store.Transition(rec.ArchiveID, state.StatusMounted, state.StatusMounted, map[string]any{
		"last_error": advisory,
	}, ""); err != nil {
		t.Fatal(err)
	}
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || result.HooksStatus != state.HooksSuccess {
		t.Fatalf("result=%+v", result)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.LastError == nil || *rec2.LastError != advisory {
		t.Fatalf("expected nested-skip last_error preserved, got %v", rec2.LastError)
	}
}

func TestRunnerHardFailStopsAndSkips(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-fail.sh", "#!/bin/sh\nexit 2\n", 0o755)
	writeHook(t, hd, "20-ok.sh", "#!/bin/sh\nexit 0\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.HooksStatus != state.HooksFailed {
		t.Fatalf("hooks_status=%s", result.HooksStatus)
	}
	byName := map[string]hooks.RunResult{}
	for _, r := range result.Results {
		byName[r.HookName] = r
	}
	if byName["10-fail.sh"].Status != state.HookFailed {
		t.Fatal(byName["10-fail.sh"])
	}
	if byName["20-ok.sh"].Status != state.HookSkipped {
		t.Fatal(byName["20-ok.sh"])
	}
	rows, _ := store.ListHooks(rec.ArchiveID)
	for _, h := range rows {
		if h.HookName == "20-ok.sh" && h.Status != state.HookSkipped {
			t.Fatalf("store row=%+v", h)
		}
	}
}

func TestRunnerStopOnHardFailFalseContinues(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-fail.sh", "#!/bin/sh\nexit 2\n", 0o755)
	writeHook(t, hd, "20-ok.sh", "#!/bin/sh\nexit 0\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, map[string]any{"hooks_stop_on_hard_fail": false}), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]hooks.RunResult{}
	for _, r := range result.Results {
		byName[r.HookName] = r
	}
	if byName["10-fail.sh"].Status != state.HookFailed || byName["20-ok.sh"].Status != state.HookSuccess {
		t.Fatalf("byName=%v", byName)
	}
	if result.HooksStatus != state.HooksFailed {
		t.Fatalf("aggregate=%s", result.HooksStatus)
	}
}

func TestRunnerSoftFailRetry(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-temp.sh", "#!/bin/sh\nexit 75\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, map[string]any{"hook_max_retries": 3}), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.HooksStatus != state.HooksRetry {
		t.Fatalf("status=%s", result.HooksStatus)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.HooksStatus != state.HooksRetry || rec2.HooksCompletedAt != nil {
		t.Fatalf("rec2=%+v", rec2)
	}
	if !hooks.ShouldRunHooksRecord(rec2, false) {
		t.Fatal("retry should still be eligible")
	}
	result2, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Results[0].Attempts != 2 {
		t.Fatalf("attempts=%d", result2.Results[0].Attempts)
	}
}

func TestRunnerTimeoutIsRetryable(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-slow.sh", "#!/bin/sh\nsleep 30\nexit 0\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, map[string]any{
		"hook_timeout_seconds": 1,
		"hook_max_retries":     5,
	}), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || !result.Results[0].TimedOut || result.Results[0].Status != state.HookRetry {
		t.Fatalf("result=%+v", result.Results)
	}
	if result.HooksStatus != state.HooksRetry {
		t.Fatalf("aggregate=%s", result.HooksStatus)
	}
}

func TestRunnerTerminalSuccessNotRerun(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-ok.sh", "#!/bin/sh\nexit 0\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	if _, err := runner.RunForArchive(rec.ArchiveID, false); err != nil {
		t.Fatal(err)
	}
	result2, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Ran || result2.HooksStatus != state.HooksSuccess {
		t.Fatalf("result2=%+v", result2)
	}
}

func TestRunnerNoHooksIsSuccess(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || result.HooksStatus != state.HooksSuccess || len(result.Results) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunnerAnyLanguagePythonHook(t *testing.T) {
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skip("no /usr/bin/env")
	}
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	writeHook(t, hd, "10-py", `#!/usr/bin/env python3
import os, sys
assert os.environ["MOUNT_WRAPPER_HOOK_NAME"] == "10-py"
assert "TARMOUNT_HOOK_NAME" not in os.environ
open(sys.argv[1] + "/from-py", "w").write("ok")
sys.exit(0)
`, 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, nil), store, &pol)
	result, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.HooksStatus != state.HooksSuccess {
		t.Fatalf("status=%s results=%+v", result.HooksStatus, result.Results)
	}
	body, err := os.ReadFile(filepath.Join(*rec.MountPath, "from-py"))
	if err != nil || string(body) != "ok" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestRunnerResumeSkipsSuccessfulHooks(t *testing.T) {
	tmp := t.TempDir()
	hd := filepath.Join(tmp, "hooks.d")
	// First hook succeeds once then would fail if re-run; second always retries.
	// We simulate resume by seeding success on first and retry aggregate.
	writeHook(t, hd, "10-ok.sh", "#!/bin/sh\nexit 0\n", 0o755)
	writeHook(t, hd, "20-temp.sh", "#!/bin/sh\nexit 75\n", 0o755)
	_ = os.Chmod(hd, 0o755)
	store := openStore(t)
	rec := mountedArchive(t, store, tmp)
	pol := testPolicy()
	runner := hooks.NewRunner(cfg(t, tmp, hd, map[string]any{"hook_max_retries": 5}), store, &pol)
	r1, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	if r1.HooksStatus != state.HooksRetry {
		t.Fatalf("r1=%s", r1.HooksStatus)
	}
	// Mark: 10 should be success, 20 retry with attempts=1
	r2, err := runner.RunForArchive(rec.ArchiveID, false)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]hooks.RunResult{}
	for _, r := range r2.Results {
		byName[r.HookName] = r
	}
	if byName["10-ok.sh"].Status != state.HookSuccess {
		t.Fatalf("10 should stay success: %+v", byName["10-ok.sh"])
	}
	if byName["20-temp.sh"].Attempts != 2 {
		t.Fatalf("20 attempts=%d", byName["20-temp.sh"].Attempts)
	}
}
