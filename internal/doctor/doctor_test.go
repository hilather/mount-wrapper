package doctor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/doctor"
)

// testEnv holds injectable fakes for all external probes.
type testEnv struct {
	which        map[string]string
	exists       map[string]bool
	dirs         map[string]bool
	exec         map[string]bool
	writable     map[string]bool
	free         map[string]int64
	freeOK       map[string]bool
	users        map[string]bool
	files        map[string]string
	binOut       map[string]string // key: "path|--version" or "path|--help"
	binErr       map[string]error
	pid1         string
	pid1Err      error
	writes       map[string]string
	mkdirs       []string
	isWSL        bool
	platform     string
	goVersion    string
	serviceUser  string
	fuseConfPath string
}

func newEnv() *testEnv {
	return &testEnv{
		which:     map[string]string{},
		exists:    map[string]bool{},
		dirs:      map[string]bool{},
		exec:      map[string]bool{},
		writable:  map[string]bool{},
		free:      map[string]int64{},
		freeOK:    map[string]bool{},
		users:     map[string]bool{},
		files:     map[string]string{},
		binOut:    map[string]string{},
		binErr:    map[string]error{},
		writes:    map[string]string{},
		platform:  "linux",
		pid1:      "systemd",
		goVersion: "go1.25.0",
	}
}

func (e *testEnv) setExec(path string, version, help string) {
	e.exists[path] = true
	e.exec[path] = true
	if version != "" {
		e.binOut[path+"|--version"] = version + "\n"
	}
	if help != "" {
		e.binOut[path+"|--help"] = help
	} else {
		e.binOut[path+"|--help"] = "Usage: tool -f --foreground\n"
	}
}

func (e *testEnv) opts(cfg *config.Config) doctor.Options {
	wsl := e.isWSL
	return doctor.Options{
		Config:       cfg,
		Platform:     e.platform,
		GoVersion:    e.goVersion,
		IsWSL:        &wsl,
		ServiceUser:  e.serviceUser,
		FuseConfPath: e.fuseConfPath,
		Which: func(name string) string {
			return e.which[name]
		},
		PathExists: func(path string) bool {
			return e.exists[path] || e.dirs[path] || e.exec[path]
		},
		IsExecutable: func(path string) bool {
			return e.exec[path]
		},
		IsDir: func(path string) bool {
			return e.dirs[path]
		},
		Writable: func(path string) bool {
			if v, ok := e.writable[path]; ok {
				return v
			}
			return false
		},
		FreeBytes: func(path string) (int64, bool) {
			if ok, present := e.freeOK[path]; present {
				return e.free[path], ok
			}
			if v, ok := e.free[path]; ok {
				return v, true
			}
			return 0, false
		},
		LookupUser: func(name string) bool {
			return e.users[name]
		},
		ReadFile: func(path string) (string, error) {
			if v, ok := e.files[path]; ok {
				return v, nil
			}
			return "", os.ErrNotExist
		},
		RunBin: func(bin string, args ...string) (string, error) {
			key := bin + "|" + strings.Join(args, " ")
			out := e.binOut[key]
			err := e.binErr[key]
			return out, err
		},
		WriteFile: func(path string, content []byte, mode os.FileMode) error {
			e.writes[path] = string(content)
			e.exists[path] = true
			return nil
		},
		MkdirAll: func(path string, mode os.FileMode) error {
			e.mkdirs = append(e.mkdirs, path)
			e.dirs[path] = true
			return nil
		},
		ReadPID1Comm: func() (string, error) {
			if e.pid1Err != nil {
				return "", e.pid1Err
			}
			return e.pid1, nil
		},
	}
}

func mustCfg(t *testing.T, raw map[string]any, path string) *config.Config {
	t.Helper()
	cfg, err := config.FromMap(raw, path)
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}
	return cfg
}

func checkByName(r *doctor.Report, name string) doctor.CheckResult {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return doctor.CheckResult{}
}

func names(r *doctor.Report) map[string]struct{} {
	m := make(map[string]struct{}, len(r.Checks))
	for _, c := range r.Checks {
		m[c.Name] = struct{}{}
	}
	return m
}

