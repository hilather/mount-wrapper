package service

import (
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/metrics"
)

// ConvertSidecarMeta implements metrics.ConvertMetaProvider by reading the
// convert metadata sidecar.
//
// Resolution order for a given archivePath:
//  1. Sidecar next to archivePath (convert.MetadataPath) — covers in-place /
//     relocated convert results where store.archive_path already points at the
//     converted file.
//  2. When Config is set and outer/all nonsolid cache would use a different
//     dest, sidecar next to convert.ResolveMountArchivePath (cache path).
//
// metrics.ComputeArchiveMetrics already prefers convert fields from the store
// (ArchiveInput.ConvertSourceSizeBytes / ConvertDurationSeconds) when both are
// set; this provider supplies original size, size delta, and duration from the
// sidecar when those store fields are incomplete or absent.
type ConvertSidecarMeta struct {
	// Config is optional. When non-nil, outer nonsolid cache dest is also
	// checked after a miss on archivePath.
	Config *config.Config
}

// ReadConvertMetadata implements metrics.ConvertMetaProvider.
func (m ConvertSidecarMeta) ReadConvertMetadata(archivePath string) *metrics.ConvertMetadata {
	if got := mapConvertMeta(convert.ReadConvertMetadata(archivePath)); got != nil {
		return got
	}
	if m.Config == nil || archivePath == "" {
		return nil
	}
	// Outer/all nonsolid scope may keep store archive_path as the source while
	// writing the sidecar next to the cache dest used for mount.
	cachePath := convert.ResolveMountArchivePath(m.Config, archivePath)
	if cachePath == "" || cachePath == archivePath {
		return nil
	}
	return mapConvertMeta(convert.ReadConvertMetadata(cachePath))
}

func mapConvertMeta(cm *convert.ConvertMetadata) *metrics.ConvertMetadata {
	if cm == nil {
		return nil
	}
	return &metrics.ConvertMetadata{
		OriginalSizeBytes:      cm.OriginalSizeBytes,
		SizeDeltaBytes:         cm.SizeDeltaBytes,
		ConvertDurationSeconds: cm.ConvertDurationSeconds,
	}
}

// Ensure ConvertSidecarMeta satisfies metrics.ConvertMetaProvider.
var _ metrics.ConvertMetaProvider = ConvertSidecarMeta{}
