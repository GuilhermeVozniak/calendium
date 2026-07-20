import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TimeInsights } from '@calendium/shared';

import {
  InsightsPanel,
  demoTimeInsights,
  formatMinutes,
} from '@/components/app/insights-panel';

const getTimeInsights = vi.fn();

vi.mock('@/lib/api', () => ({
  getApiClient: () => ({ getTimeInsights }),
}));

const FROM = new Date('2026-07-06T00:00:00Z');
const TO = new Date('2026-07-13T00:00:00Z');

const insights: TimeInsights = {
  from: FROM.toISOString(),
  to: TO.toISOString(),
  meetingMinutes: 150,
  focusMinutes: 180,
  taskMinutes: 60,
  meetingCount: 2,
  focusGoalMinutes: 600,
  topPeople: [
    { email: 'alice@example.com', name: 'Alice Chen', meetings: 2, minutes: 150 },
    { email: 'bob@example.com', name: '', meetings: 1, minutes: 60 },
  ],
  byDay: [
    { date: '2026-07-06', meetingMinutes: 60, focusMinutes: 0 },
    { date: '2026-07-07', meetingMinutes: 90, focusMinutes: 0 },
    { date: '2026-07-08', meetingMinutes: 0, focusMinutes: 180 },
  ],
};

function renderPanel(props: Partial<Parameters<typeof InsightsPanel>[0]> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <InsightsPanel open onOpenChange={vi.fn()} from={FROM} to={TO} {...props} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  getTimeInsights.mockReset();
  getTimeInsights.mockResolvedValue(insights);
});

describe('formatMinutes', () => {
  it.each([
    [0, '0m'],
    [45, '45m'],
    [60, '1h'],
    [150, '2h 30m'],
  ])('formats %d as %s', (mins, want) => {
    expect(formatMinutes(mins)).toBe(want);
  });
});

describe('demoTimeInsights', () => {
  it('is internally consistent: totals equal the byDay sums', () => {
    const demo = demoTimeInsights(FROM, TO);
    expect(demo.byDay).toHaveLength(7);
    expect(demo.meetingMinutes).toBe(
      demo.byDay.reduce((sum, d) => sum + d.meetingMinutes, 0)
    );
    expect(demo.focusMinutes).toBe(demo.byDay.reduce((sum, d) => sum + d.focusMinutes, 0));
    expect(demo.topPeople.length).toBeGreaterThan(0);
  });
});

describe('InsightsPanel', () => {
  it('renders nothing while closed and fetches nothing', () => {
    renderPanel({ open: false });
    expect(screen.queryByRole('dialog', { name: 'Time insights' })).not.toBeInTheDocument();
    expect(getTimeInsights).not.toHaveBeenCalled();
  });

  it('renders the totals, split legend, and top people from the fetched document', async () => {
    renderPanel();
    // "2h 30m" appears in the meeting-time tile AND the split legend (and
    // alice's row folds it into one longer string).
    expect(await screen.findAllByText('2h 30m')).toHaveLength(2);
    expect(screen.getAllByText('3h').length).toBeGreaterThan(0); // focus tile + legend
    expect(screen.getByText('1h')).toBeInTheDocument(); // task blocks tile
    expect(screen.getByText('2')).toBeInTheDocument(); // meeting count tile
    expect(screen.getByText('Meetings vs focus')).toBeInTheDocument();
    expect(screen.getByTestId('split-meeting')).toBeInTheDocument();
    expect(screen.getByTestId('split-focus')).toBeInTheDocument();
    // Focus goal scaled caption.
    expect(screen.getByText('3h of 10h goal')).toBeInTheDocument();
    // Top people: named entry plus an email-only fallback.
    expect(screen.getByText('Alice Chen')).toBeInTheDocument();
    expect(screen.getByText('2 meetings · 2h 30m')).toBeInTheDocument();
    expect(screen.getByText('bob@example.com')).toBeInTheDocument();
    // Requested exactly the visible range.
    expect(getTimeInsights).toHaveBeenCalledWith(FROM.toISOString(), TO.toISOString());
  });

  it('closes via the close button', async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    renderPanel({ onOpenChange });
    await user.click(screen.getByRole('button', { name: 'Close insights' }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('closes on Escape (keyboard parity with the shift+i toggle)', async () => {
    const onOpenChange = vi.fn();
    renderPanel({ onOpenChange });
    await screen.findAllByText('2h 30m');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('does not listen for Escape while closed', () => {
    const onOpenChange = vi.fn();
    renderPanel({ open: false, onOpenChange });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('shows a quiet error state when the fetch fails outside demo mode', async () => {
    getTimeInsights.mockRejectedValue(new Error('boom'));
    renderPanel();
    expect(await screen.findByText("Couldn't load insights.")).toBeInTheDocument();
  });
});
