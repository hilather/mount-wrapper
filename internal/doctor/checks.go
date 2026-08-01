package doctor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/paths"
	"github.com/hilather/mount-wrapper/internal/platform"
)

// darwinSunPathWarnLen is the path-length threshold for control_socket on
// macOS. sockaddr_un / sun_path is ~104 bytes including the trailing NUL;
// warn slightly earlier so operators shorten paths before bind fails.
const darwinSunPathWarnLen = 100

func checkGoVersion(opts *Options) CheckResult {
	ver := opts.goVersion()
	// runtime.Version() is typically "go1.22.0". Require go1.21+ as a soft floor
	// (module declares a higher version; doctor only fails on ancient runtimes).
	ok := true
	msg := fmt.Sprintf("Go %s", strings.TrimPrefix(ver, "go"))
	if strings.HasPrefix(ver, "go1.") {
		// Parse major.minor roughly: go1.N...
		rest := strings.TrimPrefix(ver, "go1.")
		minor := 0
		for i := 0; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
			minor = minor*10 + int(rest[i]-'0')
		}
		if minor > 0 && minor < 21 {
			ok = false
			msg = fmt.Sprintf("Go %s — require Go 1.21+", strings.TrimPrefix(ver, "go"))
		}
	}
	return CheckResult{
		Name:     "go_version",
		OK:       ok,
		Severity: map[bool]string{true: SeverityInfo, false: SeverityError}[ok],
		Message:  msg,
		Details: map[string]any{
			"version":  ver,
			"required": ">=1.21",
			"compiler": runtime.Compiler,
			"arch":     runtime.GOARCH,
		},
	}
}

func checkHostPlatform(opts *Options) CheckResult {
	plat := opts.platform()
	wsl := opts.isWSL()
	var notes []string
	if platform.IsDarwin(plat) {
		notes = append(notes, "macOS first-step support active; see docs/macos.md")
	}
	if wsl {
		notes = append(notes, "WSL detected")
	}
	msg := "platform=" + plat
	if len(notes) > 0 {
		msg += " (" + strings.Join(notes, "; ") + ")"
	}
	return infoCheck("host_platform", msg, map[string]any{
		"platform": plat,
		"peercred": platform.PeercredBackendLabel(plat),
		"wsl":      wsl,
	})
}

func checkPeercred(opts *Options) CheckResult {
	plat := opts.platform()
	label := platform.PeercredBackendLabel(plat)
	allowUnauth := platform.ControlAllowUnauth()
	msg := "control socket peer credentials via " + label
	if allowUnauth {
		msg += " — " + platform.ControlAllowUnauthEnv + "=1 (unauthenticated control allowed)"
	}
	details := map[string]any{
		"backend":      label,
		"platform":     plat,
		"allow_unauth": allowUnauth,
	}
	if opts.Config != nil && opts.Config.ControlSocket != "" {
		details["control_socket"] = opts.Config.ControlSocket
		msg += "; socket path " + opts.Config.ControlSocket
	}
	return infoCheck("peercred", msg, details)
}

func checkFuseDevice(opts *Options) CheckResult {
	probe := platform.ProbeFusePresence(opts.platform(), platform.PathExistsFunc(opts.pathExists()))
	var msg string
	if probe.OK {
		if len(probe.Found) > 0 {
			msg = "FUSE present (" + probe.Found[0] + ")"
		} else {
			msg = "FUSE present"
		}
	} else {
		hint := probe.Hint
		if hint == "" {
			hint = "FUSE not detected"
		}
		msg = "FUSE not detected — " + hint
	}
	return CheckResult{
		Name:     "fuse_device",
		OK:       probe.OK,
		Severity: map[bool]string{true: SeverityInfo, false: SeverityWarn}[probe.OK],
		Message:  msg,
		Details: map[string]any{
			"platform":   probe.Platform,
			"candidates": probe.Candidates,
			"found":      probe.Found,
			"ok":         probe.OK,
			"hint":       probe.Hint,
		},
	}
}

func checkFusermount(opts *Options) CheckResult {
	probe := platform.ProbeUnmountTool(opts.platform(), platform.WhichFunc(opts.which()))
	if probe.OK {
		tool := probe.Tool
		cmdPreview := tool
		if len(probe.CommandTemplate) >= 2 {
			cmdPreview = strings.Join(probe.CommandTemplate[:2], " ")
		} else if len(probe.CommandTemplate) == 1 {
			cmdPreview = probe.CommandTemplate[0]
		}
		return infoCheck("fusermount", fmt.Sprintf("unmount tool ready: %s (%s)", tool, cmdPreview), map[string]any{
			"platform":         probe.Platform,
			"tool":             probe.Tool,
			"command_template": probe.CommandTemplate,
			"ok":               true,
		})
	}
	return warnCheck("fusermount", false, probe.Tool+" not found on PATH", map[string]any{
		"platform": probe.Platform,
		"tool":     probe.Tool,
		"ok":       false,
	})
}

