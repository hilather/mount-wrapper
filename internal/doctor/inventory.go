package doctor

// CoreCheckNames is the always-on diagnostic inventory in Run order when no
// Config is loaded. Keep in sync with the first checks block in Run (run.go).
//
// Config-dependent checks are appended later by Run; their names are frozen
// below as CheckName* constants. Unit tests (TestDoctorCheckInventory) assert
// presence, gating, and that new probes warn rather than hard-fail.
var CoreCheckNames = []string{
	"go_version",
	"host_platform",
	"peercred",
	"fuse_device",
	"fusermount",
	"user_allow_other",
	"ratarmount_bin",
	"archiveconverter",
	"sevenzip_bin",
	"mount_backend",
	"systemd_pid1",
	"service_user",
}

// Config-gated / platform-gated check names (not always present).
// Keep in sync with Run and the check* helpers in checks.go.
const (
	// CheckNameWebBindSecurity is emitted whenever Config is non-nil.
	CheckNameWebBindSecurity = "web_bind_security"
	// CheckNameConvertCacheDir is emitted when convert_7z_nonsolid or
	// convert_zip_to_7z is enabled.
	CheckNameConvertCacheDir = "convert_cache_dir"
	// CheckNameArchiveconverterOutputDir is emitted when
	// archiveconverter_enabled is true and archiveconverter_output_dir is set.
	CheckNameArchiveconverterOutputDir = "path.archiveconverter_output_dir"
	// CheckNameControlSocketPathLength is emitted on Darwin when Config is
	// non-nil (including empty control_socket).
	CheckNameControlSocketPathLength = "control_socket_path_length"
	// CheckNameControlSocketLive is emitted when Config is non-nil and
	// control_socket is non-empty. Probes path existence + short status
	// dial; missing serve / dial fail / auth deny are warn (never hard-fail).
	CheckNameControlSocketLive = "control_socket_live"
	// CheckNameSystemdUnit is emitted on non-Darwin hosts when PID 1 is
	// systemd. Best-effort systemctl is-active / is-enabled for
	// mount-wrapper.service; inactive / unavailable → warn (never hard-fail).
	CheckNameSystemdUnit = "systemd_unit"
	// CheckNameLaunchdAgent is emitted on Darwin only. Best-effort launchctl
	// list/print for the packaging example label (DefaultLaunchdLabel);
	// not loaded / launchctl missing / unclassifiable → warn (never hard-fail);
	// clear list/print loaded shape → info.
	CheckNameLaunchdAgent = "launchd_agent"
	// CheckNamePidfileLive is emitted when Config is non-nil and pid_file is
	// non-empty. Stats the path, parses the PID, and probes process liveness;
	// missing / stale / unreadable → warn (never hard-fail).
	CheckNamePidfileLive = "pidfile_live"
	// CheckNameConfig is the trailing schema summary when Config is non-nil.
	CheckNameConfig = "config"
	// CheckNameFixSystemd is emitted only when Options.FixSystemd is true.
	CheckNameFixSystemd = "fix_systemd"
	// CheckNameIndexLayout is emitted whenever Config is non-nil.
	CheckNameIndexLayout = "index_layout"
	// CheckNameWindowsVisibleParentOX is emitted whenever Config is non-nil.
	// On Linux with windows_visible, walks mount_root ancestors for o+x;
	// macOS is info-only; windows_visible false is info "not required".
	CheckNameWindowsVisibleParentOX = "windows_visible_parent_ox"
)

// DefaultSystemdUnit is the packaged unit name probed by checkSystemdUnit.
const DefaultSystemdUnit = "mount-wrapper.service"

// DefaultLaunchdLabel is the launchd user-agent Label from
// packaging/launchd/com.hilather.mount-wrapper.plist.example, probed by
// checkLaunchdAgent on Darwin.
const DefaultLaunchdLabel = "com.hilather.mount-wrapper"


// Name prefixes for config-dependent path/disk/source checks (exact suffix
// depends on keys and free-space dedupe).
const (
	PathCheckPrefix  = "path."
	DiskCheckPrefix  = "disk."
	SourceDirsPrefix = "source_dirs"
)
