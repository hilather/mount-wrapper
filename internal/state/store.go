package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed archive lifecycle store.
//
// Only the serve process should write in production. This type is the single
// writer API; callers must not share a Store across goroutines without external
// serialization (v1 is a single-threaded service loop).
type Store struct {
	db     *sql.DB
	DBPath string // empty for in-memory
	// txnDepth tracks nested Transaction calls (SQLite SAVEPOINT not used in v1).
	txnDepth int
}

// Open opens (or creates) the state database at path and applies migrations.
func Open(path string) (*Store, error) {
	return openDB(path, true)
}

// OpenMemory opens an in-memory database (tests) and applies migrations.
func OpenMemory() (*Store, error) {
	return openDB(":memory:", true)
}

// OpenNoMigrate opens a database without applying migrations (advanced/tests).
func OpenNoMigrate(path string) (*Store, error) {
	return openDB(path, false)
}

func openDB(path string, migrate bool) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, stateErrorf("create state db dir: %v", err)
		}
		// modernc DSN: plain path works; enable foreign keys via PRAGMA after open.
		dsn = path
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, stateErrorf("open sqlite: %v", err)
	}
	// Single connection: serialize all access; matches single-writer rule.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, stateErrorf("pragma foreign_keys: %v", err)
	}
	// WAL is best-effort (may fail on some exotic FS).
	_, _ = db.Exec(`PRAGMA journal_mode = WAL`)

	dbPath := path
	if path == ":memory:" {
		dbPath = ""
	}
	s := &Store{db: db, DBPath: dbPath}
	if migrate {
		if _, err := s.Migrate(); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// DB returns the underlying *sql.DB for advanced/test use. Prefer Store methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// SchemaVersion returns the applied schema version (0 if unmigrated).
func (s *Store) SchemaVersion() (int, error) {
	row := s.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`)
	var v int
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		// Table missing or other error — treat missing table as 0 via detect.
		return s.detectSchemaVersion()
	}
	return v, nil
}

// Migrate applies pending migrations. Returns the resulting schema version.
func (s *Store) Migrate() (int, error) {
	current, err := s.detectSchemaVersion()
	if err != nil {
		return 0, err
	}
	if current > CurrentSchemaVersion {
		return 0, schemaErrorf(
			"database schema version %d is newer than supported %d; upgrade the package",
			current, CurrentSchemaVersion,
		)
	}
	needed, err := MigrationsNeeded(current, CurrentSchemaVersion)
	if err != nil {
		return 0, schemaErrorf("%v", err)
	}
	for _, version := range needed {
		sqlText, err := MigrationSQL(version)
		if err != nil {
			return 0, schemaErrorf("load migration %d: %v", version, err)
		}
		// Do not wrap in Transaction — multi-statement rebuilds (004) manage
		// their own pragmas; match Python executescript behavior.
		if err := s.execScript(sqlText); err != nil {
			return 0, schemaErrorf("apply migration %d: %v", version, err)
		}
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
			return 0, schemaErrorf("schema_version count after %d: %v", version, err)
		}
		if count == 0 {
			if _, err := s.db.Exec(`INSERT INTO schema_version(version) VALUES (?)`, version); err != nil {
				return 0, schemaErrorf("seed schema_version %d: %v", version, err)
			}
		} else {
			if _, err := s.db.Exec(`UPDATE schema_version SET version = ?`, version); err != nil {
				return 0, schemaErrorf("update schema_version %d: %v", version, err)
			}
		}
	}
	return s.SchemaVersion()
}

func (s *Store) execScript(script string) error {
	for _, stmt := range splitSQLStatements(script) {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("%w\nstatement: %s", err, truncate(stmt, 200))
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (s *Store) detectSchemaVersion() (int, error) {
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&name)
	if err == sql.ErrNoRows {
		var archives string
		err2 := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name='archives'`,
		).Scan(&archives)
		if err2 == nil {
			return 0, schemaErrorf("archives table exists without schema_version; manual recovery required")
		}
		if err2 != sql.ErrNoRows {
			return 0, stateErrorf("detect archives table: %v", err2)
		}
		return 0, nil
	}
	if err != nil {
		return 0, stateErrorf("detect schema_version table: %v", err)
	}
	var v int
	err = s.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&v)
	if err == sql.ErrNoRows {
		// Table exists but empty mid-migration — treat as 0 only if no archives.
		return 0, nil
	}
	if err != nil {
		return 0, stateErrorf("read schema version: %v", err)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// Transaction begins an immediate transaction; commits on success, rolls back
// on error. Nested calls reuse the outer transaction (no SAVEPOINT in v1).
func (s *Store) Transaction(fn func() error) (err error) {
	if s.txnDepth > 0 {
		s.txnDepth++
		defer func() { s.txnDepth-- }()
		return fn()
	}
	s.txnDepth = 1
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		s.txnDepth = 0
		return stateErrorf("begin: %v", err)
	}
	defer func() {
		s.txnDepth = 0
		if err != nil {
			_, _ = s.db.Exec(`ROLLBACK`)
			return
		}
		if _, cErr := s.db.Exec(`COMMIT`); cErr != nil {
			_, _ = s.db.Exec(`ROLLBACK`)
			err = stateErrorf("commit: %v", cErr)
		}
	}()
	err = fn()
	return err
}