func checkUserAllowOther(opts *Options) CheckResult {
	plat := opts.platform()
	windowsVisible := true
	if opts.Config != nil {
		windowsVisible = opts.Config.WindowsVisible
	} else {
		windowsVisible = platform.DefaultWindowsVisible(plat)
	}

	if platform.IsDarwin(plat) {
		return infoCheck("user_allow_other",
			"macOS: user_allow_other / windows_visible are WSL-oriented; "+
				"keep windows_visible: false for single-user mounts",
			map[string]any{
				"platform":        "darwin",
				"windows_visible": windowsVisible,
			},
		)
	}

	fuseConf := opts.fuseConfPath()
	enabled := false
	if text, err := opts.readFile()(fuseConf); err == nil {
		for _, line := range strings.Split(text, "\n") {
			stripped := strings.TrimSpace(line)
			if strings.HasPrefix(stripped, "#") {
				continue
			}
			if stripped == "user_allow_other" {
				enabled = true
				break
			}
		}
	}

	if windowsVisible && !enabled {
		return warnCheck("user_allow_other", false,
			"windows_visible is true but user_allow_other is not set in "+fuseConf+
				"; Windows may not see FUSE mounts via \\\\wsl.localhost\\...",
			map[string]any{
				"fuse_conf":       fuseConf,
				"enabled":         false,
				"windows_visible": windowsVisible,
			},
		)
	}
	msg := "user_allow_other not required (windows_visible=false)"
	if enabled {
		msg = "user_allow_other enabled"
	}
	return infoCheck("user_allow_other", msg, map[string]any{
		"enabled":         enabled,
		"windows_visible": windowsVisible,
		"fuse_conf":       fuseConf,
	})
}

func checkRatarmount(opts *Options) CheckResult {
	backend := config.BackendRust
	var configured string
	if opts.Config != nil {
		backend = opts.Config.MountBackend
		configured = opts.Config.RatarmountBin
		if backend == "" {
			backend = config.BackendRust
		}
	}
	// Normalize when possible; invalid backend still surfaces in details.
	if nb, err := config.NormalizeMountBackend(backend); err == nil {
		backend = nb
	}
	label := mounter.BackendLabel(backend)

	which := opts.which()
	isExec := opts.isExecutable()
	resolved, _ := mounter.ResolveRatarmountBin(backend, configured, mounter.ResolveOptions{
		Which:              mounter.WhichFunc(which),
		IsExecutable:       mounter.ExecutableFunc(isExec),
		SearchPathDisabled: false,
	})

	// Candidate list for messaging (parity with Python tried list).
	candidates := []string{}
	if opts.Config != nil {
		eff := opts.Config.EffectiveRatarmountBin()
		candidates = append(candidates, eff)
		if configured != "" && configured != eff {
			candidates = append(candidates, configured)
		}
	}
	def := mounter.DefaultRatarmountBin(backend)
	if !containsString(candidates, def) {
		candidates = append(candidates, def)
	}
	if p := which("ratarmount-rs"); p != "" && !containsString(candidates, p) {
		candidates = append(candidates, p)
	}
	// Prefer resolver result first, then the messaging candidate list.
	tryOrder := make([]string, 0, len(candidates)+1)
	if resolved != "" {
		tryOrder = append(tryOrder, resolved)
	}
	tryOrder = append(tryOrder, candidates...)
	seen := map[string]struct{}{}
	for _, candidate := range tryOrder {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		// Bare name: resolve via which.
		path := candidate
		if !strings.Contains(path, string(filepath.Separator)) && !strings.HasPrefix(path, ".") {
			if w := which(path); w != "" {
				path = w
			}
		}

		if isExec(path) {
			version := probeBinVersion(opts, path)
			hasF := probeSupportsForeground(opts, path)
			// hasF nil = unknown (still ok); false = missing support (error)
			ok := true
			sev := SeverityInfo
			msg := fmt.Sprintf("%s: found executable %s", label, path)
			if version != "" {
				msg += " (" + version + ")"
			}
			if hasF != nil && !*hasF {
				ok = false
				sev = SeverityError
				msg += " — missing -f/--foreground support"
			}
			return CheckResult{
				Name:     "ratarmount_bin",
				OK:       ok,
				Severity: sev,
				Message:  msg,
				Details: map[string]any{
					"path":                path,
					"version":             nullIfEmpty(version),
					"supports_foreground": boolOrNil(hasF),
					"mount_backend":       backend,
				},
			}
		}
		if opts.pathExists()(path) {
			return warnCheck("ratarmount_bin", false,
				fmt.Sprintf("%s: %s exists but is not executable", label, path),
				map[string]any{"path": path, "mount_backend": backend},
			)
		}
	}

	hint := fmt.Sprintf(
		"ratarmount-rs not found (tried %s, PATH). "+
			"Build/install ratarmount-rs and ensure it is on PATH, "+
			"or set ratarmount_bin to the binary path. Python ratarmount is not supported.",
		config.DefaultRustRatarmountBin,
	)
	return warnCheck("ratarmount_bin", false, hint, map[string]any{
		"tried":         candidates,
		"mount_backend": backend,
	})
}

