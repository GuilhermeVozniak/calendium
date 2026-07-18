const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockAiAskCited = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    aiAskCited: (...args: unknown[]) => mockAiAskCited(...args),
  },
}));

const mockBack = jest.fn();
const mockPush = jest.fn();
jest.mock('expo-router', () => ({
  useRouter: () => ({ back: mockBack, push: mockPush }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import AskAiScreen from './ask-ai';

const AI_ENABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: true, push: false } };
const AI_DISABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

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
      <AskAiScreen />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG });
});

describe('AskAiScreen', () => {
  it('shows a disabled message instead of the ask form when AI is off for this server', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG });
    await renderScreen();
    expect(screen.getByText('AI features are disabled on this server.')).toBeTruthy();
    expect(screen.queryByPlaceholderText('Ask about your mailbox…')).toBeNull();
  });

  it('asks a question and renders the cited answer with its sources', async () => {
    mockAiAskCited.mockResolvedValue({
      answer: 'You have two open items due Thursday.',
      model: 'gpt-mock',
      sources: [
        { threadId: 'thr_1', subject: 'Q3 planning — final review', snippet: 'Two open questions…' },
      ],
    });
    await renderScreen();

    await fireEvent.changeText(screen.getByPlaceholderText('Ask about your mailbox…'), 'What is due this week?');
    await fireEvent.press(screen.getByTestId('ask-submit'));
    await flush();

    expect(mockAiAskCited).toHaveBeenCalledWith({ question: 'What is due this week?' });
    expect(screen.getByText('You have two open items due Thursday.')).toBeTruthy();
    expect(screen.getByText('Q3 planning — final review')).toBeTruthy();
  });

  it('opens the source thread and dismisses the modal when a source is tapped', async () => {
    mockAiAskCited.mockResolvedValue({
      answer: 'Here is what I found.',
      model: 'gpt-mock',
      sources: [{ threadId: 'thr_42', subject: 'Contract renewal', snippet: 'Countersign by Thursday.' }],
    });
    await renderScreen();

    await fireEvent.changeText(screen.getByPlaceholderText('Ask about your mailbox…'), 'Any contracts pending?');
    await fireEvent.press(screen.getByTestId('ask-submit'));
    await flush();

    await fireEvent.press(screen.getByText('Contract renewal'));

    expect(mockBack).toHaveBeenCalledTimes(1);
    expect(mockPush).toHaveBeenCalledWith({ pathname: '/thread/[id]', params: { id: 'thr_42' } });
  });

  it('shows the daily AI limit message on a 429', async () => {
    mockAiAskCited.mockRejectedValue(new ApiRequestError(429, 'rate_limited', 'too many requests'));
    await renderScreen();

    await fireEvent.changeText(screen.getByPlaceholderText('Ask about your mailbox…'), 'Anything urgent?');
    await fireEvent.press(screen.getByTestId('ask-submit'));
    await flush();

    expect(screen.getByText(/today's AI limit/)).toBeTruthy();
  });

  it('shows a generic error message on other failures (never fabricates a fake answer)', async () => {
    mockAiAskCited.mockRejectedValue(new Error('network down'));
    await renderScreen();

    await fireEvent.changeText(screen.getByPlaceholderText('Ask about your mailbox…'), 'Anything urgent?');
    await fireEvent.press(screen.getByTestId('ask-submit'));
    await flush();

    expect(screen.getByText('network down')).toBeTruthy();
  });
});
