package scanner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/paths"
	"github.com/hilather/mount-wrapper/internal/scanner"
	"github.com/hilather/mount-wrapper/internal/state"
)

func cfg(source string, overrides map[string]any) *config.Config {
	raw := map[string]any{
		"source_dirs":           []any{source},
		"content_fingerprint":   false,
		"stable_file_mode":      "two_scans",
		"min_file_age_seconds":  30,
		"use_inotify":           false,
		"poll_interval_seconds": 60,
	}
	for k, v := range overrides {
		raw[k] = v
	}
	c, err := config.FromMap(raw, "")
	if err != nil {
		panic(err)
	}
	return c
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func openScanner(t *testing.T, source string, overrides map[string]any) (*scanner.Scanner, *state.Store) {
	t.Helper()
	store, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c := cfg(source, overrides)
	// Disable wslpath; absolute Linux paths only.
	s, err := scanner.New(c, store, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.PathOpts = &paths.ToWSLOpts{} // no wslpath
	_ = s.ReloadSources()
	return s, store
}

func TestStableFileGateTwoScans(t *testing.T) {
	gate, err := scanner.NewStableFileGate("two_scans", 30)
	if err != nil {
		t.Fatal(err)
	}
	now := 1_000_000.0
	if gate.Check("p", 10, 100, now, false) {
		t.Fatal("first check should be unstable")
	}
	if !gate.Check("p", 10, 100, now+1, false) {
		t.Fatal("second identical should be stable")
	}
	if !gate.Peek("p", nil, nil, nil) {
		t.Fatal("peek should be stable")
	}
}

func TestStableFileGateResetsOnChange(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("two_scans", 30)
	now := 1_000_000.0
	gate.Check("p", 10, 100, now, false)
	if gate.Check("p", 11, 100, now+1, false) {
		t.Fatal("size change should reset")
	}
	if !gate.Check("p", 11, 100, now+2, false) {
		t.Fatal("second after change should stabilize")
	}
}

func TestStableFileGateAssumeStable(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("two_scans", 30)
	if !gate.Check("p", 1, 1, 0, true) {
		t.Fatal("assume_stable")
	}
	if !gate.Peek("p", nil, nil, nil) {
		t.Fatal("peek after assume")
	}
}

func TestStableFileGateMinAge(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("min_age", 30)
	now := 1_000_000.0
	mtimeNs := int64((now - 100) * 1e9)
	if gate.Check("p", 5, mtimeNs, now, false) {
		t.Fatal("first min_age should be false (no prior size)")
	}
	if !gate.Check("p", 5, mtimeNs, now+1, false) {
		t.Fatal("second min_age with age should pass")
	}
}

func TestStableFileGateMinAgeTooNew(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("min_age", 30)
	now := 1_000_000.0
	mtimeNs := int64((now - 5) * 1e9)
	gate.Check("p", 5, mtimeNs, now, false)
	if gate.Check("p", 5, mtimeNs, now+1, false) {
		t.Fatal("young file should not pass min_age")
	}
}

func TestStableFileGateBoth(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("both", 30)
	now := 1_000_000.0
	mtimeNs := int64((now - 100) * 1e9)
	if gate.Check("p", 1, mtimeNs, now, false) {
		t.Fatal("first both false")
	}
	if !gate.Check("p", 1, mtimeNs, now+1, false) {
		t.Fatal("second both true")
	}
}

func TestStableFileGateForgetMissing(t *testing.T) {
	gate, _ := scanner.NewStableFileGate("two_scans", 30)
	gate.Check("a", 1, 1, 0, false)
	gate.Check("b", 1, 1, 0, false)
	gate.ForgetMissing([]string{"a"})
	if gate.Peek("b", nil, nil, nil) {
		// b forgotten; peek false because prior removed
	}
	// a still present with consecutive_same=1 → peek false for two_scans
	if gate.Peek("a", nil, nil, nil) {
		t.Fatal("a only seen once")
	}
}

func TestFingerprintBasic(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, filepath.Join(dir, "a.tar"), []byte("abc"))
	st, _ := os.Stat(f)
	fp, err := scanner.ComputeFingerprint(f, st.Size(), st.ModTime().UnixNano(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(fp, ":")
	if len(want) != 2 {
		t.Fatalf("fp=%q", fp)
	}
}

func TestFingerprintContent(t *testing.T) {
	dir := t.TempDir()
	data := strings.Repeat("abc", 100)
	f := writeFile(t, filepath.Join(dir, "a.tar"), []byte(data))
	st, _ := os.Stat(f)
	fp, err := scanner.ComputeFingerprint(f, st.Size(), st.ModTime().UnixNano(), true)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(fp, ":")
	if len(parts) != 3 {
		t.Fatalf("fp=%q", fp)
	}
	h, err := scanner.PartialContentHash(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if parts[2] != h {
		t.Fatalf("hash %q != %q", parts[2], h)
	}
	// change content
	if err := os.WriteFile(f, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := scanner.PartialContentHash(f, 4)
	if err != nil {
		t.Fatal(err)
	}
	if h == h2 {
		t.Fatal("hash should change")
	}
}

func TestListSourceFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.tar.gz"), []byte("x"))
	writeFile(t, filepath.Join(dir, "sub", "b.zip"), []byte("y"))
	files, err := scanner.ListSourceFiles(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "a.tar.gz" {
		t.Fatalf("non-recursive=%v", files)
	}
	files, err = scanner.ListSourceFiles(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("recursive=%v", files)
	}
}

func TestScannerInsertNotStableFirst(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("x"))
	sc, store := openScanner(t, src, nil)
	r1, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.InsertedIDs) != 1 {
		t.Fatalf("inserted=%v", r1.InsertedIDs)
	}
	if len(r1.StableArchiveIDs) != 0 {
		t.Fatalf("stable=%v", r1.StableArchiveIDs)
	}
	rec, _ := store.GetArchive(r1.InsertedIDs[0])
	if rec == nil || rec.Status != state.StatusDiscovered || rec.ArchiveBasename != "data.tar.gz" {
		t.Fatalf("rec=%+v", rec)
	}
}

func TestScannerStableOnSecondScan(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("x"))
	sc, _ := openScanner(t, src, nil)
	if _, err := sc.Scan(false); err != nil {
		t.Fatal(err)
	}
	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.InsertedIDs) != 0 || len(r2.TouchedIDs) != 1 || len(r2.StableArchiveIDs) != 1 {
		t.Fatalf("r2 inserted=%v touched=%v stable=%v", r2.InsertedIDs, r2.TouchedIDs, r2.StableArchiveIDs)
	}
}

func TestScannerAssumeStable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "data.zip"), []byte("z"))
	sc, _ := openScanner(t, src, nil)
	r, err := sc.Scan(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.InsertedIDs) != 1 || len(r.StableArchiveIDs) != 1 || !r.AssumeStable {
		t.Fatalf("%+v", r)
	}
}

