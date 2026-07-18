import { addDays, addMinutes, setHours, setMinutes, startOfDay, startOfWeek } from 'date-fns';

import type {
  Attendee,
  AvailabilitySlot,
  Calendar,
  Event,
  EventInput,
  EventPatch,
  RsvpStatus,
} from '@calendium/shared';

/**
 * In-memory mock for the calendar API, used as a transparent fallback while
 * the Go backend is unreachable (local dev / demo). Mutations persist for the
 * lifetime of the page so create/edit/delete flows feel real.
 */

/** Email the mock data treats as the signed-in user (RSVP self-detection). */
export const MOCK_SELF_EMAIL = 'you@calendium.app';

const MOCK_ACCOUNT_ID = 'acct-mock-google';

function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

function attendee(
  email: string,
  name: string | null,
  response: RsvpStatus,
  organizer = false,
  optional = false
): Attendee {
  return { email, name, response, organizer, optional };
}

interface SeedEvent {
  calendarId: string;
  title: string;
  /** Day offset from the start (Sunday) of the current week. */
  day: number;
  /** Week offset from the current week (0 = this week). Lets seeds spread
   *  across a full month so month view has something in every row. */
  weekOffset?: number;
  hour?: number;
  minute?: number;
  durationMinutes?: number;
  allDay?: boolean;
  location?: string;
  description?: string;
  attendees?: Attendee[];
  meet?: boolean;
  reminders?: number[];
  status?: Event['status'];
}

interface MockStore {
  calendars: Calendar[];
  events: Event[];
}

let store: MockStore | null = null;
let nextId = 1;

