package status_test

import (
	"math"
	"testing"

	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/state"
	"github.com/hilather/mount-wrapper/internal/status"
)

func almostEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func strPtr(s string) *string { return &s }
func int64Ptr(v int64) *int64 { return &v }

func TestElapsedSeconds(t *testing.T) {
	// 2026-01-01T12:00:00Z − 2026-01-01T11:53:00Z = 420s
	now := float64(1767273600) // use ParseISO for clarity
	startedEpoch := status.ParseISOToEpoch("2026-01-01T12:00:00Z")
	if startedEpoch == nil {
		t.Fatal("parse now")
	}
	now = *startedEpoch
	elapsed := status.ElapsedSeconds("2026-01-01T11:53:00Z", now, nil)
	if elapsed == nil || !almostEqual(*elapsed, 420.0, 0.01) {
		t.Fatalf("elapsed=%v want ~420", elapsed)
	}
	if status.ElapsedSeconds("not-a-date", now, nil) != nil {
		t.Fatal("expected nil for bad iso")
	}
	// Negative clamp
	neg := status.ElapsedSeconds("2026-01-01T13:00:00Z", now, nil)
	if neg == nil || *neg != 0 {
		t.Fatalf("negative clamp: %v", neg)
	}
}

func TestSourceFSLabel(t *testing.T) {
	if got := status.SourceFSLabel("/mnt/d/Archives/a.tar"); got != "drvfs" {
		t.Fatalf("drvfs: %s", got)
	}
	if got := status.SourceFSLabel("/var/lib/mount-wrapper/inbox/a.tar"); got != "linux" {
		t.Fatalf("linux: %s", got)
	}
	if got := status.SourceFSLabel(""); got != "unknown" {
		t.Fatalf("empty: %s", got)
	}
}

func TestShouldLogIndexProgress(t *testing.T) {
	if !status.ShouldLogIndexProgress(nil, 100, 60) {
		t.Fatal("nil last → true")
	}
	last := 50.0
	if status.ShouldLogIndexProgress(&last, 100, 60) {
		t.Fatal("50s ago with 60s interval → false")
	}
	last = 40.0
	if !status.ShouldLogIndexProgress(&last, 100, 60) {
		t.Fatal("60s elapsed → true")
	}
}

func TestBuildIndexingArchivesAndErrors(t *testing.T) {
	store, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	a, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       "/src",
		ArchivePath:     "/src/big.tar.gz",
		ArchiveBasename: "big.tar.gz",
		SizeBytes:       1000,
		MtimeNs:         1,
		Fingerprint:     "1000:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	mp := "/mounts/big"
	pid := int64(1234)
	started := "2026-01-01T00:00:00Z"
	if _, err := store.Transition(a.ArchiveID, state.StatusIndexing, "discovered", map[string]any{
		"index_started_at": started,
		"mount_pid":        pid,
		"mount_path":       mp,
	}, ""); err != nil {
		t.Fatal(err)
	}

	b, err := store.InsertDiscovered(state.InsertDiscoveredParams{
		SourceDir:       "/src",
		ArchivePath:     "/src/bad.zip",
		ArchiveBasename: "bad.zip",
		SizeBytes:       10,
		MtimeNs:         2,
		Fingerprint:     "10:2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(b.ArchiveID, state.StatusIndexing, "discovered", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(b.ArchiveID, state.StatusIndexFailed, "indexing", map[string]any{
		"mount_attempts":  10,
		"mount_retryable": false,
		"last_error":      "boom",
	}, ""); err != nil {
		t.Fatal(err)
	}

	recs, err := store.ListArchives(nil)
	if err != nil {
		t.Fatal(err)
	}
	inputs := status.FromStateRecords(recs)

	nowEpoch := status.ParseISOToEpoch("2026-01-01T00:07:00Z")
	if nowEpoch == nil {
		t.Fatal("parse now")
	}
	payload := status.Build(status.Options{
		Version:          "test",
		PID:              1,
		ConfigPath:       "/tmp/config.yaml",
		OverlayDir:       "/tmp/overlays",
		IndexDir:         "/tmp/indexes",
		MinFreeBytes:     1,
		MaxMountAttempts: 10,
		Archives:         inputs,
		LastScan:         map[string]any{"duration_ms": 42.5, "seen": 2},
		LastScanAt:       "2026-01-01T00:06:00Z",
		LowDisk:          false,
		Now:              *nowEpoch,
		PIDAlive:         func(pid int) bool { return pid == 1234 },
		GeneratedAt:      "2026-01-01T00:07:00Z",
	})

	if payload.Indexing != 1 {
		t.Fatalf("indexing=%d", payload.Indexing)
	}
	if payload.IndexFailed != 1 {
		t.Fatalf("index_failed=%d", payload.IndexFailed)
	}
	if payload.LastScanDurationMs == nil || *payload.LastScanDurationMs != 42.5 {
		t.Fatalf("duration_ms=%v", payload.LastScanDurationMs)
	}
	if payload.LastScanAt != "2026-01-01T00:06:00Z" {
		t.Fatalf("last_scan_at=%s", payload.LastScanAt)
	}
	if len(payload.IndexingArchives) != 1 {
		t.Fatalf("indexing_archives len=%d", len(payload.IndexingArchives))
	}
	job := payload.IndexingArchives[0]
	if job.Basename != "big.tar.gz" {
		t.Fatalf("basename=%s", job.Basename)
	}
	if job.ElapsedS == nil || !almostEqual(*job.ElapsedS, 420.0, 0.1) {
		t.Fatalf("elapsed_s=%v", job.ElapsedS)
	}
	if job.SourceFS != "linux" {
		t.Fatalf("source_fs=%s", job.SourceFS)
	}
	if job.ProgressLabel != "indexing" {
		t.Fatalf("progress_label=%s", job.ProgressLabel)
	}
	if !job.PIDAlive {
		t.Fatal("pid_alive expected true")
	}

	foundStuck := false
	for _, e := range payload.ErrorsRecent {
		if e.Basename == "bad.zip" && e.Stuck {
			foundStuck = true
		}
	}
	if !foundStuck {
		t.Fatalf("errors_recent: %+v", payload.ErrorsRecent)
	}

	var big *status.ArchiveDict
	for i := range payload.Archives {
		if payload.Archives[i].ArchiveBasename == "big.tar.gz" {
			big = &payload.Archives[i]
			break
		}
	}
	if big == nil {
		t.Fatal("big archive missing")
	}
	if big.ElapsedS == nil || !almostEqual(*big.ElapsedS, 420.0, 0.1) {
		t.Fatalf("big elapsed=%v", big.ElapsedS)
	}
	if big.Status != "indexing" {
		t.Fatalf("status=%s", big.Status)
	}
	if big.ProgressLabel != "indexing" {
		t.Fatalf("progress=%s", big.ProgressLabel)
	}
	if big.SourceFS != "linux" {
		t.Fatalf("fs=%s", big.SourceFS)
	}
}

