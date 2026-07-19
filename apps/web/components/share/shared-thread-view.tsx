'use client';

import * as React from 'react';
import type { Message, SharedThreadView as SharedThreadViewData } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link2Off, Loader2, Users } from 'lucide-react';

import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { getApiClient } from '@/lib/api';
import { openCollabStream } from '@/lib/collab-stream';
import { DEMO_MODE } from '@/lib/demo';
import { displayName, formatFullTime, initials } from '@/lib/mail-utils';
import { cn } from '@/lib/utils';

/** Splits a plain-text body into visible text and collapsed quoted history
 * (same heuristic as the app thread view — bodies render as plain text,
 * never as raw HTML). */
function splitQuoted(bodyText: string): { main: string; quoted: string | null } {
  const lines = bodyText.split('\n');
  const index = lines.findIndex(
    (line) => /^On .+ wrote:\s*$/.test(line.trim()) || line.startsWith('>')
  );
  if (index === -1) return { main: bodyText, quoted: null };
  return {
    main: lines.slice(0, index).join('\n').trimEnd(),
    quoted: lines.slice(index).join('\n').trim(),
  };
}

function recipientsLine(message: Message): string {
  const to = message.to.map(displayName);
  const cc = message.cc.map(displayName);
  return `to ${to.join(', ')}${cc.length ? `, cc ${cc.join(', ')}` : ''}`;
}

/** Canned view for explicit demo mode only (honesty policy — see lib/demo.ts). */
const DEMO_VIEW: SharedThreadViewData = {
  subject: 'Q3 launch plan — final review',
  audience: 'external',
  updatedAt: new Date().toISOString(),
  messages: [
    {
      id: 'demo_m1',
      threadId: 'demo_t1',
      accountId: 'demo_acc',
      from: { name: 'Priya Raman', email: 'priya@example.com' },
      to: [{ name: 'Jordan Lee', email: 'jordan@example.com' }],
      cc: [],
      bcc: [],
      subject: 'Q3 launch plan — final review',
      bodyHtml: '',
      bodyText:
        'Sharing the final launch checklist. Design sign-off landed this morning; the only open item is the pricing page copy.',
      attachments: [],
      sentAt: new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString(),
      isDraft: false,
      openedAt: null,
      reactions: [],
    },
    {
      id: 'demo_m2',
      threadId: 'demo_t1',
      accountId: 'demo_acc',
      from: { name: 'Jordan Lee', email: 'jordan@example.com' },
      to: [{ name: 'Priya Raman', email: 'priya@example.com' }],
      cc: [],
      bcc: [],
      subject: 'Re: Q3 launch plan — final review',
      bodyHtml: '',
      bodyText: 'Copy is done — shipping it after the standup. We are go for Thursday.',
      attachments: [],
      sentAt: new Date(Date.now() - 30 * 60 * 1000).toISOString(),
      isDraft: false,
      openedAt: null,
      reactions: [],
    },
  ],
};