// ---------------------------------------------------------------------------
// Meta
// ---------------------------------------------------------------------------

// GetMeta returns a meta value or nil if missing.
func (s *Store) GetMeta(key string) (*string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, stateErrorf("get meta %q: %v", key, err)
	}
	return &v, nil
}

// SetMeta upserts a meta key/value.
func (s *Store) SetMeta(key, value string) error {
	return s.Transaction(func() error {
		_, err := s.db.Exec(`
			INSERT INTO meta(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`, key, value)
		if err != nil {
			return stateErrorf("set meta %q: %v", key, err)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Archives — read
// ---------------------------------------------------------------------------

// GetArchive returns an archive by id, or nil if not found.
func (s *Store) GetArchive(archiveID string) (*ArchiveRecord, error) {
	row := s.db.QueryRow(archivesSelect+` WHERE archive_id = ?`, archiveID)
	rec, err := scanArchive(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, stateErrorf("get archive: %v", err)
	}
	return rec, nil
}

// GetArchiveByPath returns an archive by unique archive_path, or nil if not found.
func (s *Store) GetArchiveByPath(archivePath string) (*ArchiveRecord, error) {
	row := s.db.QueryRow(archivesSelect+` WHERE archive_path = ?`, archivePath)
	rec, err := scanArchive(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, stateErrorf("get archive by path: %v", err)
	}
	return rec, nil
}

// ListArchives returns archives ordered by created_at, archive_id.
// statusFilter may be nil (all), a string, or []string.
func (s *Store) ListArchives(statusFilter any) ([]*ArchiveRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch v := statusFilter.(type) {
	case nil:
		rows, err = s.db.Query(archivesSelect + ` ORDER BY created_at, archive_id`)
	case string:
		rows, err = s.db.Query(
			archivesSelect+` WHERE status = ? ORDER BY created_at, archive_id`, v,
		)
	case []string:
		if len(v) == 0 {
			return nil, nil
		}
		placeholders := placeholders(len(v))
		args := make([]any, len(v))
		for i, st := range v {
			args[i] = st
		}
		q := archivesSelect + ` WHERE status IN (` + placeholders + `) ORDER BY created_at, archive_id`
		rows, err = s.db.Query(q, args...)
	default:
		return nil, stateErrorf("status filter must be nil, string, or []string; got %T", statusFilter)
	}
	if err != nil {
		return nil, stateErrorf("list archives: %v", err)
	}
	defer rows.Close()
	return scanArchives(rows)
}

// ListAbsentPastGrace returns absent rows whose removed_at is at or before beforeISO.
func (s *Store) ListAbsentPastGrace(beforeISO string) ([]*ArchiveRecord, error) {
	rows, err := s.db.Query(archivesSelect+`
		WHERE status = 'absent'
		  AND removed_at IS NOT NULL
		  AND removed_at <= ?
		ORDER BY removed_at, archive_id
	`, beforeISO)
	if err != nil {
		return nil, stateErrorf("list absent past grace: %v", err)
	}
	defer rows.Close()
	return scanArchives(rows)
}

func scanArchives(rows *sql.Rows) ([]*ArchiveRecord, error) {
	var out []*ArchiveRecord
	for rows.Next() {
		rec, err := scanArchive(rows)
		if err != nil {
			return nil, stateErrorf("scan archive: %v", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, stateErrorf("list archives rows: %v", err)
	}
	if out == nil {
		out = []*ArchiveRecord{}
	}
	return out, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// ---------------------------------------------------------------------------
// Archives — create / update / transition
// ---------------------------------------------------------------------------

// InsertDiscoveredParams holds fields for a newly discovered archive.
type InsertDiscoveredParams struct {
	SourceDir       string
	ArchivePath     string
	ArchiveBasename string
	SizeBytes       int64
	MtimeNs         int64
	Fingerprint     string
	ArchiveID       string // optional; generated if empty
	IndexPath       *string
	OverlayPath     *string
	MountPath       *string
	Now             string // optional; UTCNowISO if empty
}

// InsertDiscovered inserts a newly discovered archive in discovered status.
func (s *Store) InsertDiscovered(p InsertDiscoveredParams) (*ArchiveRecord, error) {
	aid := p.ArchiveID
	if aid == "" {
		aid = NewArchiveID()
	}
	ts := p.Now
	if ts == "" {
		ts = UTCNowISO()
	}
	err := s.Transaction(func() error {
		_, err := s.db.Exec(`
			INSERT INTO archives (
			  archive_id, source_dir, archive_path, archive_basename,
			  size_bytes, mtime_ns, fingerprint,
			  index_path, overlay_path, mount_path,
			  status, mount_retryable, mount_attempts,
			  last_seen_at, removed_at, first_mounted_at,
			  hooks_status, hooks_completed_at, last_error,
			  mount_pid, index_started_at, created_at, updated_at
			) VALUES (
			  ?, ?, ?, ?,
			  ?, ?, ?,
			  ?, ?, ?,
			  'discovered', 1, 0,
			  ?, NULL, NULL,
			  'none', NULL, NULL,
			  NULL, NULL, ?, ?
			)
		`,
			aid, p.SourceDir, p.ArchivePath, p.ArchiveBasename,
			p.SizeBytes, p.MtimeNs, p.Fingerprint,
			nullStr(p.IndexPath), nullStr(p.OverlayPath), nullStr(p.MountPath),
			ts, ts, ts,
		)
		if err != nil {
			return stateErrorf("cannot insert archive_path=%q: %v", p.ArchivePath, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetArchive(aid)
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Transition moves an archive to toStatus, optionally patching fields.
//
// expected may be nil (no optimistic lock), a string, or []string.
// If expected is set, the row must currently be in that status (or one of those);
// otherwise TransitionError is raised (optimistic lock). Status self-transitions
// are allowed and only patch fields.
//
// now empty uses UTCNowISO(). fields may be nil.
//
// Auto-fields (parity with Python):
//   - removed_at when → absent (if not in fields)
//   - clear removed_at when absent → discovered
//   - index_started_at when → indexing or mounting (if not in fields)
//   - clear index_duration_seconds when newly entering indexing
//   - first_mounted_at when → mounted/hooks_running from indexing/mounting if unset
//
// Callers clear mount_pid explicitly when leaving PID statuses (Python does not
// auto-clear mount_pid inside Transition).
func (s *Store) Transition(archiveID, toStatus string, expected any, fields map[string]any, now string) (*ArchiveRecord, error) {
	if _, ok := ARCHIVE_STATUSES[toStatus]; !ok {
		return nil, transitionErrorf("unknown to_status %q", toStatus)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	// Copy so we can mutate mount_retryable coercion without side effects.
	fields = cloneFields(fields)

	var unknown []string
	for k := range fields {
		if _, ok := updatableFields[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, stateErrorf("unknown update fields: %v", unknown)
	}

	if hs, ok := fields["hooks_status"]; ok {
		hsStr, _ := hs.(string)
		if _, ok := HOOKS_STATUSES[hsStr]; !ok {
			return nil, stateErrorf("invalid hooks_status %q", hs)
		}
	}
	if mr, ok := fields["mount_retryable"]; ok {
		fields["mount_retryable"] = boolToInt(mr)
	}

	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}

	err := s.Transaction(func() error {
		row := s.db.QueryRow(archivesSelect+` WHERE archive_id = ?`, archiveID)
		rec, err := scanArchive(row)
		if err == sql.ErrNoRows {
			return notFoundErrorf("archive_id=%q not found", archiveID)
		}
		if err != nil {
			return stateErrorf("select archive for transition: %v", err)
		}

		current := rec.Status
		if expected != nil {
			expectedSet, err := normalizeExpected(expected)
			if err != nil {
				return err
			}
			if _, ok := expectedSet[current]; !ok {
				list := sortedKeys(expectedSet)
				return transitionErrorf(
					"optimistic lock failed for %s: status is %q, expected %v",
					archiveID, current, list,
				)
			}
		}

		if err := ValidateTransition(current, toStatus); err != nil {
			return err
		}

		auto := map[string]any{}
		if toStatus == StatusAbsent {
			if _, ok := fields["removed_at"]; !ok {
				auto["removed_at"] = ts
			}
		}
		if toStatus == StatusDiscovered && current == StatusAbsent {
			if _, ok := fields["removed_at"]; !ok {
				auto["removed_at"] = nil
			}
		}
		// Python: if to_status in ("indexing", "mounting") and "index_started_at" not in fields
		if toStatus == StatusIndexing || toStatus == StatusMounting {
			if _, ok := fields["index_started_at"]; !ok {
				auto["index_started_at"] = ts
			}
		}
		if toStatus == StatusIndexing && current != StatusIndexing {
			if _, ok := fields["index_duration_seconds"]; !ok {
				auto["index_duration_seconds"] = nil
			}
		}
		if (toStatus == StatusMounted || toStatus == StatusHooksRunning) &&
			(current == StatusIndexing || current == StatusMounting) {
			if _, ok := fields["first_mounted_at"]; !ok && rec.FirstMountedAt == nil {
				auto["first_mounted_at"] = ts
			}
		}

		merged := map[string]any{}
		for k, v := range auto {
			merged[k] = v
		}
		for k, v := range fields {
			merged[k] = v
		}

		sets := []string{"status = ?", "updated_at = ?"}
		values := []any{toStatus, ts}
		for key, value := range merged {
			sets = append(sets, key+" = ?")
			values = append(values, value)
		}
		values = append(values, archiveID)

		res, err := s.db.Exec(
			`UPDATE archives SET `+strings.Join(sets, ", ")+` WHERE archive_id = ?`,
			values...,
		)
		if err != nil {
			return stateErrorf("update archive: %v", err)
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return transitionErrorf("update failed for archive_id=%q", archiveID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetArchive(archiveID)
}

func cloneFields(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func boolToInt(v any) int {
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case int:
		if t != 0 {
			return 1
		}
		return 0
	case int64:
		if t != 0 {
			return 1
		}
		return 0
	default:
		// truthy fallback
		if v == nil {
			return 0
		}
		return 1
	}
}

func normalizeExpected(expected any) (map[string]struct{}, error) {
	switch v := expected.(type) {
	case string:
		return setOf(v), nil
	case []string:
		return setOf(v...), nil
	default:
		return nil, stateErrorf("expected status must be string or []string; got %T", expected)
	}
}

// ClaimIndexing is an optimistic claim: discovered → indexing.
func (s *Store) ClaimIndexing(archiveID string, fields map[string]any, now string) (*ArchiveRecord, error) {
	return s.Transition(archiveID, StatusIndexing, StatusDiscovered, fields, now)
}

// ClaimConverting is an optimistic claim: discovered → converting.
// (Explicit helper required by mount-wrapper Phase 2; Python uses generic Transition.)
func (s *Store) ClaimConverting(archiveID string, fields map[string]any, now string) (*ArchiveRecord, error) {
	return s.Transition(archiveID, StatusConverting, StatusDiscovered, fields, now)
}

// ClaimMounting is an optimistic claim into mounting from remount-capable statuses.
// Default expected: mount_failed | mounted | unmounting.
func (s *Store) ClaimMounting(archiveID string, expected any, fields map[string]any, now string) (*ArchiveRecord, error) {
	if expected == nil {
		expected = []string{StatusMountFailed, StatusMounted, StatusUnmounting}
	}
	return s.Transition(archiveID, StatusMounting, expected, fields, now)
}

// TouchSeen updates last_seen_at (and optional stat fields) without status change.
func (s *Store) TouchSeen(archiveID string, sizeBytes, mtimeNs *int64, fingerprint *string, now string) (*ArchiveRecord, error) {
	rec, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFoundErrorf("archive_id=%q not found", archiveID)
	}
	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}
	fields := map[string]any{"last_seen_at": ts}
	if sizeBytes != nil {
		fields["size_bytes"] = *sizeBytes
	}
	if mtimeNs != nil {
		fields["mtime_ns"] = *mtimeNs
	}
	if fingerprint != nil {
		fields["fingerprint"] = *fingerprint
	}
	return s.Transition(archiveID, rec.Status, rec.Status, fields, now)
}

// MarkAbsent moves an archive through unmounting if needed, then to absent.
func (s *Store) MarkAbsent(archiveID string, now string, lastError *string) (*ArchiveRecord, error) {
	rec, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFoundErrorf("archive_id=%q not found", archiveID)
	}
	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}
	fields := map[string]any{
		"removed_at": ts,
		"mount_pid":  nil,
	}
	if lastError != nil {
		fields["last_error"] = *lastError
	}

	switch rec.Status {
	case StatusAbsent:
		return s.Transition(archiveID, StatusAbsent, StatusAbsent, fields, ts)
	case StatusUnmounting:
		return s.Transition(archiveID, StatusAbsent, StatusUnmounting, fields, ts)
	default:
		// Two-step: current → unmounting → absent
		if _, err := s.Transition(archiveID, StatusUnmounting, rec.Status, map[string]any{"mount_pid": nil}, ts); err != nil {
			return nil, err
		}
		return s.Transition(archiveID, StatusAbsent, StatusUnmounting, fields, ts)
	}
}

// Reappear transitions absent → discovered when the path reappears before purge grace.
func (s *Store) Reappear(archiveID string, sizeBytes, mtimeNs int64, fingerprint string, now string) (*ArchiveRecord, error) {
	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}
	return s.Transition(archiveID, StatusDiscovered, StatusAbsent, map[string]any{
		"size_bytes":      sizeBytes,
		"mtime_ns":        mtimeNs,
		"fingerprint":     fingerprint,
		"last_seen_at":    ts,
		"removed_at":      nil,
		"mount_attempts":  0,
		"mount_retryable": true,
		"last_error":      nil,
	}, ts)
}

// ResetMountAttempts clears attempts/retryable; optionally re-queues failed mounts.
// requeue: index_failed → discovered; mount_failed → mounting.
func (s *Store) ResetMountAttempts(archiveID string, requeue bool, now string) (*ArchiveRecord, error) {
	rec, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFoundErrorf("archive_id=%q not found", archiveID)
	}
	fields := map[string]any{
		"mount_attempts":  0,
		"mount_retryable": true,
		"last_error":      nil,
	}
	if !requeue {
		return s.Transition(archiveID, rec.Status, rec.Status, fields, now)
	}
	switch rec.Status {
	case StatusIndexFailed:
		return s.Transition(archiveID, StatusDiscovered, StatusIndexFailed, fields, now)
	case StatusMountFailed:
		return s.Transition(archiveID, StatusMounting, StatusMountFailed, fields, now)
	default:
		return s.Transition(archiveID, rec.Status, rec.Status, fields, now)
	}
}

// ResetAllPresentAttempts resets attempts for all non-absent archives. Returns count.
func (s *Store) ResetAllPresentAttempts(now string) (int, error) {
	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}
	var n int64
	err := s.Transaction(func() error {
		res, err := s.db.Exec(`
			UPDATE archives
			SET mount_attempts = 0,
			    mount_retryable = 1,
			    updated_at = ?
			WHERE status != 'absent'
		`, ts)
		if err != nil {
			return stateErrorf("reset all present attempts: %v", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// RecordContentChange re-enters discovered after a fingerprint change and
// optionally resets hooks. Caller is responsible for unmounting first when
// currently mounted. From mounted/hooks_running this goes via unmounting → discovered.
func (s *Store) RecordContentChange(
	archiveID string,
	sizeBytes, mtimeNs int64,
	fingerprint string,
	resetHooks bool,
	now string,
) (*ArchiveRecord, error) {
	rec, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFoundErrorf("archive_id=%q not found", archiveID)
	}
	ts := now
	if ts == "" {
		ts = UTCNowISO()
	}

	fields := map[string]any{
		"size_bytes":             sizeBytes,
		"mtime_ns":               mtimeNs,
		"fingerprint":            fingerprint,
		"last_seen_at":           ts,
		"mount_attempts":         0,
		"mount_retryable":        true,
		"mount_pid":              nil,
		"index_started_at":       nil,
		"index_duration_seconds": nil,
		"mount_duration_seconds": nil,
		"last_error":             nil,
	}
	if resetHooks {
		fields["first_mounted_at"] = nil
		fields["hooks_status"] = HooksNone
		fields["hooks_completed_at"] = nil
	} else {
		fields["first_mounted_at"] = nullStr(rec.FirstMountedAt)
	}

	var updated *ArchiveRecord
	switch {
	case rec.Status == StatusDiscovered:
		updated, err = s.Transition(archiveID, StatusDiscovered, StatusDiscovered, fields, ts)
	case rec.Status == StatusUnmounting || rec.Status == StatusAbsent ||
		rec.Status == StatusIndexFailed || rec.Status == StatusMountFailed ||
		rec.Status == StatusIndexing || rec.Status == StatusMounting:
		allowed := ALLOWED_TRANSITIONS[rec.Status]
		if _, ok := allowed[StatusDiscovered]; ok || rec.Status == StatusDiscovered {
			updated, err = s.Transition(archiveID, StatusDiscovered, rec.Status, fields, ts)
		} else {
			if _, err = s.Transition(archiveID, StatusUnmounting, rec.Status, map[string]any{"mount_pid": nil}, ts); err != nil {
				return nil, err
			}
			updated, err = s.Transition(archiveID, StatusDiscovered, StatusUnmounting, fields, ts)
		}
	default:
		// mounted / hooks_running / converting (etc.)
		if _, err = s.Transition(archiveID, StatusUnmounting, rec.Status, map[string]any{"mount_pid": nil}, ts); err != nil {
			return nil, err
		}
		updated, err = s.Transition(archiveID, StatusDiscovered, StatusUnmounting, fields, ts)
	}
	if err != nil {
		return nil, err
	}

	if resetHooks {
		if err := s.Transaction(func() error {
			_, err := s.db.Exec(`DELETE FROM hooks WHERE archive_id = ?`, archiveID)
			if err != nil {
				return stateErrorf("delete hooks on content change: %v", err)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	rec2, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec2 != nil {
		return rec2, nil
	}
	return updated, nil
}

// ---------------------------------------------------------------------------
// Purge
// ---------------------------------------------------------------------------

// PurgeArchive DELETEs the archive row (CASCADE hooks). Does not touch filesystem
// artifacts. Caller (cleaner) must unmount and handle index/overlay files first.
// After purge, the same archive_path may be rediscovered with a new id.
func (s *Store) PurgeArchive(archiveID string) error {
	return s.Transaction(func() error {
		res, err := s.db.Exec(`DELETE FROM archives WHERE archive_id = ?`, archiveID)
		if err != nil {
			return stateErrorf("purge archive: %v", err)
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return notFoundErrorf("archive_id=%q not found", archiveID)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Hooks rows
// ---------------------------------------------------------------------------

// ListHooks returns hook rows for an archive ordered by hook_name.
func (s *Store) ListHooks(archiveID string) ([]*HookRecord, error) {
	rows, err := s.db.Query(`
		SELECT archive_id, hook_name, status, attempts,
		       last_exit_code, last_run_at, last_error
		FROM hooks WHERE archive_id = ? ORDER BY hook_name
	`, archiveID)
	if err != nil {
		return nil, stateErrorf("list hooks: %v", err)
	}
	defer rows.Close()
	var out []*HookRecord
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, stateErrorf("scan hook: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, stateErrorf("list hooks rows: %v", err)
	}
	if out == nil {
		out = []*HookRecord{}
	}
	return out, nil
}

// UpsertHookParams holds fields for UpsertHook.
type UpsertHookParams struct {
	Status       string // default pending
	Attempts     *int   // nil: 0 on insert, keep existing on update
	LastExitCode *int   // nil on update keeps existing
	LastRunAt    *string
	LastError    *string
}

// UpsertHook inserts or updates a hook row for an archive.
func (s *Store) UpsertHook(archiveID, hookName string, p UpsertHookParams) (*HookRecord, error) {
	status := p.Status
	if status == "" {
		status = HookPending
	}
	if _, ok := HOOK_ROW_STATUSES[status]; !ok {
		return nil, stateErrorf("invalid hook status %q", status)
	}
	rec, err := s.GetArchive(archiveID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFoundErrorf("archive_id=%q not found", archiveID)
	}

	err = s.Transaction(func() error {
		var existingStatus string
		var existingAttempts int
		var existingExit sql.NullInt64
		var existingRun sql.NullString
		var existingErr sql.NullString
		err := s.db.QueryRow(`
			SELECT status, attempts, last_exit_code, last_run_at, last_error
			FROM hooks WHERE archive_id = ? AND hook_name = ?
		`, archiveID, hookName).Scan(
			&existingStatus, &existingAttempts, &existingExit, &existingRun, &existingErr,
		)
		if err == sql.ErrNoRows {
			attempts := 0
			if p.Attempts != nil {
				attempts = *p.Attempts
			}
			_, err := s.db.Exec(`
				INSERT INTO hooks (
				  archive_id, hook_name, status, attempts,
				  last_exit_code, last_run_at, last_error
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, archiveID, hookName, status, attempts,
				nullInt(p.LastExitCode), nullStr(p.LastRunAt), nullStr(p.LastError))
			if err != nil {
				return stateErrorf("insert hook: %v", err)
			}
			return nil
		}
		if err != nil {
			return stateErrorf("select hook: %v", err)
		}
		attempts := existingAttempts
		if p.Attempts != nil {
			attempts = *p.Attempts
		}
		var exitCode any = nil
		if p.LastExitCode != nil {
			exitCode = *p.LastExitCode
		} else if existingExit.Valid {
			exitCode = existingExit.Int64
		}
		var runAt any = nil
		if p.LastRunAt != nil {
			runAt = *p.LastRunAt
		} else if existingRun.Valid {
			runAt = existingRun.String
		}
		var lastErr any = nil
		if p.LastError != nil {
			lastErr = *p.LastError
		} else if existingErr.Valid {
			lastErr = existingErr.String
		}
		_, err = s.db.Exec(`
			UPDATE hooks SET
			  status = ?,
			  attempts = ?,
			  last_exit_code = ?,
			  last_run_at = ?,
			  last_error = ?
			WHERE archive_id = ? AND hook_name = ?
		`, status, attempts, exitCode, runAt, lastErr, archiveID, hookName)
		if err != nil {
			return stateErrorf("update hook: %v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	hooks, err := s.ListHooks(archiveID)
	if err != nil {
		return nil, err
	}
	for _, h := range hooks {
		if h.HookName == hookName {
			return h, nil
		}
	}
	return nil, stateErrorf("hook upsert failed")
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// SeedHooks inserts missing hook rows for hookNames; leaves existing rows intact.
func (s *Store) SeedHooks(archiveID string, hookNames []string, status string) ([]*HookRecord, error) {
	if status == "" {
		status = HookPending
	}
	for _, name := range hookNames {
		var one int
		err := s.db.QueryRow(
			`SELECT 1 FROM hooks WHERE archive_id = ? AND hook_name = ?`,
			archiveID, name,
		).Scan(&one)
		if err == sql.ErrNoRows {
			if _, err := s.UpsertHook(archiveID, name, UpsertHookParams{Status: status}); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, stateErrorf("seed hooks: %v", err)
		}
	}
	return s.ListHooks(archiveID)
}

// DeleteHooks deletes all hook rows for an archive. Returns rows deleted.
func (s *Store) DeleteHooks(archiveID string) (int, error) {
	var n int64
	err := s.Transaction(func() error {
		res, err := s.db.Exec(`DELETE FROM hooks WHERE archive_id = ?`, archiveID)
		if err != nil {
			return stateErrorf("delete hooks: %v", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
