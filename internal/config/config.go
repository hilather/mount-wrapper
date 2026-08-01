package config

import (
	"fmt"
	"regexp"
	"strings"
)

// ConfigError is returned for invalid configuration or I/O failures during load.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	if e == nil {
		return "config error"
	}
	return e.Message
}

func configErrorf(format string, args ...any) *ConfigError {
	return &ConfigError{Message: fmt.Sprintf(format, args...)}
}

// Enumerated mode sets (YAML string values).
const (
	StableFileTwoScans = "two_scans"
	StableFileMinAge   = "min_age"
	StableFileBoth     = "both"

	OverlayCleanupQuarantine = "quarantine"
	OverlayCleanupDelete     = "delete"
	OverlayCleanupRetain     = "retain"

	OnContentRemountResetHooks = "remount_reset_hooks"
	OnContentRemountKeepHooks  = "remount_keep_hooks"

	HooksCwdMount      = "mount"
	HooksCwdArchiveDir = "archive_dir"
	HooksCwdHooksDir   = "hooks_dir"

	Convert7zScopeNested  = "nested"
	Convert7zScopeOuter   = "outer"
	Convert7zScopeFlatten = "flatten"
	Convert7zScopeAll     = "all"

	ArchiveconverterModeConvert       = "convert"
	ArchiveconverterModeConvertSingle = "convert-single"

	ArchiveconverterBackendCLI    = "cli"
	ArchiveconverterBackendNative = "native"
)

// Valid sets for enum fields.
var (
	StableFileModes = map[string]struct{}{
		StableFileTwoScans: {},
		StableFileMinAge:   {},
		StableFileBoth:     {},
	}
	OverlayCleanupModes = map[string]struct{}{
		OverlayCleanupQuarantine: {},
		OverlayCleanupDelete:     {},
		OverlayCleanupRetain:     {},
	}
	OnContentChangeModes = map[string]struct{}{
		OnContentRemountResetHooks: {},
		OnContentRemountKeepHooks:  {},
	}
	HooksCwdModes = map[string]struct{}{
		HooksCwdMount:      {},
		HooksCwdArchiveDir: {},
		HooksCwdHooksDir:   {},
	}
	LogLevels = map[string]struct{}{
		"CRITICAL": {},
		"ERROR":    {},
		"WARNING":  {},
		"INFO":     {},
		"DEBUG":    {},
	}
	ArchiveconverterModes = map[string]struct{}{
		ArchiveconverterModeConvert:       {},
		ArchiveconverterModeConvertSingle: {},
	}
	ArchiveconverterBackends = map[string]struct{}{
		ArchiveconverterBackendCLI:    {},
		ArchiveconverterBackendNative: {},
	}
	Convert7zScopes = map[string]struct{}{
		Convert7zScopeNested:  {},
		Convert7zScopeOuter:   {},
		Convert7zScopeFlatten: {},
		Convert7zScopeAll:     {},
	}
)

// Config is validated mount-wrapper configuration (schema version 1).
// Duration-valued YAML keys are stored as *_Seconds float64 fields.
type Config struct {
	Version int

	SourceDirs                    []string
	MountRoot                     string
	IndexDir                      string
	OverlayDir                    string
	StateDB                       string
	ArchivesDir                   string
	MoveArchivesToLinux           bool
	ArchiveRelocateOverheadBytes  int
	NameRegex                     string
	Recursive                     bool
	RecursiveMount                bool
	RecursiveMountExtensions      []string
	IndexSmallestFirst            bool
	PollIntervalSeconds           float64
	ReconcileIntervalSeconds      float64
	UseInotify                    bool
	StableFileMode                string
	MinFileAgeSeconds             float64
	ContentFingerprint            bool
	OnContentChange               string
	WriteOverlay                  bool
	WindowsVisible                bool
	AllowIndexesOnDrvfs           bool
	CleanupAfterSeconds           float64
	OverlayCleanup                string
	QuarantineRetainForSeconds    float64
	QuarantineMaxBytes            int
	MinFreeBytes                  int
	MaxArchiveBytes               int
	MaxConcurrentIndex            int
	MaxConcurrentConvert          int
	MaxConcurrentMount            int
	MaxMountAttempts              int
	MountReadyTimeoutSeconds      float64
	UnmountTimeoutSeconds         float64
	MountBackend                  string
	RatarmountBin                 string
	RatarmountIndexWorkers        int
	RatarmountDebug               int
	Ratarmount7zDebug             bool
	RatarmountLogDir              string
	RatarmountRustLog             string
	Convert7zNonsolid             bool
	Convert7zScope                string
	Convert7zBin                  string
	Convert7zCacheDir             string
	Convert7zOverheadBytes        int
	Convert7zFlattenExtractBuffer int
	Convert7zInnerPrefixStrip     string
	Convert7zFlattenExclude       []string
	ConvertZipTo7z                bool
	ExtraRatarmountArgs           []string

	ArchiveconverterEnabled              bool
	ArchiveconverterBin                  string
	ArchiveconverterOutputDir            string
	ArchiveconverterMode                 string
	ArchiveconverterBackend              string
	ArchiveconverterLevel                int
	ArchiveconverterThreads              *int // nil = omit (tool auto)
	ArchiveconverterVerify               bool
	ArchiveconverterRequired             bool
	ArchiveconverterTempDir              string
	ArchiveconverterNativePipeline       string
	ArchiveconverterNativeCodec          string
	ArchiveconverterNativeLargeThreshold int
	ArchiveconverterNestedConcurrency    *int // nil = omit
	ArchiveconverterNestedSizeBudget     string
	ArchiveconverterBasenameMatch        bool
	ArchiveconverterExcludeInner         []string
	ArchiveconverterExcludeOuter         []string
	ArchiveconverterRename               []string
	ArchiveconverterExtraArgs            []string
	ArchiveconverterOverheadBytes        int
	ArchiveconverterTimeoutSeconds       float64

	HooksDir            string
	HooksParallel       bool
	HooksStopOnHardFail bool
	HookTimeoutSeconds  float64
	HookMaxRetries      int
	HookRerunOnFailure  bool
	HooksCwd            string

	ControlSocket string
	PIDFile       string

	WebEnabled bool
	WebHost    string
	WebPort    int
	WebToken   string

	LogLevel     string
	StrictConfig bool

	// ConfigPath is the original path the config was loaded from (not a YAML key).
	ConfigPath string
	// UnknownKeys are keys ignored in warn mode (strict_config: false).
	UnknownKeys []string
}

// EffectiveRatarmountBin returns the binary path/name for the configured backend.
// Explicit non-empty RatarmountBin wins; empty falls back to DefaultRatarmountBin.
// Full PATH / sibling resolution is left to the mounter/doctor packages.
func (c *Config) EffectiveRatarmountBin() string {
	if c == nil {
		return DefaultRustRatarmountBin
	}
	configured := strings.TrimSpace(c.RatarmountBin)
	if configured == "" {
		return DefaultRatarmountBin(c.MountBackend)
	}
	return configured
}

// CompiledNameRegex compiles NameRegex. Valid configs always succeed.
func (c *Config) CompiledNameRegex() (*regexp.Regexp, error) {
	return regexp.Compile(c.NameRegex)
}
