package archives

import (
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// ArchivesDirPath returns the configured archives directory, or "" if unset.
func ArchivesDirPath(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.ArchivesDir) == "" {
		return ""
	}
	return cfg.ArchivesDir
}

// IsArchivesPath reports whether path lives under archives_dir (after resolve).
func IsArchivesPath(cfg *config.Config, path string) bool {
	root := ArchivesDirPath(cfg)
	if root == "" || path == "" {
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

// IsConvertedOutputPath reports whether path lives under archiveconverter_output_dir.
func IsConvertedOutputPath(cfg *config.Config, path string) bool {
	if cfg == nil {
		return false
	}
	raw := strings.TrimSpace(cfg.ArchiveconverterOutputDir)
	if raw == "" || path == "" {
		return false
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)
	rootResolved, err := filepath.Abs(raw)
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
