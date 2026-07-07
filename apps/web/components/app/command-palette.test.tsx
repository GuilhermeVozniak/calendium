import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

/** Opens the palette via its mod+k shortcut and waits for the list to render. */
async function openPalette() {
  fireEvent.keyDown(document.body, { key: 'k', ctrlKey: true, metaKey: true });
  await screen.findByPlaceholderText('Type a command or search…');
}

beforeEach(() => {
  vi.clearAllMocks();
  currentPathname = '/mail';
  themeState = { resolvedTheme: 'light', setTheme: vi.fn() };
});

describe('CommandPalette — opening', () => {
  it('is closed until the mod+k shortcut is pressed', () => {
    render(<CommandPalette />);
    expect(screen.queryByPlaceholderText('Type a command or search…')).not.toBeInTheDocument();
  });

  it('opens on mod+k and lists commands from every group', async () => {
    render(<CommandPalette />);
    await openPalette();
    expect(screen.getByText('Compose')).toBeInTheDocument();
    expect(screen.getByText('Search mail')).toBeInTheDocument();
    expect(screen.getByText('Archive conversation')).toBeInTheDocument();
    expect(screen.getByText('Go to Inbox')).toBeInTheDocument();
    expect(screen.getByText('Go to Calendar')).toBeInTheDocument();
    expect(screen.getByText('Toggle theme')).toBeInTheDocument();
    expect(screen.getByText('Sign out')).toBeInTheDocument();
  });

  it('toggles closed on a second mod+k', async () => {
    render(<CommandPalette />);
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
    render(<CommandPalette />);
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
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Archive conversation'));
    expect(received).toEqual(['archive']);
    expect(pushMock).not.toHaveBeenCalled();
    unsubscribe();
  });

  it('queues the mail command and navigates to /mail when on another route', async () => {
    currentPathname = '/calendar';
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Set follow-up reminder…'));
    expect(pushMock).toHaveBeenCalledWith('/mail');
    expect(takePendingMailCommand()).toBe('reminder');
  });
});

describe('CommandPalette — navigation', () => {
  it('pushes the target route for a Navigate command', async () => {
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Go to Calendar'));
    expect(pushMock).toHaveBeenCalledWith('/calendar');
  });

  it('pushes /settings for "Go to Settings"', async () => {
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Go to Settings'));
    expect(pushMock).toHaveBeenCalledWith('/settings');
  });
});

describe('CommandPalette — appearance', () => {
  it('sets dark theme explicitly via "Dark theme"', async () => {
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Dark theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('dark');
  });

  it('sets system theme via "System theme"', async () => {
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('System theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('system');
  });

  it('"Toggle theme" flips from light to dark', async () => {
    themeState.resolvedTheme = 'light';
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Toggle theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('dark');
  });

  it('"Toggle theme" flips from dark to light', async () => {
    themeState.resolvedTheme = 'dark';
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Toggle theme'));
    expect(themeState.setTheme).toHaveBeenCalledWith('light');
  });
});

describe('CommandPalette — account', () => {
  it('signs out and redirects to /signin', async () => {
    const user = userEvent.setup();
    render(<CommandPalette />);
    await openPalette();
    await user.click(screen.getByText('Sign out'));
    await waitFor(() => expect(signOutMock).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/signin'));
  });
});
