import '@testing-library/jest-dom/vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const openExternal = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('@/lib/wails', () => ({ desktop: { OpenExternal: openExternal }, isDesktop: true }));

const exitDemo = vi.hoisted(() => vi.fn());
const DEFAULT_CONFIG = {
  webUrl: 'https://web.example',
  authBaseUrl: 'https://web.example/api/auth',
  features: { billing: true, google: false, microsoft: false, ai: false, push: false },
};
const serverState = vi.hoisted(() => ({ config: null as unknown, demoMode: false }));
vi.mock('@/lib/server-config', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/server-config')>()),
  useServerConfig: () => ({ config: serverState.config, demoMode: serverState.demoMode, clear: vi.fn(), exitDemo }),
}));

const signOut = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('@/lib/auth', () => ({ signOut, clearStoredToken: vi.fn() }));
vi.mock('@/lib/offline', () => ({ clearOfflineState: vi.fn(async () => {}) }));

import { PaywallView } from './PaywallView';

// Tests mutate serverState; reset it so no test depends on another's order.
beforeEach(() => {
  vi.clearAllMocks();
  serverState.config = DEFAULT_CONFIG;
  serverState.demoMode = false;
});

describe('PaywallView', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended'],
    ['none', 'Subscribe to keep using Calendium'],
    ['canceled', 'Your subscription has ended'],
    ['past_due', 'Payment failed'],
    ['paused', 'Your subscription is paused'],
  ] as const)('renders %s copy with no purchase UI', (reason, title) => {
    render(<PaywallView reason={reason} />);
    expect(screen.getByRole('heading', { name: title })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Subscribe/ })).not.toBeInTheDocument();
  });

  it('opens <webUrl>/settings in the system browser', async () => {
    render(<PaywallView reason="trial_ended" />);
    await userEvent.click(screen.getByRole('button', { name: /Open billing in your browser/ }));
    expect(openExternal).toHaveBeenCalledWith('https://web.example/settings?tab=billing');
  });

  it('falls back to the auth origin when webUrl is empty', async () => {
    serverState.config = { webUrl: '', authBaseUrl: 'https://auth.example/api/auth', features: { billing: true } };
    render(<PaywallView reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: /Open billing in your browser/ }));
    expect(openExternal).toHaveBeenCalledWith('https://auth.example/settings?tab=billing');
  });

  it('disables the billing button when no web origin is known', () => {
    serverState.config = { webUrl: '', authBaseUrl: '', features: { billing: true } };
    render(<PaywallView reason="none" />);
    expect(screen.getByRole('button', { name: /Open billing in your browser/ })).toBeDisabled();
  });

  it('exits the demo instead of signing out in demo mode', async () => {
    serverState.demoMode = true;
    render(<PaywallView reason="trial_ended" />);
    await userEvent.click(screen.getByRole('button', { name: /Sign out/ }));
    expect(exitDemo).toHaveBeenCalled();
    expect(signOut).not.toHaveBeenCalled();
  });

  it('signs out', async () => {
    render(<PaywallView reason="canceled" />);
    await userEvent.click(screen.getByRole('button', { name: /Sign out/ }));
    expect(signOut).toHaveBeenCalled();
  });
});
