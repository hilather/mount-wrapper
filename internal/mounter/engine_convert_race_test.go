package mounter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/state"
)

func convertRaceEngineConfig(t *testing.T) (*config.Config, *state.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	store, err := state.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	convertedDir := filepath.Join(tmp, "converted")
	cfg := &config.Config{
		MountRoot:                 filepath.Join(tmp, "mounts"),
		IndexDir:                  filepath.Join(tmp, "indexes"),
		OverlayDir:                filepath.Join(tmp, "overlays"),
		StateDB:                   filepath.Join(tmp, "state.db"),
		ArchiveconverterOutputDir: convertedDir,
		MaxConcurrentIndex:        2,
		MaxConcurrentMount:        2,
		MaxConcurrentConvert:      2,
		MaxMountAttempts:          3,
		MountBackend:              "rust",
		RatarmountBin:             "true",
		NameRegex:                 config.DefaultNameRegex,
	}
	for _, d := range []string{cfg.MountRoot, cfg.IndexDir, cfg.OverlayDir, convertedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, store, tmp
}

func insertNamedArchive(t *testing.T, store *state.Store, dir, basename string) *state.ArchiveRecord {
	t.Helper()
	path := filepath.Join(dir, basename)
	if err := os.WriteFile(path, []byte("archive-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       dir,
		ArchivePath:     path,
		ArchiveBasename: basename,
		SizeBytes:       st.Size(),
		MtimeNs:         st.ModTime().UnixNano(),
		Fingerprint:     "fp-race",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestBeginMount_SkipsStatusConvertingWithoutJob(t *testing.T) {
	cfg, store, tmp := convertRaceEngineConfig(t)
	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := insertNamedArchive(t, store, srcDir, "SUP-LAB3.7z")
	rec, err := store.Transition(rec.ArchiveID, state.StatusConverting, state.StatusDiscovered, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(cfg, store)
	first := true
	managed, err := eng.BeginMount(rec, &first)
	if err != nil {
		t.Fatal(err)
	}
	if managed != nil {
		t.Fatal("expected no mount while converting")
	}
	fresh, _ := store.GetArchive(rec.ArchiveID)
	if fresh.Status != state.StatusConverting {
		t.Fatalf("status=%s", fresh.Status)
	}
}

func TestPollConvert_RecoversMountFailedAndPreservesBasename(t *testing.T) {
	cfg, store, tmp := convertRaceEngineConfig(t)
	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	basename := "SUP-36264-LAB3--2026-08-05--17-17-04.7z"
	rec := insertNamedArchive(t, store, srcDir, basename)

	convertedPath := convert.ConvertedFilePath(cfg, rec.ArchiveID)
	if err := os.WriteFile(convertedPath, []byte(strings.Repeat("7z", 128)), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := store.Transition(rec.ArchiveID, state.StatusConverting, state.StatusDiscovered, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	uuidMount := filepath.Join(cfg.MountRoot, rec.ArchiveID+".7z")
	rec, err = store.Transition(rec.ArchiveID, state.StatusMountFailed, state.StatusConverting, map[string]any{
		"mount_path": uuidMount,
		"last_error": "simulated race",
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(cfg, store)
	eng.StartProcess = func(req MountRequest, opts CmdOptions, mustExist bool) (*exec.Cmd, error) {
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	job := &convertJob{
		archiveID:  rec.ArchiveID,
		sourcePath: rec.ArchivePath,
		sourceSize: 14,
		needsIndex: true,
		startedAt:  time.Now(),
	}
	job.markDone(convertedPath, "", nil)
	eng.mu.Lock()
	eng.convertJobs[rec.ArchiveID] = job
	eng.mu.Unlock()

	eng.PollConvert()

	fresh, _ := store.GetArchive(rec.ArchiveID)
	if fresh.ArchiveBasename != basename {
		t.Fatalf("basename=%q want %q", fresh.ArchiveBasename, basename)
	}
	if fresh.Status != state.StatusDiscovered && fresh.Status != state.StatusIndexing && fresh.Status != state.StatusMounting {
		t.Fatalf("status=%s want discovered/indexing/mounting", fresh.Status)
	}
	if fresh.MountPath != nil {
		wantMount := filepath.Join(cfg.MountRoot, basename)
		if *fresh.MountPath != wantMount {
			t.Fatalf("mount_path=%q want %q or nil", *fresh.MountPath, wantMount)
		}
	}
	if fresh.ArchivePath != convertedPath {
		t.Fatalf("archive_path=%q", fresh.ArchivePath)
	}
}

func TestArchiveBasenameAfterConvert_UUIDPath(t *testing.T) {
	cfg := &config.Config{ArchiveconverterOutputDir: "/tmp/converted"}
	rec := &state.ArchiveRecord{
		ArchiveID:       "6e77b95e-593e-42b0-a477-60c1dafa43ba",
		ArchiveBasename: "SUP-LAB3.7z",
	}
	got := archiveBasenameAfterConvert(cfg, rec, "/tmp/converted/6e77b95e-593e-42b0-a477-60c1dafa43ba.7z")
	if got != "SUP-LAB3.7z" {
		t.Fatalf("got %q", got)
	}
}
