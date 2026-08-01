package match

import (
	"errors"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/config"
)

func TestDefaultRegexMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"a.tar", true},
		{"a.tar.gz", true},
		{"a.tgz", true},
		{"a.tar.bz2", true},
		{"a.tbz2", true},
		{"a.tar.xz", true},
		{"a.txz", true},
		{"a.tar.zst", true},
		{"a.zip", true},
		// Default regex is case-sensitive — uppercase ZIP must not match.
		{"My.Archive.ZIP", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchesArchiveName(tc.name, "", nil)
			if err != nil {
				t.Fatalf("MatchesArchiveName(%q): %v", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("MatchesArchiveName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestDefaultRegexRejects(t *testing.T) {
	t.Parallel()
	rejects := []string{
		"a.rar",
		"a.7z",
		"a.iso",
		"a.tar.lz4",
		"readme.txt",
		"archive",
		"foo.tar.gz.part",
	}
	for _, name := range rejects {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchesArchiveName(name, "", nil)
			if err != nil {
				t.Fatalf("MatchesArchiveName(%q): %v", name, err)
			}
			if got {
				t.Fatalf("MatchesArchiveName(%q) = true, want false", name)
			}
		})
	}
}

func TestCaseInsensitiveOptIn(t *testing.T) {
	t.Parallel()
	re := `(?i).*\.(tar(\.(gz|bz2|xz|zst))?|tgz|tbz2|txz|zip)$`
	got, err := MatchesArchiveName("DATA.ZIP", re, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("expected case-insensitive regex to match DATA.ZIP")
	}
}

func TestISOOptInViaCustomRegex(t *testing.T) {
	t.Parallel()
	got, err := MatchesArchiveName("foo.iso", `.*\.iso$`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("expected custom iso regex to match foo.iso")
	}
	got, err = MatchesArchiveName("foo.zip", `.*\.iso$`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected custom iso regex to reject foo.zip")
	}
}

func TestNormalizeExtensions(t *testing.T) {
	t.Parallel()
	got := NormalizeExtensions([]string{"ZIP", ".tar.gz", "zip", ""})
	want := []string{".zip", ".tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeExtensions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeExtensions = %v, want %v", got, want)
		}
	}
	if NormalizeExtensions(nil) != nil {
		t.Fatal("nil input should yield nil")
	}
	if NormalizeExtensions([]string{}) != nil {
		t.Fatal("empty input should yield nil")
	}
	if NormalizeExtensions([]string{"", "  "}) != nil {
		t.Fatal("blank-only input should yield nil")
	}
}

func TestExtensionAllowList(t *testing.T) {
	t.Parallel()
	// zip matches default regex but not allow-list of tar.gz only
	got, err := MatchesArchiveName("a.zip", "", []string{".tar.gz"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("a.zip should fail extension allow-list of .tar.gz")
	}
	got, err = MatchesArchiveName("a.tar.gz", "", []string{".tar.gz", ".zip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("a.tar.gz should match allow-list including .tar.gz")
	}
}

func TestExtensionMultipart(t *testing.T) {
	t.Parallel()
	if !ExtensionAllowed("foo.tar.gz", []string{".tar.gz"}) {
		t.Fatal("foo.tar.gz should be allowed for .tar.gz")
	}
	if ExtensionAllowed("foo.gz", []string{".tar.gz"}) {
		t.Fatal("foo.gz should not match .tar.gz allow-list")
	}
}

func TestEmptyExtensionsMeansNoFilter(t *testing.T) {
	t.Parallel()
	for _, exts := range [][]string{nil, {}} {
		got, err := MatchesArchiveName("a.zip", "", exts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatalf("empty extensions %v should not filter a.zip", exts)
		}
	}
}

func TestUsesBasenameOnly(t *testing.T) {
	t.Parallel()
	cases := []string{
		`D:\Archives\data.tar.gz`,
		"/mnt/d/Archives/data.zip",
		"/tmp/data.tgz",
	}
	for _, name := range cases {
		got, err := MatchesArchiveName(name, "", nil)
		if err != nil {
			t.Fatalf("MatchesArchiveName(%q): %v", name, err)
		}
		if !got {
			t.Fatalf("MatchesArchiveName(%q) = false, want true", name)
		}
	}
}

func TestRejectsEmptyAndDot(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", ".."} {
		got, err := MatchesArchiveName(name, "", nil)
		if err != nil {
			t.Fatalf("MatchesArchiveName(%q): %v", name, err)
		}
		if got {
			t.Fatalf("MatchesArchiveName(%q) = true, want false", name)
		}
	}
}

func TestFilterPreservesOrderAndBasenames(t *testing.T) {
	t.Parallel()
	names := []string{"skip.txt", "a.zip", "b.tar", "c.rar", "d.tgz"}
	got, err := FilterArchiveNames(names, "", nil)
	if err != nil {
		t.Fatalf("FilterArchiveNames: %v", err)
	}
	want := []string{"a.zip", "b.tar", "d.tgz"}
	if len(got) != len(want) {
		t.Fatalf("FilterArchiveNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FilterArchiveNames = %v, want %v", got, want)
		}
	}

	got, err = FilterArchiveNames([]string{"/tmp/x.zip", `D:\y.tar`}, "", nil)
	if err != nil {
		t.Fatalf("FilterArchiveNames: %v", err)
	}
	want = []string{"x.zip", "y.tar"}
	if len(got) != len(want) {
		t.Fatalf("FilterArchiveNames basenames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FilterArchiveNames basenames = %v, want %v", got, want)
		}
	}
}

func TestInvalidRegex(t *testing.T) {
	t.Parallel()
	_, err := CompileNameRegex("[unclosed")
	if err == nil {
		t.Fatal("expected MatchError for invalid regex")
	}
	var me *MatchError
	if !errors.As(err, &me) {
		t.Fatalf("error type %T, want *MatchError", err)
	}
	if !strings.Contains(err.Error(), "invalid name_regex") {
		t.Fatalf("error message %q missing invalid name_regex", err.Error())
	}

	ok, err := MatchesArchiveName("a.zip", "[unclosed", nil)
	if err == nil || ok {
		t.Fatal("MatchesArchiveName should fail on invalid regex")
	}
	_, err = FilterArchiveNames([]string{"a.zip"}, "[unclosed", nil)
	if err == nil {
		t.Fatal("FilterArchiveNames should fail on invalid regex")
	}
}

func TestCompileNameRegexDefault(t *testing.T) {
	t.Parallel()
	re, err := CompileNameRegex("")
	if err != nil {
		t.Fatalf("CompileNameRegex(\"\"): %v", err)
	}
	if re.String() != config.DefaultNameRegex {
		t.Fatalf("default pattern = %q, want %q", re.String(), config.DefaultNameRegex)
	}
}

func TestFilterWithExtensions(t *testing.T) {
	t.Parallel()
	names := []string{"a.zip", "b.tar.gz", "c.tar"}
	got, err := FilterArchiveNames(names, "", []string{"tar.gz", "ZIP"})
	if err != nil {
		t.Fatalf("FilterArchiveNames: %v", err)
	}
	want := []string{"a.zip", "b.tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
