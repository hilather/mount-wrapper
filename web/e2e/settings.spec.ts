import { expect, test } from '@playwright/test'
import { mockConfigApi, mockShellApi, type ConfigPostCall } from './helpers'

test.describe('Settings page (mocked API)', () => {
  test('loads groups, edits a field, Validate dry-run succeeds', async ({ page }) => {
    const posts: ConfigPostCall[] = []

    await mockShellApi(page)
    await mockConfigApi(page, {
      onPost: (call) => {
        posts.push(call)
      },
    })

    await page.goto('/')
    await page.getByRole('button', { name: 'Settings', exact: true }).click()

    await expect(page.getByRole('heading', { name: 'Settings', level: 2 })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Sources', level: 3 })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Paths', level: 3 })).toBeVisible()

    // Hot-loaded meta line after GET /api/config
    await expect(page.getByText(/Loaded from service/i)).toBeVisible()
    await expect(page.getByText(/\/tmp\/e2e\/config\.yaml/)).toBeVisible()

    // Edit a hot-reloadable numeric field
    const poll = page.locator('#cfg-poll_interval_seconds')
    await expect(poll).toBeVisible()
    await poll.fill('45')

    await page.getByRole('button', { name: 'Validate', exact: true }).click()

    await expect(page.getByText(/Validation OK \(dry-run\)/i)).toBeVisible()
    await expect(page.locator('.banner')).toContainText(/poll_interval_seconds|Changed/i)

    // At least one dry-run POST with apply:false
    await expect.poll(() => posts.length).toBeGreaterThanOrEqual(1)
    const dryRun = posts.find((p) => p.apply === false)
    expect(dryRun).toBeTruthy()
    expect(dryRun?.config).toBeTruthy()
    expect(Number(dryRun?.config?.poll_interval_seconds)).toBe(45)
  })

  test('Apply posts apply:true and shows Applied banner', async ({ page }) => {
    const posts: ConfigPostCall[] = []

    await mockShellApi(page)
    await mockConfigApi(page, {
      onPost: (call) => {
        posts.push(call)
      },
    })

    await page.goto('/')
    await page.getByRole('button', { name: 'Settings', exact: true }).click()

    await expect(page.getByRole('heading', { name: 'Settings', level: 2 })).toBeVisible()

    // Toggle a simple bool (recursive under Sources)
    const recursive = page.locator('#cfg-recursive')
    await expect(recursive).toBeVisible()
    const wasChecked = await recursive.isChecked()
    await recursive.setChecked(!wasChecked)

    await page.getByRole('button', { name: 'Apply', exact: true }).click()

    // Prefer .banner (footnote also contains the substring "live-applied").
    await expect(page.locator('.banner').filter({ hasText: /Applied/i })).toBeVisible()
    await expect(page.locator('.banner')).toContainText(/written=true|reloaded=true|Changed/i)

    await expect.poll(() => posts.some((p) => p.apply === true)).toBe(true)
    const applyCall = posts.find((p) => p.apply === true)
    expect(applyCall?.config?.recursive).toBe(!wasChecked)
  })

  test('Apply with restart_required (web_token) shows sticky banner across Validate/Reload', async ({
    page,
  }) => {
    const posts: ConfigPostCall[] = []

    await mockShellApi(page)
    await mockConfigApi(page, {
      onPost: (call) => {
        posts.push(call)
      },
    })

    await page.goto('/')
    await page.getByRole('button', { name: 'Settings', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Settings', level: 2 })).toBeVisible()

    // web_token is restart-required and never live-applied (serve-start capture).
    const token = page.locator('#cfg-web_token')
    await expect(token).toBeVisible()
    await token.fill('e2e-restart-token')

    await page.getByRole('button', { name: 'Apply', exact: true }).click()

    const sticky = page.getByTestId('restart-required-banner')
    await expect(sticky).toBeVisible()
    await expect(sticky).toContainText(/web_token/)
    await expect(sticky).toContainText(/Process restart required/i)
    await expect(sticky).toContainText(/not live-applied|serve start/i)

    await expect.poll(() => posts.some((p) => p.apply === true)).toBe(true)
    const applyCall = posts.find((p) => p.apply === true)
    expect(applyCall?.config?.web_token).toBe('e2e-restart-token')

    // Validate dry-run must not clear sticky restart banner.
    await page.getByRole('button', { name: 'Validate', exact: true }).click()
    await expect(page.getByText(/Validation OK \(dry-run\)/i)).toBeVisible()
    await expect(sticky).toBeVisible()
    await expect(sticky).toContainText(/web_token/)

    // Reload from service clears the transient Applied/Validate banner only.
    await page.getByRole('button', { name: 'Reload from service', exact: true }).click()
    await expect(page.getByText(/Loaded from service/i)).toBeVisible()
    await expect(sticky).toBeVisible()
    await expect(sticky).toContainText(/web_token/)

    // Dismiss clears sticky state.
    await page.getByTestId('restart-required-dismiss').click()
    await expect(sticky).toHaveCount(0)
  })
})