func checkArchiveconverter(opts *Options) CheckResult {
	enabled := false
	configured := ""
	if opts.Config != nil {
		enabled = opts.Config.ArchiveconverterEnabled
		configured = strings.TrimSpace(opts.Config.ArchiveconverterBin)
	}

	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	}
	// Auto candidates: config default home path + bare name.
	def := config.DefaultArchiveconverterBin()
	if !containsString(candidates, def) {
		candidates = append(candidates, def)
	}
	if !containsString(candidates, "archiveconverter") {
		candidates = append(candidates, "archiveconverter")
	}

	which := opts.which()
	isExec := opts.isExecutable()
	for _, candidate := range candidates {
		path := candidate
		if !strings.Contains(path, string(filepath.Separator)) && !strings.HasPrefix(path, ".") {
			if w := which(path); w != "" {
				path = w
			}
		}
		if !isExec(path) {
			continue
		}
		version := probeBinVersion(opts, path)
		msg := "archiveconverter found at " + path
		if version != "" {
			msg += " (" + version + ")"
		}
		if !enabled {
			msg += " — disabled (set archiveconverter_enabled: true to convert .7z before index)"
		}
		return infoCheck("archiveconverter", msg, map[string]any{
			"path":    path,
			"version": nullIfEmpty(version),
			"enabled": enabled,
		})
	}

	if enabled {
		tried := configured
		if tried == "" {
			tried = "(auto)"
		}
		return warnCheck("archiveconverter", false,
			"archiveconverter_enabled is true but binary not found. "+
				"Build sibling archiveconverter (`cargo build --release`) or set "+
				"archiveconverter_bin. Without it, solid nested .7z mounts may be slow/fail.",
			map[string]any{"enabled": true, "tried": tried},
		)
	}
	return infoCheck("archiveconverter",
		"archiveconverter not installed (optional). "+
			"Enable with archiveconverter_enabled for solid→non-solid .7z conversion.",
		map[string]any{"enabled": false},
	)
}

func checkSevenZipBin(opts *Options) CheckResult {
	// Report 7z binary when convert_7z_nonsolid or convert_zip_to_7z is enabled,
	// or always as optional info when config present.
	enabled := false
	configured := ""
	if opts.Config != nil {
		enabled = opts.Config.Convert7zNonsolid || opts.Config.ConvertZipTo7z
		configured = strings.TrimSpace(opts.Config.Convert7zBin)
	}

	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, configured)
	}
	for _, name := range []string{"7z", "7zz", "7za"} {
		if !containsString(candidates, name) {
			candidates = append(candidates, name)
		}
	}

	which := opts.which()
	isExec := opts.isExecutable()
	for _, candidate := range candidates {
		path := candidate
		if !strings.Contains(path, string(filepath.Separator)) && !strings.HasPrefix(path, ".") {
			if w := which(path); w != "" {
				path = w
			}
		}
		if !isExec(path) {
			continue
		}
		version := probeBinVersion(opts, path)
		msg := "7z tool found at " + path
		if version != "" {
			msg += " (" + truncate(version, 80) + ")"
		}
		if !enabled {
			msg += " — convert_7z_nonsolid / convert_zip_to_7z disabled"
		}
		return infoCheck("sevenzip_bin", msg, map[string]any{
			"path":    path,
			"version": nullIfEmpty(version),
			"enabled": enabled,
		})
	}

	if enabled {
		tried := configured
		if tried == "" {
			tried = "7z/7zz/7za"
		}
		return warnCheck("sevenzip_bin", false,
			"convert_7z_* / convert_zip_to_7z enabled but 7z binary not found on PATH. "+
				"Install p7zip/7zip or set convert_7z_bin.",
			map[string]any{"enabled": true, "tried": tried},
		)
	}
	// Optional when features off — still emit a soft info so the check appears.
	return infoCheck("sevenzip_bin",
		"7z not required (convert_7z_nonsolid / convert_zip_to_7z disabled)",
		map[string]any{"enabled": false},
	)
}

func checkMountBackend(opts *Options) CheckResult {
	backend := config.BackendRust
	if opts.Config != nil && opts.Config.MountBackend != "" {
		backend = opts.Config.MountBackend
	}
	if nb, err := config.NormalizeMountBackend(backend); err == nil {
		backend = nb
	}
	label := mounter.BackendLabel(backend)
	return infoCheck("mount_backend",
		fmt.Sprintf("using %s (only supported engine)", label),
		map[string]any{"mount_backend": backend},
	)
}

