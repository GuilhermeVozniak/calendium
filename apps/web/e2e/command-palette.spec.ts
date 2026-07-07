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
});
