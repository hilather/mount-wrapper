package metrics

import (
	"os"
	"path/filepath"
)

// ComputeArchiveMetrics builds ArchiveMetrics from an ArchiveInput using the
// given size and extracted-size providers (parity compute_archive_metrics).
//
// Extracted primary size (used for space_saved):
//   - Prefer deep leaf from the index when nested content is fully known
//     (no opaque nested archive members).
//   - When the index has opaque nested members (or PreferMount), promote a mount
//     walk for mounted/walkable paths so browsable nested content is counted.
//   - Fall back to shallow index extract with incomplete/opaque signals rather
//     than inventing deep sizes from packed nested blobs.
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
		shallowSize   *int64
		deepSize      *int64
		source        string
		nesting       string
		deepComplete  *bool
		opaqueCount   int
		opaqueBytes   int64
		errMsg        string
		indexAnalysis IndexExtractedAnalysis
		haveAnalysis  bool
	)

	// --- Index analysis (deep/shallow/opaque) ---
	if indexPresent {
		if analyzer, ok := extracted.(IndexAnalyzer); ok {
			indexAnalysis = analyzer.AnalyzeIndex(in.IndexPath)
			haveAnalysis = true
		} else {
			// Scalar provider: treat FromIndex as complete deep == primary.
			sz, err := extracted.FromIndex(in.IndexPath)
			if sz != nil {
				indexAnalysis = IndexExtractedAnalysis{
					NaiveSum:     sz,
					Shallow:      sz,
					DeepLeaf:     sz,
					DeepComplete: true,
				}
				haveAnalysis = true
			} else {
				errMsg = err
			}
		}
		if haveAnalysis && indexAnalysis.ErrMsg != "" {
			errMsg = indexAnalysis.ErrMsg
			haveAnalysis = false
		}
		if haveAnalysis {
			shallowSize = indexAnalysis.Shallow
			deepSize = indexAnalysis.DeepLeaf
			opaqueCount = indexAnalysis.OpaqueNestedCount
			opaqueBytes = indexAnalysis.OpaqueNestedBytes
			dc := indexAnalysis.DeepComplete
			deepComplete = &dc
		}
	}

	indexDeepIncomplete := haveAnalysis && !indexAnalysis.DeepComplete
	promoteMount := indexDeepIncomplete && statusLikelyMounted(in.Status)
	mountWalk := opts.MountWalk != nil && *opts.MountWalk

	// PreferMount: try mount first.
	if opts.PreferMount && mountWalk && in.MountPath != "" {
		if shouldWalkMount(in.MountPath, true, false) {
			sz, err := extracted.FromMount(in.MountPath)
			if sz != nil {
				extractedSize = sz
				source = ExtractedSourceMount
				nesting = NestingMount
				t := true
				deepComplete = &t
				errMsg = ""
			} else if errMsg == "" {
				errMsg = err
			}
		}
	}

	// Default path: index primary when deep complete (or incomplete without walk).
	if extractedSize == nil && !opts.PreferMount && haveAnalysis {
		if indexAnalysis.DeepComplete {
			extractedSize = indexAnalysis.DeepLeaf
			source = ExtractedSourceIndex
			nesting = NestingDeep
			errMsg = ""
		} else {
			// Incomplete: try mount promotion for live mounts before falling back.
			if mountWalk && in.MountPath != "" && shouldWalkMount(in.MountPath, false, promoteMount) {
				sz, err := extracted.FromMount(in.MountPath)
				if sz != nil {
					extractedSize = sz
					source = ExtractedSourceMount
					nesting = NestingMount
					t := true
					deepComplete = &t
					errMsg = ""
				} else if errMsg == "" {
					errMsg = err
				}
			}
			if extractedSize == nil {
				// Honest shallow: do not invent deep from packed nested blobs.
				extractedSize = indexAnalysis.Shallow
				source = ExtractedSourceIndex
				nesting = indexAnalysis.NestingLabel()
				errMsg = ""
			}
		}
	}

	// Index missing / analysis failed: legacy scalar FromIndex + mount fallback.
	if extractedSize == nil && !opts.PreferMount && !haveAnalysis && indexPresent {
		sz, err := extracted.FromIndex(in.IndexPath)
		if sz != nil {
			extractedSize = sz
			source = ExtractedSourceIndex
			nesting = NestingDeep
			t := true
			deepComplete = &t
			shallowSize = sz
			deepSize = sz
			errMsg = ""
		} else if errMsg == "" {
			errMsg = err
		}
	}

	// Mount walk when index missing/failed (original fallback).
	if extractedSize == nil && mountWalk && in.MountPath != "" {
		if shouldWalkMount(in.MountPath, opts.PreferMount, promoteMount) {
			sz, err := extracted.FromMount(in.MountPath)
			if sz != nil {
				extractedSize = sz
				source = ExtractedSourceMount
				nesting = NestingMount
				t := true
				deepComplete = &t
				errMsg = ""
			} else if errMsg == "" {
				errMsg = err
			}
		}
	}

	// PreferMount walk failed: fall back to index primary.
	if extractedSize == nil && opts.PreferMount && haveAnalysis {
		extractedSize = indexAnalysis.Primary()
		if extractedSize != nil {
			source = ExtractedSourceIndex
			nesting = indexAnalysis.NestingLabel()
			errMsg = ""
		}
	}
	if extractedSize == nil && opts.PreferMount && indexPresent && !haveAnalysis {
		sz, err := extracted.FromIndex(in.IndexPath)
		if sz != nil {
			extractedSize = sz
			source = ExtractedSourceIndex
			nesting = NestingDeep
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
		ArchiveID:                 in.ArchiveID,
		ArchivePath:               in.ArchivePath,
		ArchiveBasename:           in.ArchiveBasename,
		Status:                    in.Status,
		MountPath:                 in.MountPath,
		ArchiveSizeBytes:          archSize,
		IndexSizeBytes:            idxSize,
		ExtractedSizeBytes:        extractedSize,
		ExtractedSizeShallowBytes: shallowSize,
		ExtractedSizeDeepBytes:    deepSize,
		SpaceSavedBytes:           saved,
		SpaceSavedVsArchiveBytes:  savedVsArch,
		ConvertSourceSizeBytes:    convertSource,
		ConvertSizeDeltaBytes:     convertDelta,
		ConvertDurationSeconds:    convertDuration,
		IndexPath:                 in.IndexPath,
		IndexPresent:              indexPresent,
		ExtractedSource:           source,
		ExtractedNesting:          nesting,
		ExtractedDeepComplete:     deepComplete,
		OpaqueNestedCount:         opaqueCount,
		OpaqueNestedBytes:         opaqueBytes,
		Error:                     errMsg,
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

// statusLikelyMounted reports statuses where a mount path may hold browsable
// nested content for deep extracted promotion.
func statusLikelyMounted(status string) bool {
	return status == StatusMounted || status == StatusHooksRunning
}

// shouldWalkMount mirrors Python: walk real mounts always; plain dirs when
// prefer_mount (tests) or when promoteIncomplete (index deep incomplete + live
// mount status). MapExtractedProvider fakes: missing path allowed when
// preferMount or promoteIncomplete so tests inject FromMount without real dirs.
func shouldWalkMount(mountPath string, preferMount, promoteIncomplete bool) bool {
	if mountPath == "" {
		return false
	}
	st, err := os.Stat(mountPath)
	if err != nil {
		return preferMount || promoteIncomplete
	}
	if !st.IsDir() {
		return false
	}
	if preferMount || promoteIncomplete {
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
