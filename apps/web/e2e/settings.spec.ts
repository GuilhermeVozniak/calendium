import { expect, test } from './fixtures';

test.describe('Settings', () => {
  test('renders connected accounts and snippets from demo data', async ({ page }) => {
    await page.goto('/settings');

    await expect(page.getByText('Connected accounts', { exact: true })).toBeVisible();
    await expect(page.getByText('guilherme.vozniak.a@gmail.com')).toBeVisible();
    await expect(page.getByText('g.vozniak@fabrikam.com')).toBeVisible();
    await expect(page.getByText('Re-auth required')).toBeVisible();

    await page.getByRole('tab', { name: 'Snippets' }).click();
    await expect(page.getByText('Thanks — will review')).toBeVisible();
  });

  test('split order can be reordered and survives navigation to mail', async ({ page }) => {
    await page.goto('/settings');
    await page.getByRole('tab', { name: 'Mailbox' }).click();
    await expect(page.getByText('Split order')).toBeVisible();
    await page.getByRole('button', { name: 'Move VIP up' }).click();
    await page.goto('/mail');
    const tabs = page.getByRole('tab');
    await expect(tabs.first()).toHaveText('VIP');
  });

  test('account tab renders both actions and demo mode never calls the real routes', async ({ page }) => {
    const forbidden: string[] = [];
    page.on('request', (req) => {
      if (/\/api\/auth\/delete-user|\/v1\/me\/export/.test(req.url())) forbidden.push(req.url());
    });
    // Piece 2's password card lists sign-in methods; Better Auth has no DB here.
    await page.route('**/api/auth/list-accounts*', (route) => route.fulfill({ json: [] }));
    await page.goto('/settings?tab=account');
    // exact: 'Account' is also a substring of the 'Accounts' tab.
    await expect(page.getByRole('tab', { name: 'Account', exact: true })).toHaveAttribute('data-state', 'active');
    // Scoped to the tab panel: the sidebar's user menu also shows the email.
    await expect(page.getByRole('tabpanel').getByText('e2e@calendium.app')).toBeVisible();

    await page.getByRole('button', { name: 'Download my data' }).click();
    await expect(page.getByText('Not available in demo').first()).toBeVisible();

    await page.getByRole('button', { name: 'Delete account' }).click();
    await expect(page.getByText('Not available in demo').first()).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);

    await page.getByRole('tab', { name: 'AI', exact: true }).click();
    await expect(page.getByRole('switch', { name: 'Background AI processing' })).toBeVisible();

    expect(forbidden).toEqual([]);
  });

  test('a paywalled user still reaches Account & data (export + delete)', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'canceled');
    });
    await page.goto('/mail');
    await expect(page.getByRole('heading', { name: 'Your subscription has ended' })).toBeVisible();
    await page.getByRole('button', { name: 'Account & data' }).click();
    await expect(page.getByRole('button', { name: 'Download my data' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Delete account' })).toBeVisible();
    await page.getByRole('button', { name: /Back to subscription/ }).click();
    await expect(page.getByRole('button', { name: 'Subscribe · $50/year' })).toBeVisible();
  });
});
