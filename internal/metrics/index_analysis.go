package metrics

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Nesting quality labels for ArchiveMetrics.ExtractedNesting.
const (
	// NestingDeep: primary extracted is deep-leaf from index (no opaque nested members).
	NestingDeep = "deep"
	// NestingShallow: primary is shallow (opaque nests incomplete or no deep known).
	NestingShallow = "shallow"
	// NestingDeepIncomplete: deep leaf partial known but opaque nests remain; primary may still be shallow.
	NestingDeepIncomplete = "deep_incomplete"
	// NestingMount: primary extracted came from a mount walk (browsable tree).
	NestingMount = "mount"
)

// nestedArchiveSuffixes mirrors convert.EmbeddedArchiveSuffixes for opaque-member
// detection without importing internal/convert (keeps metrics free of convert deps).
// Keep in sync when nested automount / zip-repack suffix sets change.
var nestedArchiveSuffixes = []string{
	".tar.gz",
	".tgz",
	".tar.bz2",
	".tbz2",
	".tbz",
	".tar.xz",
	".txz",
	".tar.zst",
	".tar.zstd",
	".tzst",
	".tar",
	".zip",
	".jar",
	".7z",
	".rar",
	".cab",
	".ar",
	".a",
	".cpio",
	".sqlar",
	".squashfs",
	".asar",
	".xar",
	".warc",
}

// IndexExtractedAnalysis is deep/shallow/opaque analysis of a ratarmount index.
//
// Semantics:
//   - Shallow: one-level extract (depth-0 non-dir rows when recursiondepth exists;
//     otherwise all non-dir sizes). Nested archives remain as packed file sizes.
//   - DeepLeaf: sum of non-dir files that are not expanded containers and not
//     opaque nested archive blobs. When the index flattened nested TAR rows
//     (recursiondepth > 0), container blobs are excluded so deep is not
//     blob+leaves double-count.
//   - DeepComplete: no opaque nested archive members remain unexpanded in the index.
//   - Primary (via Primary): DeepLeaf when DeepComplete; else Shallow (do not invent
//     deep sizes from packed nested blobs alone).
type IndexExtractedAnalysis struct {
	// NaiveSum is SUM of all non-dir sizes (legacy; may double-count flatten).
	NaiveSum *int64
	// Shallow is one-level logical extract size.
	Shallow *int64
	// DeepLeaf is known deep-leaf content (excludes expanded containers + opaque blobs).
	DeepLeaf *int64
	// DeepComplete is true when OpaqueNestedCount == 0.
	DeepComplete bool
	// OpaqueNestedCount is nested-looking members without expanded child rows.
	OpaqueNestedCount int
	// OpaqueNestedBytes is total packed size of opaque nested members.
	OpaqueNestedBytes int64
	// HasRecursionDepth is true when the files table has a recursiondepth column.
	HasRecursionDepth bool
	// ErrMsg is set on failure; size fields are nil.
	ErrMsg string
}

// Primary returns the index-only primary extracted size for space_saved:
// deep leaf when complete, otherwise shallow (honest incomplete).
func (a IndexExtractedAnalysis) Primary() *int64 {
	if a.ErrMsg != "" {
		return nil
	}
	if a.DeepComplete && a.DeepLeaf != nil {
		return a.DeepLeaf
	}
	return a.Shallow
}

// NestingLabel returns a stable nesting quality string for metrics JSON.
func (a IndexExtractedAnalysis) NestingLabel() string {
	if a.ErrMsg != "" {
		return ""
	}
	if a.DeepComplete {
		return NestingDeep
	}
	if a.DeepLeaf != nil && a.OpaqueNestedCount > 0 {
		return NestingDeepIncomplete
	}
	return NestingShallow
}

