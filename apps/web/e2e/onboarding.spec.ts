import { expect, openCommandPalette, test } from './fixtures';

/**
 * Concierge onboarding tour (M2.8 Task 19), in demo mode like the rest of the
 * suite. The shared fixture pre-seeds the tour as done on every navigation so
 * its popover never overlays unrelated specs; this file's init script (which
 * runs after the fixture's, in registration order) removes that seed exactly
 * once — on the first load — so the first-run auto-start path is exercised
 * for real, while later reloads keep the completed state a real user's
 * localStorage would hold.
 */
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    if (!window.localStorage.getItem('calendium.e2e.tour-seed-cleared')) {
      window.localStorage.setItem('calendium.e2e.tour-seed-cleared', '1');
      window.localStorage.removeItem('calendium.tour.v1');
    }
  });
});

test('first visit tours, skip persists across reload, palette restarts it', async ({ page }) => {
  await page.goto('/mail');

  // First demo visit: step 1 anchored over the split-inbox tabs.
  const popover = page.getByTestId('tour-popover');
  await expect(popover).toBeVisible();
  await expect(popover).toContainText('Your inbox, split');
  await expect(popover).toContainText('1 of 8');

  // Walk two steps.
  await popover.getByRole('button', { name: 'Next' }).click();
  await expect(popover).toContainText('Triage at speed');
  await popover.getByRole('button', { name: 'Next' }).click();
  await expect(popover).toContainText('Every action, one keystroke');

  // Skip: dismisses immediately…
  await popover.getByRole('button', { name: 'Skip tour' }).click();
  await expect(popover).toBeHidden();

  // …and a reload shows nothing (done persisted — the auto-start guard
  // respects it on a fresh page load).
  await page.reload();
  await expect(page.getByPlaceholder('Search')).toBeVisible();
  await expect(popover).toBeHidden();

  // Palette "Restart tour" brings it back from step 1.
  await openCommandPalette(page);
  await page.getByPlaceholder('Type a command or search…').fill('restart tour');
  await page.getByRole('option', { name: 'Restart tour' }).click();
  await expect(popover).toBeVisible();
  await expect(popover).toContainText('1 of 8');
});
