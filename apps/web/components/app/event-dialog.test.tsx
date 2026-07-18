import type { Attendee, Calendar, ConnectedAccount, Event, EventTemplate } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { EventDialog, type EventDialogProps } from '@/components/app/event-dialog';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const createEventApiMock = vi.fn();
const updateEventApiMock = vi.fn();
const deleteEventApiMock = vi.fn();
const sendRsvpApiMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  createEventApi: (...args: unknown[]) => createEventApiMock(...args),
  updateEventApi: (...args: unknown[]) => updateEventApiMock(...args),
  deleteEventApi: (...args: unknown[]) => deleteEventApiMock(...args),
  sendRsvpApi: (...args: unknown[]) => sendRsvpApiMock(...args),
}));

// A fixed instant so the create-flow's default start/end are deterministic
// (real nextHalfHour() depends on `new Date()`).
const DEFAULT_START = new Date(2026, 6, 7, 10, 0, 0, 0);
const nextHalfHourMock = vi.fn(() => DEFAULT_START);
vi.mock('@/lib/quick-add', () => ({
  nextHalfHour: () => nextHalfHourMock(),
}));

const useSelfEmailsMock = vi.fn();
vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => useSelfEmailsMock(),
}));

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
}));

const fetchEventTemplatesMock = vi.fn();
const createEventTemplateApiMock = vi.fn();
const recordTemplateUsageMock = vi.fn();
vi.mock('@/lib/template-data', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/template-data')>();
  return {
    ...actual,
    fetchEventTemplates: (...args: unknown[]) => fetchEventTemplatesMock(...args),
    createEventTemplateApi: (...args: unknown[]) => createEventTemplateApiMock(...args),
    recordTemplateUsage: (...args: unknown[]) => recordTemplateUsageMock(...args),
  };
});

const conflictWarningMock = vi.fn((_props: unknown) => null);
vi.mock('@/components/app/calendar/conflict-warning', () => ({
  ConflictWarning: (props: unknown) => conflictWarningMock(props),
}));

