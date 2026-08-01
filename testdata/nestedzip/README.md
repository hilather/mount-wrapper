# Nested zip fixtures

Small zip with embedded archive members for convert zip→7z repack tests.

## Layout

| Path | Role |
|------|------|
| `nested-with-archives.zip` | Zip containing `payloads/inner.7z`, `payloads/bundle.tar.gz`, plus plain `readme.txt` / `pad.bin`. |
| `manifest.json` | Member names, sizes, and which basenames count as embedded archives. |

## What is **not** committed

- The repacked `.7z` / convert metadata sidecar (produced under `t.TempDir()` when `7z` is on `PATH`).
- Full engine convert / FUSE of this zip (operator path; needs serve + engines).

## Residual

| Need | When |
|------|------|
| `ZipHasEmbeddedArchives` / `ShouldRepackZip` on committed bytes | Always — pure zip open + suffix scan (no 7z) |
| `RunZipRepack` with real `7z` | Skips unless `7z` (or `7zz`) is executable |
| Engine / mount after repack | Outside default unit CI |

Default `make test` stays green without 7z: offline membership tests run; real-binary repack calls `t.Skip`. **No stream-flatten** in this path (CLI extract → stored `-ms=off` 7z only).

## Regenerating the fixture

```bash
# from repo root; requires python3 + 7z (or 7zz) + tar
BUILD=$(mktemp -d)
mkdir -p "$BUILD/payloads"
printf 'top-level readme for nestedzip fixture\n' > "$BUILD/readme.txt"
# ~4 KiB pad so stored 7z output exceeds ZipRepackMinOKSize floor (1024)
python3 -c "open('$BUILD/pad.bin','wb').write(b'PADDATA\n'*512)"
printf 'inner payload for nestedzip fixture\n' > "$BUILD/inner.txt"
(cd "$BUILD" && 7z a -t7z -mx=0 -ms=off payloads/inner.7z inner.txt >/dev/null)
tar -czf "$BUILD/payloads/bundle.tar.gz" -C "$BUILD" inner.txt

python3 - <<PY
import zipfile, os, json
root = "$BUILD"
out = "testdata/nestedzip/nested-with-archives.zip"
members = [
    "readme.txt",
    "pad.bin",
    "payloads/inner.7z",
    "payloads/bundle.tar.gz",
]
with zipfile.ZipFile(out, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for name in members:
        zf.write(os.path.join(root, name), arcname=name)
info = []
with zipfile.ZipFile(out) as zf:
    for i in zf.infolist():
        low = i.filename.lower()
        emb = any(low.endswith(s) for s in (".7z", ".tar.gz", ".tgz", ".tar", ".zip", ".rar"))
        info.append({
            "name": i.filename,
            "compressed_size": i.compress_size,
            "uncompressed_size": i.file_size,
            "embedded_archive": emb,
        })
man = {
    "fixture": "nested-with-archives.zip",
    "purpose": "zip with nested archive members for ShouldRepackZip / RunZipRepack real-7z tests",
    "members": info,
}
with open("testdata/nestedzip/manifest.json", "w") as f:
    json.dump(man, f, indent=2)
    f.write("\n")
print("wrote", out, "size", os.path.getsize(out))
PY
rm -rf "$BUILD"
```

Sanity: after regenerate, `7z x` the zip and `7z a -t7z -ms=off -mx=0` the work tree; output must be ≥ `max(zip_size/4, 1024)`.
