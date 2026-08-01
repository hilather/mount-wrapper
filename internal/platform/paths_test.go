package platform

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxDefaultPaths(t *testing.T) {
	t.Parallel()
	p := LinuxDefaultPaths()
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"mount_root", p.MountRoot, "/var/lib/mount-wrapper/mounts"},
		{"index_dir", p.IndexDir, "/var/lib/mount-wrapper/indexes"},
		{"overlay_dir", p.OverlayDir, "/var/lib/mount-wrapper/overlays"},
		{"state_db", p.StateDB, "/var/lib/mount-wrapper/state.db"},
		{"hooks_dir", p.HooksDir, "/etc/mount-wrapper/hooks.d"},
		{"ratarmount_bin", p.RatarmountBin, "ratarmount-rs"},
		{"control_socket", p.ControlSocket, "/run/mount-wrapper/control.sock"},
		{"pid_file", p.PIDFile, "/run/mount-wrapper/mount-wrapper.pid"},
		{"archiveconverter_output_dir", p.ArchiveConverterOutputDir, "/var/lib/mount-wrapper/converted"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
	// Must not leak upstream tarmount-wsl product paths.
	all := strings.Join([]string{
		p.MountRoot, p.IndexDir, p.OverlayDir, p.StateDB, p.HooksDir,
		p.ControlSocket, p.PIDFile, p.ArchiveConverterOutputDir,
	}, " ")
	if strings.Contains(all, "tarmount") {
		t.Fatalf("paths still reference tarmount: %s", all)
	}
}

func TestDarwinDefaultPathsUnderCustomHome(t *testing.T) {
	t.Parallel()
	// Portable absolute home for path-shape assertions (not the real $HOME).
	home := "/Users/test"
	p := DarwinDefaultPaths(home)

	if !strings.Contains(p.MountRoot, "Application Support") {
		t.Fatalf("mount_root missing Application Support: %s", p.MountRoot)
	}
	if !strings.HasPrefix(p.MountRoot, home) {
		t.Fatalf("mount_root not under home: %s", p.MountRoot)
	}
	wantMount := filepath.Join(home, "Library", "Application Support", "mount-wrapper", "mounts")
	if p.MountRoot != wantMount {
		t.Fatalf("mount_root=%q want %q", p.MountRoot, wantMount)
	}
	wantSock := filepath.Join(home, "Library", "Caches", "mount-wrapper", "run", "control.sock")
	if p.ControlSocket != wantSock {
		t.Fatalf("control_socket=%q want %q", p.ControlSocket, wantSock)
	}
	wantPID := filepath.Join(home, "Library", "Caches", "mount-wrapper", "run", "mount-wrapper.pid")
	if p.PIDFile != wantPID {
		t.Fatalf("pid_file=%q want %q", p.PIDFile, wantPID)
	}
	if !strings.HasSuffix(p.StateDB, "state.db") {
		t.Fatalf("state_db=%q", p.StateDB)
	}
	if p.RatarmountBin != "ratarmount-rs" {
		t.Fatalf("ratarmount_bin=%q", p.RatarmountBin)
	}
	wantConverted := filepath.Join(home, "Library", "Application Support", "mount-wrapper", "converted")
	if p.ArchiveConverterOutputDir != wantConverted {
		t.Fatalf("converted=%q want %q", p.ArchiveConverterOutputDir, wantConverted)
	}
}

func TestDefaultPathsFor(t *testing.T) {
	t.Parallel()
	linux := DefaultPathsFor("linux")
	if linux.MountRoot != "/var/lib/mount-wrapper/mounts" {
		t.Fatalf("linux paths unexpected: %+v", linux)
	}
	// Darwin without relying on real home: call DarwinDefaultPaths directly
	// is covered above; DefaultPathsFor("darwin") uses UserHomeDir.
	darwin := DefaultPathsFor("darwin")
	if !strings.Contains(darwin.MountRoot, "Application Support") {
		t.Fatalf("darwin paths unexpected: %+v", darwin)
	}
	other := DefaultPathsFor("windows")
	if other.MountRoot != linux.MountRoot {
		t.Fatalf("other should use linux layout: %+v", other)
	}
}

func TestDefaultWindowsVisibleAndUseInotify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		platform string
		winVis   bool
		inotify  bool
	}{
		{"linux", true, true},
		{"darwin", false, false},
		{"windows", true, false},
		{"other", true, false},
	}
	for _, tc := range cases {
		if got := DefaultWindowsVisible(tc.platform); got != tc.winVis {
			t.Errorf("DefaultWindowsVisible(%q)=%v want %v", tc.platform, got, tc.winVis)
		}
		if got := DefaultUseInotify(tc.platform); got != tc.inotify {
			t.Errorf("DefaultUseInotify(%q)=%v want %v", tc.platform, got, tc.inotify)
		}
	}
}