func TestDoctorWithoutConfig(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/usr/bin/fusermount3"
	e.users["mount-wrapper"] = true
	rm := "/usr/local/bin/ratarmount-rs"
	e.which["ratarmount-rs"] = rm
	e.setExec(rm, "ratarmount-rs 0.1.0", "Usage: -f --foreground")

	r := doctor.Run(e.opts(nil))
	if r == nil {
		t.Fatal("nil report")
	}
	n := names(r)
	for _, want := range []string{
		"go_version", "host_platform", "peercred", "fuse_device", "fusermount",
		"user_allow_other", "ratarmount_bin", "archiveconverter", "sevenzip_bin",
		"mount_backend", "systemd_pid1", "service_user",
	} {
		if _, ok := n[want]; !ok {
			t.Errorf("missing check %q", want)
		}
	}
	// Without config: no path.* / source_dirs / config checks.
	for name := range n {
		if strings.HasPrefix(name, "path.") || strings.HasPrefix(name, "source_dirs") || name == "config" {
			t.Errorf("unexpected config-dependent check %q without config", name)
		}
	}
	if !r.OK {
		t.Fatalf("expected OK report, got FAIL: %s", doctor.FormatText(r))
	}
	rmCheck := checkByName(r, "ratarmount_bin")
	if !rmCheck.OK || !strings.Contains(rmCheck.Message, "ratarmount-rs") {
		t.Fatalf("ratarmount_bin: %+v", rmCheck)
	}
}

func TestDoctorRustBackendSkipsPythonSevenzip(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount"] = "/bin/fusermount"
	e.users["mount-wrapper"] = true
	e.setExec("/usr/bin/false", "false", "no foreground here") // still "executable"

	cfg := mustCfg(t, map[string]any{
		"mount_backend":  "rust",
		"ratarmount_bin": "/usr/bin/false",
		"mount_root":     "/var/lib/mount-wrapper/mounts",
	}, "/tmp/test-config.yaml")

	// false has no -f/--foreground in help → error severity when help lacks it.
	e.binOut["/usr/bin/false|--help"] = "Usage: false\n"
	e.binOut["/usr/bin/false|--version"] = "false 1\n"

	r := doctor.Run(e.opts(cfg))
	n := names(r)
	if _, ok := n["sevenzip_backend"]; ok {
		t.Fatal("sevenzip_backend should not exist (Python-only upstream check)")
	}
	if _, ok := n["py7zr_backend"]; ok {
		t.Fatal("py7zr_backend should not exist")
	}
	mb := checkByName(r, "mount_backend")
	if !mb.OK || (!strings.Contains(strings.ToLower(mb.Message), "rust") &&
		!strings.Contains(strings.ToLower(mb.Message), "ratarmount-rs")) {
		t.Fatalf("mount_backend: %+v", mb)
	}
}

func TestDoctorWithConfigSourceDirs(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/usr/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.dirs["/tmp"] = true
	e.dirs["/mnt/d/Archives"] = true
	e.writable["/var/lib/mount-wrapper"] = true
	e.exists["/var/lib/mount-wrapper"] = true
	e.dirs["/var/lib/mount-wrapper"] = true
	e.writable["/run/mount-wrapper"] = true
	e.exists["/run/mount-wrapper"] = true
	e.dirs["/run/mount-wrapper"] = true
	// Parent of default paths
	for _, p := range []string{
		"/var/lib/mount-wrapper/mounts",
		"/var/lib/mount-wrapper/indexes",
		"/var/lib/mount-wrapper/overlays",
	} {
		e.dirs[p] = true
		e.exists[p] = true
		e.writable[p] = true
	}
	rm := "/opt/ratarmount-rs"
	e.setExec(rm, "v1", "")

	cfg := mustCfg(t, map[string]any{
		"source_dirs":    []any{"/tmp", `D:\Archives`},
		"mount_root":     "/var/lib/mount-wrapper/mounts",
		"ratarmount_bin": rm,
	}, "/tmp/test-config.yaml")

	r := doctor.Run(e.opts(cfg))
	if r.ConfigPath != "/tmp/test-config.yaml" {
		t.Fatalf("config_path=%q", r.ConfigPath)
	}
	if checkByName(r, "config").Name == "" {
		t.Fatal("missing config check")
	}
	var sourceChecks []doctor.CheckResult
	for _, c := range r.Checks {
		if strings.HasPrefix(c.Name, "source_dirs") {
			sourceChecks = append(sourceChecks, c)
		}
	}
	if len(sourceChecks) != 2 {
		t.Fatalf("source checks=%d %+v", len(sourceChecks), sourceChecks)
	}
	var drive doctor.CheckResult
	for _, c := range sourceChecks {
		if m, _ := c.Details["mapped"].(string); m == "/mnt/d/Archives" {
			drive = c
		}
	}
	if drive.Name == "" {
		t.Fatalf("no DrvFs source check: %+v", sourceChecks)
	}
	if drvfs, _ := drive.Details["drvfs"].(bool); !drvfs {
		t.Fatalf("expected drvfs true: %+v", drive.Details)
	}
}

func TestDoctorRejectsWSLUNCSource(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	cfg := mustCfg(t, map[string]any{
		"source_dirs": []any{`\\wsl.localhost\Ubuntu\home\u\archives`},
	}, "/tmp/test-config.yaml")

	r := doctor.Run(e.opts(cfg))
	bad := checkByName(r, "source_dirs[0]")
	if bad.OK || bad.Severity != doctor.SeverityError {
		t.Fatalf("expected error for UNC WSL source: %+v", bad)
	}
	if !strings.Contains(bad.Message, "UNC WSL") {
		t.Fatalf("message=%q", bad.Message)
	}
	if r.OK {
		t.Fatal("report should hard-fail on source path error")
	}
}

