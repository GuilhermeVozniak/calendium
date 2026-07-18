import type { AiEventProposal, AttachmentHit, Message, Thread } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import {
  Archive,
  CalendarPlus,
  Clock,
  Loader2,
  MailOpen,
  MessageSquareText,
  Paperclip,
  Reply,
  Send,
  Smile,
  Sparkles,
  Star,
  UserRound,
} from 'lucide-react';
import * as React from 'react';

import { api, fetchAttachmentBlob, orMock } from '@/lib/api';
import { openCompose } from '@/lib/compose';
import {
  createMockEvent,
  mockAiAskCited,
  mockAttachmentBlob,
  mockAttachmentsForThread,
  mockCalendars,
  mockContactSummary,
  mockInstantReplies,
  mockProposeEvent,
  mockReactToMessage,
  mockRemoveReaction,
  mockThread,
} from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Badge } from '@/ui/badge';
import { Button } from '@/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/ui/dropdown';
import { Input } from '@/ui/input';
import { Kbd } from '@/ui/kbd';
import { Tooltip } from '@/ui/tooltip';
import type { MailAction } from '@/views/InboxView';

const REACTION_EMOJIS = ['👍', '❤️', '😂', '🎉', '👀'];

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${kb.toFixed(0)} KB`;
  return `${(kb / 1024).toFixed(1)} MB`;
}

// ---------------------------------------------------------------------------
// Reaction bar (M2.5) — a small emoji palette per message. The delivery badge
// on each chip reflects the *actual* ReactionResult the server (or its mock
// stand-in) returned, not just what "send as reply" was requested — an
// honestly-sourced delivery indicator rather than an assumed one.
// ---------------------------------------------------------------------------

function ReactionBar({ threadId, message }: { threadId: string; message: Message }) {
  const queryClient = useQueryClient();
  const [busyEmoji, setBusyEmoji] = React.useState<string | null>(null);

  function updateReactions(reactions: Message['reactions']) {
    queryClient.setQueryData<{ thread: Thread; messages: Message[] } | undefined>(
      ['thread', threadId],
      (prev) =>
        prev && {
          ...prev,
          messages: prev.messages.map((m) => (m.id === message.id ? { ...m, reactions } : m)),
        }
    );
  }

  async function toggle(emoji: string, sendReply: boolean) {
    const existing = message.reactions.find((r) => r.emoji === emoji);
    setBusyEmoji(emoji);
    try {
      if (existing) {
        await orMock(
          () => api.removeReaction(message.id, emoji),
          () => mockRemoveReaction(message.id, emoji)
        );
        updateReactions(message.reactions.filter((r) => r.emoji !== emoji));
      } else {
        const result = await orMock(
          () => api.reactToMessage(message.id, emoji, sendReply),
          () => mockReactToMessage(message.id, emoji, sendReply)
        );
        updateReactions([...message.reactions.filter((r) => r.emoji !== emoji), result.reaction]);
      }
    } catch (e) {
      toast({ title: 'Could not update the reaction', description: errorMessage(e), variant: 'destructive' });
    } finally {
      setBusyEmoji(null);
    }
  }

  return (
    <div className="mt-2 flex flex-wrap items-center gap-1">
      {message.reactions.map((r) => (
        <Tooltip key={r.id} label={r.delivery === 'sent' ? 'Delivered as a reply' : 'Saved locally only'}>
          <button
            type="button"
            aria-label={r.delivery === 'sent' ? 'Delivered as a reply' : 'Saved locally only'}
            onClick={() => void toggle(r.emoji, false)}
            className="inline-flex items-center gap-1 rounded-full border bg-accent/50 px-2 py-0.5 text-xs"
          >
            <span>{r.emoji}</span>
            {r.delivery === 'sent' && <Send className="size-2.5" />}
          </button>
        </Tooltip>
      ))}
      <DropdownMenu>
        <DropdownMenuTrigger>
          <button
            type="button"
            aria-label={`React to message from ${message.from.name ?? message.from.email}`}
            className="inline-flex items-center justify-center rounded-full border px-1.5 py-0.5 text-muted-foreground hover:bg-accent"
          >
            <Smile className="size-3.5" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel>React</DropdownMenuLabel>
          {REACTION_EMOJIS.map((emoji) => (
            <DropdownMenuItem
              key={emoji}
              disabled={busyEmoji !== null}
              aria-label={
                message.reactions.some((r) => r.emoji === emoji)
                  ? `Remove ${emoji} reaction`
                  : `React with ${emoji}`
              }
              onSelect={() => void toggle(emoji, false)}
            >
              <span className="text-base">{emoji}</span>
              {message.reactions.some((r) => r.emoji === emoji) ? 'Remove' : 'React'}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuLabel>React &amp; reply</DropdownMenuLabel>
          {REACTION_EMOJIS.map((emoji) => (
            <DropdownMenuItem
              key={`send-${emoji}`}
              disabled={busyEmoji !== null}
              aria-label={`React with ${emoji} and send as reply`}
              onSelect={() => void toggle(emoji, true)}
            >
              <span className="text-base">{emoji}</span> Send as reply
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Attachments (M2.5) — a compact chip list scoped to the thread; PDFs open a
// preview dialog that streams the real attachment bytes into a blob iframe.
// ---------------------------------------------------------------------------

function AttachmentPreviewDialog({
  attachment,
  open,
  onOpenChange,
}: {
  attachment: AttachmentHit;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [url, setUrl] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    let objectUrl: string | null = null;
    setLoading(true);
    setError(null);
    orMock(
      () => fetchAttachmentBlob(attachment.id),
      () => mockAttachmentBlob(attachment.id)
    )
      .then((blob) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setUrl(objectUrl);
      })
      .catch((e) => {
        if (!cancelled) setError(errorMessage(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [attachment.id]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="truncate">{attachment.filename}</DialogTitle>
        </DialogHeader>
        <div className="h-[70vh] w-full overflow-hidden rounded-md border bg-muted/30">
          {loading ? (
            <div className="flex h-full items-center justify-center">
              <Loader2 className="size-5 animate-spin text-muted-foreground" />
            </div>
          ) : error ? (
            <div className="flex h-full items-center justify-center p-4 text-center text-sm text-muted-foreground">
              {error}
            </div>
          ) : (
            url && <iframe title={attachment.filename} src={url} className="h-full w-full" />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AttachmentsSection({ threadId }: { threadId: string }) {
  const { data: attachments = [] } = useQuery({
    queryKey: ['attachments', threadId],
    queryFn: () =>
      orMock(
        () => api.searchAttachments({ threadId }).then((r) => r.items),
        () => mockAttachmentsForThread(threadId)
      ),
  });
  const [preview, setPreview] = React.useState<AttachmentHit | null>(null);

  if (attachments.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2">
      <Paperclip className="size-3.5 shrink-0 text-muted-foreground" />
      {attachments.map((a) => {
        const isPdf = a.mimeType === 'application/pdf';
        return (
          <button
            key={a.id}
            type="button"
            onClick={() => isPdf && setPreview(a)}
            className={cn(
              'rounded-full border px-2.5 py-1 text-xs',
              isPdf ? 'hover:bg-accent' : 'cursor-default opacity-80'
            )}
          >
            {a.filename} · {formatBytes(a.sizeBytes)}
          </button>
        );
      })}
      {preview && (
        <AttachmentPreviewDialog
          attachment={preview}
          open
          onOpenChange={(open) => !open && setPreview(null)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Contact summary header (M2.5) — what the local mirror knows about the
// thread's correspondent. Resolution-gated: hidden entirely (rather than
// shown empty) when the server/mock has no history for this address.
// ---------------------------------------------------------------------------

function ContactSummaryHeader({ email }: { email: string }) {
  const { data: contact } = useQuery({
    queryKey: ['contact', email],
    retry: false,
    queryFn: () =>
      orMock(
        () => api.getContact(email),
        () => mockContactSummary(email)
      ),
  });

  if (!contact) return null;

  return (
    <div className="flex items-center gap-1.5 border-b px-4 py-1.5 text-xs text-muted-foreground">
      <UserRound className="size-3.5 shrink-0" />
      <span className="font-medium text-foreground">{contact.name ?? contact.email}</span>
      <span>· {contact.domain}</span>
      <span>
        · {contact.threadCount} thread{contact.threadCount === 1 ? '' : 's'}
      </span>
      <span>
        · {contact.messageCount} message{contact.messageCount === 1 ? '' : 's'}
      </span>
      {contact.lastMessageAt && <span>· last {format(new Date(contact.lastMessageAt), 'MMM d')}</span>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Ask AI — a compact, thread-scoped Q&A dialog (the web app's persistent
// sidebar doesn't fit this app's single-window, view-switching shell).
// ---------------------------------------------------------------------------

function AskAiDialog({
  threadId,
  open,
  onOpenChange,
}: {
  threadId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [question, setQuestion] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [result, setResult] = React.useState<{
    answer: string;
    sources: { threadId: string; subject: string; snippet: string }[];
  } | null>(null);

  React.useEffect(() => {
    if (!open) {
      setQuestion('');
      setResult(null);
    }
  }, [open]);

  async function ask() {
    const q = question.trim();
    if (!q) return;
    setBusy(true);
    try {
      const res = await orMock(
        () => api.aiAskCited({ question: q, threadId }),
        () => mockAiAskCited(q, threadId)
      );
      setResult(res);
    } catch (e) {
      toast({ title: 'AI is unavailable', description: errorMessage(e), variant: 'destructive' });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Ask AI about this thread</DialogTitle>
        </DialogHeader>
        <div className="flex gap-2">
          <Input
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                void ask();
              }
            }}
            placeholder="What's the status of this?"
            disabled={busy}
          />
          <Button size="sm" aria-label="Ask" disabled={busy || !question.trim()} onClick={() => void ask()}>
            {busy ? <Loader2 className="animate-spin" /> : <Send />}
          </Button>
        </div>
        {result && (
          <div className="flex flex-col gap-2 text-sm">
            <p className="whitespace-pre-wrap leading-relaxed">{result.answer}</p>
            {result.sources.length > 0 && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-muted-foreground">Sources</p>
                {result.sources.map((source) => (
                  <div key={source.threadId} className="rounded-md border px-2.5 py-1.5 text-xs">
                    <span className="block truncate font-medium">{source.subject}</span>
                    <span className="block truncate text-muted-foreground">{source.snippet}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Propose event — fetches an AI-drafted event from the thread, lets the user
// review it, and only creates it (via the real createEvent call) on confirm.
// ---------------------------------------------------------------------------

function ProposeEventDialog({
  threadId,
  open,
  onOpenChange,
}: {
  threadId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [loading, setLoading] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [proposal, setProposal] = React.useState<AiEventProposal | null>(null);

  React.useEffect(() => {
    if (!open) {
      setProposal(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    orMock(
      () => api.proposeEvent(threadId),
      () => mockProposeEvent(threadId)
    )
      .then((res) => {
        if (!cancelled) setProposal(res);
      })
      .catch((e) => {
        if (cancelled) return;
        toast({ title: 'AI is unavailable', description: errorMessage(e), variant: 'destructive' });
        onOpenChange(false);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, threadId]);

  async function create() {
    if (!proposal) return;
    setCreating(true);
    try {
      const calendars = await orMock(
        () => api.listCalendars(),
        () => mockCalendars
      );
      const calendar = calendars.find((c) => c.isPrimary && c.canWrite) ?? calendars.find((c) => c.canWrite);
      if (!calendar) throw new Error('No writable calendar available');
      const input = {
        calendarId: calendar.id,
        title: proposal.title,
        start: proposal.start,
        end: proposal.end,
        location: proposal.location,
        description: proposal.notes,
        attendeeEmails: proposal.attendees,
      };
      // Demo branch actually inserts into the mock event store (rather than
      // just resolving) so the created event shows up in the demo calendar —
      // the toast below stays gated on this promise resolving either way.
      await orMock(
        () => api.createEvent(input),
        () => createMockEvent(input)
      );
      toast({ title: 'Event created' });
      onOpenChange(false);
    } catch (e) {
      toast({ title: 'Could not create the event', description: errorMessage(e), variant: 'destructive' });
    } finally {
      setCreating(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Create event with AI</DialogTitle>
        </DialogHeader>
        {loading || !proposal ? (
          <div className="flex flex-col items-center gap-2 py-6 text-center text-sm text-muted-foreground">
            <Loader2 className="size-5 animate-spin" />
            Proposing an event from this thread…
          </div>
        ) : (
          <>
            <div className="flex flex-col gap-1 text-sm">
              <p className="font-medium">{proposal.title}</p>
              <p className="text-xs text-muted-foreground">
                {format(new Date(proposal.start), 'EEE, MMM d, HH:mm')} –{' '}
                {format(new Date(proposal.end), 'HH:mm')}
              </p>
              {proposal.attendees.length > 0 && (
                <p className="text-xs text-muted-foreground">{proposal.attendees.join(', ')}</p>
              )}
              {proposal.notes && <p className="text-xs text-muted-foreground">{proposal.notes}</p>}
            </div>
            <DialogFooter>
              <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button size="sm" disabled={creating} onClick={() => void create()}>
                {creating ? <Loader2 className="animate-spin" /> : null}
                Create event
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function ThreadPane({
  threadId,
  onAction,
}: {
  threadId: string | null;
  onAction: (action: MailAction) => void;
}) {
  const { data } = useQuery({
    queryKey: ['thread', threadId],
    enabled: threadId !== null,
    queryFn: () =>
      orMock(
        () => api.getThread(threadId!),
        () => mockThread(threadId!)
      ),
  });
  const aiEnabled = useServerConfig().config?.features.ai ?? false;
  const [askOpen, setAskOpen] = React.useState(false);
  const [proposeOpen, setProposeOpen] = React.useState(false);
  const [instantReplies, setInstantReplies] = React.useState<string[] | null>(null);
  const [instantRepliesLoading, setInstantRepliesLoading] = React.useState(false);

  React.useEffect(() => {
    if (!aiEnabled || !threadId || !data) return;
    if (data.thread.instantReplies && data.thread.instantReplies.length > 0) {
      setInstantReplies(data.thread.instantReplies);
      return;
    }
    setInstantReplies(null);
    let cancelled = false;
    setInstantRepliesLoading(true);
    orMock(
      () => api.getInstantReplies(threadId).then((res) => res.replies),
      () => mockInstantReplies(threadId)
    )
      .then((replies) => {
        if (!cancelled) setInstantReplies(replies);
      })
      .catch(() => {
        // Non-critical: the chips just don't render.
      })
      .finally(() => {
        if (!cancelled) setInstantRepliesLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [aiEnabled, threadId, data?.thread.id]);

  if (!threadId || !data) {
    return (
      <div className="flex min-w-0 flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
        <MailOpen className="size-8 text-muted-foreground/50" />
        <p className="text-sm font-medium">Select a conversation</p>
        <p className="flex items-center gap-1 text-xs text-muted-foreground">
          Use <Kbd>J</Kbd> and <Kbd>K</Kbd> to move, <Kbd>⌘K</Kbd> for commands
        </p>
      </div>
    );
  }

  const { thread, messages } = data;

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
        <h2 className="min-w-0 truncate text-sm font-semibold">{thread.subject}</h2>
        <Badge variant="outline" className="capitalize">
          {thread.split}
        </Badge>
        <div className="ml-auto flex items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            className="mr-1"
            disabled={messages.length === 0}
            onClick={() =>
              openCompose({ kind: 'reply', thread, message: messages[messages.length - 1]!, messages })
            }
          >
            <Reply /> Reply
          </Button>
          <Tooltip
            label={
              <>
                Archive <Kbd className="bg-primary-foreground/20 text-primary-foreground">E</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Archive" onClick={() => onAction('archive')}>
              <Archive />
            </Button>
          </Tooltip>
          <Tooltip
            label={
              <>
                Star <Kbd className="bg-primary-foreground/20 text-primary-foreground">S</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Star" onClick={() => onAction('star')}>
              <Star className={thread.starred ? 'fill-chart-4 text-chart-4' : ''} />
            </Button>
          </Tooltip>
          <Tooltip
            label={
              <>
                Snooze 3h <Kbd className="bg-primary-foreground/20 text-primary-foreground">H</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Snooze" onClick={() => onAction('snooze')}>
              <Clock />
            </Button>
          </Tooltip>
          {aiEnabled && (
            <>
              <Tooltip label="Ask AI about this thread">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Ask AI"
                  onClick={() => setAskOpen(true)}
                >
                  <MessageSquareText />
                </Button>
              </Tooltip>
              <Tooltip label="Create event with AI">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Create event with AI"
                  onClick={() => setProposeOpen(true)}
                >
                  <CalendarPlus />
                </Button>
              </Tooltip>
            </>
          )}
        </div>
      </header>
      {thread.participants[0] && <ContactSummaryHeader email={thread.participants[0].email} />}
      <AttachmentsSection threadId={thread.id} />
      {aiEnabled && (
        <div className="flex items-start gap-2 border-b px-4 py-2 text-xs text-muted-foreground">
          <Sparkles className="mt-0.5 size-3.5 shrink-0 opacity-70" />
          {thread.summary ? (
            <p className="leading-relaxed">{thread.summary}</p>
          ) : (
            <div className="h-3.5 w-2/3 animate-pulse rounded bg-muted" data-testid="thread-summary-skeleton" />
          )}
        </div>
      )}
      {aiEnabled && !instantRepliesLoading && instantReplies && instantReplies.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-b px-4 py-2">
          {instantReplies.slice(0, 3).map((reply) => (
            <button
              key={reply}
              type="button"
              onClick={() =>
                openCompose({
                  kind: 'reply',
                  thread,
                  message: messages[messages.length - 1]!,
                  body: reply,
                  messages,
                })
              }
              className="max-w-64 truncate rounded-full border px-2.5 py-1 text-xs hover:bg-accent"
            >
              {reply}
            </button>
          ))}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-2xl flex-col gap-3 p-4">
          {messages.map((message) => (
            <article key={message.id} className="rounded-lg border bg-card p-4 shadow-sm">
              <div className="mb-2 flex items-baseline gap-2">
                <span className="text-sm font-medium">
                  {message.from.name ?? message.from.email}
                </span>
                <span className="truncate text-xs text-muted-foreground">{message.from.email}</span>
                <span className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">
                  {format(new Date(message.sentAt), 'MMM d, HH:mm')}
                </span>
              </div>
              <div className="whitespace-pre-wrap text-sm leading-relaxed">{message.bodyText}</div>
              <ReactionBar threadId={thread.id} message={message} />
            </article>
          ))}
        </div>
      </div>
      {aiEnabled && (
        <>
          <AskAiDialog threadId={thread.id} open={askOpen} onOpenChange={setAskOpen} />
          <ProposeEventDialog threadId={thread.id} open={proposeOpen} onOpenChange={setProposeOpen} />
        </>
      )}
    </div>
  );
}
