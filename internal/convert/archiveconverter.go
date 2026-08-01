package convert

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// DefaultArchiveconverterName is the PATH name for archiveconverter.
const DefaultArchiveconverterName = "archiveconverter"

// IsSevenzPath reports whether path has a .7z suffix (case-insensitive).
func IsSevenzPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".7z")
}

// ConvertedDirPath returns the directory for non-solid converted archives.
func ConvertedDirPath(cfg *config.Config) string {
	if cfg == nil {
		return config.DefaultPaths["archiveconverter_output_dir"]
	}
	if d := strings.TrimSpace(cfg.ArchiveconverterOutputDir); d != "" {
		return d
	}
	return config.DefaultPaths["archiveconverter_output_dir"]
}

// ConvertedFilePath returns `{output_dir}/{archiveID}.7z`.
func ConvertedFilePath(cfg *config.Config, archiveID string) string {
	return filepath.Join(ConvertedDirPath(cfg), archiveID+".7z")
}

// IsConvertedPath reports whether path lives under archiveconverter_output_dir.
// Does not import archives (avoids import cycle); same path rules as archives.IsConvertedOutputPath.
func IsConvertedPath(cfg *config.Config, path string) bool {
	if cfg == nil || path == "" {
		return false
	}
	root := strings.TrimSpace(cfg.ArchiveconverterOutputDir)
	if root == "" {
		return false
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)
	rootResolved, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rootResolved = filepath.Clean(rootResolved)
	if resolved == rootResolved {
		return true
	}
	prefix := rootResolved + string(filepath.Separator)
	return strings.HasPrefix(resolved, prefix)
}

// ExistingConvertedPath returns a usable prior conversion product, if present and non-empty.
func ExistingConvertedPath(cfg *config.Config, archiveID string) string {
	if archiveID == "" {
		return ""
	}
	dest := ConvertedFilePath(cfg, archiveID)
	st, err := os.Stat(dest)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 {
		return ""
	}
	return dest
}

// ResolveArchiveconverterBin locates the archiveconverter binary.
//
// Priority (parity with converter.resolve_archiveconverter_bin):
//  1. Explicit non-empty configured path (returned even if missing, after exec check)
//  2. Default ~/.local/bin/archiveconverter when executable
//  3. ExtraCandidates / SiblingRelease when executable
//  4. PATH search for "archiveconverter" when enabled
//  5. Empty string when unavailable (auto mode)
func ResolveArchiveconverterBin(configured string, opts ResolveOptions) string {
	which := whichOf(opts)
	isExec := execOf(opts)

	if configured = strings.TrimSpace(configured); configured != "" {
		// Explicit override: prefer resolved executable path; else return as-is.
		if candidate := resolveIfExecutable(configured, which, isExec); candidate != "" {
			return candidate
		}
		return configured
	}

	defaultBin := config.DefaultArchiveconverterBin()
	if candidate := resolveIfExecutable(defaultBin, which, isExec); candidate != "" {
		return candidate
	}

	for _, c := range opts.ExtraCandidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if candidate := resolveIfExecutable(c, which, isExec); candidate != "" {
			return candidate
		}
	}

	if sib := strings.TrimSpace(opts.SiblingRelease); sib != "" && isExec(sib) {
		return sib
	}

	if !opts.SearchPathDisabled {
		if p := which(DefaultArchiveconverterName); p != "" {
			return p
		}
	}
	return ""
}

// EffectiveArchiveconverterBin resolves binary for cfg.
// Empty or default home path is treated as auto-detect (parity with
// effective_archiveconverter_bin).
func EffectiveArchiveconverterBin(cfg *config.Config, opts ResolveOptions) string {
	var configured string
	if cfg != nil {
		configured = strings.TrimSpace(cfg.ArchiveconverterBin)
	}
	defaultBin := config.DefaultArchiveconverterBin()
	if configured == "" || configured == defaultBin {
		return ResolveArchiveconverterBin("", opts)
	}
	return ResolveArchiveconverterBin(configured, opts)
}

// ArchiveconverterAvailable reports whether conversion can be attempted.
func ArchiveconverterAvailable(cfg *config.Config, opts ResolveOptions) bool {
	if cfg == nil || !cfg.ArchiveconverterEnabled {
		return false
	}
	bin := EffectiveArchiveconverterBin(cfg, opts)
	if bin == "" {
		return false
	}
	return execOf(opts)(bin)
}

