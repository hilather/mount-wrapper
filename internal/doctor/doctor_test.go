package doctor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
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
	// modes overrides DirMode for existing dirs (default 0o755 when in dirs).
	modes        map[string]os.FileMode
	users        map[string]bool
	files        map[string]string
	binOut       map[string]string // key: "path|--version" or "path|--help"
	binErr       map[string]error
	// controlReq injects control socket status probes (nil = production dial).
	controlReq   doctor.ControlRequestFunc
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
		modes:     map[string]os.FileMode{},
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
		DirMode: func(path string) (os.FileMode, bool) {
			if m, ok := e.modes[path]; ok {
				return m, true
			}
			if e.dirs[path] {
				return 0o755, true
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
		ControlRequest: e.controlReq,
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

// inventoryBaseEnv is a healthy baseline for inventory tests (Linux, FUSE, bins).
func inventoryBaseEnv() *testEnv {
	e := newEnv()
	e.exists["/dev/fuse"] = true
	e.which["fusermount3"] = "/usr/bin/fusermount3"
	e.users["mount-wrapper"] = true
	rm := "/usr/local/bin/ratarmount-rs"
	e.which["ratarmount-rs"] = rm
	e.setExec(rm, "ratarmount-rs 0.1.0", "Usage: -f --foreground")
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

func orderedNames(r *doctor.Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, c.Name)
	}
	return out
}

// TestDoctorCheckInventory freezes check-name inventory, config/platform
// gating, and the severity contract for newer probes (warn, not hard-fail).
func TestDoctorCheckInventory(t *testing.T) {
	t.Parallel()

	// Long Darwin socket (>100 bytes warn threshold).
	longSock := "/tmp/" + strings.Repeat("x", 100) + ".sock"

	cases := []struct {
		name string
		// setup mutates env and returns optional config (nil = no config).
		setup func(t *testing.T, e *testEnv) *config.Config
		// required: every name must appear at least once.
		required []string
		// requiredPrefix: at least one check name with this prefix.
		requiredPrefix []string
		// forbidden: must not appear.
		forbidden []string
		// wantOrderPrefix: report names must start with this sequence.
		wantOrderPrefix []string
		// check: optional extra assertions on a named check + report.
		check func(t *testing.T, r *doctor.Report)
		// wantReportOK: when non-nil, assert r.OK.
		wantReportOK *bool
	}{
		{
			name: "always_on_no_config",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return nil
			},
			required:        append([]string(nil), doctor.CoreCheckNames...),
			wantOrderPrefix: append([]string(nil), doctor.CoreCheckNames...),
			forbidden: []string{
				doctor.CheckNameWebBindSecurity,
				doctor.CheckNameConvertCacheDir,
				doctor.CheckNameArchiveconverterOutputDir,
				doctor.CheckNameControlSocketPathLength,
				doctor.CheckNameControlSocketLive,
				doctor.CheckNameWindowsVisibleParentOX,
				doctor.CheckNameConfig,
				doctor.CheckNameIndexLayout,
				doctor.CheckNameFixSystemd,
			},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				// No path.* / disk.* / source_dirs without config.
				for _, c := range r.Checks {
					if strings.HasPrefix(c.Name, doctor.PathCheckPrefix) ||
						strings.HasPrefix(c.Name, doctor.DiskCheckPrefix) ||
						strings.HasPrefix(c.Name, doctor.SourceDirsPrefix) {
						t.Errorf("unexpected config-dependent check %q without config", c.Name)
					}
				}
				// Core order equals full report when no config.
				got := orderedNames(r)
				if len(got) != len(doctor.CoreCheckNames) {
					t.Fatalf("check count=%d want %d: %v", len(got), len(doctor.CoreCheckNames), got)
				}
			},
		},
		{
			name: "config_baseline_linux_convert_off",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return mustCfg(t, map[string]any{
					"source_dirs":             []any{"/tmp"},
					"convert_7z_nonsolid":     false,
					"convert_zip_to_7z":       false,
					"archiveconverter_enabled": false,
					"web_enabled":             false,
					"control_socket":          "/run/mount-wrapper/control.sock",
				}, "/tmp/inventory-config.yaml")
			},
			required: append(append([]string(nil), doctor.CoreCheckNames...),
				doctor.CheckNameWebBindSecurity,
				doctor.CheckNameWindowsVisibleParentOX,
				doctor.CheckNameIndexLayout,
				doctor.CheckNameControlSocketLive,
				doctor.CheckNameConfig,
				"path.mount_root",
				"path.index_dir",
				"path.overlay_dir",
				"path.control_socket_dir",
				"source_dirs[0]",
			),
			requiredPrefix: []string{doctor.DiskCheckPrefix},
			wantOrderPrefix: append([]string(nil), doctor.CoreCheckNames...),
			forbidden: []string{
				doctor.CheckNameConvertCacheDir,
				doctor.CheckNameArchiveconverterOutputDir,
				doctor.CheckNameControlSocketPathLength, // linux
				doctor.CheckNameFixSystemd,
			},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
				// Default windows_visible true; inventory dirs have o+x via DirMode.
				if !c.OK || c.Severity != doctor.SeverityInfo {
					t.Fatalf("windows_visible_parent_ox baseline: %+v", c)
				}
				// Socket path missing → warn offline (not hard-fail).
				live := checkByName(r, doctor.CheckNameControlSocketLive)
				if live.OK || live.Severity != doctor.SeverityWarn {
					t.Fatalf("control_socket_live offline baseline: %+v", live)
				}
				if doctor.HardFail(r.Checks) {
					t.Fatalf("control_socket_live warn must not hard-fail")
				}
			},
		},
		{
			name: "control_socket_live_missing_path_warn",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				// Sock path deliberately absent from e.exists / e.dirs.
				return mustCfg(t, map[string]any{
					"control_socket":      "/run/mount-wrapper/missing.sock",
					"convert_7z_nonsolid": false,
					"convert_zip_to_7z":   false,
				}, "")
			},
			required:     []string{doctor.CheckNameControlSocketLive},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameControlSocketLive)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("want warn missing sock: %+v", c)
				}
				if !strings.Contains(c.Message, "not found") {
					t.Fatalf("message=%q", c.Message)
				}
				if doctor.HardFail(r.Checks) {
					t.Fatal("missing sock must not hard-fail")
				}
			},
		},
		{
			name: "control_socket_live_dial_fail_warn",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				sock := "/run/mount-wrapper/control.sock"
				e.exists[sock] = true
				e.controlReq = func(socketPath, op string) (map[string]any, error) {
					if socketPath != sock || op != "status" {
						t.Fatalf("unexpected probe path=%q op=%q", socketPath, op)
					}
					return nil, &control.Error{
						Message: "cannot connect to control socket " + sock + ": connection refused",
						Code:    "UNAVAILABLE",
					}
				}
				return mustCfg(t, map[string]any{
					"control_socket": sock,
				}, "")
			},
			required:     []string{doctor.CheckNameControlSocketLive},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameControlSocketLive)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("want dial-fail warn: %+v", c)
				}
				if !strings.Contains(c.Message, "not reachable") {
					t.Fatalf("message=%q", c.Message)
				}
				if code, _ := c.Details["code"].(string); code != "UNAVAILABLE" {
					t.Fatalf("details.code=%v", c.Details["code"])
				}
				if doctor.HardFail(r.Checks) {
					t.Fatal("dial fail must not hard-fail")
				}
			},
		},
		{
			name: "control_socket_live_auth_denied_warn",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				sock := "/run/mount-wrapper/control.sock"
				e.exists[sock] = true
				e.controlReq = func(socketPath, op string) (map[string]any, error) {
					return map[string]any{
						"ok":    false,
						"code":  "PERMISSION_DENIED",
						"error": "permission denied: user must be root or in group mount-wrapper (uid 1000 is not root or in group mount-wrapper)",
					}, nil
				}
				return mustCfg(t, map[string]any{
					"control_socket": sock,
				}, "")
			},
			required:     []string{doctor.CheckNameControlSocketLive},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameControlSocketLive)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("want auth warn: %+v", c)
				}
				if !strings.Contains(c.Message, "auth denied") {
					t.Fatalf("message missing auth denied: %q", c.Message)
				}
				if !strings.Contains(c.Message, "mount-wrapper") {
					t.Fatalf("message missing group hint: %q", c.Message)
				}
				if g, _ := c.Details["auth_group"].(string); g != "mount-wrapper" {
					t.Fatalf("auth_group=%v", c.Details["auth_group"])
				}
				if doctor.HardFail(r.Checks) {
					t.Fatal("auth denied must not hard-fail")
				}
			},
		},
		{
			name: "control_socket_live_ok_with_version",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				sock := "/run/mount-wrapper/control.sock"
				e.exists[sock] = true
				e.controlReq = func(socketPath, op string) (map[string]any, error) {
					return map[string]any{
						"ok": true,
						"data": map[string]any{
							"version": "0.1.5-test",
							"pid":     float64(4242),
						},
					}, nil
				}
				return mustCfg(t, map[string]any{
					"control_socket": sock,
				}, "")
			},
			required:     []string{doctor.CheckNameControlSocketLive},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameControlSocketLive)
				if !c.OK || c.Severity != doctor.SeverityInfo {
					t.Fatalf("want info reachable: %+v", c)
				}
				if !strings.Contains(c.Message, "0.1.5-test") {
					t.Fatalf("message missing version: %q", c.Message)
				}
				if v, _ := c.Details["version"].(string); v != "0.1.5-test" {
					t.Fatalf("details.version=%v", c.Details["version"])
				}
				if pid, _ := c.Details["pid"].(int); pid != 4242 {
					t.Fatalf("details.pid=%v", c.Details["pid"])
				}
			},
		},
		{
			name: "control_socket_empty_skips_live",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				// Validated configs always have a non-empty control_socket; doctor
				// still skips the live probe when the field is cleared (defensive).
				cfg := mustCfg(t, map[string]any{
					"convert_7z_nonsolid": false,
					"convert_zip_to_7z":   false,
				}, "")
				cfg.ControlSocket = ""
				return cfg
			},
			forbidden: []string{doctor.CheckNameControlSocketLive},
		},
		{
			name: "windows_visible_parent_ox_missing_bit_warn",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				// mount_root and a parent lack o+x (0700).
				e.modes["/var/lib/mount-wrapper/mounts"] = 0o700
				e.modes["/var/lib/mount-wrapper"] = 0o700
				e.dirs["/var/lib/mount-wrapper"] = true
				e.exists["/var/lib/mount-wrapper"] = true
				return mustCfg(t, map[string]any{
					"windows_visible": true,
					"mount_root":      "/var/lib/mount-wrapper/mounts",
					"convert_7z_nonsolid": false,
					"convert_zip_to_7z":   false,
				}, "")
			},
			required:     []string{doctor.CheckNameWindowsVisibleParentOX},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("want warn: %+v", c)
				}
				if !strings.Contains(c.Message, "chmod o+x") {
					t.Fatalf("message missing fix hint: %q", c.Message)
				}
				hint, _ := c.Details["fix_hint"].(string)
				if !strings.Contains(hint, "chmod o+x") {
					t.Fatalf("details.fix_hint=%q", hint)
				}
				if doctor.HardFail(r.Checks) {
					t.Fatalf("o+x warn must not hard-fail: %s", doctor.FormatText(r))
				}
			},
		},
		{
			name: "windows_visible_false_parent_ox_info",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return mustCfg(t, map[string]any{
					"windows_visible": false,
					"mount_root":      "/var/lib/mount-wrapper/mounts",
				}, "")
			},
			required: []string{doctor.CheckNameWindowsVisibleParentOX},
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
				if !c.OK || c.Severity != doctor.SeverityInfo {
					t.Fatalf("want info when windows_visible=false: %+v", c)
				}
				if !strings.Contains(c.Message, "not required") {
					t.Fatalf("message=%q", c.Message)
				}
			},
		},
		{
			name: "darwin_windows_visible_parent_ox_info",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				e.platform = "darwin"
				e.exists["/Library/Filesystems/macfuse.fs"] = true
				e.which["umount"] = "/usr/bin/umount"
				return mustCfg(t, map[string]any{
					"windows_visible": true,
					"mount_root":      "/var/lib/mount-wrapper/mounts",
				}, "")
			},
			required: []string{doctor.CheckNameWindowsVisibleParentOX},
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
				if !c.OK || c.Severity != doctor.SeverityInfo {
					t.Fatalf("darwin want info: %+v", c)
				}
				if !strings.Contains(c.Message, "macOS") {
					t.Fatalf("message=%q", c.Message)
				}
			},
		},
		{
			name: "web_non_loopback_empty_token_warn_report_ok",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return mustCfg(t, map[string]any{
					"web_enabled":         true,
					"web_host":            "0.0.0.0",
					"web_token":           "",
					"convert_7z_nonsolid": false,
					"convert_zip_to_7z":   false,
				}, "")
			},
			required: []string{doctor.CheckNameWebBindSecurity},
			forbidden: []string{
				doctor.CheckNameConvertCacheDir,
			},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWebBindSecurity)
				if c.Name == "" {
					t.Fatal("missing web_bind_security")
				}
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("web_bind_security ok=%v sev=%q want ok=false sev=warn; msg=%q",
						c.OK, c.Severity, c.Message)
				}
				if doctor.HardFail(r.Checks) {
					t.Fatalf("web_bind_security must not hard-fail: %s", doctor.FormatText(r))
				}
			},
		},
		{
			name: "convert_off_skips_convert_cache_dir",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return mustCfg(t, map[string]any{
					"convert_7z_nonsolid": false,
					"convert_zip_to_7z":   false,
				}, "")
			},
			forbidden: []string{doctor.CheckNameConvertCacheDir},
			required:  []string{doctor.CheckNameWebBindSecurity},
		},
		{
			name: "convert_on_emits_convert_cache_dir",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				cache := "/var/lib/mount-wrapper/nonsolid-cache"
				e.dirs[cache] = true
				e.exists[cache] = true
				e.writable[cache] = true
				e.setExec("/usr/bin/7z", "7z 23.0", "")
				e.which["7z"] = "/usr/bin/7z"
				return mustCfg(t, map[string]any{
					"convert_7z_nonsolid":  true,
					"convert_7z_cache_dir": cache,
				}, "")
			},
			required: []string{doctor.CheckNameConvertCacheDir},
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameConvertCacheDir)
				if !c.OK || c.Severity != doctor.SeverityInfo {
					t.Fatalf("convert_cache_dir: %+v", c)
				}
			},
		},
		{
			name: "archiveconverter_output_dir_gated",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				out := "/var/lib/mount-wrapper/converted"
				e.dirs[out] = true
				e.exists[out] = true
				e.writable[out] = true
				e.setExec("/usr/bin/archiveconverter", "ac 1", "")
				return mustCfg(t, map[string]any{
					"convert_7z_nonsolid":         false,
					"convert_zip_to_7z":           false,
					"archiveconverter_enabled":    true,
					"archiveconverter_bin":        "/usr/bin/archiveconverter",
					"archiveconverter_output_dir": out,
				}, "")
			},
			required:  []string{doctor.CheckNameArchiveconverterOutputDir},
			forbidden: []string{doctor.CheckNameConvertCacheDir},
		},
		{
			name: "darwin_long_socket_warn_not_hard_fail",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				e.platform = "darwin"
				e.exists["/Library/Filesystems/macfuse.fs"] = true
				e.which["umount"] = "/usr/bin/umount"
				// no service user lookup on darwin messaging path
				parent := filepath.Dir(longSock)
				e.dirs[parent] = true
				e.exists[parent] = true
				e.writable[parent] = true
				return mustCfg(t, map[string]any{
					"control_socket": longSock,
				}, "")
			},
			required: []string{doctor.CheckNameControlSocketPathLength},
			wantReportOK: boolPtr(true),
			check: func(t *testing.T, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameControlSocketPathLength)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("control_socket_path_length ok=%v sev=%q msg=%q",
						c.OK, c.Severity, c.Message)
				}
				if doctor.HardFail(r.Checks) {
					t.Fatalf("long socket warn must not hard-fail: %s", doctor.FormatText(r))
				}
			},
		},
		{
			name: "linux_skips_control_socket_path_length",
			setup: func(t *testing.T, e *testEnv) *config.Config {
				t.Helper()
				return mustCfg(t, map[string]any{
					"control_socket": longSock,
				}, "")
			},
			forbidden: []string{doctor.CheckNameControlSocketPathLength},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := inventoryBaseEnv()
			cfg := tc.setup(t, e)
			r := doctor.Run(e.opts(cfg))
			if r == nil {
				t.Fatal("nil report")
			}
			n := names(r)
			got := orderedNames(r)

			for _, want := range tc.required {
				if _, ok := n[want]; !ok {
					t.Errorf("missing required check %q; have %v", want, got)
				}
			}
			for _, pref := range tc.requiredPrefix {
				found := false
				for name := range n {
					if strings.HasPrefix(name, pref) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing any check with prefix %q; have %v", pref, got)
				}
			}
			for _, ban := range tc.forbidden {
				if _, ok := n[ban]; ok {
					t.Errorf("forbidden check %q present; have %v", ban, got)
				}
			}
			if len(tc.wantOrderPrefix) > 0 {
				if len(got) < len(tc.wantOrderPrefix) {
					t.Fatalf("got %d checks, want at least %d prefix: %v",
						len(got), len(tc.wantOrderPrefix), got)
				}
				for i, want := range tc.wantOrderPrefix {
					if got[i] != want {
						t.Fatalf("order[%d]=%q want %q; full=%v", i, got[i], want, got)
					}
				}
			}
			if tc.wantReportOK != nil && r.OK != *tc.wantReportOK {
				t.Fatalf("report OK=%v want %v:\n%s", r.OK, *tc.wantReportOK, doctor.FormatText(r))
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func TestDoctorWithoutConfig(t *testing.T) {
	t.Parallel()
	e := inventoryBaseEnv()

	r := doctor.Run(e.opts(nil))
	if r == nil {
		t.Fatal("nil report")
	}
	n := names(r)
	for _, want := range doctor.CoreCheckNames {
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
	// Default layout under /var/lib/mount-wrapper is covered by the base RW path;
	// archives_dir is not re-listed when nested under that parent.
	if !strings.Contains(text, "/var/lib/mount-wrapper") {
		t.Fatal("missing packaged data root")
	}
	rwLine := readWritePathsLine(text)
	if strings.Contains(rwLine, "/var/lib/mount-wrapper/archives") {
		t.Fatalf("default archives_dir should be covered by parent, got: %s", rwLine)
	}
	// Defaults must not emit separate mount/index/overlay/converted entries.
	for _, p := range []string{
		"/var/lib/mount-wrapper/mounts",
		"/var/lib/mount-wrapper/indexes",
		"/var/lib/mount-wrapper/overlays",
		"/var/lib/mount-wrapper/converted",
	} {
		if strings.Contains(rwLine, p) {
			t.Fatalf("default path %s should be covered by parent: %s", p, rwLine)
		}
	}
	if !strings.Contains(text, "Generated by mount-wrapper doctor") {
		t.Fatal("missing generator comment")
	}
	// Source dirs are read-only, not RW.
	roLine := readOnlyPathsLine(text)
	if !strings.Contains(roLine, "/var/lib/mount-wrapper/inbox") {
		t.Fatalf("inbox should be ReadOnlyPaths: %s", roLine)
	}
	if strings.Contains(rwLine, "/var/lib/mount-wrapper/inbox") {
		t.Fatalf("inbox must not be ReadWritePaths: %s", rwLine)
	}
}

func TestBuildSystemdDropinCustomDataPaths(t *testing.T) {
	t.Parallel()
	cfg := mustCfg(t, map[string]any{
		"source_dirs":                 []any{"/data/foo/sources"},
		"archives_dir":                "/data/foo/archives",
		"move_archives_to_linux":      true,
		"mount_root":                  "/data/foo/mounts",
		"index_dir":                   "/data/foo/indexes",
		"overlay_dir":                 "/data/foo/overlays",
		"convert_7z_cache_dir":        "/data/foo/7z-cache",
		"archiveconverter_output_dir": "/data/foo/converted",
		// state_db must sit with other data when not under default FHS root
		"state_db": "/data/foo/state.db",
	}, "")
	text := doctor.BuildSystemdDropin(cfg)
	roLine := readOnlyPathsLine(text)
	rwLine := readWritePathsLine(text)

	if !strings.Contains(roLine, "/data/foo/sources") {
		t.Fatalf("source_dirs should be RO: %s", roLine)
	}
	if strings.Contains(rwLine, "/data/foo/sources") {
		t.Fatalf("source_dirs must not be RW: %s", rwLine)
	}

	wantRW := []string{
		"/var/lib/mount-wrapper",
		"/var/log/mount-wrapper",
		"/run/mount-wrapper",
		"/data/foo/archives",
		"/data/foo/mounts",
		"/data/foo/indexes",
		"/data/foo/overlays",
		"/data/foo/7z-cache",
		"/data/foo/converted",
	}
	for _, p := range wantRW {
		if !strings.Contains(rwLine, p) {
			t.Fatalf("missing ReadWritePaths entry %s in: %s", p, rwLine)
		}
	}

	// Deduped: each path once.
	fields := strings.Fields(strings.TrimPrefix(rwLine, "ReadWritePaths="))
	seen := make(map[string]int, len(fields))
	for _, f := range fields {
		seen[f]++
		if seen[f] > 1 {
			t.Fatalf("duplicate path %q in ReadWritePaths: %s", f, rwLine)
		}
	}
}

func TestBuildSystemdDropinCustomPathsCoveredByParent(t *testing.T) {
	t.Parallel()
	// When archives_dir and data dirs share a custom parent that is already listed
	// (here via archives_dir first), children of the same tree are still listed
	// individually — only the packaged bases act as broad covers. Sibling custom
	// paths that equal an earlier entry are skipped.
	cfg := mustCfg(t, map[string]any{
		"source_dirs":                 []any{"/opt/inbox"},
		"archives_dir":                "/data/stage",
		"move_archives_to_linux":      true,
		"mount_root":                  "/data/stage", // same as archives_dir → dedupe
		"index_dir":                   "/data/indexes",
		"overlay_dir":                 "/data/overlays",
		"convert_7z_cache_dir":        "", // empty → omit
		"archiveconverter_output_dir": "/data/converted",
		"state_db":                    "/data/state.db",
	}, "")
	text := doctor.BuildSystemdDropin(cfg)
	rwLine := readWritePathsLine(text)
	fields := strings.Fields(strings.TrimPrefix(rwLine, "ReadWritePaths="))
	count := 0
	for _, f := range fields {
		if f == "/data/stage" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected /data/stage once (archives_dir + mount_root dedupe), got %d in %s", count, rwLine)
	}
	for _, p := range []string{"/data/indexes", "/data/overlays", "/data/converted"} {
		if !strings.Contains(rwLine, p) {
			t.Fatalf("missing %s: %s", p, rwLine)
		}
	}
	// Empty convert_7z_cache_dir must not emit a blank token.
	for _, f := range fields {
		if f == "" {
			t.Fatalf("empty RW path token: %s", rwLine)
		}
	}
}

func readWritePathsLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "ReadWritePaths=") {
			return line
		}
	}
	return ""
}

func readOnlyPathsLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "ReadOnlyPaths=") {
			return line
		}
	}
	return ""
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

func TestFixSystemdDryRunNoWrite(t *testing.T) {
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
	opts.DryRun = true
	opts.DropinPath = dropin

	wantContent := doctor.BuildSystemdDropin(cfg)
	r := doctor.Run(opts)

	if len(e.writes) != 0 {
		t.Fatalf("dry-run wrote files: %v", e.writes)
	}
	if len(e.mkdirs) != 0 {
		t.Fatalf("dry-run created dirs: %v", e.mkdirs)
	}
	if _, err := os.Stat(dropin); !os.IsNotExist(err) {
		t.Fatalf("dropin path should not exist on dry-run: err=%v", err)
	}

	fix := checkByName(r, "fix_systemd")
	if !fix.OK {
		t.Fatalf("fix_systemd: %+v", fix)
	}
	if !strings.Contains(fix.Message, "dry-run") || !strings.Contains(fix.Message, dropin) {
		t.Fatalf("message want dry-run + path: %+v", fix)
	}
	if dry, _ := fix.Details["dry_run"].(bool); !dry {
		t.Fatalf("details.dry_run want true: %+v", fix.Details)
	}
	gotContent, _ := fix.Details["content"].(string)
	if gotContent != wantContent {
		t.Fatalf("details.content mismatch:\ngot:\n%s\nwant:\n%s", gotContent, wantContent)
	}
	if len(r.FixesApplied) != 0 {
		t.Fatalf("dry-run must not set fixes_applied: %v", r.FixesApplied)
	}
	// Notes: summary line + full unit text matching BuildSystemdDropin.
	if len(r.Notes) < 2 {
		t.Fatalf("expected notes with preview content, got %v", r.Notes)
	}
	foundUnit := false
	for _, n := range r.Notes {
		if n == strings.TrimRight(wantContent, "\n") || n == wantContent {
			foundUnit = true
			break
		}
		if strings.Contains(n, "Generated by mount-wrapper doctor") &&
			strings.Contains(n, "[Service]") {
			foundUnit = true
			break
		}
	}
	if !foundUnit {
		t.Fatalf("notes missing unit text: %v", r.Notes)
	}
	// Content in notes must match BuildSystemdDropin exactly (trimmed trailing NL).
	trimmedWant := strings.TrimRight(wantContent, "\n")
	matched := false
	for _, n := range r.Notes {
		if n == trimmedWant {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("notes unit text != BuildSystemdDropin; notes=%v want=%q", r.Notes, trimmedWant)
	}
}

func TestApplyFixSystemdDryRunSkipsWrite(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dropin := filepath.Join(tmp, "nested", "sources.conf")
	cfg := mustCfg(t, map[string]any{
		"source_dirs": []any{"/tmp"},
	}, "")

	wrote := false
	mkdirs := 0
	opts := &doctor.Options{
		DryRun: true,
		WriteFile: func(path string, content []byte, mode os.FileMode) error {
			wrote = true
			return nil
		},
		MkdirAll: func(path string, mode os.FileMode) error {
			mkdirs++
			return nil
		},
	}
	ok, msg := doctor.ApplyFixSystemd(cfg, dropin, opts)
	if !ok {
		t.Fatalf("ApplyFixSystemd dry-run: ok=%v msg=%s", ok, msg)
	}
	if wrote || mkdirs != 0 {
		t.Fatalf("dry-run must not write/mkdir: wrote=%v mkdirs=%d", wrote, mkdirs)
	}
	if !strings.Contains(msg, "dry-run") || !strings.Contains(msg, dropin) {
		t.Fatalf("msg=%q", msg)
	}
	want := doctor.BuildSystemdDropin(cfg)
	if !strings.Contains(want, "[Service]") {
		t.Fatalf("BuildSystemdDropin empty/unexpected: %q", want)
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

// chmodOXUnder opens path and every ancestor that is strictly under root
// (filepath.Rel succeeds without "..") so t.TempDir's nested 0700 parents do
// not fail windows_visible_parent_ox. Does not touch root itself (/tmp).
func chmodOXUnder(t *testing.T, path, root string) {
	t.Helper()
	root = filepath.Clean(root)
	for p := filepath.Clean(path); p != root; {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			break
		}
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatalf("chmod 0755 %s: %v", p, err)
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
}

// TestWindowsVisibleParentOXRealDirs uses real temp directories with modes
// 0755 (ok) vs 0700 (missing o+x). DirMode is left nil so default os.Stat runs.
func TestWindowsVisibleParentOXRealDirs(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Open the whole temp chain under /tmp (t.TempDir nests 0700 dirs).
	chmodOXUnder(t, base, "/tmp")

	mkOpts := func(t *testing.T, mountRoot string) doctor.Options {
		t.Helper()
		e := inventoryBaseEnv()
		cfg := mustCfg(t, map[string]any{
			"windows_visible":     true,
			"mount_root":          mountRoot,
			"convert_7z_nonsolid": false,
			"convert_zip_to_7z":   false,
		}, "")
		opts := e.opts(cfg)
		// Real filesystem mode probe (ignore injectable DirMode map).
		opts.DirMode = nil
		return opts
	}

	t.Run("all_0755_ok", func(t *testing.T) {
		parent := filepath.Join(base, "open")
		mounts := filepath.Join(parent, "mounts")
		if err := os.MkdirAll(mounts, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{parent, mounts} {
			if err := os.Chmod(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		r := doctor.Run(mkOpts(t, mounts))
		c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
		if !c.OK || c.Severity != doctor.SeverityInfo {
			t.Fatalf("want info ok: %+v", c)
		}
		if raw, ok := c.Details["missing_ox"]; ok {
			switch v := raw.(type) {
			case []string:
				if len(v) != 0 {
					t.Fatalf("missing_ox=%v", v)
				}
			case []any:
				if len(v) != 0 {
					t.Fatalf("missing_ox=%v", v)
				}
			}
		}
	})

	t.Run("parent_0700_warn", func(t *testing.T) {
		parent := filepath.Join(base, "closed")
		mounts := filepath.Join(parent, "mounts")
		if err := os.MkdirAll(mounts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(mounts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Fatal(err)
		}
		r := doctor.Run(mkOpts(t, mounts))
		c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
		if c.OK || c.Severity != doctor.SeverityWarn {
			t.Fatalf("want warn: %+v", c)
		}
		if !strings.Contains(c.Message, parent) {
			t.Fatalf("message should name closed parent %q: %q", parent, c.Message)
		}
		if !strings.Contains(c.Message, "chmod o+x") {
			t.Fatalf("message missing fix: %q", c.Message)
		}
		hint, _ := c.Details["fix_hint"].(string)
		if !strings.Contains(hint, parent) || !strings.Contains(hint, "chmod o+x") {
			t.Fatalf("fix_hint=%q", hint)
		}
		if doctor.HardFail(r.Checks) {
			t.Fatalf("hard fail unexpected: %s", doctor.FormatText(r))
		}
	})

	t.Run("mount_root_itself_0700_warn", func(t *testing.T) {
		parent := filepath.Join(base, "leaf-closed-parent")
		mounts := filepath.Join(parent, "mounts")
		if err := os.MkdirAll(mounts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(mounts, 0o700); err != nil {
			t.Fatal(err)
		}
		r := doctor.Run(mkOpts(t, mounts))
		c := checkByName(r, doctor.CheckNameWindowsVisibleParentOX)
		if c.OK || c.Severity != doctor.SeverityWarn {
			t.Fatalf("want warn for mount_root 0700: %+v", c)
		}
		if !strings.Contains(c.Message, mounts) {
			t.Fatalf("message should name mounts %q: %q", mounts, c.Message)
		}
	})
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

// doctorReportTopKeys is the frozen ToMap / FormatJSON root object key set
// (order independent). Keep in sync with Report.ToMap and docs/openapi.yaml
// DoctorReport.
var doctorReportTopKeys = []string{
	"ok", "checks", "config_path", "notes", "fixes_applied",
}

// doctorCheckKeys is the frozen per-check object key set.
var doctorCheckKeys = []string{
	"name", "ok", "severity", "message", "details",
}

var doctorAllowedSeverities = map[string]struct{}{
	doctor.SeverityInfo:  {},
	doctor.SeverityWarn:  {},
	doctor.SeverityError: {},
}

// assertDoctorMapShape freezes the JSON/map contract without full message goldens:
// root key set, array types, per-check keys, severity enum, and one-way
// HardFail policy (ok=true is invalid when any check is ok=false severity=error).
// Bidirectional OK vs HardFail is asserted separately for Run-produced reports.
func assertDoctorMapShape(t *testing.T, m map[string]any) {
	t.Helper()
	if m == nil {
		t.Fatal("nil map")
	}
	wantKeys := make(map[string]struct{}, len(doctorReportTopKeys))
	for _, k := range doctorReportTopKeys {
		wantKeys[k] = struct{}{}
	}
	if len(m) != len(wantKeys) {
		t.Errorf("root key count=%d want %d; keys=%v", len(m), len(wantKeys), mapKeys(m))
	}
	for k := range m {
		if _, ok := wantKeys[k]; !ok {
			t.Errorf("unexpected root key %q", k)
		}
	}
	for _, k := range doctorReportTopKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing root key %q", k)
		}
	}

	okVal, ok := m["ok"].(bool)
	if !ok {
		t.Fatalf("ok: want bool, got %T (%v)", m["ok"], m["ok"])
	}

	checks, ok := m["checks"].([]any)
	if !ok {
		t.Fatalf("checks: want array, got %T", m["checks"])
	}
	// ToMap may store []string; FormatJSON re-parse yields []any — accept both.
	notes, ok := asStringAnySlice(m["notes"])
	if !ok {
		t.Fatalf("notes: want string array, got %T (must not be null)", m["notes"])
	}
	fixes, ok := asStringAnySlice(m["fixes_applied"])
	if !ok {
		t.Fatalf("fixes_applied: want string array, got %T (must not be null)", m["fixes_applied"])
	}
	for i, n := range notes {
		if _, ok := n.(string); !ok {
			t.Errorf("notes[%d]: want string, got %T", i, n)
		}
	}
	for i, f := range fixes {
		if _, ok := f.(string); !ok {
			t.Errorf("fixes_applied[%d]: want string, got %T", i, f)
		}
	}
	// config_path: string or JSON null only.
	switch cp := m["config_path"].(type) {
	case nil:
		// empty / omitted path
	case string:
		if cp == "" {
			t.Error("config_path empty string should be null in ToMap/FormatJSON")
		}
	default:
		t.Errorf("config_path: want string|null, got %T", m["config_path"])
	}

	checkKeysWant := make(map[string]struct{}, len(doctorCheckKeys))
	for _, k := range doctorCheckKeys {
		checkKeysWant[k] = struct{}{}
	}
	hardFail := false
	for i, raw := range checks {
		c, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("checks[%d]: want object, got %T", i, raw)
		}
		if len(c) != len(checkKeysWant) {
			t.Errorf("checks[%d] key count=%d want %d; keys=%v", i, len(c), len(checkKeysWant), mapKeys(c))
		}
		for k := range c {
			if _, ok := checkKeysWant[k]; !ok {
				t.Errorf("checks[%d]: unexpected key %q", i, k)
			}
		}
		for _, k := range doctorCheckKeys {
			if _, ok := c[k]; !ok {
				t.Errorf("checks[%d]: missing key %q", i, k)
			}
		}
		name, _ := c["name"].(string)
		if name == "" {
			t.Errorf("checks[%d]: name empty", i)
		}
		cok, ok := c["ok"].(bool)
		if !ok {
			t.Errorf("checks[%d].ok: want bool, got %T", i, c["ok"])
		}
		sev, _ := c["severity"].(string)
		if _, ok := doctorAllowedSeverities[sev]; !ok {
			t.Errorf("checks[%d] %q: severity %q not in {info,warn,error}", i, name, sev)
		}
		if _, ok := c["message"].(string); !ok {
			t.Errorf("checks[%d] %q: message want string, got %T", i, name, c["message"])
		}
		// details never null — empty object when no extras.
		if c["details"] == nil {
			t.Errorf("checks[%d] %q: details is null; want object", i, name)
		} else if _, ok := c["details"].(map[string]any); !ok {
			t.Errorf("checks[%d] %q: details want object, got %T", i, name, c["details"])
		}
		if !cok && sev == doctor.SeverityError {
			hardFail = true
		}
	}
	// Severity policy: report ok must not be true when any HardFail check exists.
	// (ok=false with only warns is allowed for constructed reports.)
	if hardFail && okVal {
		t.Error("severity: HardFail checks present (ok=false severity=error) but report ok=true")
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// asStringAnySlice accepts ToMap's []string or JSON-decoded []any of strings.
func asStringAnySlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	default:
		return nil, false
	}
}

func checkNamesFromMap(m map[string]any) map[string]struct{} {
	out := make(map[string]struct{})
	checks, _ := m["checks"].([]any)
	for _, raw := range checks {
		c, _ := raw.(map[string]any)
		if name, _ := c["name"].(string); name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// TestDoctorFormatJSONStructural is a structural golden for FormatJSON / ToMap:
// fixed env, frozen key sets, severity policy, notes/fixes_applied arrays, and
// gated check names when config enables them — not full message string goldens.
func TestDoctorFormatJSONStructural(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// report builds the Report under test (may call Run with injectables).
		report func(t *testing.T) *doctor.Report
		// nilReport: exercise FormatJSON(nil) / ToMap(nil) separately.
		nilReport bool

		wantOK           *bool
		wantConfigPath   any // string or nil; omit check when wantConfigPathUnset
		wantConfigPathSet bool
		wantNotesLen     *int
		wantFixesLen     *int
		wantMinChecks    int
		requiredChecks   []string
		forbiddenChecks  []string
		// assertHardFailConsistency: when true, report ok must equal !HardFail(checks).
		assertHardFailConsistency bool
		extra                     func(t *testing.T, m map[string]any, r *doctor.Report)
	}{
		{
			name:      "nil_report",
			nilReport: true,
			wantOK:    boolPtr(false),
			wantConfigPathSet: true,
			wantConfigPath:    nil,
			wantNotesLen:      intPtr(0),
			wantFixesLen:      intPtr(0),
			wantMinChecks:     0,
		},
		{
			name: "empty_report_defaults",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				return &doctor.Report{OK: true}
			},
			wantOK:            boolPtr(true),
			wantConfigPathSet: true,
			wantConfigPath:    nil,
			wantNotesLen:      intPtr(0),
			wantFixesLen:      intPtr(0),
			wantMinChecks:     0,
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				// Nil Details on a check must serialize as {}.
				r2 := &doctor.Report{
					OK: true,
					Checks: []doctor.CheckResult{
						{Name: "x", OK: true, Severity: doctor.SeverityInfo, Message: "m", Details: nil},
					},
				}
				m2 := r2.ToMap()
				assertDoctorMapShape(t, m2)
				checks := m2["checks"].([]any)
				c0 := checks[0].(map[string]any)
				d, ok := c0["details"].(map[string]any)
				if !ok || len(d) != 0 {
					t.Fatalf("nil Details → empty object; got %v", c0["details"])
				}
			},
		},
		{
			name: "constructed_mixed_severity_notes_fixes",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				return &doctor.Report{
					OK:         false,
					ConfigPath: "/etc/mount-wrapper/config.yaml",
					Notes:      []string{"note-a"},
					FixesApplied: []string{
						"wrote drop-in",
					},
					Checks: []doctor.CheckResult{
						{
							Name: "go_version", OK: true, Severity: doctor.SeverityInfo,
							Message: "ok", Details: map[string]any{"version": "go1.25.0"},
						},
						{
							Name: "fuse_device", OK: false, Severity: doctor.SeverityWarn,
							Message: "missing fuse", Details: map[string]any{},
						},
						{
							Name: "ratarmount_bin", OK: false, Severity: doctor.SeverityError,
							Message: "not found", Details: map[string]any{"path": ""},
						},
					},
				}
			},
			wantOK:                    boolPtr(false),
			wantConfigPathSet:         true,
			wantConfigPath:            "/etc/mount-wrapper/config.yaml",
			wantNotesLen:              intPtr(1),
			wantFixesLen:              intPtr(1),
			wantMinChecks:             3,
			requiredChecks:            []string{"go_version", "fuse_device", "ratarmount_bin"},
			assertHardFailConsistency: true,
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				checks := m["checks"].([]any)
				// severity policy per constructed check (not message golden).
				byName := map[string]map[string]any{}
				for _, raw := range checks {
					c := raw.(map[string]any)
					byName[c["name"].(string)] = c
				}
				if byName["fuse_device"]["severity"] != doctor.SeverityWarn || byName["fuse_device"]["ok"] != false {
					t.Fatalf("fuse_device: %+v", byName["fuse_device"])
				}
				if byName["ratarmount_bin"]["severity"] != doctor.SeverityError || byName["ratarmount_bin"]["ok"] != false {
					t.Fatalf("ratarmount_bin: %+v", byName["ratarmount_bin"])
				}
				if m["notes"].([]any)[0] != "note-a" {
					t.Fatalf("notes: %v", m["notes"])
				}
				if m["fixes_applied"].([]any)[0] != "wrote drop-in" {
					t.Fatalf("fixes_applied: %v", m["fixes_applied"])
				}
			},
		},
		{
			name: "run_core_no_config",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				return doctor.Run(inventoryBaseEnv().opts(nil))
			},
			wantOK:                    boolPtr(true),
			wantConfigPathSet:         true,
			wantConfigPath:            nil,
			wantNotesLen:              intPtr(0),
			wantFixesLen:              intPtr(0),
			wantMinChecks:             len(doctor.CoreCheckNames),
			requiredChecks:            append([]string(nil), doctor.CoreCheckNames...),
			forbiddenChecks: []string{
				doctor.CheckNameWebBindSecurity,
				doctor.CheckNameConvertCacheDir,
				doctor.CheckNameWindowsVisibleParentOX,
				doctor.CheckNameControlSocketLive,
				doctor.CheckNameConfig,
				doctor.CheckNameIndexLayout,
				doctor.CheckNameFixSystemd,
			},
			assertHardFailConsistency: true,
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				if len(r.Checks) != len(doctor.CoreCheckNames) {
					t.Fatalf("core check count=%d want %d", len(r.Checks), len(doctor.CoreCheckNames))
				}
				// Core order frozen in JSON checks array.
				checks := m["checks"].([]any)
				for i, want := range doctor.CoreCheckNames {
					c := checks[i].(map[string]any)
					if c["name"] != want {
						t.Fatalf("checks[%d].name=%v want %q", i, c["name"], want)
					}
				}
			},
		},
		{
			name: "run_config_gated_convert_web",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				e := inventoryBaseEnv()
				cache := "/var/lib/mount-wrapper/nonsolid-cache"
				e.dirs[cache] = true
				e.exists[cache] = true
				e.writable[cache] = true
				e.setExec("/usr/bin/7z", "7z 23.0", "")
				e.which["7z"] = "/usr/bin/7z"
				cfg := mustCfg(t, map[string]any{
					"source_dirs":          []any{"/tmp"},
					"convert_7z_nonsolid":  true,
					"convert_7z_cache_dir": cache,
					"convert_zip_to_7z":    false,
					"web_enabled":          true,
					"web_host":             "0.0.0.0",
					"web_token":            "",
					"control_socket":       "/run/mount-wrapper/control.sock",
				}, "/tmp/doctor-struct-config.yaml")
				return doctor.Run(e.opts(cfg))
			},
			wantConfigPathSet: true,
			wantConfigPath:    "/tmp/doctor-struct-config.yaml",
			wantNotesLen:      intPtr(0),
			wantFixesLen:      intPtr(0),
			wantMinChecks:     len(doctor.CoreCheckNames) + 1,
			requiredChecks: []string{
				doctor.CheckNameWebBindSecurity,
				doctor.CheckNameWindowsVisibleParentOX,
				doctor.CheckNameConvertCacheDir,
				doctor.CheckNameIndexLayout,
				doctor.CheckNameControlSocketLive,
				doctor.CheckNameConfig,
				"path.mount_root",
				"source_dirs[0]",
			},
			forbiddenChecks: []string{
				doctor.CheckNameControlSocketPathLength, // linux
				doctor.CheckNameFixSystemd,
			},
			assertHardFailConsistency: true,
			// web_bind_security is warn-only → report remains OK.
			wantOK: boolPtr(true),
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				c := checkByName(r, doctor.CheckNameWebBindSecurity)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("web_bind_security: ok=%v sev=%q", c.OK, c.Severity)
				}
				live := checkByName(r, doctor.CheckNameControlSocketLive)
				if live.OK || live.Severity != doctor.SeverityWarn {
					t.Fatalf("control_socket_live offline: ok=%v sev=%q", live.OK, live.Severity)
				}
				// Presence of disk.* gated by free-space probes (prefix only).
				foundDisk := false
				for name := range checkNamesFromMap(m) {
					if strings.HasPrefix(name, doctor.DiskCheckPrefix) {
						foundDisk = true
						break
					}
				}
				if !foundDisk {
					t.Fatalf("expected disk.* check when config present; names=%v", orderedNames(r))
				}
			},
		},
		{
			name: "run_fix_systemd_notes_without_config",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				opts := inventoryBaseEnv().opts(nil)
				opts.FixSystemd = true
				return doctor.Run(opts)
			},
			wantOK:                    boolPtr(true), // fix_systemd is warn when no config
			wantConfigPathSet:         true,
			wantConfigPath:            nil,
			wantNotesLen:              intPtr(1),
			wantFixesLen:              intPtr(0),
			requiredChecks:            []string{doctor.CheckNameFixSystemd},
			assertHardFailConsistency: true,
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				notes := m["notes"].([]any)
				if len(notes) != 1 {
					t.Fatalf("notes=%v", notes)
				}
				// Structural: non-empty note string; avoid full-message golden.
				s, _ := notes[0].(string)
				if s == "" {
					t.Fatal("notes[0] empty")
				}
				c := checkByName(r, doctor.CheckNameFixSystemd)
				if c.OK || c.Severity != doctor.SeverityWarn {
					t.Fatalf("fix_systemd: %+v", c)
				}
			},
		},
		{
			name: "run_fix_systemd_fixes_applied",
			report: func(t *testing.T) *doctor.Report {
				t.Helper()
				e := inventoryBaseEnv()
				tmp := t.TempDir()
				dropin := filepath.Join(tmp, "systemd", "sources.conf")
				cfg := mustCfg(t, map[string]any{
					"source_dirs": []any{"/tmp"},
					"mount_root":  filepath.Join(tmp, "m"),
				}, filepath.Join(tmp, "c.yaml"))
				// mount_root parent may not exist in fakes — mark tmp tree writable.
				e.dirs[tmp] = true
				e.exists[tmp] = true
				e.writable[tmp] = true
				e.dirs[filepath.Join(tmp, "m")] = true
				e.exists[filepath.Join(tmp, "m")] = true
				e.writable[filepath.Join(tmp, "m")] = true
				opts := e.opts(cfg)
				opts.FixSystemd = true
				opts.DropinPath = dropin
				return doctor.Run(opts)
			},
			wantMinChecks:             1,
			requiredChecks:            []string{doctor.CheckNameFixSystemd},
			wantFixesLen:              intPtr(1),
			wantNotesLen:              intPtr(0),
			assertHardFailConsistency: true,
			extra: func(t *testing.T, m map[string]any, r *doctor.Report) {
				t.Helper()
				// config_path is the loaded yaml path (non-null string).
				if cp, ok := m["config_path"].(string); !ok || cp == "" {
					t.Fatalf("config_path want non-empty string, got %v", m["config_path"])
				}
				fixes := m["fixes_applied"].([]any)
				if len(fixes) != 1 {
					t.Fatalf("fixes_applied=%v", fixes)
				}
				if s, _ := fixes[0].(string); s == "" {
					t.Fatal("fixes_applied[0] empty")
				}
				c := checkByName(r, doctor.CheckNameFixSystemd)
				if !c.OK {
					t.Fatalf("fix_systemd: %+v", c)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var r *doctor.Report
			if !tc.nilReport {
				r = tc.report(t)
			}

			// ToMap structural golden
			var tm map[string]any
			if tc.nilReport {
				tm = (*doctor.Report)(nil).ToMap()
			} else {
				tm = r.ToMap()
			}
			assertDoctorMapShape(t, tm)

			// FormatJSON must match ToMap structurally (CLI --json / API parity).
			js, err := doctor.FormatJSON(r) // nil ok: FormatJSON nil-coalesces
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(js, "\n") {
				t.Fatal("FormatJSON should end with newline")
			}
			var jm map[string]any
			if err := json.Unmarshal([]byte(js), &jm); err != nil {
				t.Fatalf("FormatJSON parse: %v\n%s", err, js)
			}
			assertDoctorMapShape(t, jm)

			// FormatJSON encodes the same ToMap payload (after nil coalesce).
			var compare *doctor.Report
			if r == nil {
				compare = &doctor.Report{OK: false}
			} else {
				compare = r
			}
			wantJS, err := json.MarshalIndent(compare.ToMap(), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if js != string(wantJS)+"\n" {
				t.Fatalf("FormatJSON != MarshalIndent(ToMap)\n got: %s\nwant: %s\n", js, string(wantJS)+"\n")
			}

			if tc.wantOK != nil {
				if ok, _ := jm["ok"].(bool); ok != *tc.wantOK {
					t.Fatalf("ok=%v want %v", ok, *tc.wantOK)
				}
			}
			if tc.wantConfigPathSet {
				if jm["config_path"] != tc.wantConfigPath {
					t.Fatalf("config_path=%v want %v", jm["config_path"], tc.wantConfigPath)
				}
			}
			if tc.wantNotesLen != nil {
				notes := jm["notes"].([]any)
				if len(notes) != *tc.wantNotesLen {
					t.Fatalf("notes len=%d want %d: %v", len(notes), *tc.wantNotesLen, notes)
				}
			}
			if tc.wantFixesLen != nil {
				fixes := jm["fixes_applied"].([]any)
				if len(fixes) != *tc.wantFixesLen {
					t.Fatalf("fixes_applied len=%d want %d: %v", len(fixes), *tc.wantFixesLen, fixes)
				}
			}
			checks := jm["checks"].([]any)
			if len(checks) < tc.wantMinChecks {
				t.Fatalf("checks len=%d want >= %d", len(checks), tc.wantMinChecks)
			}
			names := checkNamesFromMap(jm)
			for _, want := range tc.requiredChecks {
				if _, ok := names[want]; !ok {
					t.Errorf("missing required check %q; have %v", want, orderedNames(r))
				}
			}
			for _, ban := range tc.forbiddenChecks {
				if _, ok := names[ban]; ok {
					t.Errorf("forbidden check %q present", ban)
				}
			}
			if tc.assertHardFailConsistency && r != nil {
				wantOK := !doctor.HardFail(r.Checks)
				if r.OK != wantOK {
					t.Fatalf("report.OK=%v want %v (HardFail consistency)", r.OK, wantOK)
				}
				if jm["ok"] != wantOK {
					t.Fatalf("json ok=%v want %v", jm["ok"], wantOK)
				}
			}
			if tc.extra != nil {
				tc.extra(t, jm, r)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

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
