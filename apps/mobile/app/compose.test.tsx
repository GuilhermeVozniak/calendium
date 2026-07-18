const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockListAccounts = jest.fn();
const mockSaveDraft = jest.fn();
const mockUpdateDraft = jest.fn();
const mockSendDraft = jest.fn();
const mockAiCompose = jest.fn();
const mockAiEditDraft = jest.fn();
const mockGetSendSuggestion = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listAccounts: (...args: unknown[]) => mockListAccounts(...args),
    saveDraft: (...args: unknown[]) => mockSaveDraft(...args),
    updateDraft: (...args: unknown[]) => mockUpdateDraft(...args),
    sendDraft: (...args: unknown[]) => mockSendDraft(...args),
    aiCompose: (...args: unknown[]) => mockAiCompose(...args),
    aiEditDraft: (...args: unknown[]) => mockAiEditDraft(...args),
    getSendSuggestion: (...args: unknown[]) => mockGetSendSuggestion(...args),
  },
}));

const mockBack = jest.fn();
jest.mock('expo-router', () => ({
  useRouter: () => ({ back: mockBack }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Alert } from 'react-native';
import ComposeScreen from './compose';

const AI_ENABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: true, push: false } };
const AI_DISABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

let alertSpy: jest.SpyInstance;
// Captures the option list passed to the last Alert.alert action sheet so
// tests can press a specific option by its label.
let lastAlertButtons: Array<{ text?: string; onPress?: () => void }> | undefined;

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
      <ComposeScreen />
    </QueryClientProvider>
  );
}

async function pressAlertOption(label: string) {
  const button = lastAlertButtons?.find((b) => b.text === label);
  await act(async () => {
    button?.onPress?.();
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG });
  mockListAccounts.mockResolvedValue([
    { id: 'acct_1', email: 'you@example.com', signatureHtml: '', autoBcc: [] },
  ]);
  // Default: no Smart Send suggestion (thin/no open history) — the honest
  // "not enough data yet" outcome most recipients hit; individual tests
  // override this to exercise the suggestion line itself.
  mockGetSendSuggestion.mockRejectedValue(
    new ApiRequestError(404, 'not_found', 'Not enough open history yet.')
  );
  lastAlertButtons = undefined;
  alertSpy = jest.spyOn(Alert, 'alert').mockImplementation((_title, _message, buttons) => {
    lastAlertButtons = buttons as typeof lastAlertButtons;
  });
});

afterEach(() => {
  alertSpy.mockRestore();
});

describe('ComposeScreen — AI edit action sheet', () => {
  it('hides both AI buttons when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG });
    await renderScreen();
    await flush();

    expect(screen.queryByText('AI draft')).toBeNull();
    expect(screen.queryByText('Edit with AI')).toBeNull();
  });

  it('only shows "Edit with AI" once the body has content', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByText('AI draft')).toBeTruthy();
    expect(screen.queryByText('Edit with AI')).toBeNull();

    await fireEvent.changeText(screen.getByPlaceholderText('Write your message…'), 'Thanks for the update.');

    expect(screen.getByText('Edit with AI')).toBeTruthy();
  });

  it('saves a draft on first edit, applies the AI result, and reuses that draft on Send', async () => {
    mockSaveDraft.mockResolvedValue({ id: 'draft_1' });
    mockAiEditDraft.mockResolvedValue({ text: 'Thanks for the update — sounds great!', model: 'gpt-mock' });
    mockUpdateDraft.mockResolvedValue({ id: 'draft_1' });
    mockSendDraft.mockResolvedValue({ id: 'msg_1' });

    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByPlaceholderText('To'), 'friend@example.com');
    await fireEvent.changeText(screen.getByPlaceholderText('Write your message…'), 'Thanks for the update.');
    await fireEvent.press(screen.getByText('Edit with AI'));
    await pressAlertOption('Improve');
    await flush();

    expect(mockSaveDraft).toHaveBeenCalledTimes(1);
    expect(mockAiEditDraft).toHaveBeenCalledWith('improve', 'draft_1', undefined);
    expect(screen.getByDisplayValue('Thanks for the update — sounds great!')).toBeTruthy();

    await fireEvent.press(screen.getByText('Send'));
    await flush();

    // Reuses the draft created by the AI edit instead of saving a second one.
    expect(mockSaveDraft).toHaveBeenCalledTimes(1);
    expect(mockUpdateDraft).toHaveBeenCalledWith('draft_1', expect.objectContaining({ subject: '' }));
    expect(mockSendDraft).toHaveBeenCalledWith('draft_1');
  });

  it('syncs the locally edited body to the server before a chained AI edit', async () => {
    mockSaveDraft.mockResolvedValue({ id: 'draft_1' });
    mockUpdateDraft.mockResolvedValue({ id: 'draft_1' });
    mockAiEditDraft
      .mockResolvedValueOnce({ text: 'Thanks for the update — sounds great!', model: 'gpt-mock' })
      .mockResolvedValueOnce({ text: 'Thanks — sounds great!', model: 'gpt-mock' });

    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByPlaceholderText('Write your message…'), 'Thanks for the update.');
    await fireEvent.press(screen.getByText('Edit with AI'));
    await pressAlertOption('Improve');
    await flush();

    expect(mockAiEditDraft).toHaveBeenNthCalledWith(1, 'improve', 'draft_1', undefined);
    expect(screen.getByDisplayValue('Thanks for the update — sounds great!')).toBeTruthy();
    // First edit created the draft — no sync needed yet.
    expect(mockUpdateDraft).not.toHaveBeenCalled();

    await fireEvent.press(screen.getByText('Edit with AI'));
    await pressAlertOption('Shorten');
    await flush();

    // The chained edit must see the first edit's result server-side: the
    // draft is synced with the locally edited body before the second
    // aiEditDraft call, and that sync happens before that call fires.
    expect(mockUpdateDraft).toHaveBeenCalledWith(
      'draft_1',
      expect.objectContaining({
        bodyHtml: expect.stringContaining('Thanks for the update — sounds great!'),
      })
    );
    expect(mockUpdateDraft.mock.invocationCallOrder[0]).toBeLessThan(
      mockAiEditDraft.mock.invocationCallOrder[1]
    );
    expect(mockAiEditDraft).toHaveBeenNthCalledWith(2, 'shorten', 'draft_1', undefined);
    expect(screen.getByDisplayValue('Thanks — sounds great!')).toBeTruthy();
  });

  it('shows the daily AI limit message on a 429 and leaves the draft body untouched', async () => {
    mockSaveDraft.mockResolvedValue({ id: 'draft_1' });
    mockAiEditDraft.mockRejectedValue(new ApiRequestError(429, 'rate_limited', 'too many requests'));

    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByPlaceholderText('To'), 'friend@example.com');
    await fireEvent.changeText(screen.getByPlaceholderText('Write your message…'), 'Thanks for the update.');
    await fireEvent.press(screen.getByText('Edit with AI'));
    await pressAlertOption('Shorten');
    await flush();

    expect(alertSpy).toHaveBeenCalledWith(
      'Daily AI limit reached',
      "You've used today's AI budget — try again tomorrow."
    );
    expect(screen.getByDisplayValue('Thanks for the update.')).toBeTruthy();
  });
});

