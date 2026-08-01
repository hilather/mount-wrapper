package metrics_test

import (
	"fmt"
	"testing"

	"github.com/hilather/mount-wrapper/internal/metrics"
)

func TestComputeSpaceSaved(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr

	tests := []struct {
		name       string
		extracted  *int64
		index      *int64
		archive    *int64
		wantSaved  *int64
		wantVsArch *int64
	}{
		{
			name:       "primary and secondary positive",
			extracted:  i64(10_000),
			index:      i64(100),
			archive:    i64(2_000),
			wantSaved:  i64(9_900),
			wantVsArch: i64(7_900),
		},
		{
			name:       "clamped to zero when extract smaller than costs",
			extracted:  i64(50),
			index:      i64(100),
			archive:    i64(200),
			wantSaved:  i64(0),
			wantVsArch: i64(0),
		},
		{
			name:       "index required for both formulas",
			extracted:  i64(10_000),
			index:      nil,
			archive:    i64(2_000),
			wantSaved:  nil,
			wantVsArch: nil,
		},
		{
			name:       "archive missing only drops vs-archive",
			extracted:  i64(10_000),
			index:      i64(100),
			archive:    nil,
			wantSaved:  i64(9_900),
			wantVsArch: nil,
		},
		{
			name:       "extracted missing",
			extracted:  nil,
			index:      i64(100),
			archive:    i64(2_000),
			wantSaved:  nil,
			wantVsArch: nil,
		},
		{
			name:       "exact equality yields zero",
			extracted:  i64(1000),
			index:      i64(1000),
			archive:    i64(0),
			wantSaved:  i64(0),
			wantVsArch: i64(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSaved, gotVs := metrics.ComputeSpaceSaved(tt.extracted, tt.index, tt.archive)
			assertInt64Ptr(t, "space_saved", gotSaved, tt.wantSaved)
			assertInt64Ptr(t, "space_saved_vs_archive", gotVs, tt.wantVsArch)
		})
	}
}

func TestConvertSizeDeltaBytes(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr
	tests := []struct {
		name    string
		archive *int64
		source  *int64
		want    *int64
	}{
		{name: "shrink", archive: i64(100), source: i64(250), want: i64(-150)},
		{name: "grow", archive: i64(300), source: i64(100), want: i64(200)},
		{name: "missing archive", archive: nil, source: i64(100), want: nil},
		{name: "missing source", archive: i64(100), source: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := metrics.ConvertSizeDeltaBytes(tt.archive, tt.source)
			assertInt64Ptr(t, "delta", got, tt.want)
		})
	}
}