function seed(): MockStore {
  const tz = localTimeZone();
  const weekStart = startOfWeek(new Date(), { weekStartsOn: 0 });

  const calendars: Calendar[] = [
    {
      id: 'cal-personal',
      accountId: MOCK_ACCOUNT_ID,
      name: 'Personal',
      color: '#6366f1',
      timeZone: tz,
      isPrimary: true,
      isVisible: true,
      canWrite: true,
    },
    {
      id: 'cal-work',
      accountId: MOCK_ACCOUNT_ID,
      name: 'Work',
      color: '#10b981',
      timeZone: tz,
      isPrimary: false,
      isVisible: true,
      canWrite: true,
    },
  ];

  const seeds: SeedEvent[] = [];

  // Daily standup, Mon-Fri.
  for (let day = 1; day <= 5; day++) {
    seeds.push({
      calendarId: 'cal-work',
      title: 'Daily standup',
      day,
      hour: 9,
      minute: 30,
      durationMinutes: 15,
      meet: true,
      attendees: [
        attendee(MOCK_SELF_EMAIL, 'You', 'accepted', true),
        attendee('rafael@calendium.app', 'Rafael Costa', 'accepted'),
        attendee('marina@calendium.app', 'Marina Duarte', 'accepted'),
      ],
    });
  }

  seeds.push(
    { calendarId: 'cal-personal', title: 'Gym', day: 1, hour: 7, durationMinutes: 60 },
    { calendarId: 'cal-personal', title: 'Gym', day: 3, hour: 7, durationMinutes: 60 },
    { calendarId: 'cal-personal', title: 'Gym', day: 5, hour: 7, durationMinutes: 60 },
    {
      calendarId: 'cal-work',
      title: 'Sprint planning',
      day: 1,
      hour: 11,
      durationMinutes: 90,
      meet: true,
      description: 'Scope the sync-engine milestone.',
    },
    {
      calendarId: 'cal-work',
      title: 'Ship v0.2',
      day: 2,
      allDay: true,
    },
    {
      calendarId: 'cal-work',
      title: '1:1 with Rafael',
      day: 2,
      hour: 14,
      durationMinutes: 45,
      meet: true,
      attendees: [
        attendee(MOCK_SELF_EMAIL, 'You', 'accepted', true),
        attendee('rafael@calendium.app', 'Rafael Costa', 'accepted'),
      ],
    },
    { calendarId: 'cal-work', title: 'Product sync', day: 2, hour: 16, durationMinutes: 30 },
    {
      calendarId: 'cal-personal',
      title: 'Lunch with Ana',
      day: 3,
      hour: 12,
      minute: 30,
      durationMinutes: 60,
      location: 'Nolita, Pinheiros',
      attendees: [
        attendee('ana.lima@gmail.com', 'Ana Lima', 'accepted', true),
        attendee(MOCK_SELF_EMAIL, 'You', 'accepted'),
      ],
    },
    {
      calendarId: 'cal-work',
      title: 'Team offsite planning',
      day: 3,
      hour: 14,
      durationMinutes: 60,
      description: 'Venue shortlist + budget.',
    },
    {
      calendarId: 'cal-work',
      title: 'Interview: Staff Engineer',
      day: 4,
      hour: 10,
      durationMinutes: 60,
      meet: true,
      attendees: [
        attendee('recruiting@calendium.app', 'Recruiting', 'accepted', true),
        attendee(MOCK_SELF_EMAIL, 'You', 'needs_action'),
        attendee('candidate@example.com', 'Jordan Reyes', 'accepted'),
      ],
    },
    {
      calendarId: 'cal-work',
      title: 'Design review',
      day: 4,
      hour: 15,
      durationMinutes: 60,
      meet: true,
      description: 'Calendar week-view polish pass.',
      attendees: [
        attendee(MOCK_SELF_EMAIL, 'You', 'accepted', true),
        attendee('marina@calendium.app', 'Marina Duarte', 'accepted'),
        attendee('leo@calendium.app', 'Leo Tanaka', 'tentative'),
      ],
    },
    {
      calendarId: 'cal-work',
      title: 'Marketing review',
      day: 4,
      hour: 15,
      minute: 30,
      durationMinutes: 45,
      status: 'tentative',
    },
    { calendarId: 'cal-work', title: 'Investor update draft', day: 4, hour: 17, durationMinutes: 60 },
    {
      calendarId: 'cal-personal',
      title: 'Dentist',
      day: 5,
      hour: 11,
      minute: 30,
      durationMinutes: 45,
      location: 'Av. Paulista 1000',
      reminders: [60, 10],
    },
    {
      calendarId: 'cal-personal',
      title: 'Coffee with Marina',
      day: 5,
      hour: 15,
      minute: 30,
      durationMinutes: 30,
      location: 'Coffee Lab',
    },
    {
      calendarId: 'cal-personal',
      title: 'Flight to Floripa',
      day: 6,
      hour: 8,
      durationMinutes: 120,
      location: 'GRU T2',
      reminders: [1440, 120],
    },
    { calendarId: 'cal-personal', title: 'Brunch with parents', day: 0, hour: 11, durationMinutes: 90 },

    // Spread a lighter set of events across neighboring weeks so month view
    // (which shows 5-6 weeks at once) isn't empty outside the current week.
    { calendarId: 'cal-work', title: 'Monthly planning', weekOffset: -2, day: 1, hour: 10, durationMinutes: 60 },
    { calendarId: 'cal-personal', title: 'Gym', weekOffset: -2, day: 3, hour: 7, durationMinutes: 60 },
    {
      calendarId: 'cal-work',
      title: 'Board update draft',
      weekOffset: -2,
      day: 4,
      hour: 14,
      durationMinutes: 45,
    },
    { calendarId: 'cal-personal', title: 'Haircut', weekOffset: -1, day: 2, hour: 17, durationMinutes: 45 },
    {
      calendarId: 'cal-work',
      title: 'Roadmap review',
      weekOffset: -1,
      day: 3,
      hour: 11,
      durationMinutes: 60,
      meet: true,
    },
    { calendarId: 'cal-work', title: 'All-hands', weekOffset: -1, day: 5, hour: 9, durationMinutes: 30, meet: true },
    {
      calendarId: 'cal-personal',
      title: 'Anniversary dinner',
      weekOffset: 1,
      day: 5,
      hour: 19,
      durationMinutes: 120,
      location: 'Mocotó',
    },
    {
      calendarId: 'cal-work',
      title: 'Customer call',
      weekOffset: 1,
      day: 2,
      hour: 13,
      durationMinutes: 30,
      meet: true,
    },
    { calendarId: 'cal-personal', title: 'Gym', weekOffset: 1, day: 1, hour: 7, durationMinutes: 60 },
    { calendarId: 'cal-work', title: 'Retro', weekOffset: 1, day: 4, hour: 16, durationMinutes: 45 },
    {
      calendarId: 'cal-work',
      title: 'Quarterly business review',
      weekOffset: 2,
      day: 2,
      hour: 10,
      durationMinutes: 90,
      meet: true,
    },
    {
      calendarId: 'cal-personal',
      title: 'Weekend trip planning',
      weekOffset: 2,
      day: 4,
      hour: 18,
      durationMinutes: 30,
    },
    { calendarId: 'cal-work', title: 'Perf reviews due', weekOffset: 2, day: 5, allDay: true }
  );

  const events: Event[] = seeds.map((s, i) => {
    const dayDate = addDays(weekStart, (s.weekOffset ?? 0) * 7 + s.day);
    const start = s.allDay
      ? startOfDay(dayDate)
      : setMinutes(setHours(startOfDay(dayDate), s.hour ?? 9), s.minute ?? 0);
    const end = s.allDay ? addDays(startOfDay(dayDate), 1) : addMinutes(start, s.durationMinutes ?? 60);
    return {
      id: `evt-mock-${i + 1}`,
      calendarId: s.calendarId,
      title: s.title,
      description: s.description ?? null,
      location: s.location ?? null,
      start: start.toISOString(),
      end: end.toISOString(),
      allDay: s.allDay ?? false,
      recurrenceRule: null,
      attendees: s.attendees ?? [],
      conferencing: s.meet ? { provider: 'meet', url: 'https://meet.google.com/mock-demo' } : null,
      status: s.status ?? 'confirmed',
      visibility: 'default',
      reminderMinutes: s.reminders ?? [10],
    };
  });

  return { calendars, events };
}

function getStore(): MockStore {
  store ??= seed();
  return store;
}

