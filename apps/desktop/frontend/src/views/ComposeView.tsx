import type { AiEditAction, Draft, EmailAddress } from '@calendium/shared';
import { ApiRequestError, buildInstantIntro } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { format } from 'date-fns';
import { ChevronDown, Clock, Loader2, Send, Sparkles, UserRoundPlus, Zap } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { applySignature, type ComposeIntent, onOpenCompose, toHtml } from '@/lib/compose';
import { mockAccounts, mockSendSuggestion, mockUser } from '@/lib/mock';
import { getActiveServerConfig, isDemoMode } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Button } from '@/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/ui/dropdown';
import { Input } from '@/ui/input';

const AI_EDIT_ACTIONS: { action: AiEditAction; label: string }[] = [
  { action: 'improve', label: 'Improve' },
  { action: 'shorten', label: 'Shorten' },
  { action: 'simplify', label: 'Simplify' },
  { action: 'fix_grammar', label: 'Fix grammar' },
];

function parseAddresses(value: string): EmailAddress[] {
  return value
    .split(/[,;\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((email) => ({ name: null, email }));
}

function ensureRe(subject: string): string {
  return /^re:/i.test(subject.trim()) ? subject : `Re: ${subject}`;
}

/** yyyy-MM-ddTHH:mm, the value shape <input type="datetime-local"> expects. */
function toDatetimeLocalValue(isoDate: string): string {
  return format(new Date(isoDate), "yyyy-MM-dd'T'HH:mm");
}

/**
 * The composer: blank new mail or a reply seeded from ThreadPane. Saves a draft
 * (createDraft/saveDraft) with the signed-in account from listAccounts, sends
 * it, and shows a post-send Undo toast for undoSendSeconds (contract item 4).
 * Mount once at the app root; it listens for openCompose().
 */
export function ComposeHost() {
  const [intent, setIntent] = useState<ComposeIntent | null>(null);
  const [accountId, setAccountId] = useState('');
  const [to, setTo] = useState('');
  const [cc, setCc] = useState('');
  const [showCc, setShowCc] = useState(false);
  const [bcc, setBcc] = useState('');
  const [showBcc, setShowBcc] = useState(false);
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleAt, setScheduleAt] = useState('');
  const [busy, setBusy] = useState<null | 'send' | 'draft'>(null);
  // A real draftId, seeded lazily by ensureDraftId() the first time AI edit
  // needs one (aiEditDraft edits an existing draft in place). Once set,
  // submit() updates that same draft instead of creating a duplicate.
  const [liveDraftId, setLiveDraftId] = useState<string | null>(null);
  const [aiEditBusy, setAiEditBusy] = useState(false);
  const [toneOpen, setToneOpen] = useState(false);
  const [tone, setTone] = useState('');

  const { data: accounts = [] } = useQuery({
    queryKey: ['accounts'],
    enabled: intent !== null,
    queryFn: () => orMock(() => api.listAccounts(), () => mockAccounts),
  });
  const { data: me } = useQuery({
    queryKey: ['me'],
    enabled: intent !== null,
    queryFn: () => orMock(() => api.getMe(), () => mockUser),
  });

  useEffect(() => onOpenCompose(setIntent), []);

  // Seed the form whenever the composer opens.
  useEffect(() => {
    if (!intent) return;
    setLiveDraftId(null);
    if (intent.kind === 'reply') {
      setTo(intent.message.from.email);
      setCc('');
      setShowCc(false);
      setBcc('');
      setShowBcc(false);
      setSubject(ensureRe(intent.thread.subject));
    } else {
      setTo('');
      setCc('');
      setShowCc(false);
      setBcc('');
      setShowBcc(false);
      setSubject('');
    }
    setBody(intent.kind === 'reply' ? (intent.body ?? '') : '');
    setScheduleOpen(false);
    setScheduleAt('');
    setBusy(null);
    setToneOpen(false);
    setTone('');
  }, [intent]);

  // Default the sending account: the reply's thread account if known, else first.
  useEffect(() => {
    if (!intent || accounts.length === 0) return;
    const preferred =
      intent.kind === 'reply' && accounts.some((a) => a.id === intent.thread.accountId)
        ? intent.thread.accountId
        : accounts[0]!.id;
    setAccountId(preferred);
  }, [intent, accounts]);

  // Auto-applies (and swaps) the selected account's signature onto the body:
  // fires on open and whenever the From account changes, so switching
  // accounts mid-compose replaces the signature without disturbing anything
  // typed above it (lib/compose.ts's applySignature is swap-based).
  useEffect(() => {
    if (!intent || !accountId) return;
    const account = accounts.find((a) => a.id === accountId);
    if (!account) return;
    setBody((prev) => applySignature(prev, account.signatureHtml));
  }, [intent, accountId, accounts]);

  // Smart Send (M2.5): debounce the first "to" recipient and look up a
  // suggested send time. Resolution-gated — a thin-history 404 (real or mock)
  // simply means no suggestion, so the chip stays hidden rather than guessing.
  const [debouncedToEmail, setDebouncedToEmail] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setDebouncedToEmail(parseAddresses(to)[0]?.email ?? ''), 300);
    return () => clearTimeout(t);
  }, [to]);
  const { data: sendSuggestion } = useQuery({
    queryKey: ['send-suggestion', debouncedToEmail],
    enabled: intent !== null && debouncedToEmail.length > 0,
    retry: false,
    queryFn: () =>
      orMock(
        () => api.getSendSuggestion(debouncedToEmail),
        () => {
          const suggestion = mockSendSuggestion(debouncedToEmail);
          if (!suggestion) throw new ApiRequestError(404, 'not_found', 'No send suggestion available.');
          return suggestion;
        }
      ),
  });

  // Instant Intro (M2.5): only meaningful for a reply carrying the full
  // thread (ThreadPane's Reply button passes `messages`), and only when it
  // actually resolves to a third participant to move the introducer's BCC to.
  const introDraft = useMemo(() => {
    if (intent?.kind !== 'reply' || !intent.messages || !me?.email) return null;
    return buildInstantIntro(intent.thread, intent.messages, me.email);
  }, [intent, me?.email]);

  function applyInstantIntro() {
    if (!introDraft) return;
    setTo(introDraft.to.map((a) => a.email).join(', '));
    if (introDraft.bcc.length > 0) {
      setBcc(introDraft.bcc.map((a) => a.email).join(', '));
      setShowBcc(true);
    }
    setSubject(introDraft.subject);
    setBody(introDraft.body);
  }

  function close() {
    setIntent(null);
  }

  function buildInput(scheduledAt: string | null) {
    return {
      accountId,
      threadId: intent?.kind === 'reply' ? intent.thread.id : null,
      to: parseAddresses(to),
      cc: showCc ? parseAddresses(cc) : [],
      bcc: showBcc ? parseAddresses(bcc) : ([] as EmailAddress[]),
      subject,
      bodyHtml: toHtml(body),
      scheduledAt,
    };
  }

  async function persistDraft(scheduledAt: string | null): Promise<Draft> {
    const input = buildInput(scheduledAt);
    // Once a draft id exists (reopened via AI edit), update it in place rather
    // than creating a duplicate.
    if (liveDraftId) {
      if (isDemoMode()) {
        return {
          id: liveDraftId,
          ...input,
          sendAttempts: 0,
          lastError: null,
          aiGenerated: false,
          updatedAt: new Date().toISOString(),
        };
      }
      return api.updateDraft(liveDraftId, input);
    }
    if (isDemoMode()) {
      return {
        id: `draft_demo_${Date.now()}`,
        ...input,
        sendAttempts: 0,
        aiGenerated: false,
        lastError: null,
        updatedAt: new Date().toISOString(),
      };
    }
    return api.saveDraft(input);
  }

  /**
   * aiEditDraft needs a real draftId; a brand-new compose session doesn't have
   * one until the first AI edit lazily persists the current form as a draft
   * and remembers the id for the rest of the session (submit() then updates
   * that same draft instead of creating a duplicate — see persistDraft above).
   */
  async function ensureDraftId(): Promise<string> {
    if (liveDraftId) return liveDraftId;
    const draft = await persistDraft(null);
    setLiveDraftId(draft.id);
    return draft.id;
  }

  async function runAiEdit(action: AiEditAction, toneValue?: string) {
    setAiEditBusy(true);
    try {
      const draftId = await ensureDraftId();
      const res = await orMock(
        () => api.aiEditDraft(action, draftId, toneValue),
        () => ({ text: `${body || 'Draft'} (edited — demo mode, no AI configured)`, model: 'demo/local-fallback' })
      );
      setBody(res.text);
      setToneOpen(false);
      setTone('');
    } catch (e) {
      toast({ title: 'AI edit failed', description: errorMessage(e), variant: 'destructive' });
    } finally {
      setAiEditBusy(false);
    }
  }

  function showSentToast(draftId: string, scheduledAt: string | null) {
    const seconds = getActiveServerConfig()?.undoSendSeconds ?? 15;
    toast({
      title: scheduledAt ? 'Scheduled to send' : 'Message sent',
      description: `Undo within ${seconds}s.`,
      durationMs: seconds * 1000,
      action: { label: 'Undo', onClick: () => undoSend(draftId) },
    });
  }

  async function undoSend(draftId: string) {
    try {
      if (!isDemoMode() && draftId) await api.unsendDraft(draftId);
      toast({ title: 'Send undone', description: 'Your draft was restored.' });
    } catch (e) {
      if (e instanceof ApiRequestError && e.status === 409) {
        toast({
          title: 'Already sent',
          description: 'The message left before you could undo it.',
          variant: 'destructive',
        });
      } else {
        toast({ title: 'Undo failed', description: errorMessage(e), variant: 'destructive' });
      }
    }
  }

  async function submit(mode: 'send' | 'draft', scheduledAt: string | null) {
    if (!accountId) {
      toast({ title: 'No account', description: 'Connect a mailbox first.', variant: 'destructive' });
      return;
    }
    if (mode === 'send' && parseAddresses(to).length === 0) {
      toast({ title: 'Add a recipient', description: 'Enter at least one address.', variant: 'destructive' });
      return;
    }
    setBusy(mode);
    try {
      const draft = await persistDraft(scheduledAt);
      if (mode === 'draft') {
        toast({ title: 'Draft saved' });
        close();
        return;
      }
      if (!isDemoMode()) await api.sendDraft(draft.id);
      close();
      showSentToast(draft.id, scheduledAt);
    } catch (e) {
      setBusy(null);
      toast({
        title: scheduledAt ? 'Could not schedule' : 'Could not send',
        description: errorMessage(e),
        variant: 'destructive',
      });
    }
  }

  const fieldClass =
    'flex h-8 w-full rounded-md border border-input bg-transparent px-2.5 py-1 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';

  return (
    <Dialog open={intent !== null} onOpenChange={(open) => (open ? undefined : close())}>
      <DialogContent className="max-w-2xl gap-3">
        <DialogHeader>
          <DialogTitle>{intent?.kind === 'reply' ? 'Reply' : 'New message'}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          {accounts.length > 1 && (
            <div className="flex items-center gap-2">
              <span className="w-12 shrink-0 text-xs text-muted-foreground">From</span>
              <select
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
                className={cn(fieldClass, 'cursor-default')}
              >
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.email}
                  </option>
                ))}
              </select>
            </div>
          )}

          <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 text-xs text-muted-foreground">To</span>
            <Input
              aria-label="To"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="name@example.com"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
            />
            {!showCc && (
              <button
                type="button"
                className="shrink-0 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => setShowCc(true)}
              >
                Cc
              </button>
            )}
          </div>

          {showCc && (
            <div className="flex items-center gap-2">
              <span className="w-12 shrink-0 text-xs text-muted-foreground">Cc</span>
              <Input
                aria-label="Cc"
                value={cc}
                onChange={(e) => setCc(e.target.value)}
                placeholder="name@example.com"
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
              />
            </div>
          )}

          {showBcc && (
            <div className="flex items-center gap-2">
              <span className="w-12 shrink-0 text-xs text-muted-foreground">Bcc</span>
              <Input
                aria-label="Bcc"
                value={bcc}
                onChange={(e) => setBcc(e.target.value)}
                placeholder="name@example.com"
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
              />
            </div>
          )}

          {introDraft && (
            <button
              type="button"
              onClick={applyInstantIntro}
              className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <UserRoundPlus className="size-3" />
              Instant Intro — reply-all, move {introDraft.bcc[0]?.name ?? introDraft.bcc[0]?.email} to BCC
            </button>
          )}

          <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 text-xs text-muted-foreground">Subject</span>
            <Input
              aria-label="Subject"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder="Subject"
            />
          </div>

          <textarea
            aria-label="Body"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Write your message…"
            rows={12}
            className="min-h-48 w-full resize-none rounded-md border border-input bg-transparent px-2.5 py-2 text-sm leading-relaxed shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />

          {scheduleOpen && (
            <div className="flex items-center gap-2">
              <span className="w-12 shrink-0 text-xs text-muted-foreground">Send at</span>
              <input
                type="datetime-local"
                value={scheduleAt}
                onChange={(e) => setScheduleAt(e.target.value)}
                className={fieldClass}
              />
              <Button
                size="sm"
                disabled={busy !== null || !scheduleAt}
                onClick={() => submit('send', new Date(scheduleAt).toISOString())}
              >
                Schedule
              </Button>
            </div>
          )}

          {toneOpen && (
            <div className="flex items-center gap-2">
              <span className="w-12 shrink-0 text-xs text-muted-foreground">Tone</span>
              <Input
                aria-label="Target tone"
                value={tone}
                autoFocus
                onChange={(e) => setTone(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && tone.trim()) {
                    e.preventDefault();
                    void runAiEdit('change_tone', tone.trim());
                  }
                }}
                placeholder="e.g. more formal"
                disabled={aiEditBusy}
              />
              <Button
                size="sm"
                disabled={aiEditBusy || !tone.trim()}
                onClick={() => void runAiEdit('change_tone', tone.trim())}
              >
                {aiEditBusy ? <Loader2 className="animate-spin" /> : null}
                Apply
              </Button>
            </div>
          )}
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" disabled={busy !== null} onClick={() => submit('send', null)}>
            {busy === 'send' ? <Loader2 className="animate-spin" /> : <Send />}
            Send
          </Button>
          <Button
            variant="ghost"
            size="sm"
            aria-label="Send later"
            onClick={() => setScheduleOpen((v) => !v)}
          >
            <Clock /> Send later
          </Button>
          {sendSuggestion && (
            <Button
              variant="ghost"
              size="sm"
              aria-label="Smart Send"
              onClick={() => {
                setScheduleOpen(true);
                setScheduleAt(toDatetimeLocalValue(sendSuggestion.suggestedAt));
              }}
            >
              <Zap /> Smart Send · {format(new Date(sendSuggestion.suggestedAt), 'MMM d, HH:mm')}
            </Button>
          )}
          {getActiveServerConfig()?.features.ai && (
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button variant="outline" size="sm" disabled={aiEditBusy}>
                  {aiEditBusy ? <Loader2 className="animate-spin" /> : <Sparkles />}
                  Edit with AI
                  <ChevronDown className="size-3 opacity-60" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuLabel>Edit with AI</DropdownMenuLabel>
                {AI_EDIT_ACTIONS.map(({ action, label }) => (
                  <DropdownMenuItem key={action} onSelect={() => void runAiEdit(action)}>
                    {label}
                  </DropdownMenuItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setToneOpen(true)}>Change tone…</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            disabled={busy !== null}
            onClick={() => submit('draft', scheduleOpen && scheduleAt ? new Date(scheduleAt).toISOString() : null)}
          >
            {busy === 'draft' ? <Loader2 className="animate-spin" /> : null}
            Save draft
          </Button>
          <Button variant="ghost" size="sm" disabled={busy !== null} onClick={close}>
            Discard
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
