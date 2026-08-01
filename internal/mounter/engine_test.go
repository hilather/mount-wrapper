package mounter_test

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

func testEngineConfig(t *testing.T) (*config.Config, *state.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	store, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := &config.Config{
		MountRoot:            filepath.Join(tmp, "mounts"),
		IndexDir:             filepath.Join(tmp, "indexes"),
		OverlayDir:           filepath.Join(tmp, "overlays"),
		StateDB:              filepath.Join(tmp, "state.db"),
		MaxConcurrentIndex:   2,
		MaxConcurrentMount:   0,
		MaxConcurrentConvert: 1,
		MaxMountAttempts:     3,
		MountBackend:         "rust",
		RatarmountBin:        "true", // unused when StartProcess injected
		NameRegex:            config.DefaultNameRegex,
		PollIntervalSeconds:  60,
		IndexSmallestFirst:   true,
	}
	for _, d := range []string{cfg.MountRoot, cfg.IndexDir, cfg.OverlayDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, store, tmp
}

func insertArchive(t *testing.T, store *state.Store, path string) *state.ArchiveRecord {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       filepath.Dir(path),
		ArchivePath:     path,
		ArchiveBasename: filepath.Base(path),
		SizeBytes:       st.Size(),
		MtimeNs:         st.ModTime().UnixNano(),
		Fingerprint:     "fp-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestBeginMount_FakeStartMarksIndexing(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("not-a-real-tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	// Inject sleep-forever style via `true` which exits immediately — we treat as process.
	// Better: long-running `sleep 60` so CheckChild stays running.
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		// Write a non-empty index for index_only phase so complete path can work.
		if req.IndexOnly {
			if err := os.WriteFile(req.IndexPath, []byte("sqlite-fake"), 0o644); err != nil {
				return nil, err
			}
		}
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	eng.IsMount = func(string) bool { return false }

	first := true
	managed, err := eng.BeginMount(rec, &first)
	if err != nil {
		t.Fatalf("BeginMount: %v", err)
	}
	if managed == nil {
		t.Fatal("expected managed mount")
	}
	if managed.Phase != mounter.PhaseIndexOnly {
		t.Fatalf("phase=%s", managed.Phase)
	}
	// Cleanup child
	_, _ = eng.Unmount(rec.ArchiveID, false)
}

func TestCheckChild_IndexCompleteAndMarkMounted(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "b.tar.gz")
	if err := os.WriteFile(archive, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	var mountPath string
	ismount := false
	eng.IsMount = func(p string) bool {
		return ismount && p == mountPath
	}
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		mountPath = req.MountPath
		if req.IndexOnly {
			if err := os.WriteFile(req.IndexPath, []byte("idx"), 0o644); err != nil {
				return nil, err
			}
			// Exit immediately after writing index.
			cmd := exec.Command("true")
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd, nil
		}
		// Mount phase: long-running; ismount flipped later.
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	first := true
	if _, err := eng.BeginMount(rec, &first); err != nil {
		t.Fatal(err)
	}

	// Wait for index process to exit.
	deadline := time.Now().Add(3 * time.Second)
	var stateStr string
	for time.Now().Before(deadline) {
		stateStr = eng.CheckChild(rec.ArchiveID)
		if stateStr == mounter.ChildIndexComplete || stateStr == mounter.ChildExited {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stateStr != mounter.ChildIndexComplete {
		t.Fatalf("want index_complete, got %s", stateStr)
	}

	if _, err := eng.CompleteIndexAndStartMount(rec.ArchiveID); err != nil {
		t.Fatalf("CompleteIndexAndStartMount: %v", err)
	}
	ismount = true
	if got := eng.CheckChild(rec.ArchiveID); got != mounter.ChildMounted {
		t.Fatalf("want mounted, got %s", got)
	}
	if _, err := eng.MarkMounted(rec.ArchiveID); err != nil {
		t.Fatal(err)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusMounted {
		t.Fatalf("status=%s", rec2.Status)
	}
	// Clear ismount so UnmountSequence does not poll forever in tests.
	ismount = false
	_, _ = eng.Unmount(rec.ArchiveID, false)
}

func TestMarkFailedIncrementsAttempts(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	archive := filepath.Join(tmp, "c.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)
	eng := mounter.NewEngine(cfg, store)
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		_ = mounter.PreparePaths(req)
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	eng.IsMount = func(string) bool { return false }
	first := true
	if _, err := eng.BeginMount(rec, &first); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.MarkFailed(rec.ArchiveID, "test fail"); err != nil {
		t.Fatal(err)
	}
	rec2, _ := store.GetArchive(rec.ArchiveID)
	if rec2.Status != state.StatusIndexFailed {
		t.Fatalf("status=%s", rec2.Status)
	}
	if rec2.MountAttempts != 1 {
		t.Fatalf("attempts=%d", rec2.MountAttempts)
	}
	if !rec2.MountRetryable {
		t.Fatal("expected retryable")
	}
}

func zipWithEmbedded(t *testing.T, dest, member string, payload []byte) string {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	wr, err := w.Create(member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestEngine_ZipRepackConvertThenMount(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	cfg.Convert7zNonsolid = true
	cfg.Convert7zScope = config.Convert7zScopeFlatten
	cfg.ConvertZipTo7z = true
	cfg.Convert7zOverheadBytes = 0
	cfg.MinFreeBytes = 0
	cfg.Convert7zBin = "7z"

	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithEmbedded(t, filepath.Join(tmp, "nested.zip"), "bundle.tgz", []byte("payload"))
	rec := insertArchive(t, store, zipPath)

	eng := mounter.NewEngine(cfg, store)
	eng.Run7z = func(bin string, args []string, cwd string) error {
		if len(args) > 0 && args[0] == "x" {
			var out string
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					out = strings.TrimPrefix(a, "-o")
				}
			}
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(out, "bundle.tgz"), []byte("payload"), 0o644)
		}
		if len(args) > 0 && args[0] == "a" {
			var partial string
			for _, a := range args {
				if strings.HasSuffix(a, ".partial") {
					partial = a
				}
			}
			return os.WriteFile(partial, []byte(strings.Repeat("7z", 600)), 0o644)
		}
		return nil
	}
	eng.IsMount = func(string) bool { return false }
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		if err := mounter.PreparePaths(req); err != nil {
			return nil, err
		}
		if req.IndexOnly {
			if err := os.WriteFile(req.IndexPath, []byte("idx"), 0o644); err != nil {
				return nil, err
			}
		}
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	first := true
	managed, err := eng.BeginMount(rec, &first)
	if err != nil {
		t.Fatalf("BeginMount: %v", err)
	}
	if managed != nil {
		t.Fatal("expected convert path (nil managed)")
	}
	if eng.ConvertJobCount() != 1 {
		t.Fatalf("convert jobs=%d", eng.ConvertJobCount())
	}

	// Wait for async convert worker.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if eng.ConvertJobCount() == 1 {
			// still registered until PollConvert
			// peek store status
			fresh, _ := store.GetArchive(rec.ArchiveID)
			if fresh != nil && fresh.Status == state.StatusConverting {
				// job may already be done internally
			}
		}
		// PollConvert finishes done jobs
		eng.PollConvert()
		fresh, _ := store.GetArchive(rec.ArchiveID)
		if fresh != nil && fresh.Status != state.StatusConverting {
			rec = fresh
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !strings.HasSuffix(strings.ToLower(rec.ArchivePath), ".7z") {
		t.Fatalf("archive_path after convert=%q status=%s last=%v", rec.ArchivePath, rec.Status, rec.LastError)
	}
	if convert.ReadConvertMetadata(rec.ArchivePath) == nil {
		t.Fatal("expected convert metadata on .7z")
	}
	// Should have started mount/index after PollConvert.
	if rec.Status != state.StatusIndexing && rec.Status != state.StatusMounting {
		// PollConvert calls BeginMount; if indexing, ok.
		if eng.Live.Get(rec.ArchiveID) == nil && rec.Status == state.StatusDiscovered {
			// race: convert done but mount not claimed — retry once
			eng.PollConvert()
			rec, _ = store.GetArchive(rec.ArchiveID)
		}
	}
	if eng.Live.Get(rec.ArchiveID) == nil && rec.Status != state.StatusIndexing && rec.Status != state.StatusMounting {
		t.Fatalf("expected live mount after convert, status=%s err=%v", rec.Status, rec.LastError)
	}
	_, _ = eng.Unmount(rec.ArchiveID, false)
}

func TestEngine_FlattenConvertSuccess(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	cfg.Convert7zNonsolid = true
	cfg.Convert7zScope = config.Convert7zScopeFlatten
	cfg.Convert7zOverheadBytes = 0
	cfg.MinFreeBytes = 0
	cfg.ArchiveconverterEnabled = false

	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	archive := filepath.Join(tmp, "solid.7z")
	if err := os.WriteFile(archive, []byte(strings.Repeat("S", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := insertArchive(t, store, archive)

	eng := mounter.NewEngine(cfg, store)
	eng.NeedsFlatten = func(string) bool { return true }
	eng.Run7z = func(bin string, args []string, cwd string) error {
		if len(args) > 0 && args[0] == "x" {
			var out string
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					out = strings.TrimPrefix(a, "-o")
				}
			}
			return os.MkdirAll(out, 0o755)
		}
		if len(args) > 0 && args[0] == "a" {
			for _, a := range args {
				if strings.Contains(a, ".partial") {
					return os.WriteFile(a, []byte(strings.Repeat("F", 600)), 0o644)
				}
			}
		}
		return nil
	}
	eng.IsMount = func(string) bool { return false }
	eng.StartProcess = func(req mounter.MountRequest, opts mounter.CmdOptions, mustExist bool) (*exec.Cmd, error) {
		_ = mounter.PreparePaths(req)
		if req.IndexOnly {
			_ = os.WriteFile(req.IndexPath, []byte("idx"), 0o644)
		}
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}

	first := true
	if _, err := eng.BeginMount(rec, &first); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		eng.PollConvert()
		fresh, _ := store.GetArchive(rec.ArchiveID)
		if fresh != nil && fresh.Status != state.StatusConverting {
			rec = fresh
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.LastError != nil && *rec.LastError != "" {
		t.Fatalf("convert failed: %s status=%s", *rec.LastError, rec.Status)
	}
	if convert.ReadConvertMetadata(archive) == nil {
		t.Fatal("expected flatten metadata")
	}
	st, err := os.Stat(archive)
	if err != nil || st.Size() != 600 {
		t.Fatalf("archive size=%v err=%v", st, err)
	}
	_, _ = eng.Unmount(rec.ArchiveID, false)
}

func TestEngine_ZipRepackConvertFailure(t *testing.T) {
	cfg, store, tmp := testEngineConfig(t)
	cfg.Convert7zNonsolid = true
	cfg.Convert7zScope = config.Convert7zScopeFlatten
	cfg.ConvertZipTo7z = true
	cfg.Convert7zOverheadBytes = 0
	cfg.MinFreeBytes = 0

	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithEmbedded(t, filepath.Join(tmp, "bad.zip"), "x.tgz", []byte("p"))
	rec := insertArchive(t, store, zipPath)

	eng := mounter.NewEngine(cfg, store)
	eng.Run7z = func(string, []string, string) error {
		return &convert.Error{Op: "run_7z", Msg: "7z failed: boom"}
	}

	first := true
	if _, err := eng.BeginMount(rec, &first); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		eng.PollConvert()
		fresh, _ := store.GetArchive(rec.ArchiveID)
		if fresh != nil && fresh.Status != state.StatusConverting {
			rec = fresh
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.Status != state.StatusIndexFailed {
		t.Fatalf("status=%s last=%v", rec.Status, rec.LastError)
	}
	if rec.LastError == nil || *rec.LastError == "" {
		t.Fatal("expected last_error")
	}
}

