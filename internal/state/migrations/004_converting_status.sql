-- Schema version 4: add converting archive status (flatten/repack in progress).

PRAGMA foreign_keys=OFF;

CREATE TABLE archives_new (
  archive_id         TEXT PRIMARY KEY,
  source_dir         TEXT NOT NULL,
  archive_path       TEXT NOT NULL UNIQUE,
  archive_basename   TEXT NOT NULL,
  size_bytes         INTEGER NOT NULL,
  mtime_ns           INTEGER NOT NULL,
  fingerprint        TEXT NOT NULL,
  index_path         TEXT,
  overlay_path       TEXT,
  mount_path         TEXT,
  status             TEXT NOT NULL,
  mount_retryable    INTEGER NOT NULL DEFAULT 1,
  mount_attempts     INTEGER NOT NULL DEFAULT 0,
  last_seen_at       TEXT NOT NULL,
  removed_at         TEXT,
  first_mounted_at   TEXT,
  hooks_status       TEXT NOT NULL DEFAULT 'none',
  hooks_completed_at TEXT,
  last_error         TEXT,
  mount_pid          INTEGER,
  index_started_at   TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL,
  index_duration_seconds REAL,
  mount_duration_seconds REAL,
  CHECK (status IN (
    'discovered', 'indexing', 'index_failed', 'mounting', 'mount_failed',
    'mounted', 'hooks_running', 'unmounting', 'absent', 'converting'
  )),
  CHECK (hooks_status IN (
    'none', 'pending', 'running', 'success', 'failed', 'retry'
  )),
  CHECK (mount_retryable IN (0, 1)),
  CHECK (mount_attempts >= 0)
);

INSERT INTO archives_new (
  archive_id, source_dir, archive_path, archive_basename,
  size_bytes, mtime_ns, fingerprint,
  index_path, overlay_path, mount_path,
  status, mount_retryable, mount_attempts,
  last_seen_at, removed_at, first_mounted_at,
  hooks_status, hooks_completed_at, last_error,
  mount_pid, index_started_at, created_at, updated_at,
  index_duration_seconds, mount_duration_seconds
)
SELECT
  archive_id, source_dir, archive_path, archive_basename,
  size_bytes, mtime_ns, fingerprint,
  index_path, overlay_path, mount_path,
  status, mount_retryable, mount_attempts,
  last_seen_at, removed_at, first_mounted_at,
  hooks_status, hooks_completed_at, last_error,
  mount_pid, index_started_at, created_at, updated_at,
  index_duration_seconds, mount_duration_seconds
FROM archives;

DROP TABLE archives;
ALTER TABLE archives_new RENAME TO archives;

CREATE INDEX idx_archives_status ON archives(status);
CREATE INDEX idx_archives_removed ON archives(removed_at);
CREATE INDEX idx_archives_path ON archives(archive_path);

PRAGMA foreign_keys=ON;
