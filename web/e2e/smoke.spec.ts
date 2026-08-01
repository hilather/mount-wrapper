import { expect, test } from '@playwright/test'
import { mockShellApi } from './helpers'

test.describe('SPA smoke (mocked API)', () => {
  test('Archives heading and connection badge render', async ({ page }) => {
    await mockShellApi(page)
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