func TestBuildSystemdDropinContainsSources(t *testing.T) {
	t.Parallel()
	cfg := mustCfg(t, map[string]any{
		"source_dirs":            []any{`D:\Archives`, "/var/lib/mount-wrapper/inbox"},
		"archives_dir":           "/var/lib/mount-wrapper/archives",
		"move_archives_to_linux": true,
	}, "")
	text := doctor.BuildSystemdDropin(cfg)
	if !strings.Contains(text, "[Service]") {
		t.Fatal("missing [Service]")
	}
	if !strings.Contains(text, "DeviceAllow=/dev/fuse rw") {
		t.Fatal("missing DeviceAllow")
	}
	if !strings.Contains(text, "/mnt/d") && !strings.Contains(text, "/mnt/d/Archives") {
		t.Fatalf("missing drive mapping: %s", text)
	}
	if !strings.Contains(text, "/var/lib/mount-wrapper/inbox") {
		t.Fatal("missing inbox")
	}
	if !strings.Contains(text, "ReadWritePaths=") {
		t.Fatal("missing ReadWritePaths")
	}
	if !strings.Contains(text, "archives") {
		t.Fatal("missing archives")
	}
	if !strings.Contains(text, "Generated by mount-wrapper doctor") {
		t.Fatal("missing generator comment")
	}
}

func TestFixSystemdWritesDropin(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
	e.dirs["/tmp"] = true

	tmp := t.TempDir()
	dropin := filepath.Join(tmp, "systemd", "sources.conf")
	cfg := mustCfg(t, map[string]any{
		"source_dirs": []any{"/tmp"},
		"mount_root":  filepath.Join(tmp, "m"),
	}, filepath.Join(tmp, "c.yaml"))

	opts := e.opts(cfg)
	opts.FixSystemd = true
	opts.DropinPath = dropin

	r := doctor.Run(opts)
	body, ok := e.writes[dropin]
	if !ok {
		t.Fatalf("dropin not written; writes=%v", e.writes)
	}
	if !strings.Contains(body, "Generated by mount-wrapper doctor") {
		t.Fatalf("body=%s", body)
	}
	if !strings.Contains(body, "/tmp") {
		t.Fatalf("body missing /tmp: %s", body)
	}
	fix := checkByName(r, "fix_systemd")
	if !fix.OK {
		t.Fatalf("fix_systemd: %+v", fix)
	}
	if len(r.FixesApplied) == 0 {
		t.Fatal("expected fixes_applied")
	}
}

func TestFixSystemdWithoutConfig(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	opts := e.opts(nil)
	opts.FixSystemd = true
	r := doctor.Run(opts)
	fix := checkByName(r, "fix_systemd")
	if fix.OK {
		t.Fatalf("expected fix_systemd fail: %+v", fix)
	}
}

func TestUserAllowOtherWindowsVisible(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.platform = "linux"
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
	e.fuseConfPath = "/etc/fuse.conf"
	e.files["/etc/fuse.conf"] = "# comment\n# user_allow_other\n"

	cfg := mustCfg(t, map[string]any{
		"windows_visible": true,
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "user_allow_other")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("expected warn: %+v", c)
	}

	// Enable and re-run
	e.files["/etc/fuse.conf"] = "user_allow_other\n"
	r = doctor.Run(e.opts(cfg))
	c = checkByName(r, "user_allow_other")
	if !c.OK {
		t.Fatalf("expected ok: %+v", c)
	}
}

func TestFuseMissingIsWarn(t *testing.T) {
	t.Parallel()
	e := newEnv()
	// no /dev/fuse
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	r := doctor.Run(e.opts(nil))
	c := checkByName(r, "fuse_device")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("fuse_device: %+v", c)
	}
	// Warn only → report still OK
	if !r.OK {
		// may fail for other reasons
		if HardFailOnlyErrors(r) {
			t.Fatalf("unexpected hard fail: %s", doctor.FormatText(r))
		}
	}
}

// HardFailOnlyErrors reimplements HardFail for test clarity when r.OK is false for other reasons.
func HardFailOnlyErrors(r *doctor.Report) bool {
	return doctor.HardFail(r.Checks)
}

func TestArchiveconverterEnabledMissing(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	cfg := mustCfg(t, map[string]any{
		"archiveconverter_enabled": true,
		"archiveconverter_bin":     "/no/such/archiveconverter",
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "archiveconverter")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("archiveconverter: %+v", c)
	}
}

func TestSevenZipEnabledMissing(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	cfg := mustCfg(t, map[string]any{
		"convert_7z_nonsolid": true,
		"convert_7z_bin":      "/no/such/7z",
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "sevenzip_bin")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("sevenzip_bin: %+v", c)
	}
}