func TestScannerIgnoresNonMatching(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "readme.txt"), []byte("x"))
	writeFile(t, filepath.Join(src, "data.rar"), []byte("x"))
	writeFile(t, filepath.Join(src, "ok.tar"), []byte("x"))
	sc, _ := openScanner(t, src, nil)
	r, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Observations) != 1 || r.Observations[0].Basename != "ok.tar" {
		t.Fatalf("obs=%+v", r.Observations)
	}
}

func TestScannerMarkAbsent(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	f := writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("x"))
	sc, store := openScanner(t, src, nil)
	r1, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	aid := r1.InsertedIDs[0]
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r2.AbsentIDs {
		if id == aid {
			found = true
		}
	}
	if !found {
		t.Fatalf("absent=%v", r2.AbsentIDs)
	}
	rec, _ := store.GetArchive(aid)
	if rec == nil || rec.Status != state.StatusAbsent || rec.RemovedAt == nil {
		t.Fatalf("rec=%+v", rec)
	}
}

func TestScannerReappear(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	f := writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("x"))
	sc, store := openScanner(t, src, nil)
	r1, _ := sc.Scan(false)
	aid := r1.InsertedIDs[0]
	_ = os.Remove(f)
	_, _ = sc.Scan(false)
	writeFile(t, f, []byte("x"))
	r3, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r3.ReappearedIDs {
		if id == aid {
			found = true
		}
	}
	if !found {
		t.Fatalf("reappeared=%v", r3.ReappearedIDs)
	}
	rec, _ := store.GetArchive(aid)
	if rec == nil || rec.Status != state.StatusDiscovered {
		t.Fatalf("rec=%+v", rec)
	}
}

