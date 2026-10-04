import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const downloadExportMock = vi.fn();
const getSettingsMock = vi.fn();
const updateSettingsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    downloadExport: (...args: unknown[]) => downloadExportMock(...args),
    getSettings: (...args: unknown[]) => getSettingsMock(...args),
    updateSettings: (...args: unknown[]) => updateSettingsMock(...args),
  }),
}));

const saveBlobMock = vi.fn();
vi.mock('@/lib/account-data', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/account-data')>();
  return { ...actual, saveBlob: (...args: unknown[]) => saveBlobMock(...args) };
});

const listAccountsMock = vi.fn(async () => ({ data: [], error: null }));
const deleteUserMock = vi.fn();
const sessionState = { data: { user: { email: 'me@example.com', name: 'Me' } }, isPending: false };
vi.mock('@/lib/auth-client', () => ({
  useSession: () => sessionState,
  authClient: {
    useSession: () => sessionState,
    listAccounts: () => listAccountsMock(),
    deleteUser: (...args: unknown[]) => deleteUserMock(...args),
  },
}));

const demo = vi.hoisted(() => ({ DEMO_MODE: false }));
vi.mock('@/lib/demo', () => demo);

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastInfo = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    info: (...args: unknown[]) => toastInfo(...args),
  },
}));

vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: vi.fn(), push: vi.fn() }) }));
vi.mock('@/lib/sign-out', () => ({ performSignOut: vi.fn() }));

import { AccountSection } from './account-section';
import { BackgroundAiCard } from './background-ai-card';

function renderWithQuery(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const SETTINGS = { timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true };

beforeEach(() => {
  vi.clearAllMocks();
  demo.DEMO_MODE = false;
  getSettingsMock.mockResolvedValue(SETTINGS);
  updateSettingsMock.mockImplementation(async (s: typeof SETTINGS) => s);
});

describe('AccountSection', () => {
  it('shows the signed-in identity and both actions', async () => {
    renderWithQuery(<AccountSection />);
    expect(await screen.findByText(/me@example.com/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Download my data' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete account' })).toBeInTheDocument();
  });

  it('renders the sign-in/password slot in place of its own identity card', () => {
    renderWithQuery(
      <AccountSection>
        <div>password-slot</div>
      </AccountSection>
    );
    expect(screen.getByText('password-slot')).toBeInTheDocument();
    expect(screen.queryByText(/me@example.com/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Download my data' })).toBeInTheDocument();
  });

  it('download calls downloadExport and hands the blob to the browser', async () => {
    const blob = new Blob(['PK'], { type: 'application/zip' });
    downloadExportMock.mockResolvedValue(blob);
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await waitFor(() => expect(downloadExportMock).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(saveBlobMock).toHaveBeenCalledWith(blob, expect.stringMatching(/^calendium-export-\d{4}-\d{2}-\d{2}\.zip$/))
    );
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('export_throttled shows the retry time', async () => {
    const err = new ApiRequestError(409, 'export_throttled', 'later');
    err.retryAfterSeconds = 1800;
    downloadExportMock.mockRejectedValue(err);
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('You exported your data recently. Try again in 30 minutes.')
    );
    expect(saveBlobMock).not.toHaveBeenCalled();
  });

  it('other export failures show a generic retry toast', async () => {
    downloadExportMock.mockRejectedValue(new ApiRequestError(500, 'internal', 'boom'));
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Could not prepare your export. Try again.'));
  });

  it('opens the delete dialog', async () => {
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Delete account' }));
    expect(await screen.findByRole('dialog')).toHaveTextContent(/delete your account/i);
  });

  it('demo mode renders both buttons but only toasts — no export, no delete dialog', async () => {
    demo.DEMO_MODE = true;
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await user.click(screen.getByRole('button', { name: 'Delete account' }));
    expect(toastInfo).toHaveBeenCalledTimes(2);
    expect(toastInfo).toHaveBeenCalledWith('Not available in demo');
    expect(downloadExportMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(listAccountsMock).not.toHaveBeenCalled();
  });
});

describe('BackgroundAiCard', () => {
  it('reflects the stored switch and PUTs the whole document with aiBackground flipped', async () => {
    const user = userEvent.setup();
    renderWithQuery(<BackgroundAiCard />);
    const toggle = await screen.findByRole('switch', { name: 'Background AI processing' });
    await waitFor(() => expect(toggle).toBeChecked());
    await user.click(toggle);
    await waitFor(() => expect(updateSettingsMock).toHaveBeenCalledWith({ ...SETTINGS, aiBackground: false }));
    await waitFor(() => expect(toggle).not.toBeChecked());
  });

  it('shows the stored off state', async () => {
    getSettingsMock.mockResolvedValue({ ...SETTINGS, aiBackground: false });
    renderWithQuery(<BackgroundAiCard />);
    const toggle = await screen.findByRole('switch', { name: 'Background AI processing' });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).not.toBeChecked();
  });
});