func checkSystemd(opts *Options) CheckResult {
	plat := opts.platform()
	if platform.IsDarwin(plat) {
		// serve has no --foreground flag (process is always foreground).
		// launchd example lives under packaging/launchd/ (see docs/macos.md).
		return infoCheck("systemd_pid1",
			"macOS: systemd not used; run as your login user with "+
				"`mount-wrapper serve` (launchd example: packaging/launchd/; docs/macos.md)",
			map[string]any{"platform": "darwin", "is_systemd": false, "launchd": true},
		)
	}
	comm, err := opts.readPID1()()
	if err != nil {
		comm = "unknown"
	}
	isSystemd := comm == "systemd"
	sev := SeverityInfo
	if !isSystemd {
		sev = SeverityWarn
	}
	msg := "systemd is PID 1"
	if !isSystemd {
		msg = fmt.Sprintf("PID 1 is %q (enable systemd in /etc/wsl.conf for the service unit)", comm)
	}
	return CheckResult{
		Name:     "systemd_pid1",
		OK:       true, // informational / soft warn only
		Severity: sev,
		Message:  msg,
		Details:  map[string]any{"pid1": comm, "is_systemd": isSystemd},
	}
}

// checkSystemdUnit best-effort probes systemctl is-active / is-enabled for
// mount-wrapper.service when PID 1 is systemd on non-Darwin hosts.
// Skipped on Darwin or when PID 1 is not systemd. Offline-safe: inactive,
// disabled, failed, not-found, or missing systemctl are severity warn
// (never hard-fail).
func checkSystemdUnit(opts *Options) CheckResult {
	plat := opts.platform()
	if platform.IsDarwin(plat) {
		return CheckResult{}
	}
	comm, err := opts.readPID1()()
	if err != nil || comm != "systemd" {
		return CheckResult{}
	}

	name := CheckNameSystemdUnit
	unit := DefaultSystemdUnit
	sysctl := opts.systemctl()
	active, activeErr := sysctl("is-active", unit)
	enabled, enabledErr := sysctl("is-enabled", unit)

	details := map[string]any{
		"unit":    unit,
		"active":  active,
		"enabled": enabled,
	}
	if activeErr != nil {
		details["active_error"] = activeErr.Error()
	}
	if enabledErr != nil {
		details["enabled_error"] = enabledErr.Error()
	}

	// systemctl binary missing / unusable: both probes empty + err.
	if active == "" && enabled == "" && (activeErr != nil || enabledErr != nil) {
		errMsg := "systemctl unavailable"
		if activeErr != nil {
			errMsg = activeErr.Error()
		} else if enabledErr != nil {
			errMsg = enabledErr.Error()
		}
		return warnCheck(name, false,
			fmt.Sprintf("cannot probe %s via systemctl: %s", unit, errMsg),
			details)
	}

	isActive := active == "active"
	// Treat common "will start / is wired" states as enabled for messaging.
	isEnabled := enabled == "enabled" ||
		enabled == "enabled-runtime" ||
		enabled == "static" ||
		enabled == "indirect" ||
		enabled == "generated" ||
		enabled == "alias"
	details["is_active"] = isActive
	details["is_enabled"] = isEnabled

	if isActive {
		msg := unit + " is active"
		if enabled != "" {
			msg += " (enabled=" + enabled + ")"
		}
		return infoCheck(name, msg, details)
	}

	// Not active: warn with active/enabled state (never hard-fail).
	activeDisp := active
	if activeDisp == "" {
		activeDisp = "unknown"
	}
	enabledDisp := enabled
	if enabledDisp == "" {
		enabledDisp = "unknown"
	}
	msg := fmt.Sprintf("%s is not active (active=%s, enabled=%s); start with systemctl start %s or enable --now",
		unit, activeDisp, enabledDisp, unit)
	if active == "failed" {
		msg = fmt.Sprintf("%s is failed (enabled=%s); check journalctl -u %s", unit, enabledDisp, unit)
	} else if active == "not-found" || enabled == "not-found" {
		msg = fmt.Sprintf("%s unit not found; install the package or copy packaging/systemd/%s",
			unit, unit)
	}
	return warnCheck(name, false, msg, details)
}