func TestScannerContentChange(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	f := writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("AAAA"))
	sc, store := openScanner(t, src, map[string]any{"content_fingerprint": false})
	r1, _ := sc.Scan(false)
	aid := r1.InsertedIDs[0]
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(f, []byte("BBBBBBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	// touch mtime
	now := time.Now()
	_ = os.Chtimes(f, now, now)
	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r2.ContentChangedIDs {
		if id == aid {
			found = true
		}
	}
	if !found {
		t.Fatalf("content_changed=%v", r2.ContentChangedIDs)
	}
	rec, _ := store.GetArchive(aid)
	if rec == nil || rec.Status != state.StatusDiscovered {
		t.Fatalf("rec=%+v", rec)
	}
}

func TestScannerContentFingerprintSameSize(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	f := writeFile(t, filepath.Join(src, "data.tar.gz"), []byte("AAAA"))
	sc, store := openScanner(t, src, map[string]any{"content_fingerprint": true})
	r1, _ := sc.Scan(false)
	aid := r1.InsertedIDs[0]
	rec1, _ := store.GetArchive(aid)
	st, _ := os.Stat(f)
	if err := os.WriteFile(f, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	// restore same mtime
	_ = os.Chtimes(f, st.ModTime(), st.ModTime())
	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range r2.ContentChangedIDs {
		if id == aid {
			found = true
		}
	}
	if !found {
		t.Fatalf("content_changed=%v errs=%v", r2.ContentChangedIDs, r2.Errors)
	}
	rec2, _ := store.GetArchive(aid)
	if rec2 == nil || rec2.Fingerprint == rec1.Fingerprint {
		t.Fatalf("fp1=%q fp2=%v", rec1.Fingerprint, rec2)
	}
}

func TestScannerMaxArchiveBytes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, filepath.Join(src, "big.tar"), make([]byte, 100))
	sc, _ := openScanner(t, src, map[string]any{"max_archive_bytes": 50})
	r, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.InsertedIDs) != 0 {
		t.Fatalf("inserted=%v", r.InsertedIDs)
	}
	if len(r.SkippedFiles) == 0 {
		t.Fatal("expected skipped")
	}
}

func TestScannerMissingSource(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	sc, _ := openScanner(t, missing, nil)
	r, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SkippedSources) == 0 || len(r.InsertedIDs) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestScannerGrowingFileNotStable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	f := writeFile(t, filepath.Join(src, "download.tar.gz"), []byte("x"))
	sc, _ := openScanner(t, src, nil)
	_, _ = sc.Scan(false)
	if err := os.WriteFile(f, []byte("xy"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, _ := sc.Scan(false)
	if len(r2.StableArchiveIDs) != 0 {
		t.Fatalf("stable after grow=%v", r2.StableArchiveIDs)
	}
	r3, _ := sc.Scan(false)
	if len(r3.StableArchiveIDs) != 1 {
		t.Fatalf("stable third=%v", r3.StableArchiveIDs)
	}
}

// TestScannerRenameGetsNewArchiveID: a file renamed to a new path is a new
// archive_id; the old path becomes absent (path identity, not content identity).
func TestScannerRenameGetsNewArchiveID(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	oldPath := writeFile(t, filepath.Join(src, "old.tar.gz"), []byte("payload"))
	sc, store := openScanner(t, src, nil)
	r1, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.InsertedIDs) != 1 {
		t.Fatalf("inserted=%v", r1.InsertedIDs)
	}
	oldID := r1.InsertedIDs[0]

	newPath := filepath.Join(src, "renamed.tar.gz")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.InsertedIDs) != 1 {
		t.Fatalf("expected one insert for new path, got %v (absent=%v)", r2.InsertedIDs, r2.AbsentIDs)
	}
	newID := r2.InsertedIDs[0]
	if newID == oldID {
		t.Fatal("rename must allocate a new archive_id")
	}
	foundAbsent := false
	for _, id := range r2.AbsentIDs {
		if id == oldID {
			foundAbsent = true
		}
	}
	if !foundAbsent {
		t.Fatalf("old path should be absent; absent=%v", r2.AbsentIDs)
	}
	oldRec, _ := store.GetArchive(oldID)
	if oldRec == nil || oldRec.Status != state.StatusAbsent {
		t.Fatalf("old rec=%+v", oldRec)
	}
	newRec, _ := store.GetArchive(newID)
	if newRec == nil || newRec.Status != state.StatusDiscovered || filepath.Base(newRec.ArchivePath) != "renamed.tar.gz" {
		t.Fatalf("new rec=%+v", newRec)
	}
}