// ShouldConvert reports whether solid→non-solid conversion should run before index.
//
// Only for .7z when enabled, binary available, a fresh index is needed, and the
// archive is not already a converted product (parity with converter.should_convert).
func ShouldConvert(cfg *config.Config, archivePath string, needsIndex bool, opts ResolveOptions) bool {
	if cfg == nil || !cfg.ArchiveconverterEnabled {
		return false
	}
	if !needsIndex {
		return false
	}
	if !IsSevenzPath(archivePath) {
		return false
	}
	if IsConvertedPath(cfg, archivePath) {
		return false
	}
	if !ArchiveconverterAvailable(cfg, opts) {
		return false
	}
	return true
}

// BuildConvertCmd builds archiveconverter argv (does not start a process).
// bin must already be resolved (non-empty). Mode, backend, and knobs come from cfg.
func BuildConvertCmd(cfg *config.Config, bin, inputPath, outputPath string) ([]string, error) {
	if strings.TrimSpace(bin) == "" {
		return nil, convertErrorf("build_cmd", "archiveconverter binary not found")
	}
	if cfg == nil {
		return nil, convertErrorf("build_cmd", "config is nil")
	}
	mode := strings.TrimSpace(cfg.ArchiveconverterMode)
	if mode == "" {
		mode = config.ArchiveconverterModeConvert
	}
	cmd := []string{bin, mode, inputPath, "-o", outputPath}

	if mode == config.ArchiveconverterModeConvert {
		for _, pattern := range cfg.ArchiveconverterExcludeInner {
			cmd = append(cmd, "--exclude-inner", pattern)
		}
		for _, pattern := range cfg.ArchiveconverterExcludeOuter {
			cmd = append(cmd, "--exclude-outer", pattern)
		}
		for _, rule := range cfg.ArchiveconverterRename {
			cmd = append(cmd, "--rename", rule)
		}
		if cfg.ArchiveconverterBasenameMatch {
			cmd = append(cmd, "--basename-match")
		}
	} else {
		// convert-single: --exclude for inner-only patterns.
		for _, pattern := range cfg.ArchiveconverterExcludeInner {
			cmd = append(cmd, "--exclude", pattern)
		}
	}

	backend := strings.TrimSpace(cfg.ArchiveconverterBackend)
	if backend == "" {
		backend = config.ArchiveconverterBackendNative
	}
	cmd = append(cmd, "--backend", backend)
	cmd = append(cmd, "--level", strconv.Itoa(cfg.ArchiveconverterLevel))
	if cfg.ArchiveconverterThreads != nil {
		cmd = append(cmd, "--threads", strconv.Itoa(*cfg.ArchiveconverterThreads))
	}
	if cfg.ArchiveconverterVerify {
		cmd = append(cmd, "--verify")
	}
	if td := strings.TrimSpace(cfg.ArchiveconverterTempDir); td != "" {
		cmd = append(cmd, "--temp-dir", td)
	}

	if mode == config.ArchiveconverterModeConvert {
		if cfg.ArchiveconverterNestedConcurrency != nil {
			cmd = append(cmd, "--nested-concurrency", strconv.Itoa(*cfg.ArchiveconverterNestedConcurrency))
		}
		if budget := strings.TrimSpace(cfg.ArchiveconverterNestedSizeBudget); budget != "" {
			cmd = append(cmd, "--nested-size-budget", budget)
		}
	}

	if backend == config.ArchiveconverterBackendNative {
		if pipeline := strings.TrimSpace(cfg.ArchiveconverterNativePipeline); pipeline != "" {
			cmd = append(cmd, "--native-pipeline", pipeline)
		}
		if codec := strings.TrimSpace(cfg.ArchiveconverterNativeCodec); codec != "" {
			cmd = append(cmd, "--native-codec", codec)
		}
		if cfg.ArchiveconverterNativeLargeThreshold > 0 {
			cmd = append(cmd, "--native-large-threshold",
				strconv.Itoa(cfg.ArchiveconverterNativeLargeThreshold))
		}
	}

	cmd = append(cmd, cfg.ArchiveconverterExtraArgs...)
	return cmd, nil
}

// ArchiveconverterTimeout returns the process timeout duration seconds from cfg.
// 0 means no timeout (caller passes nil to exec).
func ArchiveconverterTimeout(cfg *config.Config) float64 {
	if cfg == nil {
		return 0
	}
	if cfg.ArchiveconverterTimeoutSeconds < 0 {
		return 0
	}
	return cfg.ArchiveconverterTimeoutSeconds
}
