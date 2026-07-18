import { expect, openCommandPalette, test } from './fixtures';

test.describe('Calendar', () => {
  test('creates an event and it appears on the calendar', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    const title = `Playwright QA sync ${Date.now()}`;

    await page.getByRole('button', { name: 'New event' }).click();
    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible();
    await titleInput.fill(title);
    await page.getByRole('button', { name: 'Create event' }).click();

    await expect(page.getByText('Event created')).toBeVisible();

    // The agenda view lists events as plain text rows — the most reliable
    // place to confirm the new event landed in the (mock) event store.
    await page.getByRole('tab', { name: 'Agenda' }).click();
    await expect(page.getByRole('button', { name: new RegExp(title) })).toBeVisible();
  });

  test('quick-add parses natural language input and creates an event', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // The quick-add input is labeled "Quick add event"
    const quickAddInput = page.getByLabel('Quick add event');
    await expect(quickAddInput).toBeVisible();
    await quickAddInput.focus();
    await quickAddInput.fill('lunch with ana@acme.com tomorrow 1pm at Cafe');

    // After filling, the parsing should produce preview chips (location, time, etc).
    await expect(page.getByText('Cafe')).toBeVisible({ timeout: 5000 });

    // Press Enter to submit the quick-add and open the event dialog.
    await quickAddInput.press('Enter');

    // Wait for the event dialog to open.
    await page.waitForTimeout(500);

    // Verify the dialog opened and create the event.
    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible({ timeout: 3000 });
    await page.getByRole('button', { name: 'Create event' }).click();
    await expect(page.getByText('Event created')).toBeVisible({ timeout: 3000 });
  });

  test('view switching with d/w/m/q/y/a keys', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // Current view should default to week.
    await expect(page.getByRole('tab', { name: 'Week' })).toBeVisible();

    // Press 'm' to switch to month view.
    await page.keyboard.press('m');
    await expect(page.getByRole('tab', { name: 'Month' })).toBeVisible({ timeout: 3000 });

    // Press 'q' to switch to quarter view.
    await page.keyboard.press('q');
    await expect(page.getByRole('tab', { name: 'Quarter' })).toBeVisible({ timeout: 3000 });

    // Press 'y' to switch to year view.
    await page.keyboard.press('y');
    await expect(page.getByRole('tab', { name: 'Year' })).toBeVisible({ timeout: 3000 });

    // Press 'w' to switch back to week view.
    await page.keyboard.press('w');
    await expect(page.getByRole('tab', { name: 'Week' })).toBeVisible({ timeout: 3000 });

    // Press 'a' to switch to agenda (ticker) view.
    await page.keyboard.press('a');
    await expect(page.getByRole('tab', { name: 'Agenda' })).toBeVisible({ timeout: 3000 });
  });

  test('keyboard navigation with t/j/k keys', async ({ page }) => {
    await page.goto('/calendar');

    // 't' should jump to today.
    await page.keyboard.press('t');
    // Allow navigation to settle
    await page.waitForTimeout(300);

    // 'j' should move forward (next week/period).
    await page.keyboard.press('j');
    await page.waitForTimeout(300);

    // 'k' should move backward (previous week/period).
    await page.keyboard.press('k');
    await page.waitForTimeout(300);

    // The calendar should still be visible and functional.
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();
  });

  test('double-booking warning when creating over a busy slot', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // New events default to "next half hour from now" (lib/quick-add.ts's
    // nextHalfHour), which always falls inside the deterministic "Live team
    // sync" seed's now -> now+2h window (lib/calendar-mock.ts) - so opening
    // the New event dialog is guaranteed to conflict, regardless of what
    // day/time the suite runs.
    await page.getByRole('button', { name: 'New event' }).click();

    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible();

    const conflictAlert = page.getByRole('alert');
    await expect(conflictAlert).toBeVisible({ timeout: 5000 });
    await expect(conflictAlert).toContainText('Live team sync');
  });

  test('join button appears on conference event', async ({ page }) => {
    await page.goto('/calendar');

    // "Live team sync" is a deterministic, always-in-progress seed
    // (lib/calendar-mock.ts's now -> now+2h event) so this assertion doesn't
    // depend on what day/time the suite runs.
    const liveSyncEvent = page.getByText('Live team sync').first();
    await expect(liveSyncEvent).toBeVisible({ timeout: 3000 });
    await liveSyncEvent.click();

    const joinButton = page.getByRole('button', { name: /join/i });
    await expect(joinButton).toBeVisible({ timeout: 3000 });
    await expect(joinButton).toHaveAttribute('data-joinable', 'true');
  });

  test('apply event template via keyboard shortcut', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // Open command palette to search for templates.
    await openCommandPalette(page);

    const input = page.getByPlaceholder('Type a command or search…');
    await expect(input).toBeVisible();

    // Search for "1:1" template which is seeded in the mock data.
    await input.fill('1:1');
    await expect(page.getByText('New event from template: 1:1')).toBeVisible();

    // Use keyboard to select and activate the template (press Enter after search appears).
    await input.press('Enter');

    // The event dialog must open with the template's title prefilled.
    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible({ timeout: 5000 });
    await expect(titleInput).toHaveValue('1:1 with teammate');
  });

  test('toggle calendar peek from mail page with mod+shift+k', async ({ page }) => {
    // The peek panel is only shown at the xl breakpoint (Tailwind's
    // "hidden ... xl:flex" on CalendarPeek) - a viewport below that renders
    // it present-but-hidden, which would make this assertion pass vacuously.
    await page.setViewportSize({ width: 1440, height: 900 });

    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Compose/i })).toBeVisible();

    const isMac = await page.evaluate(() => /Mac|iPhone|iPad|iPod/.test(navigator.platform));
    const shortcut = isMac ? 'Meta+Shift+k' : 'Control+Shift+k';

    await page.keyboard.press(shortcut);

    const peek = page.getByTestId('calendar-peek');
    await expect(peek).toBeVisible();

    await page.keyboard.press(shortcut);
    await expect(peek).not.toBeVisible();
  });
});
