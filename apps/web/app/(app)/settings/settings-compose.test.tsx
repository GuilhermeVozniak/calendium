import type { ConnectedAccount } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks — mock only the API boundary (@/lib/api) so the real settings-data.ts
// wrappers (setSignatureApi/setAutoBccApi/fetchAccounts/...) run for real,
// matching the demo-mode fallback contract under test below. This mirrors
// compose.test.tsx's own '@/lib/api' mock.
// ---------------------------------------------------------------------------

const listAccountsMock = vi.fn();
const connectAccountMock = vi.fn();
const disconnectAccountMock = vi.fn();
const setVipSendersMock = vi.fn();
const setSignatureMock = vi.fn();
const setAutoBccMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listAccounts: (...args: unknown[]) => listAccountsMock(...args),
    connectAccount: (...args: unknown[]) => connectAccountMock(...args),
    disconnectAccount: (...args: unknown[]) => disconnectAccountMock(...args),
    setVipSenders: (...args: unknown[]) => setVipSendersMock(...args),
    setSignature: (...args: unknown[]) => setSignatureMock(...args),
    setAutoBcc: (...args: unknown[]) => setAutoBccMock(...args),
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

import { AccountsSection } from './page';

const ACCOUNT: ConnectedAccount = {
  id: 'acc1',
  provider: 'google',
  email: 'me@example.com',
  status: 'active',
  scopes: [],
  vipSenders: [],
  signatureHtml: '<p>Thanks,<br>Me</p>',
  autoBcc: ['archive@example.com'],
  lastSyncedAt: null,
  createdAt: new Date().toISOString(),
};

function renderSection() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountsSection />
    </QueryClientProvider>
  );
}

async function openComposeDialog() {
  const user = userEvent.setup();
  renderSection();
  const composeButton = await screen.findByRole('button', { name: 'Compose' });
  await user.click(composeButton);
  return { user, dialog: await screen.findByRole('dialog') };
}

/** The auto-BCC chip row's draft input, queried via its "Bcc" row label
 * (mirrors compose.test.tsx's getToInput helper for the same ChipsRow). */
function getBccInput(dialog: HTMLElement): HTMLElement {
  const label = within(dialog).getByText('Bcc');
  return within(label.parentElement as HTMLElement).getByRole('textbox');
}

beforeEach(() => {
  vi.clearAllMocks();
  listAccountsMock.mockResolvedValue([ACCOUNT]);
});

describe('Compose settings dialog (signature + auto-BCC)', () => {
  it('renders the current signature and auto-BCC list from account data', async () => {
    const { dialog } = await openComposeDialog();
    expect(within(dialog).getByText('archive@example.com')).toBeInTheDocument();
    const editor = within(dialog).getByRole('textbox', { name: 'Signature' });
    expect(editor).toHaveTextContent('Thanks,');
    expect(editor).toHaveTextContent('Me');
  });

  it('saves an edited signature by calling setSignature with the edited HTML', async () => {
    setSignatureMock.mockResolvedValue({ ...ACCOUNT, signatureHtml: '<p>Thanks,<br>Me Extra</p>' });
    const { user, dialog } = await openComposeDialog();

    const editor = within(dialog).getByRole('textbox', { name: 'Signature' });
    await user.click(editor);
    await user.type(editor, ' Extra');

    await user.click(within(dialog).getByRole('button', { name: 'Save signature' }));

    await waitFor(() => expect(setSignatureMock).toHaveBeenCalledTimes(1));
    const [accountId, html] = setSignatureMock.mock.calls[0]!;
    expect(accountId).toBe('acc1');
    expect(html).toContain('Extra');
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Signature saved'));
  });

  it('saves an edited auto-BCC list by calling setAutoBcc with the edited addresses', async () => {
    setAutoBccMock.mockResolvedValue({
      ...ACCOUNT,
      autoBcc: ['archive@example.com', 'new@example.com'],
    });
    const { user, dialog } = await openComposeDialog();

    const bccInput = getBccInput(dialog);
    await user.type(bccInput, 'new@example.com{Enter}');
    expect(within(dialog).getByText('new@example.com')).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Save auto-BCC' }));

    await waitFor(() =>
      expect(setAutoBccMock).toHaveBeenCalledWith('acc1', [
        'archive@example.com',
        'new@example.com',
      ])
    );
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Auto-BCC saved'));
  });

  it('rejects an invalid auto-BCC email inline, keeping the typed text and not creating a chip', async () => {
    const { user, dialog } = await openComposeDialog();

    const bccInput = getBccInput(dialog);
    await user.type(bccInput, 'not-an-email{Enter}');

    expect(bccInput).toHaveValue('not-an-email');
    expect(setAutoBccMock).not.toHaveBeenCalled();
  });

  it('surfaces a 400 auto-BCC validation failure inline instead of a toast', async () => {
    const { ApiRequestError } = await import('@calendium/shared');
    setAutoBccMock.mockRejectedValue(
      new ApiRequestError(400, 'validation_failed', 'invalid auto-BCC address "bob@x"')
    );
    const { user, dialog } = await openComposeDialog();

    await user.click(within(dialog).getByRole('button', { name: 'Save auto-BCC' }));

    expect(await within(dialog).findByText('invalid auto-BCC address "bob@x"')).toBeInTheDocument();
    expect(toastError).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// settings-data.ts demo-mode round trip — verifies setSignatureApi/
// setAutoBccApi fall back to the in-memory mock store (and that the store
// actually persists the change) when the real API is unreachable and
// NEXT_PUBLIC_DEMO_MODE is explicitly on, per the honesty policy in
// lib/demo.ts (mock data served ONLY in explicit demo mode).
// ---------------------------------------------------------------------------

describe('settings-data — demo mode round-trip for signature/auto-BCC', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('persists a saved signature and auto-BCC list into the mock store', async () => {
    // The test file's static `import { AccountsSection } from './page'` above
    // already evaluated (and cached) settings-data.ts/demo.ts with DEMO_MODE
    // false, before this test ever runs — reset the module registry first so
    // the dynamic import below re-evaluates against the freshly stubbed env.
    vi.resetModules();
    vi.stubEnv('NEXT_PUBLIC_DEMO_MODE', 'true');
    setSignatureMock.mockRejectedValueOnce(new Error('offline'));
    setAutoBccMock.mockRejectedValueOnce(new Error('offline'));

    const { setSignatureApi, setAutoBccApi } = await import('@/lib/settings-data');
    const { settingsMock } = await import('@/lib/settings-mock');
    const accountId = settingsMock.listAccounts()[0]!.id;

    const sigUpdated = await setSignatureApi(accountId, '<p>New sig</p>');
    expect(sigUpdated.signatureHtml).toBe('<p>New sig</p>');
    expect(
      settingsMock.listAccounts().find((a) => a.id === accountId)?.signatureHtml
    ).toBe('<p>New sig</p>');

    const bccUpdated = await setAutoBccApi(accountId, ['new@example.com']);
    expect(bccUpdated.autoBcc).toEqual(['new@example.com']);
    expect(settingsMock.listAccounts().find((a) => a.id === accountId)?.autoBcc).toEqual([
      'new@example.com',
    ]);
  });

  it('does not fall back to the mock store outside demo mode — the real failure propagates', async () => {
    vi.resetModules();
    vi.stubEnv('NEXT_PUBLIC_DEMO_MODE', undefined);
    setSignatureMock.mockRejectedValueOnce(new Error('offline'));

    const { setSignatureApi } = await import('@/lib/settings-data');
    await expect(setSignatureApi('acc1', '<p>New sig</p>')).rejects.toThrow('offline');
  });
});
