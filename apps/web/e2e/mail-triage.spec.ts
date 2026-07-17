import { expect, test } from './fixtures';

/**
 * The inbox is the app's default landing surface. This exercises the whole
 * demo data path end to end: real-API attempt -> fast failure (no backend) ->
 * lib/mail-mock.ts fallback -> render -> a real triage mutation applied to
 * the in-memory mock store -> the list re-rendering without that thread.
 */
test.describe('Mail — inbox triage', () => {
  test('loads the inbox and archives a thread from the thread view', async ({ page }) => {
    await page.goto('/mail');

    // The default "Important" split renders the seeded mock threads.
    await expect(
      page.getByRole('button', { name: /Postmortem: checkout latency spike/ })
    ).toBeVisible();

    const target = page.getByRole('button', { name: /Renewal terms for FY27/ });
    await expect(target).toBeVisible();
    await target.click();

    // Thread view opened for that conversation.
    await expect(
      page.getByRole('heading', { name: 'Renewal terms for FY27 — need your sign-off' })
    ).toBeVisible();

    await page.getByRole('button', { name: 'Archive' }).click();

    await expect(page.getByText('Archived', { exact: true })).toBeVisible();
    // Archiving closes the thread pane and returns to the list; the archived
    // thread no longer matches the (still active) Important split filter.
    await expect(page.getByRole('button', { name: /Renewal terms for FY27/ })).toHaveCount(0);
  });

  test('Z undoes an archive, restoring the thread to the list', async ({ page }) => {
    await page.goto('/mail');
    const target = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await expect(target).toBeVisible();
    await target.hover(); // hover selects the row (onMouseEnter)
    await page.keyboard.press('e');
    await expect(page.getByText('Archived', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toHaveCount(0);

    await page.keyboard.press('z');
    await expect(page.getByText('Undone', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
  });

  test('H opens the snooze picker (rebound from Z)', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Renewal terms for FY27/ }).first()).toBeVisible();
    await page.keyboard.press('h');
    await expect(page.getByText('Snooze until…')).toBeVisible();
    await page.keyboard.press('Escape');
  });
});