func TestIndexLayoutDrvFs(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
	e.dirs["/mnt/d/indexes"] = true
	e.exists["/mnt/d/indexes"] = true
	e.writable["/mnt/d"] = true
	e.exists["/mnt/d"] = true
	e.dirs["/mnt/d"] = true

	// Load with allow flag so FromMap accepts DrvFs path, then flip it off
	// so doctor still warns about layout policy.
	cfg := mustCfg(t, map[string]any{
		"index_dir":              "/mnt/d/indexes",
		"allow_indexes_on_drvfs": true,
	}, "")
	cfg.AllowIndexesOnDrvfs = false

	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "index_layout")
	if c.OK {
		t.Fatalf("expected DrvFs index warn: %+v", c)
	}
}

func TestLowDiskWarn(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	mountRoot := "/var/lib/mount-wrapper/mounts"
	e.dirs[mountRoot] = true
	e.exists[mountRoot] = true
	e.writable[mountRoot] = true
	e.dirs["/var/lib/mount-wrapper/indexes"] = true
	e.exists["/var/lib/mount-wrapper/indexes"] = true
	e.writable["/var/lib/mount-wrapper/indexes"] = true
	e.dirs["/var/lib/mount-wrapper/overlays"] = true
	e.exists["/var/lib/mount-wrapper/overlays"] = true
	e.writable["/var/lib/mount-wrapper/overlays"] = true
	e.dirs["/run/mount-wrapper"] = true
	e.exists["/run/mount-wrapper"] = true
	e.writable["/run/mount-wrapper"] = true

	e.free[mountRoot] = 1024
	e.freeOK[mountRoot] = true
	e.free["/var/lib/mount-wrapper/indexes"] = 1 << 40
	e.freeOK["/var/lib/mount-wrapper/indexes"] = true
	e.free["/var/lib/mount-wrapper/overlays"] = 1 << 40
	e.freeOK["/var/lib/mount-wrapper/overlays"] = true

	cfg := mustCfg(t, map[string]any{
		"mount_root":     mountRoot,
		"min_free_bytes": 4096,
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "disk.mount_root")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("disk.mount_root: %+v", c)
	}
}

func TestServiceUserMissing(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	// no users
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	r := doctor.Run(e.opts(nil))
	c := checkByName(r, "service_user")
	if c.OK || c.Severity != doctor.SeverityWarn {
		t.Fatalf("service_user: %+v", c)
	}
}

func TestDarwinMessaging(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.platform = "darwin"
	e.exists["/Library/Filesystems/macfuse.fs"] = true
	e.which["umount"] = "/usr/bin/umount"
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	r := doctor.Run(e.opts(nil))
	su := checkByName(r, "service_user")
	if !su.OK || !strings.Contains(su.Message, "macOS") {
		t.Fatalf("service_user: %+v", su)
	}
	sd := checkByName(r, "systemd_pid1")
	if !sd.OK || !strings.Contains(sd.Message, "macOS") {
		t.Fatalf("systemd_pid1: %+v", sd)
	}
	if strings.Contains(sd.Message, "--foreground") {
		t.Fatalf("systemd_pid1 must not mention --foreground (serve has none): %s", sd.Message)
	}
	if !strings.Contains(sd.Message, "launchd") {
		t.Fatalf("systemd_pid1 should mention launchd: %s", sd.Message)
	}
	if launchd, _ := sd.Details["launchd"].(bool); !launchd {
		t.Fatalf("systemd_pid1 details.launchd: %+v", sd.Details)
	}
	ua := checkByName(r, "user_allow_other")
	if !ua.OK || !strings.Contains(ua.Message, "macOS") {
		t.Fatalf("user_allow_other: %+v", ua)
	}
}

func TestFormatTextAndJSON(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	cfg := mustCfg(t, map[string]any{"source_dirs": []any{"/tmp"}}, "/etc/mount-wrapper/config.yaml")
	e.dirs["/tmp"] = true
	for _, p := range []string{
		"/var/lib/mount-wrapper/mounts",
		"/var/lib/mount-wrapper/indexes",
		"/var/lib/mount-wrapper/overlays",
		"/run/mount-wrapper",
	} {
		e.dirs[p] = true
		e.exists[p] = true
		e.writable[p] = true
	}

	r := doctor.Run(e.opts(cfg))
	text := doctor.FormatText(r)
	if !strings.Contains(text, "mount-wrapper doctor:") {
		t.Fatalf("text header: %s", text)
	}
	if !strings.Contains(text, "config: /etc/mount-wrapper/config.yaml") {
		t.Fatalf("text config path: %s", text)
	}
	if !strings.Contains(text, "go_version") {
		t.Fatalf("text checks: %s", text)
	}

	js, err := doctor.FormatJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, js)
	}
	if _, ok := m["ok"].(bool); !ok {
		t.Fatalf("ok field: %v", m["ok"])
	}
	checks, ok := m["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("checks: %v", m["checks"])
	}
	// Report.ToMap shape
	tm := r.ToMap()
	if tm["ok"] != r.OK {
		t.Fatalf("ToMap ok mismatch")
	}
}

