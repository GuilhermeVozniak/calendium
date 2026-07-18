import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { AvailabilitySlot, BookingLink } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AvailabilityDialog, buildShareText } from '@/components/app/availability';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchAvailabilityMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchAvailability: (...args: unknown[]) => fetchAvailabilityMock(...args),
}));

const listBookingLinksMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listBookingLinks: (...args: unknown[]) => listBookingLinksMock(...args),
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

function makeSlot(startIso: string, durationMin: number): AvailabilitySlot {
  const start = new Date(startIso);
  const end = new Date(start.getTime() + durationMin * 60_000);
  return { start: start.toISOString(), end: end.toISOString() };
}

const BOOKING_LINK: BookingLink = {
  id: 'link-1',
  slug: 'guilherme-30min',
  title: '30-minute chat',
  description: null,
  calendarId: 'cal-1',
  durationMinutes: 30,
  timeZone: 'America/New_York',
  windows: [],
  bufferBeforeMin: 0,
  bufferAfterMin: 0,
  dailyLimit: 0,
  minNoticeMin: 0,
  maxAdvanceDays: 0,
  respectWorkingHours: true,
  addConferencing: false,
  active: true,
  createdAt: '2026-01-01T00:00:00Z',
};

function renderDialog(onOpenChange = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AvailabilityDialog open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listBookingLinksMock.mockResolvedValue([]);
});

/**
 * (Re-)installs a mock `navigator.clipboard.writeText` and returns it. jsdom
 * 29 ships a real (permission-gated) Clipboard implementation that gets
 * lazily instantiated on the `navigator` instance the first time something
 * touches it after a prior click/focus event — which silently clobbers a
 * mock installed earlier in the test. Calling this immediately before the
 * "Copy to clipboard" click (never earlier) and asserting against the
 * returned local reference — never re-reading `navigator.clipboard.writeText`
 * afterwards — sidesteps that: it doesn't matter if jsdom swaps the object
 * out again during some *later* interaction, because nothing after the copy
 * click needs the mock anymore.
 */
function mockClipboardWriteText() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  return writeText;
}

// ---------------------------------------------------------------------------
// buildShareText — pure function, fixed IANA zones (never machine-TZ-dependent)
// ---------------------------------------------------------------------------

describe('buildShareText', () => {
  it('matches the original (pre-conversion) format when recipientTZ === ownerTZ', () => {
    const slots = [makeSlot('2026-07-20T18:00:00Z', 30)];
    const same = buildShareText(slots, 'America/New_York', 'America/New_York');
    // Byte-identical shape to the format that shipped before Task 16: header
    // names the raw owner zone string, no per-slot re-conversion.
    expect(same.startsWith('Here are a few times that work for me (all times America/New_York):')).toBe(
      true
    );
    expect(same).not.toContain('Or pick a time:');
  });

  it('converts every slot into the recipient zone and labels it with name + abbreviation', () => {
    // 18:00 UTC on Jul 20 2026 (a Monday) is 2:00 PM in New York (EDT) and
    // 11:30 PM in Kolkata (UTC+5:30, no DST) — fixed instants, fixed zones,
    // independent of whatever TZ the test runner's machine is in.
    const slots = [makeSlot('2026-07-20T18:00:00Z', 30)];
    const text = buildShareText(slots, 'America/New_York', 'Asia/Calcutta');
    expect(text).toContain('Here are a few times that work for me (all times India Standard Time — GMT+5:30):');
    expect(text).toContain('11:30 PM – 12:00 AM');
  });

  it('produces the documented "Eastern Time — EDT" header for a New York recipient in summer', () => {
    const slots = [makeSlot('2026-07-20T12:00:00Z', 30)];
    const text = buildShareText(slots, 'Europe/London', 'America/New_York');
    expect(text).toContain('Here are a few times that work for me (all times Eastern Time — EDT):');
  });

  it('appends a booking-link footer line when a booking URL is supplied', () => {
    const slots = [makeSlot('2026-07-20T18:00:00Z', 30)];
    const text = buildShareText(
      slots,
      'America/New_York',
      'America/New_York',
      'https://calendium.app/book/guilherme-30min'
    );
    expect(text).toContain('Or pick a time: https://calendium.app/book/guilherme-30min');
  });

  it('omits the footer line entirely when no booking URL is supplied', () => {
    const slots = [makeSlot('2026-07-20T18:00:00Z', 30)];
    expect(buildShareText(slots, 'UTC', 'UTC')).not.toContain('Or pick a time');
  });

  it('groups converted slots by the recipient zone calendar day, correctly across a UTC-day boundary', () => {
    // 23:30 UTC on Jan 15 is still Jan 15, 6:30 PM in New York (EST) — the
    // day heading must read "Thursday, Jan 15", not "Friday, Jan 16" (the
    // UTC date), when converted for a New York recipient.
    const slots = [makeSlot('2026-01-15T23:30:00Z', 30)];
    const text = buildShareText(slots, 'UTC', 'America/New_York');
    expect(text).toContain('Thursday, Jan 15');
  });
});