// checkPidfileLive probes configured pid_file path existence, PID parse, and
// process liveness. Offline-safe: missing path, unreadable, invalid PID, or
// dead process are severity warn (never hard-fail). Skipped when Config is
// nil or pid_file is empty.
func checkPidfileLive(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	path := strings.TrimSpace(cfg.PIDFile)
	if path == "" {
		return CheckResult{}
	}
	name := CheckNamePidfileLive
	details := map[string]any{
		"path": path,
	}

	if !opts.pathExists()(path) {
		return warnCheck(name, false,
			fmt.Sprintf("pidfile %s not found (serve not running or path wrong)", path),
			details)
	}
	details["exists"] = true

	content, err := opts.readFile()(path)
	if err != nil {
		details["error"] = err.Error()
		return warnCheck(name, false,
			fmt.Sprintf("pidfile %s unreadable: %v", path, err),
			details)
	}

	pidStr := strings.TrimSpace(firstLine(content))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		details["raw"] = pidStr
		return warnCheck(name, false,
			fmt.Sprintf("pidfile %s has invalid pid %q", path, pidStr),
			details)
	}
	details["pid"] = pid

	alive := opts.processAlive()(pid)
	details["alive"] = alive
	if !alive {
		return warnCheck(name, false,
			fmt.Sprintf("pidfile %s pid %d is not running (stale pidfile?)", path, pid),
			details)
	}
	return infoCheck(name,
		fmt.Sprintf("pidfile %s pid %d is alive", path, pid),
		details)
}

func checkServiceUser(opts *Options) CheckResult {
	plat := opts.platform()
	if platform.IsDarwin(plat) {
		return infoCheck("service_user",
			"macOS first-step: run as your login user "+
				"(no dedicated mount-wrapper system user yet)",
			map[string]any{"platform": "darwin", "user": "current"},
		)
	}
	name := opts.serviceUser()
	if opts.lookupUser()(name) {
		return infoCheck("service_user", "user "+name+" exists", map[string]any{"user": name})
	}
	return warnCheck("service_user", false,
		"user "+name+" not found (install the package or create the service user)",
		map[string]any{"user": name},
	)
}

func checkServicePaths(opts *Options) []CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return nil
	}
	var out []CheckResult
	for _, item := range []struct {
		name string
		path string
	}{
		{"mount_root", cfg.MountRoot},
		{"index_dir", cfg.IndexDir},
		{"overlay_dir", cfg.OverlayDir},
	} {
		out = append(out, checkOnePath(opts, "path."+item.name, item.name, item.path))
	}
	// Control socket parent (run dir).
	if cfg.ControlSocket != "" {
		parent := filepath.Dir(cfg.ControlSocket)
		out = append(out, checkOnePath(opts, "path.control_socket_dir", "control_socket_dir", parent))
	}
	return out
}

