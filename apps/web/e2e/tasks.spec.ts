import { expect, openCommandPalette, test } from './fixtures';

/**
 * Task rail e2e (M2.8 Task 3/3b), demo mode: the rail's data comes from
 * lib/tasks-mock.ts through lib/use-tasks.ts's DEMO_MODE fallback, so the
 * seeded ids (demo-task-1…6) are deterministic. Note the demo task store is
 * module-scoped in the client bundle — a full page reload re-seeds it — so
 * persistence across actions is asserted within a page session, not across
 * reloads.
 */
test.describe('Tasks', () => {
  test('task rail is visible on the calendar with the demo groups', async ({ page }) => {
    await page.goto('/calendar');
    const rail = page.getByTestId('task-rail');
    await expect(rail).toBeVisible();
    // "Due today" seed and an unscheduled seed (lib/tasks-mock.ts).
    await expect(rail.getByText('Prep board deck')).toBeVisible();
    await expect(rail.getByText('Book flights to Lisbon')).toBeVisible();
  });

  test('⇧T toggles the rail closed and open again', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByTestId('task-rail')).toBeVisible();

    await page.keyboard.press('Shift+T');
    await expect(page.getByTestId('task-rail')).not.toBeVisible();

    await page.keyboard.press('Shift+T');
    await expect(page.getByTestId('task-rail')).toBeVisible();
  });

  test('checks a task off in place (stays in its group, strikethrough)', async ({ page }) => {
    await page.goto('/calendar');
    const item = page.getByTestId('task-item-demo-task-3'); // Book flights to Lisbon
    await expect(item).toBeVisible();

    await page.getByRole('checkbox', { name: 'Complete "Book flights to Lisbon"' }).click();

    // Optimistic in-place completion: same row, now checked (the checkbox
    // flips to the reopen affordance) — it does not jump into Completed.
    const reopen = page.getByRole('checkbox', { name: 'Reopen "Book flights to Lisbon"' });
    await expect(reopen).toBeVisible();
    await expect(reopen).toHaveAttribute('aria-checked', 'true');
    await expect(item).toBeVisible();
  });

  test('quick-add creates a task on Enter and clears the input', async ({ page }) => {
    await page.goto('/calendar');
    const input = page.getByLabel('Add a task');
    await input.fill('Write launch notes');
    await input.press('Enter');

    await expect(page.getByTestId('task-rail').getByText('Write launch notes')).toBeVisible();
    await expect(input).toHaveValue('');
  });

  test('dragging a rail task onto a day column creates a grid timeblock', async ({ page }) => {
    await page.goto('/calendar');

    // "Deep work: roadmap doc" is deterministically timeblocked today at
    // 10:00 (lib/tasks-mock.ts), so its grid block's parent IS today's
    // droppable day column — no brittle nth-column math.
    const scheduledBlock = page.getByTestId('grid-task-demo-task-5');
    await expect(scheduledBlock).toBeVisible();
    const dayColumn = scheduledBlock.locator('..');

    const dataTransfer = await page.evaluateHandle(() => new DataTransfer());
    await page.getByTestId('task-item-demo-task-3').dispatchEvent('dragstart', { dataTransfer });
    await dayColumn.dispatchEvent('drop', { dataTransfer });

    // The dropped task renders as a grid block and leaves the rail's
    // unscheduled group (it now has a timeblock).
    await expect(page.getByTestId('grid-task-demo-task-3')).toBeVisible();
    await expect(page.getByTestId('task-item-demo-task-3')).not.toBeVisible();
  });

  test('palette "New task" reopens the rail and focuses its quick-add', async ({ page }) => {
    await page.goto('/calendar');
    await expect(page.getByTestId('task-rail')).toBeVisible();

    // Hide the rail first to prove the command reopens it.
    await page.keyboard.press('Shift+T');
    await expect(page.getByTestId('task-rail')).not.toBeVisible();

    await openCommandPalette(page);
    const input = page.getByPlaceholder('Type a command or search…');
    await input.fill('New task');
    await input.press('Enter');

    await expect(page.getByTestId('task-rail')).toBeVisible();
    await expect(page.getByLabel('Add a task')).toBeFocused();
  });

  test('⇧N from the mail page jumps to the calendar and focuses quick-add', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Compose/i })).toBeVisible();

    await page.keyboard.press('Shift+N');

    // First navigation to /calendar may need Next dev to compile the route.
    await expect(page).toHaveURL(/\/calendar$/, { timeout: 15_000 });
    await expect(page.getByLabel('Add a task')).toBeFocused({ timeout: 15_000 });
  });
});
