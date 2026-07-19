const mockListThreads = jest.fn();
const mockListOpens = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listThreads: (...args: unknown[]) => mockListThreads(...args),
    listOpens: (...args: unknown[]) => mockListOpens(...args),
  },
}));

const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: mockPush }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

// The screen now imports @/lib/offline (offline triage queue + queued badge),
// which pulls in AsyncStorage and expo-network at module scope; both need
// test fakes here.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);
jest.mock('expo-network', () => ({
  addNetworkStateListener: jest.fn(() => ({ remove: jest.fn() })),
  getNetworkStateAsync: jest.fn(() =>
    Promise.resolve({ isConnected: false, isInternetReachable: false })
  ),
}));

// The Opens sheet tests below never render a non-empty thread list, so the
// real swipeable rows never mount — but the module import itself still runs
// at load time, and pulling in real reanimated/gesture-handler native glue
// under jest is unrelated to what this suite covers. Stub it to a plain View.
jest.mock('react-native-gesture-handler/ReanimatedSwipeable', () => {
  const { View } = require('react-native');
  return { __esModule: true, default: View };
});

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import InboxScreen from './inbox';

// useQuery/useInfiniteQuery resolve on a real macrotask; `waitFor` is
// unreliable in this jest-expo + React 19 setup (see
// hooks/use-push-registration.test.ts), so tests drive a tick inside `act()`
// and assert directly afterward (same pattern as compose.test.tsx etc).
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <InboxScreen />
    </QueryClientProvider>
  );
}

const PAGE_1 = {
  items: [
    {
      messageId: 'msg_1_open',
      threadId: 'thr_1',
      accountId: 'acct_1',
      subject: 'Q3 planning — final review',
      recipients: [{ name: 'Sarah Chen', email: 'sarah@acme.com' }],
      openedAt: new Date().toISOString(),
      sentAt: new Date().toISOString(),
    },
  ],
  nextCursor: 'cursor_2',
};

const PAGE_2 = {
  items: [
    {
      messageId: 'msg_2_open',
      threadId: 'thr_2',
      accountId: 'acct_1',
      subject: 'Contract renewal: Acme Corp',
      recipients: [{ name: 'Marcus Webb', email: 'marcus@acme.com' }],
      openedAt: new Date().toISOString(),
      sentAt: new Date().toISOString(),
    },
  ],
  nextCursor: null,
};

beforeEach(() => {
  jest.clearAllMocks();
  // No threads in any split for this suite — keeps the swipeable thread rows
  // out of the tree entirely, since Opens is the only thing under test here.
  mockListThreads.mockResolvedValue({ items: [], nextCursor: null });
});

describe('InboxScreen — Recent Opens sheet', () => {
  it('is closed by default and does not fetch opens', async () => {
    await renderScreen();
    await flush();

    expect(screen.queryByText('Recent opens')).toBeNull();
    expect(mockListOpens).not.toHaveBeenCalled();
  });

  it('opens the sheet and renders the first page from listOpens', async () => {
    mockListOpens.mockResolvedValueOnce(PAGE_1);

    await renderScreen();
    await flush();

    await fireEvent.press(await screen.findByTestId('open-opens-sheet'));
    await flush();

    expect(screen.getByText('Recent opens')).toBeTruthy();
    expect(mockListOpens).toHaveBeenCalledWith({ limit: 20, cursor: undefined });
    expect(screen.getByText('Q3 planning — final review')).toBeTruthy();
  });

  it('closes the sheet on the close button', async () => {
    mockListOpens.mockResolvedValueOnce(PAGE_1);

    await renderScreen();
    await flush();

    await fireEvent.press(await screen.findByTestId('open-opens-sheet'));
    await flush();
    expect(screen.getByText('Recent opens')).toBeTruthy();

    await fireEvent.press(await screen.findByTestId('close-opens-sheet'));

    expect(screen.queryByText('Recent opens')).toBeNull();
  });

  // Kept last in the file: this test's pagination round-trip (initial fetch
  // + fetchNextPage) leaves more async work in flight than the others, and
  // this ordering avoids that tail interacting with a subsequent test's own
  // render (see the identical ordering fix in thread/[id].test.tsx).
  it('loads the next page on end-reached, using the previous page nextCursor', async () => {
    mockListOpens.mockResolvedValueOnce(PAGE_1).mockResolvedValueOnce(PAGE_2);

    await renderScreen();
    await flush();

    await fireEvent.press(await screen.findByTestId('open-opens-sheet'));
    await flush();

    expect(screen.getByText('Q3 planning — final review')).toBeTruthy();
    expect(screen.queryByText('Contract renewal: Acme Corp')).toBeNull();

    fireEvent(await screen.findByTestId('opens-list'), 'endReached');
    await flush();

    expect(mockListOpens).toHaveBeenLastCalledWith({ limit: 20, cursor: 'cursor_2' });
    expect(screen.getByText('Contract renewal: Acme Corp')).toBeTruthy();
  });
});
