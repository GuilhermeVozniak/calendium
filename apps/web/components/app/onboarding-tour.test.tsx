import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const push = vi.fn();
vi.mock('next/navigation', () => ({
  usePathname: () => '/mail',
  useRouter: () => ({ push }),
}));

import { TOUR_STORAGE_KEY, resetTourStateForTests } from '@/lib/tour-state';
import type { TourStep } from '@/lib/tour-steps';

import { OnboardingTour } from './onboarding-tour';

const STEPS: TourStep[] = [
  { id: 'one', target: 'one', title: 'Step one', body: 'First stop.', page: 'mail' },
  { id: 'two', target: 'two', title: 'Step two', body: 'Second stop.', page: 'mail', shortcut: 'H' },
  { id: 'three', target: 'three', title: 'Step three', body: 'Third stop.', page: 'any' },
];

const realGetRect = Element.prototype.getBoundingClientRect;

describe('OnboardingTour', () => {
  beforeEach(() => {
    window.localStorage.clear();
    resetTourStateForTests();
    push.mockClear();
    // jsdom lays nothing out, so every rect is 0x0 — which the engine treats
    // as a hidden anchor. Give rendered elements a real-looking box.
    Element.prototype.getBoundingClientRect = () =>
      ({
        top: 100,
        left: 100,
        bottom: 120,
        right: 200,
        width: 100,
        height: 20,
        x: 100,
        y: 100,
        toJSON: () => ({}),
      }) as DOMRect;
  });

  afterEach(() => {
    Element.prototype.getBoundingClientRect = realGetRect;
  });

  function renderTour({ omit }: { omit?: string } = {}) {
    return render(
      <div>
        {STEPS.filter((s) => s.target !== omit).map((s) => (
          <div key={s.id} data-tour={s.target} />
        ))}
        <OnboardingTour steps={STEPS} anchorRetryMs={0} anchorRetryLimit={1} />
      </div>
    );
  }

  it('auto-starts for a fresh user and renders the first step anchored to its target', async () => {
    renderTour();
    expect(await screen.findByRole('dialog', { name: 'Step one' })).toBeInTheDocument();
    expect(screen.getByText('First stop.')).toBeInTheDocument();
    expect(screen.getByText('1 of 3')).toBeInTheDocument();
  });

  it('does not auto-start when the tour is already done', async () => {
    window.localStorage.setItem(TOUR_STORAGE_KEY, JSON.stringify({ done: true }));
    renderTour();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('Next advances; Done on the last step ends the tour and persists completion', async () => {
    renderTour();
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('dialog', { name: 'Step two' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('dialog', { name: 'Step three' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(window.localStorage.getItem(TOUR_STORAGE_KEY)).toContain('"done":true');
  });

  it('Skip tour dismisses immediately and persists completion', async () => {
    renderTour();
    fireEvent.click(await screen.findByRole('button', { name: 'Skip tour' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(window.localStorage.getItem(TOUR_STORAGE_KEY)).toContain('"done":true');
  });

  it('a missing anchor is skipped gracefully instead of blocking the tour', async () => {
    renderTour({ omit: 'two' });
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('dialog', { name: 'Step three' })).toBeInTheDocument();
  });
});
