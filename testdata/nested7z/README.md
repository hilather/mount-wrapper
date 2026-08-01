# Nested 7z fixtures

Small archives and captured `7z l -slt` listings for convert/flatten probe tests.

## Layout

| Path | Role |
|------|------|
| `SUP-36264-nested-mini.7z` | Outer non-solid 7z with four nested `*.7z` members (+ log). Copied from upstream `tarmount-wsl/tests/fixtures/nested-7z-mini/` (~142 KiB). |
| `SUP-36264-nested-mini.l-slt.txt` | Captured technical listing for offline `Parse7zListNeedsFlatten` (no 7z required). |
| `nested-multi-mini.l-slt.txt` | Captured listing for a synthetic multi-inner outer (inners generated in tests when `7z` is available). |
| `solid-mini.l-slt.txt` | Captured listing for a tiny solid (`Solid = +`) archive. |
| `manifest.json` | Inner member names / sizes for the mini outer. |

## What is **not** committed

- Upstream `nested-7z-multi/nested-multi-support.7z` (~1.8 MiB). Tests **generate** a minimal multi-nested outer in `t.TempDir()` when `7z` is on `PATH`.
- Full engine flatten / FUSE convert of these archives (still requires real engines + 7z).

## Residual

| Need | When |
|------|------|
| Unit parse of listings | Always — uses `*.l-slt.txt` and sample strings |
| `Probe7zNeedsFlatten` against real bytes | Skips unless `7z` (or configured bin) is executable |
| Full `RunFlattenConvert` / mount | Operator path; needs `7z` on `PATH` and engines outside default unit CI |

Default `make test` stays green without 7z: offline listing tests run; real-binary probes call `t.Skip`.

## Regenerating listing captures

```bash
7z l -slt testdata/nested7z/SUP-36264-nested-mini.7z | sed -n '/^Listing archive:/,$p' \
  | sed 's|.*/SUP-36264-nested-mini.7z|SUP-36264-nested-mini.7z|g' \
  > testdata/nested7z/SUP-36264-nested-mini.l-slt.txt
```

Multi/solid listings are produced the same way from TempDir archives in
`internal/convert/nested_fixture_test.go` (or regenerate manually and sanitize paths).
