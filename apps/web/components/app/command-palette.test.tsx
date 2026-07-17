import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Event, Thread } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { CommandPalette } from '@/components/app/command-palette';
import { onMailCommand, takePendingMailCommand, type MailCommand } from '@/lib/mail-utils';

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
  fetchSearchMock.mockResolvedValue({ threads: [], events: [] });
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
