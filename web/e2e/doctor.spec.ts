import { expect, test } from '@playwright/test'
import { MOCK_ARCHIVE_ROWS, MOCK_DOCTOR_REPORT, mockShellApi } from './helpers'

test.describe('Doctor panel (mocked API)', () => {
  test('opens panel, GETs /api/doctor, shows check names', async ({ page }) => {
    await mockShellApi(page, {
      archives: MOCK_ARCHIVE_ROWS,
      doctor: MOCK_DOCTOR_REPORT,
    })

    const doctorReq = page.waitForRequest(
      (req) => req.method() === 'GET' && /\/api\/doctor(?:\?|$)/.test(new URL(req.url()).pathname),
    )

    await page.goto('/')
    await page.getByRole('button', { name: 'Doctor', exact: true }).click()

    await expect(page.getByRole('heading', { name: 'Doctor', level: 2 })).toBeVisible()
    const req = await doctorReq
    expect(new URL(req.url()).pathname).toBe('/api/doctor')

    const panel = page.locator('section[aria-labelledby="doctor-heading"]')
    const list = panel.locator('pre.doctor-list')
    await expect(list).toBeVisible()
    for (const check of MOCK_DOCTOR_REPORT.checks) {
      await expect(list).toContainText(check.name)
      await expect(list).toContainText(check.message)
    }
    // Mock report has ok:true with one warn-fail check → "OK · N checks".
    await expect(panel.locator('.summary .pill')).toContainText(/OK\s*·\s*3\s*checks/i)
  })
})
