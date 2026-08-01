import { expect, test } from '@playwright/test'
import {
  MOCK_ARCHIVE_ROWS,
  MOCK_FAILED_ARCHIVE,
  MOCK_MOUNTED_ARCHIVE,
  acceptNextDialog,
  mockShellApi,
  type ActionPostCall,
} from './helpers'

test.describe('Archives table + actions (mocked API)', () => {
  test('table shows mounted and mount_failed rows', async ({ page }) => {
    await mockShellApi(page, { archives: MOCK_ARCHIVE_ROWS })
    await page.goto('/')

    await expect(page.getByRole('heading', { name: 'Archives', level: 2 })).toBeVisible()

    const table = page.locator('table.archives')
    await expect(table).toBeVisible()
    await expect(table.getByText(MOCK_MOUNTED_ARCHIVE.archive_basename, { exact: true })).toBeVisible()
    await expect(table.getByText(MOCK_FAILED_ARCHIVE.archive_basename, { exact: true })).toBeVisible()
    await expect(table.locator('.status-chip.mounted')).toHaveText(/mounted/i)
    await expect(table.locator('.status-chip.mount_failed')).toHaveText(/mount_failed/i)
    await expect(table.getByRole('button', { name: 'Retry' })).toHaveCount(2)
    await expect(table.getByRole('button', { name: 'Unmount' })).toHaveCount(2)
    await expect(table.getByRole('button', { name: 'Purge' })).toHaveCount(2)
  })

  test('Retry and Unmount POST expected bodies', async ({ page }) => {
    const posts: ActionPostCall[] = []
    await mockShellApi(page, {
      archives: MOCK_ARCHIVE_ROWS,
      onAction: (call) => posts.push(call),
    })
    await page.goto('/')

    const failedRow = page.locator('table.archives tbody tr').filter({
      hasText: MOCK_FAILED_ARCHIVE.archive_basename,
    })
    await expect(failedRow).toBeVisible()

    await failedRow.getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => posts.some((p) => p.path === '/api/retry')).toBe(true)
    const retry = posts.find((p) => p.path === '/api/retry')
    expect(retry?.body).toEqual({ archive_id: MOCK_FAILED_ARCHIVE.archive_id })
    await expect(page.locator('.toast-stack')).toContainText(/Retry queued/i)

    const mountedRow = page.locator('table.archives tbody tr').filter({
      hasText: MOCK_MOUNTED_ARCHIVE.archive_basename,
    })
    acceptNextDialog(page)
    await mountedRow.getByRole('button', { name: 'Unmount' }).click()
    await expect.poll(() => posts.some((p) => p.path === '/api/unmount' && !p.body.all)).toBe(true)
    const unmount = posts.find((p) => p.path === '/api/unmount' && p.body.archive_id)
    expect(unmount?.body).toEqual({ archive_id: MOCK_MOUNTED_ARCHIVE.archive_id })
    await expect(page.locator('.toast-stack')).toContainText(/Unmount requested/i)
  })

  test('Rescan posts assume_stable false/true and shows toast', async ({ page }) => {
    const posts: ActionPostCall[] = []
    await mockShellApi(page, {
      archives: MOCK_ARCHIVE_ROWS,
      onAction: (call) => posts.push(call),
    })
    await page.goto('/')

    await page.getByRole('button', { name: 'Rescan', exact: true }).click()
    await expect.poll(() => posts.filter((p) => p.path === '/api/rescan').length).toBeGreaterThanOrEqual(1)
    const plain = posts.find((p) => p.path === '/api/rescan')
    expect(plain?.body).toEqual({ assume_stable: false })
    await expect(page.locator('.toast-stack')).toContainText(/Rescan done/i)
    await expect(page.locator('.toast-stack')).toContainText(/seen=2/)

    acceptNextDialog(page)
    await page.getByRole('button', { name: 'Rescan (assume stable)', exact: true }).click()
    await expect
      .poll(() => posts.filter((p) => p.path === '/api/rescan' && p.body.assume_stable === true).length)
      .toBeGreaterThanOrEqual(1)
    const assume = posts.find((p) => p.path === '/api/rescan' && p.body.assume_stable === true)
    expect(assume?.body).toEqual({ assume_stable: true })
    await expect(page.locator('.toast-stack')).toContainText(/Rescan done/i)
  })

  test('Purge and Unmount all confirm and POST with required bodies', async ({ page }) => {
    const posts: ActionPostCall[] = []
    await mockShellApi(page, {
      archives: MOCK_ARCHIVE_ROWS,
      onAction: (call) => posts.push(call),
    })
    await page.goto('/')

    acceptNextDialog(page)
    await page.getByRole('button', { name: 'Unmount all', exact: true }).click()
    await expect.poll(() => posts.some((p) => p.path === '/api/unmount' && p.body.all === true)).toBe(true)
    const unmountAll = posts.find((p) => p.path === '/api/unmount' && p.body.all === true)
    expect(unmountAll?.body).toEqual({ all: true })
    await expect(page.locator('.toast-stack')).toContainText(/Unmount all requested/i)

    const failedRow = page.locator('table.archives tbody tr').filter({
      hasText: MOCK_FAILED_ARCHIVE.archive_basename,
    })
    acceptNextDialog(page)
    await failedRow.getByRole('button', { name: 'Purge' }).click()
    await expect.poll(() => posts.some((p) => p.path === '/api/purge')).toBe(true)
    const purge = posts.find((p) => p.path === '/api/purge')
    expect(purge?.body).toEqual({
      archive_id: MOCK_FAILED_ARCHIVE.archive_id,
      yes: true,
    })
    await expect(page.locator('.toast-stack')).toContainText(/Purged/i)
  })
})
