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

    await expect(page.getByText(/^Applied/i).or(page.getByText(/Applied/i))).toBeVisible()
    await expect(page.locator('.banner')).toContainText(/written=true|reloaded=true|Changed/i)

    await expect.poll(() => posts.some((p) => p.apply === true)).toBe(true)
    const applyCall = posts.find((p) => p.apply === true)
    expect(applyCall?.config?.recursive).toBe(!wasChecked)
  })
})
