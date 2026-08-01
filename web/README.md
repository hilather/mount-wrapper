# mount-wrapper web (Svelte 5 + Vite)

Operator SPA source. Production build is copied into `../internal/webui/dist` for `embed.FS`.

```bash
npm install
npm run dev      # http://localhost:5173 — proxies /api → :8787
npm run build
npm run check
npm test         # vitest unit tests (no browsers)
```

## Optional Playwright smoke

Mocks `/api/*` in-browser (no mount-wrapper daemon). Gated so default scripts stay green without browsers:

```bash
npm run test:e2e:install     # once: Chromium
RUN_E2E=1 npm run test:e2e   # or from repo root: make web-e2e
npm run test:e2e             # without RUN_E2E=1 → skip exit 0
```

Specs:

| File | Coverage |
|------|----------|
| `e2e/smoke.spec.ts` | Archives heading + connection badge |
| `e2e/settings.spec.ts` | Settings groups, Validate dry-run, Apply (`GET`/`POST /api/config`) |
| `e2e/helpers.ts` | Shared `page.route` mocks |

Default CI `web-check` does **not** install browsers or set `RUN_E2E`.

Typed API shapes: `src/lib/api-types.ts` (hand-written re-exports; D11 — not OpenAPI).

From repo root: `make web-dev`, `make web-build`, `make web-test`, `make web-e2e`.
