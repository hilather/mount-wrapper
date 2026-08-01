# Cutting a release

How to ship the next patch after **v0.1.5** (e.g. **v0.1.6**). Operators
install from GitHub Releases — see [install.md](./install.md). Change log:
[CHANGELOG.md](../CHANGELOG.md). Field verification: [field-test.md](./field-test.md).

Do **not** tag until tests, docs, and review for the change set are green.

## Prerequisites

- Clean `main` (or the release branch), pushed to `origin`
- Local tools: Go 1.25+, Node 22+ (for SPA embed), optional docker/podman for
  Rocky/musl smoke
- `PATH` includes your Go/Node bins (see [dev.md](./dev.md) / root `Makefile`)
- GitHub Actions enabled; `contents: write` on `release.yml` (default for
  `GITHUB_TOKEN` on this repo’s workflow)

## 1. Prep CHANGELOG

1. Move items under `## [Unreleased]` into a new section, e.g.:

   ```markdown
   ## [0.1.6] - YYYY-MM-DD
   ```

2. Leave an empty `## [Unreleased]` at the top for later work.
3. Add compare / tag links at the bottom (Keep a Changelog style):

   ```markdown
   [Unreleased]: https://github.com/hilather/mount-wrapper/compare/v0.1.6...HEAD
   [0.1.5]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.5
   [0.1.4]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.4
   [0.1.3]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.3
   …
   ```

4. Commit the changelog (and any last doc nits) on `main` **before** the tag
   so the release commit history matches published notes.

## 2. Local verification

```bash
export PATH="$HOME/.local/go/bin:$HOME/.local/node-v22.14.0-linux-x64/bin:$PATH"

make test
make vet
make web-check    # if web/ changed since last release
make web-build
make build
make smoke        # version / doctor / config show / serve --once
# Deb content inventory (nfpm + dpkg-deb; soft-skips if tools missing):
# make smoke-package
# Always-on under make test: TestPackageTarInventory (synthetic GoReleaser layout).
# Real dist tar after snapshot (no nfpm required):
# PACKAGE_TAR=dist/mount-wrapper_*_linux_amd64.tar.gz SKIP_DEB=1 ./scripts/smoke-package-contents.sh
# CHECK_TAR=1 ./scripts/smoke-package-contents.sh

# Optional (docker/podman):
# make smoke-rocky
# make smoke-musl

# Optional local multi-arch snapshot (does not publish):
# make release-snapshot
# sha256sum -c dist/SHA256SUMS
```

Confirm `./bin/mount-wrapper version` reflects a dirty or describe string
pre-tag; post-tag builds should show the tag via `git describe`.

## 3. Tag and push

Annotated tag (preferred; matches v0.1.0 style):

```bash
# On the commit you want to release (usually latest main):
git status   # clean
git pull --ff-only origin main

git tag -a v0.1.6 -m "mount-wrapper v0.1.5

Brief summary of the patch (see CHANGELOG)."

# Push branch first if needed, then the tag:
git push origin main
git push origin v0.1.6
```

Tag pattern must match `v*` so [`.github/workflows/release.yml`](../.github/workflows/release.yml)
runs GoReleaser publish (`release --clean`).

**Do not** retag or force-push an existing release tag after artifacts are
published; cut `v0.1.7` instead if a fix is needed.

## 4. Verify GitHub Actions

1. Open **Actions** → workflow **release** for tag `v0.1.6`.
2. Expect job **goreleaser** green:
   - checkout (full history), Go 1.25, Node 22, `npm ci` in `web/`
   - GoReleaser builds SPA embed + multi-arch binaries
3. Open the GitHub **Release** for `v0.1.6` and confirm assets:

   | Asset class | Examples |
   |-------------|---------|
   | Linux tarballs | `mount-wrapper_*_linux_amd64.tar.gz`, `*_linux_arm64.tar.gz` |
   | macOS tarballs | `*_darwin_amd64.tar.gz`, `*_darwin_arm64.tar.gz` |
   | Packages | `*.deb`, `*.rpm` (amd64 + arm64) |
   | Checksums | `SHA256SUMS` |

4. Confirm workflow artifact **mount-wrapper-dist** uploaded (same `dist/*`).
5. Spot-check CI on the same commit if not already green:
   - **ci** — Ubuntu go-test + web; **macos-unit-smoke**
   - **smoke** — Ubuntu binary smoke, Rocky 8 container, **package-contents-smoke**
     (nfpm deb inventory), **musl-static-smoke**

## 5. Smoke an installed artifact (recommended)

```bash
# From the release tarball or package:
sha256sum -c SHA256SUMS
./scripts/smoke-binary.sh   # or PATH=… make smoke with BIN set
# Then optional real FUSE field test: docs/field-test.md
```

## 6. Snapshot-only dry run (no tag)

To exercise packaging without publishing:

- Actions → **release** → **Run workflow** with `snapshot: true` (default), or
- Locally: `make release-snapshot`

## Optional: refresh Homebrew formula digests

After `dist/SHA256SUMS` exists (snapshot or real release assets):

```bash
VERSION=0.1.5 SHA256SUMS=dist/SHA256SUMS \
  OUT=packaging/homebrew/mount-wrapper.rb \
  ./scripts/update-homebrew-formula.sh
# Local only — do not commit real digests unless publishing a tap:
# brew install --formula ./packaging/homebrew/mount-wrapper.rb
```

The in-tree sketch keeps `REPLACE_ME_DARWIN_*` placeholders; the script rewrites
`version` + both darwin `sha256` lines. CI does **not** run `brew install`.

## Residual / not part of the tag flow

- Homebrew **tap** automation (formula sketch is usable with
  `scripts/update-homebrew-formula.sh`; no tap publish yet)
- Dual goreleaser **musl** artifact matrix (optional D7 path is local/CI only)
- macFUSE real-mount CI (local/manual; see [macos.md](./macos.md))

## Version policy (v0.1.x)

| Kind | When |
|------|------|
| Patch (`0.1.x`) | Fixes, engine policy tightenings, CI/docs, convert hardening that does not break config schema |
| Minor (`0.2.0`) | New public config keys / CLI surface / API ops that operators must learn |
| Major (`1.0.0`) | When the project declares stable public API / packaging (see product plan) |

Embed path: `make build` / goreleaser set `main.version` / `main.commit` /
`main.date` (`mount-wrapper version`).
