package mounter_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

func TestParseNestedMountFailure(t *testing.T) {
	t.Parallel()
	line := "[Warning] ratarmountcore.mountsource.compositing.automount: " +
		"Mounting of '/bad.7z' failed because of: corrupt data"
	got := mounter.ParseNestedMountFailure(line)
	if got == nil || got.Path != "/bad.7z" || got.Reason != "corrupt data" {
		t.Fatalf("got %+v", got)
	}
	if mounter.ParseNestedMountFailure("nothing here") != nil {
		t.Fatal("expected nil")
	}
	// Leading/trailing whitespace
	got = mounter.ParseNestedMountFailure("  " + line + "\n")
	if got == nil || got.Path != "/bad.7z" {
		t.Fatalf("whitespace: %+v", got)
	}
}

func TestFormatNestedSkipSummary(t *testing.T) {
	t.Parallel()
	if s := mounter.FormatNestedSkipSummary(nil, 3); s != "" {
		t.Fatalf("empty: %q", s)
	}
	if s := mounter.FormatNestedSkipSummary([]string{"/a.7z"}, 3); s != "skipped 1 nested mount: /a.7z" {
		t.Fatalf("one: %q", s)
	}
	paths := []string{"/a.7z", "/b.7z", "/c.7z"}
	if s := mounter.FormatNestedSkipSummary(paths, 3); s != "skipped 3 nested mounts: /a.7z, /b.7z, /c.7z" {
		t.Fatalf("three: %q", s)
	}
	paths = append(paths, "/d.7z", "/e.7z")
	want := "skipped 5 nested mounts: /a.7z, /b.7z, /c.7z (+2 more)"
	if s := mounter.FormatNestedSkipSummary(paths, 3); s != want {
		t.Fatalf("five: got %q want %q", s, want)
	}
	// maxSamples <= 0 uses default
	if s := mounter.FormatNestedSkipSummary(paths, 0); !strings.Contains(s, "(+2 more)") {
		t.Fatalf("default samples: %q", s)
	}
}