// ---------------------------------------------------------------------------
// AvailabilityDialog — recipient-TZ picker + booking-link footer, wired end
// to end. Slot data always comes from the mocked fetchAvailability (i.e. the
// real API call in production), never fabricated client-side.
// ---------------------------------------------------------------------------

describe('AvailabilityDialog', () => {
  it('copies slot text pre-converted into the selected recipient timezone', async () => {
    fetchAvailabilityMock.mockResolvedValue([makeSlot('2026-07-20T18:00:00Z', 30)]);
    const user = userEvent.setup();
    renderDialog();

    // The slot chips render in the machine's local timezone (unrelated to
    // this feature — only the copied text is recipient-TZ-aware), so wait on
    // the TZ-independent "N slot(s) selected" footer instead of a formatted
    // time string.
    await waitFor(() => expect(screen.getByText('1 slot selected')).toBeInTheDocument());

    // Open the recipient-timezone picker and search for Kolkata/Calcutta.
    await user.click(screen.getByLabelText('Recipient timezone'));
    const search = await screen.findByPlaceholderText('Search timezone…');
    await user.type(search, 'Calcutta');
    await user.click(await screen.findByText('Asia/Calcutta'));

    const writeText = mockClipboardWriteText();
    await user.click(screen.getByRole('button', { name: /copy to clipboard/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalled());
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain('India Standard Time');
    expect(copied).toContain('11:30 PM');
  });

  it('does not show a booking-link picker when there are no active links', async () => {
    fetchAvailabilityMock.mockResolvedValue([makeSlot('2026-07-20T18:00:00Z', 30)]);
    listBookingLinksMock.mockResolvedValue([]);
    renderDialog();

    await waitFor(() => expect(fetchAvailabilityMock).toHaveBeenCalled());
    expect(screen.queryByLabelText('Booking link')).not.toBeInTheDocument();
  });

  it('appends the public booking URL to the copied text when a link is picked', async () => {
    fetchAvailabilityMock.mockResolvedValue([makeSlot('2026-07-20T18:00:00Z', 30)]);
    listBookingLinksMock.mockResolvedValue([BOOKING_LINK]);
    const user = userEvent.setup();
    renderDialog();

    const picker = await screen.findByLabelText('Booking link');
    await user.click(picker);
    await user.click(await screen.findByText('30-minute chat'));

    const writeText = mockClipboardWriteText();
    await user.click(screen.getByRole('button', { name: /copy to clipboard/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalled());
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain(`Or pick a time: ${window.location.origin}/book/guilherme-30min`);
  });

  it('filters an inactive booking link out of the picker', async () => {
    fetchAvailabilityMock.mockResolvedValue([makeSlot('2026-07-20T18:00:00Z', 30)]);
    listBookingLinksMock.mockResolvedValue([{ ...BOOKING_LINK, active: false }]);
    renderDialog();

    await waitFor(() => expect(fetchAvailabilityMock).toHaveBeenCalled());
    expect(screen.queryByLabelText('Booking link')).not.toBeInTheDocument();
  });
});
