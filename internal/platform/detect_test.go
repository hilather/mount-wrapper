package platform

import (
	"errors"
	"testing"
)

func TestHostPlatformOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"linux", PlatformLinux},
		{"linux2", PlatformLinux},
		{"Linux", PlatformLinux},
		{"LINUX", PlatformLinux},
		{"darwin", PlatformDarwin},
		{"Darwin", PlatformDarwin},
		{"windows", PlatformOther},
		{"freebsd", PlatformOther},
		{"", PlatformOther},
		{"  linux  ", PlatformLinux},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := HostPlatformOf(tc.in); got != tc.want {
				t.Fatalf("HostPlatformOf(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHostPlatformMatchesGOOS(t *testing.T) {
	// Smoke: HostPlatform is one of the three labels.
	got := HostPlatform()
	switch got {
	case PlatformLinux, PlatformDarwin, PlatformOther:
	default:
		t.Fatalf("HostPlatform()=%q unexpected", got)
	}
	if HostPlatformOf("linux") != PlatformLinux {
		t.Fatal("expected linux mapping")
	}
}

func TestIsLinuxIsDarwin(t *testing.T) {
	t.Parallel()
	if !IsLinux("linux") || IsLinux("darwin") {
		t.Fatal("IsLinux mismatch")
	}
	if !IsDarwin("darwin") || IsDarwin("linux") {
		t.Fatal("IsDarwin mismatch")
	}
}

func TestIsWSL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		platform   string
		env        map[string]string
		version    string
		versionErr error
		want       bool
	}{
		{
			name:     "darwin never",
			platform: "darwin",
			env:      map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			want:     false,
		},
		{
			name:     "distro name",
			platform: "linux",
			env:      map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			want:     true,
		},
		{
			name:     "interop",
			platform: "linux",
			env:      map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"},
			want:     true,
		},
		{
			name:     "proc microsoft",
			platform: "linux",
			version:  "Linux version 5.15.0-microsoft-standard-WSL2",
			want:     true,
		},
		{
			name:     "proc wsl case",
			platform: "linux",
			version:  "something WSL something",
			want:     true,
		},
		{
			name:     "native linux",
			platform: "linux",
			version:  "Linux version 6.1.0-generic",
			want:     false,
		},
		{
			name:       "proc missing",
			platform:   "linux",
			versionErr: errors.New("no file"),
			want:       false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(k string) string {
				if tc.env == nil {
					return ""
				}
				return tc.env[k]
			}
			readVersion := func() (string, error) {
				if tc.versionErr != nil {
					return "", tc.versionErr
				}
				return tc.version, nil
			}
			if got := isWSL(tc.platform, getenv, readVersion); got != tc.want {
				t.Fatalf("isWSL=%v want %v", got, tc.want)
			}
		})
	}
}
