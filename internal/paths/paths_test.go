package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToWSLPathDriveLetters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"backslash", `D:\Archives`, "/mnt/d/Archives"},
		{"forward_slash", "D:/Archives", "/mnt/d/Archives"},
		{"drive_root_backslash", `C:\`, "/mnt/c"},
		{"drive_root_slash", "C:/", "/mnt/c"},
		{"drive_letter_only", "E:", "/mnt/e"},
		{"nested_backslash", `D:\Data\Archives\2024`, "/mnt/d/Data/Archives/2024"},
		{"mixed_separators", `D:/Data\Archives`, "/mnt/d/Data/Archives"},
		{"lower_drive", `d:\foo`, "/mnt/d/foo"},
		{"upper_drive_z", `Z:\bar`, "/mnt/z/bar"},
		{"strips_double_quotes", `  "D:\Archives"  `, "/mnt/d/Archives"},
		{"strips_single_quotes", "  'C:/temp'  ", "/mnt/c/temp"},
	}
	// Disable wslpath so pure-Go mapping is exercised even if wslpath is on PATH.
	opts := &ToWSLOpts{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ToWSLPath(tc.in, opts)
			if err != nil {
				t.Fatalf("ToWSLPath(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ToWSLPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToWSLPathLinuxPassthrough(t *testing.T) {
	t.Parallel()
	opts := &ToWSLOpts{}
	for _, p := range []string{
		"/var/lib/tarmount-wsl/inbox",
		"/mnt/d/Archives",
		"/home/user/archives",
	} {
		got, err := ToWSLPath(p, opts)
		if err != nil {
			t.Fatalf("ToWSLPath(%q): %v", p, err)
		}
		if got != p {
			t.Fatalf("passthrough: got %q want %q", got, p)
		}
	}
}

func TestToWSLPathWSLUNCReject(t *testing.T) {
	t.Parallel()
	opts := &ToWSLOpts{}
	paths := []string{
		`\\wsl.localhost\Ubuntu-24.04\home\u\archives`,
		`\\wsl$\Ubuntu\home\u`,
		"//wsl.localhost/Ubuntu-24.04/home/u",
		"//wsl$/Ubuntu/tmp",
		`\\WSL.LOCALHOST\Ubuntu\tmp`,
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			_, err := ToWSLPath(p, opts)
			if err == nil {
				t.Fatalf("expected error for %q", p)
			}
			var pe *PathMapError
			if !errors.As(err, &pe) {
				t.Fatalf("want PathMapError, got %T: %v", err, err)
			}
			if !strings.Contains(pe.Message, "UNC WSL paths are not supported") {
				t.Fatalf("unexpected message: %s", pe.Message)
			}
		})
	}
}

func TestToWSLPathGenericUNC(t *testing.T) {
	t.Parallel()

	t.Run("without_runner_fails", func(t *testing.T) {
		t.Parallel()
		_, err := ToWSLPath(`\\fileserver\share\archives`, &ToWSLOpts{})
		if err == nil {
			t.Fatal("expected error without wslpath")
		}
		if !strings.Contains(err.Error(), "without wslpath") {
			t.Fatalf("unexpected message: %v", err)
		}
	})

	t.Run("with_runner_succeeds", func(t *testing.T) {
		t.Parallel()
		runner := func(p string) (string, error) {
			if !strings.Contains(strings.ReplaceAll(p, `\`, `/`), "fileserver") {
				t.Fatalf("runner got unexpected path %q", p)
			}
			return "/mnt/unc/share/archives", nil
		}
		got, err := ToWSLPath(`\\fileserver\share\archives`, &ToWSLOpts{WSLPathRunner: runner})
		if err != nil {
			t.Fatal(err)
		}
		if got != "/mnt/unc/share/archives" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestToWSLPathErrors(t *testing.T) {
	t.Parallel()
	opts := &ToWSLOpts{}

	for _, p := range []string{"", "   "} {
		_, err := ToWSLPath(p, opts)
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("empty path %q: want empty error, got %v", p, err)
		}
	}

	_, err := ToWSLPath("relative/path", opts)
	if err == nil || !strings.Contains(err.Error(), "Cannot map path") {
		t.Fatalf("relative without wslpath: %v", err)
	}

	got, err := ToWSLPath("relative/path", &ToWSLOpts{
		WSLPathRunner: func(p string) (string, error) {
			return "/resolved/" + p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/resolved/relative/path" {
		t.Fatalf("got %q", got)
	}
}

func TestMapSourceDirs(t *testing.T) {
	t.Parallel()
	opts := &ToWSLOpts{}

	pairs, err := MapSourceDirs([]string{`D:\Archives`, "/var/lib/tarmount-wsl/inbox"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("len=%d", len(pairs))
	}
	if pairs[0] != [2]string{`D:\Archives`, "/mnt/d/Archives"} {
		t.Fatalf("pair0=%v", pairs[0])
	}
	if pairs[1] != [2]string{"/var/lib/tarmount-wsl/inbox", "/var/lib/tarmount-wsl/inbox"} {
		t.Fatalf("pair1=%v", pairs[1])
	}

	_, err = MapSourceDirs([]string{"/ok", `\\wsl$\Ubuntu\x`}, opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "source_dirs[1]") {
		t.Fatalf("want indexed error, got %v", err)
	}
}

func TestIsDrvFsPath(t *testing.T) {
	t.Parallel()
	trueCases := []string{"/mnt/c/Users", "/mnt/d", "/mnt/Z/foo", "/mnt//c//foo"}
	for _, p := range trueCases {
		if !IsDrvFsPath(p) {
			t.Fatalf("IsDrvFsPath(%q) = false, want true", p)
		}
	}
	falseCases := []string{"/var/lib/tarmount-wsl", "/mnt", "/mnt/cache/foo", "mnt/c", "relative"}
	for _, p := range falseCases {
		if IsDrvFsPath(p) {
			t.Fatalf("IsDrvFsPath(%q) = true, want false", p)
		}
	}
}

func TestIsWSLUNCAndUNC(t *testing.T) {
	t.Parallel()
	if !IsWSLUNCPath(`\\wsl.localhost\Ubuntu\home`) {
		t.Fatal("expected WSL UNC")
	}
	if !IsWSLUNCPath("//wsl$/Ubuntu") {
		t.Fatal("expected WSL UNC //wsl$")
	}
	if IsWSLUNCPath(`\\server\share`) {
		t.Fatal("generic UNC is not WSL UNC")
	}
	if IsWSLUNCPath(`D:\Archives`) {
		t.Fatal("drive letter is not WSL UNC")
	}

	if !IsUNCPath(`\\server\share`) {
		t.Fatal("expected UNC")
	}
	if !IsUNCPath("//server/share") {
		t.Fatal("expected // UNC")
	}
	if IsUNCPath(`D:\Archives`) {
		t.Fatal("drive is not UNC")
	}
	if IsUNCPath("/mnt/c") {
		t.Fatal("linux is not UNC")
	}
}

func TestSanitizeMountName(t *testing.T) {
	t.Parallel()

	if got := SanitizeMountName("data.tar.gz", "abc12345-uuid", nil); got != "data.tar.gz" {
		t.Fatalf("simple: %q", got)
	}

	if got := SanitizeMountName("my archive (1).tar", "id", nil); got != "my_archive_1_.tar" {
		t.Fatalf("unsafe: %q", got)
	}

	if got := SanitizeMountName("___foo...bar___", "id", nil); got != "foo...bar" {
		t.Fatalf("collapse/strip: %q", got)
	}

	name := SanitizeMountName("...weird name!!!", "id", nil)
	if strings.HasPrefix(name, ".") {
		t.Fatalf("should not start with dot: %q", name)
	}
	if strings.Contains(name, " ") {
		t.Fatalf("should not contain space: %q", name)
	}

	if got := SanitizeMountName("", "id", nil); got != "archive" {
		t.Fatalf("empty: %q", got)
	}
	if got := SanitizeMountName("!!!", "id", nil); got != "archive" {
		t.Fatalf("all unsafe: %q", got)
	}

	long := strings.Repeat("a", 200) + ".tar"
	got := SanitizeMountName(long, "id", nil)
	if len(got) > 120 {
		t.Fatalf("max length: len=%d %q", len(got), got)
	}

	archiveID := "abcdef12-3456-7890"
	taken := map[string]struct{}{"data.tar.gz": {}}
	got = SanitizeMountName("data.tar.gz", archiveID, taken)
	if got != "data.tar.gz--abcdef12" {
		t.Fatalf("collision: %q", got)
	}
	if _, ok := taken[got]; ok {
		t.Fatalf("candidate should not already be in taken: %q", got)
	}

	taken = map[string]struct{}{
		"data.tar.gz":           {},
		"data.tar.gz--abcdef12": {},
	}
	got = SanitizeMountName("data.tar.gz", archiveID, taken)
	// After 8-char suffix is taken, expand id to 9 chars: "abcdef12-"
	if got != "data.tar.gz--abcdef12-" {
		t.Fatalf("double collision: got %q, want data.tar.gz--abcdef12-", got)
	}
}

func TestEnsureServiceDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b", "c")
	if err := EnsureServiceDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatal("not a dir")
	}
	// Mode check is best-effort; on some FS umask may apply to MkdirAll.
	// Chmod is applied after create — verify leaf is at least user-accessible.
	if st.Mode().Perm()&0o500 != 0o500 {
		t.Fatalf("expected user r-x, got %o", st.Mode().Perm())
	}

	// EnsureServiceDirectories creates multiple paths without chown.
	d2 := filepath.Join(root, "x")
	d3 := filepath.Join(root, "y")
	if err := EnsureServiceDirectories([]string{d2, d3}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d2, d3} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}

	// Unknown owner/group must not fail (best-effort no-op).
	if err := EnsureServiceDirectories([]string{d2}, &EnsureServiceDirsOpts{
		Owner: "nonexistent-user-mount-wrapper-test",
		Group: "nonexistent-group-mount-wrapper-test",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPathMapErrorType(t *testing.T) {
	t.Parallel()
	_, err := ToWSLPath("", &ToWSLOpts{})
	var pe *PathMapError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PathMapError, got %T", err)
	}
	if pe.Error() == "" {
		t.Fatal("empty error string")
	}
}
