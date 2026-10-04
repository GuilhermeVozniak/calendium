import { expect, openCommandPalette, test } from './fixtures';

/**
 * AI suite, driven entirely in demo mode (see playwright.config.ts): the real
 * API is unreachable, so every AI surface falls back to the scripted demo
 * fixtures in lib/mail-mock.ts / lib/classifiers-mock.ts (lib/use-instance.ts
 * also falls back to a demo InstanceInfo with `features.ai: true`, so these
 * surfaces render at all). Nothing here depends on a live model call.
 */

test.describe('AI suite', () => {
  // Wider than the default viewport: the mail list header can wrap onto a
  // second line at 1280px once the Ask AI sidebar (320px) is open, which
  // would otherwise intercept clicks meant for elements below it.
  test.use({ viewport: { width: 1600, height: 900 } });

  test('renders the AI thread summary above the message list', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: /Postmortem: checkout latency spike/ }).click();
    await expect(
      page.getByText(/Checkout p99 latency spiked to 2\.4s/)
    ).toBeVisible();
  });

  test('an instant-reply chip opens the composer prefilled with that reply', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: /Postmortem: checkout latency spike/ }).click();

    const chip = page.getByRole('button', { name: /Thanks for the update — looks resolved\./ });
    await expect(chip).toBeVisible();
    await chip.click();

    await expect(page.getByText('Reply', { exact: true })).toBeVisible();
    // M2.5 per-account signatures (components/app/compose.tsx) now
    // auto-append the from-account's signature to every compose body,
    // replies included — so the instant-reply text is the prefix, not the
    // whole value.
    await expect(page.getByPlaceholder(/Write your message/)).toHaveValue(
      /^Thanks for the update — looks resolved\.\n\n-- \nGuilherme Vozniak\nCalendium$/
    );
  });

  test('Ask AI answers a question with a clickable source that opens the thread', async ({ page }) => {
    await page.goto('/mail');
    // The billing gate mounts the whole shell (palette + shortcuts included)
    // only once the subscription resolves — wait for the inbox first.
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
    // Open via the command palette (rather than the raw ⌘J shortcut) since a
    // real browser may reserve Ctrl/Cmd+J for its own downloads panel.
    await openCommandPalette(page);
    // Scoped to the palette dialog: the (always-mounted-but-collapsed) Ask AI
    // sidebar header also has the exact text "Ask AI" elsewhere on the page.
    await page.getByRole('dialog').getByText('Ask AI').click();

    await expect(page.getByPlaceholder('Ask about your mailbox…')).toBeVisible();
    await page.getByPlaceholder('Ask about your mailbox…').fill('What is the status of the incident?');
    await page.getByLabel('Ask', { exact: true }).click();

    await expect(page.getByText(/Based on your mailbox/)).toBeVisible();
    const source = page.getByText('Postmortem: checkout latency spike').last();
    await expect(source).toBeVisible();
    await source.click();

    await expect(page).toHaveURL(/\/mail\?t=/);
  });

  test('classifier settings: create, toggle, and delete a classifier', async ({ page }) => {
    await page.goto('/settings/classifiers');

    // Seeded demo classifier is listed.
    await expect(page.getByText('Recruiter outreach')).toBeVisible();

    // Create — validation blocks a classifier with neither split nor label.
    await page.getByRole('button', { name: 'New classifier' }).click();
    await page.getByLabel('Name', { exact: true }).fill('Newsletters');
    await page.getByLabel('Prompt').fill('Marketing newsletters and promotions.');
    await page.getByRole('button', { name: 'Create' }).click();
    await expect(
      page.getByText('Choose a target split or a label — at least one is required.')
    ).toBeVisible();

    await page.getByLabel('Label name').fill('Marketing');
    await page.getByRole('button', { name: 'Create' }).click();
    await expect(page.getByText('Classifier created')).toBeVisible();
    await expect(page.getByText('Newsletters', { exact: true })).toBeVisible();

    // Toggle enabled off.
    await page.getByLabel('Disable Newsletters').click();

    // Delete.
    await page.getByLabel('Delete Newsletters').click();
    await expect(page.getByText('Classifier deleted')).toBeVisible();
    await expect(page.getByText('Newsletters', { exact: true })).toHaveCount(0);
  });
});
