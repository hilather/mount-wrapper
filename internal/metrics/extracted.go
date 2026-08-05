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
//
// Providers that also implement IndexAnalyzer supply deep/shallow/opaque analysis;
// ComputeArchiveMetrics uses that when available so space_saved uses deep leaf
// when complete and stays honest when nested members are opaque.
type ExtractedSizeProvider interface {
	// FromIndex returns the primary extracted size from a ratarmount SQLite index
	// (deep leaf when complete; shallow when opaque nests remain).
	// Returns (size, errorMessage). size is nil on failure/missing index.
	FromIndex(indexPath string) (size *int64, errMsg string)
	// FromMount sums regular file sizes under a mount point (deep browsable tree;
	// slow fallback / promotion when index deep is incomplete).
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
	// Index maps index path → primary size. Missing key uses IndexErr (default "index file missing").
	Index    map[string]int64
	IndexErr map[string]string
	// Analysis optional full deep/shallow analysis per index path. When set,
	// AnalyzeIndex returns it; FromIndex uses Analysis.Primary().
	Analysis map[string]IndexExtractedAnalysis
	// Mount maps mount path → size.
	Mount    map[string]int64
	MountErr map[string]string
}

// AnalyzeIndex implements IndexAnalyzer.
func (m MapExtractedProvider) AnalyzeIndex(indexPath string) IndexExtractedAnalysis {
	if indexPath == "" {
		return IndexExtractedAnalysis{ErrMsg: "no index_path"}
	}
	if m.IndexErr != nil {
		if msg, ok := m.IndexErr[indexPath]; ok {
			return IndexExtractedAnalysis{ErrMsg: msg}
		}
	}
	if m.Analysis != nil {
		if a, ok := m.Analysis[indexPath]; ok {
			return a
		}
	}
	if m.Index != nil {
		if v, ok := m.Index[indexPath]; ok {
			// Synthesize complete deep == shallow when only a scalar is provided.
			return IndexExtractedAnalysis{
				NaiveSum:     Int64Ptr(v),
				Shallow:      Int64Ptr(v),
				DeepLeaf:     Int64Ptr(v),
				DeepComplete: true,
			}
		}
	}
	return IndexExtractedAnalysis{ErrMsg: "index file missing"}
}

// FromIndex implements ExtractedSizeProvider.
func (m MapExtractedProvider) FromIndex(indexPath string) (*int64, string) {
	a := m.AnalyzeIndex(indexPath)
	if a.ErrMsg != "" {
		return nil, a.ErrMsg
	}
	return a.Primary(), ""
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
