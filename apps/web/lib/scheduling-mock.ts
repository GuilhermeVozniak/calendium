import type {
  Booking,
  BookingLink,
  BookingLinkInput,
  BusyInterval,
  MeetingPoll,
  PollInput,
  UserSettings,
} from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';

/**
 * In-memory mock for the scheduling API (booking links, bookings, meeting
 * polls, working-hours settings), used as a transparent fallback while the Go
 * backend is unreachable (explicit demo mode only — see lib/demo.ts). Mutations
 * persist for the lifetime of the page so create/edit/delete flows feel real.
 *
 * Errors mirror the real API's shape (ApiRequestError with the same status +
 * code the backend returns — see backend/internal/adapter/in/httpapi/codec.go)
 * so callers - the slug-conflict check in booking-links.tsx in particular -
 * behave identically against the mock and the real server.
 */

function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

function isoAt(daysFromNow: number, hour: number, minute = 0): string {
  const d = new Date();
  d.setDate(d.getDate() + daysFromNow);
  d.setHours(hour, minute, 0, 0);
  return d.toISOString();
}

interface SchedulingStore {
  links: BookingLink[];
  bookings: Booking[];
  polls: MeetingPoll[];
  settings: UserSettings;
}

let store: SchedulingStore | null = null;
let nextId = 1;

function seed(): SchedulingStore {
  const tz = localTimeZone();

  const links: BookingLink[] = [
    {
      id: 'link-intro',
      slug: 'intro-call',
      title: '30-minute intro call',
      description: 'A quick chat to see if we are a fit.',
      calendarId: 'cal-personal',
      durationMinutes: 30,
      timeZone: tz,
      windows: [
        { weekday: 1, start: '09:00', end: '17:00' },
        { weekday: 2, start: '09:00', end: '17:00' },
        { weekday: 3, start: '09:00', end: '17:00' },
        { weekday: 4, start: '09:00', end: '17:00' },
        { weekday: 5, start: '09:00', end: '13:00' },
      ],
      bufferBeforeMin: 0,
      bufferAfterMin: 15,
      dailyLimit: 4,
      minNoticeMin: 60,
      maxAdvanceDays: 30,
      respectWorkingHours: true,
      addConferencing: true,
      active: true,
      createdAt: isoAt(-7, 9),
    },
  ];

  const bookings: Booking[] = [
    {
      id: 'booking-1',
      linkId: 'link-intro',
      status: 'confirmed',
      start: isoAt(2, 10),
      end: isoAt(2, 10, 30),
      inviteeName: 'Jordan Lee',
      inviteeEmail: 'jordan@example.com',
      inviteeTimeZone: tz,
      note: null,
      eventId: null,
      createdAt: isoAt(-1, 9),
    },
    {
      id: 'booking-2',
      linkId: 'link-intro',
      status: 'hold',
      start: isoAt(3, 14),
      end: isoAt(3, 14, 30),
      inviteeName: 'Sam Rivera',
      inviteeEmail: 'sam@example.com',
      inviteeTimeZone: tz,
      note: 'Looking forward to it!',
      eventId: null,
      createdAt: isoAt(0, 8),
    },
  ];

  const polls: MeetingPoll[] = [
    {
      id: 'poll-1',
      token: 'demo-token-1',
      title: 'Team sync — pick a time',
      description: null,
      calendarId: 'cal-personal',
      durationMinutes: 30,
      options: [
        { id: 'opt-1', start: isoAt(4, 10), end: isoAt(4, 10, 30) },
        { id: 'opt-2', start: isoAt(5, 14), end: isoAt(5, 14, 30) },
      ],
      status: 'open',
      winnerOptionId: null,
      eventId: null,
      createdAt: isoAt(-2, 9),
    },
  ];

  const settings: UserSettings = {
    timeZone: tz,
    workingHours: [
      { weekday: 1, start: '09:00', end: '17:00' },
      { weekday: 2, start: '09:00', end: '17:00' },
      { weekday: 3, start: '09:00', end: '17:00' },
      { weekday: 4, start: '09:00', end: '17:00' },
      { weekday: 5, start: '09:00', end: '17:00' },
    ],
    workingLocation: '',
    aiBackground: true,
  };

  return { links, bookings, polls, settings };
}