func TestProgressLabelsConvertingMountingLive(t *testing.T) {
	now := *status.ParseISOToEpoch("2026-01-01T00:10:00Z")
	started := "2026-01-01T00:00:00Z"
	conv := &status.ArchiveInput{
		ArchiveID:       "c1",
		ArchivePath:     "/src/a.7z",
		ArchiveBasename: "a.7z",
		Status:          "converting",
		IndexStartedAt:  &started,
		HooksStatus:     "none",
	}
	idx := &status.ArchiveInput{
		ArchiveID:       "i1",
		ArchivePath:     "/mnt/d/Archives/nested.tar",
		ArchiveBasename: "nested.tar",
		Status:          "indexing",
		IndexStartedAt:  &started,
		HooksStatus:     "none",
		MountPID:        int64Ptr(99),
	}
	mnt := &status.ArchiveInput{
		ArchiveID:       "m1",
		ArchivePath:     "/src/b.tar",
		ArchiveBasename: "b.tar",
		Status:          "mounting",
		IndexStartedAt:  &started,
		HooksStatus:     "none",
	}
	hooks := &status.ArchiveInput{
		ArchiveID:       "h1",
		ArchivePath:     "/src/c.tar",
		ArchiveBasename: "c.tar",
		Status:          "hooks_running",
		HooksStatus:     "running",
	}

	live := map[string]status.LiveMount{
		"i1": {PID: 100, Phase: "index_only", IsFirstIndex: true},
		"m1": {PID: 101, Phase: "mount", IsFirstIndex: false},
	}
	payload := status.Build(status.Options{
		Version:          "0.1.0",
		PID:              42,
		MaxMountAttempts: 10,
		Archives:         []*status.ArchiveInput{conv, idx, mnt, hooks},
		Live:             live,
		Now:              now,
		PIDAlive:         func(pid int) bool { return pid > 0 },
		GeneratedAt:      "2026-01-01T00:10:00Z",
	})

	byBase := map[string]status.ArchiveDict{}
	for _, a := range payload.Archives {
		byBase[a.ArchiveBasename] = a
	}
	if byBase["a.7z"].ProgressLabel != "converting to non-solid" {
		t.Fatalf("converting label=%q", byBase["a.7z"].ProgressLabel)
	}
	if byBase["a.7z"].ElapsedS == nil || !almostEqual(*byBase["a.7z"].ElapsedS, 600.0, 0.1) {
		t.Fatalf("converting elapsed=%v", byBase["a.7z"].ElapsedS)
	}
	if byBase["nested.tar"].ProgressLabel != "building index" {
		t.Fatalf("indexing live label=%q", byBase["nested.tar"].ProgressLabel)
	}
	if byBase["nested.tar"].SourceFS != "drvfs" {
		t.Fatalf("drvfs: %s", byBase["nested.tar"].SourceFS)
	}
	if byBase["nested.tar"].MountPhase != "index_only" {
		t.Fatalf("phase=%s", byBase["nested.tar"].MountPhase)
	}
	if byBase["b.tar"].ProgressLabel != "mounting FUSE" {
		t.Fatalf("mounting live label=%q", byBase["b.tar"].ProgressLabel)
	}
	if byBase["c.tar"].ProgressLabel != "hooks" {
		t.Fatalf("hooks label=%q", byBase["c.tar"].ProgressLabel)
	}

	// indexing_archives: 3 in-progress (not hooks_running)
	if len(payload.IndexingArchives) != 3 {
		t.Fatalf("indexing_archives=%d", len(payload.IndexingArchives))
	}
	// Longest first — all same elapsed, order stable by insertion among ties.
	if payload.Counts["converting"] != 1 || payload.Counts["indexing"] != 1 || payload.Counts["mounting"] != 1 {
		t.Fatalf("counts=%v", payload.Counts)
	}
	if payload.Counts["hooks_running"] != 1 {
		t.Fatalf("hooks_running count=%d", payload.Counts["hooks_running"])
	}
}

