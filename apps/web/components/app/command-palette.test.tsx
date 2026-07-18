import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { CalendarSet, Event, EventTemplate, Thread } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { CommandPalette } from '@/components/app/command-palette';
import { onCalendarCommand, takePendingCalendarCommand, type CalendarCommand } from '@/lib/calendar-commands';
import { onMailCommand, takePendingMailCommand, type MailCommand } from '@/lib/mail-utils';
import { resetShortcutHints } from '@/lib/shortcut-hints';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const pushMock = vi.fn();
const replaceMock = vi.fn();
let currentPathname = '/mail';
vi.mock('next/navigation', () => ({
  usePathname: () => currentPathname,
  useRouter: () => ({ push: pushMock, replace: replaceMock }),
}));

const openComposeMock = vi.fn();
vi.mock('@/components/app/compose', () => ({
  useCompose: () => ({ openCompose: openComposeMock }),
}));

let themeState = { resolvedTheme: 'light' as 'light' | 'dark', setTheme: vi.fn() };
vi.mock('@/components/theme-provider', () => ({
  useTheme: () => themeState,
}));

const signOutMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  signOut: (...args: unknown[]) => signOutMock(...args),
}));

const fetchSearchMock = vi.fn();
vi.mock('@/lib/search-data', () => ({
  fetchSearch: (q: string) => fetchSearchMock(q),
}));

const fetchEventTemplatesMock = vi.fn();
vi.mock('@/lib/template-data', () => ({
  fetchEventTemplates: () => fetchEventTemplatesMock(),
}));

const fetchCalendarSetsMock = vi.fn();
vi.mock('@/lib/set-data', () => ({
  fetchCalendarSets: () => fetchCalendarSetsMock(),
}));

// The palette's "Ask AI" item only needs the toggle callback — the sidebar's
// own open/close and Q&A behavior has its own dedicated tests
// (components/ai/ask-sidebar.test.tsx).
const openAskSidebarMock = vi.fn();
vi.mock('@/components/ai/ask-sidebar', () => ({
  useAskSidebar: () => ({ openSidebar: openAskSidebarMock, toggle: vi.fn(), close: vi.fn(), open: false }),
}));

const dispatchAiEditCommandMock = vi.fn();
vi.mock('@/components/compose/ai-edit-menu', () => ({
  dispatchAiEditCommand: (...args: unknown[]) => dispatchAiEditCommandMock(...args),
}));

let aiEnabled = false;
vi.mock('@/lib/use-instance', () => ({
  useInstance: () => ({ data: { features: { ai: aiEnabled } } }),
}));

const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    message: (...args: unknown[]) => toastMessage(...args),
  },
}));

/** The palette fetches live search results, so it needs a QueryClient. */
function renderPalette() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <CommandPalette />
    </QueryClientProvider>
  );
}

/** Opens the palette via its mod+k shortcut and waits for the list to render. */
async function openPalette() {
  fireEvent.keyDown(document.body, { key: 'k', ctrlKey: true, metaKey: true });
  await screen.findByPlaceholderText('Type a command or search…');
}

beforeEach(() => {
  vi.clearAllMocks();
  currentPathname = '/mail';
  themeState = { resolvedTheme: 'light', setTheme: vi.fn() };
  aiEnabled = false;
  fetchSearchMock.mockResolvedValue({ threads: [], events: [] });
  fetchEventTemplatesMock.mockResolvedValue([]);
  fetchCalendarSetsMock.mockResolvedValue([]);
  resetShortcutHints();
});

describe('CommandPalette — AI commands', () => {
  it('does not show the AI group when the server has no AI', async () => {
    aiEnabled = false;
    renderPalette();
    await openPalette();
    expect(screen.queryByText('Ask AI')).not.toBeInTheDocument();
  });

  it('opens the Ask AI sidebar and closes the palette', async () => {
    aiEnabled = true;
    renderPalette();
    await openPalette();
    await userEvent.click(screen.getByText('Ask AI'));
    expect(openAskSidebarMock).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument()
    );
  });

  it('dispatches the propose-event mail command for "Create event with AI"', async () => {
    aiEnabled = true;
    renderPalette();
    await openPalette();
    const events: MailCommand[] = [];
    const off = onMailCommand((c) => events.push(c));
    await userEvent.click(screen.getByText('Create event with AI'));
    off();
    expect(events).toEqual(['propose-event']);
  });

  it('dispatches an AI edit command for "AI: Improve draft"', async () => {
    aiEnabled = true;
    renderPalette();
    await openPalette();
    await userEvent.click(screen.getByText('AI: Improve draft'));
    expect(dispatchAiEditCommandMock).toHaveBeenCalledWith('improve');
  });
});

