package state

import (
	"database/sql"
)

// ArchiveRecord is one row from the archives table.
type ArchiveRecord struct {
	ArchiveID              string
	SourceDir              string
	ArchivePath            string
	ArchiveBasename        string
	SizeBytes              int64
	MtimeNs                int64
	Fingerprint            string
	IndexPath              *string
	OverlayPath            *string
	MountPath              *string
	Status                 string
	MountRetryable         bool
	MountAttempts          int
	LastSeenAt             string
	RemovedAt              *string
	FirstMountedAt         *string
	HooksStatus            string
	HooksCompletedAt       *string
	LastError              *string
	MountPID               *int64
	IndexStartedAt         *string
	IndexDurationSeconds   *float64
	MountDurationSeconds   *float64
	ConvertSourceSizeBytes *int64
	ConvertDurationSeconds *float64
	CreatedAt              string
	UpdatedAt              string
}

// HookRecord is one row from the hooks table.
type HookRecord struct {
	ArchiveID    string
	HookName     string
	Status       string
	Attempts     int
	LastExitCode *int
	LastRunAt    *string
	LastError    *string
}

func scanArchive(row interface {
	Scan(dest ...any) error
}) (*ArchiveRecord, error) {
	var (
		r                      ArchiveRecord
		indexPath              sql.NullString
		overlayPath            sql.NullString
		mountPath              sql.NullString
		removedAt              sql.NullString
		firstMountedAt         sql.NullString
		hooksCompletedAt       sql.NullString
		lastError              sql.NullString
		mountPID               sql.NullInt64
		indexStartedAt         sql.NullString
		indexDurationSeconds   sql.NullFloat64
		mountDurationSeconds   sql.NullFloat64
		convertSourceSizeBytes sql.NullInt64
		convertDurationSeconds sql.NullFloat64
		mountRetryable         int
	)
	err := row.Scan(
		&r.ArchiveID,
		&r.SourceDir,
		&r.ArchivePath,
		&r.ArchiveBasename,
		&r.SizeBytes,
		&r.MtimeNs,
		&r.Fingerprint,
		&indexPath,
		&overlayPath,
		&mountPath,
		&r.Status,
		&mountRetryable,
		&r.MountAttempts,
		&r.LastSeenAt,
		&removedAt,
		&firstMountedAt,
		&r.HooksStatus,
		&hooksCompletedAt,
		&lastError,
		&mountPID,
		&indexStartedAt,
		&r.CreatedAt,
		&r.UpdatedAt,
		&indexDurationSeconds,
		&mountDurationSeconds,
		&convertSourceSizeBytes,
		&convertDurationSeconds,
	)
	if err != nil {
		return nil, err
	}
	r.MountRetryable = mountRetryable != 0
	r.IndexPath = nullStringPtr(indexPath)
	r.OverlayPath = nullStringPtr(overlayPath)
	r.MountPath = nullStringPtr(mountPath)
	r.RemovedAt = nullStringPtr(removedAt)
	r.FirstMountedAt = nullStringPtr(firstMountedAt)
	r.HooksCompletedAt = nullStringPtr(hooksCompletedAt)
	r.LastError = nullStringPtr(lastError)
	r.MountPID = nullInt64Ptr(mountPID)
	r.IndexStartedAt = nullStringPtr(indexStartedAt)
	r.IndexDurationSeconds = nullFloat64Ptr(indexDurationSeconds)
	r.MountDurationSeconds = nullFloat64Ptr(mountDurationSeconds)
	r.ConvertSourceSizeBytes = nullInt64Ptr(convertSourceSizeBytes)
	r.ConvertDurationSeconds = nullFloat64Ptr(convertDurationSeconds)
	return &r, nil
}

// archivesSelect is SELECT * column order matching scanArchive (table order after v6).
// After 004 rebuild + 005/006 adds, SQLite column order is:
// base columns from 001, then index_duration, mount_duration (in 004 rebuild),
// then convert_source_size_bytes (005), convert_duration_seconds (006).
// 001 order ends with index_started_at, created_at, updated_at; 002/003 added
// duration cols; 004 rebuild includes them before convert cols.
const archivesSelect = `
SELECT
  archive_id, source_dir, archive_path, archive_basename,
  size_bytes, mtime_ns, fingerprint,
  index_path, overlay_path, mount_path,
  status, mount_retryable, mount_attempts,
  last_seen_at, removed_at, first_mounted_at,
  hooks_status, hooks_completed_at, last_error,
  mount_pid, index_started_at, created_at, updated_at,
  index_duration_seconds, mount_duration_seconds,
  convert_source_size_bytes, convert_duration_seconds
FROM archives`

func scanHook(row interface {
	Scan(dest ...any) error
}) (*HookRecord, error) {
	var (
		h            HookRecord
		lastExitCode sql.NullInt64
		lastRunAt    sql.NullString
		lastError    sql.NullString
	)
	err := row.Scan(
		&h.ArchiveID,
		&h.HookName,
		&h.Status,
		&h.Attempts,
		&lastExitCode,
		&lastRunAt,
		&lastError,
	)
	if err != nil {
		return nil, err
	}
	if lastExitCode.Valid {
		v := int(lastExitCode.Int64)
		h.LastExitCode = &v
	}
	h.LastRunAt = nullStringPtr(lastRunAt)
	h.LastError = nullStringPtr(lastError)
	return &h, nil
}

func nullStringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func nullFloat64Ptr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}
