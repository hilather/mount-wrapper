package metrics_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/metrics"

	_ "modernc.org/sqlite"
)

// makeRatarmountIndex creates a minimal ratarmount-compatible files table.
// members: path, name, size, isDir
func makeRatarmountIndex(t *testing.T, path string, members []struct {
	path  string
	name  string
	size  int64
	isDir bool
}) {
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
		mode := int64(0o100644) // S_IFREG | 0644
		if m.isDir {
			mode = int64(0o040555) // S_IFDIR | 0555
		}
		_, err = db.Exec(
			`INSERT INTO files VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.path, m.name, i, i*100, m.size, 0.0, mode, 0, "", 0, 0, 0, 0, 0, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIndexSizeAndArchiveSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := filepath.Join(dir, "a.tar")
	if err := os.WriteFile(arch, make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "a.index.sqlite")
	if err := os.WriteFile(idx, make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}

	var fs metrics.FSSizeProvider
	if got := fs.FileSize(arch); got == nil || *got != 1234 {
		t.Fatalf("archive size=%v want 1234", got)
	}
	if got := fs.IndexSize(idx); got == nil || *got != 50 {
		t.Fatalf("index size=%v want 50", got)
	}
	if got := fs.IndexSize(filepath.Join(dir, "missing")); got == nil || *got != 0 {
		t.Fatalf("missing index size=%v want 0", got)
	}
	if got := fs.IndexSize(""); got != nil {
		t.Fatalf("empty index path=%v want nil", got)
	}
	if got := fs.FileSize(""); got != nil {
		t.Fatalf("empty file path=%v want nil", got)
	}
}

func TestIndexSizeIncludesSidecars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx := filepath.Join(dir, "x.index")
	if err := os.WriteFile(idx, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idx+"-wal", make([]byte, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	var fs metrics.FSSizeProvider
	if got := fs.IndexSize(idx); got == nil || *got != 17 {
		t.Fatalf("index+wal=%v want 17", got)
	}
}

func TestExtractedSizeFromIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx := filepath.Join(dir, "x.index.sqlite")
	makeRatarmountIndex(t, idx, []struct {
		path  string
		name  string
		size  int64
		isDir bool
	}{
		{"", "a.txt", 1100, false},
		{"/subdir", "b.bin", 5000, false},
		{"", "subdir", 0, true},
	})

	total, errMsg := metrics.ExtractedSizeFromIndex(idx)
	if errMsg != "" {
		t.Fatalf("err=%q", errMsg)
	}
	if total == nil || *total != 6100 {
		t.Fatalf("total=%v want 6100", total)
	}

	total, errMsg = metrics.ExtractedSizeFromIndex(filepath.Join(dir, "nope.sqlite"))
	if total != nil || errMsg == "" {
		t.Fatalf("missing: total=%v err=%q", total, errMsg)
	}

	total, errMsg = metrics.ExtractedSizeFromIndex("")
	if total != nil || errMsg != "no index_path" {
		t.Fatalf("empty: total=%v err=%q", total, errMsg)
	}
}

func TestExtractedSizeFromMount(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "mnt")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.bin"), make([]byte, 20), 0o644); err != nil {
		t.Fatal(err)
	}

	p := metrics.DefaultExtractedProvider{}
	total, errMsg := p.FromMount(root)
	if errMsg != "" {
		t.Fatalf("err=%q", errMsg)
	}
	if total == nil || *total != 30 {
		t.Fatalf("total=%v want 30", total)
	}
}

func TestComputeArchiveMetricsWithFakes(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr

	in := metrics.ArchiveInput{
		ArchiveID:       "id-1",
		ArchivePath:     "/data/a.tar",
		ArchiveBasename: "a.tar",
		Status:          "mounted",
		IndexPath:       "/idx/a.sqlite",
		MountPath:       "/mnt/a",
	}
	sizes := metrics.MapSizeProvider{
		Files:   map[string]int64{"/data/a.tar": 500},
		Indexes: map[string]int64{"/idx/a.sqlite": 42},
	}
	extracted := metrics.MapExtractedProvider{
		Index: map[string]int64{"/idx/a.sqlite": 10_000},
	}

	m := metrics.ComputeArchiveMetrics(in, sizes, extracted, nil, metrics.ComputeOptions{})
	if m.ArchiveSizeBytes == nil || *m.ArchiveSizeBytes != 500 {
		t.Fatalf("archive=%v", m.ArchiveSizeBytes)
	}
	if m.IndexSizeBytes == nil || *m.IndexSizeBytes != 42 {
		t.Fatalf("index=%v", m.IndexSizeBytes)
	}
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 10_000 {
		t.Fatalf("extracted=%v", m.ExtractedSizeBytes)
	}
	if m.ExtractedSource != metrics.ExtractedSourceIndex {
		t.Fatalf("source=%q", m.ExtractedSource)
	}
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, i64(10_000-42))
	assertInt64Ptr(t, "space_saved_vs_archive", m.SpaceSavedVsArchiveBytes, i64(10_000-500-42))
	if !m.IndexPresent {
		t.Fatal("index should be present")
	}
}

func TestComputeArchiveMetricsPreferMount(t *testing.T) {
	t.Parallel()
	in := metrics.ArchiveInput{
		ArchiveID:   "id-2",
		ArchivePath: "/data/a.tar",
		IndexPath:   "/idx/a.sqlite",
		MountPath:   "/mnt/a",
	}
	sizes := metrics.MapSizeProvider{
		Files:   map[string]int64{"/data/a.tar": 100},
		Indexes: map[string]int64{"/idx/a.sqlite": 10},
	}
	extracted := metrics.MapExtractedProvider{
		Index: map[string]int64{"/idx/a.sqlite": 999},
		Mount: map[string]int64{"/mnt/a": 50},
	}
	m := metrics.ComputeArchiveMetrics(in, sizes, extracted, nil, metrics.ComputeOptions{
		PreferMount: true,
	})
	if m.ExtractedSource != metrics.ExtractedSourceMount {
		t.Fatalf("source=%q want mount", m.ExtractedSource)
	}
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 50 {
		t.Fatalf("extracted=%v want 50", m.ExtractedSizeBytes)
	}
}

func TestComputeArchiveMetricsPreferMountFallsBackToIndex(t *testing.T) {
	t.Parallel()
	in := metrics.ArchiveInput{
		ArchiveID:   "id-3",
		ArchivePath: "/data/a.tar",
		IndexPath:   "/idx/a.sqlite",
		MountPath:   "/mnt/missing",
	}
	sizes := metrics.MapSizeProvider{
		Files:   map[string]int64{"/data/a.tar": 100},
		Indexes: map[string]int64{"/idx/a.sqlite": 10},
	}
	extracted := metrics.MapExtractedProvider{
		Index:    map[string]int64{"/idx/a.sqlite": 777},
		MountErr: map[string]string{"/mnt/missing": "mount path not a directory"},
	}
	m := metrics.ComputeArchiveMetrics(in, sizes, extracted, nil, metrics.ComputeOptions{
		PreferMount: true,
	})
	if m.ExtractedSource != metrics.ExtractedSourceIndex {
		t.Fatalf("source=%q want index fallback", m.ExtractedSource)
	}
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 777 {
		t.Fatalf("extracted=%v", m.ExtractedSizeBytes)
	}
}

func TestComputeArchiveMetricsFullRecordFS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch := filepath.Join(dir, "src", "data.tar")
	if err := os.MkdirAll(filepath.Dir(arch), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arch, make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "indexes", "id.index.sqlite")
	makeRatarmountIndex(t, idx, []struct {
		path  string
		name  string
		size  int64
		isDir bool
	}{
		{"", "hello.txt", 10_000, false},
		{"", "dir", 0, true},
	})

	in := metrics.ArchiveInput{
		ArchiveID:       "rec-1",
		ArchivePath:     arch,
		ArchiveBasename: "data.tar",
		Status:          "discovered",
		IndexPath:       idx,
		MountPath:       filepath.Join(dir, "mounts", "data"),
	}
	m := metrics.ComputeArchiveMetrics(in, metrics.FSSizeProvider{}, metrics.DefaultExtractedProvider{}, nil, metrics.ComputeOptions{})
	if m.ArchiveSizeBytes == nil || *m.ArchiveSizeBytes != 500 {
		t.Fatalf("archive=%v", m.ArchiveSizeBytes)
	}
	st, err := os.Stat(idx)
	if err != nil {
		t.Fatal(err)
	}
	if m.IndexSizeBytes == nil || *m.IndexSizeBytes != st.Size() {
		t.Fatalf("index=%v want %d", m.IndexSizeBytes, st.Size())
	}
	if m.ExtractedSizeBytes == nil || *m.ExtractedSizeBytes != 10_000 {
		t.Fatalf("extracted=%v", m.ExtractedSizeBytes)
	}
	if m.ExtractedSource != metrics.ExtractedSourceIndex {
		t.Fatalf("source=%q", m.ExtractedSource)
	}
	idxSz := *m.IndexSizeBytes
	assertInt64Ptr(t, "space_saved", m.SpaceSavedBytes, metrics.Int64Ptr(max(0, 10_000-idxSz)))
	assertInt64Ptr(t, "space_saved_vs_archive", m.SpaceSavedVsArchiveBytes, metrics.Int64Ptr(max(0, 10_000-500-idxSz)))
}

func TestComputeArchiveMetricsConvertFromMeta(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr
	f64 := metrics.Float64Ptr
	in := metrics.ArchiveInput{
		ArchiveID:   "c1",
		ArchivePath: "/data/out.tar",
	}
	sizes := metrics.MapSizeProvider{
		Files: map[string]int64{"/data/out.tar": 8000},
	}
	meta := metrics.MapConvertMeta{
		"/data/out.tar": &metrics.ConvertMetadata{
			OriginalSizeBytes:      10000,
			SizeDeltaBytes:         -2000,
			ConvertDurationSeconds: f64(9.5),
		},
	}
	m := metrics.ComputeArchiveMetrics(in, sizes, metrics.MapExtractedProvider{}, meta, metrics.ComputeOptions{})
	assertInt64Ptr(t, "convert_source", m.ConvertSourceSizeBytes, i64(10000))
	assertInt64Ptr(t, "convert_delta", m.ConvertSizeDeltaBytes, i64(-2000))
	if m.ConvertDurationSeconds == nil || *m.ConvertDurationSeconds != 9.5 {
		t.Fatalf("duration=%v", m.ConvertDurationSeconds)
	}
}

func TestCollectorCacheAndSummary(t *testing.T) {
	t.Parallel()
	src := &metrics.MapArchiveSource{
		ByID: map[string]metrics.ArchiveInput{
			"a1": {
				ArchiveID:       "a1",
				ArchivePath:     "/a.tar",
				ArchiveBasename: "a.tar",
				Status:          "mounted",
				IndexPath:       "/a.idx",
			},
		},
		Order: []string{"a1"},
	}
	c := metrics.NewCollector(src, metrics.CollectorConfig{CacheTTLSeconds: 60})
	c.Sizes = metrics.MapSizeProvider{
		Files:   map[string]int64{"/a.tar": 100},
		Indexes: map[string]int64{"/a.idx": 10},
	}
	c.Extracted = metrics.MapExtractedProvider{
		Index: map[string]int64{"/a.idx": 1000},
	}

	m1, err := c.GetOne("a1", metrics.QueryOptions{})
	if err != nil || m1 == nil {
		t.Fatalf("get1: %v %#v", err, m1)
	}
	m2, err := c.GetOne("a1", metrics.QueryOptions{})
	if err != nil || m2 == nil {
		t.Fatalf("get2: %v", err)
	}
	// Cached value equality (value semantics; pointers equal by content).
	if m1.ExtractedSizeBytes == nil || m2.ExtractedSizeBytes == nil ||
		*m1.ExtractedSizeBytes != *m2.ExtractedSizeBytes {
		t.Fatalf("cache miss or mismatch: %v vs %v", m1.ExtractedSizeBytes, m2.ExtractedSizeBytes)
	}

	// Mutate source + sizes; cache should still serve old value.
	c.Sizes = metrics.MapSizeProvider{
		Files:   map[string]int64{"/a.tar": 999},
		Indexes: map[string]int64{"/a.idx": 10},
	}
	m3, err := c.GetOne("a1", metrics.QueryOptions{})
	if err != nil || m3.ArchiveSizeBytes == nil || *m3.ArchiveSizeBytes != 100 {
		t.Fatalf("expected cached archive size 100, got %v", m3)
	}

	// no_cache recomputes
	m4, err := c.GetOne("a1", metrics.QueryOptions{UseCache: metrics.BoolPtr(false)})
	if err != nil || m4.ArchiveSizeBytes == nil || *m4.ArchiveSizeBytes != 999 {
		t.Fatalf("no_cache should recompute: %v", m4)
	}

	all, err := c.GetAll(metrics.QueryOptions{}, nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("get_all: %v len=%d", err, len(all))
	}
	// no_cache GetOne put 999 into cache; GetAll reuses it.
	sum, err := c.Summary(all, metrics.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.ArchiveCount != 1 {
		t.Fatalf("archive_count=%d", sum.ArchiveCount)
	}
	if sum.TotalExtractedSizeBytes != 1000 {
		t.Fatalf("total_extracted=%d", sum.TotalExtractedSizeBytes)
	}
	if sum.TotalArchiveSizeBytes != 999 {
		t.Fatalf("total_archive=%d want 999 (cached after no_cache recompute)", sum.TotalArchiveSizeBytes)
	}

	// Unknown id
	u, err := c.GetOne("nope", metrics.QueryOptions{})
	if err != nil || u != nil {
		t.Fatalf("unknown: %v %#v", err, u)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	t.Parallel()
	cache := metrics.NewCache(1.0) // 1s TTL
	now := time.Unix(1000, 0)
	cache.SetClock(func() time.Time { return now })
	cache.Put(metrics.ArchiveMetrics{ArchiveID: "x", Status: "mounted"})
	if _, ok := cache.Get("x"); !ok {
		t.Fatal("expected hit")
	}
	now = now.Add(2 * time.Second)
	if _, ok := cache.Get("x"); ok {
		t.Fatal("expected miss after TTL")
	}
}

func TestCacheInvalidate(t *testing.T) {
	t.Parallel()
	cache := metrics.NewCache(60)
	cache.Put(metrics.ArchiveMetrics{ArchiveID: "a"})
	cache.Put(metrics.ArchiveMetrics{ArchiveID: "b"})
	cache.Invalidate("a")
	if _, ok := cache.Get("a"); ok {
		t.Fatal("a should be gone")
	}
	if _, ok := cache.Get("b"); !ok {
		t.Fatal("b should remain")
	}
	cache.Invalidate("")
	if _, ok := cache.Get("b"); ok {
		t.Fatal("all cleared")
	}
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
