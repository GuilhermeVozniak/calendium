import * as React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ThemeProvider, useTheme } from '@/components/theme-provider';

const STORAGE_KEY = 'calendium-theme';

/** Builds a `matchMedia` stub whose `matches` and change-listener are controllable. */
function mockMatchMedia(initialMatches: boolean) {
  let matches = initialMatches;
  const listeners = new Set<(event: { matches: boolean }) => void>();
  const mql = {
    get matches() {
      return matches;
    },
    media: '(prefers-color-scheme: dark)',
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn((_event: string, handler: (event: { matches: boolean }) => void) => {
      listeners.add(handler);
    }),
    removeEventListener: vi.fn((_event: string, handler: (event: { matches: boolean }) => void) => {
      listeners.delete(handler);
    }),
    dispatchEvent: vi.fn(),
  };
  window.matchMedia = vi.fn().mockReturnValue(mql) as unknown as typeof window.matchMedia;
  return {
    setMatches(next: boolean) {
      matches = next;
      for (const listener of listeners) listener({ matches: next });
    },
    listenerCount: () => listeners.size,
  };
}

function Consumer() {
  const { theme, resolvedTheme, setTheme } = useTheme();
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="resolved">{resolvedTheme}</span>
      <button onClick={() => setTheme('light')}>set-light</button>
      <button onClick={() => setTheme('dark')}>set-dark</button>
      <button onClick={() => setTheme('system')}>set-system</button>
    </div>
  );
}

function ThrowingConsumer() {
  useTheme();
  return null;
}

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.classList.remove('dark');
  document.documentElement.style.colorScheme = '';
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useTheme', () => {
  it('throws when used outside a ThemeProvider', () => {
    // Suppress the expected React error boundary console noise.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() => render(<ThrowingConsumer />)).toThrow(
      'useTheme must be used within a <ThemeProvider>'
    );
    spy.mockRestore();
  });
});

describe('ThemeProvider — system detection', () => {
  it('resolves to light and does not add the dark class when the system prefers light', async () => {
    mockMatchMedia(false);
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('theme')).toHaveTextContent('system'));
    expect(screen.getByTestId('resolved')).toHaveTextContent('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(document.documentElement.style.colorScheme).toBe('light');
  });

  it('resolves to dark and adds the dark class when the system prefers dark', async () => {
    mockMatchMedia(true);
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('dark'));
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe('dark');
  });

  it('reacts to a live system theme change while theme=system', async () => {
    const media = mockMatchMedia(false);
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('light'));
    expect(media.listenerCount()).toBeGreaterThan(0);

    media.setMatches(true);
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('dark'));
  });

  it('stops listening for system changes once the user picks an explicit theme', async () => {
    const media = mockMatchMedia(false);
    const user = userEvent.setup();
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('light'));

    await user.click(screen.getByText('set-dark'));
    await waitFor(() => expect(screen.getByTestId('theme')).toHaveTextContent('dark'));
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');

    // A system-preference flip no longer moves the resolved theme.
    media.setMatches(true);
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');
  });
});

describe('ThemeProvider — persistence', () => {
  it('hydrates the stored preference from localStorage on mount', async () => {
    mockMatchMedia(false);
    window.localStorage.setItem(STORAGE_KEY, 'dark');
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('theme')).toHaveTextContent('dark'));
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');
  });

  it('ignores a corrupt stored value and falls back to system', async () => {
    mockMatchMedia(false);
    window.localStorage.setItem(STORAGE_KEY, 'purple');
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('theme')).toHaveTextContent('system'));
  });

  it('persists an explicit choice to localStorage and applies it', async () => {
    mockMatchMedia(false);
    const user = userEvent.setup();
    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('light'));

    await user.click(screen.getByText('set-dark'));
    await waitFor(() => expect(window.localStorage.getItem(STORAGE_KEY)).toBe('dark'));
    expect(screen.getByTestId('theme')).toHaveTextContent('dark');
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    await user.click(screen.getByText('set-light'));
    await waitFor(() => expect(window.localStorage.getItem(STORAGE_KEY)).toBe('light'));
    expect(document.documentElement.classList.contains('dark')).toBe(false);

    await user.click(screen.getByText('set-system'));
    await waitFor(() => expect(window.localStorage.getItem(STORAGE_KEY)).toBe('system'));
    expect(screen.getByTestId('theme')).toHaveTextContent('system');
  });

  it('still applies the theme for the session when localStorage throws', async () => {
    mockMatchMedia(false);
    const user = userEvent.setup();
    const getItemSpy = vi
      .spyOn(window.localStorage.__proto__, 'getItem')
      .mockImplementation(() => {
        throw new Error('private mode');
      });
    const setItemSpy = vi
      .spyOn(window.localStorage.__proto__, 'setItem')
      .mockImplementation(() => {
        throw new Error('private mode');
      });

    render(
      <ThemeProvider>
        <Consumer />
      </ThemeProvider>
    );
    await waitFor(() => expect(screen.getByTestId('theme')).toHaveTextContent('system'));

    await user.click(screen.getByText('set-dark'));
    await waitFor(() => expect(screen.getByTestId('resolved')).toHaveTextContent('dark'));
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    getItemSpy.mockRestore();
    setItemSpy.mockRestore();
  });
});
