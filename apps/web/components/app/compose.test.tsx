import * as React from 'react';
import type { ConnectedAccount, Snippet } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ComposeProvider,
  htmlToText,
  textToHtml,
  useCompose,
  type ComposeInitial,
} from '@/components/app/compose';

// ---------------------------------------------------------------------------
// Mocks — the data layer + session/theme surfaces ComposeForm depends on.
// ---------------------------------------------------------------------------

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
}));

const useSnippetsMock = vi.fn();
const runAiComposeMock = vi.fn();
vi.mock('@/lib/use-mail', () => ({
  useSnippets: () => useSnippetsMock(),
  runAiCompose: (...args: unknown[]) => runAiComposeMock(...args),
}));

const useInstanceMock = vi.fn();
vi.mock('@/lib/use-instance', () => ({
  useInstance: () => useInstanceMock(),
}));

const saveDraftMock = vi.fn();
const updateDraftMock = vi.fn();
const sendDraftMock = vi.fn();
const unsendDraftMock = vi.fn();
const setThreadReminderMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    saveDraft: (...args: unknown[]) => saveDraftMock(...args),
    updateDraft: (...args: unknown[]) => updateDraftMock(...args),
    sendDraft: (...args: unknown[]) => sendDraftMock(...args),
    unsendDraft: (...args: unknown[]) => unsendDraftMock(...args),
    setThreadReminder: (...args: unknown[]) => setThreadReminderMock(...args),
  }),
}));

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

const ACCOUNT: ConnectedAccount = {
  id: 'acc1',
  provider: 'google',
  email: 'me@example.com',
  status: 'active',
  scopes: [],
  vipSenders: [],
  lastSyncedAt: null,
  createdAt: new Date().toISOString(),
};

function Harness({ initial }: { initial?: ComposeInitial }) {
  const { openCompose } = useCompose();
  React.useEffect(() => {
    openCompose(initial);
    // Open exactly once per render — deps intentionally omitted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return null;
}

function renderCompose(initial?: ComposeInitial) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ComposeProvider>
        <Harness initial={initial} />
      </ComposeProvider>
    </QueryClientProvider>
  );
}

/** The "To" chip draft input — queried via its row label rather than by
 * placeholder, since the placeholder disappears once a chip exists. */
function getToInput(): HTMLElement {
  const label = screen.getByText('To');
  return within(label.parentElement as HTMLElement).getByRole('textbox');
}

/** Waits for the accounts query to resolve so `noAccounts`/`fromAccountId`
 * have settled before a test presses Send. */
async function waitForAccountsLoaded(email: string) {
  await screen.findByText(email);
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchAccountsMock.mockResolvedValue([ACCOUNT]);
  useSnippetsMock.mockReturnValue({ data: [] as Snippet[] });
  useInstanceMock.mockReturnValue({
    data: { features: { ai: false }, undoSendSeconds: 15 },
  });
  saveDraftMock.mockResolvedValue({ id: 'draft1' });
  sendDraftMock.mockResolvedValue(undefined);
});

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

describe('htmlToText', () => {
  it('converts <br> and <br/> tags to newlines', () => {
    expect(htmlToText('Line1<br>Line2<br/>Line3')).toBe('Line1\nLine2\nLine3');
  });

  it('converts adjacent </p><p> boundaries into a blank line', () => {
    expect(htmlToText('<p>First</p><p>Second</p>')).toBe('First\n\nSecond');
  });

  it('strips every other tag without leaving markup behind', () => {
    expect(htmlToText('<strong>Bold</strong> and <em>italic</em>')).toBe('Bold and italic');
  });

  it('decodes &amp; &lt; &gt; entities in that order', () => {
    expect(htmlToText('Tom &amp; Jerry &lt;tag&gt;')).toBe('Tom & Jerry <tag>');
  });

  it('trims leading and trailing whitespace left over from stripped tags', () => {
    expect(htmlToText('  <p>  hi  </p>  ')).toBe('hi');
  });

  it('returns an empty string for empty input', () => {
    expect(htmlToText('')).toBe('');
  });

  it('handles a realistic multi-paragraph snippet body', () => {
    const html = '<p>Hi there,</p><p>Thanks for reaching out!<br/>Talk soon.</p>';
    expect(htmlToText(html)).toBe('Hi there,\n\nThanks for reaching out!\nTalk soon.');
  });
});

describe('textToHtml', () => {
  it('wraps single-line text in a <p> and converts newlines to <br/>', () => {
    expect(textToHtml('line1\nline2')).toBe('<p>line1<br/>line2</p>');
  });

  it('splits into separate <p> paragraphs on a blank line', () => {
    expect(textToHtml('para1\n\npara2')).toBe('<p>para1</p><p>para2</p>');
  });

  it('collapses 3+ consecutive newlines into a single paragraph break', () => {
    expect(textToHtml('para1\n\n\npara2')).toBe('<p>para1</p><p>para2</p>');
  });

  it('escapes &, <, > characters', () => {
    expect(textToHtml('Tom & Jerry <3')).toBe('<p>Tom &amp; Jerry &lt;3</p>');
  });

  it('round-trips through htmlToText for plain text', () => {
    const original = 'Hello there,\n\nThanks!';
    expect(htmlToText(textToHtml(original))).toBe(original);
  });
});

