import { expect, openCommandPalette, test } from './fixtures';

test.describe('Command palette', () => {
  test('opens with ⌘K / Ctrl+K and runs a navigation command', async ({ page }) => {
    await page.goto('/mail');
    // Sanity check the app shell is mounted before probing the shortcut.
    await expect(page.getByRole('button', { name: /Compose/ })).toBeVisible();

    await openCommandPalette(page);

    const input = page.getByPlaceholder('Type a command or search…');
    await expect(input).toBeVisible();
    const goToCalendar = page.getByText('Go to Calendar');
    await expect(goToCalendar).toBeVisible();

    await goToCalendar.click();

    // A client-side route transition still needs Next dev to compile
    // /calendar on first request — give it more room than the default
    // assertion timeout before checking the URL settled.
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible({ timeout: 15_000 });
    await expect(page).toHaveURL(/\/calendar$/);
  });

  test('create calendar event from command palette', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // Close any open dialogs first
    await page.keyboard.press('Escape');
    await page.waitForTimeout(200);

    // Open command palette
    await openCommandPalette(page);
    const input = page.getByPlaceholder('Type a command or search…');
    await expect(input).toBeVisible();

    // Type "new event"
    await input.type('new event', { delay: 50 });
    await page.waitForTimeout(300);

    // Press Enter or click on the result
    await input.press('Enter');

    // The event creation dialog should open.
    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible({ timeout: 5000 });
  });

  test('search calendar events from the palette', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    // Close any open dialogs
    await page.keyboard.press('Escape');
    await page.waitForTimeout(200);

    // Open command palette
    await openCommandPalette(page);
    const input = page.getByPlaceholder('Type a command or search…');
    await expect(input).toBeVisible();

    // Search for an existing event like "standup"
    await input.type('standup', { delay: 50 });
    await page.waitForTimeout(300);

    // The palette should show search results including "Daily standup" from mock data
    const result = page.getByText(/Daily standup/i).first();
    await expect(result).toBeVisible({ timeout: 3000 });
  });
});