// checkWindowsVisibleParentOX walks mount_root and its ancestors when
// windows_visible is true on Linux, warning if any existing directory lacks
// other-execute (o+x). Windows UNC (\\wsl.localhost\…) and allow_other clients
// need traverse permission on every parent; the daemon does not chmod parents.
//
// Always emitted when Config is non-nil: macOS and windows_visible=false are
// info-only (no hard fail). Severity is warn when o+x is missing.
func checkWindowsVisibleParentOX(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	name := CheckNameWindowsVisibleParentOX
	plat := opts.platform()
	wv := cfg.WindowsVisible
	mountRoot := strings.TrimSpace(cfg.MountRoot)
	details := map[string]any{
		"windows_visible": wv,
		"mount_root":      mountRoot,
		"platform":        plat,
	}

	if platform.IsDarwin(plat) {
		return infoCheck(name,
			"macOS: parent o+x traverse is for Windows/WSL UNC with windows_visible; "+
				"keep windows_visible: false for single-user mounts",
			details)
	}

	if !wv {
		return infoCheck(name,
			"windows_visible is false; parent o+x (other-execute) not required for UNC traverse",
			details)
	}

	if mountRoot == "" {
		return warnCheck(name, false,
			"windows_visible is true but mount_root is empty — set mount_root and ensure "+
				"every ancestor has o+x (chmod o+x) for Windows UNC traverse (docs/architecture.md)",
			details)
	}

	dirMode := opts.dirMode()
	var missingOX []string
	var checked []string
	modes := map[string]string{}
	for p := filepath.Clean(mountRoot); ; {
		if mode, ok := dirMode(p); ok {
			checked = append(checked, p)
			modes[p] = fmt.Sprintf("%04o", mode&os.ModePerm)
			if mode&0o001 == 0 {
				missingOX = append(missingOX, p)
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	details["checked"] = checked
	details["modes"] = modes
	details["missing_ox"] = missingOX

	if len(checked) == 0 {
		return warnCheck(name, false,
			fmt.Sprintf("windows_visible is true but no existing ancestors of mount_root %q "+
				"could be inspected for o+x; create the path and run chmod o+x on each "+
				"directory from / through mount_root so Windows UNC can traverse "+
				"(docs/architecture.md, docs/install.md)", mountRoot),
			details)
	}

	if len(missingOX) > 0 {
		hint := "chmod o+x " + strings.Join(missingOX, " ")
		details["fix_hint"] = hint
		return warnCheck(name, false,
			fmt.Sprintf("windows_visible is true but path(s) lack o+x (other-execute) needed "+
				"for Windows UNC traverse: %s — fix: %s (see docs/architecture.md / docs/install.md; "+
				"create-user.sh sets o+x on packaged /var/lib/mount-wrapper paths only)",
				strings.Join(missingOX, ", "), hint),
			details)
	}

	return infoCheck(name,
		fmt.Sprintf("mount_root ancestors have o+x for windows_visible UNC traverse (%d path(s))", len(checked)),
		details)
}

func checkOnePath(opts *Options, checkName, label, pathStr string) CheckResult {
	if pathStr == "" {
		return warnCheck(checkName, false, label+" is empty", map[string]any{"path": pathStr})
	}
	exists := opts.pathExists()(pathStr)
	isDir := opts.isDir()(pathStr)
	if exists && !isDir {
		return errorCheck(checkName, false,
			fmt.Sprintf("%s exists but is not a directory: %s", label, pathStr),
			map[string]any{"path": pathStr},
		)
	}
	parent := resolveParent(pathStr, opts.pathExists())
	parentExists := opts.pathExists()(parent)
	writable := false
	if parentExists {
		writable = opts.writable()(parent)
	}
	if exists {
		return infoCheck(checkName, fmt.Sprintf("%s exists at %s", label, pathStr), map[string]any{
			"path": pathStr, "exists": true, "parent_writable": writable,
		})
	}
	if parentExists && writable {
		return infoCheck(checkName,
			fmt.Sprintf("%s does not exist yet; parent writable: true", label),
			map[string]any{"path": pathStr, "exists": false, "parent_writable": true},
		)
	}
	if parentExists {
		return warnCheck(checkName, false,
			fmt.Sprintf("%s does not exist yet; parent not writable: %s", label, parent),
			map[string]any{"path": pathStr, "exists": false, "parent_writable": false},
		)
	}
	return warnCheck(checkName, false,
		fmt.Sprintf("%s and parent missing: %s", label, pathStr),
		map[string]any{"path": pathStr, "exists": false, "parent_writable": false},
	)
}

func checkSourceDirs(opts *Options) []CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return nil
	}
	if len(cfg.SourceDirs) == 0 {
		return []CheckResult{
			warnCheck("source_dirs", false, "no source_dirs configured", map[string]any{}),
		}
	}
	var out []CheckResult
	for i, src := range cfg.SourceDirs {
		name := fmt.Sprintf("source_dirs[%d]", i)
		mapped, err := paths.ToWSLPath(src, &paths.ToWSLOpts{})
		if err != nil {
			out = append(out, errorCheck(name, false, fmt.Sprintf("%q: %v", src, err), map[string]any{
				"configured": src,
			}))
			continue
		}
		exists := opts.isDir()(mapped)
		drvfs := paths.IsDrvFsPath(mapped)
		sev := SeverityInfo
		ok := true
		note := "directory exists"
		if !exists {
			sev = SeverityWarn
			// Soft: missing source is warn, still ok=true (parity with Python)
			ok = true
			note = "mapped path not found or not a directory"
		}
		fsNote := "Linux FS"
		if drvfs {
			fsNote = "DrvFs"
		}
		out = append(out, CheckResult{
			Name:     name,
			OK:       ok,
			Severity: sev,
			Message:  fmt.Sprintf("%q → %s (%s; %s)", src, mapped, fsNote, note),
			Details: map[string]any{
				"configured": src,
				"mapped":     mapped,
				"exists":     exists,
				"drvfs":      drvfs,
			},
		})
	}
	return out
}

func checkIndexLayout(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{} // skipped by caller
	}
	indexDir := cfg.IndexDir
	onDrvfs := paths.IsDrvFsPath(indexDir)
	allow := cfg.AllowIndexesOnDrvfs
	if onDrvfs && !allow {
		return warnCheck("index_layout", false,
			fmt.Sprintf("index_dir %q appears to be on DrvFs; keep indexes on the Linux filesystem "+
				"or set allow_indexes_on_drvfs: true", indexDir),
			map[string]any{
				"index_dir":              indexDir,
				"drvfs":                  true,
				"allow_indexes_on_drvfs": allow,
			},
		)
	}
	msg := "index_dir on Linux filesystem"
	if onDrvfs {
		msg = "index_dir on DrvFs allowed (allow_indexes_on_drvfs=true)"
	}
	return infoCheck("index_layout", msg, map[string]any{
		"index_dir":              indexDir,
		"drvfs":                  onDrvfs,
		"allow_indexes_on_drvfs": allow,
	})
}