function ReadOnlyMessage({ message, index }: { message: Message; index: number }) {
  const [quotedShown, setQuotedShown] = React.useState(false);
  const { main, quoted } = splitQuoted(message.bodyText);
  return (
    <div className={cn('px-4 py-4', index > 0 && 'border-t')}>
      <div className="flex items-start gap-3">
        <Avatar className="mt-0.5 size-8">
          <AvatarFallback className="text-xs">{initials(message.from)}</AvatarFallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline justify-between gap-2">
            <span className="truncate text-sm font-semibold">
              {displayName(message.from)}
              <span className="text-muted-foreground ml-2 hidden text-xs font-normal sm:inline">
                {message.from.email}
              </span>
            </span>
            <span className="text-muted-foreground shrink-0 text-xs">
              {formatFullTime(message.sentAt)}
            </span>
          </div>
          <p className="text-muted-foreground truncate text-xs">{recipientsLine(message)}</p>
        </div>
      </div>

      <div className="mt-3 text-sm leading-relaxed whitespace-pre-wrap">{main}</div>

      {quoted && (
        <div className="mt-3">
          <button
            type="button"
            className="bg-muted text-muted-foreground hover:bg-accent inline-flex h-5 items-center rounded-full px-2 text-xs"
            onClick={() => setQuotedShown((shown) => !shown)}
            aria-label="Toggle quoted text"
          >
            •••
          </button>
          {quotedShown && (
            <div className="text-muted-foreground mt-2 border-l-2 pl-3 text-sm whitespace-pre-wrap">
              {quoted}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function CenteredNotice({
  title,
  description,
  icon,
}: {
  title: string;
  description?: string;
  icon?: React.ReactNode;
}) {
  return (
    <div className="mx-auto flex max-w-md flex-col items-center gap-3 px-4 py-24 text-center">
      {icon}
      <h1 className="text-lg font-semibold">{title}</h1>
      {description ? <p className="text-muted-foreground text-sm">{description}</p> : null}
    </div>
  );
}

/**
 * Public, read-only live view of a shared conversation
 * (GET /v1/shared/threads/{token}). Works signed out for external shares;
 * team-audience shares authenticate via the ApiClient's optional bearer.
 * Live refresh is SSE-driven: `share.updated` events on the token-scoped
 * stream invalidate the ['shared-thread', token] query (no polling).
 * Unknown, revoked, and expired links all render the same inactive state —
 * the server never explains which (no oracle), and neither do we.
 * The raw token is a secret: it appears only in the API request itself,
 * never in logs or analytics.
 */
export function SharedThreadView({ token }: { token: string }) {
  const queryClient = useQueryClient();

  const viewQuery = useQuery<SharedThreadViewData, Error>({
    queryKey: ['shared-thread', token],
    queryFn: () => (DEMO_MODE ? Promise.resolve(DEMO_VIEW) : getApiClient().getSharedThread(token)),
    retry: false,
  });

  React.useEffect(() => {
    if (DEMO_MODE) return;
    return openCollabStream(
      (ev) => {
        if (ev.type === 'share.updated') {
          void queryClient.invalidateQueries({ queryKey: ['shared-thread', token] });
        }
      },
      { path: `/v1/shared/threads/${encodeURIComponent(token)}/stream` }
    );
  }, [token, queryClient]);

  if (viewQuery.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Loader2 className="text-muted-foreground size-6 animate-spin" />
      </div>
    );
  }

  if (viewQuery.isError) {
    if (viewQuery.error instanceof ApiRequestError && viewQuery.error.status === 404) {
      return (
        <CenteredNotice
          icon={<Link2Off className="text-muted-foreground size-6" />}
          title="This link is no longer active"
          description="The conversation may have been unshared, or the link may have expired. Ask the person who shared it for a new link."
        />
      );
    }
    return (
      <CenteredNotice
        title="Something went wrong"
        description="We couldn't load this conversation. Please try again in a moment."
      />
    );
  }

  const view = viewQuery.data;

  return (
    <div className="mx-auto flex min-h-svh w-full max-w-3xl flex-col px-4 py-10">
      <header>
        <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
          Shared conversation · read-only
        </p>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <h1 className="text-xl font-semibold tracking-tight">{view.subject}</h1>
          {view.audience === 'team' && (
            <Badge variant="outline" className="gap-1">
              <Users className="size-3" />
              Team
            </Badge>
          )}
        </div>
        <p className="text-muted-foreground mt-1 text-xs">
          Live view · updated {formatFullTime(view.updatedAt)}
        </p>
      </header>

      <div className="mt-6 flex flex-col rounded-lg border">
        {view.messages.length === 0 ? (
          <p className="text-muted-foreground px-4 py-12 text-center text-sm">
            No messages in this conversation yet.
          </p>
        ) : (
          view.messages.map((message, index) => (
            <ReadOnlyMessage key={message.id} message={message} index={index} />
          ))
        )}
      </div>

      <p className="text-muted-foreground mt-6 text-center text-xs">
        Shared live from Calendium — this page updates as the conversation does.
      </p>
    </div>
  );
}
