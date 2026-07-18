const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockGetThread = jest.fn();
const mockGetInstantReplies = jest.fn();
const mockMarkThreadOpened = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    getThread: (...args: unknown[]) => mockGetThread(...args),
    getInstantReplies: (...args: unknown[]) => mockGetInstantReplies(...args),
    markThreadOpened: (...args: unknown[]) => mockMarkThreadOpened(...args),
  },
}));

const mockRouterBack = jest.fn();
const mockCanGoBack = jest.fn(() => true);
jest.mock('expo-router', () => ({
  useLocalSearchParams: () => ({ id: 'thr_1' }),
  useRouter: () => ({ back: mockRouterBack, canGoBack: mockCanGoBack, replace: jest.fn() }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import type { Message, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import ThreadScreen from './[id]';

const AI_ENABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: true, push: false } };
const AI_DISABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

const THREAD: Thread = {
  id: 'thr_1',
  accountId: 'acct_1',
  subject: 'Q3 planning — final review',
  snippet: 'Two open questions before we lock it…',
  participants: [{ name: 'Sarah Chen', email: 'sarah@acme.com' }],
  labelIds: [],
  split: 'important',
  messageCount: 1,
  unread: false,
  starred: false,
  lastMessageAt: new Date().toISOString(),
  openedAt: new Date().toISOString(),
  snoozedUntil: null,
  remindAt: null,
  unsubscribeMailto: null,
  unsubscribeUrl: null,
  unsubscribeOneClick: false,
  summary: 'Sarah needs a decision on the two open items before Thursday.',
};

const MESSAGES: Message[] = [
  {
    id: 'msg_1',
    threadId: 'thr_1',
    accountId: 'acct_1',
    from: { name: 'Sarah Chen', email: 'sarah@acme.com' },
    to: [{ name: 'You', email: 'you@calendium.app' }],
    cc: [],
    bcc: [],
    subject: THREAD.subject,
    bodyHtml: '<p>Hi</p>',
    bodyText: 'Hi',
    attachments: [],
    sentAt: new Date().toISOString(),
    isDraft: false,
    openedAt: null,
  },
];

// useQuery/useMutation resolve on a real macrotask; `waitFor` is unreliable in
// this jest-expo + React 19 setup (see hooks/use-push-registration.test.ts),
// so tests drive a tick inside `act()` and assert directly afterward.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ThreadScreen />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG });
  mockGetThread.mockResolvedValue({ thread: THREAD, messages: MESSAGES });
  mockMarkThreadOpened.mockResolvedValue(undefined);
  mockGetInstantReplies.mockResolvedValue({
    replies: ['Sounds good, thanks!', "I'll take a look and follow up."],
  });
});

describe('ThreadScreen — AI surfacing', () => {
  it('renders the summary line from thread.summary and the instant-reply chips', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByText('Sarah needs a decision on the two open items before Thursday.')).toBeTruthy();
    expect(mockGetInstantReplies).toHaveBeenCalledWith('thr_1');
    expect(screen.getByText('Sounds good, thanks!')).toBeTruthy();
  });

  it('prefills the reply box when an instant-reply chip is tapped', async () => {
    await renderScreen();
    await flush();

    await fireEvent.press(screen.getByText('Sounds good, thanks!'));

    expect(screen.getByDisplayValue('Sounds good, thanks!')).toBeTruthy();
  });

  it('hides the summary and instant replies entirely when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG });
    await renderScreen();
    await flush();

    expect(
      screen.queryByText('Sarah needs a decision on the two open items before Thursday.')
    ).toBeNull();
    expect(mockGetInstantReplies).not.toHaveBeenCalled();
    expect(screen.queryByText('Sounds good, thanks!')).toBeNull();
  });
});
