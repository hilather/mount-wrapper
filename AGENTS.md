# Agent instructions — mount-wrapper

You are working in **mount-wrapper**, a Go daemon + TypeScript SPA rewrite of
[tarmount-wsl](https://github.com/mbrewer/tarmount-wsl): archive discovery,
indexing/mount via external ratarmount engines, hooks, cleanup, control plane,
and an operator dashboard.

This file is **mandatory policy** for every coding agent session in this repo
(Grok, Claude, Codex, Cursor, etc.).

**Behavior / plan sources of truth:**

| Surface | Source |
|---------|--------|
| Phased plan & decisions | `TODO.md` |
| Architecture sketch | `docs/architecture.md` |
| Dev workflow | `docs/dev.md` |
| Migration / parity | `docs/migration.md`, `docs/parity.md`, `tools/parity/` |
| Public product summary | `README.md` |
| Implemented Go code | `cmd/`, `internal/` |
| SPA | `web/` |
| Upstream parity reference | sibling `../tarmount-wsl` (local) |

Do not invent decisions that contradict the **Decisions log** in `TODO.md`.

---

## Non-negotiable: documentation stays current

**Every change that affects user-visible behavior, CLI, config keys, API,
defaults, architecture, packaging, or operator workflow must update docs in
the same change** (same commit preferred; same PR/session required before push).

| Change type | Update these |
|-------------|--------------|
| New/changed CLI command or flag | CLI help text in code + `README.md` + `docs/dev.md` if workflow changes |
| Config key, default, hot vs restart | `README.md` / example config under `packaging/examples/` + `TODO.md` Appendix D if inventory moves + SPA settings schema when present |
| HTTP/SSE API | `docs/` (or `docs/web-ui.md` when added) + SPA client types/comments |
| Status / lifecycle / convert pipeline | `docs/architecture.md` or design notes; keep status enum lists accurate |
| Package layout / new `internal/` package | `README.md` layout + this file’s project map + `TODO.md` Appendix A if mapped |
| Packaging / service user / paths | `packaging/**` examples + `README.md` paths table + Decisions implications in `TODO.md` if a decision changes |
| Phase checklist progress | `TODO.md` checkboxes |
| Docs-only / comment-only | No extra churn; still fix anything you know is wrong |

When removing or renaming a flag, config key, API path, or status: **grep**
`README.md`, `TODO.md`, `docs/`, `AGENTS.md`, `packaging/`, `web/` and fix all hits.

**Do not** leave `TODO.md` claiming a phase is pending after you implement it,
or claiming a default that no longer matches code.

**Skill:** `.grok/skills/keep-docs-current/SKILL.md`  
Run before commit when user-visible surfaces may have drifted.

---

## Non-negotiable: tests cover every change

**Every change that alters behavior must add or update automated tests where
it makes sense — in the same change.**

Rules:

1. **User-visible behavior**, **config parse/validate**, **path mapping**,
   **state transitions**, **control/API protocol**, **scanner/mounter logic**,
   **hooks exit codes**, **metrics formulas**, **error handling**, and
   **bug fixes** MUST get tests in the **same PR** (same commit preferred).
2. **New features:** unit tests for pure logic **and**, when applicable, a
   higher-level test (CLI, API handler, or package integration) for the
   public path.
3. **Bug fixes:** a **regression test that fails before the fix and passes
   after** (red–green). Prefer a minimal fixture under `testdata/` or table-
   driven cases.
4. **Refactors with no behavior change:** new tests not strictly required if
   the suite still covers the paths; run the suite and do **not** delete
   coverage without replacement.
5. Do **not** claim “tested manually only” for shippable behavior.
6. Do not skip core-path tests with `t.Skip` / `testing.Short` without a
   documented reason; skipped tests do **not** count as coverage under this
   policy unless the skip is environment-gated (e.g. no `/dev/fuse`) and
   documented.
7. **Locations:**
   - Go: co-located `*_test.go` next to code; integration behind build tags
     or markers when FUSE/real engines are required
   - SPA: unit tests for formatters/stores when present; optional Playwright
     for smoke later
8. Before marking work done: **`make test`** (and **`make web-check`** if
   `web/` changed) green.
9. When behavior changes, **docs still required** — tests **and** docs together.

### Required tests by change type

| Change type | Required tests |
|-------------|----------------|
| Config parse / validate / duration / hot keys | Table-driven unit tests (valid + invalid) |
| Path / WSL / DrvFs helpers | Unit tests with fixed path strings |
| State transitions / claim / purge | Table-driven transition + cascade tests |
| Control socket ops / framing | Encode/decode + auth allow/deny with fakes |
| CLI flags / exit codes | `internal/cli` tests or golden help/exit |
| HTTP API / auth / SSE basics | Handler tests with fake store/control |
| Scanner stable-file / rescan | Unit tests with temp dirs + fake clock if needed |
| Metrics formulas | Pure function unit tests |
| Bug fix | Regression test that reproduced the bug |
| SPA formatter / store logic | Vitest (or project test runner) when introduced |
| Docs-only / comment-only | No new tests required |
| Pure refactor | Full suite green; no coverage drop without replacement |
| Packaging-only (unit files) | Smoke that examples still parse if config schema exists |

**Skill:** `.grok/skills/keep-tests-current/SKILL.md`  
Run before every commit when `cmd/`, `internal/`, `web/`, or behavior changed.

---

## Non-negotiable: code review every change set

**Do not treat implementation as done until the change set has been code-reviewed.**

Applies to agent-authored work (features, fixes, refactors with behavior risk).

### What “reviewed” means

1. After tests + docs are in place, run a **structured review** of the full
   diff (staged + unstaged + relevant untracked):
   - Prefer the Grok **`/review`** skill (local mode) or project skill
     `.grok/skills/review-changes/SKILL.md`.
   - If `/review` is unavailable, perform an equivalent **self-review** using
     the checklist below and write findings in the session response.
2. **Address all `bug`-severity findings** before claiming done (fix or
   explicitly defer with user agreement).
3. **Address or consciously defer** `suggestion` findings; nits may ship with
   a note.
4. Re-run **`make test`** (and web checks if needed) after review fixes.
5. Tiny pure-docs typo fixes may use a lighter self-review; still re-read the
   diff for accuracy.

### Review checklist (minimum)

- Correctness vs `TODO.md` decisions and parity intent  
- Missing tests / missing docs  
- Error handling and edge cases (partial indexes, DrvFs, peercred, race claims)  
- Security: hooks path escape, token default empty, loopback bind, socket auth  
- No secrets or local paths leaked into committed files  
- API/CLI naming consistency with decided prefixes (`mount-wrapper`,
  `MOUNT_WRAPPER_*`, paths under `/…/mount-wrapper`)  

**Skill:** `.grok/skills/review-changes/SKILL.md`

---

## Before-done / before-commit procedure

```text
1. Diff change set (git status / git diff)
2. keep-tests-current   (if code/behavior)
3. keep-docs-current    (if user-visible or plan/progress)
4. make test            (+ make web-check if web/ changed)
5. review-changes       (/review or structured self-review)
6. Fix bugs from review; re-test
7. Commit code + tests + docs together when practical
```

Do **not** present work as complete after step 1–4 only when 2–5 apply.

---

## Locked product decisions (do not quietly reverse)

| ID | Choice |
|----|--------|
| Module / remote | `github.com/hilather/mount-wrapper` |
| Binary / package / unit | `mount-wrapper` |
| Service user/group | `mount-wrapper` / `mount-wrapper` |
| Paths | `/etc`, `/var/lib`, `/run` + `/mount-wrapper` |
| SPA | Svelte 5; embedded in `serve` |
| Live UI | SSE + poll fallback |
| Hooks env | `MOUNT_WRAPPER_*` only (no `TARMOUNT_*`) |
| Default / only mount backend | `rust` (`ratarmount-rs` only; Python `ratarmount` unsupported) |
| License | MIT |

Full log: `TODO.md` → Decisions log.

---

## Build / test commands

```bash
export PATH="$HOME/.local/go/bin:$HOME/.local/node-v22.14.0-linux-x64/bin:$PATH"

make test          # go test ./...
make vet
make build         # ./bin/mount-wrapper
make web-check     # when web/ changed
make web-build     # SPA → internal/webui/dist for embed.FS
make lint          # golangci-lint if installed
```

Prefer table-driven Go tests. FUSE/real-engine tests: build tags or skip when
`/dev/fuse` / binaries are absent — document the gate in the test name/comment.

---

## Project map (short)

| Path | Role |
|------|------|
| `cmd/mount-wrapper/` | Binary entry |
| `internal/cli` | CLI surface |
| `internal/config` | YAML config |
| `internal/platform` | Host/FUSE/peercred |
| `internal/paths` | WSL/DrvFs path helpers |
| `internal/state` | SQLite lifecycle |
| `internal/scanner` | Discovery |
| `internal/mounter` | Engine children |
| `internal/convert` | 7z / zip convert pipeline |
| `internal/control` | Unix socket control plane |
| `internal/service` | Serve loop |
| `internal/api` | HTTP + SSE |
| `internal/webui` | `embed.FS` of SPA dist |
| `web/` | Svelte 5 SPA source |
| `packaging/` | systemd, launchd, examples, create-user, WSL/nfpm sketches |
| `docs/` | Architecture, dev, install, migration, parity, security notes |
| `tools/parity/` | Offline inventories vs tarmount-wsl |
| `packaging/man/` | Man page (`mount-wrapper.1`) |
| `testdata/` | Fixtures |
| `TODO.md` | Phased parity plan |

---

## Out of scope (unless TODO / design reopened)

- Reimplementing ratarmount inside Go  
- Windows native (non-WSL) FUSE service  
- WSL1  
- Explorer shell extension  
- Auto-commit write overlays into archives  
- Vendoring a private Python ratarmount venv in this package  

---

## Skills (project)

| Skill | Path |
|-------|------|
| keep-docs-current | `.grok/skills/keep-docs-current/SKILL.md` |
| keep-tests-current | `.grok/skills/keep-tests-current/SKILL.md` |
| review-changes | `.grok/skills/review-changes/SKILL.md` |

Also use bundled Grok **`/review`** and **`/check-work`** when available.