// find-a-time.tsx has its own unit tests (find-a-time.test.tsx) covering the
// grid/proposal-form/proposals-list internals in isolation; here we only
// verify EventDialog wires the right component with the right props/callback
// under the right conditions, via lightweight stand-ins.
const findATimeGridMock = vi.fn((_props: unknown) => (
  <div data-testid="find-a-time-grid" />
));
const proposeTimeFormMock = vi.fn((_props: unknown) => (
  <div data-testid="propose-time-form" />
));
const proposalsListMock = vi.fn((props: { onAccepted: () => void }) => (
  <div data-testid="proposals-list">
    <button type="button" onClick={props.onAccepted}>
      Mock accept
    </button>
  </div>
));
vi.mock('@/components/app/find-a-time', () => ({
  FindATimeGrid: (props: unknown) => findATimeGridMock(props),
  ProposeTimeForm: (props: unknown) => proposeTimeFormMock(props),
  ProposalsList: (props: { onAccepted: () => void }) => proposalsListMock(props),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

const CAL_WORK: Calendar = {
  id: 'cal-work',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

const CAL_PERSONAL: Calendar = {
  id: 'cal-personal',
  accountId: 'acc1',
  name: 'Personal',
  color: '#22c55e',
  timeZone: 'UTC',
  isPrimary: false,
  isVisible: true,
  canWrite: true,
};

const CAL_MS: Calendar = {
  id: 'cal-ms',
  accountId: 'acc2',
  name: 'Outlook',
  color: '#0ea5e9',
  timeZone: 'UTC',
  isPrimary: false,
  isVisible: true,
  canWrite: true,
};

function attendee(overrides: Partial<Attendee>): Attendee {
  return {
    email: 'x@example.com',
    name: null,
    response: 'needs_action',
    organizer: false,
    optional: false,
    ...overrides,
  };
}

function renderDialog(props: Partial<EventDialogProps> = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onOpenChange = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <EventDialog
        open
        onOpenChange={onOpenChange}
        calendars={[CAL_WORK, CAL_PERSONAL]}
        event={null}
        defaults={null}
        {...props}
      />
    </QueryClientProvider>
  );
  return { ...utils, onOpenChange };
}

function fmt(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const TEMPLATE_1: EventTemplate = {
  id: 'tpl-1',
  name: '1:1',
  title: '1:1 with teammate',
  description: 'Weekly sync',
  location: 'Zoom',
  durationMinutes: 45,
  allDay: false,
  calendarId: 'cal-personal',
  attendeeEmails: ['teammate@example.com'],
  addConferencing: true,
  reminderMinutes: [10, 60],
  recurrenceRule: 'FREQ=WEEKLY',
  usageCount: 3,
};

const ACCOUNT_GOOGLE: ConnectedAccount = {
  id: 'acc1',
  provider: 'google',
  email: 'me@example.com',
  status: 'active',
  scopes: [],
  vipSenders: [],
  signatureHtml: '',
  autoBcc: [],
  lastSyncedAt: null,
  createdAt: new Date(2026, 0, 1).toISOString(),
};

const ACCOUNT_MICROSOFT: ConnectedAccount = {
  id: 'acc2',
  provider: 'microsoft',
  email: 'me@outlook.com',
  status: 'active',
  scopes: [],
  vipSenders: [],
  signatureHtml: '',
  autoBcc: [],
  lastSyncedAt: null,
  createdAt: new Date(2026, 0, 1).toISOString(),
};

beforeEach(() => {
  vi.clearAllMocks();
  useSelfEmailsMock.mockReturnValue(new Set<string>(['me@example.com']));
  fetchEventTemplatesMock.mockResolvedValue([TEMPLATE_1]);
  createEventTemplateApiMock.mockResolvedValue({ ...TEMPLATE_1, id: 'tpl-new' });
  createEventApiMock.mockResolvedValue({ id: 'ev-new' });
  updateEventApiMock.mockResolvedValue({ id: 'ev1' });
  deleteEventApiMock.mockResolvedValue(undefined);
  sendRsvpApiMock.mockResolvedValue({ id: 'ev1' });
  fetchAccountsMock.mockResolvedValue([ACCOUNT_GOOGLE, ACCOUNT_MICROSOFT]);
});

// ---------------------------------------------------------------------------
// Title field
// ---------------------------------------------------------------------------

describe('EventDialog — title', () => {
  it('edits the title field', async () => {
    const user = userEvent.setup();
    renderDialog();
    const titleInput = await screen.findByLabelText('Event title');
    await user.type(titleInput, 'Team sync');
    expect(titleInput).toHaveValue('Team sync');
  });
});

// ---------------------------------------------------------------------------
// ConflictWarning wiring (Task 12 integration — mounted by the controller)
// ---------------------------------------------------------------------------

describe('EventDialog — conflict warning wiring', () => {
  it('mounts ConflictWarning with the parsed slot in create mode (no ignoreEventId)', async () => {
    renderDialog();
    await screen.findByLabelText('Event title');
    expect(conflictWarningMock).toHaveBeenCalled();
    const props = conflictWarningMock.mock.calls.at(-1)?.[0] as Record<string, unknown>;
    expect(props.allDay).toBe(false);
    expect(props.start).toBeInstanceOf(Date);
    expect(props.end).toBeInstanceOf(Date);
    expect(props.ignoreEventId).toBeUndefined();
  });

  it('passes the edited event id as ignoreEventId in edit mode', async () => {
    const event: Event = {
      id: 'ev-conflict',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [],
      conferencing: null,
      reminderMinutes: [],
      status: 'confirmed',
      visibility: 'default',
    };
    renderDialog({ event });
    await screen.findByLabelText('Event title');
    const props = conflictWarningMock.mock.calls.at(-1)?.[0] as Record<string, unknown>;
    expect(props.ignoreEventId).toBe('ev-conflict');
  });
});

// ---------------------------------------------------------------------------
// Start/end time fields — exercises the unexported parseInput/formatInput
// helpers indirectly (event-dialog.tsx does not export them).
// ---------------------------------------------------------------------------

describe('EventDialog — start/end time fields', () => {
  it('prefills start/end from the next-half-hour default with a 30-minute span', async () => {
    renderDialog();
    const startInput = (await screen.findByLabelText('Start')) as HTMLInputElement;
    const endInput = screen.getByLabelText('End') as HTMLInputElement;
    expect(startInput.value).toBe(fmt(DEFAULT_START));
    expect(endInput.value).toBe(fmt(new Date(DEFAULT_START.getTime() + 30 * 60_000)));
  });

  it('shifts the end time to preserve duration when the start time changes', async () => {
    renderDialog();
    const startInput = (await screen.findByLabelText('Start')) as HTMLInputElement;
    const endInput = screen.getByLabelText('End') as HTMLInputElement;

    const newStart = new Date(2026, 6, 7, 14, 0, 0, 0);
    fireEvent.change(startInput, { target: { value: fmt(newStart) } });

    expect(startInput.value).toBe(fmt(newStart));
    // Original span was 30 minutes; it is preserved across the shift.
    expect(endInput.value).toBe(fmt(new Date(newStart.getTime() + 30 * 60_000)));
  });

  it('round-trips an existing event\'s start/end through the edit flow', async () => {
    const event: Event = {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: 'Daily sync',
      location: 'Room 1',
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [attendee({ email: 'me@example.com', organizer: true })],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [10, 60],
    };
    renderDialog({ event });

    const startInput = (await screen.findByLabelText('Start')) as HTMLInputElement;
    const endInput = screen.getByLabelText('End') as HTMLInputElement;
    expect(startInput.value).toBe(fmt(new Date(event.start)));
    expect(endInput.value).toBe(fmt(new Date(event.end)));

    // Saving without touching the fields sends back the same ISO instants —
    // proof the format -> parse round trip is lossless for timed events.
    await userEvent.setup().click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(updateEventApiMock).toHaveBeenCalledTimes(1));
    const [, patch] = updateEventApiMock.mock.calls[0]!;
    expect(patch.start).toBe(event.start);
    expect(patch.end).toBe(event.end);
  });
});

// ---------------------------------------------------------------------------
// All-day toggle
// ---------------------------------------------------------------------------

describe('EventDialog — all-day toggle', () => {
  it('switches the start/end inputs from datetime-local to date and back', async () => {
    const user = userEvent.setup();
    renderDialog();
    const startInput = await screen.findByLabelText('Start');
    expect(startInput).toHaveAttribute('type', 'datetime-local');

    const allDaySwitch = screen.getByRole('switch', { name: /all day/i });
    await user.click(allDaySwitch);
    expect(startInput).toHaveAttribute('type', 'date');
    expect((startInput as HTMLInputElement).value).toBe(
      `${DEFAULT_START.getFullYear()}-${String(DEFAULT_START.getMonth() + 1).padStart(2, '0')}-${String(DEFAULT_START.getDate()).padStart(2, '0')}`
    );

    await user.click(allDaySwitch);
    expect(startInput).toHaveAttribute('type', 'datetime-local');
  });
});

// ---------------------------------------------------------------------------
// Attendees
// ---------------------------------------------------------------------------

describe('EventDialog — attendees', () => {
  it('adds a valid attendee email and clears the draft input', async () => {
    const user = userEvent.setup();
    renderDialog();
    const attendeeInput = await screen.findByLabelText('Add attendee');
    await user.type(attendeeInput, 'guest@example.com{Enter}');
    expect(await screen.findByText('guest@example.com')).toBeInTheDocument();
    expect(attendeeInput).toHaveValue('');
  });

  it('rejects an invalid attendee email with a toast and does not add a chip', async () => {
    const user = userEvent.setup();
    renderDialog();
    const attendeeInput = await screen.findByLabelText('Add attendee');
    await user.type(attendeeInput, 'not-an-email{Enter}');
    expect(toastError).toHaveBeenCalledWith('"not-an-email" is not a valid email address');
    expect(screen.queryByText('not-an-email')).not.toBeInTheDocument();
  });

  it('removes an attendee via its remove button', async () => {
    const user = userEvent.setup();
    renderDialog();
    const attendeeInput = await screen.findByLabelText('Add attendee');
    await user.type(attendeeInput, 'guest@example.com{Enter}');
    await screen.findByText('guest@example.com');
    await user.click(screen.getByRole('button', { name: 'Remove guest@example.com' }));
    expect(screen.queryByText('guest@example.com')).not.toBeInTheDocument();
  });

  it('removes the last attendee on Backspace when the draft is empty', async () => {
    const user = userEvent.setup();
    renderDialog();
    const attendeeInput = await screen.findByLabelText('Add attendee');
    await user.type(attendeeInput, 'guest@example.com{Enter}');
    await screen.findByText('guest@example.com');
    await user.click(attendeeInput);
    await user.keyboard('{Backspace}');
    expect(screen.queryByText('guest@example.com')).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Quick-add defaults prefill (location/reminderMinutes/attendeeEmails were
// already wired; recurrenceRule is the piece this suite adds coverage for).
// ---------------------------------------------------------------------------

describe('EventDialog — quick-add defaults prefill', () => {
  it('shows defaults.recurrenceRule in the recurrence field and pre-chips defaults.attendeeEmails', async () => {
    renderDialog({
      defaults: {
        recurrenceRule: 'FREQ=WEEKLY;BYDAY=FR',
        attendeeEmails: ['ana@acme.com'],
        location: 'Cafe',
      },
    });

    expect(await screen.findByText('Every week on FR')).toBeInTheDocument();
    expect(screen.getByText('ana@acme.com')).toBeInTheDocument();
    expect(screen.getByLabelText('Location')).toHaveValue('Cafe');
  });

  it('shows "Does not repeat" when no recurrence default is given', async () => {
    renderDialog();
    expect(await screen.findByText('Does not repeat')).toBeInTheDocument();
  });

  it('clears a prefilled recurrence via its remove button and omits it from the create payload', async () => {
    const user = userEvent.setup();
    renderDialog({ defaults: { recurrenceRule: 'FREQ=DAILY' } });
    await screen.findByText('Every day');

    await user.click(screen.getByRole('button', { name: 'Remove recurrence' }));
    expect(screen.queryByText('Every day')).not.toBeInTheDocument();
    expect(screen.getByText('Does not repeat')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    expect(createEventApiMock.mock.calls[0]![0].recurrenceRule).toBeUndefined();
  });

  it('includes the prefilled recurrenceRule in the create payload when left untouched', async () => {
    const user = userEvent.setup();
    renderDialog({ defaults: { recurrenceRule: 'FREQ=WEEKLY;BYDAY=FR' } });
    await screen.findByText('Every week on FR');

    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    expect(createEventApiMock.mock.calls[0]![0].recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=FR');
  });
});

// ---------------------------------------------------------------------------
// Edit-mode recurrence clear — the backend's EventPatch.RecurrenceRule is a
// *string where nil means "unchanged" and non-nil "" means "CLEAR"; JSON.stringify
// drops `undefined` keys entirely, so dismissing an existing recurrence must
// send an explicit empty string, not omit the field.
// ---------------------------------------------------------------------------

describe('EventDialog — edit-mode recurrence clear (tri-state PATCH)', () => {
  function recurringEvent(): Event {
    return {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: 'FREQ=DAILY',
      attendees: [],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [],
    };
  }

  it('sends recurrenceRule: "" in the PATCH when an existing recurrence is dismissed and saved', async () => {
    const user = userEvent.setup();
    renderDialog({ event: recurringEvent() });
    await screen.findByText('Every day');

    await user.click(screen.getByRole('button', { name: 'Remove recurrence' }));
    expect(screen.getByText('Does not repeat')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(updateEventApiMock).toHaveBeenCalledTimes(1));
    const [, patch] = updateEventApiMock.mock.calls[0]!;
    expect(patch.recurrenceRule).toBe('');
  });

  it('sends the unchanged recurrenceRule in the PATCH when left untouched', async () => {
    const user = userEvent.setup();
    renderDialog({ event: recurringEvent() });
    await screen.findByText('Every day');

    await user.click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(updateEventApiMock).toHaveBeenCalledTimes(1));
    const [, patch] = updateEventApiMock.mock.calls[0]!;
    expect(patch.recurrenceRule).toBe('FREQ=DAILY');
  });

  it('omits recurrenceRule from the PATCH when the event never had one', async () => {
    const user = userEvent.setup();
    const event = { ...recurringEvent(), recurrenceRule: null };
    renderDialog({ event });
    await screen.findByText('Does not repeat');

    await user.click(screen.getByRole('button', { name: /save changes/i }));
    await waitFor(() => expect(updateEventApiMock).toHaveBeenCalledTimes(1));
    const [, patch] = updateEventApiMock.mock.calls[0]!;
    expect(patch.recurrenceRule).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Reminders — exercises the unexported reminderLabel() helper indirectly.
// ---------------------------------------------------------------------------

describe('EventDialog — reminders', () => {
  it('shows the default 10-minute reminder using the expected label', async () => {
    renderDialog();
    expect(await screen.findByText('10 min before')).toBeInTheDocument();
  });

  it('adds a preset reminder from the select and labels it correctly', async () => {
    const user = userEvent.setup();
    renderDialog();
    await screen.findByText('10 min before');

    await user.click(screen.getByRole('combobox', { name: 'Add reminder' }));
    const option = await screen.findByRole('option', { name: '1 hour before' });
    await user.click(option);

    expect(await screen.findByText('1 hour before')).toBeInTheDocument();
  });

  it('removes a reminder via its remove button', async () => {
    const user = userEvent.setup();
    renderDialog();
    await screen.findByText('10 min before');
    await user.click(screen.getByRole('button', { name: 'Remove reminder 10 min before' }));
    expect(screen.queryByText('10 min before')).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Conferencing (Task 13 — add-conferencing switch on create, Join row on edit)
// ---------------------------------------------------------------------------

describe('EventDialog — conferencing', () => {
  it('shows an "Add video conferencing" switch in create mode', async () => {
    renderDialog();
    expect(await screen.findByLabelText('Add video conferencing')).toBeInTheDocument();
  });

  it('shows Google Meet helper text when the selected calendar belongs to a Google account', async () => {
    const user = userEvent.setup();
    renderDialog();
    const toggle = await screen.findByLabelText('Add video conferencing');
    await user.click(toggle);
    expect(await screen.findByText(/Google Meet link will be added/i)).toBeInTheDocument();
  });

  it('does not show helper text when accounts are still loading (undefined provider)', async () => {
    const user = userEvent.setup();
    fetchAccountsMock.mockImplementation(() => new Promise(() => {})); // Never resolves
    renderDialog();
    const toggle = await screen.findByLabelText('Add video conferencing');
    await user.click(toggle);
    expect(screen.queryByText(/Google Meet link will be added/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Teams meeting will be added/i)).not.toBeInTheDocument();
  });

  it('shows Teams helper text when the selected calendar belongs to a Microsoft account', async () => {
    const user = userEvent.setup();
    renderDialog({ calendars: [CAL_WORK, CAL_PERSONAL, CAL_MS], defaults: { calendarId: 'cal-ms' } });
    const toggle = await screen.findByLabelText('Add video conferencing');
    await user.click(toggle);
    expect(await screen.findByText(/Teams meeting will be added/i)).toBeInTheDocument();
  });

  it('submits addConferencing: true when the switch is toggled on and the event is created', async () => {
    const user = userEvent.setup();
    renderDialog();
    const toggle = await screen.findByLabelText('Add video conferencing');
    await user.click(toggle);
    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    expect(createEventApiMock.mock.calls[0]![0].addConferencing).toBe(true);
  });

  function baseEvent(overrides: Partial<Event> = {}): Event {
    return {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [],
      ...overrides,
    };
  }

  it('does not show the "Add video conferencing" switch in edit mode', async () => {
    renderDialog({ event: baseEvent() });
    await screen.findByLabelText('Event title');
    expect(screen.queryByLabelText('Add video conferencing')).not.toBeInTheDocument();
  });

  it('shows a Join row in edit mode when the event has a detected conference', async () => {
    const event = baseEvent({
      conferencing: { provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' },
    });
    renderDialog({ event });
    expect(await screen.findByRole('button', { name: /join meet/i })).toBeInTheDocument();
  });

  it('shows no Join row in edit mode when the event has no detected conference', async () => {
    renderDialog({ event: baseEvent() });
    await screen.findByLabelText('Event title');
    expect(screen.queryByRole('button', { name: /join/i })).not.toBeInTheDocument();
  });

  it('notes in edit mode that conferencing can only be added at creation time', async () => {
    renderDialog({ event: baseEvent() });
    await screen.findByLabelText('Event title');
    expect(screen.getByText(/only be added when creating an event/i)).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Save / create / delete flows
// ---------------------------------------------------------------------------

describe('EventDialog — save flow', () => {
  it('creates an event with the expected payload and closes on success', async () => {
    const user = userEvent.setup();
    const { onOpenChange } = renderDialog();
    const titleInput = await screen.findByLabelText('Event title');
    await user.type(titleInput, 'Team sync');

    await user.click(screen.getByRole('button', { name: /create event/i }));

    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    const input = createEventApiMock.mock.calls[0]![0];
    expect(input.title).toBe('Team sync');
    expect(input.calendarId).toBe('cal-work');
    expect(input.start).toBe(DEFAULT_START.toISOString());
    expect(input.end).toBe(new Date(DEFAULT_START.getTime() + 30 * 60_000).toISOString());
    expect(input.reminderMinutes).toEqual([10]);

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(toastSuccess).toHaveBeenCalledWith('Event created');
  });

  it('falls back to "(No title)" when the title is left blank', async () => {
    const user = userEvent.setup();
    renderDialog();
    await screen.findByLabelText('Event title');
    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    expect(createEventApiMock.mock.calls[0]![0].title).toBe('(No title)');
  });

  it('shows a delete button only in edit mode and calls deleteEventApi', async () => {
    const user = userEvent.setup();
    renderDialog();
    expect(screen.queryByRole('button', { name: /delete/i })).not.toBeInTheDocument();

    const event: Event = {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [10],
    };
    renderDialog({ event });
    await user.click(screen.getByRole('button', { name: /delete/i }));
    await waitFor(() => expect(deleteEventApiMock).toHaveBeenCalledWith('ev1'));
    expect(toastSuccess).toHaveBeenCalledWith('Event deleted');
  });

  it('surfaces a save failure via a toast without silently succeeding', async () => {
    createEventApiMock.mockRejectedValue(new Error('network down'));
    const user = userEvent.setup();
    const { onOpenChange } = renderDialog();
    await screen.findByLabelText('Event title');
    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Could not save the event'));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });
});

// ---------------------------------------------------------------------------
// RSVP (invite banner)
// ---------------------------------------------------------------------------

describe('EventDialog — RSVP', () => {
  it('shows the "Going?" banner when the current user is an invited attendee', async () => {
    const event: Event = {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'All hands',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [
        attendee({ email: 'organizer@example.com', organizer: true, name: 'Organizer' }),
        attendee({ email: 'me@example.com', response: 'needs_action' }),
      ],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [],
    };
    const user = userEvent.setup();
    renderDialog({ event });

    expect(await screen.findByText(/Going\?/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Yes' }));
    await waitFor(() => expect(sendRsvpApiMock).toHaveBeenCalledWith('ev1', 'accepted'));
    expect(toastSuccess).toHaveBeenCalledWith('RSVP sent');
  });
});

// ---------------------------------------------------------------------------
// Start from template (create mode only)
// ---------------------------------------------------------------------------

describe('EventDialog — start from template', () => {
  it('does not show the template select in edit mode', async () => {
    const event: Event = {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [],
    };
    renderDialog({ event });
    await screen.findByLabelText('Event title');
    expect(
      screen.queryByRole('combobox', { name: 'Start from template' })
    ).not.toBeInTheDocument();
  });

  it('prefills all fields from the selected template, anchored at the dialog\'s pending slot time', async () => {
    const user = userEvent.setup();
    const slotStart = new Date(2026, 6, 10, 15, 0, 0, 0);
    renderDialog({
      defaults: {
        start: slotStart.toISOString(),
        end: new Date(slotStart.getTime() + 30 * 60_000).toISOString(),
      },
    });

    const select = await screen.findByRole('combobox', { name: 'Start from template' });
    await user.click(select);
    await user.click(await screen.findByRole('option', { name: '1:1' }));

    await user.click(screen.getByRole('button', { name: /create event/i }));
    await waitFor(() => expect(createEventApiMock).toHaveBeenCalledTimes(1));
    const payload = createEventApiMock.mock.calls[0]![0];

    expect(payload.title).toBe('1:1 with teammate');
    expect(payload.location).toBe('Zoom');
    expect(payload.description).toBe('Weekly sync');
    expect(payload.start).toBe(slotStart.toISOString());
    expect(payload.end).toBe(new Date(slotStart.getTime() + 45 * 60_000).toISOString());
    expect(payload.attendeeEmails).toEqual(['teammate@example.com']);
    expect(payload.addConferencing).toBe(true);
    expect(payload.reminderMinutes).toEqual([10, 60]);
    expect(payload.recurrenceRule).toBe('FREQ=WEEKLY');
    expect(payload.calendarId).toBe('cal-personal');

    expect(recordTemplateUsageMock).toHaveBeenCalledWith('tpl-1');
  });
});

// ---------------------------------------------------------------------------
// Save as template
// ---------------------------------------------------------------------------

describe('EventDialog — save as template', () => {
  it('posts the current form as an EventTemplateInput with durationMinutes derived from start/end', async () => {
    const user = userEvent.setup();
    renderDialog();
    const titleInput = await screen.findByLabelText('Event title');
    await user.type(titleInput, 'Focus block');
    await user.type(screen.getByLabelText('Location'), 'Home office');

    await user.click(screen.getByRole('button', { name: /save as template/i }));

    await waitFor(() => expect(createEventTemplateApiMock).toHaveBeenCalledTimes(1));
    const input = createEventTemplateApiMock.mock.calls[0]![0];
    expect(input.name).toBe('Focus block');
    expect(input.title).toBe('Focus block');
    expect(input.location).toBe('Home office');
    expect(input.durationMinutes).toBe(30);
    expect(input.calendarId).toBe('cal-work');
    expect(toastSuccess).toHaveBeenCalledWith('Saved as template');
  });

  it('is available in edit mode too', async () => {
    const event: Event = {
      id: 'ev1',
      calendarId: 'cal-work',
      title: 'Standup',
      description: null,
      location: null,
      start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
      end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
      allDay: false,
      recurrenceRule: null,
      attendees: [],
      conferencing: null,
      status: 'confirmed',
      visibility: 'default',
      reminderMinutes: [],
    };
    renderDialog({ event });
    await screen.findByLabelText('Event title');
    expect(screen.getByRole('button', { name: /save as template/i })).toBeInTheDocument();
  });

  it('surfaces a save-as-template failure via a toast without silently succeeding', async () => {
    createEventTemplateApiMock.mockRejectedValue(new Error('network down'));
    const user = userEvent.setup();
    renderDialog();
    await screen.findByLabelText('Event title');
    await user.click(screen.getByRole('button', { name: /save as template/i }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Could not save the template')
    );
  });
});

// ---------------------------------------------------------------------------
// Find-a-Time + propose-new-time wiring (Task 15). find-a-time.tsx owns its
// own unit tests; these confirm EventDialog mounts the right piece with the
// right props for each of the three states the brief calls out.
// ---------------------------------------------------------------------------

function attendeeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'ev1',
    calendarId: 'cal-work',
    title: 'Planning',
    description: null,
    location: null,
    start: new Date(2026, 6, 8, 9, 0, 0, 0).toISOString(),
    end: new Date(2026, 6, 8, 9, 30, 0, 0).toISOString(),
    allDay: false,
    recurrenceRule: null,
    attendees: [
      attendee({ email: 'me@example.com', organizer: true }),
      attendee({ email: 'guest@example.com' }),
    ],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
    ...overrides,
  };
}

describe('EventDialog — Find-a-Time grid', () => {
  it('shows the grid once an attendee is added while creating an event', async () => {
    const user = userEvent.setup();
    renderDialog();
    expect(screen.queryByTestId('find-a-time-grid')).not.toBeInTheDocument();

    const attendeeInput = await screen.findByLabelText('Add attendee');
    await user.type(attendeeInput, 'guest@example.com{Enter}');

    expect(await screen.findByTestId('find-a-time-grid')).toBeInTheDocument();
    const props = findATimeGridMock.mock.calls.at(-1)?.[0] as Record<string, unknown>;
    expect(props.attendeeEmails).toEqual(['guest@example.com']);
    expect(props.durationMinutes).toBe(30);
  });

  it('shows the grid in edit mode when the current user organizes the event', async () => {
    renderDialog({ event: attendeeEvent() });
    expect(await screen.findByTestId('find-a-time-grid')).toBeInTheDocument();
  });

  it('does not show the grid for a non-organizer viewing the event (propose-time flow instead)', async () => {
    useSelfEmailsMock.mockReturnValue(new Set<string>(['guest@example.com']));
    renderDialog({ event: attendeeEvent() });
    await screen.findByLabelText('Event title');
    expect(screen.queryByTestId('find-a-time-grid')).not.toBeInTheDocument();
  });

  it('picking a slot on the grid sets the start/end fields', async () => {
    renderDialog({ event: attendeeEvent() });
    await screen.findByTestId('find-a-time-grid');
    const props = findATimeGridMock.mock.calls.at(-1)?.[0] as { onPick: (start: Date, end: Date) => void };
    const onPick = props.onPick;
    onPick(new Date(2026, 6, 9, 15, 0), new Date(2026, 6, 9, 15, 30));
    const startInput = (await screen.findByLabelText('Start')) as HTMLInputElement;
    expect(startInput.value).toBe(fmt(new Date(2026, 6, 9, 15, 0)));
  });
});

describe('EventDialog — propose-new-time (non-organizer view)', () => {
  it('shows the propose-time form instead of the grid or proposals list', async () => {
    useSelfEmailsMock.mockReturnValue(new Set<string>(['guest@example.com']));
    renderDialog({ event: attendeeEvent() });

    expect(await screen.findByTestId('propose-time-form')).toBeInTheDocument();
    expect(screen.queryByTestId('find-a-time-grid')).not.toBeInTheDocument();
    expect(screen.queryByTestId('proposals-list')).not.toBeInTheDocument();
    const props = proposeTimeFormMock.mock.calls.at(-1)?.[0] as Record<string, unknown>;
    expect(props.eventId).toBe('ev1');
  });
});

describe('EventDialog — pending proposals (organizer view)', () => {
  it('shows the proposals list for the organizer, and accepting invalidates events and closes the dialog', async () => {
    const user = userEvent.setup();
    const { onOpenChange } = renderDialog({ event: attendeeEvent() });

    expect(await screen.findByTestId('proposals-list')).toBeInTheDocument();
    const props = proposalsListMock.mock.calls.at(-1)?.[0] as Record<string, unknown>;
    expect(props.eventId).toBe('ev1');

    await user.click(screen.getByRole('button', { name: /mock accept/i }));

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(toastSuccess).toHaveBeenCalledWith('Proposal accepted');
  });

  it('does not show the proposals list while creating a new event', async () => {
    renderDialog();
    await screen.findByLabelText('Event title');
    expect(screen.queryByTestId('proposals-list')).not.toBeInTheDocument();
  });
});