// LooksLikeNestedArchiveName reports whether basename looks like a nested archive
// (aligned with convert.MemberLooksLikeEmbeddedArchive / automount suffixes).
func LooksLikeNestedArchiveName(name string) bool {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "" || base == "." || base == ".." {
		return false
	}
	lower := strings.ToLower(base)
	for _, suffix := range nestedArchiveSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// AnalyzeIndexExtracted opens a ratarmount SQLite index and computes
// shallow / deep-leaf / opaque-nested analysis.
func AnalyzeIndexExtracted(indexPath string) IndexExtractedAnalysis {
	if indexPath == "" {
		return IndexExtractedAnalysis{ErrMsg: "no index_path"}
	}
	st, err := os.Stat(indexPath)
	if err != nil || !st.Mode().IsRegular() {
		return IndexExtractedAnalysis{ErrMsg: "index file missing"}
	}

	db, err := openIndexDB(indexPath)
	if err != nil {
		return IndexExtractedAnalysis{ErrMsg: fmt.Sprintf("cannot open index: %v", err)}
	}
	defer db.Close()

	var n int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='files'`,
	).Scan(&n)
	if err != nil {
		return IndexExtractedAnalysis{ErrMsg: fmt.Sprintf("index query failed: %v", err)}
	}
	if n == 0 {
		return IndexExtractedAnalysis{ErrMsg: "index has no files table"}
	}

	cols, err := filesTableColumns(db)
	if err != nil {
		return IndexExtractedAnalysis{ErrMsg: err.Error()}
	}
	if !cols["size"] {
		return IndexExtractedAnalysis{ErrMsg: "files table has no size column"}
	}

	rows, err := loadIndexFileRows(db, cols)
	if err != nil {
		return IndexExtractedAnalysis{ErrMsg: err.Error()}
	}
	return analyzeIndexRows(rows, cols["recursiondepth"])
}

// ExtractedSizeFromIndex returns the primary extracted size from the index
// (deep leaf when complete; shallow when opaque nests remain).
// Directories (S_IFDIR) are excluded when a mode column is present.
func ExtractedSizeFromIndex(indexPath string) (*int64, string) {
	a := AnalyzeIndexExtracted(indexPath)
	if a.ErrMsg != "" {
		return nil, a.ErrMsg
	}
	return a.Primary(), ""
}

type indexFileRow struct {
	path  string
	name  string
	size  int64
	depth int
	isDir bool
}

func filesTableColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(files)`)
	if err != nil {
		return nil, fmt.Errorf("index query failed: %v", err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("index query failed: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index query failed: %v", err)
	}
	return cols, nil
}

func loadIndexFileRows(db *sql.DB, cols map[string]bool) ([]indexFileRow, error) {
	hasMode := cols["mode"]
	hasDepth := cols["recursiondepth"]
	hasPath := cols["path"]
	hasName := cols["name"]

	// Build SELECT list dynamically.
	sel := []string{"size"}
	if hasPath {
		sel = append(sel, "path")
	}
	if hasName {
		sel = append(sel, "name")
	}
	if hasMode {
		sel = append(sel, "mode")
	}
	if hasDepth {
		sel = append(sel, "recursiondepth")
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM files"
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("index query failed: %v", err)
	}
	defer rows.Close()

	var out []indexFileRow
	for rows.Next() {
		var size int64
		var path, name string
		var mode sql.NullInt64
		var depth sql.NullInt64

		// Scan in sel order.
		dest := []any{&size}
		if hasPath {
			dest = append(dest, &path)
		}
		if hasName {
			dest = append(dest, &name)
		}
		if hasMode {
			dest = append(dest, &mode)
		}
		if hasDepth {
			dest = append(dest, &depth)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("index query failed: %v", err)
		}
		isDir := false
		if hasMode && mode.Valid {
			isDir = (mode.Int64 & sIFDIR) != 0
		}
		d := 0
		if hasDepth && depth.Valid {
			d = int(depth.Int64)
		}
		out = append(out, indexFileRow{
			path:  path,
			name:  name,
			size:  size,
			depth: d,
			isDir: isDir,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index query failed: %v", err)
	}
	return out, nil
}

// analyzeIndexRows is the pure core of index deep/shallow analysis (unit-testable).
func analyzeIndexRows(rows []indexFileRow, hasRecursionDepth bool) IndexExtractedAnalysis {
	// Collect non-dir files for size math; keep all rows for container detection
	// (dir stubs under nested paths also mark expansion).
	type fileInfo struct {
		row      indexFileRow
		fullPath string
	}
	var files []fileInfo
	var allFull []string // all entries (dir + file) full paths for prefix checks
	for _, r := range rows {
		fp := joinIndexPath(r.path, r.name)
		allFull = append(allFull, fp)
		if r.isDir {
			continue
		}
		files = append(files, fileInfo{row: r, fullPath: fp})
	}

	// expandedContainers: non-dir file full paths that have any other index entry under them.
	expanded := map[string]struct{}{}
	for _, f := range files {
		prefix := f.fullPath + "/"
		for _, other := range allFull {
			if other == f.fullPath {
				continue
			}
			if strings.HasPrefix(other, prefix) || other == f.fullPath {
				// other under this file path ⇒ this file was expanded as a container
				if strings.HasPrefix(other, prefix) {
					expanded[f.fullPath] = struct{}{}
					break
				}
			}
		}
		// Also: any row whose path equals this fullPath or starts with fullPath+"/"
		// (ratarmount stores children with path=/outer/inner.tar/...)
		if _, ok := expanded[f.fullPath]; !ok {
			for _, r := range rows {
				p := r.path
				if p == f.fullPath || strings.HasPrefix(p, f.fullPath+"/") {
					// Child content under this nested archive path.
					// Skip self-row (path+name == this file).
					if joinIndexPath(r.path, r.name) == f.fullPath {
						continue
					}
					expanded[f.fullPath] = struct{}{}
					break
				}
			}
		}
	}

	var naive, shallow, deepLeaf int64
	var opaqueCount int
	var opaqueBytes int64

	for _, f := range files {
		naive += f.row.size

		// Shallow: depth 0 only when recursiondepth present; else all non-dirs.
		if !hasRecursionDepth || f.row.depth == 0 {
			shallow += f.row.size
		}

		_, isContainer := expanded[f.fullPath]
		looksNested := LooksLikeNestedArchiveName(f.row.name)
		if isContainer {
			// Expanded nested archive blob — exclude from deep leaf.
			continue
		}
		if looksNested {
			// Opaque nested member: do not invent deep size from packed blob.
			opaqueCount++
			opaqueBytes += f.row.size
			continue
		}
		deepLeaf += f.row.size
	}

	complete := opaqueCount == 0
	return IndexExtractedAnalysis{
		NaiveSum:          Int64Ptr(naive),
		Shallow:           Int64Ptr(shallow),
		DeepLeaf:          Int64Ptr(deepLeaf),
		DeepComplete:      complete,
		OpaqueNestedCount: opaqueCount,
		OpaqueNestedBytes: opaqueBytes,
		HasRecursionDepth: hasRecursionDepth,
	}
}

// joinIndexPath builds the ratarmount-style full path for a files row.
func joinIndexPath(path, name string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	name = strings.ReplaceAll(name, "\\", "/")
	if path == "" || path == "/" {
		if name == "" {
			return ""
		}
		// Root member: use leading slash for stable prefix checks when children
		// use absolute-style paths; also accept bare name.
		if strings.HasPrefix(name, "/") {
			return name
		}
		return "/" + name
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")
	if name == "" {
		return path
	}
	return path + "/" + name
}

// IndexAnalyzer is optionally implemented by ExtractedSizeProvider to return
// full deep/shallow/opaque analysis (DefaultExtractedProvider, MapExtractedProvider).
type IndexAnalyzer interface {
	AnalyzeIndex(indexPath string) IndexExtractedAnalysis
}

// AnalyzeIndex implements IndexAnalyzer for DefaultExtractedProvider.
func (p DefaultExtractedProvider) AnalyzeIndex(indexPath string) IndexExtractedAnalysis {
	return AnalyzeIndexExtracted(indexPath)
}
