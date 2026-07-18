import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiRequestError, type AvailabilitySlot, type Booking, type PublicBookingPage as PublicBookingPageDoc } from '@calendium/shared';

import { formatInTZ } from '@/lib/timezone';

const fetchPublicBookingPageMock = vi.fn();
const fetchPublicSlotsMock = vi.fn();
const createPublicBookingMock = vi.fn();

vi.mock('@calendium/shared', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@calendium/shared')>();
  return {
    ...actual,
    fetchPublicBookingPage: (...args: unknown[]) => fetchPublicBookingPageMock(...args),
    fetchPublicSlots: (...args: unknown[]) => fetchPublicSlotsMock(...args),
    createPublicBooking: (...args: unknown[]) => createPublicBookingMock(...args),
  };
});

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { PublicBookingPage } from './booking-page';

const PAGE: PublicBookingPageDoc = {
  slug: 'jane-30min',
  title: 'Intro call',
  description: 'A quick chat to see if we are a fit.',
  ownerName: 'Jane Doe',
  durationMinutes: 30,
  timeZone: 'America/New_York',
};

const SLOT_A: AvailabilitySlot = { start: '2026-07-22T14:00:00.000Z', end: '2026-07-22T14:30:00.000Z' };
const SLOT_B: AvailabilitySlot = { start: '2026-07-22T15:00:00.000Z', end: '2026-07-22T15:30:00.000Z' };

const BOOKING: Booking = {
  id: 'bk_1',
  linkId: 'link_1',
  status: 'confirmed',
  start: SLOT_A.start,
  end: SLOT_A.end,
  inviteeName: 'Alex Guest',
  inviteeEmail: 'alex@example.com',
  inviteeTimeZone: 'UTC',
  note: null,
  eventId: 'ev_1',
  createdAt: '2026-07-18T00:00:00.000Z',
};

function renderPage() {
  return render(<PublicBookingPage slug="jane-30min" />);
}