describe('CommandPalette — opening', () => {
  it('is closed until the mod+k shortcut is pressed', () => {
    renderPalette();
    expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument();
  });

  it('opens on mod+k and lists commands from every group', async () => {
    renderPalette();
    await openPalette();
    expect(screen.getByText('Compose')).toBeInTheDocument();
    expect(screen.getByText('Search mail')).toBeInTheDocument();
    expect(screen.getByText('Archive conversation')).toBeInTheDocument();
    expect(screen.getByText('Go to Inbox')).toBeInTheDocument();
    expect(screen.getByText('Go to Calendar')).toBeInTheDocument();
    expect(screen.getByText('Toggle theme')).toBeInTheDocument();
    expect(screen.getByText('Sign out')).toBeInTheDocument();
  });

  it('shows the H hint on Snooze and the ⇧H hint on the reminder item', async () => {
    renderPalette();
    await openPalette();
    const snoozeItem = screen.getByText('Snooze…').closest('[cmdk-item]');
    expect(snoozeItem).not.toBeNull();
    expect(snoozeItem?.textContent).toContain('H');

    const reminderItem = screen.getByText('Set follow-up reminder…').closest('[cmdk-item]');
    expect(reminderItem).not.toBeNull();
    expect(reminderItem?.textContent).toContain('⇧');
    expect(reminderItem?.textContent).toContain('H');
  });

  it('toggles closed on a second mod+k', async () => {
    renderPalette();
    await openPalette();
    fireEvent.keyDown(document.body, { key: 'k', ctrlKey: true, metaKey: true });
    await waitFor(() =>
      expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument()
    );
  });
});

describe('CommandPalette — mail commands', () => {
  it('dispatches "Compose" via openCompose and closes the palette', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Compose'));
    expect(openComposeMock).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument()
    );
  });

  it('dispatches a mail command directly (as a DOM event) when already on /mail', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Archive conversation'));
    expect(received).toEqual(['archive']);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('queues the mail command and navigates to /mail when on another route', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Set follow-up reminder…'));
    expect(pushMock).toHaveBeenCalledWith('/mail');
    expect(takePendingMailCommand()).toBe('reminder');
  });

  it('dispatches "Undo last action" via the undo mail command', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Undo last action'));
    expect(received).toEqual(['undo']);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('dispatches "Label conversation…" via the label mail command', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Label conversation…'));
    expect(received).toEqual(['label']);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('dispatches "Toggle calendar peek" via the toggle-calendar-peek mail command', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Toggle calendar peek'));
    expect(received).toEqual(['toggle-calendar-peek']);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('queues "Toggle calendar peek" and navigates to /mail when on another route', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Toggle calendar peek'));
    expect(pushMock).toHaveBeenCalledWith('/mail');
    expect(takePendingMailCommand()).toBe('toggle-calendar-peek');
  });
});

describe('CommandPalette — navigation', () => {
  it('pushes the target route for a Navigate command', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Go to Calendar'));
    expect(pushMock).toHaveBeenCalledWith('/calendar');
  });

  it('pushes /settings for "Go to Settings"', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Go to Settings'));
    expect(pushMock).toHaveBeenCalledWith('/settings');
  });
});

describe('CommandPalette — appearance', () => {
  it('sets dark theme explicitly via "Dark theme"', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Dark theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('dark');
  });

  it('sets system theme via "System theme"', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('System theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('system');
  });

  it('"Toggle theme" flips from light to dark', async () => {
    themeState.resolvedTheme = 'light';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Toggle theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('dark');
  });

  it('"Toggle theme" flips from dark to light', async () => {
    themeState.resolvedTheme = 'dark';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Toggle theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('light');
  });
});

