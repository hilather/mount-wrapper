package service

import (
	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/state"
)

// storeMetricsSource adapts state.Store to metrics.ArchiveSource.
type storeMetricsSource struct {
	store *state.Store
}

func (s *storeMetricsSource) Get(archiveID string) (*metrics.ArchiveInput, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	rec, err := s.store.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	in := archiveRecordToInput(rec)
	return &in, nil
}

func (s *storeMetricsSource) List(statuses []string) ([]metrics.ArchiveInput, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	var filter any
	if len(statuses) > 0 {
		filter = statuses
	}
	recs, err := s.store.ListArchives(filter)
	if err != nil {
		return nil, err
	}
	out := make([]metrics.ArchiveInput, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		out = append(out, archiveRecordToInput(rec))
	}
	return out, nil
}

func archiveRecordToInput(rec *state.ArchiveRecord) metrics.ArchiveInput {
	in := metrics.ArchiveInput{
		ArchiveID:              rec.ArchiveID,
		ArchivePath:            rec.ArchivePath,
		ArchiveBasename:        rec.ArchiveBasename,
		Status:                 rec.Status,
		ConvertSourceSizeBytes: rec.ConvertSourceSizeBytes,
		ConvertDurationSeconds: rec.ConvertDurationSeconds,
	}
	if rec.MountPath != nil {
		in.MountPath = *rec.MountPath
	}
	if rec.IndexPath != nil {
		in.IndexPath = *rec.IndexPath
	}
	return in
}