describe('PublicBookingPage', () => {
  beforeEach(() => {
    fetchPublicBookingPageMock.mockReset().mockResolvedValue(PAGE);
    fetchPublicSlotsMock.mockReset().mockResolvedValue([SLOT_A, SLOT_B]);
    createPublicBookingMock.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it('shows the header once the booking page document loads', async () => {
    renderPage();
    expect(await screen.findByText('Intro call')).toBeInTheDocument();
    expect(screen.getByText('Jane Doe', { exact: false })).toBeInTheDocument();
    expect(screen.getByText('A quick chat to see if we are a fit.')).toBeInTheDocument();
    expect(screen.getByText('30 min', { exact: false })).toBeInTheDocument();
  });

  it('renders slot times in the selected (default browser) timezone', async () => {
    renderPage();
    // The component defaults to browserTimeZone(); compute the expected label
    // the same way the component does so this assertion is host-TZ-agnostic.
    const expected = formatInTZ(SLOT_A.start, Intl.DateTimeFormat().resolvedOptions().timeZone, 'time');
    expect(await screen.findByRole('button', { name: new RegExp(expected) })).toBeInTheDocument();
  });

  it('switches displayed slot labels when the visitor changes timezone, without refetching slots', async () => {
    renderPage();
    await screen.findByText('Intro call');
    await waitFor(() => expect(fetchPublicSlotsMock).toHaveBeenCalledTimes(1));

    const tzTrigger = screen.getByLabelText('Times shown in');
    await userEvent.click(tzTrigger);
    const option = await screen.findByRole('option', { name: /New York/i });
    await userEvent.click(option);

    const expected = formatInTZ(SLOT_A.start, 'America/New_York', 'time');
    expect(await screen.findByRole('button', { name: new RegExp(expected) })).toBeInTheDocument();

    // TZ is purely a display concern — the same absolute slots are re-labeled,
    // not re-fetched.
    expect(fetchPublicSlotsMock).toHaveBeenCalledTimes(1);
  });

  it('books a slot end-to-end and shows a confirmation screen only after the mutation resolves', async () => {
    let resolveBooking!: (b: Booking) => void;
    createPublicBookingMock.mockReturnValue(
      new Promise<Booking>((resolve) => {
        resolveBooking = resolve;
      })
    );

    renderPage();
    const expectedSlotLabel = formatInTZ(
      SLOT_A.start,
      Intl.DateTimeFormat().resolvedOptions().timeZone,
      'time'
    );
    const slotButton = await screen.findByRole('button', { name: new RegExp(expectedSlotLabel) });
    await userEvent.click(slotButton);

    await userEvent.type(screen.getByLabelText('Name'), 'Alex Guest');
    await userEvent.type(screen.getByLabelText('Email'), 'alex@example.com');
    await userEvent.click(screen.getByRole('button', { name: /confirm booking/i }));

    // Mutation is in flight — no confirmation yet (honesty: no optimistic UI).
    expect(screen.queryByText(/you’re booked/i)).not.toBeInTheDocument();

    resolveBooking(BOOKING);
    expect(await screen.findByText(/you’re booked/i)).toBeInTheDocument();
    expect(createPublicBookingMock).toHaveBeenCalledWith(
      '',
      'jane-30min',
      expect.objectContaining({ start: SLOT_A.start, inviteeName: 'Alex Guest', inviteeEmail: 'alex@example.com' })
    );
  });

  it('on a 409 conflict, refetches slots and shows a friendly message instead of a confirmation', async () => {
    createPublicBookingMock.mockRejectedValue(new ApiRequestError(409, 'conflict', 'conflict'));
    fetchPublicSlotsMock.mockResolvedValueOnce([SLOT_A, SLOT_B]).mockResolvedValueOnce([SLOT_B]);

    renderPage();
    const expectedSlotLabel = formatInTZ(
      SLOT_A.start,
      Intl.DateTimeFormat().resolvedOptions().timeZone,
      'time'
    );
    const slotButton = await screen.findByRole('button', { name: new RegExp(expectedSlotLabel) });
    await userEvent.click(slotButton);
    await userEvent.type(screen.getByLabelText('Name'), 'Alex Guest');
    await userEvent.type(screen.getByLabelText('Email'), 'alex@example.com');
    await userEvent.click(screen.getByRole('button', { name: /confirm booking/i }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith('That slot was just taken'));
    await waitFor(() => expect(fetchPublicSlotsMock).toHaveBeenCalledTimes(2));
    expect(screen.queryByText(/you’re booked/i)).not.toBeInTheDocument();
  });

  it('on a 429, shows a rate-limit message rather than a toast', async () => {
    createPublicBookingMock.mockRejectedValue(new ApiRequestError(429, 'rate_limited', 'slow down'));

    renderPage();
    const expectedSlotLabel = formatInTZ(
      SLOT_A.start,
      Intl.DateTimeFormat().resolvedOptions().timeZone,
      'time'
    );
    const slotButton = await screen.findByRole('button', { name: new RegExp(expectedSlotLabel) });
    await userEvent.click(slotButton);
    await userEvent.type(screen.getByLabelText('Name'), 'Alex Guest');
    await userEvent.type(screen.getByLabelText('Email'), 'alex@example.com');
    await userEvent.click(screen.getByRole('button', { name: /confirm booking/i }));

    expect(await screen.findByText(/too many requests/i)).toBeInTheDocument();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('shows "no times available" when the week has no slots', async () => {
    fetchPublicSlotsMock.mockResolvedValue([]);
    renderPage();
    expect(await screen.findByText(/no times available/i)).toBeInTheDocument();
  });

  it('shows a not-found message for an unknown slug', async () => {
    fetchPublicBookingPageMock.mockRejectedValue(new ApiRequestError(404, 'not_found', 'not found'));
    renderPage();
    expect(await screen.findByText(/doesn’t exist|not found/i)).toBeInTheDocument();
  });

  it('shows a loading state before the booking page document resolves', async () => {
    let resolvePage!: (p: PublicBookingPageDoc) => void;
    fetchPublicBookingPageMock.mockReturnValue(
      new Promise<PublicBookingPageDoc>((resolve) => {
        resolvePage = resolve;
      })
    );
    renderPage();
    expect(screen.getByTestId('booking-page-loading')).toBeInTheDocument();
    resolvePage(PAGE);
    await screen.findByText('Intro call');
  });
});
