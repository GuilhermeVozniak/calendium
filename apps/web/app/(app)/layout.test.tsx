import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * Shell composition: the BillingGate must wrap the WHOLE authenticated shell
 * (side rail + compose, command palette, global shortcuts, Ask sidebar,
 * onboarding tour) so a denied user sees only the paywall, while the outbox
 * replay lifecycle (background sync) keeps running outside the gate.
 */

const gate = { denied: false };
vi.mock('@/components/app/paywall', () => ({
  BillingGate: ({ children }: { children: React.ReactNode }) =>
    gate.denied ? <section aria-label="Subscription required">paywall</section> : children,
  BillingTrialBanner: () => null,
}));

const syncAccessTokenOwner = vi.fn();
const sessionState = vi.hoisted(() => ({
  value: { data: { user: { id: 'u1', email: 'ada@example.com', name: 'Ada' } } as unknown, isPending: false },
}));
vi.mock('@/lib/auth-client', () => ({
  authClient: { useSession: () => sessionState.value },
  syncAccessTokenOwner: (id: string | null) => syncAccessTokenOwner(id),
}));

const routerReplace = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: routerReplace, push: vi.fn() }),
  usePathname: () => '/mail',
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock('@/components/app/command-palette', () => ({ CommandPalette: () => <div data-testid="command-palette" /> }));
vi.mock('@/components/app/onboarding-tour', () => ({ OnboardingTour: () => <div data-testid="onboarding-tour" /> }));
vi.mock('@/components/ai/ask-sidebar', () => ({
  AskSidebarProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  AskSidebarPanel: () => <div data-testid="ask-sidebar" />,
}));
vi.mock('@/components/app/account-switcher', () => ({ AccountSwitcher: () => null }));
vi.mock('@/components/app/acting-as', () => ({ ActingBanner: () => null, ActAsMenuItems: () => null }));
vi.mock('@/components/app/outbox-indicator', () => ({ OutboxIndicator: () => null }));
vi.mock('@/components/app/attachments-pane', () => ({
  AttachmentsPaneProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/components/app/compose', () => ({
  ComposeProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useCompose: () => ({ openCompose: vi.fn() }),
}));
vi.mock('@/components/theme-provider', () => ({ useTheme: () => ({ theme: 'light', setTheme: vi.fn() }) }));
vi.mock('@/lib/use-accounts', () => ({
  ActiveAccountProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useActiveAccount: () => ({ accounts: [], setActiveAccountId: vi.fn() }),
}));
vi.mock('@/lib/use-mail', () => ({ useApiOnline: () => true }));

const startOutboxReplay = vi.fn((_qc: unknown) => () => {});
vi.mock('@/lib/offline/queue', () => ({ startOutboxReplay: (qc: unknown) => startOutboxReplay(qc) }));

const useShortcuts = vi.fn();
const useChords = vi.fn();
vi.mock('@/lib/shortcuts', () => ({
  useShortcuts: (...a: unknown[]) => useShortcuts(...a),
  useChords: (...a: unknown[]) => useChords(...a),
  accountSwitchShortcuts: () => [],
}));

import { beginAccountDeletion, endAccountDeletion } from '@/lib/account-deletion';

import AppLayout from './layout';

function renderLayout() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AppLayout>
        <div data-testid="page">inbox</div>
      </AppLayout>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  endAccountDeletion();
  gate.denied = false;
  sessionState.value = { data: { user: { id: 'u1', email: 'ada@example.com', name: 'Ada' } }, isPending: false };
});

describe('(app) layout access-token owner', () => {
  it('ties the JWT cache to the session user id, including the signed-out transition', () => {
    const { rerender } = renderLayout();
    expect(syncAccessTokenOwner).toHaveBeenLastCalledWith('u1');
    sessionState.value = { data: null, isPending: false };
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <AppLayout>
          <div data-testid="page">inbox</div>
        </AppLayout>
      </QueryClientProvider>
    );
    expect(syncAccessTokenOwner).toHaveBeenLastCalledWith(null);
  });

  it('does not report an owner while the session is still loading', () => {
    sessionState.value = { data: null, isPending: true };
    renderLayout();
    expect(syncAccessTokenOwner).not.toHaveBeenCalled();
  });
});

describe('(app) layout signed-out redirect', () => {
  it('sends a signed-out visitor to /signin', () => {
    sessionState.value = { data: null, isPending: false };
    renderLayout();
    expect(routerReplace).toHaveBeenCalledWith('/signin');
  });

  // Track C I-1: deleteUser flips the session to null before the dialog's
  // hard navigation to /goodbye; the soft /signin redirect must not race it.
  it('does not redirect to /signin while an account deletion is in progress', () => {
    beginAccountDeletion();
    sessionState.value = { data: null, isPending: false };
    renderLayout();
    expect(routerReplace).not.toHaveBeenCalled();
  });
});

describe('(app) layout billing gate', () => {
  it('renders the full shell when access is granted', () => {
    renderLayout();
    expect(screen.getByTestId('page')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Compose/ })).toBeInTheDocument();
    expect(screen.getByTestId('command-palette')).toBeInTheDocument();
    expect(screen.getByTestId('ask-sidebar')).toBeInTheDocument();
    expect(useShortcuts).toHaveBeenCalled();
  });

  it('a denied user sees only the paywall — no rail, compose, palette, shortcuts, Ask or tour', () => {
    gate.denied = true;
    renderLayout();
    expect(screen.getByRole('region', { name: 'Subscription required' })).toBeInTheDocument();
    expect(screen.queryByTestId('page')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Compose/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Settings' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('command-palette')).not.toBeInTheDocument();
    expect(screen.queryByTestId('ask-sidebar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('onboarding-tour')).not.toBeInTheDocument();
    expect(useShortcuts).not.toHaveBeenCalled();
    expect(useChords).not.toHaveBeenCalled();
  });

  it('keeps background outbox replay running while paywalled', () => {
    gate.denied = true;
    renderLayout();
    expect(startOutboxReplay).toHaveBeenCalledTimes(1);
  });
});
