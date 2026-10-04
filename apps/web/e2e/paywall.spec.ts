import { expect, test } from './fixtures';

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