func TestResolveConvertFields(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr
	f64 := metrics.Float64Ptr

	meta := &metrics.ConvertMetadata{
		OriginalSizeBytes:      9000,
		SizeDeltaBytes:         -1000,
		ConvertDurationSeconds: f64(12.5),
	}

	t.Run("both set on record uses archive minus source", func(t *testing.T) {
		src, delta, dur := metrics.ResolveConvertFields(i64(8000), i64(9000), f64(3.0), meta)
		assertInt64Ptr(t, "source", src, i64(9000))
		assertInt64Ptr(t, "delta", delta, i64(-1000))
		if dur == nil || *dur != 3.0 {
			t.Fatalf("duration=%v want 3.0", dur)
		}
	})

	t.Run("missing source fills from meta including delta", func(t *testing.T) {
		src, delta, dur := metrics.ResolveConvertFields(i64(8000), nil, nil, meta)
		assertInt64Ptr(t, "source", src, i64(9000))
		assertInt64Ptr(t, "delta", delta, i64(-1000))
		if dur == nil || *dur != 12.5 {
			t.Fatalf("duration=%v want 12.5", dur)
		}
	})

	t.Run("source set duration missing keeps source delta nil from meta path", func(t *testing.T) {
		// Parity: only takes meta size delta when convert_source was None.
		src, delta, dur := metrics.ResolveConvertFields(i64(8000), i64(9000), nil, meta)
		assertInt64Ptr(t, "source", src, i64(9000))
		assertInt64Ptr(t, "delta", delta, nil)
		if dur == nil || *dur != 12.5 {
			t.Fatalf("duration=%v want 12.5", dur)
		}
	})

	t.Run("no meta no fields", func(t *testing.T) {
		src, delta, dur := metrics.ResolveConvertFields(i64(8000), nil, nil, nil)
		assertInt64Ptr(t, "source", src, nil)
		assertInt64Ptr(t, "delta", delta, nil)
		if dur != nil {
			t.Fatalf("duration=%v want nil", dur)
		}
	})
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	i64 := metrics.Int64Ptr
	f64 := metrics.Float64Ptr

	items := []metrics.ArchiveMetrics{
		{
			ArchiveID:              "a",
			ArchiveSizeBytes:       i64(100),
			IndexSizeBytes:         i64(10),
			ExtractedSizeBytes:     i64(1000),
			SpaceSavedBytes:        i64(990),
			ConvertSourceSizeBytes: i64(200),
			ConvertSizeDeltaBytes:  i64(-100),
			ConvertDurationSeconds: f64(5.0),
		},
		{
			ArchiveID:          "b",
			ArchiveSizeBytes:   i64(50),
			IndexSizeBytes:     i64(5),
			ExtractedSizeBytes: i64(500),
			SpaceSavedBytes:    i64(495),
			// no convert
		},
		{
			ArchiveID:              "c",
			ArchiveSizeBytes:       i64(1),
			ConvertSourceSizeBytes: i64(10),
			ConvertDurationSeconds: f64(20.0),
		},
	}

	s := metrics.Summarize(items)
	if s.ArchiveCount != 3 {
		t.Fatalf("archive_count=%d", s.ArchiveCount)
	}
	if s.ArchivesWithExtractedSize != 2 {
		t.Fatalf("with_extracted=%d", s.ArchivesWithExtractedSize)
	}
	if s.ArchivesWithConvertMetadata != 2 {
		t.Fatalf("with_convert=%d", s.ArchivesWithConvertMetadata)
	}
	if s.TotalArchiveSizeBytes != 151 {
		t.Fatalf("total_archive=%d", s.TotalArchiveSizeBytes)
	}
	if s.TotalIndexSizeBytes != 15 {
		t.Fatalf("total_index=%d", s.TotalIndexSizeBytes)
	}
	if s.TotalExtractedSizeBytes != 1500 {
		t.Fatalf("total_extracted=%d", s.TotalExtractedSizeBytes)
	}
	if s.TotalSpaceSavedBytes != 1485 {
		t.Fatalf("total_saved=%d", s.TotalSpaceSavedBytes)
	}
	assertInt64Ptr(t, "total_convert_source", s.TotalConvertSourceSizeBytes, i64(210))
	assertInt64Ptr(t, "total_convert_delta", s.TotalConvertSizeDeltaBytes, i64(-100))
	if s.ArchivesWithConvertDuration == nil || *s.ArchivesWithConvertDuration != 2 {
		t.Fatalf("with_convert_duration=%v", s.ArchivesWithConvertDuration)
	}
	if s.MaxConvertDurationSeconds == nil || *s.MaxConvertDurationSeconds != 20.0 {
		t.Fatalf("max_convert_duration=%v", s.MaxConvertDurationSeconds)
	}

	empty := metrics.Summarize(nil)
	if empty.ArchiveCount != 0 || empty.TotalConvertSourceSizeBytes != nil {
		t.Fatalf("empty summary: %+v", empty)
	}
}

func assertInt64Ptr(t *testing.T, label string, got, want *int64) {
	t.Helper()
	if got == nil && want == nil {
		return
	}
	if got == nil || want == nil || *got != *want {
		t.Fatalf("%s: got %v want %v", label, fmtPtr(got), fmtPtr(want))
	}
}

func fmtPtr(p *int64) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
}
