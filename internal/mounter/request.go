package mounter

import (
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/paths"
)

// MountRequest holds parameters for one ratarmount / ratarmount-rs invocation.
type MountRequest struct {
	ArchiveID                string
	ArchivePath              string
	IndexPath                string
	OverlayPath              string // empty when write overlay disabled
	MountPath                string
	AllowOther               bool
	IndexWorkers             int
	RecursiveMount           bool
	RecursiveMountExtensions []string
	ExtraArgs                []string
	RatarmountBin            string
	MountBackend             string
	RatarmountDebug          int
	RatarmountLogDir         string
	IndexOnly                bool
}

// RequestFromConfig builds a MountRequest from config and archive identity.
//
// mountName, when non-empty, is used as the mount directory basename; otherwise
// paths.SanitizeMountName is applied using archiveBasename and takenMountNames.
// ratarmountBin, when non-empty, overrides config.EffectiveRatarmountBin().
func RequestFromConfig(
	cfg *config.Config,
	archiveID, archivePath, archiveBasename string,
	takenMountNames map[string]struct{},
	mountName string,
	ratarmountBin string,
) MountRequest {
	if cfg == nil {
		cfg = &config.Config{}
	}
	name := mountName
	if name == "" {
		name = paths.SanitizeMountName(archiveBasename, archiveID, takenMountNames)
	}
	indexPath := filepath.Join(cfg.IndexDir, archiveID+".index.sqlite")
	overlayPath := ""
	if cfg.WriteOverlay {
		overlayPath = filepath.Join(cfg.OverlayDir, archiveID)
	}
	mountPath := filepath.Join(cfg.MountRoot, name)

	bin := strings.TrimSpace(ratarmountBin)
	if bin == "" {
		bin = cfg.EffectiveRatarmountBin()
	}
	backend := cfg.MountBackend
	if backend == "" {
		backend = BackendRust
	}

	exts := append([]string(nil), cfg.RecursiveMountExtensions...)
	extras := append([]string(nil), cfg.ExtraRatarmountArgs...)

	return MountRequest{
		ArchiveID:                archiveID,
		ArchivePath:              archivePath,
		IndexPath:                indexPath,
		OverlayPath:              overlayPath,
		MountPath:                mountPath,
		AllowOther:               cfg.WindowsVisible,
		IndexWorkers:             cfg.RatarmountIndexWorkers,
		RecursiveMount:           cfg.RecursiveMount,
		RecursiveMountExtensions: exts,
		ExtraArgs:                extras,
		RatarmountBin:            bin,
		MountBackend:             backend,
		RatarmountDebug:          cfg.RatarmountDebug,
		RatarmountLogDir:         cfg.RatarmountLogDir,
		IndexOnly:                false,
	}
}
