const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockGetThread = jest.fn();
const mockGetInstantReplies = jest.fn();
const mockMarkThreadOpened = jest.fn();
const mockReactToMessage = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    getThread: (...args: unknown[]) => mockGetThread(...args),
    getInstantReplies: (...args: unknown[]) => mockGetInstantReplies(...args),
    markThreadOpened: (...args: unknown[]) => mockMarkThreadOpened(...args),
    reactToMessage: (...args: unknown[]) => mockReactToMessage(...args),
  },
}));

let lastAlertButtons: Array<{ text?: string; onPress?: () => void }> | undefined;
let alertSpy: jest.SpyInstance;

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
import { Alert } from 'react-native';
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
    reactions: [],
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
  lastAlertButtons = undefined;
  alertSpy = jest.spyOn(Alert, 'alert').mockImplementation((_title, _message, buttons) => {
    lastAlertButtons = buttons as typeof lastAlertButtons;
  });
});

afterEach(() => {
  alertSpy.mockRestore();
});

async function pressAlertOption(label: string) {
  const button = lastAlertButtons?.find((b) => b.text === label);
  await act(async () => {
    button?.onPress?.();
  });
}

describe('ThreadScreen — AI surfacing', () => {
  it('renders the summary line from thread.summary and the instant-reply chips', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByText('Sarah needs a decision on the two open items before Thursday.')).toBeTruthy();
    expect(mockGetInstantReplies).toHaveBeenCalledWith('thr_1');
    // findByText: the replies arrive from a second async round (getThread ->
    // render -> getInstantReplies -> setState), which one flush() may not cover.
    expect(await screen.findByText('Sounds good, thanks!')).toBeTruthy();
  });

  it('prefills the reply box when an instant-reply chip is tapped', async () => {
    await renderScreen();
    await flush();

    await fireEvent.press(await screen.findByText('Sounds good, thanks!'));

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

// Pure rendering checks (no live interaction): pre-seeds a reaction on the
// fixture message so the "sent" tiny-reply indicator's honesty — driven
// strictly by the loaded reaction's `delivery` field — is verified straight
// from data, independent of the live long-press flow exercised below.
describe('ThreadScreen — reaction chips (rendering)', () => {
  it('shows the tiny-reply "sent" indicator only for a reaction whose delivery is "sent"', async () => {
    mockGetThread.mockResolvedValue({
      thread: THREAD,
      messages: [
        {
          ...MESSAGES[0],
          reactions: [
            { id: 'r_sent', messageId: 'msg_1', emoji: '👍', delivery: 'sent', createdAt: new Date().toISOString() },
            { id: 'r_local', messageId: 'msg_1', emoji: '✅', delivery: 'local', createdAt: new Date().toISOString() },
          ],
        },
      ],
    });

    await renderScreen();
    await flush();

    expect(screen.getByText('👍')).toBeTruthy();
    expect(screen.getByText('✅')).toBeTruthy();
    expect(screen.getByTestId('reaction-sent-r_sent')).toBeTruthy();
    expect(screen.queryByTestId('reaction-sent-r_local')).toBeNull();
  });
});

describe('ThreadScreen — reaction long-press interaction', () => {
  it('long-press on a message opens the reaction picker and calls reactToMessage with sendReply', async () => {
    mockReactToMessage.mockResolvedValue({
      reaction: {
        id: 'reaction_1',
        messageId: 'msg_1',
        emoji: '👍',
        delivery: 'sent',
        createdAt: new Date().toISOString(),
      },
      draftId: 'draft_1',
    });
    await renderScreen();
    await flush();

    fireEvent(await screen.findByTestId('message-msg_1'), 'longPress');
    await pressAlertOption('👍');
    await flush();

    expect(mockReactToMessage).toHaveBeenCalledWith('msg_1', '👍', true);
  });
});