func TestHardFail(t *testing.T) {
	t.Parallel()
	if doctor.HardFail([]doctor.CheckResult{
		{OK: false, Severity: doctor.SeverityWarn},
	}) {
		t.Fatal("warn should not hard-fail")
	}
	if !doctor.HardFail([]doctor.CheckResult{
		{OK: false, Severity: doctor.SeverityError},
	}) {
		t.Fatal("error should hard-fail")
	}
}

func TestGoVersionAncientFails(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.goVersion = "go1.18.10"
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	r := doctor.Run(e.opts(nil))
	c := checkByName(r, "go_version")
	if c.OK || c.Severity != doctor.SeverityError {
		t.Fatalf("go_version: %+v", c)
	}
	if r.OK {
		t.Fatal("expected hard fail")
	}
}

func TestPeercredIncludesSocket(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
	for _, p := range []string{
		"/var/lib/mount-wrapper/mounts",
		"/var/lib/mount-wrapper/indexes",
		"/var/lib/mount-wrapper/overlays",
		"/run/mount-wrapper",
	} {
		e.dirs[p] = true
		e.exists[p] = true
		e.writable[p] = true
	}

	cfg := mustCfg(t, map[string]any{
		"control_socket": "/run/mount-wrapper/control.sock",
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "peercred")
	if !c.OK {
		t.Fatalf("peercred: %+v", c)
	}
	if sock, _ := c.Details["control_socket"].(string); sock != "/run/mount-wrapper/control.sock" {
		t.Fatalf("details=%v", c.Details)
	}
	if !strings.Contains(c.Message, "control.sock") {
		t.Fatalf("message=%q", c.Message)
	}
}

func TestPathNotDirectoryIsError(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	// mount_root exists as a file (not dir)
	e.exists["/tmp/not-a-dir"] = true
	// IsDir false
	e.dirs["/tmp"] = true
	e.exists["/tmp"] = true
	e.writable["/tmp"] = true
	// defaults for other paths
	for _, p := range []string{
		"/var/lib/mount-wrapper/indexes",
		"/var/lib/mount-wrapper/overlays",
		"/run/mount-wrapper",
	} {
		e.dirs[p] = true
		e.exists[p] = true
		e.writable[p] = true
	}

	cfg := mustCfg(t, map[string]any{
		"mount_root": "/tmp/not-a-dir",
	}, "")
	r := doctor.Run(e.opts(cfg))
	c := checkByName(r, "path.mount_root")
	if c.OK || c.Severity != doctor.SeverityError {
		t.Fatalf("path.mount_root: %+v", c)
	}
}

func TestWSLHostPlatformNote(t *testing.T) {
	t.Parallel()
	e := newEnv()
	e.isWSL = true
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/bin/fusermount3"
	e.users["mount-wrapper"] = true
	e.setExec("/bin/ratarmount-rs", "v1", "")
	e.which["ratarmount-rs"] = "/bin/ratarmount-rs"

	r := doctor.Run(e.opts(nil))
	c := checkByName(r, "host_platform")
	if !strings.Contains(c.Message, "WSL") {
		t.Fatalf("message=%q", c.Message)
	}
	if wsl, _ := c.Details["wsl"].(bool); !wsl {
		t.Fatalf("details=%v", c.Details)
	}
}

func TestFormatNilReport(t *testing.T) {
	t.Parallel()
	text := doctor.FormatText(nil)
	if !strings.Contains(text, "no report") {
		t.Fatalf("%q", text)
	}
	js, err := doctor.FormatJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"ok"`) {
		t.Fatalf("%s", js)
	}
}

func TestWebBindSecurity(t *testing.T) {
	t.Parallel()

	baseEnv := func() *testEnv {
		e := newEnv()
		e.exists["/dev/fuse"] = true
		e.which["fusermount3"] = "/bin/fusermount3"
		e.users["mount-wrapper"] = true
		e.setExec("/bin/ratarmount-rs", "v1", "")
		e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
		for _, p := range []string{
			"/var/lib/mount-wrapper/mounts",
			"/var/lib/mount-wrapper/indexes",
			"/var/lib/mount-wrapper/overlays",
			"/run/mount-wrapper",
		} {
			e.dirs[p] = true
			e.exists[p] = true
			e.writable[p] = true
		}
		return e
	}

	cases := []struct {
		name       string
		raw        map[string]any
		wantOK     bool
		wantSev    string
		wantSubstr string
		wantAbsent bool // no config → check not present
	}{
		{
			name: "disabled",
			raw: map[string]any{
				"web_enabled": false,
				"web_host":    "0.0.0.0",
				"web_token":   "",
			},
			wantOK:     true,
			wantSev:    doctor.SeverityInfo,
			wantSubstr: "web disabled",
		},
		{
			name: "loopback_no_token",
			raw: map[string]any{
				"web_enabled": true,
				"web_host":    "127.0.0.1",
				"web_token":   "",
			},
			wantOK:     true,
			wantSev:    doctor.SeverityInfo,
			wantSubstr: "loopback",
		},
		{
			name: "localhost_no_token",
			raw: map[string]any{
				"web_enabled": true,
				"web_host":    "localhost",
			},
			wantOK:     true,
			wantSev:    doctor.SeverityInfo,
			wantSubstr: "loopback",
		},
		{
			name: "v6_loopback_no_token",
			raw: map[string]any{
				"web_enabled": true,
				"web_host":    "::1",
			},
			wantOK:     true,
			wantSev:    doctor.SeverityInfo,
			wantSubstr: "loopback",
		},
		{
			name: "non_loopback_empty_token_warn",
			raw: map[string]any{
				"web_enabled": true,
				"web_host":    "0.0.0.0",
				"web_token":   "",
			},
			wantOK:     false,
			wantSev:    doctor.SeverityWarn,
			wantSubstr: "not loopback",
		},
		{
			name: "non_loopback_with_token_ok",
			raw: map[string]any{
				"web_enabled": true,
				"web_host":    "192.168.1.10",
				"web_token":   "s3cret",
			},
			wantOK:     true,
			wantSev:    doctor.SeverityInfo,
			wantSubstr: "web_token set",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := baseEnv()
			cfg := mustCfg(t, tc.raw, "")
			r := doctor.Run(e.opts(cfg))
			c := checkByName(r, "web_bind_security")
			if c.Name == "" {
				t.Fatal("missing web_bind_security check")
			}
			if c.OK != tc.wantOK || c.Severity != tc.wantSev {
				t.Fatalf("ok=%v sev=%q want ok=%v sev=%q; msg=%q",
					c.OK, c.Severity, tc.wantOK, tc.wantSev, c.Message)
			}
			if !strings.Contains(c.Message, tc.wantSubstr) {
				t.Fatalf("message %q missing %q", c.Message, tc.wantSubstr)
			}
			// Warn must not hard-fail the report by itself.
			if tc.wantSev == doctor.SeverityWarn && !r.OK && HardFailOnlyErrors(r) {
				t.Fatalf("warn should not hard-fail report: %s", doctor.FormatText(r))
			}
			if tc.wantSev == doctor.SeverityWarn && !r.OK {
				// other errors may set OK=false; ensure this check alone is not error
				if c.Severity == doctor.SeverityError {
					t.Fatal("web_bind_security must not be error severity")
				}
			}
			if tc.wantSev == doctor.SeverityWarn {
				// Explicit: report OK remains true for warn-only failure.
				// Isolate: ensure no error-severity checks.
				if doctor.HardFail(r.Checks) {
					t.Fatalf("hard fail unexpected: %s", doctor.FormatText(r))
				}
				if !r.OK {
					t.Fatalf("report OK should stay true for warn-only web_bind_security")
				}
			}
		})
	}

	t.Run("no_config_skipped", func(t *testing.T) {
		t.Parallel()
		e := baseEnv()
		r := doctor.Run(e.opts(nil))
		if c := checkByName(r, "web_bind_security"); c.Name != "" {
			t.Fatalf("unexpected web_bind_security without config: %+v", c)
		}
	})
}

func TestConvertCacheDir(t *testing.T) {
	t.Parallel()

	baseEnv := func() *testEnv {
		e := newEnv()
		e.exists["/dev/fuse"] = true
		e.which["fusermount3"] = "/bin/fusermount3"
		e.users["mount-wrapper"] = true
		e.setExec("/bin/ratarmount-rs", "v1", "")
		e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
		// 7z present so sevenzip_bin does not also warn in convert-on cases.
		e.setExec("/usr/bin/7z", "7z 23.0", "")
		e.which["7z"] = "/usr/bin/7z"
		for _, p := range []string{
			"/var/lib/mount-wrapper/mounts",
			"/var/lib/mount-wrapper/indexes",
			"/var/lib/mount-wrapper/overlays",
			"/run/mount-wrapper",
		} {
			e.dirs[p] = true
			e.exists[p] = true
			e.writable[p] = true
		}
		return e
	}

	cache := "/var/lib/mount-wrapper/nonsolid-cache"
	acOut := "/var/lib/mount-wrapper/converted"

	cases := []struct {
		name              string
		raw               map[string]any
		setup             func(e *testEnv)
		wantConvert       bool // convert_cache_dir present
		wantConvertOK     bool
		wantConvertSev    string
		wantConvertSubstr string
		wantAC            bool // path.archiveconverter_output_dir present
		wantACOK          bool
		wantACSev         string
	}{
		{
			name: "convert_off_skips",
			raw: map[string]any{
				"convert_7z_nonsolid": false,
				"convert_zip_to_7z":   false,
			},
			wantConvert: false,
			wantAC:      false,
		},
		{
			name: "nonsolid_cache_exists",
			raw: map[string]any{
				"convert_7z_nonsolid": true,
				"convert_7z_cache_dir": cache,
			},
			setup: func(e *testEnv) {
				e.dirs[cache] = true
				e.exists[cache] = true
				e.writable[cache] = true
			},
			wantConvert:       true,
			wantConvertOK:     true,
			wantConvertSev:    doctor.SeverityInfo,
			wantConvertSubstr: "exists",
		},
		{
			name: "zip_to_7z_parent_writable",
			raw: map[string]any{
				"convert_zip_to_7z":    true,
				"convert_7z_cache_dir": cache,
			},
			setup: func(e *testEnv) {
				// cache missing; parent writable → info
				parent := "/var/lib/mount-wrapper"
				e.dirs[parent] = true
				e.exists[parent] = true
				e.writable[parent] = true
			},
			wantConvert:       true,
			wantConvertOK:     true,
			wantConvertSev:    doctor.SeverityInfo,
			wantConvertSubstr: "parent writable",
		},
		{
			name: "nonsolid_parent_not_writable",
			raw: map[string]any{
				"convert_7z_nonsolid":  true,
				"convert_7z_cache_dir": cache,
			},
			setup: func(e *testEnv) {
				parent := "/var/lib/mount-wrapper"
				e.dirs[parent] = true
				e.exists[parent] = true
				e.writable[parent] = false
			},
			wantConvert:       true,
			wantConvertOK:     false,
			wantConvertSev:    doctor.SeverityWarn,
			wantConvertSubstr: "parent not writable",
		},
		{
			name: "archiveconverter_output_missing_parent",
			raw: map[string]any{
				// convert_zip_to_7z defaults true — disable so only AC path is probed.
				"convert_7z_nonsolid":         false,
				"convert_zip_to_7z":           false,
				"archiveconverter_enabled":    true,
				"archiveconverter_bin":        "/usr/bin/archiveconverter",
				"archiveconverter_output_dir": acOut,
			},
			setup: func(e *testEnv) {
				e.setExec("/usr/bin/archiveconverter", "ac 1", "")
				// neither out nor parent writable/present beyond defaults
			},
			wantConvert: false,
			wantAC:      true,
			wantACOK:    false,
			wantACSev:   doctor.SeverityWarn,
		},
		{
			name: "archiveconverter_output_ok",
			raw: map[string]any{
				"convert_7z_nonsolid":         false,
				"convert_zip_to_7z":           false,
				"archiveconverter_enabled":    true,
				"archiveconverter_bin":        "/usr/bin/archiveconverter",
				"archiveconverter_output_dir": acOut,
			},
			setup: func(e *testEnv) {
				e.setExec("/usr/bin/archiveconverter", "ac 1", "")
				e.dirs[acOut] = true
				e.exists[acOut] = true
				e.writable[acOut] = true
			},
			wantConvert: false,
			wantAC:      true,
			wantACOK:    true,
			wantACSev:   doctor.SeverityInfo,
		},
		{
			name: "both_features",
			raw: map[string]any{
				"convert_7z_nonsolid":         true,
				"convert_zip_to_7z":           true,
				"convert_7z_cache_dir":        cache,
				"archiveconverter_enabled":    true,
				"archiveconverter_bin":        "/usr/bin/archiveconverter",
				"archiveconverter_output_dir": acOut,
			},
			setup: func(e *testEnv) {
				e.setExec("/usr/bin/archiveconverter", "ac 1", "")
				e.dirs[cache] = true
				e.exists[cache] = true
				e.writable[cache] = true
				e.dirs[acOut] = true
				e.exists[acOut] = true
				e.writable[acOut] = true
			},
			wantConvert:       true,
			wantConvertOK:     true,
			wantConvertSev:    doctor.SeverityInfo,
			wantConvertSubstr: "exists",
			wantAC:            true,
			wantACOK:          true,
			wantACSev:         doctor.SeverityInfo,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := baseEnv()
			if tc.setup != nil {
				tc.setup(e)
			}
			cfg := mustCfg(t, tc.raw, "")
			r := doctor.Run(e.opts(cfg))

			cc := checkByName(r, "convert_cache_dir")
			if tc.wantConvert {
				if cc.Name == "" {
					t.Fatal("missing convert_cache_dir")
				}
				if cc.OK != tc.wantConvertOK || cc.Severity != tc.wantConvertSev {
					t.Fatalf("convert_cache_dir ok=%v sev=%q want %v/%q msg=%q",
						cc.OK, cc.Severity, tc.wantConvertOK, tc.wantConvertSev, cc.Message)
				}
				if tc.wantConvertSubstr != "" && !strings.Contains(cc.Message, tc.wantConvertSubstr) {
					t.Fatalf("convert message %q missing %q", cc.Message, tc.wantConvertSubstr)
				}
			} else if cc.Name != "" {
				t.Fatalf("unexpected convert_cache_dir: %+v", cc)
			}

			ac := checkByName(r, "path.archiveconverter_output_dir")
			if tc.wantAC {
				if ac.Name == "" {
					t.Fatal("missing path.archiveconverter_output_dir")
				}
				if ac.OK != tc.wantACOK || ac.Severity != tc.wantACSev {
					t.Fatalf("ac output ok=%v sev=%q want %v/%q msg=%q",
						ac.OK, ac.Severity, tc.wantACOK, tc.wantACSev, ac.Message)
				}
			} else if ac.Name != "" {
				t.Fatalf("unexpected ac output check: %+v", ac)
			}
		})
	}
}

func TestControlSocketPathLength(t *testing.T) {
	t.Parallel()

	baseEnv := func(plat string) *testEnv {
		e := newEnv()
		e.platform = plat
		if plat == "darwin" {
			e.exists["/Library/Filesystems/macfuse.fs"] = true
			e.which["umount"] = "/usr/bin/umount"
		} else {
			e.exists["/dev/fuse"] = true
			e.which["fusermount3"] = "/bin/fusermount3"
			e.users["mount-wrapper"] = true
		}
		e.setExec("/bin/ratarmount-rs", "v1", "")
		e.which["ratarmount-rs"] = "/bin/ratarmount-rs"
		for _, p := range []string{
			"/var/lib/mount-wrapper/mounts",
			"/var/lib/mount-wrapper/indexes",
			"/var/lib/mount-wrapper/overlays",
			"/run/mount-wrapper",
			"/tmp",
		} {
			e.dirs[p] = true
			e.exists[p] = true
			e.writable[p] = true
		}
		return e
	}

	// Path length 110 (> darwinSunPathWarnLen 100).
	longSock := "/tmp/" + strings.Repeat("a", 100) + ".sock"

	cases := []struct {
		name       string
		plat       string
		socket     string
		wantPresent bool
		wantOK     bool
		wantSev    string
		wantSubstr string
	}{
		{
			name:        "linux_skipped",
			plat:        "linux",
			socket:      longSock,
			wantPresent: false,
		},
		{
			name:        "darwin_short_ok",
			plat:        "darwin",
			socket:      "/tmp/mw-control.sock",
			wantPresent: true,
			wantOK:      true,
			wantSev:     doctor.SeverityInfo,
			wantSubstr:  "within macOS sun_path",
		},
		{
			name:        "darwin_long_warn",
			plat:        "darwin",
			socket:      longSock,
			wantPresent: true,
			wantOK:      false,
			wantSev:     doctor.SeverityWarn,
			wantSubstr:  "sun_path",
		},
		{
			name:        "darwin_empty_info",
			plat:        "darwin",
			socket:      "",
			wantPresent: true,
			wantOK:      true,
			wantSev:     doctor.SeverityInfo,
			wantSubstr:  "empty",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := baseEnv(tc.plat)
			raw := map[string]any{}
			if tc.socket != "" {
				raw["control_socket"] = tc.socket
				// Parent of long sock for path.control_socket_dir
				parent := filepath.Dir(tc.socket)
				e.dirs[parent] = true
				e.exists[parent] = true
				e.writable[parent] = true
			}
			cfg := mustCfg(t, raw, "")
			// Empty socket in raw leaves default control socket from FromMap —
			// force empty when testing empty case.
			if tc.socket == "" {
				cfg.ControlSocket = ""
			}
			r := doctor.Run(e.opts(cfg))
			c := checkByName(r, "control_socket_path_length")
			if !tc.wantPresent {
				if c.Name != "" {
					t.Fatalf("unexpected check on %s: %+v", tc.plat, c)
				}
				return
			}
			if c.Name == "" {
				t.Fatal("missing control_socket_path_length")
			}
			if c.OK != tc.wantOK || c.Severity != tc.wantSev {
				t.Fatalf("ok=%v sev=%q want %v/%q msg=%q details=%v",
					c.OK, c.Severity, tc.wantOK, tc.wantSev, c.Message, c.Details)
			}
			if tc.wantSubstr != "" && !strings.Contains(c.Message, tc.wantSubstr) {
				t.Fatalf("message %q missing %q", c.Message, tc.wantSubstr)
			}
			if tc.wantSev == doctor.SeverityWarn {
				if doctor.HardFail(r.Checks) {
					t.Fatalf("warn should not hard-fail: %s", doctor.FormatText(r))
				}
			}
		})
	}
}
