# testdata

Fixtures for unit and integration tests.

## nested7z/

Nested mini 7z outer + captured `7z l -slt` listings for convert flatten
probes. See [nested7z/README.md](nested7z/README.md).

| Kind | Source |
|------|--------|
| Mini outer binary | Copied from upstream `tarmount-wsl/tests/fixtures/nested-7z-mini/` (~142 KiB) |
| Multi outer binary | **Not** committed (upstream multi ~1.8 MiB); generated in TempDir when `7z` available |
| Listing text | `*.l-slt.txt` — offline `Parse7zListNeedsFlatten` without 7z |

**Residual:** full engine convert / flatten still needs `7z` on `PATH`. Default
`make test` skips real-binary probes when 7z is missing. **No stream-flatten.**

## nestedzip/

Zip with nested archive members for zip→7z repack. See
[nestedzip/README.md](nestedzip/README.md).

| Kind | Source |
|------|--------|
| `nested-with-archives.zip` | Committed (~1 KiB): `payloads/inner.7z`, `payloads/bundle.tar.gz`, pad + readme |
| `manifest.json` | Member inventory |

**Residual:** full engine convert after repack still needs serve + engines.
Default `make test` runs offline `ShouldRepackZip` / membership; real
`RunZipRepack` skips when `7z` is missing.
