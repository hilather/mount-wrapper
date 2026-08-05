package metrics

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestParseProcStatusMem(t *testing.T) {
	t.Parallel()
	sample := []byte("Name:\tsleep\nVmRSS:\t   2048 kB\nVmHWM:\t   4096 kB\nVmSize:\t   8000 kB\n")
	st := parseProcStatusMem(sample)
	if st.vmRSSKB != 2048 {
		t.Fatalf("VmRSS=%d want 2048", st.vmRSSKB)
	}
	if st.vmHWMKB != 4096 {
		t.Fatalf("VmHWM=%d want 4096", st.vmHWMKB)
	}
}

func TestComputeArchiveMetrics_MountRSS(t *testing.T) {
	t.Parallel()
	in := ArchiveInput{
		ArchiveID:   "rss-1",
		ArchivePath: "/data/a.tar",
		Status:      "mounted",
		IndexPath:   "/idx/a.sqlite",
		MountPID:    4242,
	}
	sizes := MapSizeProvider{
		Files:   map[string]int64{"/data/a.tar": 100},
		Indexes: map[string]int64{"/idx/a.sqlite": 10},
	}
	extracted := MapExtractedProvider{
		Index: map[string]int64{"/idx/a.sqlite": 1000},
	}
	mem := MapProcessMem{
		RSS:     map[int]int64{4242: 50 * 1024 * 1024},
		RSSPeak: map[int]int64{4242: 80 * 1024 * 1024},
	}
	m := ComputeArchiveMetrics(in, sizes, extracted, nil, mem, ComputeOptions{})
	if m.MountPID != 4242 {
		t.Fatalf("mount_pid=%d", m.MountPID)
	}
	if m.MountRSSBytes == nil || *m.MountRSSBytes != 50*1024*1024 {
		t.Fatalf("mount_rss=%v", m.MountRSSBytes)
	}
	if m.MountRSSPeakBytes == nil || *m.MountRSSPeakBytes != 80*1024*1024 {
		t.Fatalf("mount_rss_peak=%v", m.MountRSSPeakBytes)
	}

	// No PID → no RSS.
	in2 := in
	in2.MountPID = 0
	m2 := ComputeArchiveMetrics(in2, sizes, extracted, nil, mem, ComputeOptions{})
	if m2.MountRSSBytes != nil || m2.MountRSSPeakBytes != nil {
		t.Fatalf("expected nil RSS without pid: rss=%v peak=%v", m2.MountRSSBytes, m2.MountRSSPeakBytes)
	}
}

func TestSummarize_MountRSS(t *testing.T) {
	t.Parallel()
	items := []ArchiveMetrics{
		{ArchiveID: "a", MountRSSBytes: Int64Ptr(1000), MountRSSPeakBytes: Int64Ptr(1500)},
		{ArchiveID: "b", MountRSSBytes: Int64Ptr(2000)},
		{ArchiveID: "c"}, // unmounted
	}
	s := Summarize(items)
	if s.ArchivesWithMountRSS != 2 {
		t.Fatalf("with_mount_rss=%d", s.ArchivesWithMountRSS)
	}
	if s.TotalMountRSSBytes != 3000 {
		t.Fatalf("total_rss=%d", s.TotalMountRSSBytes)
	}
	if s.TotalMountRSSPeakBytes != 1500 {
		t.Fatalf("total_peak=%d", s.TotalMountRSSPeakBytes)
	}
}

func TestDefaultProcessMem_LiveProcess(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("process RSS only implemented on linux/darwin")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	// Give the process a moment to appear in /proc or ps.
	time.Sleep(50 * time.Millisecond)
	pid := cmd.Process.Pid
	var mem DefaultProcessMem
	rss := mem.RSSBytes(pid)
	if rss == nil || *rss <= 0 {
		t.Fatalf("RSS for sleep pid %d: %v", pid, rss)
	}
	// Sanity: sleep should be under 100 MiB.
	if *rss > 100*1024*1024 {
		t.Fatalf("RSS unreasonably large: %d", *rss)
	}
	if runtime.GOOS == "linux" {
		peak := mem.RSSPeakBytes(pid)
		if peak == nil || *peak < *rss {
			t.Fatalf("peak=%v rss=%v", peak, rss)
		}
	}
}

func TestDefaultProcessMem_Self(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("linux /proc only")
	}
	var mem DefaultProcessMem
	rss := mem.RSSBytes(os.Getpid())
	if rss == nil || *rss <= 0 {
		t.Fatalf("self RSS=%v", rss)
	}
	peak := mem.RSSPeakBytes(os.Getpid())
	if peak == nil || *peak < *rss {
		t.Fatalf("self peak=%v rss=%v", peak, rss)
	}
}
