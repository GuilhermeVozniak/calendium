import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ContactSummary } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ContactPane } from '@/components/app/contact-pane';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock('@/lib/contact-utils', () => ({
  gravatarUrl: vi.fn().mockResolvedValue('https://www.gravatar.com/avatar/deadbeef?d=404'),
  faviconUrl: (domain: string) => `https://icons.duckduckgo.com/ip3/${domain}.ico`,
  companyFromDomain: (domain: string) => (domain === 'northwind.com' ? 'Northwind' : ''),
}));

const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock }),
}));

const openComposeMock = vi.fn();
vi.mock('@/components/app/compose', () => ({
  useCompose: () => ({ openCompose: openComposeMock }),
}));

let contactQueryResult: unknown;
vi.mock('@/lib/use-mail', () => ({
  useContact: () => contactQueryResult,
}));

// CRM context (M2.8): default "no CRM connected" so existing pane tests are
// unaffected; the CRM-specific tests below flip this to a HubSpot fixture.
let crmQueryResult: unknown;
vi.mock('@/lib/use-crm', () => ({
  useCrmContext: () => crmQueryResult,
  logCrmEmail: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const SUMMARY: ContactSummary = {
  email: 'daniel.cho@northwind.com',
  name: 'Daniel Cho',
  domain: 'northwind.com',
  threadCount: 3,
  messageCount: 7,
  lastMessageAt: new Date().toISOString(),
  recentThreads: [
    {
      id: 'thr_02',
      accountId: 'acc_1',
      subject: 'Renewal terms for FY27 — need your sign-off',
      snippet: 'legal cleared the redlines',
      participants: [{ name: 'Daniel Cho', email: 'daniel.cho@northwind.com' }],
      labelIds: ['inbox'],
      split: 'important',
      messageCount: 1,
      unread: false,
      starred: true,
      lastMessageAt: new Date().toISOString(),
      openedAt: null,
      snoozedUntil: null,
      remindAt: null,
      unsubscribeMailto: null,
      unsubscribeUrl: null,
      unsubscribeOneClick: false,
    },
  ],
};

function setResult(result: unknown) {
  contactQueryResult = result;
}

const CRM_CONTEXT = {
  vendor: 'hubspot' as const,
  contact: {
    id: '301',
    email: 'daniel.cho@northwind.com',
    name: 'Daniel Cho',
    company: 'Northwind',
    title: 'VP Procurement',
    phone: '',
    owner: '7',
    vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-1/301',
  },
  deals: [
    {
      id: '9001',
      name: 'FY27 Renewal',
      stage: 'Contract sent',
      amount: 48000,
      closeDate: '2026-08-01T00:00:00Z',
      vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-3/9001',
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  setResult({ data: undefined, isLoading: true, isError: false });
  crmQueryResult = { data: [], isLoading: false, isError: false };
});

describe('ContactPane', () => {
  it('renders the mocked contact summary — name, email, company, stats', () => {
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={vi.fn()} />);

    expect(screen.getByText('Daniel Cho')).toBeInTheDocument();
    expect(screen.getByText('daniel.cho@northwind.com')).toBeInTheDocument();
    expect(screen.getByText('Northwind')).toBeInTheDocument();
    expect(screen.getByText('3 conversations · 7 messages · last just now')).toBeInTheDocument();
    expect(screen.getByText('Renewal terms for FY27 — need your sign-off')).toBeInTheDocument();
  });

  it('clicking a recent thread routes to its real /mail?t= route', async () => {
    const user = userEvent.setup();
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={vi.fn()} />);

    await user.click(screen.getByText('Renewal terms for FY27 — need your sign-off'));

    expect(pushMock).toHaveBeenCalledWith('/mail?t=thr_02');
  });

  it('the compose quick action opens compose prefilled with the contact', async () => {
    const user = userEvent.setup();
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: /compose to/i }));

    expect(openComposeMock).toHaveBeenCalledWith({
      to: [{ name: 'Daniel Cho', email: 'daniel.cho@northwind.com' }],
    });
  });

  it('shows a loading state while the summary is in flight', () => {
    setResult({ data: undefined, isLoading: true, isError: false });
    render(<ContactPane email="x@example.com" onClose={vi.fn()} />);

    expect(screen.queryByText(/conversations/)).not.toBeInTheDocument();
  });

  it('shows an honest error state — never a fabricated summary — on failure', () => {
    setResult({ data: undefined, isLoading: false, isError: true });
    render(<ContactPane email="x@example.com" onClose={vi.fn()} />);

    expect(screen.getByText(/couldn.t load/i)).toBeInTheDocument();
  });

  it('shows an honest empty state when the contact has no shared history', () => {
    setResult({ data: null, isLoading: false, isError: false });
    render(<ContactPane email="x@example.com" onClose={vi.fn()} />);

    expect(screen.getByText(/no conversations/i)).toBeInTheDocument();
  });

  it('renders the CRM section — contact, deal with stage badge, HubSpot link — when a CRM is connected', () => {
    crmQueryResult = { data: [CRM_CONTEXT], isLoading: false, isError: false };
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={vi.fn()} />);

    expect(screen.getByText('HubSpot')).toBeInTheDocument();
    expect(screen.getByText('VP Procurement · Northwind')).toBeInTheDocument();
    expect(screen.getByText('FY27 Renewal')).toBeInTheDocument();
    expect(screen.getByText('Contract sent')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /open in hubspot/i });
    expect(link).toHaveAttribute(
      'href',
      'https://app.hubspot.com/contacts/424242/record/0-1/301'
    );
  });

  it('hides the CRM section entirely when no CRM is connected', () => {
    crmQueryResult = { data: [], isLoading: false, isError: false };
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={vi.fn()} />);

    expect(screen.queryByTestId('crm-card')).not.toBeInTheDocument();
    expect(screen.queryByText('HubSpot')).not.toBeInTheDocument();
  });

  it('calls onClose when the close button is clicked', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    setResult({ data: { contact: SUMMARY, source: 'demo' }, isLoading: false, isError: false });
    render(<ContactPane email={SUMMARY.email} onClose={onClose} />);

    await user.click(screen.getByRole('button', { name: /close/i }));

    expect(onClose).toHaveBeenCalledOnce();
  });
});