func TestArchiveToDict_NestedSkipsFromInputAndLive(t *testing.T) {
	// Mounted row with skip fields pre-filled (service last_error / live path).
	n := 2
	sum := "skipped 2 nested mounts: /a.7z, /b.7z"
	mounted := &status.ArchiveInput{
		ArchiveID:          "m1",
		ArchivePath:        "/src/outer.tar",
		ArchiveBasename:    "outer.tar",
		Status:             "mounted",
		HooksStatus:        "success",
		NestedSkipsCount:   &n,
		NestedSkipsSummary: sum,
	}
	// Live wins over empty input for an in-progress/mounting row.
	mounting := &status.ArchiveInput{
		ArchiveID:       "m2",
		ArchivePath:     "/src/other.tar",
		ArchiveBasename: "other.tar",
		Status:          "mounting",
		HooksStatus:     "none",
	}
	live := map[string]status.LiveMount{
		"m2": {
			PID:                55,
			Phase:              "mount",
			NestedSkipsCount:   1,
			NestedSkipsSummary: "skipped 1 nested mount: /nested/bad.7z",
		},
	}
	payload := status.Build(status.Options{
		Version:  "test",
		PID:      1,
		Archives: []*status.ArchiveInput{mounted, mounting},
		Live:     live,
		Now:      1,
	})
	byID := map[string]status.ArchiveDict{}
	for _, a := range payload.Archives {
		byID[a.ArchiveID] = a
	}
	got := byID["m1"]
	if got.NestedSkipsCount == nil || *got.NestedSkipsCount != 2 {
		t.Fatalf("mounted count=%v", got.NestedSkipsCount)
	}
	if got.NestedSkipsSummary != sum {
		t.Fatalf("mounted summary=%q", got.NestedSkipsSummary)
	}
	got2 := byID["m2"]
	if got2.NestedSkipsCount == nil || *got2.NestedSkipsCount != 1 {
		t.Fatalf("live count=%v", got2.NestedSkipsCount)
	}
	if got2.NestedSkipsSummary != "skipped 1 nested mount: /nested/bad.7z" {
		t.Fatalf("live summary=%q", got2.NestedSkipsSummary)
	}
}