func checkFreeSpace(opts *Options) []CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return nil
	}
	threshold := int64(cfg.MinFreeBytes)
	if threshold <= 0 && opts.MinFreeWarnBytes > 0 {
		threshold = opts.MinFreeWarnBytes
	}

	var out []CheckResult
	seen := map[string]struct{}{}
	for _, item := range []struct {
		name string
		path string
	}{
		{"mount_root", cfg.MountRoot},
		{"index_dir", cfg.IndexDir},
		{"overlay_dir", cfg.OverlayDir},
	} {
		if item.path == "" {
			continue
		}
		// Deduplicate by cleaned path so same FS is not reported thrice when identical.
		key := filepath.Clean(item.path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		free, ok := opts.freeBytes()(item.path)
		if !ok {
			out = append(out, infoCheck("disk."+item.name,
				fmt.Sprintf("free space for %s unavailable", item.name),
				map[string]any{"path": item.path, "free_bytes": nil},
			))
			continue
		}
		details := map[string]any{
			"path":       item.path,
			"free_bytes": free,
			"threshold":  threshold,
		}
		if threshold > 0 && free < threshold {
			out = append(out, warnCheck("disk."+item.name, false,
				fmt.Sprintf("%s low disk: %s free (threshold %s)",
					item.name, humanBytes(free), humanBytes(threshold)),
				details,
			))
			continue
		}
		out = append(out, infoCheck("disk."+item.name,
			fmt.Sprintf("%s free space: %s", item.name, humanBytes(free)),
			details,
		))
	}
	return out
}

func checkConfig(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	return infoCheck("config",
		fmt.Sprintf("config schema version %d loaded", cfg.Version),
		map[string]any{
			"overlay_cleanup":       cfg.OverlayCleanup,
			"stable_file_mode":      cfg.StableFileMode,
			"poll_interval_seconds": cfg.PollIntervalSeconds,
			"recursive_mount":       cfg.RecursiveMount,
			"control_socket":        cfg.ControlSocket,
			"mount_backend":         cfg.MountBackend,
			"ratarmount_bin":        cfg.EffectiveRatarmountBin(),
		},
	)
}

// checkWebBindSecurity warns when the HTTP UI would listen on a non-loopback
// host with an empty web_token (open API). Loopback binds may omit the token.
// Severity is always warn (never hard-fail). Skipped when config is nil.
func checkWebBindSecurity(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	host := strings.TrimSpace(cfg.WebHost)
	tokenSet := strings.TrimSpace(cfg.WebToken) != ""
	enabled := cfg.WebEnabled
	loopback := isLoopbackHost(host)
	details := map[string]any{
		"web_enabled":   enabled,
		"web_host":      host,
		"web_token_set": tokenSet,
		"loopback":      loopback,
	}
	if !enabled {
		return infoCheck("web_bind_security",
			"web disabled (web_enabled: false); bind security not applicable",
			details)
	}
	displayHost := host
	if displayHost == "" {
		displayHost = "127.0.0.1"
	}
	if loopback {
		msg := fmt.Sprintf("web bind %s is loopback; web_token optional", displayHost)
		if tokenSet {
			msg = fmt.Sprintf("web bind %s is loopback with web_token set", displayHost)
		}
		return infoCheck("web_bind_security", msg, details)
	}
	if !tokenSet {
		return warnCheck("web_bind_security", false,
			fmt.Sprintf("web_host %q is not loopback and web_token is empty — "+
				"API/dashboard are open to any client that can reach the bind address; "+
				"set web_token or bind loopback (see docs/security.md)", displayHost),
			details)
	}
	return infoCheck("web_bind_security",
		fmt.Sprintf("web bind %s is non-loopback with web_token set", displayHost),
		details)
}

// checkConvertDirs probes convert cache / archiveconverter output directories
// when those features are enabled. Returns nil when nothing is enabled.
func checkConvertDirs(opts *Options) []CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return nil
	}
	var out []CheckResult
	convertOn := cfg.Convert7zNonsolid || cfg.ConvertZipTo7z
	if convertOn {
		cache := convert.DefaultNonsolidCacheDir(cfg)
		// Label matches config key; check name is the public doctor id.
		c := checkOnePath(opts, "convert_cache_dir", "convert_7z_cache_dir", cache)
		if c.Details == nil {
			c.Details = map[string]any{}
		}
		c.Details["convert_7z_nonsolid"] = cfg.Convert7zNonsolid
		c.Details["convert_zip_to_7z"] = cfg.ConvertZipTo7z
		c.Details["resolved_cache_dir"] = cache
		out = append(out, c)
	}
	if cfg.ArchiveconverterEnabled {
		outDir := strings.TrimSpace(cfg.ArchiveconverterOutputDir)
		if outDir != "" {
			c := checkOnePath(opts, "path.archiveconverter_output_dir", "archiveconverter_output_dir", outDir)
			if c.Details == nil {
				c.Details = map[string]any{}
			}
			c.Details["archiveconverter_enabled"] = true
			out = append(out, c)
		}
	}
	return out
}