function getStore(): SchedulingStore {
  store ??= seed();
  return store;
}

function conflict(message: string): never {
  throw new ApiRequestError(409, 'conflict', message);
}

function notFound(message: string): never {
  throw new ApiRequestError(404, 'not_found', message);
}

export const schedulingMock = {
  listBookingLinks(): BookingLink[] {
    return getStore().links.map((l) => ({ ...l, windows: l.windows.map((w) => ({ ...w })) }));
  },

  createBookingLink(input: BookingLinkInput): BookingLink {
    const s = getStore();
    if (s.links.some((l) => l.slug === input.slug)) {
      conflict(`slug "${input.slug}" is already taken`);
    }
    const link: BookingLink = {
      id: `link-local-${nextId++}`,
      createdAt: new Date().toISOString(),
      ...input,
      description: input.description ?? null,
    };
    s.links.unshift(link);
    return { ...link };
  },

  updateBookingLink(id: string, input: BookingLinkInput): BookingLink {
    const s = getStore();
    const idx = s.links.findIndex((l) => l.id === id);
    if (idx === -1) notFound('booking link not found');
    if (s.links.some((l) => l.id !== id && l.slug === input.slug)) {
      conflict(`slug "${input.slug}" is already taken`);
    }
    const updated: BookingLink = {
      ...s.links[idx]!,
      ...input,
      description: input.description ?? null,
    };
    s.links[idx] = updated;
    return { ...updated };
  },

  deleteBookingLink(id: string): void {
    const s = getStore();
    s.links = s.links.filter((l) => l.id !== id);
  },

  listBookings(): Booking[] {
    return getStore().bookings.map((b) => ({ ...b }));
  },

  cancelBooking(id: string): void {
    const s = getStore();
    const idx = s.bookings.findIndex((b) => b.id === id);
    if (idx === -1) notFound('booking not found');
    s.bookings[idx] = { ...s.bookings[idx]!, status: 'cancelled' };
  },

  listPolls(): MeetingPoll[] {
    return getStore().polls.map((p) => ({ ...p, options: p.options.map((o) => ({ ...o })) }));
  },

  createPoll(input: PollInput): MeetingPoll {
    const s = getStore();
    const id = `poll-local-${nextId++}`;
    const poll: MeetingPoll = {
      id,
      token: `demo-token-${id}`,
      title: input.title,
      description: input.description ?? null,
      calendarId: input.calendarId,
      durationMinutes: input.durationMinutes,
      options: input.options.map((o, i) => ({ id: `opt-local-${id}-${i}`, ...o })),
      status: 'open',
      winnerOptionId: null,
      eventId: null,
      createdAt: new Date().toISOString(),
    };
    s.polls.unshift(poll);
    return { ...poll };
  },

  confirmPoll(id: string, optionId: string): MeetingPoll {
    const s = getStore();
    const idx = s.polls.findIndex((p) => p.id === id);
    if (idx === -1) notFound('poll not found');
    const poll = s.polls[idx]!;
    if (!poll.options.some((o) => o.id === optionId)) {
      throw new ApiRequestError(400, 'validation_failed', 'unknown option');
    }
    const updated: MeetingPoll = {
      ...poll,
      status: 'confirmed',
      winnerOptionId: optionId,
      eventId: `evt-mock-${id}`,
    };
    s.polls[idx] = updated;
    return { ...updated };
  },

  deletePoll(id: string): void {
    const s = getStore();
    s.polls = s.polls.filter((p) => p.id !== id);
  },

  getSettings(): UserSettings {
    const s = getStore().settings;
    return { ...s, workingHours: s.workingHours.map((w) => ({ ...w })) };
  },

  updateSettings(next: UserSettings): UserSettings {
    const s = getStore();
    s.settings = { ...next, workingHours: next.workingHours.map((w) => ({ ...w })) };
    return { ...s.settings, workingHours: s.settings.workingHours.map((w) => ({ ...w })) };
  },

  getFreeBusy(_emails: string[], _from: string, _to: string): Record<string, BusyInterval[]> {
    return {};
  },
};
