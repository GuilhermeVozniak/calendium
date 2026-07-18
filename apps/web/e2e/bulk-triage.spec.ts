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

  test('command palette archive honors bulk selection', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await expect(first).toBeVisible();
    await first.hover();

    // Select two rows
    await page.keyboard.press('x');
    await page.keyboard.press('Shift+j');
    await expect(page.getByText('2 selected')).toBeVisible();

    // Open command palette and run Archive (Meta+k on macOS, Ctrl+k on Linux)
    await page.keyboard.press('Meta+k');
    await expect(page.getByPlaceholder('Type a command or search…')).toBeVisible();
    const archiveButton = page.getByText('Archive conversation').first();
    await archiveButton.click();

    // Both rows should be archived
    await expect(page.getByText(/Archived 2 conversations/)).toBeVisible();
    await expect(first).not.toBeVisible();
  });

  test('L labels the hovered conversation from the picker', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await first.hover();
    await page.keyboard.press('l');
    await expect(page.getByPlaceholder('Label as…')).toBeVisible();
    await page.getByText('Updates', { exact: true }).click();
    await expect(page.getByText(/Labeled “Updates”/)).toBeVisible();
  });

  test('unsubscribe button appears on newsletter threads and confirms', async ({ page }) => {
    await page.goto('/mail?split=news');
    const newsletter = page.getByRole('button', { name: /The Batch/ }).first();
    await expect(newsletter).toBeVisible();
    await newsletter.click();
    // Wait for thread view to load
    await page.waitForURL(/\?.*t=/);
    // Wait for the Unsubscribe button to appear anywhere on the page
    const unsubBtn = page.locator('button[aria-label="Unsubscribe"]');
    await unsubBtn.waitFor({ timeout: 5000 });
    await unsubBtn.click();
    await expect(page.getByText(/Unsubscribed|unsubscribe page/)).toBeVisible();
  });

  test('bulk unsubscribe archives the selected newsletters', async ({ page }) => {
    await page.goto('/mail?split=news');
    const first = page.getByRole('button', { name: /The Batch/ });
    await first.hover();
    await page.keyboard.press('x');
    await page.keyboard.press('Shift+j');
    await page.getByRole('button', { name: 'Unsubscribe' }).click();
    await expect(page.getByText(/Unsubscribed from 2 senders/)).toBeVisible();
    await expect(page.getByRole('button', { name: /The Batch/ })).toHaveCount(0);
  });
});
