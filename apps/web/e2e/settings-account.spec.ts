import { expect, test } from './fixtures';

const NOW = new Date().toISOString();

test.describe('Settings → Account', () => {
  test('changes the password through the stubbed Better Auth endpoints', async ({ page }) => {
    await page.route('**/api/auth/list-accounts*', (route) =>
      route.fulfill({ json: [{ id: 'acc-1', providerId: 'credential', accountId: 'e2e-user-1', scopes: [], createdAt: NOW, updatedAt: NOW }] })
    );
    await page.route('**/api/auth/change-password', (route) => route.fulfill({ json: { token: null, user: { id: 'e2e-user-1' } } }));

    await page.goto('/settings?tab=account');
    // exact: 'Account' is also a substring of the 'Accounts' tab.
    await expect(page.getByRole('tab', { name: 'Account', exact: true })).toHaveAttribute('data-state', 'active');
    // Scoped to the tab panel: the sidebar's user menu also shows the email.
    await expect(page.getByRole('tabpanel').getByText('e2e@calendium.app')).toBeVisible();

    await page.getByLabel('Current password').fill('old-password-1');
    // exact: 'New password' is also a substring of 'Confirm new password'.
    await page.getByLabel('New password', { exact: true }).fill('correct-horse-battery');
    await page.getByLabel('Confirm new password').fill('correct-horse-battery');
    await page.getByRole('button', { name: 'Update password' }).click();

    await expect(page.getByText('Password updated. Other devices were signed out.')).toBeVisible();
  });

  test('a social-only account sees its sign-in method instead of the form', async ({ page }) => {
    await page.route('**/api/auth/list-accounts*', (route) =>
      route.fulfill({ json: [{ id: 'acc-2', providerId: 'google', accountId: '123', scopes: [], createdAt: NOW, updatedAt: NOW }] })
    );
    await page.goto('/settings?tab=account');
    await expect(page.getByText(/You sign in with Google\./)).toBeVisible();
    await expect(page.getByLabel('Current password')).toHaveCount(0);
  });
});
