package metrics

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// S_IFDIR is the Unix directory mode bit used in ratarmount index "mode" columns.
const sIFDIR = 0o040000 // 16384

// ExtractedSizeProvider resolves logical extracted size from an index or mount.
// Full SQLite index walks and FUSE walks are injected so tests can fake them.
type ExtractedSizeProvider interface {
	// FromIndex sums member sizes from a ratarmount SQLite index.
	// Returns (size, errorMessage). size is nil on failure/missing index.
	FromIndex(indexPath string) (size *int64, errMsg string)
	// FromMount sums regular file sizes under a mount point (slow fallback).
	FromMount(mountPath string) (size *int64, errMsg string)
}

// DefaultExtractedProvider implements ExtractedSizeProvider with real FS/SQLite.
type DefaultExtractedProvider struct {
	// MaxFiles caps mount walks (default 500_000).
	MaxFiles int
	// Timeout caps mount walks (default 30s).
	Timeout time.Duration
	// Now is an optional clock for tests; defaults to time.Now.
	Now func() time.Time
	// IsMount reports whether path is a mount point. When nil, uses a best-effort
	// check: always allow walk if PreferMount-style plain dirs are requested by
	// the caller (FromMount itself only checks directory).
	// Callers decide whether to invoke FromMount for non-mount dirs.
}

func (p DefaultExtractedProvider) maxFiles() int {
	if p.MaxFiles <= 0 {
		return 500_000
	}
	return p.MaxFiles
}

func (p DefaultExtractedProvider) timeout() time.Duration {
	if p.Timeout <= 0 {
		return 30 * time.Second
	}
	return p.Timeout
}

func (p DefaultExtractedProvider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// FromIndex implements ExtractedSizeProvider (parity extracted_size_from_index).
func (p DefaultExtractedProvider) FromIndex(indexPath string) (*int64, string) {
	return ExtractedSizeFromIndex(indexPath)
}

// FromMount implements ExtractedSizeProvider (parity extracted_size_from_mount).
func (p DefaultExtractedProvider) FromMount(mountPath string) (*int64, string) {
	return extractedSizeFromMount(mountPath, p.maxFiles(), p.timeout(), p.now)
}

func openIndexDB(indexPath string) (*sql.DB, error) {
	tryOpen := func(dsn string) (*sql.DB, error) {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		// Force a real open; sql.Open is lazy.
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, err
		}
		_, _ = db.Exec(`PRAGMA query_only = ON`)
		return db, nil
	}
	if db, err := tryOpen(indexPath); err == nil {
		return db, nil
	}
	// URI form for absolute paths (and RO when supported).
	dsn := "file:" + filepath.ToSlash(indexPath) + "?mode=ro"
	return tryOpen(dsn)
}

