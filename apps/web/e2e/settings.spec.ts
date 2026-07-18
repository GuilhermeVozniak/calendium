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
});
