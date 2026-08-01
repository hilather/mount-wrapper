package platform

import (
	"reflect"
	"strings"
	"testing"
)

func TestFuseDeviceCandidates(t *testing.T) {
	t.Parallel()
	linux := FuseDeviceCandidates("linux")
	if !reflect.DeepEqual(linux, []string{"/dev/fuse"}) {
		t.Fatalf("linux candidates=%v", linux)
	}
	darwin := FuseDeviceCandidates("darwin")
	want := []string{
		"/Library/Filesystems/macfuse.fs",
		"/Library/Filesystems/osxfuse.fs",
		"/dev/macfuse0",
		"/dev/osxfuse0",
		"/dev/fuse",
	}
	if !reflect.DeepEqual(darwin, want) {
		t.Fatalf("darwin candidates=%v want %v", darwin, want)
	}
}

func TestUnmountCommandLinux(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		lazy  bool
		which WhichFunc
		want  []string
	}{
		{
			name: "prefers fusermount3 lazy",
			lazy: true,
			which: func(n string) string {
				return map[string]string{
					"fusermount3": "/bin/fusermount3",
					"fusermount":  "/bin/fusermount",
				}[n]
			},
			want: []string{"/bin/fusermount3", "-u", "-z", "/mnt/x"},
		},
		{
			name: "fusermount fallback",
			lazy: false,
			which: func(n string) string {
				if n == "fusermount" {
					return "/bin/fusermount"
				}
				return ""
			},
			want: []string{"/bin/fusermount", "-u", "/mnt/x"},
		},
		{
			name: "umount lazy last resort",
			lazy: true,
			which: func(n string) string {
				if n == "umount" {
					return "/bin/umount"
				}
				return ""
			},
			want: []string{"/bin/umount", "-l", "/mnt/x"},
		},
		{
			name:  "none",
			lazy:  false,
			which: func(string) string { return "" },
			want:  nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UnmountCommand("/mnt/x", tc.lazy, "linux", tc.which)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestUnmountCommandDarwin(t *testing.T) {
	t.Parallel()
	umountOnly := func(n string) string {
		if n == "umount" {
			return "/sbin/umount"
		}
		return ""
	}
	got := UnmountCommand("/Volumes/x", true, "darwin", umountOnly)
	want := []string{"/sbin/umount", "-f", "/Volumes/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lazy umount: got %#v want %#v", got, want)
	}
	got2 := UnmountCommand("/Volumes/x", false, "darwin", umountOnly)
	want2 := []string{"/sbin/umount", "/Volumes/x"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("umount: got %#v want %#v", got2, want2)
	}

	diskutilOnly := func(n string) string {
		if n == "diskutil" {
			return "/usr/sbin/diskutil"
		}
		return ""
	}
	got3 := UnmountCommand("/Volumes/x", true, "darwin", diskutilOnly)
	want3 := []string{"/usr/sbin/diskutil", "unmount", "force", "/Volumes/x"}
	if !reflect.DeepEqual(got3, want3) {
		t.Fatalf("diskutil force: got %#v want %#v", got3, want3)
	}
	got4 := UnmountCommand("/Volumes/x", false, "darwin", diskutilOnly)
	want4 := []string{"/usr/sbin/diskutil", "unmount", "/Volumes/x"}
	if !reflect.DeepEqual(got4, want4) {
		t.Fatalf("diskutil: got %#v want %#v", got4, want4)
	}
}

func TestUnmountFuseRunner(t *testing.T) {
	t.Parallel()
	var seen [][]string
	runner := func(argv []string) int {
		cp := append([]string(nil), argv...)
		seen = append(seen, cp)
		return 0
	}
	which := func(n string) string {
		if n == "fusermount3" {
			return "/bin/fusermount3"
		}
		return ""
	}
	code := UnmountFuse("/tmp/m", false, "linux", runner, which)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], []string{"/bin/fusermount3", "-u", "/tmp/m"}) {
		t.Fatalf("seen=%#v", seen)
	}
}

func TestUnmountFuseMissingReturns127(t *testing.T) {
	t.Parallel()
	code := UnmountFuse("/tmp/m", false, "darwin", nil, func(string) string { return "" })
	if code != 127 {
		t.Fatalf("code=%d want 127", code)
	}
}

func TestProbeFusePresence(t *testing.T) {
	t.Parallel()
	darwinOK := ProbeFusePresence("darwin", func(p string) bool {
		return p == "/Library/Filesystems/macfuse.fs"
	})
	if !darwinOK.OK {
		t.Fatalf("expected ok: %+v", darwinOK)
	}
	if len(darwinOK.Found) != 1 || darwinOK.Found[0] != "/Library/Filesystems/macfuse.fs" {
		t.Fatalf("found=%v", darwinOK.Found)
	}

	linuxMissing := ProbeFusePresence("linux", func(string) bool { return false })
	if linuxMissing.OK {
		t.Fatal("expected not ok")
	}
	if !strings.Contains(linuxMissing.Hint, "fuse3") && !strings.Contains(strings.ToLower(linuxMissing.Hint), "fuse") {
		t.Fatalf("hint=%q", linuxMissing.Hint)
	}
}

func TestProbeUnmountTool(t *testing.T) {
	t.Parallel()
	probe := ProbeUnmountTool("darwin", func(n string) string {
		if n == "umount" {
			return "/sbin/umount"
		}
		return ""
	})
	if !probe.OK {
		t.Fatalf("expected ok: %+v", probe)
	}
	if !strings.Contains(probe.Tool, "umount") {
		t.Fatalf("tool=%q", probe.Tool)
	}
	if len(probe.CommandTemplate) == 0 {
		t.Fatal("empty command template")
	}
}

func TestUnmountToolName(t *testing.T) {
	t.Parallel()
	if UnmountToolName("darwin") != "umount (macFUSE)" {
		t.Fatal(UnmountToolName("darwin"))
	}
	if UnmountToolName("linux") != "fusermount3/fusermount" {
		t.Fatal(UnmountToolName("linux"))
	}
}
