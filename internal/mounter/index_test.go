package mounter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

func TestPartialIndexCleanup_matrix(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	idx := filepath.Join(tmp, "partial.index.sqlite")
	if err := os.WriteFile(idx, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := "2020-01-01T00:00:00Z"

	cases := []struct {
		name           string
		status         string
		firstMountedAt *string
		indexPath      string
		indexIsFile    bool
		wantShould     bool
		wantDeleteFile bool
	}{
		{
			name: "indexing never mounted", status: state.StatusIndexing,
			indexPath: idx, indexIsFile: true, wantShould: true, wantDeleteFile: true,
		},
		{
			name: "index_failed", status: state.StatusIndexFailed,
			indexPath: idx, indexIsFile: true, wantShould: true, wantDeleteFile: true,
		},
		{
			name: "discovered", status: state.StatusDiscovered,
			indexPath: idx, indexIsFile: true, wantShould: true, wantDeleteFile: true,
		},
		{
			name: "mount_failed", status: state.StatusMountFailed,
			indexPath: idx, indexIsFile: true, wantShould: true, wantDeleteFile: true,
		},
		{
			name: "keep after successful mount", status: state.StatusIndexing,
			firstMountedAt: &first, indexPath: idx, indexIsFile: true,
			wantShould: false, wantDeleteFile: false,
		},
		{
			name: "mounted status not partial", status: state.StatusMounted,
			indexPath: idx, indexIsFile: true, wantShould: false, wantDeleteFile: false,
		},
		{
			name: "no index path", status: state.StatusIndexing,
			indexPath: "", indexIsFile: false, wantShould: false, wantDeleteFile: false,
		},
		{
			name: "index missing on disk", status: state.StatusIndexing,
			indexPath: filepath.Join(tmp, "missing.sqlite"), indexIsFile: false,
			wantShould: true, wantDeleteFile: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Restore file for delete cases that consume it.
			if tc.indexIsFile && tc.indexPath == idx {
				_ = os.WriteFile(idx, []byte("partial"), 0o644)
			}
			got := mounter.ShouldDeletePartialIndex(tc.status, tc.firstMountedAt, tc.indexPath)
			if got != tc.wantShould {
				t.Fatalf("ShouldDeletePartialIndex=%v want %v", got, tc.wantShould)
			}
			gotFile := mounter.ShouldDeletePartialIndexFile(tc.status, tc.firstMountedAt, tc.indexPath, tc.indexIsFile)
			wantFileRule := tc.wantShould && tc.indexIsFile
			if gotFile != wantFileRule {
				t.Fatalf("ShouldDeletePartialIndexFile=%v want %v", gotFile, wantFileRule)
			}
			// Use a per-case copy for Apply so parallel subtests don't race.
			path := tc.indexPath
			if tc.indexIsFile && tc.indexPath == idx {
				path = filepath.Join(tmp, tc.name+".index.sqlite")
				if err := os.WriteFile(path, []byte("partial"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			deleted := mounter.ApplyPartialIndexCleanup(tc.status, tc.firstMountedAt, path)
			if deleted != tc.wantDeleteFile {
				t.Fatalf("ApplyPartialIndexCleanup=%v want %v", deleted, tc.wantDeleteFile)
			}
			if tc.wantDeleteFile {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("file should be gone: %v", err)
				}
			}
		})
	}
}

func TestIndexFileReady(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	empty := filepath.Join(tmp, "empty.sqlite")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if mounter.IndexFileReady(empty) {
		t.Fatal("empty file not ready")
	}
	good := filepath.Join(tmp, "good.sqlite")
	if err := os.WriteFile(good, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mounter.IndexFileReady(good) {
		t.Fatal("non-empty should be ready")
	}
	if mounter.IndexFileReady("") || mounter.IndexFileReady(filepath.Join(tmp, "nope")) {
		t.Fatal("missing should not be ready")
	}
}

func TestArchiveUsesInMemoryIndex(t *testing.T) {
	t.Parallel()
	if !mounter.ArchiveUsesInMemoryIndex("/a/b.7z", "python") {
		t.Fatal("python 7z should use in-memory")
	}
	if mounter.ArchiveUsesInMemoryIndex("/a/b.7z", "rust") {
		t.Fatal("rust 7z should not")
	}
	if mounter.ArchiveUsesInMemoryIndex("/a/b.tar", "python") {
		t.Fatal("tar should not")
	}
}

func TestIndexBuildVerified(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	idx := filepath.Join(tmp, "i.sqlite")
	if err := os.WriteFile(idx, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mounter.IndexBuildVerified(idx, "/a.tar", nil, "rust") {
		t.Fatal("disk index should verify")
	}
	zero := 0
	if !mounter.IndexBuildVerified("/missing", "/a.7z", &zero, "python") {
		t.Fatal("python 7z exit 0 should verify without disk")
	}
	one := 1
	if mounter.IndexBuildVerified("/missing", "/a.7z", &one, "python") {
		t.Fatal("exit 1 should not verify")
	}
	if mounter.IndexBuildVerified("/missing", "/a.7z", &zero, "rust") {
		t.Fatal("rust needs disk index")
	}
}

func TestUsesSinglePhaseMount(t *testing.T) {
	t.Parallel()
	if mounter.UsesSinglePhaseMount("/a.7z", nil, "rust", true) {
		t.Fatal("rust never single-phase")
	}
	if !mounter.UsesSinglePhaseMount("/a.7z", nil, "python", true) {
		t.Fatal("python sevenzip available → single-phase")
	}
	if mounter.UsesSinglePhaseMount("/a.7z", nil, "python", false) {
		t.Fatal("sevenzip unavailable → two-phase")
	}
	if mounter.UsesSinglePhaseMount("/a.7z", []string{"--use-backend", "py7zr"}, "python", true) {
		t.Fatal("forced py7zr disables single-phase")
	}
	if mounter.UsesSinglePhaseMount("/a.tar", nil, "python", true) {
		t.Fatal("non-7z")
	}
}

func TestResolveNeedsIndex(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing.sqlite")
	if !mounter.ResolveNeedsIndex(missing, "/a.tar", nil, nil, "rust", false) {
		t.Fatal("missing index needs rebuild")
	}
	ready := filepath.Join(tmp, "ready.sqlite")
	if err := os.WriteFile(ready, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mounter.ResolveNeedsIndex(ready, "/a.tar", nil, nil, "rust", false) {
		t.Fatal("existing index skips rebuild")
	}
	trueVal := true
	if !mounter.ResolveNeedsIndex(ready, "/a.tar", &trueVal, nil, "rust", false) {
		t.Fatal("firstIndex forces rebuild")
	}
	// Single-phase 7z skips index-only even without disk index.
	if mounter.ResolveNeedsIndex(missing, "/a.7z", &trueVal, nil, "python", true) {
		t.Fatal("single-phase should skip index-only")
	}
}

func TestForcedBackend(t *testing.T) {
	t.Parallel()
	if got := mounter.ForcedBackend([]string{"--use-backend", "LibArchive"}); got != "libarchive" {
		t.Fatalf("got %q", got)
	}
	if got := mounter.ForcedBackend([]string{"--use-backend=py7zr"}); got != "py7zr" {
		t.Fatalf("got %q", got)
	}
	if got := mounter.ForcedBackend([]string{"-B", "sevenzip"}); got != "sevenzip" {
		t.Fatalf("got %q", got)
	}
	if got := mounter.ForcedBackend(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
