import type { BookingLink, Calendar } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { BookingLinks } from '@/components/app/booking-links';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchBookingLinksMock = vi.fn();
const createBookingLinkApiMock = vi.fn();
const updateBookingLinkApiMock = vi.fn();
const deleteBookingLinkApiMock = vi.fn();
vi.mock('@/lib/scheduling-data', () => ({
  fetchBookingLinks: (...args: unknown[]) => fetchBookingLinksMock(...args),
  createBookingLinkApi: (...args: unknown[]) => createBookingLinkApiMock(...args),
  updateBookingLinkApi: (...args: unknown[]) => updateBookingLinkApiMock(...args),
  deleteBookingLinkApi: (...args: unknown[]) => deleteBookingLinkApiMock(...args),
}));

const fetchCalendarsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

const CALENDAR: Calendar = {
  id: 'cal-1',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

function makeLink(overrides: Partial<BookingLink> = {}): BookingLink {
  return {
    id: 'link-1',
    slug: 'intro-call',
    title: '30-minute intro call',
    description: null,
    calendarId: CALENDAR.id,
    durationMinutes: 30,
    timeZone: 'UTC',
    windows: [{ weekday: 1, start: '09:00', end: '17:00' }],
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    dailyLimit: 0,
    minNoticeMin: 60,
    maxAdvanceDays: 30,
    respectWorkingHours: true,
    addConferencing: false,
    active: true,
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

function renderComponent() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <BookingLinks />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchCalendarsMock.mockResolvedValue([CALENDAR]);
});

describe('BookingLinks', () => {
  it('renders the link list and copies the real public URL to the clipboard', async () => {
    fetchBookingLinksMock.mockResolvedValue([makeLink()]);
    renderComponent();

    await screen.findByText('30-minute intro call');
    expect(screen.getByText('/book/intro-call')).toBeInTheDocument();

    const user = userEvent.setup();
    // userEvent.setup() installs its own Clipboard stub on navigator.clipboard
    // (for copy/paste support), replacing anything defined beforehand — so the
    // spy must be attached after setup(), not in beforeEach.
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
    await user.click(screen.getByRole('button', { name: /Copy link/ }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/book/intro-call'));
    });
    // Not a fabricated success — the toast only fires after the clipboard
    // write actually resolves (resolution-gated).
    expect(toastSuccess).toHaveBeenCalledWith('Booking link copied to clipboard');
  });

  it('shows an empty state with no links', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    renderComponent();
    await screen.findByText(/No booking links yet/);
  });

  it('flags an invalid slug locally, before any network call, and disables submit', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));

    const slugInput = await screen.findByLabelText('Slug');
    await user.type(slugInput, 'Not A Valid Slug!');

    expect(
      screen.getByText(/Lowercase letters, digits, and hyphens only/)
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create booking link/ })).toBeDisabled();
    expect(createBookingLinkApiMock).not.toHaveBeenCalled();
  });

  it('surfaces a 409 slug conflict inline on the slug field, not just a toast', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    createBookingLinkApiMock.mockRejectedValue(
      new ApiRequestError(409, 'conflict', 'slug "taken" is already taken')
    );
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));

    await user.type(await screen.findByLabelText('Title'), 'Intro call');
    await user.type(screen.getByLabelText('Slug'), 'taken');
    await user.click(screen.getByRole('combobox', { name: /^Calendar$/ }));
    await user.click(await screen.findByRole('option', { name: 'Work' }));

    await user.click(screen.getByRole('button', { name: /Create booking link/ }));

    await screen.findByText(/is already taken - try another slug/);
    // The generic failure toast must not also fire for this specific,
    // inline-handled case.
    expect(toastError).not.toHaveBeenCalled();
  });

  it('windows editor adds and removes availability rows', async () => {
    fetchBookingLinksMock.mockResolvedValue([]);
    renderComponent();
    await screen.findByText(/No booking links yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New booking link/ }));

    await screen.findByLabelText('Title');
    expect(screen.getAllByLabelText(/^Window \d+ start$/)).toHaveLength(1);

    await user.click(screen.getByRole('button', { name: /Add window/ }));
    expect(screen.getAllByLabelText(/^Window \d+ start$/)).toHaveLength(2);

    await user.click(screen.getByRole('button', { name: 'Remove window 2' }));
    expect(screen.getAllByLabelText(/^Window \d+ start$/)).toHaveLength(1);

    // A single remaining row cannot be removed (a booking link needs at
    // least one window).
    expect(screen.getByRole('button', { name: 'Remove window 1' })).toBeDisabled();
  });

  it('deletes a booking link', async () => {
    fetchBookingLinksMock.mockResolvedValue([makeLink()]);
    deleteBookingLinkApiMock.mockResolvedValue(undefined);
    renderComponent();

    await screen.findByText('30-minute intro call');
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Delete 30-minute intro call/ }));

    await waitFor(() => expect(deleteBookingLinkApiMock).toHaveBeenCalledWith('link-1'));
    expect(toastSuccess).toHaveBeenCalledWith('Booking link deleted');
  });
});
