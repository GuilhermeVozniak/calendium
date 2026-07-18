'use client';

import * as React from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { DraftInput, EmailAddress, Snippet } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { format } from 'date-fns';
import { BellRing, Check, ChevronDown, Clock, Loader2, Send, Sparkles, X } from 'lucide-react';
import { toast } from 'sonner';

import { ChipsRow, parseAddress } from '@/components/app/chips-row';
import { TimePickerDialog } from '@/components/app/snooze-menu';
import { AiEditMenu } from '@/components/compose/ai-edit-menu';
import { AiDraftBadge } from '@/components/mail/ai-draft-badge';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Textarea } from '@/components/ui/textarea';
import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { formatOptionTime, reminderOptions, sendLaterOptions } from '@/lib/mail-utils';
import { fetchAccounts } from '@/lib/settings-data';
import { MOD_KEY } from '@/lib/shortcuts';
import { useInstance } from '@/lib/use-instance';
import { aiErrorMessage, runAiCompose, useSendSuggestion, useSnippets } from '@/lib/use-mail';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Context — openCompose() from anywhere in the app shell
// ---------------------------------------------------------------------------

export interface ComposeInitial {
  to?: EmailAddress[];
  cc?: EmailAddress[];
  bcc?: EmailAddress[];
  subject?: string;
  body?: string;
  /** Set when replying/forwarding — enables “remind me if no reply”. */
  threadId?: string | null;
  /** Preselect the sending account (e.g. when reopening a draft). */
  accountId?: string;
  /** When reopening a server draft, edit it in place instead of creating a new one. */
  draftId?: string;
  /** True when reopening an AI-generated draft (auto-reply / auto-draft) — shows the AI draft badge. */
  aiGenerated?: boolean;
}

interface ComposeContextValue {
  openCompose: (initial?: ComposeInitial) => void;
}

const ComposeContext = React.createContext<ComposeContextValue | null>(null);

export function useCompose(): ComposeContextValue {
  const ctx = React.useContext(ComposeContext);
  if (!ctx) throw new Error('useCompose must be used within a <ComposeProvider>');
  return ctx;
}