func TestEnrichReasonWithNestedSkips(t *testing.T) {
	t.Parallel()
	if g := mounter.EnrichReasonWithNestedSkips("boom", nil); g != "boom" {
		t.Fatalf("no skips: %q", g)
	}
	if g := mounter.EnrichReasonWithNestedSkips("", []string{"/x.7z"}); g != "skipped 1 nested mount: /x.7z" {
		t.Fatalf("empty reason: %q", g)
	}
	got := mounter.EnrichReasonWithNestedSkips(
		"ratarmount exited before mount ready",
		[]string{"/bad.7z", "/worse.7z"},
	)
	want := "ratarmount exited before mount ready; skipped 2 nested mounts: /bad.7z, /worse.7z"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPreserveNestedSkipInReason(t *testing.T) {
	t.Parallel()
	pure := "skipped 2 nested mounts: /a.7z, /b.7z"
	if g := mounter.PreserveNestedSkipInReason("hooks hard-failed", ""); g != "hooks hard-failed" {
		t.Fatalf("no prior: %q", g)
	}
	if g := mounter.PreserveNestedSkipInReason("hooks hard-failed", "engine exit 1"); g != "hooks hard-failed" {
		t.Fatalf("no skip segment: %q", g)
	}
	want := "hooks hard-failed; " + pure
	if g := mounter.PreserveNestedSkipInReason("hooks hard-failed", pure); g != want {
		t.Fatalf("pure prior: got %q want %q", g, want)
	}
	enrichedPrior := "ratarmount exited; " + pure
	if g := mounter.PreserveNestedSkipInReason("hooks hard-failed", enrichedPrior); g != want {
		t.Fatalf("enriched prior: got %q want %q", g, want)
	}
	if g := mounter.PreserveNestedSkipInReason(want, pure); g != want {
		t.Fatalf("idempotent: got %q", g)
	}
	if g := mounter.PreserveNestedSkipInReason("", pure); g != pure {
		t.Fatalf("empty reason: %q", g)
	}
}

func TestExtractNestedSkipSummary(t *testing.T) {
	t.Parallel()
	sum, n := mounter.ExtractNestedSkipSummary("")
	if sum != "" || n != 0 {
		t.Fatalf("empty: %q %d", sum, n)
	}
	sum, n = mounter.ExtractNestedSkipSummary("engine exit 1")
	if sum != "" || n != 0 {
		t.Fatalf("no skip: %q %d", sum, n)
	}
	pure := "skipped 2 nested mounts: /a.7z, /b.7z"
	sum, n = mounter.ExtractNestedSkipSummary(pure)
	if n != 2 || sum != pure {
		t.Fatalf("pure: sum=%q n=%d", sum, n)
	}
	enriched := "ratarmount exited; " + pure
	sum, n = mounter.ExtractNestedSkipSummary(enriched)
	if n != 2 || sum != pure {
		t.Fatalf("enriched: sum=%q n=%d", sum, n)
	}
	one := "skipped 1 nested mount: /x.7z"
	sum, n = mounter.ExtractNestedSkipSummary(one)
	if n != 1 || sum != one {
		t.Fatalf("one: sum=%q n=%d", sum, n)
	}
	if !mounter.IsNestedSkipOnlyLastError(pure) {
		t.Fatal("expected pure summary only")
	}
	if mounter.IsNestedSkipOnlyLastError(enriched) {
		t.Fatal("enriched should not be skip-only")
	}
	if mounter.IsNestedSkipOnlyLastError("boom") {
		t.Fatal("plain error is not skip-only")
	}
}

func TestMarkMounted_PersistsNestedSkipSummaryInLastError(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	rec, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusDiscovered, map[string]any{
		"mount_path": filepath.Join(cfg.MountRoot, "m"),
		"index_path": filepath.Join(cfg.IndexDir, "i.sqlite"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	managed := &mounter.ManagedMount{
		ArchiveID: rec.ArchiveID,
		Phase:     mounter.PhaseMount,
		PID:       4242,
		Request: mounter.MountRequest{
			ArchivePath:  archive,
			IndexPath:    filepath.Join(cfg.IndexDir, "i.sqlite"),
			MountPath:    filepath.Join(cfg.MountRoot, "m"),
			MountBackend: "rust",
		},
		StartedAt: time.Now(),
	}
	managed.NoteNestedSkip("/inner/broken.7z")
	managed.NoteNestedSkip("/inner/also.7z")
	eng.Live.Put(managed)

	updated, err := eng.MarkMounted(rec.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != state.StatusMounted {
		t.Fatalf("status=%s", updated.Status)
	}
	if updated.LastError == nil {
		t.Fatal("expected last_error with nested skip summary on mounted success")
	}
	le := *updated.LastError
	if !strings.Contains(le, "skipped 2 nested mounts") {
		t.Fatalf("last_error=%q", le)
	}
	if !strings.Contains(le, "/inner/broken.7z") {
		t.Fatalf("missing sample path: %q", le)
	}
	// Live entry kept for FUSE child.
	if eng.Live.Get(rec.ArchiveID) == nil {
		t.Fatal("expected live mount retained after MarkMounted")
	}
}

// Remount / re-MarkMounted: live SkippedNested empty but SQLite already holds a
// pure nested-skip advisory — must not wipe last_error to nil.
func TestMarkMounted_KeepsPureNestedSkipAdvisoryWhenLiveSkipsEmpty(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	advisory := "skipped 2 nested mounts: /inner/a.7z, /inner/b.7z"
	rec, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusDiscovered, map[string]any{
		"mount_path": filepath.Join(cfg.MountRoot, "m"),
		"index_path": filepath.Join(cfg.IndexDir, "i.sqlite"),
		"last_error": advisory,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastError == nil || *rec.LastError != advisory {
		t.Fatalf("precondition last_error=%v", rec.LastError)
	}

	// Live mount with no nested skips (remount FUSE child did not re-emit).
	managed := &mounter.ManagedMount{
		ArchiveID: rec.ArchiveID,
		Phase:     mounter.PhaseMount,
		PID:       5151,
		Request: mounter.MountRequest{
			ArchivePath:  archive,
			IndexPath:    filepath.Join(cfg.IndexDir, "i.sqlite"),
			MountPath:    filepath.Join(cfg.MountRoot, "m"),
			MountBackend: "rust",
		},
		StartedAt: time.Now(),
	}
	eng.Live.Put(managed)

	updated, err := eng.MarkMounted(rec.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != state.StatusMounted {
		t.Fatalf("status=%s", updated.Status)
	}
	if updated.LastError == nil {
		t.Fatal("expected pure nested-skip last_error retained when live skips empty")
	}
	if *updated.LastError != advisory {
		t.Fatalf("last_error=%q want %q", *updated.LastError, advisory)
	}

	// Enriched (failure) last_error is not skip-only — still cleared on clean mount.
	rec2, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusMounted, map[string]any{
		"last_error": "ratarmount exited; " + advisory,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	managed2 := &mounter.ManagedMount{
		ArchiveID: rec2.ArchiveID,
		Phase:     mounter.PhaseMount,
		PID:       5152,
		Request:   managed.Request,
		StartedAt: time.Now(),
	}
	eng.Live.Put(managed2)
	cleared, err := eng.MarkMounted(rec2.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.LastError != nil {
		t.Fatalf("enriched last_error should clear on clean mount, got %q", *cleared.LastError)
	}
}

// Index-phase nested skips must survive dropLive → FUSE beginMount: persist
// pure summary on the indexing→mounting transition and carry onto new live.
func TestCompleteIndexAndStartMount_PersistsAndCarriesNestedSkips(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	indexPath := filepath.Join(cfg.IndexDir, "i.sqlite")
	mountPath := filepath.Join(cfg.MountRoot, "m")
	// Index file present so IndexBuildVerified succeeds without a real child exit.
	if err := os.MkdirAll(cfg.IndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("idx"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := store.Transition(rec.ArchiveID, state.StatusIndexing, state.StatusDiscovered, map[string]any{
		"mount_path": mountPath,
		"index_path": indexPath,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	indexManaged := &mounter.ManagedMount{
		ArchiveID: rec.ArchiveID,
		Phase:     mounter.PhaseIndexOnly,
		PID:       1,
		Request: mounter.MountRequest{
			ArchivePath:  archive,
			IndexPath:    indexPath,
			MountPath:    mountPath,
			MountBackend: "rust",
			IndexOnly:    true,
		},
		StartedAt: time.Now(),
	}
	indexManaged.NoteNestedSkip("/nested/from-index.7z")
	indexManaged.NoteNestedSkip("/nested/also-index.7z")
	eng.Live.Put(indexManaged)

	// Fake FUSE-phase start (no real ratarmount).
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	eng.IsMount = func(string) bool { return false }

	newManaged, err := eng.CompleteIndexAndStartMount(rec.ArchiveID)
	if err != nil {
		t.Fatalf("CompleteIndexAndStartMount: %v", err)
	}
	if newManaged == nil {
		t.Fatal("expected FUSE-phase ManagedMount")
	}
	if newManaged.Phase != mounter.PhaseMount {
		t.Fatalf("phase=%s want mount", newManaged.Phase)
	}

	// Carried onto new live entry.
	gotSkips := newManaged.NestedSkips()
	if len(gotSkips) != 2 || gotSkips[0] != "/nested/from-index.7z" || gotSkips[1] != "/nested/also-index.7z" {
		t.Fatalf("carried skips=%v", gotSkips)
	}
	// Same entry in registry.
	live := eng.Live.Get(rec.ArchiveID)
	if live == nil || len(live.NestedSkips()) != 2 {
		t.Fatalf("live skips=%v", live)
	}

	// Persisted pure advisory on indexing→mounting transition.
	mid, err := store.GetArchive(rec.ArchiveID)
	if err != nil || mid == nil {
		t.Fatalf("get mid: %v", err)
	}
	if mid.Status != state.StatusMounting {
		t.Fatalf("status=%s", mid.Status)
	}
	if mid.LastError == nil {
		t.Fatal("expected last_error pure skip summary after CompleteIndexAndStartMount")
	}
	wantSum := mounter.FormatNestedSkipSummary(
		[]string{"/nested/from-index.7z", "/nested/also-index.7z"},
		mounter.DefaultNestedSkipSamples,
	)
	if *mid.LastError != wantSum {
		t.Fatalf("last_error=%q want %q", *mid.LastError, wantSum)
	}

	// MarkMounted still writes the summary (from live skips).
	mounted, err := eng.MarkMounted(rec.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if mounted.LastError == nil || *mounted.LastError != wantSum {
		t.Fatalf("after MarkMounted last_error=%v", mounted.LastError)
	}

	// Cleanup long-running sleep child.
	_, _ = eng.Unmount(rec.ArchiveID, false)
}

func TestDrainRatarmountStderr_FakeLines(t *testing.T) {
	t.Parallel()
	// Synthetic stderr (no FUSE / no real ratarmount).
	input := strings.Join([]string{
		"some noise line",
		"[Warning] automount: Mounting of '/nested/bad.7z' failed because of: corrupt data",
		"info: indexing…",
		"Mounting of '/other.zip' failed because of: unsupported format",
		"",
	}, "\n")

	var (
		mu    sync.Mutex
		paths []string
		rsns  []string
	)
	mounter.DrainRatarmountStderr(strings.NewReader(input), "aid-1", func(path, reason string) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, path)
		rsns = append(rsns, reason)
	}, false)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/nested/bad.7z" || paths[1] != "/other.zip" {
		t.Fatalf("paths=%v", paths)
	}
	if rsns[0] != "corrupt data" || rsns[1] != "unsupported format" {
		t.Fatalf("reasons=%v", rsns)
	}
}

func TestManagedMount_NoteNestedSkipConcurrent(t *testing.T) {
	t.Parallel()
	m := &mounter.ManagedMount{ArchiveID: "a"}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.NoteNestedSkip("/n.7z")
		}(i)
	}
	wg.Wait()
	if n := len(m.NestedSkips()); n != 50 {
		t.Fatalf("count=%d", n)
	}
}

func TestMarkFailed_EnrichesLastErrorWithNestedSkips(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	// Claim mounting so MarkFailed has a valid transition.
	rec, err := store.Transition(rec.ArchiveID, state.StatusMounting, state.StatusDiscovered, map[string]any{
		"mount_path": filepath.Join(cfg.MountRoot, "m"),
		"index_path": filepath.Join(cfg.IndexDir, "i.sqlite"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Live entry with pre-recorded nested skips (simulates drain without FUSE).
	managed := &mounter.ManagedMount{
		ArchiveID: rec.ArchiveID,
		Phase:     mounter.PhaseMount,
		Request: mounter.MountRequest{
			ArchivePath:  archive,
			IndexPath:    filepath.Join(cfg.IndexDir, "i.sqlite"),
			MountPath:    filepath.Join(cfg.MountRoot, "m"),
			MountBackend: "rust",
		},
		StartedAt: time.Now(),
	}
	managed.NoteNestedSkip("/inner/broken.7z")
	managed.NoteNestedSkip("/inner/also.7z")
	// No drain goroutine (stderrDone nil): WaitStderrDrain is a no-op.
	eng.Live.Put(managed)

	updated, err := eng.MarkFailed(rec.ArchiveID, "ratarmount exited before mount ready")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastError == nil {
		t.Fatal("expected last_error")
	}
	le := *updated.LastError
	if !strings.Contains(le, "ratarmount exited before mount ready") {
		t.Fatalf("missing base reason: %q", le)
	}
	if !strings.Contains(le, "skipped 2 nested mounts") {
		t.Fatalf("missing skip summary: %q", le)
	}
	if !strings.Contains(le, "/inner/broken.7z") {
		t.Fatalf("missing sample path: %q", le)
	}
	if eng.Live.Get(rec.ArchiveID) != nil {
		t.Fatal("live should be dropped")
	}
}

func TestBeginMount_StderrDrainCapturesNestedSkips(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	// Fake child that writes nested-failure lines to opts.Stderr then exits
	// without producing an index file → CheckChild = exited → MarkFailed.
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		// Shell: print two nested fails to stderr, then exit 1 (no index).
		script := `
echo "[Warning] Mounting of '/nested/a.7z' failed because of: corrupt" >&2
echo "noise" >&2
echo "Mounting of '/nested/b.7z' failed because of: no codec" >&2
exit 1
`
		cmd := exec.Command("sh", "-c", script)
		if opts.Env != nil {
			cmd.Env = opts.Env
		}
		if opts.Stdout != nil {
			cmd.Stdout = opts.Stdout
		}
		if opts.Stderr != nil {
			cmd.Stderr = opts.Stderr
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	eng.IsMount = func(string) bool { return false }

	first := true
	managed, err := eng.BeginMount(rec, &first)
	if err != nil {
		t.Fatal(err)
	}
	if managed == nil {
		t.Fatal("expected managed mount")
	}

	// Wait for child exit (stderr drain finishes after child closes pipe).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if eng.CheckChild(managed.ArchiveID) == mounter.ChildExited {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if eng.CheckChild(managed.ArchiveID) != mounter.ChildExited {
		t.Fatal("expected child exited")
	}
	// Allow drain goroutine to finish parsing lines into ManagedMount.
	managed.WaitStderrDrain(2 * time.Second)
	if n := len(managed.NestedSkips()); n != 2 {
		t.Fatalf("expected 2 nested skips after drain, got %d %v", n, managed.NestedSkips())
	}

	// ProgressLive → MarkFailed should enrich last_error from drained skips.
	eng.ProgressLive()

	fresh, err := store.GetArchive(rec.ArchiveID)
	if err != nil || fresh == nil {
		t.Fatalf("get: %v", err)
	}
	// Index-only first index uses index_failed.
	if fresh.Status != state.StatusIndexFailed {
		t.Fatalf("status=%s last=%v", fresh.Status, fresh.LastError)
	}
	if fresh.LastError == nil {
		t.Fatal("expected last_error")
	}
	le := *fresh.LastError
	if !strings.Contains(le, "skipped 2 nested mounts") {
		t.Fatalf("expected skip summary in last_error: %q", le)
	}
	if !strings.Contains(le, "/nested/a.7z") || !strings.Contains(le, "/nested/b.7z") {
		t.Fatalf("expected sample paths: %q", le)
	}
}
