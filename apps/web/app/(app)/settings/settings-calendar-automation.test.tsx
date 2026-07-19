import type { CalendarPrefs } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock only the API boundary (@/lib/api) so the real calendar-prefs-data.ts
// wrapper runs for real (settings-compose.test.tsx pattern).
const getCalendarPrefsMock = vi.fn();
const updateCalendarPrefsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    getCalendarPrefs: (...args: unknown[]) => getCalendarPrefsMock(...args),
    updateCalendarPrefs: (...args: unknown[]) => updateCalendarPrefsMock(...args),
  }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { ApiRequestError } from '@calendium/shared';

import { CalendarAutomationSection } from './calendar-automation';

const PREFS: CalendarPrefs = {
  timeZone: 'Europe/Amsterdam',
  workDays: [1, 2, 3, 4, 5],
  workdayStartMinutes: 540,
  workdayEndMinutes: 1020,
  focusGoalMinutesPerWeek: 600,
  focusAutoDecline: true,
  focusDeclineMessage: 'Deep work.',
  autoBufferMinutes: 10,
  oooAutoDecline: false,
  oooDeclineMessage: '',
  travelBuffers: false,
  travelMode: 'driving',
  leaveAlerts: false,
  homeLat: null,
  homeLon: null,
  weatherEnabled: false,
};

function renderSection() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <CalendarAutomationSection />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  getCalendarPrefsMock.mockResolvedValue(PREFS);
  updateCalendarPrefsMock.mockImplementation(async (patch: Partial<CalendarPrefs>) => ({
    ...PREFS,
    ...patch,
  }));
});

describe('CalendarAutomationSection', () => {
  it('hydrates the form from GET /v1/prefs/calendar', async () => {
    renderSection();

    const tz = await screen.findByLabelText('Time zone');
    expect(tz).toHaveValue('Europe/Amsterdam');

    // Focus goal 600 min => 10 h slider; auto-decline on with its message.
    expect(screen.getByLabelText(/Weekly focus goal/)).toHaveValue('10');
    expect(screen.getByText('(10 h/week)')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Auto-decline invites during focus blocks' })).toBeChecked();
    expect(screen.getByLabelText('Focus decline message')).toHaveValue('Deep work.');

    // Working-day chips: Mon pressed, Sun not.
    expect(screen.getByRole('button', { name: 'Mon' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Sun' })).toHaveAttribute('aria-pressed', 'false');

    // Off automations render unchecked, and their dependent inputs stay hidden.
    expect(
      screen.getByRole('switch', { name: 'Auto-decline invites while out of office' })
    ).not.toBeChecked();
    expect(screen.queryByLabelText('Out-of-office decline message')).not.toBeInTheDocument();
  });

  it('saves the edited form as one PATCH and toasts success', async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByLabelText('Time zone');

    await user.click(screen.getByRole('switch', { name: 'Show weather on outdoor and located events' }));
    await user.click(screen.getByRole('button', { name: 'Sat' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(updateCalendarPrefsMock).toHaveBeenCalledTimes(1));
    const patch = updateCalendarPrefsMock.mock.calls[0]![0] as Partial<CalendarPrefs>;
    expect(patch.weatherEnabled).toBe(true);
    expect(patch.workDays).toEqual([1, 2, 3, 4, 5, 6]);
    expect(patch.timeZone).toBe('Europe/Amsterdam'); // untouched fields ride along unchanged
    expect(patch.homeLat).toBeNull();
    expect(toastSuccess).toHaveBeenCalledWith('Calendar automation settings saved');
  });

  it('typed home coordinates are sent as numbers', async () => {
    const user = userEvent.setup();
    renderSection();
    await screen.findByLabelText('Time zone');

    await user.type(screen.getByLabelText('Home latitude'), '52.37');
    await user.type(screen.getByLabelText('Home longitude'), '4.89');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(updateCalendarPrefsMock).toHaveBeenCalledTimes(1));
    const patch = updateCalendarPrefsMock.mock.calls[0]![0] as Partial<CalendarPrefs>;
    expect(patch.homeLat).toBe(52.37);
    expect(patch.homeLon).toBe(4.89);
  });

  it('surfaces a 400 validation failure without crashing the form', async () => {
    updateCalendarPrefsMock.mockRejectedValue(
      new ApiRequestError(400, 'validation_failed', 'validation failed')
    );
    const user = userEvent.setup();
    renderSection();
    await screen.findByLabelText('Time zone');

    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        'Could not save — some values are invalid. Check the highlighted fields.'
      )
    );
    expect(screen.getByLabelText('Time zone')).toBeInTheDocument();
  });

  it('surfaces the 402 paywall with a subscription hint', async () => {
    updateCalendarPrefsMock.mockRejectedValue(
      new ApiRequestError(402, 'payment_required', 'payment required')
    );
    const user = userEvent.setup();
    renderSection();
    await screen.findByLabelText('Time zone');

    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Calendar automation requires an active subscription.')
    );
  });
});