export function ComposeProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = React.useState(false);
  const [initial, setInitial] = React.useState<ComposeInitial | null>(null);
  const [session, setSession] = React.useState(0);

  const openCompose = React.useCallback((init?: ComposeInitial) => {
    setInitial(init ?? null);
    setSession((s) => s + 1);
    setOpen(true);
  }, []);

  const value = React.useMemo(() => ({ openCompose }), [openCompose]);

  return (
    <ComposeContext.Provider value={value}>
      {children}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="top-[8%] translate-y-0 gap-0 p-0 sm:max-w-2xl"
          showCloseButton={false}
        >
          <DialogTitle className="sr-only">New message</DialogTitle>
          <DialogDescription className="sr-only">Compose an email</DialogDescription>
          <ComposeForm key={session} initial={initial} close={() => setOpen(false)} />
        </DialogContent>
      </Dialog>
    </ComposeContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ChipsRow and parseAddress now live in components/app/chips-row.tsx (M2.5
// Task 13) so the settings signature/auto-BCC editor can reuse them without
// duplicating the parse/commit logic. Re-exported here so any existing
// import of these symbols from compose.tsx keeps working unchanged.
export { ChipsRow, parseAddress };

export function htmlToText(html: string): string {
  return html
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/p>\s*<p>/gi, '\n\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .trim();
}

export function textToHtml(text: string): string {
  const escaped = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return escaped
    .split(/\n{2,}/)
    .map((para) => `<p>${para.replaceAll('\n', '<br/>')}</p>`)
    .join('');
}

/**
 * Buckets a Smart Send suggestion's UTC `suggestedAt` into the recipient's
 * local part-of-day by shifting it with their inferred `utcOffsetHours`.
 */
export function localTimeBucket(
  suggestedAt: string,
  utcOffsetHours: number
): 'morning' | 'afternoon' | 'evening' {
  const raw = (new Date(suggestedAt).getUTCHours() + utcOffsetHours) % 24;
  const localHour = raw < 0 ? raw + 24 : raw;
  if (localHour < 12) return 'morning';
  if (localHour < 17) return 'afternoon';
  return 'evening';
}

// ---------------------------------------------------------------------------
// Compose form
// ---------------------------------------------------------------------------

function ComposeForm({
  initial,
  close,
}: {
  initial: ComposeInitial | null;
  close: () => void;
}) {
  const queryClient = useQueryClient();
  const instance = useInstance();
  const aiEnabled = instance.data?.features.ai ?? false;
  const undoSeconds = instance.data?.undoSendSeconds ?? 15;

  // Real connected accounts drive the From picker; no send is possible without
  // one. (Demo mode seeds a mock account through fetchAccounts.)
  const accountsQuery = useQuery({
    queryKey: ['accounts'],
    queryFn: fetchAccounts,
    staleTime: 5 * 60_000,
  });
  const accounts = React.useMemo(() => accountsQuery.data ?? [], [accountsQuery.data]);
  const [fromAccountId, setFromAccountId] = React.useState(initial?.accountId ?? '');
  React.useEffect(() => {
    if (accounts.length === 0) return;
    if (!fromAccountId || !accounts.some((a) => a.id === fromAccountId)) {
      setFromAccountId(accounts[0]!.id);
    }
  }, [accounts, fromAccountId]);
  const fromAccount = accounts.find((a) => a.id === fromAccountId) ?? null;
  const noAccounts = !accountsQuery.isLoading && accounts.length === 0;

  const [to, setTo] = React.useState<EmailAddress[]>(initial?.to ?? []);
  const [cc, setCc] = React.useState<EmailAddress[]>(initial?.cc ?? []);
  const [bcc, setBcc] = React.useState<EmailAddress[]>(initial?.bcc ?? []);
  const [showCc, setShowCc] = React.useState((initial?.cc?.length ?? 0) > 0);
  const [showBcc, setShowBcc] = React.useState((initial?.bcc?.length ?? 0) > 0);
  const [subject, setSubject] = React.useState(initial?.subject ?? '');
  const [body, setBody] = React.useState(initial?.body ?? '');
  const [sending, setSending] = React.useState(false);
  const [scheduledAt, setScheduledAt] = React.useState<Date | null>(null);
  const [remindAt, setRemindAt] = React.useState<{ label: string; when: Date } | null>(null);
  const [scheduleDialogOpen, setScheduleDialogOpen] = React.useState(false);
  // Tracks the persisted draft id across the session: seeded from a reopened
  // draft, or lazily created by ensureDraftId() the first time AI edit needs
  // one. Once set, send/save use it (PUT) instead of creating a duplicate.
  const [liveDraftId, setLiveDraftId] = React.useState<string | null>(initial?.draftId ?? null);

  const bodyRef = React.useRef<HTMLTextAreaElement>(null);

  // --- Signature auto-apply -------------------------------------------------
  // Tracks the plain-text signature block currently appended to `body` (the
  // "\n\n--\n" + htmlToText(signatureHtml) placeholder shown in the editor) so
  // switching accounts replaces it instead of stacking signatures, and so
  // send-time can strip it back off before appending the rich HTML version.
  const appliedSignatureRef = React.useRef<string | null>(null);

  React.useEffect(() => {
    const signatureHtml = fromAccount?.signatureHtml?.trim();
    const nextBlock = signatureHtml ? `\n\n--\n${htmlToText(signatureHtml)}` : null;
    if (nextBlock === appliedSignatureRef.current) return;
    // Capture the outgoing block before mutating the ref: the setBody
    // updater below may run after this line (e.g. React defers the
    // functional update), so it must not read the ref for "previous" — it
    // would see the just-written `nextBlock` instead and never strip.
    const prevBlock = appliedSignatureRef.current;
    appliedSignatureRef.current = nextBlock;
    setBody((prev) => {
      let base = prev;
      if (prevBlock && base.endsWith(prevBlock)) {
        base = base.slice(0, -prevBlock.length);
      }
      if (nextBlock && !base.endsWith(nextBlock)) {
        base = `${base}${nextBlock}`;
      }
      return base;
    });
  }, [fromAccount?.signatureHtml]);

  /**
   * Sent HTML body: the plain-text signature placeholder is stripped and
   * replaced with the account's real rich `signatureHtml` (links, formatting)
   * rather than the plain-text approximation shown in the editor.
   */
  function buildBodyHtml(): string {
    const signatureHtml = fromAccount?.signatureHtml?.trim();
    if (!signatureHtml) return textToHtml(body);
    const mainText =
      appliedSignatureRef.current && body.endsWith(appliedSignatureRef.current)
        ? body.slice(0, -appliedSignatureRef.current.length)
        : body;
    return `${textToHtml(mainText)}<p>--</p>${signatureHtml}`;
  }

  // --- Smart Send nudge ------------------------------------------------------
  // Debounced (500ms) so a suggestion isn't queried on every keystroke while
  // typing a recipient's address.
  const firstToEmail = to[0]?.email ?? null;
  const [debouncedToEmail, setDebouncedToEmail] = React.useState<string | null>(firstToEmail);
  React.useEffect(() => {
    const timer = setTimeout(() => setDebouncedToEmail(firstToEmail), 500);
    return () => clearTimeout(timer);
  }, [firstToEmail]);
  const suggestionQuery = useSendSuggestion(debouncedToEmail);
  const suggestion = suggestionQuery.data ?? null;
  const [dismissedSuggestionKey, setDismissedSuggestionKey] = React.useState<string | null>(null);
  const suggestionKey = suggestion ? `${suggestion.email}|${suggestion.suggestedAt}` : null;
  const showSuggestionChip =
    suggestion !== null &&
    suggestion.confidence >= 0.3 &&
    !scheduledAt &&
    suggestionKey !== dismissedSuggestionKey;

  // --- Snippet picker (";" trigger) ---------------------------------------
  const { data: snippets = [] } = useSnippets();
  const [snippetQuery, setSnippetQuery] = React.useState<string | null>(null);
  const [snippetIndex, setSnippetIndex] = React.useState(0);

  const matchingSnippets = React.useMemo(() => {
    if (snippetQuery === null) return [];
    const q = snippetQuery.toLowerCase();
    return snippets.filter(
      (s) => s.name.toLowerCase().includes(q) || (s.shortcut ?? '').toLowerCase().startsWith(q)
    );
  }, [snippets, snippetQuery]);

  function detectSnippetTrigger(value: string, cursor: number) {
    const before = value.slice(0, cursor);
    const match = before.match(/(?:^|[\s\n]);([\w-]*)$/);
    setSnippetQuery(match ? (match[1] ?? '') : null);
    setSnippetIndex(0);
  }

  function insertSnippet(snippet: Snippet) {
    const textarea = bodyRef.current;
    const cursor = textarea?.selectionStart ?? body.length;
    const before = body.slice(0, cursor).replace(/;([\w-]*)$/, '');
    const after = body.slice(cursor);
    const text = htmlToText(snippet.bodyHtml);
    const next = `${before}${text}${after}`;
    setBody(next);
    setSnippetQuery(null);
    requestAnimationFrame(() => {
      textarea?.focus();
      const pos = before.length + text.length;
      textarea?.setSelectionRange(pos, pos);
    });
  }

  function onBodyKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault();
      void handleSend();
      return;
    }
    if (snippetQuery === null || matchingSnippets.length === 0) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setSnippetIndex((i) => (i + 1) % matchingSnippets.length);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setSnippetIndex((i) => (i - 1 + matchingSnippets.length) % matchingSnippets.length);
    } else if (event.key === 'Enter' || event.key === 'Tab') {
      event.preventDefault();
      insertSnippet(matchingSnippets[snippetIndex]!);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      setSnippetQuery(null);
    }
  }

  // --- AI assist ------------------------------------------------------------
  const [aiOpen, setAiOpen] = React.useState(false);
  const [aiPrompt, setAiPrompt] = React.useState('');
  const [aiBusy, setAiBusy] = React.useState(false);

  async function handleAi() {
    if (!aiPrompt.trim()) return;
    setAiBusy(true);
    try {
      const res = await runAiCompose({
        action: initial?.threadId ? 'reply' : 'compose',
        prompt: aiPrompt.trim(),
        threadId: initial?.threadId ?? undefined,
      });
      setBody((prev) => (prev.trim() ? `${prev.trimEnd()}\n\n${res.text}` : res.text));
      setAiOpen(false);
      setAiPrompt('');
      if (res.source === 'demo') {
        toast.info('AI is offline — drafted locally.');
      }
      bodyRef.current?.focus();
    } catch (err) {
      toast.error(aiErrorMessage(err));
    } finally {
      setAiBusy(false);
    }
  }

  // --- AI edit menu (Improve / Shorten / Simplify / Fix grammar / Change tone) -
  /**
   * aiEditDraft needs a real draftId. A brand-new compose session doesn't have
   * one yet, so the first AI edit lazily persists the current form as a draft
   * (matching the reopened-draft PUT-vs-POST logic in handleSend below) and
   * remembers the id for the rest of the session. In demo mode, where saveDraft
   * has no real backend to hit, a local id lets the edit proceed through its
   * own demo fallback without a network round trip.
   */
  async function ensureDraftId(): Promise<string> {
    if (liveDraftId) return liveDraftId;
    const input: DraftInput = {
      accountId: fromAccountId,
      threadId: initial?.threadId ?? null,
      to,
      cc,
      bcc,
      subject,
      bodyHtml: buildBodyHtml(),
      scheduledAt: null,
    };
    try {
      const draft = await getApiClient().saveDraft(input);
      setLiveDraftId(draft.id);
      return draft.id;
    } catch (err) {
      if (DEMO_MODE) {
        const id = `draft-demo-${Date.now()}`;
        setLiveDraftId(id);
        return id;
      }
      throw err;
    }
  }

  // --- Undo send ------------------------------------------------------------
  async function handleUndo(draftId: string) {
    try {
      await getApiClient().unsendDraft(draftId);
      void queryClient.invalidateQueries({ queryKey: ['drafts'] });
      toast.success('Send undone — the message is back in your drafts.');
    } catch (err) {
      if (err instanceof ApiRequestError && err.status === 409) {
        toast.error('Too late — that message already went out.');
      } else {
        toast.error('Could not undo the send.');
      }
    }
  }

  // --- Send -----------------------------------------------------------------
  async function handleSend() {
    if (accounts.length === 0) {
      toast.error('Connect a mailbox in Settings before sending.');
      return;
    }
    if (!fromAccountId) return;
    if (to.length === 0) {
      toast.error('Add at least one recipient.');
      return;
    }
    setSending(true);
    const scheduledIso = scheduledAt ? scheduledAt.toISOString() : null;
    const successMessage = scheduledIso
      ? `Scheduled for ${format(scheduledAt!, 'EEE p')}`
      : 'Sent';
    const input: DraftInput = {
      accountId: fromAccountId,
      threadId: initial?.threadId ?? null,
      to,
      cc,
      bcc,
      subject,
      bodyHtml: buildBodyHtml(),
      scheduledAt: scheduledIso,
    };
    try {
      const api = getApiClient();
      // Reopened drafts (or ones lazily created by ensureDraftId for AI edit)
      // are edited in place (full-replace PUT) then sent, so a draft isn't
      // left behind as a duplicate.
      const draft = liveDraftId
        ? await api.updateDraft(liveDraftId, input)
        : await api.saveDraft(input);
      await api.sendDraft(draft.id);
      if (remindAt && initial?.threadId) {
        await api.setThreadReminder(initial.threadId, remindAt.when.toISOString());
      }
      void queryClient.invalidateQueries({ queryKey: ['drafts'] });
      toast.success(successMessage, {
        duration: undoSeconds * 1000,
        action: { label: 'Undo', onClick: () => void handleUndo(draft.id) },
      });
      close();
    } catch (err) {
      if (DEMO_MODE) {
        toast.success(`${successMessage} (demo mode)`);
        close();
        return;
      }
      // Never fake success: keep the dialog open with the composed text intact
      // and surface the real failure.
      const message =
        err instanceof ApiRequestError
          ? err.message
          : 'The message could not be sent. Please try again.';
      toast.error(message);
      setSending(false);
    }
  }

  const remindChoices = reminderOptions();

  return (
    <div className="flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-4 py-2.5">
        <span className="flex items-center gap-2 text-sm font-medium">
          {initial?.threadId ? 'Reply' : 'New message'}
          {initial?.aiGenerated && <AiDraftBadge />}
        </span>
        <div className="flex items-center gap-1">
          <span className="text-muted-foreground mr-1 hidden items-center gap-1 text-xs sm:flex">
            <Kbd size="sm">;</Kbd> snippets
          </span>
          <Button variant="ghost" size="icon" className="size-7" onClick={close} aria-label="Close">
            <X className="size-4" />
          </Button>
        </div>
      </div>

      {/* From account */}
      <div className="flex min-h-9 items-center gap-1.5 border-b px-4 py-1.5">
        <span className="text-muted-foreground w-8 shrink-0 text-xs">From</span>
        {accountsQuery.isLoading ? (
          <span className="text-muted-foreground text-sm">Loading accounts…</span>
        ) : noAccounts ? (
          <span className="text-destructive text-sm">
            {accountsQuery.isError
              ? "Couldn't load your accounts — check your connection."
              : 'Connect a mailbox in Settings to send.'}
          </span>
        ) : accounts.length === 1 ? (
          <span className="truncate text-sm">{fromAccount?.email}</span>
        ) : (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="hover:text-foreground flex items-center gap-1 text-sm"
              >
                <span className="truncate">{fromAccount?.email}</span>
                <ChevronDown className="size-3 shrink-0 opacity-60" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuLabel>Send from</DropdownMenuLabel>
              {accounts.map((account) => (
                <DropdownMenuItem key={account.id} onSelect={() => setFromAccountId(account.id)}>
                  <span className="truncate">{account.email}</span>
                  {account.id === fromAccountId && <Check className="ml-auto size-3.5" />}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {/* Recipients */}
      <ChipsRow
        label="To"
        chips={to}
        onChange={setTo}
        autoFocus={to.length === 0}
        trailing={
          <span className="text-muted-foreground flex gap-2 text-xs">
            {!showCc && (
              <button type="button" className="hover:text-foreground" onClick={() => setShowCc(true)}>
                Cc
              </button>
            )}
            {!showBcc && (
              <button type="button" className="hover:text-foreground" onClick={() => setShowBcc(true)}>
                Bcc
              </button>
            )}
          </span>
        }
      />
      {showCc && <ChipsRow label="Cc" chips={cc} onChange={setCc} />}
      {showBcc && <ChipsRow label="Bcc" chips={bcc} onChange={setBcc} />}

      {/* Subject */}
      <input
        value={subject}
        onChange={(event) => setSubject(event.target.value)}
        placeholder="Subject"
        autoFocus={to.length > 0}
        className="placeholder:text-muted-foreground border-b bg-transparent px-4 py-2.5 text-sm font-medium outline-none"
      />

      {/* Body + snippet popover */}
      <Popover open={snippetQuery !== null && matchingSnippets.length > 0}>
        <PopoverAnchor asChild>
          <div className="px-1">
            <Textarea
              ref={bodyRef}
              value={body}
              onChange={(event) => {
                setBody(event.target.value);
                detectSnippetTrigger(event.target.value, event.target.selectionStart ?? 0);
              }}
              onKeyDown={onBodyKeyDown}
              placeholder={'Write your message…  (type ";" for snippets)'}
              className="min-h-56 resize-y rounded-none border-0 shadow-none focus-visible:ring-0 dark:bg-transparent"
            />
          </div>
        </PopoverAnchor>
        <PopoverContent
          align="start"
          side="top"
          className="w-72 p-1"
          onOpenAutoFocus={(event) => event.preventDefault()}
        >
          <p className="text-muted-foreground px-2 py-1 text-xs">Snippets</p>
          {matchingSnippets.map((snippet, index) => (
            <button
              key={snippet.id}
              type="button"
              className={cn(
                'flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm',
                index === snippetIndex ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'
              )}
              onMouseEnter={() => setSnippetIndex(index)}
              onClick={() => insertSnippet(snippet)}
            >
              <span className="truncate">{snippet.name}</span>
              {snippet.shortcut && <Kbd size="sm">;{snippet.shortcut}</Kbd>}
            </button>
          ))}
        </PopoverContent>
      </Popover>

      {/* Scheduled / reminder chips */}
      {(scheduledAt || remindAt) && (
        <div className="flex flex-wrap items-center gap-2 px-4 pb-2">
          {scheduledAt && (
            <Badge variant="outline" className="gap-1.5 font-normal">
              <Clock className="size-3" />
              Sends {formatOptionTime(scheduledAt)}
              <button type="button" onClick={() => setScheduledAt(null)} aria-label="Cancel send later">
                <X className="size-3" />
              </button>
            </Badge>
          )}
          {remindAt && (
            <Badge variant="outline" className="gap-1.5 font-normal">
              <BellRing className="size-3" />
              Remind if no reply · {remindAt.label.toLowerCase()}
              <button type="button" onClick={() => setRemindAt(null)} aria-label="Cancel reminder">
                <X className="size-3" />
              </button>
            </Badge>
          )}
        </div>
      )}

      {/* Footer */}
      <div className="flex items-center justify-between gap-2 border-t px-3 py-2.5">
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            onClick={() => void handleSend()}
            disabled={sending || accounts.length === 0}
          >
            {sending ? <Loader2 className="animate-spin" /> : <Send />}
            {scheduledAt ? 'Schedule' : 'Send'}
            <span className="ml-1 hidden items-center gap-0.5 opacity-60 sm:flex">
              <Kbd size="sm" className="border-primary-foreground/30 bg-transparent text-inherit">
                {MOD_KEY}
              </Kbd>
              <Kbd size="sm" className="border-primary-foreground/30 bg-transparent text-inherit">
                ↵
              </Kbd>
            </span>
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="gap-1">
                <Clock />
                Send later
                <ChevronDown className="size-3 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuLabel>Send later</DropdownMenuLabel>
              {sendLaterOptions().map((option) => (
                <DropdownMenuItem key={option.id} onSelect={() => setScheduledAt(option.when)}>
                  {option.label}
                  <span className="text-muted-foreground ml-auto text-xs">
                    {formatOptionTime(option.when)}
                  </span>
                </DropdownMenuItem>
              ))}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setScheduleDialogOpen(true)}>
                Pick date &amp; time…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          {showSuggestionChip && suggestion && (
            <Badge variant="outline" className="gap-1.5 font-normal">
              <Clock className="size-3" />
              <button
                type="button"
                className="hover:underline"
                onClick={() => setScheduledAt(new Date(suggestion.suggestedAt))}
              >
                Best time: {format(new Date(suggestion.suggestedAt), 'EEE h:mm a')} — their{' '}
                {localTimeBucket(suggestion.suggestedAt, suggestion.utcOffsetHours)}
              </button>
              <button
                type="button"
                aria-label="Dismiss suggested send time"
                onClick={() =>
                  setDismissedSuggestionKey(`${suggestion.email}|${suggestion.suggestedAt}`)
                }
              >
                <X className="size-3" />
              </button>
            </Badge>
          )}

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="gap-1">
                <BellRing />
                <span className="hidden sm:inline">Remind me</span>
                <ChevronDown className="size-3 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuLabel>Remind me if no reply</DropdownMenuLabel>
              {remindChoices.map((option) => (
                <DropdownMenuItem
                  key={option.id}
                  onSelect={() => setRemindAt({ label: option.label, when: option.when })}
                >
                  {option.label}
                </DropdownMenuItem>
              ))}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setRemindAt(null)}>Off</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {/* AI assist + AI edit menu (both gated on server AI capability) */}
        {aiEnabled && (
          <div className="flex items-center gap-1.5">
            <AiEditMenu ensureDraftId={ensureDraftId} onApplied={(text) => setBody(text)} />
            <Popover open={aiOpen} onOpenChange={setAiOpen}>
              <PopoverTrigger asChild>
                <Button variant="outline" size="sm" className="gap-1.5">
                  <Sparkles />
                  AI assist
                </Button>
              </PopoverTrigger>
              <PopoverContent align="end" className="w-80 p-3">
                <p className="text-sm font-medium">Draft with AI</p>
                <p className="text-muted-foreground mt-0.5 text-xs">
                  Describe what you want to say — tone, ask, context.
                </p>
                <Textarea
                  value={aiPrompt}
                  onChange={(event) => setAiPrompt(event.target.value)}
                  onKeyDown={(event) => {
                    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                      event.preventDefault();
                      void handleAi();
                    }
                  }}
                  placeholder="e.g. polite decline, propose next week instead"
                  className="mt-2 min-h-20 text-sm"
                />
                <div className="mt-2 flex justify-end">
                  <Button size="sm" onClick={() => void handleAi()} disabled={aiBusy || !aiPrompt.trim()}>
                    {aiBusy ? <Loader2 className="animate-spin" /> : <Sparkles />}
                    Draft it
                  </Button>
                </div>
              </PopoverContent>
            </Popover>
          </div>
        )}
      </div>

      <TimePickerDialog
        open={scheduleDialogOpen}
        onOpenChange={setScheduleDialogOpen}
        title="Send later"
        description="The message will be sent automatically."
        options={sendLaterOptions()}
        onPick={(when) => setScheduledAt(when)}
      />
    </div>
  );
}
