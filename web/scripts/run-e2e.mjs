#!/usr/bin/env node
/**
 * Optional Playwright SPA smoke runner.
 *
 * Default (no env): exit 0 with skip message so `npm run test:e2e` is safe
 * offline / without browsers / in the main CI web job if accidentally invoked.
 *
 * Real run:
 *   npx playwright install chromium   # once per machine
 *   RUN_E2E=1 npm run test:e2e
 */
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))

if (process.env.RUN_E2E !== '1') {
  console.log(
    '[e2e] skipped — set RUN_E2E=1 to run Playwright smoke (requires: npx playwright install chromium)',
  )
  process.exit(0)
}

const result = spawnSync('npx', ['playwright', 'test'], {
  cwd: root,
  stdio: 'inherit',
  env: process.env,
  shell: process.platform === 'win32',
})

process.exit(result.status === null ? 1 : result.status)