func TestFormatHuman(t *testing.T) {
	started := "2026-01-01T00:00:00Z"
	now := *status.ParseISOToEpoch("2026-01-01T00:05:00Z")
	payload := status.Build(status.Options{
		Version: "0.1.0",
		PID:     42,
		Archives: []*status.ArchiveInput{{
			ArchiveID:       "id1",
			ArchivePath:     "/mnt/d/Archives/nested.tar",
			ArchiveBasename: "nested.tar",
			Status:          "indexing",
			IndexStartedAt:  &started,
			HooksStatus:     "none",
		}},
		LastScanAt:       "2026-01-01T00:04:00Z",
		LastScan:         map[string]any{"duration_ms": 10.0},
		MaxMountAttempts: 10,
		Now:              now,
		GeneratedAt:      "2026-01-01T00:05:00Z",
	})
	text := status.FormatHuman(payload)
	for _, want := range []string{
		"mount-wrapper 0.1.0",
		"pid=42",
		"indexing=1",
		"in progress:",
		"nested.tar",
		"drvfs",
	} {
		if !contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if !contains(text, "elapsed=300s") && !contains(text, "elapsed=300") {
		t.Fatalf("elapsed missing:\n%s", text)
	}
}

func TestBuildErrorsRecentStuckWithoutFailed(t *testing.T) {
	rec := &status.ArchiveInput{
		ArchiveID:       "a1",
		ArchivePath:     "/s/a.tar",
		ArchiveBasename: "a.tar",
		Status:          "discovered",
		MountAttempts:   10,
		MountRetryable:  false,
		LastError:       strPtr("gave up"),
		HooksStatus:     "none",
	}
	errors := status.BuildErrorsRecent([]*status.ArchiveInput{rec}, 10, 20)
	if len(errors) != 1 {
		t.Fatalf("len=%d", len(errors))
	}
	if !errors[0].Stuck {
		t.Fatal("expected stuck")
	}
}

func TestIncludeSizesMerge(t *testing.T) {
	fake := &fakeMetrics{
		items: []metrics.ArchiveMetrics{{
			ArchiveID:          "a1",
			ArchivePath:        "/s/a.tar",
			ArchiveBasename:    "a.tar",
			Status:             "mounted",
			ArchiveSizeBytes:   metrics.Int64Ptr(100),
			IndexSizeBytes:     metrics.Int64Ptr(10),
			ExtractedSizeBytes: metrics.Int64Ptr(500),
			SpaceSavedBytes:    metrics.Int64Ptr(490),
		}},
	}
	payload := status.Build(status.Options{
		Version: "t",
		PID:     1,
		Archives: []*status.ArchiveInput{{
			ArchiveID:       "a1",
			ArchivePath:     "/s/a.tar",
			ArchiveBasename: "a.tar",
			Status:          "mounted",
			HooksStatus:     "none",
			SizeBytes:       100,
		}},
		IncludeSizes:     true,
		Metrics:          fake,
		MaxMountAttempts: 5,
		GeneratedAt:      "2026-01-01T00:00:00Z",
	})
	if payload.MetricsSummary == nil {
		t.Fatal("metrics_summary missing")
	}
	if payload.MetricsSummary.ArchiveCount != 1 {
		t.Fatalf("summary count=%d", payload.MetricsSummary.ArchiveCount)
	}
	if len(payload.Archives) != 1 || payload.Archives[0].Metrics == nil {
		t.Fatalf("archive metrics: %+v", payload.Archives)
	}
	if payload.Archives[0].Metrics.SpaceSavedBytes == nil || *payload.Archives[0].Metrics.SpaceSavedBytes != 490 {
		t.Fatalf("space_saved=%v", payload.Archives[0].Metrics.SpaceSavedBytes)
	}
}

func TestIsMountInjection(t *testing.T) {
	mp := "/mnt/x"
	payload := status.Build(status.Options{
		Version: "t",
		PID:     1,
		Archives: []*status.ArchiveInput{{
			ArchiveID:       "a1",
			ArchivePath:     "/s/a.tar",
			ArchiveBasename: "a.tar",
			Status:          "mounted",
			MountPath:       &mp,
			HooksStatus:     "success",
		}},
		IsMount:          func(p string) bool { return p == mp },
		MaxMountAttempts: 5,
		GeneratedAt:      "t",
	})
	if payload.Archives[0].IsMounted == nil || !*payload.Archives[0].IsMounted {
		t.Fatalf("is_mounted=%v", payload.Archives[0].IsMounted)
	}
}

func TestEmptyBuild(t *testing.T) {
	p := status.Build(status.Options{Version: "v", PID: 1, GeneratedAt: "t"})
	if p.Counts["mounted"] != 0 {
		t.Fatalf("counts=%v", p.Counts)
	}
	if p.Archives == nil {
		t.Fatal("archives should be non-nil empty")
	}
	if len(p.IndexingArchives) != 0 {
		t.Fatal("indexing_archives")
	}
}

type fakeMetrics struct {
	items []metrics.ArchiveMetrics
}

func (f *fakeMetrics) GetAll(opts metrics.QueryOptions, statuses []string) ([]metrics.ArchiveMetrics, error) {
	return f.items, nil
}

func (f *fakeMetrics) Summary(items []metrics.ArchiveMetrics, opts metrics.QueryOptions) (metrics.Summary, error) {
	return metrics.Summarize(items), nil
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
