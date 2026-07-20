import { expect, openCommandPalette, test } from './fixtures';

/**
 * Concierge onboarding tour (M2.8 Task 19), in demo mode like the rest of the
 * suite. The shared fixture pre-seeds the tour as done so its popover never
 * overlays unrelated specs — but that seed would make this spec's reload
 * assertion tautological (the fixture would re-mark the tour done on every
 * load, hiding the popover whether or not the app persisted anything). So on
 * the FIRST load this init script (which runs after the fixture's, in
 * registration order) clears the seed once and sets the
 * calendium.e2e.tour-seed-off flag; the fixture skips seeding whenever that
 * flag is present. For the rest of the spec — including reloads — the tour
 * state is exactly what the app itself wrote to localStorage, so "reload
 * shows nothing" genuinely tests persistence. Other specs never set the flag
 * and keep their pre-seed.
 */
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    if (!window.localStorage.getItem('calendium.e2e.tour-seed-off')) {
      window.localStorage.setItem('calendium.e2e.tour-seed-off', '1');
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
