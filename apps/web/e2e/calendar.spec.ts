import { expect, test } from './fixtures';

test.describe('Calendar', () => {
  test('creates an event and it appears on the calendar', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByRole('button', { name: 'New event' })).toBeVisible();

    const title = `Playwright QA sync ${Date.now()}`;

    await page.getByRole('button', { name: 'New event' }).click();
    const titleInput = page.getByLabel('Event title');
    await expect(titleInput).toBeVisible();
    await titleInput.fill(title);
    await page.getByRole('button', { name: 'Create event' }).click();

    await expect(page.getByText('Event created')).toBeVisible();

    // The agenda view lists events as plain text rows — the most reliable
    // place to confirm the new event landed in the (mock) event store.
    await page.getByRole('tab', { name: 'Agenda' }).click();
    await expect(page.getByRole('button', { name: new RegExp(title) })).toBeVisible();
  });
});
