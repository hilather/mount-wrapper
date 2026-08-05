package metrics_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/metrics"

	_ "modernc.org/sqlite"
)

// indexMember is a ratarmount-like files row for synthetic indexes.
type indexMember struct {
	path  string
	name  string
	size  int64
	isDir bool
	depth int
}

// makeRatarmountIndexDepth creates a files table with optional recursiondepth.
func makeRatarmountIndexDepth(t *testing.T, path string, members []indexMember) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE files (
			path VARCHAR(65535) NOT NULL,
			name VARCHAR(65535) NOT NULL,
			offsetheader INTEGER,
			offset INTEGER,
			size INTEGER,
			mtime REAL,
			mode INTEGER,
			type INTEGER,
			linkname VARCHAR(65535),
			uid INTEGER,
			gid INTEGER,
			istar BOOL,
			issparse BOOL,
			isgenerated BOOL,
			recursiondepth INTEGER,
			PRIMARY KEY (path, name, offsetheader)
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range members {
		mode := int64(0o100644)
		if m.isDir {
			mode = int64(0o040555)
		}
		_, err = db.Exec(
			`INSERT INTO files VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.path, m.name, i, i*100, m.size, 0.0, mode, 0, "", 0, 0, 0, 0, 0, m.depth,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestAnalyzeIndexExtracted_FlattenedNestedTar mirrors nested-tar double-count:
// depth-0 nested blob + depth>0 leaves. Deep leaf must exclude the blob.
func TestAnalyzeIndexExtracted_FlattenedNestedTar(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx := filepath.Join(dir, "nested-tar.index.sqlite")
	// Fixture shaped like ratarmount nested-tar.tar.index.sqlite:
	//   /foo/ufo (6) depth0, /foo/lighter.tar (2560) depth0 expanded,
	//   /foo/lighter.tar/fighter/bar (10) depth1
	makeRatarmountIndexDepth(t, idx, []indexMember{
		{path: "", name: "foo", size: 0, isDir: true, depth: 0},
		{path: "/foo", name: "ufo", size: 6, isDir: false, depth: 0},
		{path: "/foo", name: "lighter.tar", size: 2560, isDir: false, depth: 0},
		{path: "/foo/lighter.tar", name: "fighter", size: 0, isDir: true, depth: 1},
		{path: "/foo/lighter.tar/fighter", name: "bar", size: 10, isDir: false, depth: 1},
	})

	a := metrics.AnalyzeIndexExtracted(idx)
	if a.ErrMsg != "" {
		t.Fatalf("err=%q", a.ErrMsg)
	}
	// Naive would be 6+2560+10 = 2576 (blob + leaves).
	if a.NaiveSum == nil || *a.NaiveSum != 2576 {
		t.Fatalf("naive=%v want 2576", a.NaiveSum)
	}
	// Shallow depth0: ufo + lighter.tar blob = 2566.
	if a.Shallow == nil || *a.Shallow != 2566 {
		t.Fatalf("shallow=%v want 2566", a.Shallow)
	}
	// Deep leaf: ufo + bar only (exclude expanded container blob).
	if a.DeepLeaf == nil || *a.DeepLeaf != 16 {
		t.Fatalf("deep=%v want 16", a.DeepLeaf)
	}
	if !a.DeepComplete {
		t.Fatal("expected deep complete (no opaque nests)")
	}
	if a.OpaqueNestedCount != 0 {
		t.Fatalf("opaque count=%d", a.OpaqueNestedCount)
	}
	primary := a.Primary()
	if primary == nil || *primary != 16 {
		t.Fatalf("primary=%v want deep leaf 16", primary)
	}
	if a.NestingLabel() != metrics.NestingDeep {
		t.Fatalf("nesting=%q want deep", a.NestingLabel())
	}

	// ExtractedSizeFromIndex uses primary (deep).
	got, errMsg := metrics.ExtractedSizeFromIndex(idx)
	if errMsg != "" || got == nil || *got != 16 {
		t.Fatalf("ExtractedSizeFromIndex=%v err=%q want 16", got, errMsg)
	}
}

// TestAnalyzeIndexExtracted_OpaqueNestedOnly: packed nested members only —
// deep must not invent expanded size from the packed blob; incomplete signal.
func TestAnalyzeIndexExtracted_OpaqueNestedOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx := filepath.Join(dir, "opaque.index.sqlite")
	makeRatarmountIndexDepth(t, idx, []indexMember{
		{path: "", name: "readme.txt", size: 100, isDir: false, depth: 0},
		{path: "/payloads", name: "inner.7z", size: 50_000, isDir: false, depth: 0},
		{path: "/payloads", name: "bundle.tar.gz", size: 80_000, isDir: false, depth: 0},
		{path: "", name: "payloads", size: 0, isDir: true, depth: 0},
	})

	a := metrics.AnalyzeIndexExtracted(idx)
	if a.ErrMsg != "" {
		t.Fatalf("err=%q", a.ErrMsg)
	}
	if a.DeepComplete {
		t.Fatal("expected incomplete (opaque nests)")
	}
	if a.OpaqueNestedCount != 2 {
		t.Fatalf("opaque count=%d want 2", a.OpaqueNestedCount)
	}
	if a.OpaqueNestedBytes != 130_000 {
		t.Fatalf("opaque bytes=%d want 130000", a.OpaqueNestedBytes)
	}
	// Deep leaf = plain files only (readme), not packed nest sizes.
	if a.DeepLeaf == nil || *a.DeepLeaf != 100 {
		t.Fatalf("deep=%v want 100 (readme only)", a.DeepLeaf)
	}
	// Shallow includes packed nests.
	if a.Shallow == nil || *a.Shallow != 130_100 {
		t.Fatalf("shallow=%v want 130100", a.Shallow)
	}
	// Primary must be shallow — do not claim deep from packed blobs.
	primary := a.Primary()
	if primary == nil || *primary != 130_100 {
		t.Fatalf("primary=%v want shallow 130100", primary)
	}
	if a.NestingLabel() != metrics.NestingDeepIncomplete {
		t.Fatalf("nesting=%q want deep_incomplete", a.NestingLabel())
	}
}

// TestComputeArchiveMetrics_DeepLeafSpaceSaved drives shipped ComputeArchiveMetrics
// on a flattened nested index: space_saved uses deep leaf, not naive SUM.
func TestComputeArchiveMetrics_DeepLeafSpaceSaved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := filepath.Join(dir, "outer.tar")
	if err := os.WriteFile(arch, make([]byte, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "outer.index.sqlite")
	makeRatarmountIndexDepth(t, idx, []indexMember{
		{path: "/foo", name: "ufo", size: 6, isDir: false, depth: 0},
		{path: "/foo", name: "lighter.tar", size: 2560, isDir: false, depth: 0},
		{path: "/foo/lighter.tar/fighter", name: "bar", size: 10, isDir: false, depth: 1},
	})

	in := metrics.ArchiveInput{
		ArchiveID:       "deep-1",
		ArchivePath:     arch,
		ArchiveBasename: "outer.tar",
		Status:          "discovered",
		IndexPath:       idx,
	}
	m := metrics.ComputeArchiveMetrics(in, metrics.FSSizeProvider{}, metrics.DefaultExtractedProvider{}, nil, metrics.ComputeOptions{})
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 16 {
		t.Fatalf("primary extracted=%v want 16 (deep leaf)", m.ExtractedSizeBytes)
	}
	if m.ExtractedSizeShallowBytes == nil || *m.ExtractedSizeShallowBytes != 2566 {
		t.Fatalf("shallow=%v want 2566", m.ExtractedSizeShallowBytes)
	}
	if m.ExtractedSizeDeepBytes == nil || *m.ExtractedSizeDeepBytes != 16 {
		t.Fatalf("deep field=%v want 16", m.ExtractedSizeDeepBytes)
	}
	if m.ExtractedNesting != metrics.NestingDeep {
		t.Fatalf("nesting=%q", m.ExtractedNesting)
	}
	if m.ExtractedDeepComplete == nil || !*m.ExtractedDeepComplete {
		t.Fatal("expected deep complete")
	}
	if m.ExtractedSource != metrics.ExtractedSourceIndex {
		t.Fatalf("source=%q", m.ExtractedSource)
	}
	idxSz := int64(0)
	if m.IndexSizeBytes != nil {
		idxSz = *m.IndexSizeBytes
	}
	// space_saved from deep 16, not naive 2576.
	wantSaved := max64(0, 16-idxSz)
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, metrics.Int64Ptr(wantSaved))
	wantVs := max64(0, 16-3000-idxSz)
	assertInt64Ptr(t, "space_saved_vs_archive", m.SpaceSavedVsArchiveBytes, metrics.Int64Ptr(wantVs))
}

// TestComputeArchiveMetrics_OpaqueNestedIncomplete: unmounted opaque nests →
// primary shallow + quality flags; space_saved from shallow.
func TestComputeArchiveMetrics_OpaqueNestedIncomplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := filepath.Join(dir, "bag.zip")
	if err := os.WriteFile(arch, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "bag.index.sqlite")
	makeRatarmountIndexDepth(t, idx, []indexMember{
		{path: "", name: "readme.txt", size: 100, isDir: false, depth: 0},
		{path: "/payloads", name: "inner.7z", size: 50_000, isDir: false, depth: 0},
	})

	in := metrics.ArchiveInput{
		ArchiveID:   "opaque-1",
		ArchivePath: arch,
		Status:      "discovered", // not mounted → no mount promotion
		IndexPath:   idx,
	}
	m := metrics.ComputeArchiveMetrics(in, metrics.FSSizeProvider{}, metrics.DefaultExtractedProvider{}, nil, metrics.ComputeOptions{})
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 50_100 {
		t.Fatalf("primary=%v want shallow 50100", m.ExtractedSizeBytes)
	}
	if m.ExtractedDeepComplete == nil || *m.ExtractedDeepComplete {
		t.Fatal("expected deep incomplete")
	}
	if m.OpaqueNestedCount != 1 {
		t.Fatalf("opaque count=%d", m.OpaqueNestedCount)
	}
	if m.OpaqueNestedBytes != 50_000 {
		t.Fatalf("opaque bytes=%d", m.OpaqueNestedBytes)
	}
	if m.ExtractedNesting != metrics.NestingDeepIncomplete {
		t.Fatalf("nesting=%q", m.ExtractedNesting)
	}
	// Deep field is known leaves only (readme), not invented from inner.7z.
	if m.ExtractedSizeDeepBytes == nil || *m.ExtractedSizeDeepBytes != 100 {
		t.Fatalf("deep field=%v want 100", m.ExtractedSizeDeepBytes)
	}
	idxSz := int64(0)
	if m.IndexSizeBytes != nil {
		idxSz = *m.IndexSizeBytes
	}
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, metrics.Int64Ptr(max64(0, 50_100-idxSz)))
}

// TestComputeArchiveMetrics_MountPromoteWhenOpaqueIncomplete: mounted + opaque
// index → mount walk supplies deep primary.
func TestComputeArchiveMetrics_MountPromoteWhenOpaqueIncomplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := filepath.Join(dir, "bag.zip")
	if err := os.WriteFile(arch, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "bag.index.sqlite")
	makeRatarmountIndexDepth(t, idx, []indexMember{
		{path: "", name: "readme.txt", size: 100, isDir: false, depth: 0},
		{path: "/payloads", name: "inner.7z", size: 50_000, isDir: false, depth: 0},
	})
	// Simulate recursive automount: nested content browsable under mount path.
	mnt := filepath.Join(dir, "mnt")
	if err := os.MkdirAll(filepath.Join(mnt, "payloads", "inner.7z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mnt, "readme.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nested expanded content (not the packed 50k blob).
	if err := os.WriteFile(filepath.Join(mnt, "payloads", "inner.7z", "secret.bin"), make([]byte, 900), 0o644); err != nil {
		t.Fatal(err)
	}

	in := metrics.ArchiveInput{
		ArchiveID:   "opaque-mnt",
		ArchivePath: arch,
		Status:      "mounted",
		IndexPath:   idx,
		MountPath:   mnt,
	}
	m := metrics.ComputeArchiveMetrics(in, metrics.FSSizeProvider{}, metrics.DefaultExtractedProvider{}, nil, metrics.ComputeOptions{})
	// Mount walk: readme 100 + secret 900 = 1000 (deep browsable).
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 1000 {
		t.Fatalf("primary extracted=%v want 1000 (mount deep)", m.ExtractedSizeBytes)
	}
	if m.ExtractedSource != metrics.ExtractedSourceMount {
		t.Fatalf("source=%q want mount", m.ExtractedSource)
	}
	if m.ExtractedNesting != metrics.NestingMount {
		t.Fatalf("nesting=%q", m.ExtractedNesting)
	}
	if m.ExtractedDeepComplete == nil || !*m.ExtractedDeepComplete {
		t.Fatal("mount path should mark deep complete for browsable tree")
	}
	// Index-derived quality still reports opaque for diagnostics.
	if m.OpaqueNestedCount != 1 {
		t.Fatalf("opaque count=%d (index diagnostic)", m.OpaqueNestedCount)
	}
	idxSz := int64(0)
	if m.IndexSizeBytes != nil {
		idxSz = *m.IndexSizeBytes
	}
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, metrics.Int64Ptr(max64(0, 1000-idxSz)))
}

// TestComputeArchiveMetrics_MountPromoteViaMapProvider: incomplete analysis +
// fake mount path (no real dir) still promotes when status mounted.
func TestComputeArchiveMetrics_MountPromoteViaMapProvider(t *testing.T) {
	t.Parallel()
	idxPath := "/idx/opaque.sqlite"
	in := metrics.ArchiveInput{
		ArchiveID:   "map-promote",
		ArchivePath: "/data/a.zip",
		Status:      "mounted",
		IndexPath:   idxPath,
		MountPath:   "/mnt/a",
	}
	sizes := metrics.MapSizeProvider{
		Files:   map[string]int64{"/data/a.zip": 500},
		Indexes: map[string]int64{idxPath: 40},
	}
	extracted := metrics.MapExtractedProvider{
		Analysis: map[string]metrics.IndexExtractedAnalysis{
			idxPath: {
				NaiveSum:          metrics.Int64Ptr(50_100),
				Shallow:           metrics.Int64Ptr(50_100),
				DeepLeaf:          metrics.Int64Ptr(100),
				DeepComplete:      false,
				OpaqueNestedCount: 1,
				OpaqueNestedBytes: 50_000,
			},
		},
		Mount: map[string]int64{"/mnt/a": 12_345},
	}
	m := metrics.ComputeArchiveMetrics(in, sizes, extracted, nil, metrics.ComputeOptions{})
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 12_345 {
		t.Fatalf("primary=%v want mount 12345", m.ExtractedSizeBytes)
	}
	if m.ExtractedSource != metrics.ExtractedSourceMount {
		t.Fatalf("source=%q", m.ExtractedSource)
	}
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, metrics.Int64Ptr(12_345-40))
}

func TestLooksLikeNestedArchiveName(t *testing.T) {
	t.Parallel()
	if !metrics.LooksLikeNestedArchiveName("payloads/inner.7z") {
		t.Fatal("inner.7z")
	}
	if !metrics.LooksLikeNestedArchiveName("bundle.tar.gz") {
		t.Fatal("tar.gz")
	}
	if metrics.LooksLikeNestedArchiveName("readme.txt") {
		t.Fatal("readme should not match")
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
