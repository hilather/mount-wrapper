package mounter_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/mounter"
)

func sampleReq(t *testing.T, overrides func(*mounter.MountRequest)) mounter.MountRequest {
	t.Helper()
	req := mounter.MountRequest{
		ArchiveID:                "abc-123",
		ArchivePath:              "/data/a.tar.gz",
		IndexPath:                "/var/lib/mount-wrapper/indexes/abc-123.index.sqlite",
		OverlayPath:              "/var/lib/mount-wrapper/overlays/abc-123",
		MountPath:                "/var/lib/mount-wrapper/mounts/a.tar.gz",
		AllowOther:               true,
		IndexWorkers:             4,
		RecursiveMount:           true,
		RecursiveMountExtensions: []string{"/archive", ".raw/-"},
		ExtraArgs:                nil,
		RatarmountBin:            "/usr/bin/ratarmount",
		MountBackend:             mounter.BackendRust,
		RatarmountDebug:          0,
		RatarmountLogDir:         "",
		IndexOnly:                false,
	}
	if overrides != nil {
		overrides(&req)
	}
	return req
}

func TestBuildRatarmountCmd_matrix(t *testing.T) {
	t.Parallel()

	t.Run("default recursive overlay allow_other", func(t *testing.T) {
		cmd := mounter.BuildRatarmountCmd(sampleReq(t, nil))
		if cmd[0] != "/usr/bin/ratarmount" {
			t.Fatalf("bin=%q", cmd[0])
		}
		assertContains(t, cmd, "-f")
		assertContains(t, cmd, "--index-file")
		assertContains(t, cmd, "--recursive")
		assertContains(t, cmd, "--write-overlay")
		assertPair(t, cmd, "-o", "allow_other")
		assertPair(t, cmd, "-P", "4")
		assertContains(t, cmd, "/data/a.tar.gz")
		assertContains(t, cmd, "/var/lib/mount-wrapper/mounts/a.tar.gz")
		assertPair(t, cmd, "--recursive-extensions", "/archive,.raw/-")
		if contains(cmd, "--no-mount") {
			t.Fatal("unexpected --no-mount")
		}
		if contains(cmd, "--use-backend") {
			t.Fatal("must not inject --use-backend")
		}
	})

	t.Run("rust configured bin", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.RatarmountBin = "/opt/ratarmount-rs/ratarmount"
			r.MountBackend = mounter.BackendRust
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if cmd[0] != "/opt/ratarmount-rs/ratarmount" {
			t.Fatalf("bin=%q", cmd[0])
		}
	})

	t.Run("recursive off", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.RecursiveMount = false
			r.RecursiveMountExtensions = []string{"/archive"}
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if contains(cmd, "--recursive") {
			t.Fatal("unexpected --recursive")
		}
		if contains(cmd, "--recursive-extensions") {
			t.Fatal("extensions without recursive")
		}
	})

	t.Run("no overlay", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.OverlayPath = ""
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if contains(cmd, "--write-overlay") {
			t.Fatal("unexpected overlay")
		}
	})

	t.Run("windows_visible off", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.AllowOther = false
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if contains(cmd, "allow_other") {
			t.Fatal("unexpected allow_other")
		}
	})

	t.Run("index_only", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.IndexOnly = true
			r.ArchivePath = "/data/a.tar"
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if !contains(cmd, "--no-mount") {
			t.Fatal("missing --no-mount")
		}
		if cmd[len(cmd)-2] != "/data/a.tar" {
			t.Fatalf("archive pos: %v", cmd)
		}
	})

	t.Run("dedupe recursive in extra_args", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.RecursiveMount = true
			r.ExtraArgs = []string{"--recursive", "--foo", "-r"}
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if count(cmd, "--recursive") != 1 {
			t.Fatalf("recursive count: %v", cmd)
		}
		if !contains(cmd, "--foo") {
			t.Fatal("lost --foo")
		}
		if contains(cmd, "-r") {
			t.Fatal("-r should be stripped from extras when recursive_mount")
		}
	})

	t.Run("explicit use-backend preserved", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.ExtraArgs = []string{"--use-backend", "libarchive"}
		})
		cmd := mounter.BuildRatarmountCmd(req)
		assertPair(t, cmd, "--use-backend", "libarchive")
	})

	t.Run("debug and log dir", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.RatarmountDebug = 9
			r.RatarmountLogDir = "/var/log/mount-wrapper"
		})
		cmd := mounter.BuildRatarmountCmd(req)
		assertPair(t, cmd, "-d", "3") // capped
		logPath := filepath.Join("/var/log/mount-wrapper", "abc-123.ratarmount.log")
		assertPair(t, cmd, "--log-file", logPath)
	})

	t.Run("strip no-mount from extras", func(t *testing.T) {
		req := sampleReq(t, func(r *mounter.MountRequest) {
			r.IndexOnly = false
			r.ExtraArgs = []string{"--no-mount", "--mount", "--keep"}
		})
		cmd := mounter.BuildRatarmountCmd(req)
		if contains(cmd, "--no-mount") || contains(cmd, "--mount") {
			t.Fatalf("mode flags leaked: %v", cmd)
		}
		if !contains(cmd, "--keep") {
			t.Fatal("lost --keep")
		}
	})
}

func TestRequestFromConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		IndexDir:                 "/idx",
		OverlayDir:               "/ov",
		MountRoot:                "/mntroot",
		WriteOverlay:             true,
		WindowsVisible:           true,
		RecursiveMount:           true,
		RecursiveMountExtensions: config.DefaultRecursiveMountExtensions,
		RatarmountIndexWorkers:   2,
		MountBackend:             "rust",
		RatarmountBin:            "/bin/rm",
		ExtraRatarmountArgs:      []string{"--foo"},
	}
	taken := map[string]struct{}{"a.tar.gz": {}}
	req := mounter.RequestFromConfig(cfg, "abcdef12-xxxx", "/data/a.tar.gz", "a.tar.gz", taken, "", "")
	if req.IndexPath != "/idx/abcdef12-xxxx.index.sqlite" {
		t.Fatalf("index=%q", req.IndexPath)
	}
	if req.OverlayPath != "/ov/abcdef12-xxxx" {
		t.Fatalf("overlay=%q", req.OverlayPath)
	}
	if !strings.HasSuffix(req.MountPath, "a.tar.gz--abcdef12") {
		t.Fatalf("mount collision name=%q", req.MountPath)
	}
	if req.RatarmountBin != "/bin/rm" {
		t.Fatalf("bin=%q", req.RatarmountBin)
	}
	if !req.AllowOther || req.IndexWorkers != 2 {
		t.Fatalf("allow/workers: %+v", req)
	}

	cfg.WriteOverlay = false
	req2 := mounter.RequestFromConfig(cfg, "id", "/data/a.tar", "a.tar", nil, "custom", "")
	if req2.OverlayPath != "" {
		t.Fatalf("overlay should be empty: %q", req2.OverlayPath)
	}
	if !strings.HasSuffix(req2.MountPath, "custom") {
		t.Fatalf("mount name override: %q", req2.MountPath)
	}
}

func TestBuildChildEnv(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin", "RUST_LOG=info"}
	env := mounter.BuildChildEnv(mounter.ChildEnvOptions{
		Base:              base,
		Ratarmount7zDebug: true,
		RatarmountRustLog: "ratarmount=debug",
	})
	if !containsEnv(env, "RATARMOUNT_7Z_DEBUG=1") {
		t.Fatalf("missing 7z debug: %v", env)
	}
	if !containsEnv(env, "RUST_LOG=ratarmount=debug") {
		t.Fatalf("RUST_LOG not replaced: %v", env)
	}
	// Original base RUST_LOG should be replaced, not duplicated with old value.
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, "RUST_LOG=") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("RUST_LOG count=%d env=%v", n, env)
	}
}

func TestFormatRecursiveMountExtensions(t *testing.T) {
	t.Parallel()
	got := mounter.FormatRecursiveMountExtensions([]string{"/archive", ".raw/-"})
	if got != "/archive,.raw/-" {
		t.Fatalf("got %q", got)
	}
	if !contains(config.DefaultRecursiveMountExtensions, "/compressed/-") {
		t.Fatal("default extensions missing /compressed/-")
	}
	if contains(config.DefaultRecursiveMountExtensions, "/split") {
		t.Fatal("/split must not be in defaults")
	}
}

func assertContains(t *testing.T, cmd []string, item string) {
	t.Helper()
	if !contains(cmd, item) {
		t.Fatalf("missing %q in %v", item, cmd)
	}
}

func assertPair(t *testing.T, cmd []string, flag, value string) {
	t.Helper()
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == flag && cmd[i+1] == value {
			return
		}
	}
	t.Fatalf("missing pair %s %s in %v", flag, value, cmd)
}

func contains(ss []string, item string) bool {
	for _, s := range ss {
		if s == item {
			return true
		}
	}
	return false
}

func count(ss []string, item string) int {
	n := 0
	for _, s := range ss {
		if s == item {
			n++
		}
	}
	return n
}

func containsEnv(env []string, entry string) bool {
	return contains(env, entry)
}
