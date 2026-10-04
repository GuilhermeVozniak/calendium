import { expect, test } from './fixtures';

/**
 * Password recovery and verification pages in demo mode. Better Auth's
 * server route is never hit: the endpoints these pages call are stubbed at
 * the network layer exactly like fixtures.ts stubs get-session and /token.
 */
test.describe('Auth recovery', () => {
  test('forgot password shows the generic confirmation', async ({ page }) => {
    await page.route('**/api/auth/request-password-reset*', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/forgot-password');
    await page.getByLabel('Email').fill('someone@example.com');
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await expect(page.getByText('If an account exists for that address, we sent a link.')).toBeVisible();
  });

  test('reset password with a token updates and returns to sign-in', async ({ page }) => {
    await page.route('**/api/auth/reset-password', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/reset-password?token=e2e-token');
    // exact: 'New password' is also a substring of 'Confirm new password'.
    await page.getByLabel('New password', { exact: true }).fill('correct-horse-battery');
    await page.getByLabel('Confirm new password').fill('correct-horse-battery');
    await page.getByRole('button', { name: 'Update password' }).click();
    await expect(page.getByText('Password updated. Sign in with your new password.')).toBeVisible();
    // The stubbed session makes /signin bounce straight to the inbox.
    await expect(page).toHaveURL(/\/(signin|mail)/);
  });

  test('reset password with an error shows the recovery path', async ({ page }) => {
    await page.goto('/reset-password?error=INVALID_TOKEN');
    await expect(page.getByText('This link is invalid or has expired')).toBeVisible();
    await page.getByRole('link', { name: 'Request a new link' }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
  });

  test('verify-email with the stubbed session shows Verified and continues to the inbox', async ({ page }) => {
    await page.goto('/verify-email');
    await expect(page.getByText('Email verified')).toBeVisible();
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page).toHaveURL(/\/mail/);
  });

  test('verify-email with an expired link offers a resend', async ({ page }) => {
    await page.route('**/api/auth/send-verification-email*', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/verify-email?error=TOKEN_EXPIRED');
    await expect(page.getByText('This link has expired or was already used')).toBeVisible();
    await page.getByLabel('Email').fill('someone@example.com');
    await page.getByRole('button', { name: 'Send a new link' }).click();
    await expect(page.getByText('If an account exists for that address, we sent a new link.')).toBeVisible();
  });

  test('sign-in page links to forgot password', async ({ page }) => {
    // The stubbed session redirects /signin; drop it for this one test.
    await page.route('**/api/auth/get-session*', (route) => route.fulfill({ json: null }));
    await page.goto('/signin');
    await expect(page.getByRole('link', { name: 'Forgot password?' })).toHaveAttribute('href', '/forgot-password');
  });
});