// TestScannerPurgeThenRediscoverNewArchiveID: after purge frees the path,
// rediscovery at the same path allocates a new archive_id (not Reappear).
// Complements state.TestPurgeFreesPath with the full scanner path.
func TestScannerPurgeThenRediscoverNewArchiveID(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	_ = writeFile(t, filepath.Join(src, "same.tar.gz"), []byte("payload"))
	sc, store := openScanner(t, src, nil)
	r1, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.InsertedIDs) != 1 {
		t.Fatalf("inserted=%v", r1.InsertedIDs)
	}
	oldID := r1.InsertedIDs[0]
	if err := store.PurgeArchive(oldID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetArchive(oldID); got != nil {
		t.Fatalf("purged row still present: %+v", got)
	}

	r2, err := sc.Scan(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.InsertedIDs) != 1 {
		t.Fatalf("expected re-insert after purge, got inserted=%v reappeared=%v", r2.InsertedIDs, r2.ReappearedIDs)
	}
	if len(r2.ReappearedIDs) != 0 {
		t.Fatalf("purge must not reappear same id; reappeared=%v", r2.ReappearedIDs)
	}
	newID := r2.InsertedIDs[0]
	if newID == oldID {
		t.Fatal("rediscovery after purge must allocate a new archive_id")
	}
	newRec, _ := store.GetArchive(newID)
	if newRec == nil || newRec.Status != state.StatusDiscovered {
		t.Fatalf("new rec=%+v", newRec)
	}
}

func TestScannerArchivesDirStableWithoutScan(t *testing.T) {
	tmp := t.TempDir()
	archivesDir := filepath.Join(tmp, "archives")
	archived := writeFile(t, filepath.Join(archivesDir, "moved.7z"), []byte("7z"))
	src := filepath.Join(tmp, "src")
	_ = os.MkdirAll(src, 0o755)
	sc, _ := openScanner(t, src, map[string]any{"archives_dir": archivesDir})
	if !sc.IsStable(archived) {
		t.Fatal("archives path should be stable if file exists")
	}
	if sc.IsStable(filepath.Join(tmp, "never-seen.7z")) {
		t.Fatal("unseen non-archives should be false")
	}
}

func TestWatchableSourcesSkipsDrvFs(t *testing.T) {
	// Use real existing dir + fake drvfs path
	tmp := t.TempDir()
	pathsList := scanner.WatchableSources([][2]string{
		{"D:\\x", "/mnt/d/x"},
		{"inbox", tmp},
	})
	for _, p := range pathsList {
		if strings.HasPrefix(p, "/mnt/d") {
			t.Fatalf("drvfs leaked: %v", pathsList)
		}
	}
	if len(pathsList) != 1 {
		t.Fatalf("watchable=%v", pathsList)
	}
}

func TestInotifyStartClose(t *testing.T) {
	tmp := t.TempDir()
	w := scanner.NewInotifyWatcher()
	watched := w.Start([]string{tmp})
	if len(watched) > 0 {
		if !w.Active() {
			t.Fatal("expected active")
		}
		writeFile(t, filepath.Join(tmp, "x.tar"), []byte("1"))
		_ = w.Poll(100)
	}
	w.Close()
	if w.Active() {
		t.Fatal("expected inactive after close")
	}
}
