import { expect, test } from './fixtures';

test.describe('Compose', () => {
  test('adds a typed recipient and sends in demo mode', async ({ page }) => {
    await page.goto('/mail');

    await page.getByRole('button', { name: /^Compose/ }).click();

    // Demo-mode accounts (lib/settings-mock.ts) resolve async; wait for the
    // "From" picker before touching anything that depends on it.
    await expect(page.getByText('guilherme.vozniak.a@gmail.com')).toBeVisible();

    // Type a bare email + Enter — exercises the fixed parseAddress() (a prior
    // unanchored regex corrupted plain addresses like this one).
    const recipientInput = page.getByPlaceholder('name@example.com');
    await recipientInput.fill('e2e-test@example.com');
    await recipientInput.press('Enter');
    await expect(page.getByText('e2e-test@example.com')).toBeVisible();

    await page.getByPlaceholder('Subject').fill('Playwright e2e subject');
    await page
      .getByPlaceholder(/Write your message/)
      .fill('Hello from the Playwright e2e suite.');

    // Two buttons contain the substring "Send" ("Send" and "Send later");
    // disambiguate rather than relying on brittle CSS.
    const sendButton = page.getByRole('button', { name: /Send/ }).filter({ hasNotText: 'later' });
    await sendButton.click();

    // No real backend is running, so the send request fails and DEMO_MODE
    // (components/app/compose.tsx handleSend) reports a local demo outcome
    // instead of a real send — and never fakes a bare success.
    await expect(page.getByText('Sent (demo mode)')).toBeVisible();
    await expect(page.getByPlaceholder('Subject')).toBeHidden();
  });
});