describe('CommandPalette — live search', () => {
  const thread = {
    id: 't1',
    subject: 'Quarterly report',
    participants: [{ name: 'Ada', email: 'ada@x.com' }],
  } as unknown as Thread;
  const event = {
    id: 'e1',
    title: 'Quarterly review',
    start: '2026-07-20T10:00:00.000Z',
  } as unknown as Event;

  it('shows live results for a query and opens a thread', async () => {
    fetchSearchMock.mockResolvedValue({ threads: [thread], events: [] });
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.type(screen.getByPlaceholderText('Type a command or search…'), 'quarterly');
    expect(await screen.findByText('Quarterly report')).toBeInTheDocument();
    await waitFor(() => expect(fetchSearchMock).toHaveBeenCalledWith('quarterly'));
    await user.click(screen.getByText('Quarterly report'));
    expect(pushMock).toHaveBeenCalledWith('/mail?t=t1');
  });

  it('navigates to the calendar day for an event result', async () => {
    fetchSearchMock.mockResolvedValue({ threads: [], events: [event] });
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.type(screen.getByPlaceholderText('Type a command or search…'), 'quarterly');
    expect(await screen.findByText('Quarterly review')).toBeInTheDocument();
    await user.click(screen.getByText('Quarterly review'));
    expect(pushMock).toHaveBeenCalledWith(
      `/calendar?d=${encodeURIComponent('2026-07-20T10:00:00.000Z')}`
    );
  });

  it('does not fetch for queries shorter than two characters', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.type(screen.getByPlaceholderText('Type a command or search…'), 'q');
    // Debounce window (200 ms) must elapse without a fetch.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(fetchSearchMock).not.toHaveBeenCalled();
  });
});

describe('CommandPalette — templates', () => {
  const TEMPLATE: EventTemplate = {
    id: 'tpl-1',
    name: '1:1',
    title: '1:1 with teammate',
    description: '',
    location: '',
    durationMinutes: 30,
    allDay: false,
    calendarId: null,
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [],
    recurrenceRule: null,
    usageCount: 1,
  };

  // The old "Templates" group's "Use template: <name>" rows deep-linked via
  // a bare router.push('/calendar?template=<id>') - dead on /calendar (the
  // page's mount-once effect never re-fires) and a URL landmine (the param
  // stuck around, replayed on reload). It was deleted outright: the
  // "Calendar" group's bus-based "New event from template: <name>" rows
  // (see the 'calendar templates & sets' describe below) already cover the
  // feature cross-route via runCalendarCommand/dispatchCalendarCommand.
  it('never renders the old "Use template:" rows, even when templates exist', async () => {
    fetchEventTemplatesMock.mockResolvedValue([TEMPLATE]);
    renderPalette();
    await openPalette();
    await screen.findByText('New event from template: 1:1');
    expect(screen.queryByText(/^Use template:/)).not.toBeInTheDocument();
    expect(pushMock).not.toHaveBeenCalledWith(expect.stringContaining('?template='));
  });
});

