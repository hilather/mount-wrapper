package metrics

import (
	"os"
	"path/filepath"
)

// ComputeArchiveMetrics builds ArchiveMetrics from an ArchiveInput using the
// given size and extracted-size providers (parity compute_archive_metrics).
//
// Providers must be non-nil; use FSSizeProvider / DefaultExtractedProvider for
// production, or Map* fakes in tests.
func ComputeArchiveMetrics(
	in ArchiveInput,
	sizes SizeProvider,
	extracted ExtractedSizeProvider,
	meta ConvertMetaProvider,
	opts ComputeOptions,
) ArchiveMetrics {
	opts = NormalizeComputeOptions(opts)
	if sizes == nil {
		sizes = FSSizeProvider{}
	}
	if extracted == nil {
		extracted = DefaultExtractedProvider{}
	}
	if meta == nil {
		meta = NoConvertMeta{}
	}

	archSize := sizes.FileSize(in.ArchivePath)
	idxSize := sizes.IndexSize(in.IndexPath)
	indexPresent := in.IndexPath != "" && indexFilePresent(in.IndexPath, sizes)

	var (
		extractedSize *int64
		source        string
		errMsg        string
	)

	// Prefer index (fast, works unmounted) unless PreferMount is set.
	if !opts.PreferMount && indexPresent {
		extractedSize, errMsg = extracted.FromIndex(in.IndexPath)
		if extractedSize != nil {
			source = ExtractedSourceIndex
			errMsg = ""
		}
	}

	mountWalk := opts.MountWalk != nil && *opts.MountWalk
	if extractedSize == nil && mountWalk && in.MountPath != "" {
		if shouldWalkMount(in.MountPath, opts.PreferMount) {
			sz, err := extracted.FromMount(in.MountPath)
			if sz != nil {
				extractedSize = sz
				source = ExtractedSourceMount
				errMsg = ""
			} else if errMsg == "" {
				errMsg = err
			}
		}
	}

	// If prefer_mount and walk failed/skipped, fall back to index.
	if extractedSize == nil && opts.PreferMount && indexPresent {
		sz, err := extracted.FromIndex(in.IndexPath)
		if sz != nil {
			extractedSize = sz
			source = ExtractedSourceIndex
			errMsg = ""
		} else if errMsg == "" {
			errMsg = err
		}
	}

	// Index size for space-saved: use measured size, or 0 when present-but-missing,
	// or nil when never set (parity).
	indexForSaved := idxSize
	if indexForSaved == nil && indexPresent {
		z := int64(0)
		indexForSaved = &z
	}
	saved, savedVsArch := ComputeSpaceSaved(extractedSize, indexForSaved, archSize)

	// Normalize index_size: if path set but size nil, 0.
	if in.IndexPath != "" && idxSize == nil {
		z := int64(0)
		idxSize = &z
	}

	var convertMeta *ConvertMetadata
	if in.ConvertSourceSizeBytes == nil || in.ConvertDurationSeconds == nil {
		convertMeta = meta.ReadConvertMetadata(in.ArchivePath)
	}
	convertSource, convertDelta, convertDuration := ResolveConvertFields(
		archSize,
		in.ConvertSourceSizeBytes,
		in.ConvertDurationSeconds,
		convertMeta,
	)

	return ArchiveMetrics{
		ArchiveID:                in.ArchiveID,
		ArchivePath:              in.ArchivePath,
		ArchiveBasename:          in.ArchiveBasename,
		Status:                   in.Status,
		MountPath:                in.MountPath,
		ArchiveSizeBytes:         archSize,
		IndexSizeBytes:           idxSize,
		ExtractedSizeBytes:       extractedSize,
		SpaceSavedBytes:          saved,
		SpaceSavedVsArchiveBytes: savedVsArch,
		ConvertSourceSizeBytes:   convertSource,
		ConvertSizeDeltaBytes:    convertDelta,
		ConvertDurationSeconds:   convertDuration,
		IndexPath:                in.IndexPath,
		IndexPresent:             indexPresent,
		ExtractedSource:          source,
		Error:                    errMsg,
	}
}

// IndexPresence is optionally implemented by SizeProvider to report whether an
// index file exists (distinct from size 0 for a missing path).
type IndexPresence interface {
	IndexPresent(indexPath string) bool
}

// indexFilePresent reports whether the index path exists as a regular file.
func indexFilePresent(indexPath string, sizes SizeProvider) bool {
	if indexPath == "" {
		return false
	}
	if ip, ok := sizes.(IndexPresence); ok {
		return ip.IndexPresent(indexPath)
	}
	// Default: filesystem check (FSSizeProvider and unknown providers).
	st, err := os.Stat(indexPath)
	return err == nil && st.Mode().IsRegular()
}

// shouldWalkMount mirrors Python: walk real mounts always; plain dirs only when
// prefer_mount (tests). For MapExtractedProvider, always allow when path set
// and PreferMount or when the path is an actual mount/dir.
func shouldWalkMount(mountPath string, preferMount bool) bool {
	if mountPath == "" {
		return false
	}
	st, err := os.Stat(mountPath)
	if err != nil {
		// Fake providers may not have real dirs; allow when preferMount so tests
		// can inject FromMount results without creating directories.
		return preferMount
	}
	if !st.IsDir() {
		return false
	}
	// Best-effort ismount: check if path is listed as a mount (Linux/macOS).
	// Parity allows non-ismount plain dirs when prefer_mount.
	if preferMount {
		return true
	}
	return isMountPoint(mountPath)
}

// isMountPoint is a best-effort check. On failure returns false (prefer index).
// Real FUSE detection is platform-specific; production callers with PreferMount
// false still walk when isMountPoint is true.
func isMountPoint(path string) bool {
	// Compare device of path vs parent — classic ismount heuristic.
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	parent := filepath.Dir(path)
	pst, err := os.Stat(parent)
	if err != nil {
		return false
	}
	// Use Sys for dev; if unavailable, false.
	return differentDevice(st, pst)
}
