import { expect, test, type Page } from '@playwright/test'

/** Mock control-plane JSON used by the SPA on first load (no real daemon). */
async function mockApi(page: Page) {
  await page.route('**/api/health', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        service_reachable: true,
        version: 'e2e-test',
        web_version: 'e2e',
      }),
    })
  })

  await page.route('**/api/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ok: true,
        archives: [],
        counts: { mounted: 0, discovered: 0 },
        version: 'e2e-test',
      }),
    })
  })

  await page.route('**/api/archives', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        archives: [],
        counts: { mounted: 0, discovered: 0 },
        summary: {
          archive_count: 0,
          total_archive_size_bytes: 0,
          total_space_saved_bytes: 0,
        },
        version: 'e2e-test',
      }),
    })
  })

  await page.route('**/api/wsl-info', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ mount_root: '/mnt/wsl', hint: 'e2e mock' }),
    })
  })

  // SSE will error/reconnect with a short body; poll path still drives the UI.
  await page.route('**/api/events**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      headers: {
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      },
      body: 'event: heartbeat\ndata: {}\n\n',
    })
  })
}

test.describe('SPA smoke (mocked API)', () => {
  test('Archives heading and connection badge render', async ({ page }) => {
    await mockApi(page)
    await page.goto('/')

    await expect(page.getByRole('heading', { name: 'mount-wrapper', level: 1 })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Archives', level: 2 })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Overview', level: 2 })).toBeVisible()

    const badge = page.locator('span.badge')
    await expect(badge).toBeVisible()
    // After mocked health + archives, store leaves poll or SSE-connected labels.
    await expect(badge).toHaveText(/connected|reconnecting|service down|error|…/i)
  })
})