// ---------------------------------------------------------------------------
// ComposeForm — recipient add/validation (exercises the unexported EMAIL_RE /
// parseAddress indirectly; compose.tsx does not export them, so behavior is
// verified through the rendered "To" chip input instead of a direct import).
// ---------------------------------------------------------------------------

describe('ComposeForm — recipients', () => {
  it('adds a chip for a valid single-character-local-part email and clears the input', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'x@y.io{Enter}');
    expect(await screen.findByText('x@y.io')).toBeInTheDocument();
    expect(toInput).toHaveValue('');
  });

  // Regression test for a fixed production bug: parseAddress()'s old regex
  // (`^\s*(?:"?([^"<]*)"?\s*)?<?([^\s<>]+@[^\s<>]+)>?\s*$`) greedily backtracked
  // group 1 ("name") into the email's local-part whenever a *bare* address
  // (no `<...>` brackets) had a local-part of 2+ characters — i.e. virtually
  // every real email typed as plain "name@example.com". The first character
  // of the local-part was stolen into a bogus display name and the stored
  // email was truncated (e.g. 'alice@example.com' => { name: 'alic', email:
  // 'e@example.com' }). parseAddress() was rewritten to match the
  // `<email>`-bracketed forms via one anchored pattern and bare emails via
  // EMAIL_RE directly, so no backtracking can steal local-part characters.
  it('adds a chip for a bare multi-character-local-part email without corrupting it', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'alice@example.com{Enter}');
    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(screen.queryByText('alic')).not.toBeInTheDocument();
    expect(toInput).toHaveValue('');
  });

  it('parses "Name <email>" input correctly and shows the name as the chip label', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'Alice Smith <alice@example.com>{Enter}');
    expect(await screen.findByText('Alice Smith')).toBeInTheDocument();
  });

  it('parses a quoted display name "Name" <email> and shows the name as the chip label', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, '"Alice B" <alice@example.com>{Enter}');
    expect(await screen.findByText('Alice B')).toBeInTheDocument();
  });

  it('parses an angle-bracket-only address <email> with no display name', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, '<alice@example.com>{Enter}');
    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
  });

  it('normalizes email casing to lowercase', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'ALICE@EXAMPLE.COM{Enter}');
    expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
    expect(screen.queryByText('ALICE@EXAMPLE.COM')).not.toBeInTheDocument();
  });

  it('does not add a chip for an invalid email and keeps the draft text', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'not-an-email{Enter}');
    expect(screen.queryByText('not-an-email')).not.toBeInTheDocument();
    expect(toInput).toHaveValue('not-an-email');
  });

  it('rejects an address missing a domain (no dot after @)', async () => {
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'alice@example{Enter}');
    expect(screen.queryByText('alice@example')).not.toBeInTheDocument();
    expect(screen.queryByText('alic')).not.toBeInTheDocument();
  });

  it('does not add a duplicate chip for the same email', async () => {
    const user = userEvent.setup();
    renderCompose({ to: [{ name: null, email: 'a@b.co' }] });
    const toInput = getToInput();
    await user.type(toInput, 'a@b.co{Enter}');
    expect(screen.getAllByText('a@b.co')).toHaveLength(1);
  });

  it('removes a chip via its remove button', async () => {
    const user = userEvent.setup();
    renderCompose({ to: [{ name: null, email: 'bob@example.com' }] });
    expect(await screen.findByText('bob@example.com')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Remove bob@example.com' }));
    expect(screen.queryByText('bob@example.com')).not.toBeInTheDocument();
  });

  it('removes the last chip on Backspace when the input is empty', async () => {
    const user = userEvent.setup();
    renderCompose({ to: [{ name: null, email: 'carol@example.com' }] });
    expect(screen.getByText('carol@example.com')).toBeInTheDocument();
    const toInput = getToInput();
    await user.click(toInput);
    await user.keyboard('{Backspace}');
    expect(screen.queryByText('carol@example.com')).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// ComposeForm — subject/body editing
// ---------------------------------------------------------------------------

describe('ComposeForm — subject and body', () => {
  it('edits the subject field', async () => {
    const user = userEvent.setup();
    renderCompose();
    const subjectInput = await screen.findByPlaceholderText('Subject');
    await user.type(subjectInput, 'Hello there');
    expect(subjectInput).toHaveValue('Hello there');
  });

  it('edits the body textarea', async () => {
    const user = userEvent.setup();
    renderCompose();
    const bodyInput = await screen.findByPlaceholderText(/Write your message/);
    await user.type(bodyInput, 'Some body text');
    expect(bodyInput).toHaveValue('Some body text');
  });
});

// ---------------------------------------------------------------------------
// ComposeForm — snippet insertion
// ---------------------------------------------------------------------------

describe('ComposeForm — snippets', () => {
  const SNIPPET: Snippet = {
    id: 's1',
    name: 'Thanks',
    shortcut: 'ty',
    bodyHtml: '<p>Thanks a lot!</p>',
    usageCount: 0,
  };

  it('shows matching snippets when typing ";<shortcut>" and inserts on click', async () => {
    useSnippetsMock.mockReturnValue({ data: [SNIPPET] });
    const user = userEvent.setup();
    renderCompose();
    const bodyInput = await screen.findByPlaceholderText(/Write your message/);
    await user.click(bodyInput);
    await user.type(bodyInput, ';ty');

    const option = await screen.findByText('Thanks');
    await user.click(option);

    await waitFor(() => expect(bodyInput).toHaveValue('Thanks a lot!'));
  });

  it('does not show the snippet popover when nothing matches', async () => {
    useSnippetsMock.mockReturnValue({ data: [SNIPPET] });
    const user = userEvent.setup();
    renderCompose();
    const bodyInput = await screen.findByPlaceholderText(/Write your message/);
    await user.click(bodyInput);
    await user.type(bodyInput, ';zzz');
    expect(screen.queryByText('Thanks')).not.toBeInTheDocument();
  });

  it('filters snippets by name as well as shortcut', async () => {
    useSnippetsMock.mockReturnValue({ data: [SNIPPET] });
    const user = userEvent.setup();
    renderCompose();
    const bodyInput = await screen.findByPlaceholderText(/Write your message/);
    await user.click(bodyInput);
    await user.type(bodyInput, ';than');
    expect(await screen.findByText('Thanks')).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// ComposeForm — compose -> draft (send) flow
// ---------------------------------------------------------------------------

describe('ComposeForm — send flow', () => {
  it('saves a draft and sends it on Ctrl/Cmd+Enter, then closes', async () => {
    const user = userEvent.setup();
    renderCompose();
    await waitForAccountsLoaded(ACCOUNT.email);

    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'a@b.co{Enter}');

    const subjectInput = screen.getByPlaceholderText('Subject');
    await user.type(subjectInput, 'Hi');

    const bodyInput = screen.getByPlaceholderText(/Write your message/);
    await user.type(bodyInput, 'Hello');

    fireEvent.keyDown(bodyInput, { key: 'Enter', ctrlKey: true, metaKey: true });

    await waitFor(() => expect(saveDraftMock).toHaveBeenCalledTimes(1));
    const input = saveDraftMock.mock.calls[0]![0];
    expect(input.accountId).toBe('acc1');
    expect(input.to).toEqual([{ name: null, email: 'a@b.co' }]);
    expect(input.subject).toBe('Hi');
    expect(input.bodyHtml).toBe(textToHtml('Hello'));

    await waitFor(() => expect(sendDraftMock).toHaveBeenCalledWith('draft1'));
    expect(toastSuccess).toHaveBeenCalledWith('Sent', expect.objectContaining({ duration: 15000 }));

    // Dialog closes after a successful send.
    await waitFor(() => expect(screen.queryByPlaceholderText('Subject')).not.toBeInTheDocument());
  });

  it('blocks sending when there are no recipients', async () => {
    renderCompose();
    await waitForAccountsLoaded(ACCOUNT.email);
    const bodyInput = screen.getByPlaceholderText(/Write your message/);
    fireEvent.keyDown(bodyInput, { key: 'Enter', ctrlKey: true, metaKey: true });
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Add at least one recipient.'));
    expect(saveDraftMock).not.toHaveBeenCalled();
  });

  it('blocks sending when there are no connected accounts', async () => {
    fetchAccountsMock.mockResolvedValue([]);
    const user = userEvent.setup();
    renderCompose();
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'a@b.co{Enter}');
    const bodyInput = screen.getByPlaceholderText(/Write your message/);
    fireEvent.keyDown(bodyInput, { key: 'Enter', ctrlKey: true, metaKey: true });
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Connect a mailbox in Settings before sending.')
    );
    expect(saveDraftMock).not.toHaveBeenCalled();
  });

  it('surfaces a real API failure instead of a fake success', async () => {
    saveDraftMock.mockRejectedValue(new Error('network down'));
    const user = userEvent.setup();
    renderCompose();
    await waitForAccountsLoaded(ACCOUNT.email);
    const toInput = await screen.findByPlaceholderText('name@example.com');
    await user.type(toInput, 'a@b.co{Enter}');
    const bodyInput = screen.getByPlaceholderText(/Write your message/);
    fireEvent.keyDown(bodyInput, { key: 'Enter', ctrlKey: true, metaKey: true });

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('The message could not be sent. Please try again.')
    );
    // Dialog stays open with the composed text intact — never fakes success.
    expect(screen.getByPlaceholderText('Subject')).toBeInTheDocument();
  });
});