// checkControlSocketLive probes whether the configured control_socket path
// exists and accepts a short status request. Offline-safe: missing path,
// dial failure, or auth denial are severity warn (never hard-fail).
// Skipped when Config is nil or control_socket is empty.
func checkControlSocketLive(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	sock := strings.TrimSpace(cfg.ControlSocket)
	if sock == "" {
		return CheckResult{}
	}
	name := CheckNameControlSocketLive
	details := map[string]any{
		"path": sock,
	}

	if !opts.pathExists()(sock) {
		return warnCheck(name, false,
			fmt.Sprintf("control socket %s not found (serve not running or path wrong)", sock),
			details)
	}
	details["exists"] = true

	resp, err := opts.controlRequest()(sock, "status")
	if err != nil {
		details["error"] = err.Error()
		if ce, ok := err.(*control.Error); ok && ce.Code != "" {
			details["code"] = ce.Code
		}
		return warnCheck(name, false,
			fmt.Sprintf("control socket %s not reachable (serve not running?): %v", sock, err),
			details)
	}
	if resp == nil {
		return warnCheck(name, false,
			fmt.Sprintf("control socket %s returned empty status response", sock),
			details)
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		code, _ := resp["code"].(string)
		msg, _ := resp["error"].(string)
		if msg == "" {
			msg = "request failed"
		}
		details["code"] = code
		details["error"] = msg
		if code == "PERMISSION_DENIED" {
			group := control.DefaultAuthGroup
			details["auth_group"] = group
			return warnCheck(name, false,
				fmt.Sprintf("control socket reachable but auth denied — run as root or member of group %s (%s)",
					group, msg),
				details)
		}
		return warnCheck(name, false,
			fmt.Sprintf("control socket status failed: %s", msg),
			details)
	}

	var version string
	if data, _ := resp["data"].(map[string]any); data != nil {
		if v, ok := data["version"].(string); ok {
			version = v
		}
		if pid, ok := asJSONInt(data["pid"]); ok {
			details["pid"] = pid
		}
	}
	details["reachable"] = true
	if version != "" {
		details["version"] = version
	}
	msg := fmt.Sprintf("control socket reachable at %s", sock)
	if version != "" {
		msg += " (serve " + version + ")"
	}
	return infoCheck(name, msg, details)
}

// asJSONInt coerces JSON number types from control responses to int.
func asJSONInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// checkControlSocketPathLength warns on Darwin when control_socket is long
// enough to risk sun_path overflow (~104 bytes). No-op on other platforms
// or when config/socket is empty.
func checkControlSocketPathLength(opts *Options) CheckResult {
	cfg := opts.Config
	if cfg == nil {
		return CheckResult{}
	}
	plat := opts.platform()
	if !platform.IsDarwin(plat) {
		return CheckResult{}
	}
	sock := strings.TrimSpace(cfg.ControlSocket)
	if sock == "" {
		return infoCheck("control_socket_path_length",
			"macOS: control_socket empty (set a short path under Caches; see docs/macos.md)",
			map[string]any{"platform": "darwin", "length": 0},
		)
	}
	n := len(sock)
	details := map[string]any{
		"path":       sock,
		"length":     n,
		"warn_above": darwinSunPathWarnLen,
		"platform":   "darwin",
	}
	if n > darwinSunPathWarnLen {
		return warnCheck("control_socket_path_length", false,
			fmt.Sprintf("control_socket path is %d bytes (warn > %d); macOS sun_path is ~104 including NUL — "+
				"bind may fail with \"filename too long\"; keep socket under "+
				"~/Library/Caches/mount-wrapper/run/ (docs/macos.md)", n, darwinSunPathWarnLen),
			details)
	}
	return infoCheck("control_socket_path_length",
		fmt.Sprintf("control_socket path length %d is within macOS sun_path limit", n),
		details)
}

// --- helpers ---

// isLoopbackHost reports whether host is a loopback address or name.
// Mirrors internal/api (unexported there) so doctor stays free of api deps.
func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	switch h {
	case "127.0.0.1", "localhost", "::1", "":
		return true
	}
	h = strings.Trim(h, "[]")
	if h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func probeBinVersion(opts *Options, path string) string {
	out, err := runBinTimed(opts, path, 10*time.Second, "--version")
	if err != nil && out == "" {
		return ""
	}
	line := firstLine(out)
	return truncate(line, 120)
}

func probeSupportsForeground(opts *Options, path string) *bool {
	out, err := runBinTimed(opts, path, 15*time.Second, "--help")
	if err != nil && out == "" {
		return nil
	}
	lower := strings.ToLower(out)
	v := strings.Contains(lower, "-f") || strings.Contains(lower, "foreground")
	return &v
}

func runBinTimed(opts *Options, path string, timeout time.Duration, args ...string) (string, error) {
	if opts != nil && opts.RunBin != nil {
		return opts.RunBin(path, args...)
	}
	return runBinWithTimeout(path, timeout, args...)
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolOrNil(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}

func humanBytes(n int64) string {
	if n < 0 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
