import type { UserSettings } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock only the API boundary (@/lib/api).
const getSettingsMock = vi.fn();
const updateSettingsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () =>
    new Proxy(
      {},
      {
        get: (_target, key) => {
          if (key === 'getSettings') return (...args: unknown[]) => getSettingsMock(...args);
          if (key === 'updateSettings') return (...args: unknown[]) => updateSettingsMock(...args);
          return async () => [];
        },
      }
    ),
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// Sibling sections rendered under Scheduling are out of scope here.
vi.mock('./calendar-automation', () => ({ CalendarAutomationSection: () => null }));
vi.mock('@/components/app/meeting-polls', () => ({ MeetingPolls: () => null }));
vi.mock('@/components/app/booking-links', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/app/booking-links')>()),
  BookingLinks: () => null,
}));

import { SchedulingSection } from './settings-page';

// The cache says background AI is ON; another device has since turned it
// off. Saving working hours here must not switch it back on.
const CACHED: UserSettings = {
  timeZone: 'Europe/Lisbon',
  workingHours: [],
  workingLocation: 'Home',
  aiBackground: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  getSettingsMock.mockResolvedValue(CACHED);
  updateSettingsMock.mockImplementation(async (s: Partial<UserSettings>) => ({ ...CACHED, aiBackground: false, ...s }));
});

describe('SchedulingSection save', () => {
  it('sends only the scheduling fields — never the cached aiBackground', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={queryClient}>
        <SchedulingSection />
      </QueryClientProvider>
    );
    const location = await screen.findByLabelText('Working location');
    await waitFor(() => expect(location).toHaveValue('Home'));
    await user.clear(location);
    await user.type(location, 'Office');
    await user.click(screen.getAllByRole('button', { name: 'Save' })[0]!);

    await waitFor(() => expect(updateSettingsMock).toHaveBeenCalledTimes(1));
    const sent = updateSettingsMock.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(sent).toEqual({ timeZone: 'Europe/Lisbon', workingHours: [], workingLocation: 'Office' });
    expect(sent).not.toHaveProperty('aiBackground');
    // The server's answer (switch off) replaces the stale cached value.
    await waitFor(() =>
      expect(queryClient.getQueryData<UserSettings>(['scheduling-settings'])?.aiBackground).toBe(false)
    );
  });
});
