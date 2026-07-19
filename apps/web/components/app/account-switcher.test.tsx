import type { ConnectedAccount } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
}));

import { AccountSwitcher } from '@/components/app/account-switcher';
import { MOD_KEY } from '@/lib/shortcuts';
import { ACTIVE_ACCOUNT_STORAGE_KEY, ActiveAccountProvider } from '@/lib/use-accounts';

function makeAccount(id: string, email: string): ConnectedAccount {
  return {
    id,
    provider: 'google',
    email,
    status: 'active',
    scopes: [],
    vipSenders: [],
    signatureHtml: '',
    autoBcc: [],
    lastSyncedAt: null,
    createdAt: new Date().toISOString(),
  };
}

const ACCOUNTS = [
  makeAccount('acc1', 'ada@calendium.app'),
  makeAccount('acc2', 'work@acme.com'),
];

function renderSwitcher() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ActiveAccountProvider>
        <AccountSwitcher />
      </ActiveAccountProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  fetchAccountsMock.mockResolvedValue(ACCOUNTS);
});

describe('AccountSwitcher', () => {
  it('renders nothing while no account is connected', async () => {
    fetchAccountsMock.mockResolvedValue([]);
    const { container } = renderSwitcher();
    // Give the accounts query a tick to resolve — still nothing to show.
    await Promise.resolve();
    expect(container).toBeEmptyDOMElement();
  });

  it('lists every account with its shortcut badge', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await user.click(await screen.findByRole('button', { name: /all accounts/i }));
    expect(await screen.findByText('ada@calendium.app')).toBeInTheDocument();
    expect(screen.getByText('work@acme.com')).toBeInTheDocument();
    expect(screen.getByText(`${MOD_KEY}1`)).toBeInTheDocument();
    expect(screen.getByText(`${MOD_KEY}2`)).toBeInTheDocument();
    expect(screen.getByText(`${MOD_KEY}0`)).toBeInTheDocument();
  });

  it('clicking an account switches to it and persists the selection', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await user.click(await screen.findByRole('button', { name: /all accounts/i }));
    await user.click(await screen.findByText('work@acme.com'));
    // Trigger now shows the active account.
    expect(await screen.findByRole('button', { name: /work@acme.com/i })).toBeInTheDocument();
    expect(window.localStorage.getItem(ACTIVE_ACCOUNT_STORAGE_KEY)).toBe('acc2');
  });

  it('restores a persisted selection truthfully', async () => {
    window.localStorage.setItem(ACTIVE_ACCOUNT_STORAGE_KEY, 'acc2');
    renderSwitcher();
    expect(await screen.findByRole('button', { name: /work@acme.com/i })).toBeInTheDocument();
  });

  it('falls back to all accounts when the persisted account no longer exists', async () => {
    window.localStorage.setItem(ACTIVE_ACCOUNT_STORAGE_KEY, 'acc_gone');
    renderSwitcher();
    expect(await screen.findByRole('button', { name: /all accounts/i })).toBeInTheDocument();
    await screen.findByRole('button', { name: /all accounts/i });
    expect(window.localStorage.getItem(ACTIVE_ACCOUNT_STORAGE_KEY)).toBeNull();
  });
});
