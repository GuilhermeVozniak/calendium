import { expect, openCommandPalette, test } from './fixtures';

/**
 * Demo-mode paywall: DEMO_INSTANCE advertises billing and lib/settings-mock
 * seeds the subscription from localStorage.calendium.demo.subscriptionStatus.
 */
test.describe('Paywall (demo mode)', () => {
  test('a canceled subscription replaces the inbox with the paywall', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'canceled');
    });
    await page.goto('/mail');
    await expect(page.getByRole('heading', { name: 'Your subscription has ended' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Subscribe · $50/year' })).toBeVisible();
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toHaveCount(0);
  });

  test('the paywall blocks the whole shell: no rail, compose, palette or shortcuts', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'canceled');
    });
    await page.goto('/mail');
    const heading = page.getByRole('heading', { name: 'Your subscription has ended' });
    await expect(heading).toBeVisible();
    // Focus lands on the paywall heading so screen readers announce it.
    await expect(heading).toBeFocused();
    await expect(page.getByRole('button', { name: /Compose/ })).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Settings' })).toHaveCount(0);
    await openCommandPalette(page);
    await page.keyboard.press('c');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Sign out/ })).toBeVisible();
  });

  test('a past-due subscription asks for a payment method', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'past_due');
    });
    await page.goto('/mail');
    await expect(page.getByRole('heading', { name: 'Payment failed' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Update payment method' })).toBeVisible();
  });

  test('the default trial keeps the inbox and shows no banner (9 days left)', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
    await expect(page.getByText(/Your free trial ends/)).toHaveCount(0);
  });

  test('/checkout without a transaction says there is nothing to pay', async ({ page }) => {
    await page.goto('/checkout');
    await expect(page.getByRole('heading', { name: 'Nothing to pay' })).toBeVisible();
  });
});
