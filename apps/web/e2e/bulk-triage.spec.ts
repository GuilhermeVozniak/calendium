import { expect, test } from './fixtures';

test.describe('Bulk triage', () => {
  test('x selects, shift+j extends, e bulk-archives, z undoes', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await expect(first).toBeVisible();
    await first.hover();

    await page.keyboard.press('x');
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.keyboard.press('Shift+j');
    await expect(page.getByText('2 selected')).toBeVisible();

    await page.keyboard.press('e');
    await expect(page.getByText(/Archived 2 conversations/)).toBeVisible();
    await expect(page.getByText(/selected/)).toHaveCount(0);

    await page.keyboard.press('z');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
  });

  test('escape clears the selection without closing anything', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await first.hover();
    await page.keyboard.press('x');
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByText(/selected/)).toHaveCount(0);
  });
});
