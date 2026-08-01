package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Packaging polish smoke: critical strings exist so install docs and unit
// stay aligned. Does not run create-user.sh (requires root) or publish packages.
func TestPackagingArtifacts(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))

	type wantFile struct {
		rel      string
		contains []string
	}
	cases := []wantFile{
		{
			rel: "packaging/systemd/mount-wrapper.service",
			contains: []string{
				"User=mount-wrapper",
				"Group=mount-wrapper",
				"TimeoutStopSec=300",
				"DeviceAllow=/dev/fuse",
				"EnvironmentFile=-/etc/mount-wrapper/env",
				"NoNewPrivileges=true",
				"ProtectSystem=strict",
				"RuntimeDirectory=mount-wrapper",
			},
		},
		{
			rel: "packaging/scripts/create-user.sh",
			contains: []string{
				"mount-wrapper",
				"user_allow_other",
				"id -u",
			},
		},
		{
			rel: "packaging/windows-task-scheduler.xml.example",
			contains: []string{
				"wsl.exe",
				"LogonTrigger",
			},
		},
		{
			rel: "packaging/wsl.conf.snippet",
			contains: []string{
				"systemd=true",
				"[automount]",
			},
		},
		{
			rel: "packaging/launchd/com.hilather.mount-wrapper.plist.example",
			contains: []string{
				"com.hilather.mount-wrapper",
				"serve",
				// serve has no --foreground; plist must not invent one
			},
		},
		{
			rel: ".goreleaser.yaml",
			contains: []string{
				"CGO_ENABLED=0",
				"main.version",
				"SHA256SUMS",
				"linux",
				"darwin",
			},
		},
		{
			rel: "packaging/nfpm.yaml",
			contains: []string{
				"mount-wrapper",
				"packaging/systemd/mount-wrapper.service",
			},
		},
		{
			rel: "packaging/env.example",
			contains: []string{
				"PATH=",
			},
		},
		{
			rel: "docs/install.md",
			contains: []string{
				"TimeoutStopSec",
				"DeviceAllow",
				"ratarmount-rs",
				"SHA256SUMS",
				"create-user.sh",
			},
		},
		{
			rel: "docs/security.md",
			contains: []string{
				"MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH",
				"web_token",
				"hooks",
				"loopback",
			},
		},
		{
			rel: "packaging/man/mount-wrapper.1",
			contains: []string{
				".TH MOUNT-WRAPPER",
				"serve",
				"doctor",
				"config",
				"status",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.rel, func(t *testing.T) {
			p := filepath.Join(root, tc.rel)
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", tc.rel, err)
			}
			s := string(body)
			for _, needle := range tc.contains {
				if !strings.Contains(s, needle) {
					t.Errorf("%s: missing %q", tc.rel, needle)
				}
			}
			if strings.Contains(tc.rel, "launchd") {
				// Only flag argv in ProgramArguments, not doc comments that
				// warn operators not to use a nonexistent --foreground flag.
				if i := strings.Index(s, "<key>ProgramArguments</key>"); i >= 0 {
					rest := s[i:]
					if end := strings.Index(rest, "</array>"); end >= 0 {
						rest = rest[:end]
					}
					if strings.Contains(rest, "--foreground") {
						t.Errorf("launchd ProgramArguments must not pass --foreground (serve has no such flag)")
					}
				}
			}
		})
	}

	// Examples already covered by TestLoadPackagingExamples; assert key comments/keys.
	linuxEx := filepath.Join(root, "packaging/examples/config.yaml.example")
	b, err := os.ReadFile(linuxEx)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"web_enabled", "control_socket", "mount-wrapper"} {
		if !strings.Contains(string(b), needle) {
			t.Errorf("config.yaml.example missing %q", needle)
		}
	}
	macEx := filepath.Join(root, "packaging/examples/config.yaml.macos.example")
	b, err = os.ReadFile(macEx)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"web_enabled", "control_socket", "Caches"} {
		if !strings.Contains(string(b), needle) {
			t.Errorf("config.yaml.macos.example missing %q", needle)
		}
	}
}