describe('ComposeScreen — signature auto-append', () => {
  it("appends the account's plain-text signature to the body once the account resolves", async () => {
    mockListAccounts.mockResolvedValue([
      { id: 'acct_1', email: 'you@example.com', signatureHtml: '<p>Best,<br/>Jordan</p>', autoBcc: [] },
    ]);

    await renderScreen();
    await flush();

    expect(screen.getByDisplayValue('Best,\nJordan')).toBeTruthy();
  });

  it('does not re-append the signature on later body edits (fires once only)', async () => {
    mockListAccounts.mockResolvedValue([
      { id: 'acct_1', email: 'you@example.com', signatureHtml: '<p>Best,<br/>Jordan</p>', autoBcc: [] },
    ]);
    mockAiCompose.mockResolvedValue({ text: 'Hello there!', model: 'gpt-mock' });

    await renderScreen();
    await flush();

    expect(screen.getByDisplayValue('Best,\nJordan')).toBeTruthy();

    await fireEvent.press(screen.getByText('AI draft'));
    await flush();

    // The AI response replaces the body outright; the signature-append effect
    // must not fire a second time and re-add the signature underneath it.
    expect(screen.getByDisplayValue('Hello there!')).toBeTruthy();
  });

  it('does not append anything when the account has no signature set', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByPlaceholderText('Write your message…').props.value).toBe('');
  });
});

describe('ComposeScreen — Smart Send suggestion', () => {
  it('shows a resolution-gated Smart Send line once a recipient with open history is entered', async () => {
    mockGetSendSuggestion.mockResolvedValue({
      email: 'sarah@acme.com',
      suggestedAt: new Date('2026-07-20T14:00:00Z').toISOString(),
      utcOffsetHours: -5,
      confidence: 0.8,
      sampleSize: 12,
    });

    await renderScreen();
    await flush();

    expect(screen.queryByText(/Smart Send/)).toBeNull();

    await fireEvent.changeText(screen.getByPlaceholderText('To'), 'sarah@acme.com');
    await flush();

    expect(mockGetSendSuggestion).toHaveBeenCalledWith('sarah@acme.com');
    expect(screen.getByText(/Smart Send/)).toBeTruthy();
    expect(screen.getByText(/12 past opens/)).toBeTruthy();
  });

  it('shows no suggestion (no error alert) for a recipient with too little history (404)', async () => {
    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByPlaceholderText('To'), 'newperson@example.com');
    await flush();

    expect(mockGetSendSuggestion).toHaveBeenCalledWith('newperson@example.com');
    expect(screen.queryByText(/Smart Send/)).toBeNull();
    expect(alertSpy).not.toHaveBeenCalled();
  });

  it('does not query until the recipient looks like a well-formed email address', async () => {
    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByPlaceholderText('To'), 'sarah');
    await flush();

    expect(mockGetSendSuggestion).not.toHaveBeenCalled();
  });
});
