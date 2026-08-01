-- tarmount-wsl schema version 1
-- Applied by StateStore.open(); PRAGMA journal_mode/foreign_keys set in code.

CREATE TABLE schema_version (
  version INTEGER NOT NULL
);

CREATE TABLE archives (
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
  CHECK (status IN (
    'discovered', 'indexing', 'index_failed', 'mounting', 'mount_failed',
    'mounted', 'hooks_running', 'unmounting', 'absent'
  )),
  CHECK (hooks_status IN (
    'none', 'pending', 'running', 'success', 'failed', 'retry'
  )),
  CHECK (mount_retryable IN (0, 1)),
  CHECK (mount_attempts >= 0)
);

CREATE TABLE hooks (
  archive_id     TEXT NOT NULL REFERENCES archives(archive_id) ON DELETE CASCADE,
  hook_name      TEXT NOT NULL,
  status         TEXT NOT NULL,
  attempts       INTEGER NOT NULL DEFAULT 0,
  last_exit_code INTEGER,
  last_run_at    TEXT,
  last_error     TEXT,
  PRIMARY KEY (archive_id, hook_name),
  CHECK (status IN (
    'pending', 'running', 'success', 'failed', 'retry', 'skipped'
  )),
  CHECK (attempts >= 0)
);

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE INDEX idx_archives_status ON archives(status);
CREATE INDEX idx_archives_removed ON archives(removed_at);
CREATE INDEX idx_archives_path ON archives(archive_path);