export const calendarMock = {
  listCalendars(): Calendar[] {
    return getStore().calendars.map((c) => ({ ...c }));
  },

  updateCalendar(id: string, patch: Partial<Pick<Calendar, 'isVisible' | 'color'>>): Calendar {
    const cal = getStore().calendars.find((c) => c.id === id);
    if (!cal) throw new Error(`Unknown calendar: ${id}`);
    Object.assign(cal, patch);
    return { ...cal };
  },

  listEvents(fromIso: string, toIso: string, calendarIds?: string[]): Event[] {
    const from = new Date(fromIso).getTime();
    const to = new Date(toIso).getTime();
    return getStore()
      .events.filter((e) => new Date(e.start).getTime() < to && new Date(e.end).getTime() > from)
      .filter((e) => !calendarIds?.length || calendarIds.includes(e.calendarId))
      .map((e) => ({ ...e }))
      .sort((a, b) => a.start.localeCompare(b.start));
  },

  createEvent(input: EventInput): Event {
    const attendees = (input.attendeeEmails ?? []).map((email) =>
      attendee(email, null, 'needs_action')
    );
    if (attendees.length > 0) {
      attendees.unshift(attendee(MOCK_SELF_EMAIL, 'You', 'accepted', true));
    }
    const event: Event = {
      id: `evt-local-${nextId++}`,
      calendarId: input.calendarId,
      title: input.title,
      description: input.description ?? null,
      location: input.location ?? null,
      start: input.start,
      end: input.end,
      allDay: input.allDay ?? false,
      recurrenceRule: input.recurrenceRule ?? null,
      attendees,
      conferencing: input.addConferencing
        ? { provider: 'meet', url: 'https://meet.google.com/new' }
        : null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: input.reminderMinutes ?? [10],
    };
    getStore().events.push(event);
    return { ...event };
  },

  updateEvent(id: string, patch: EventPatch): Event {
    const e = getStore().events.find((x) => x.id === id);
    if (!e) throw new Error(`Unknown event: ${id}`);
    if (patch.title !== undefined) e.title = patch.title;
    if (patch.description !== undefined) e.description = patch.description || null;
    if (patch.location !== undefined) e.location = patch.location || null;
    if (patch.start !== undefined) e.start = patch.start;
    if (patch.end !== undefined) e.end = patch.end;
    if (patch.allDay !== undefined) e.allDay = patch.allDay;
    if (patch.recurrenceRule !== undefined) e.recurrenceRule = patch.recurrenceRule || null;
    if (patch.attendeeEmails !== undefined) {
      const organizer = e.attendees.find((a) => a.organizer);
      e.attendees = patch.attendeeEmails.map(
        (email) => e.attendees.find((a) => a.email === email) ?? attendee(email, null, 'needs_action')
      );
      if (organizer && e.attendees.length > 0 && !e.attendees.some((a) => a.email === organizer.email)) {
        e.attendees.unshift(organizer);
      }
    }
    if (patch.reminderMinutes !== undefined) e.reminderMinutes = patch.reminderMinutes;
    return { ...e, attendees: e.attendees.map((a) => ({ ...a })) };
  },

  deleteEvent(id: string): void {
    const s = getStore();
    s.events = s.events.filter((e) => e.id !== id);
  },

  rsvp(id: string, response: RsvpStatus): Event {
    const e = getStore().events.find((x) => x.id === id);
    if (!e) throw new Error(`Unknown event: ${id}`);
    const self = e.attendees.find((a) => a.email === MOCK_SELF_EMAIL);
    if (self) self.response = response;
    else e.attendees.push(attendee(MOCK_SELF_EMAIL, 'You', response));
    return { ...e, attendees: e.attendees.map((a) => ({ ...a })) };
  },

  /** Free 9:00-18:00 weekday slots that do not collide with mock events. */
  availability(fromIso: string, toIso: string, durationMinutes: number): AvailabilitySlot[] {
    const from = new Date(fromIso);
    const to = new Date(toIso);
    const busy = getStore()
      .events.filter((e) => !e.allDay && e.status !== 'cancelled')
      .map((e) => ({ start: new Date(e.start).getTime(), end: new Date(e.end).getTime() }));

    const slots: AvailabilitySlot[] = [];
    const now = Date.now();
    const step = Math.max(durationMinutes, 60);
    for (let day = startOfDay(from); day <= to && slots.length < 40; day = addDays(day, 1)) {
      const dow = day.getDay();
      if (dow === 0 || dow === 6) continue; // business days only
      let added = 0;
      for (let m = 9 * 60; m + durationMinutes <= 18 * 60 && added < 4; m += step) {
        const slotStart = addMinutes(day, m);
        const slotEnd = addMinutes(slotStart, durationMinutes);
        if (slotStart.getTime() < now) continue;
        if (slotEnd.getTime() > to.getTime()) break;
        const clash = busy.some((b) => slotStart.getTime() < b.end && slotEnd.getTime() > b.start);
        if (clash) continue;
        slots.push({ start: slotStart.toISOString(), end: slotEnd.toISOString() });
        added += 1;
      }
    }
    return slots;
  },
};
