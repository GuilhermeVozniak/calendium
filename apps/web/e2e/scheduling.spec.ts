import { expect, test } from './fixtures';

/**
 * M2.4 Task 16: recipient-TZ availability text + the Time Travel overlay.
 * Runs in the same demo-mode, production-build config as every other e2e
 * spec (see playwright.config.ts) — no real Go backend, calendar data comes
 * from lib/calendar-mock.ts's deterministic in-memory store.
 */
test.describe('Availability — recipient timezone', () => {
  test('converts the copy-preview text into the selected recipient timezone', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'Share availability' })).toBeVisible();
    await page.getByRole('button', { name: 'Share availability' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText('Share availability')).toBeVisible();

    // Wait for the mock slot engine to populate at least one day group before
    // touching the recipient-timezone picker.
    await expect(dialog.locator('pre')).toBeVisible({ timeout: 5000 });

    // Own timezone: the preview's header names the raw IANA id verbatim (the
    // pre-Task-16 format), never a friendly name — confirms the default
    // (same-zone) path is untouched before we switch it.
    const before = await dialog.locator('pre').innerText();
    expect(before).toMatch(/^Here are a few times that work for me \(all times [^)]+\):/);
    expect(before).not.toContain('—');

    // Switch the recipient to a fixed, distinct zone (Kolkata/Calcutta —
    // never DST, so this assertion never depends on what day the suite
    // runs) and confirm the preview text re-converts every slot into it.
    await dialog.getByRole('button', { name: 'Recipient timezone' }).click();
    await page.getByPlaceholder('Search timezone…').fill('Calcutta');
    await page.getByText('Asia/Calcutta').click();

    await expect(dialog.locator('pre')).toContainText('India Standard Time — GMT+5:30');
  });
});

test.describe('Calendar — Time Travel overlay', () => {
  test('shift+z opens the city search, and picking a city renders a second hour gutter', async ({
    page,
  }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    await expect(page.getByPlaceholder('Jump to a city…')).toBeHidden();
    await page.keyboard.press('Shift+Z');
    await expect(page.getByPlaceholder('Jump to a city…')).toBeVisible();

    await page.getByPlaceholder('Jump to a city…').fill('Tokyo');
    await page.getByText('Asia/Tokyo').click();

    await expect(page.getByTestId('tz-gutter-timetravel')).toBeVisible();
    await expect(page.getByTestId('tz-header-timetravel')).toContainText('Tokyo');

    // Esc clears the overlay from anywhere on the page.
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('tz-gutter-timetravel')).toBeHidden();
  });

  test('persists the active overlay across a reload via localStorage', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    await page.keyboard.press('Shift+Z');
    await page.getByPlaceholder('Jump to a city…').fill('London');
    await page.getByText('Europe/London').click();
    await expect(page.getByTestId('tz-gutter-timetravel')).toBeVisible();

    await page.reload();
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();
    await expect(page.getByTestId('tz-gutter-timetravel')).toBeVisible();

    // The grid gutter's own exit control clears and persists the clear.
    await page.getByRole('button', { name: 'Exit Time Travel' }).first().click();
    await expect(page.getByTestId('tz-gutter-timetravel')).toBeHidden();
  });
});

test.describe('Public booking page', () => {
  // The public /book/[slug] page ships from a sibling task (14) in this
  // milestone, developed in a separate isolated worktree and merged in
  // afterward — it does not exist yet in this branch. This spec is wired up
  // so it can be un-skipped the moment that route lands, without needing to
  // guess at its shape ahead of time.

  test('renders the public booking page shell in demo mode', async ({ page }) => {
    await page.goto('/book/demo');
    await expect(page.getByRole('heading')).toBeVisible();
  });
});
