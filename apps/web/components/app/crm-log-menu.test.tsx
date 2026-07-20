import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Message } from '@calendium/shared';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MessageCrmMenu } from '@/components/app/crm-log-menu';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

let crmQueryResult: unknown;
const logCrmEmailMock = vi.fn();
vi.mock('@/lib/use-crm', () => ({
  useCrmContext: () => crmQueryResult,
  logCrmEmail: (...args: unknown[]) => logCrmEmailMock(...args),
}));

const HUBSPOT_CONTEXT = { vendor: 'hubspot', contact: null, deals: [] };

const MESSAGE: Message = {
  id: 'msg_1',
  threadId: 'thr_1',
  from: { name: 'Daniel Cho', email: 'daniel.cho@northwind.com' },
  to: [{ name: 'Me', email: 'me@calendium.app' }],
  cc: [],
  bcc: [],
  bodyText: 'Attached the redlines.',
  bodyHtml: null,
  snippet: 'Attached the redlines.',
  sentAt: '2026-07-18T09:30:00Z',
  openedAt: null,
  attachments: [],
  reactions: [],
} as unknown as Message;

beforeEach(() => {
  vi.clearAllMocks();
  logCrmEmailMock.mockResolvedValue(undefined);
  crmQueryResult = { data: [HUBSPOT_CONTEXT], isLoading: false, isError: false };
});

// Radix's focus scope restores focus via a 0ms setTimeout when an open menu
// unmounts. Unmount NOW and flush that timer while this file's jsdom realm is
// still alive — otherwise it fires during the next test file in the worker
// and surfaces as an unhandled cross-realm dispatchEvent TypeError.
afterEach(async () => {
  cleanup();
  await new Promise((resolve) => setTimeout(resolve, 20));
});

describe('MessageCrmMenu', () => {
  it('renders nothing when the user has no connected HubSpot', () => {
    crmQueryResult = { data: [], isLoading: false, isError: false };
    render(
      <MessageCrmMenu
        message={MESSAGE}
        subject="Renewal terms"
        contactEmail="daniel.cho@northwind.com"
        direction="inbound"
      />
    );
    expect(screen.queryByRole('button', { name: /message actions/i })).not.toBeInTheDocument();
  });

  it('renders nothing without a contact email to log against', () => {
    render(
      <MessageCrmMenu message={MESSAGE} subject="s" contactEmail={null} direction="inbound" />
    );
    expect(screen.queryByRole('button', { name: /message actions/i })).not.toBeInTheDocument();
  });

  it('logs the message to HubSpot only on an explicit menu click, then confirms with a toast', async () => {
    const user = userEvent.setup();
    render(
      <MessageCrmMenu
        message={MESSAGE}
        subject="Renewal terms"
        contactEmail="daniel.cho@northwind.com"
        direction="inbound"
      />
    );

    // Nothing is logged on render — logging is never automatic.
    expect(logCrmEmailMock).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: /message actions/i }));
    await user.click(await screen.findByText('Log to HubSpot'));

    expect(logCrmEmailMock).toHaveBeenCalledTimes(1);
    expect(logCrmEmailMock).toHaveBeenCalledWith({
      contactEmail: 'daniel.cho@northwind.com',
      subject: 'Renewal terms',
      bodyText: 'Attached the redlines.',
      sentAt: '2026-07-18T09:30:00Z',
      direction: 'inbound',
    });
    expect(toastSuccess).toHaveBeenCalledWith('Logged to HubSpot');
  });

  it('shows an error toast when logging fails', async () => {
    logCrmEmailMock.mockRejectedValueOnce(new Error('boom'));
    const user = userEvent.setup();
    render(
      <MessageCrmMenu
        message={MESSAGE}
        subject="s"
        contactEmail="daniel.cho@northwind.com"
        direction="outbound"
      />
    );

    await user.click(screen.getByRole('button', { name: /message actions/i }));
    await user.click(await screen.findByText('Log to HubSpot'));

    expect(toastError).toHaveBeenCalledWith('Could not log to HubSpot.');
    expect(toastSuccess).not.toHaveBeenCalled();
  });
});
