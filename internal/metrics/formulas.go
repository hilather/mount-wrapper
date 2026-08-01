package metrics

// ComputeSpaceSaved returns (space_saved_bytes, space_saved_vs_archive_bytes).
//
// Formulas (parity with tarmount-wsl compute_space_saved):
//
//	space_saved_bytes            = max(0, extracted − index)           when both known
//	space_saved_vs_archive_bytes = max(0, extracted − archive − index) when all three known
//
// Missing inputs yield nil for the corresponding result. Index is required for
// both formulas (mount disk cost always includes the index).
func ComputeSpaceSaved(extracted, index, archive *int64) (spaceSaved, spaceSavedVsArchive *int64) {
	if extracted != nil && index != nil {
		v := *extracted - *index
		if v < 0 {
			v = 0
		}
		spaceSaved = &v
	}
	if extracted != nil && archive != nil && index != nil {
		v := *extracted - *archive - *index
		if v < 0 {
			v = 0
		}
		spaceSavedVsArchive = &v
	}
	return spaceSaved, spaceSavedVsArchive
}

// ConvertSizeDeltaBytes returns archive_size − convert_source when both are known.
// Otherwise returns nil.
func ConvertSizeDeltaBytes(archiveSize, convertSource *int64) *int64 {
	if archiveSize == nil || convertSource == nil {
		return nil
	}
	v := *archiveSize - *convertSource
	return &v
}

// ResolveConvertFields fills convert metrics from record fields, optional
// convert metadata (sidecar), and archive size.
//
// Parity with compute_archive_metrics convert block:
//   - If convert_source or convert_duration is missing, try meta.
//   - When meta supplies original size (and source was missing), also take meta size delta.
//   - When both source and duration are already present on the record, delta is
//     archive_size − convert_source (when archive size is known).
func ResolveConvertFields(
	archiveSize *int64,
	convertSource *int64,
	convertDuration *float64,
	meta *ConvertMetadata,
) (source *int64, delta *int64, duration *float64) {
	source = convertSource
	duration = convertDuration
	var outDelta *int64

	if source == nil || duration == nil {
		if meta != nil {
			if source == nil {
				src := meta.OriginalSizeBytes
				source = &src
				d := meta.SizeDeltaBytes
				outDelta = &d
			}
			if duration == nil && meta.ConvertDurationSeconds != nil {
				duration = meta.ConvertDurationSeconds
			}
		}
	} else if archiveSize != nil {
		outDelta = ConvertSizeDeltaBytes(archiveSize, source)
	}
	return source, outDelta, duration
}

// ConvertMetadata is optional convert sidecar / external metadata.
type ConvertMetadata struct {
	OriginalSizeBytes      int64
	SizeDeltaBytes         int64
	ConvertDurationSeconds *float64
}

// Summarize aggregates metrics into a Summary (parity with MetricsService.summary).
//
// Convert totals use Python-style "or None": zero totals become nil so empty
// convert cohorts do not look like a measured zero.
func Summarize(items []ArchiveMetrics) Summary {
	var (
		totalArchive       int64
		totalIndex         int64
		totalExtracted     int64
		totalSaved         int64
		totalConvertSource int64
		totalConvertDelta  int64
		nWithExtracted     int
		nWithConvert       int
		nWithConvertDur    int
		maxConvertDuration float64
	)
	for i := range items {
		m := &items[i]
		if m.ArchiveSizeBytes != nil {
			totalArchive += *m.ArchiveSizeBytes
		}
		if m.IndexSizeBytes != nil {
			totalIndex += *m.IndexSizeBytes
		}
		if m.ExtractedSizeBytes != nil {
			totalExtracted += *m.ExtractedSizeBytes
			nWithExtracted++
		}
		if m.SpaceSavedBytes != nil {
			totalSaved += *m.SpaceSavedBytes
		}
		if m.ConvertSourceSizeBytes != nil {
			totalConvertSource += *m.ConvertSourceSizeBytes
			nWithConvert++
		}
		if m.ConvertSizeDeltaBytes != nil {
			totalConvertDelta += *m.ConvertSizeDeltaBytes
		}
		if m.ConvertDurationSeconds != nil {
			nWithConvertDur++
			if *m.ConvertDurationSeconds > maxConvertDuration {
				maxConvertDuration = *m.ConvertDurationSeconds
			}
		}
	}

	s := Summary{
		ArchiveCount:                len(items),
		ArchivesWithExtractedSize:   nWithExtracted,
		ArchivesWithConvertMetadata: nWithConvert,
		TotalArchiveSizeBytes:       totalArchive,
		TotalIndexSizeBytes:         totalIndex,
		TotalExtractedSizeBytes:     totalExtracted,
		TotalSpaceSavedBytes:        totalSaved,
	}
	// Python: total_convert_source_size_bytes: total_convert_source or None
	if totalConvertSource != 0 {
		s.TotalConvertSourceSizeBytes = &totalConvertSource
	}
	if totalConvertDelta != 0 {
		s.TotalConvertSizeDeltaBytes = &totalConvertDelta
	}
	// Python: n_with_convert_duration or None; max_convert_duration or None
	// (a pure 0.0 max is treated as absent).
	if nWithConvertDur != 0 {
		s.ArchivesWithConvertDuration = &nWithConvertDur
	}
	if maxConvertDuration != 0 {
		s.MaxConvertDurationSeconds = &maxConvertDuration
	}
	return s
}
