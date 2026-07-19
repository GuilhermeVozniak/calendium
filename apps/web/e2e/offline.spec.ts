import { expect, test } from './fixtures';

/**
 * Offline outbox (M2.6 Task 4), driven in demo mode. While the browser is
 * offline, triage mutations skip the network entirely and queue in the
 * durable outbox (the known-offline pre-check in use-mail's runOptimistic),
 * so no stubbing is needed for the offline half. For the reconnect half, the
 * thread-action endpoint is stubbed to succeed: Playwright routes fulfill
 * before the network layer, which is exactly what lets replay "reach the
 * server" inside this backend-less suite.
 */
test.describe('Offline outbox', () => {
  test('archiving offline shows the queued pill; replay on reconnect clears it', async ({
    page,
    context,
  }) => {
    await page.route('**/v1/mail/threads/*/actions', (route) =>
      route.fulfill({ json: { id: 'stub-thread' } })
    );

    await page.goto('/mail');
    const target = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await expect(target).toBeVisible();

    await context.setOffline(true);

    await target.hover(); // hover selects the row (onMouseEnter)
    await page.keyboard.press('e');

    // Queued honestly: the pill reflects the REAL outbox entry count…
    await expect(page.getByTestId('outbox-queued-count')).toContainText('1 queued');
    // …and the optimistic removal stays in place (no revert, no fake error).
    await expect(
      page.getByRole('button', { name: /Postmortem: checkout latency spike/ })
    ).toHaveCount(0);

    await context.setOffline(false);

    // Reconnect triggers replay against the (stubbed) API; the pill only
    // disappears once the ReplayReport confirms the queue drained — the UI
    // never claims "synced" before that.
    await expect(page.getByTestId('outbox-queued-count')).toHaveCount(0);
    await expect(page.getByTestId('outbox-indicator')).toHaveCount(0);
  });
});
