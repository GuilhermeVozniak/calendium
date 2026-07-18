import { expect, test } from './fixtures';

/**
 * Sub-100ms interaction budget (feature map: Speed). Measures real keydown →
 * next-paint latency inside the page: dispatch a KeyboardEvent, then await a
 * double requestAnimationFrame so React has committed and the frame painted.
 * Asserts on p95 so one GC pause can't flake the suite, with warmup discarded.
 */

function p95(samples: number[]): number {
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))]!;
}

async function measureKeys(
  page: import('@playwright/test').Page,
  keys: string[],
  rounds: number
): Promise<number[]> {
  return page.evaluate(
    async ({ keys, rounds }) => {
      const samples: number[] = [];
      const frame = () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
        );
      for (let i = 0; i < rounds; i++) {
        const key = keys[i % keys.length]!;
        const start = performance.now();
        window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
        await frame();
        samples.push(performance.now() - start);
      }
      return samples;
    },
    { keys, rounds }
  );
}

test.describe('interaction budget', () => {
  test('j/k list navigation p95 stays under 100ms', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();

    await measureKeys(page, ['j', 'k'], 10); // warmup, discarded
    const samples = await measureKeys(page, ['j', 'k'], 40);
    const budget = p95(samples);
    test.info().annotations.push({ type: 'perf', description: `j/k p95=${budget.toFixed(1)}ms` });
    expect(budget).toBeLessThan(100);
  });

  test('archive (e) p95 stays under 100ms', async ({ page }) => {
    await page.goto('/mail?split=news');
    await expect(page.getByRole('button', { name: /The Batch/ })).toBeVisible();

    // Archive then undo, repeatedly, so the list never runs dry mid-measure.
    const samples: number[] = [];
    for (let i = 0; i < 12; i++) {
      const [archive] = await measureKeys(page, ['e'], 1);
      samples.push(archive!);
      await measureKeys(page, ['z'], 1); // undo restores the row (not measured)
    }
    const budget = p95(samples.slice(2)); // first two are warmup
    test.info().annotations.push({ type: 'perf', description: `archive p95=${budget.toFixed(1)}ms` });
    expect(budget).toBeLessThan(100);
  });
});