describe('CommandPalette — calendar actions', () => {
  it('renders the calendar group with shortcut hints', async () => {
    renderPalette();
    await openPalette();

    const today = screen.getByText('Go to today').closest('[cmdk-item]');
    expect(today?.textContent).toContain('T');

    const day = screen.getByText('Day view').closest('[cmdk-item]');
    expect(day?.textContent).toContain('D');
    const week = screen.getByText('Week view').closest('[cmdk-item]');
    expect(week?.textContent).toContain('W');
    const month = screen.getByText('Month view').closest('[cmdk-item]');
    expect(month?.textContent).toContain('M');
    const quarter = screen.getByText('Quarter view').closest('[cmdk-item]');
    expect(quarter?.textContent).toContain('Q');
    const year = screen.getByText('Year view').closest('[cmdk-item]');
    expect(year?.textContent).toContain('Y');
    const ticker = screen.getByText('Ticker view').closest('[cmdk-item]');
    expect(ticker?.textContent).toContain('A');

    const newEvent = screen.getByText('New event').closest('[cmdk-item]');
    expect(newEvent?.textContent).toContain('C');
    const share = screen.getByText('Share availability').closest('[cmdk-item]');
    expect(share?.textContent).toContain('S');
  });

  it('dispatches a calendar command directly (as a DOM event) when already on /calendar', async () => {
    currentPathname = '/calendar';
    const received: CalendarCommand[] = [];
    const unsubscribe = onCalendarCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Go to today'));
    expect(received).toEqual([{ type: 'today' }]);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('queues the "Week view" command and navigates to /calendar when on another route', async () => {
    currentPathname = '/mail';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Week view'));
    expect(pushMock).toHaveBeenCalledWith('/calendar');
    expect(takePendingCalendarCommand()).toEqual({ type: 'view', view: 'week' });
  });

  it('dispatches "New event" via the new-event calendar command', async () => {
    currentPathname = '/calendar';
    const received: CalendarCommand[] = [];
    const unsubscribe = onCalendarCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('New event'));
    expect(received).toEqual([{ type: 'new-event' }]);
    unsubscribe();
  });

  it('dispatches "Share availability" via the share-availability calendar command', async () => {
    currentPathname = '/calendar';
    const received: CalendarCommand[] = [];
    const unsubscribe = onCalendarCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Share availability'));
    expect(received).toEqual([{ type: 'share-availability' }]);
    unsubscribe();
  });
});

describe('CommandPalette — calendar templates & sets', () => {
  const TEMPLATE: EventTemplate = {
    id: 'tpl-1',
    name: '1:1',
    title: '1:1 with teammate',
    description: '',
    location: '',
    durationMinutes: 30,
    allDay: false,
    calendarId: null,
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [],
    recurrenceRule: null,
    usageCount: 1,
  };
  const SET: CalendarSet = { id: 'set-1', name: 'Work', calendarIds: ['cal-1'], position: 0 };

  it('does not show template/set rows when there are none', async () => {
    renderPalette();
    await openPalette();
    expect(screen.queryByText(/New event from template:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Apply calendar set:/)).not.toBeInTheDocument();
  });

  it('lists a "New event from template" row per template and dispatches new-from-template', async () => {
    fetchEventTemplatesMock.mockResolvedValue([TEMPLATE]);
    currentPathname = '/calendar';
    const received: CalendarCommand[] = [];
    const unsubscribe = onCalendarCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();

    const item = await screen.findByText('New event from template: 1:1');
    await user.click(item);

    expect(received).toEqual([{ type: 'new-from-template', templateId: 'tpl-1' }]);
    unsubscribe();
  });

  it('lists an "Apply calendar set" row per set and dispatches toggle-set', async () => {
    fetchCalendarSetsMock.mockResolvedValue([SET]);
    currentPathname = '/calendar';
    const received: CalendarCommand[] = [];
    const unsubscribe = onCalendarCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();

    const item = await screen.findByText('Apply calendar set: Work');
    await user.click(item);

    expect(received).toEqual([{ type: 'toggle-set', setId: 'set-1' }]);
    unsubscribe();
  });
});

describe('CommandPalette — account', () => {
  it('signs out and redirects to /signin', async () => {
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Sign out'));
    await waitFor(() => expect(signOutMock).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/signin'));
  });
});

describe('CommandPalette — shortcut teaching', () => {
  it('teaches Archive shortcut when Archive conversation item is clicked', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();

    await user.click(screen.getByText('Archive conversation'));

    expect(toastMessage).toHaveBeenCalledWith('Tip: press E to Archive');
    expect(received).toEqual(['archive']);

    // Second click should NOT re-toast (dedup)
    toastMessage.mockClear();
    unsubscribe();
  });

  it('teaches Undo shortcut when Undo last action item is clicked', async () => {
    currentPathname = '/mail';
    const received: MailCommand[] = [];
    const unsubscribe = onMailCommand((command) => received.push(command));
    const user = userEvent.setup();
    renderPalette();
    await openPalette();

    await user.click(screen.getByText('Undo last action'));

    expect(toastMessage).toHaveBeenCalledWith('Tip: press Z to Undo');
    expect(received).toEqual(['undo']);

    unsubscribe();
  });

  it('teaches the today shortcut when "Go to today" is clicked', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Go to today'));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press T to jump to today');
  });

  it('teaches the view shortcut when "Week view" is clicked', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Week view'));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press W to switch to Week view');
  });

  it('teaches the new-event shortcut when "New event" is clicked', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('New event'));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press C to create a new event');
  });

  it('teaches the share-availability shortcut when "Share availability" is clicked', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    renderPalette();
    await openPalette();
    await user.click(screen.getByText('Share availability'));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press S to share availability');
  });
});
