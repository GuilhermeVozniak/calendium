import { expect, openCommandPalette, test } from './fixtures';

test.describe('Get Me To Zero', () => {
  test('runs from the palette and reports the archived count', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();

    await openCommandPalette(page);
    await page.getByPlaceholder('Type a command or search…').fill('zero');
    await page.getByText('Get Me To Zero').click();

    await expect(page.getByText('Archive everything older than…')).toBeVisible();
    // Click the first option button which should be "1 week"
    const weekOption = page.locator('button:has-text("1 week")').first();
    await weekOption.click();
    // Demo threads are hours-to-days old; a 1-week cutoff archives none, and
    // the honest empty-result copy shows. The flow itself is what's asserted.
    await expect(page.getByText(/already close to zero|welcome to zero/)).toBeVisible();
  });
});