// ExtractedSizeFromIndex sums member sizes from a ratarmount SQLite index.
// Directories (S_IFDIR) are excluded when a mode column is present.
func ExtractedSizeFromIndex(indexPath string) (*int64, string) {
	if indexPath == "" {
		return nil, "no index_path"
	}
	st, err := os.Stat(indexPath)
	if err != nil || !st.Mode().IsRegular() {
		return nil, "index file missing"
	}

	// Prefer plain path open (parity with Python); fall back to URI mode=ro.
	db, err := openIndexDB(indexPath)
	if err != nil {
		return nil, fmt.Sprintf("cannot open index: %v", err)
	}
	defer db.Close()

	var n int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='files'`,
	).Scan(&n)
	if err != nil {
		return nil, fmt.Sprintf("index query failed: %v", err)
	}
	if n == 0 {
		return nil, "index has no files table"
	}

	// Column discovery.
	rows, err := db.Query(`PRAGMA table_info(files)`)
	if err != nil {
		return nil, fmt.Sprintf("index query failed: %v", err)
	}
	hasSize, hasMode := false, false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return nil, fmt.Sprintf("index query failed: %v", err)
		}
		switch name {
		case "size":
			hasSize = true
		case "mode":
			hasMode = true
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Sprintf("index query failed: %v", err)
	}
	if !hasSize {
		return nil, "files table has no size column"
	}

	var total int64
	if hasMode {
		// S_IFDIR = 16384; exclude directories; include NULL mode.
		err = db.QueryRow(
			`SELECT COALESCE(SUM(size), 0) FROM files WHERE (mode IS NULL OR (mode & ?) = 0)`,
			sIFDIR,
		).Scan(&total)
	} else {
		err = db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&total)
	}
	if err != nil {
		return nil, fmt.Sprintf("index query failed: %v", err)
	}
	return &total, ""
}

func extractedSizeFromMount(
	mountPath string,
	maxFiles int,
	timeout time.Duration,
	now func() time.Time,
) (*int64, string) {
	if mountPath == "" {
		return nil, "no mount_path"
	}
	st, err := os.Stat(mountPath)
	if err != nil || !st.IsDir() {
		return nil, "mount path not a directory"
	}

	t0 := now()
	var total int64
	n := 0
	walkErr := filepath.WalkDir(mountPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries; continue walk.
			return nil
		}
		if now().Sub(t0) > timeout {
			return errMountTimeout
		}
		if d.IsDir() {
			return nil
		}
		// Skip symlinks: only regular files (parity lstat + S_ISREG).
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		n++
		if n > maxFiles {
			return errMountMaxFiles
		}
		total += info.Size()
		return nil
	})
	if walkErr == errMountTimeout {
		return nil, fmt.Sprintf("mount walk timed out after %s", timeout)
	}
	if walkErr == errMountMaxFiles {
		return nil, fmt.Sprintf("mount walk exceeded max_files=%d", maxFiles)
	}
	if walkErr != nil {
		return nil, fmt.Sprintf("mount walk failed: %v", walkErr)
	}
	return &total, ""
}

type mountWalkSentinel string

func (e mountWalkSentinel) Error() string { return string(e) }

const (
	errMountTimeout  mountWalkSentinel = "mount walk timeout"
	errMountMaxFiles mountWalkSentinel = "mount walk max files"
)

// MapExtractedProvider is a test ExtractedSizeProvider.
type MapExtractedProvider struct {
	// Index maps index path → size. Missing key uses IndexErr (default "index file missing").
	Index    map[string]int64
	IndexErr map[string]string
	// Mount maps mount path → size.
	Mount    map[string]int64
	MountErr map[string]string
}

// FromIndex implements ExtractedSizeProvider.
func (m MapExtractedProvider) FromIndex(indexPath string) (*int64, string) {
	if indexPath == "" {
		return nil, "no index_path"
	}
	if m.IndexErr != nil {
		if msg, ok := m.IndexErr[indexPath]; ok {
			return nil, msg
		}
	}
	if m.Index != nil {
		if v, ok := m.Index[indexPath]; ok {
			return &v, ""
		}
	}
	return nil, "index file missing"
}

// FromMount implements ExtractedSizeProvider.
func (m MapExtractedProvider) FromMount(mountPath string) (*int64, string) {
	if mountPath == "" {
		return nil, "no mount_path"
	}
	if m.MountErr != nil {
		if msg, ok := m.MountErr[mountPath]; ok {
			return nil, msg
		}
	}
	if m.Mount != nil {
		if v, ok := m.Mount[mountPath]; ok {
			return &v, ""
		}
	}
	return nil, "mount path not a directory"
}

// ConvertMetaProvider optionally supplies convert sidecar metadata.
type ConvertMetaProvider interface {
	// ReadConvertMetadata returns metadata for archivePath, or nil if absent.
	ReadConvertMetadata(archivePath string) *ConvertMetadata
}

// NoConvertMeta never returns convert metadata.
type NoConvertMeta struct{}

// ReadConvertMetadata implements ConvertMetaProvider.
func (NoConvertMeta) ReadConvertMetadata(string) *ConvertMetadata { return nil }

// MapConvertMeta is a test ConvertMetaProvider.
type MapConvertMeta map[string]*ConvertMetadata

// ReadConvertMetadata implements ConvertMetaProvider.
func (m MapConvertMeta) ReadConvertMetadata(archivePath string) *ConvertMetadata {
	if m == nil {
		return nil
	}
	return m[archivePath]
}
