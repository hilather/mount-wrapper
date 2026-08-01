package convert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/convert"
)

// writeFake7zScript writes a shell script that mimics the zip-repack 7z steps.
// mode: "ok" produces a large enough partial; "fail_extract" fails on x; "fail_create" fails on a.
func writeFake7zScript(t *testing.T, dir, mode string) string {
	t.Helper()
	path := filepath.Join(dir, "fake7z")
	var body string
	switch mode {
	case "ok":
		body = `#!/bin/sh
set -e
if [ "$1" = "x" ]; then
  out=""
  for a in "$@"; do
    case "$a" in
      -o*) out="${a#-o}" ;;
    esac
  done
  if [ -z "$out" ]; then exit 2; fi
  mkdir -p "$out"
  printf 'payload' > "$out/bundle.tgz"
  exit 0
fi
if [ "$1" = "a" ]; then
  # argv: a -t7z -ms=off -mx=0 -y <partial> *
  out="$6"
  # Write >= 1024 bytes so ZipRepackMinOKSize floor passes for small zips.
  i=0
  while [ "$i" -lt 64 ]; do
    printf 'stored-7z-output' >> "$out"
    i=$((i+1))
  done
  exit 0
fi
echo "unexpected args: $*" >&2
exit 3
`
	case "fail_extract":
		body = `#!/bin/sh
if [ "$1" = "x" ]; then
  echo "extract boom" >&2
  exit 1
fi
exit 0
`
	case "fail_create":
		body = `#!/bin/sh
if [ "$1" = "x" ]; then
  out=""
  for a in "$@"; do
    case "$a" in
      -o*) out="${a#-o}" ;;
    esac
  done
  mkdir -p "$out"
  printf 'x' > "$out/bundle.tgz"
  exit 0
fi
if [ "$1" = "a" ]; then
  echo "create boom" >&2
  exit 1
fi
exit 0
`
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunZipRepack_SuccessWithFake7zScript(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "bundle.tgz", []byte("payload"))
	bin := writeFake7zScript(t, dir, "ok")

	dest, meta, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin:   bin,
		OverheadBytes: 0,
		MinFreeBytes:  0,
		KeepSource:    true,
	})
	if err != nil {
		t.Fatalf("RunZipRepack: %v", err)
	}
	if dest != convert.ZipRepackDestPath(zipPath) {
		t.Fatalf("dest=%q", dest)
	}
	if meta.Method != convert.MethodZipRepack {
		t.Fatalf("method=%q", meta.Method)
	}
	if st, err := os.Stat(dest); err != nil || st.Size() < 1024 {
		t.Fatalf("dest missing/small: %v", err)
	}
	// Source renamed to backup.
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Fatalf("source should be moved: %v", err)
	}
	if _, err := os.Stat(convert.ZipRepackBackupPath(zipPath)); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if convert.ReadConvertMetadata(dest) == nil {
		t.Fatal("expected metadata sidecar")
	}
	// Work dir cleaned.
	if _, err := os.Stat(convert.ZipRepackWorkDir(zipPath)); !os.IsNotExist(err) {
		t.Fatal("work dir should be removed")
	}
}

func TestRunZipRepack_FailExtract(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "nested.7z", []byte("x"))
	bin := writeFake7zScript(t, dir, "fail_extract")

	_, _, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin:   bin,
		OverheadBytes: 0,
		KeepSource:    true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "7z failed") && !strings.Contains(err.Error(), "extract") {
		// DefaultRun7z wraps as "7z failed: ..."
		if !strings.Contains(err.Error(), "failed") {
			t.Fatalf("err=%v", err)
		}
	}
	// Source must remain; work dir cleaned.
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
	if _, err := os.Stat(convert.ZipRepackWorkDir(zipPath)); !os.IsNotExist(err) {
		t.Fatal("work dir should be cleaned on failure")
	}
}

func TestRunZipRepack_FailCreate(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "inner.tar", []byte("x"))
	bin := writeFake7zScript(t, dir, "fail_create")

	_, _, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin: bin,
		KeepSource:  true,
	})
	if err == nil {
		t.Fatal("expected create failure")
	}
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
}

func TestRunZipRepack_InsufficientSpace(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 10, true
	})
	t.Cleanup(restore)

	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "a.tgz", []byte("payload-bytes"))
	bin := writeFake7zScript(t, dir, "ok")

	_, _, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin:   bin,
		OverheadBytes: 0,
		MinFreeBytes:  0,
	})
	if err == nil || !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("want space error, got %v", err)
	}
}

func TestRunZipRepack_InjectableRunner(t *testing.T) {
	dir := t.TempDir()
	restore := convert.SetFreeBytesFunc(func(string) (int64, bool) {
		return 1 << 40, true
	})
	t.Cleanup(restore)

	zipPath := zipWithMember(t, filepath.Join(dir, "sample.zip"), "bundle.tgz", []byte("payload"))
	var sawExtract, sawCreate bool
	run := func(bin string, args []string, cwd string) error {
		if len(args) >= 1 && args[0] == "x" {
			sawExtract = true
			var out string
			for _, a := range args {
				if strings.HasPrefix(a, "-o") {
					out = strings.TrimPrefix(a, "-o")
				}
			}
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(out, "bundle.tgz"), []byte("payload"), 0o644)
		}
		if len(args) >= 1 && args[0] == "a" {
			sawCreate = true
			// partial is last path before *
			var partial string
			for _, a := range args {
				if strings.HasSuffix(a, ".partial") {
					partial = a
				}
			}
			if partial == "" {
				t.Fatalf("no partial in args=%v", args)
			}
			if cwd == "" {
				t.Fatal("create should set cwd to work dir")
			}
			// large enough
			return os.WriteFile(partial, []byte(strings.Repeat("7z", 600)), 0o644)
		}
		t.Fatalf("unexpected args bin=%s args=%v", bin, args)
		return nil
	}

	dest, meta, err := convert.RunZipRepack(zipPath, convert.ZipRepackParams{
		SevenZipBin: "7z-ignored",
		KeepSource:  true,
		Run7z:       run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawExtract || !sawCreate {
		t.Fatalf("extract=%v create=%v", sawExtract, sawCreate)
	}
	if meta.Method != convert.MethodZipRepack {
		t.Fatal(meta.Method)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestZipRepackParamsFromConfig(t *testing.T) {
	t.Parallel()
	// Uses defaults when cfg nil.
	p := convert.ZipRepackParamsFromConfig(nil, convert.ResolveOptions{SearchPathDisabled: true})
	if p.SevenZipBin != "7z" || !p.KeepSource {
		t.Fatalf("%+v", p)
	}
}
