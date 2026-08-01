package mounter_test

import (
	"os"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/platform"
)

func TestIsProcessAlive(t *testing.T) {
	t.Parallel()
	if mounter.IsProcessAlive(0) || mounter.IsProcessAlive(-1) {
		t.Fatal("invalid pids must be dead")
	}
	if !mounter.IsProcessAlive(os.Getpid()) {
		t.Fatal("self should be alive")
	}
}

func TestTimeoutHelpers(t *testing.T) {
	t.Parallel()
	if mounter.MountReadyTimeout(12.5) != 12500*time.Millisecond {
		t.Fatalf("mount ready: %v", mounter.MountReadyTimeout(12.5))
	}
	if mounter.MountReadyTimeout(0) != mounter.DefaultMountReadyTimeout {
		t.Fatal("mount ready default")
	}
	if mounter.UnmountTimeout(5) != 5*time.Second {
		t.Fatal("unmount")
	}
	if mounter.UnmountTimeout(-1) != mounter.DefaultUnmountTimeout {
		t.Fatal("unmount default")
	}
}

func TestLimitReached(t *testing.T) {
	t.Parallel()
	cases := []struct {
		active, limit int
		want          bool
	}{
		{0, 0, false},  // unlimited
		{5, 0, false},  // unlimited
		{0, -1, false}, // unlimited
		{0, 1, false},
		{1, 1, true},
		{2, 1, true},
		{1, 2, false},
		{2, 2, true},
	}
	for _, tc := range cases {
		if got := mounter.LimitReached(tc.active, tc.limit); got != tc.want {
			t.Fatalf("LimitReached(%d,%d)=%v want %v", tc.active, tc.limit, got, tc.want)
		}
	}
	if mounter.SlotsAvailable(0, 0) < 1 {
		t.Fatal("unlimited slots")
	}
	if mounter.SlotsAvailable(2, 2) != 0 {
		t.Fatal("full")
	}
	if mounter.SlotsAvailable(1, 3) != 2 {
		t.Fatal("two left")
	}
	if mounter.CountActive(true, false, true) != 2 {
		t.Fatal("count")
	}
}

func TestNextMountAttempt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cur, max      int
		wantAttempts  int
		wantRetryable bool
	}{
		{0, 10, 1, true},
		{9, 10, 10, false},
		{8, 10, 9, true},
		{0, 1, 1, false},
		{5, 0, 6, false}, // degenerate max
	}
	for _, tc := range cases {
		a, r := mounter.NextMountAttempt(tc.cur, tc.max)
		if a != tc.wantAttempts || r != tc.wantRetryable {
			t.Fatalf("Next(%d,%d)=(%d,%v) want (%d,%v)",
				tc.cur, tc.max, a, r, tc.wantAttempts, tc.wantRetryable)
		}
	}
	if !mounter.MountRetryable(0, 10) || mounter.MountRetryable(10, 10) {
		t.Fatal("MountRetryable")
	}
	a, r := mounter.ShouldResetAttempts()
	if a != 0 || !r {
		t.Fatalf("reset=%d %v", a, r)
	}
}

func TestIndexPathAllowed_drvfs(t *testing.T) {
	t.Parallel()
	if err := mounter.IndexPathAllowed("/mnt/c/indexes/a.sqlite", false); err == nil {
		t.Fatal("expected DrvFs refuse")
	}
	if err := mounter.IndexPathAllowed("/mnt/c/indexes/a.sqlite", true); err != nil {
		t.Fatalf("allowed flag: %v", err)
	}
	if err := mounter.IndexPathAllowed("/var/lib/mount-wrapper/indexes/a.sqlite", false); err != nil {
		t.Fatalf("linux path: %v", err)
	}
	if err := mounter.EnginePathsAllowed(
		"/mnt/d/idx",
		"/var/lib/mount-wrapper/overlays/x",
		"/var/lib/mount-wrapper/mounts/x",
		false,
	); err == nil {
		t.Fatal("engine paths should refuse index on DrvFs")
	}
	if err := mounter.EnginePathsAllowed(
		"/var/lib/mount-wrapper/indexes/x",
		"/mnt/d/ov",
		"/var/lib/mount-wrapper/mounts/x",
		false,
	); err == nil {
		t.Fatal("overlay on DrvFs should refuse")
	}
}

func TestRegistry(t *testing.T) {
	t.Parallel()
	r := mounter.NewRegistry()
	m := &mounter.ManagedMount{
		ArchiveID:    "a1",
		PID:          42,
		Phase:        mounter.PhaseIndexOnly,
		IsFirstIndex: true,
	}
	r.Put(m)
	if r.Len() != 1 || r.Get("a1") == nil {
		t.Fatal("put/get")
	}
	if !r.HoldsIndexSlot("a1") || r.HoldsMountSlot("a1") {
		t.Fatal("index slot")
	}
	if r.CountPhase(mounter.PhaseIndexOnly) != 1 {
		t.Fatal("count phase")
	}
	m.Phase = mounter.PhaseMount
	r.Put(m)
	if !r.HoldsMountSlot("a1") {
		t.Fatal("mount slot")
	}
	dropped := r.Drop("a1")
	if dropped == nil || r.Len() != 0 {
		t.Fatal("drop")
	}
	if r.Get("missing") != nil {
		t.Fatal("missing")
	}
}

func TestFusermountUnmount_adapter(t *testing.T) {
	t.Parallel()
	var calls []struct {
		path string
		lazy bool
	}
	runner := func(argv []string) int {
		lazy := false
		for _, a := range argv {
			if a == "-z" || a == "-l" || a == "-f" || a == "force" {
				lazy = true
			}
		}
		path := argv[len(argv)-1]
		calls = append(calls, struct {
			path string
			lazy bool
		}{path, lazy})
		return 0
	}
	which := func(name string) string {
		if name == "fusermount3" || name == "fusermount" || name == "umount" {
			return "/bin/" + name
		}
		return ""
	}
	code := mounter.FusermountUnmount("/tmp/m", true, "linux", runner, which)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(calls) != 1 || calls[0].path != "/tmp/m" || !calls[0].lazy {
		t.Fatalf("calls=%v", calls)
	}
	_ = platform.UnmountToolName("linux")
}

func TestUnmountSequence(t *testing.T) {
	t.Parallel()
	var unmountCalls []bool
	mounted := true
	// Clock: first calls for deadline start, then advance past timeout so loop exits.
	base := time.Unix(1_700_000_000, 0)
	n := 0
	now := func() time.Time {
		// Each call advances 100ms so 50ms timeout expires quickly.
		t0 := base.Add(time.Duration(n) * 100 * time.Millisecond)
		n++
		return t0
	}
	res := mounter.UnmountSequence(nil, 0, "/tmp/m", mounter.UnmountOptions{
		Timeout:  50 * time.Millisecond,
		Platform: "linux",
		IsMount: func(string) bool {
			return mounted
		},
		Which: func(name string) string {
			if name == "fusermount3" {
				return "/bin/fusermount3"
			}
			return ""
		},
		Runner: func(argv []string) int {
			lazy := false
			for _, a := range argv {
				if a == "-z" {
					lazy = true
					mounted = false
				}
			}
			unmountCalls = append(unmountCalls, lazy)
			return 0
		},
		Sleep: func(time.Duration) {},
		Now:   now,
	})
	if !res.LazyUsed {
		t.Fatalf("expected lazy unmount, calls=%v res=%+v", unmountCalls, res)
	}
	if res.StillMounted {
		t.Fatal("should clear after lazy")
	}
}
