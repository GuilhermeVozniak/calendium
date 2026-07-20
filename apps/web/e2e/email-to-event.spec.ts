import { expect, test } from './fixtures';

/**
 * Email-to-event drag (M2.8 Task 18), demo mode: drag a thread row from the
 * inbox onto the calendar peek's grid → the event dialog opens prefilled
 * (title from subject, thread-reference description, drop-position time) →
 * save → the event appears on the grid. Playwright's mouse-based dragTo
 * can't feed the custom application/x-calendium-thread DataTransfer payload
 * through Chromium's native DnD reliably, so the drag is dispatched as
 * synthetic DragEvents sharing one DataTransfer — the exact objects the app
 * code reads.
 */
test.describe('Email-to-event drag', () => {
  test.beforeEach(async ({ page }) => {
    // The calendar peek only renders at the xl breakpoint.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/mail');
    await expect(
      page.getByRole('button', { name: /Renewal terms for FY27/ })
    ).toBeVisible();

    const isMac = await page.evaluate(() => /Mac|iPhone|iPad|iPod/.test(navigator.platform));
    await page.keyboard.press(isMac ? 'Meta+Shift+k' : 'Control+Shift+k');
    await expect(page.getByTestId('calendar-peek')).toBeVisible();
  });

  async function dragThreadTo(
    page: import('@playwright/test').Page,
    targetSelector: string,
    offsetY: number
  ) {
    const row = await page
      .getByRole('button', { name: /Renewal terms for FY27/ })
      .elementHandle();
    const target = await page
      .getByTestId('calendar-peek')
      .locator(targetSelector)
      .elementHandle();
    await page.evaluate(
      ([rowEl, targetEl, dy]) => {
        const dt = new DataTransfer();
        const opts = { bubbles: true, cancelable: true, dataTransfer: dt };
        rowEl!.dispatchEvent(new DragEvent('dragstart', opts));
        const rect = (targetEl as Element).getBoundingClientRect();
        const at = {
          ...opts,
          clientX: rect.left + rect.width / 2,
          clientY: rect.top + (dy as number),
        };
        targetEl!.dispatchEvent(new DragEvent('dragover', at));
        targetEl!.dispatchEvent(new DragEvent('drop', at));
      },
      [row, target, offsetY] as const
    );
  }

  test('drop on the peek grid opens a prefilled dialog and saves the event', async ({ page }) => {
    // 14:00 at 48px/hour, +4px inside the slot — snaps down to 14:00.
    await dragThreadTo(page, '[data-testid^="day-column-"]', 14 * 48 + 4);

    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible();
    await expect(titleInput).toHaveValue(/Renewal terms for FY27/);
    await expect(page.getByText('Created from email')).toBeVisible();
    await expect(page.getByLabel('Description')).toHaveValue(/mailto:/);
    // exact: true — "Start" alone would also match the "Start from template" combobox.
    await expect(page.getByLabel('Start', { exact: true })).toHaveValue(/T14:00/);

    await page.getByRole('button', { name: 'Create event' }).click();
    await expect(page.getByText('Event created')).toBeVisible();

    // The new event lands on the peek's (today) grid.
    await expect(
      page.getByTestId('calendar-peek').getByText(/Renewal terms for FY27/).first()
    ).toBeVisible();
  });

  test('drop on the day header prefills an all-day event', async ({ page }) => {
    await dragThreadTo(page, '[data-testid^="day-header-"]', 10);

    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible();
    await expect(titleInput).toHaveValue(/Renewal terms for FY27/);
    await expect(page.getByRole('switch', { name: /all day/i })).toBeChecked();
    // All-day mode renders date (not datetime-local) inputs.
    await expect(page.getByLabel('Start', { exact: true })).toHaveAttribute('type', 'date');
  });
});
