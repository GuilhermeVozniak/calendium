import { expect, openCommandPalette, test } from './fixtures';

/**
 * M2.5 compose extras & contact context (Task 21): exercises the five briefed
 * demo-mode flows added in M2.5 — per-account signatures, the Recent Opens
 * feed, attachment quick access + inline PDF preview, emoji reactions, and
 * the contact pane — against the same offline lib/*-mock.ts fixtures the
 * rest of this suite relies on (see playwright.config.ts: no real backend is
 * ever started).
 */
test.describe('M2.5 compose extras & contact context', () => {
  // Wider than the 1280px default (Desktop Chrome) viewport: the Recent Opens
  // panel only renders at the `xl` (1280px) breakpoint and needs headroom
  // alongside the mail list so nothing wraps and intercepts clicks — mirrors
  // the fix already applied in ai-suite.spec.ts for the Ask AI sidebar.
  test.use({ viewport: { width: 1600, height: 900 } });

  test('per-account signature is auto-applied in a new compose', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: /^Compose/ }).click();

    // Demo-mode "from" account (lib/settings-mock.ts) resolves async; wait
    // for the picker before asserting on the body it seeds a signature into.
    await expect(page.getByText('guilherme.vozniak.a@gmail.com')).toBeVisible();

    // components/app/compose.tsx appends the resolved account's
    // signatureHtml ("<p>Guilherme Vozniak<br/>Calendium</p>") as a plain-text
    // block under an RFC-3676 "-- " delimiter as soon as the from-account
    // resolves — no typing required.
    await expect(page.getByPlaceholder(/Write your message/)).toHaveValue(
      /-- \nGuilherme Vozniak\nCalendium/
    );
  });

  test('Recent Opens feed opens and lists a real open event', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: 'Toggle Recent Opens' }).click();

    await expect(page.getByRole('heading', { name: 'Recent opens' })).toBeVisible();
    // thr_09 ("Re: advice on pricing page") has a MOCK_ME-sent message with a
    // real openedAt in lib/mail-mock.ts (computeMockOpens), so this is a
    // genuine recorded open event rather than a fabricated row.
    await expect(page.getByText(/Re: advice on pricing page/)).toBeVisible();
  });

  test('attachment search finds the demo PDF and previews it', async ({ page }) => {
    await page.goto('/mail');
    // Sanity check the app shell is mounted before probing the ⌘K/Ctrl+K
    // shortcut (mirrors command-palette.spec.ts) — under load, pressing it
    // before hydration finishes can silently drop the keypress.
    await expect(page.getByRole('button', { name: /^Compose/ })).toBeVisible();
    await openCommandPalette(page);
    const paletteInput = page.getByPlaceholder('Type a command or search…');
    await expect(paletteInput).toBeVisible();
    await page.getByText('Search attachments', { exact: true }).click();

    const searchInput = page.getByPlaceholder('Search attachments…');
    await expect(searchInput).toBeVisible();
    await searchInput.fill('Renewal');

    const hit = page.getByRole('button', { name: /FY27-Renewal-Summary\.pdf/ });
    await expect(hit).toBeVisible();
    await hit.click();

    // A real, decodable PDF (lib/mail-mock.ts DEMO_PDF_BASE64) previews
    // inline via a native iframe rather than a fabricated placeholder.
    await expect(page.locator('iframe[title="FY27-Renewal-Summary.pdf"]')).toBeVisible();
  });

  test('reacting to a message shows a reaction chip', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: /Renewal terms for FY27/ }).click();
    await expect(
      page.getByRole('heading', { name: 'Renewal terms for FY27 — need your sign-off' })
    ).toBeVisible();

    // The thread's single message is expanded by default (it's the last
    // message); hovering reveals the quick-react bar
    // (components/mail/message-reactions.tsx group-hover affordance).
    const reactButton = page.getByRole('button', { name: 'React with 👍' });
    await reactButton.hover();
    await reactButton.click();

    // Chips render strictly from the mutation response (message.reactions),
    // never an optimistic guess — see message-reactions.tsx.
    await expect(page.getByRole('button', { name: 'Remove 👍 reaction' })).toBeVisible();
  });

  test('contact pane opens from a thread and shows the sender', async ({ page }) => {
    await page.goto('/mail');
    await page.getByRole('button', { name: /Renewal terms for FY27/ }).click();
    await expect(
      page.getByRole('heading', { name: 'Renewal terms for FY27 — need your sign-off' })
    ).toBeVisible();

    await page.getByRole('button', { name: 'Toggle contact pane' }).click();

    // GET /v1/mail/contacts/{email} (demo fallback: getMockContact) resolves
    // thr_02's only participant, Daniel Cho <daniel.cho@northwind.com>.
    const pane = page.getByRole('complementary');
    await expect(pane.getByText('Daniel Cho')).toBeVisible();
    await expect(pane.getByText('daniel.cho@northwind.com')).toBeVisible();
  });
});
