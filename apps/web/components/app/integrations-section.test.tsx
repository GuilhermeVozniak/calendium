import type { IntegrationConnection } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listIntegrationsMock = vi.fn();
const connectIntegrationMock = vi.fn();
const disconnectIntegrationMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listIntegrations: (...args: unknown[]) => listIntegrationsMock(...args),
    connectIntegration: (...args: unknown[]) => connectIntegrationMock(...args),
    disconnectIntegration: (...args: unknown[]) => disconnectIntegrationMock(...args),
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

import { IntegrationsSection } from './integrations-section';

const ALL_CAPS = { todoist: true, hubspot: true, maps: false, weather: false };

const TODOIST_CONN: IntegrationConnection = {
  id: 'conn1',
  vendor: 'todoist',
  externalAccount: 'person@example.com',
  status: 'active',
  lastError: null,
  createdAt: '2026-07-19T12:00:00Z',
};

function renderSection(capabilities = ALL_CAPS) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <IntegrationsSection capabilities={capabilities} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listIntegrationsMock.mockResolvedValue([]);
});

describe('IntegrationsSection', () => {
  it('renders a connect card per advertised vendor', async () => {
    renderSection();
    expect(await screen.findByText('Todoist')).toBeInTheDocument();
    expect(screen.getByText('HubSpot')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect Todoist' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect HubSpot' })).toBeInTheDocument();
  });

  it('hides vendors the server does not advertise, and renders nothing when none are', async () => {
    const { container } = renderSection({ todoist: true, hubspot: false, maps: false, weather: false });
    expect(await screen.findByText('Todoist')).toBeInTheDocument();
    expect(screen.queryByText('HubSpot')).not.toBeInTheDocument();

    const none = renderSection({ todoist: false, hubspot: false, maps: false, weather: false });
    expect(none.container).toBeEmptyDOMElement();
    expect(container).not.toBeEmptyDOMElement();
  });

  it('renders nothing while capabilities are still loading (undefined)', () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <IntegrationsSection capabilities={undefined} />
      </QueryClientProvider>
    );
    expect(container).toBeEmptyDOMElement();
    expect(listIntegrationsMock).not.toHaveBeenCalled();
  });

  it('shows connected state with the vendor account label and a disconnect button', async () => {
    listIntegrationsMock.mockResolvedValue([TODOIST_CONN]);
    renderSection();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('person@example.com')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disconnect Todoist' })).toBeInTheDocument();
    // The other vendor still offers connect.
    expect(screen.getByRole('button', { name: 'Connect HubSpot' })).toBeInTheDocument();
  });

  it('surfaces an error connection with its lastError', async () => {
    listIntegrationsMock.mockResolvedValue([
      { ...TODOIST_CONN, status: 'error', lastError: 'token revoked upstream' },
    ]);
    renderSection();
    expect(await screen.findByText('Error')).toBeInTheDocument();
    expect(screen.getByText('token revoked upstream')).toBeInTheDocument();
  });

  it('connect opens the vendor auth URL returned by the API', async () => {
    connectIntegrationMock.mockResolvedValue({ url: 'https://todoist.com/oauth/authorize?state=x' });
    const assign = vi.fn();
    const original = window.location;
    Object.defineProperty(window, 'location', {
      value: { ...original, assign, origin: 'http://localhost:3000' },
      writable: true,
    });
    try {
      const user = userEvent.setup();
      renderSection();
      await user.click(await screen.findByRole('button', { name: 'Connect Todoist' }));
      await waitFor(() =>
        expect(connectIntegrationMock).toHaveBeenCalledWith(
          'todoist',
          'http://localhost:3000/settings?tab=integrations'
        )
      );
      await waitFor(() =>
        expect(assign).toHaveBeenCalledWith('https://todoist.com/oauth/authorize?state=x')
      );
    } finally {
      Object.defineProperty(window, 'location', { value: original, writable: true });
    }
  });

  it('disconnect asks for confirmation before revoking', async () => {
    listIntegrationsMock.mockResolvedValue([TODOIST_CONN]);
    disconnectIntegrationMock.mockResolvedValue(undefined);
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    try {
      const user = userEvent.setup();
      renderSection();
      const btn = await screen.findByRole('button', { name: 'Disconnect Todoist' });

      await user.click(btn);
      expect(confirmSpy).toHaveBeenCalled();
      expect(disconnectIntegrationMock).not.toHaveBeenCalled();

      confirmSpy.mockReturnValue(true);
      await user.click(btn);
      await waitFor(() => expect(disconnectIntegrationMock).toHaveBeenCalledWith('conn1'));
      await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Integration disconnected'));
    } finally {
      confirmSpy.mockRestore();
    }
  });

  it('surfaces a toast when the connect flow cannot start', async () => {
    connectIntegrationMock.mockRejectedValue(new Error('offline'));
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: 'Connect HubSpot' }));
    await waitFor(() => expect(toastError).toHaveBeenCalled());
  });
});
