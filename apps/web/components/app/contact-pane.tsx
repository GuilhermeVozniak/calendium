'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { Mail, Paperclip, X } from 'lucide-react';

import { useCompose } from '@/components/app/compose';
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Skeleton } from '@/components/ui/skeleton';
import { companyFromDomain, faviconUrl, gravatarUrl } from '@/lib/contact-utils';
import { formatListTime, initials } from '@/lib/mail-utils';
import { useContact } from '@/lib/use-mail';

/**
 * Fired by the "Search attachments from" quick action. Kept as a plain DOM
 * event (matching the mail-utils.ts `dispatchMailCommand` convention) rather
 * than a direct prop/import so this pane doesn't need to depend on the
 * attachment-search pane's module — the future consumer just listens.
 */
export const CONTACT_SEARCH_ATTACHMENTS_EVENT = 'calendium:search-attachments-from';

export interface SearchAttachmentsFromDetail {
  contact: string;
}

export function dispatchSearchAttachmentsFrom(email: string): void {
  window.dispatchEvent(
    new CustomEvent<SearchAttachmentsFromDetail>(CONTACT_SEARCH_ATTACHMENTS_EVENT, {
      detail: { contact: email },
    })
  );
}

/** Short, honest relative-time label — no external formatting dependency. */
function relativeTime(iso: string | null): string {
  if (!iso) return 'never';
  const diffSec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diffSec < 60) return 'just now';
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHour = Math.round(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h ago`;
  const diffDay = Math.round(diffHour / 24);
  if (diffDay < 30) return `${diffDay}d ago`;
  const diffMonth = Math.round(diffDay / 30);
  if (diffMonth < 12) return `${diffMonth}mo ago`;
  return `${Math.round(diffMonth / 12)}y ago`;
}

interface ContactAvatarProps {
  email: string;
  domain: string;
  name: string | null;
}

/** gravatar -> favicon -> initials fallback chain, advanced on each image error. */
function ContactAvatar({ email, domain, name }: ContactAvatarProps) {
  const [stage, setStage] = React.useState<'gravatar' | 'favicon' | 'initials'>('gravatar');
  const [gravatar, setGravatar] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    setStage('gravatar');
    setGravatar(null);
    void gravatarUrl(email).then((url) => {
      if (!cancelled) setGravatar(url);
    });
    return () => {
      cancelled = true;
    };
  }, [email]);

  const src = stage === 'gravatar' ? gravatar : stage === 'favicon' ? faviconUrl(domain) : null;

  return (
    <Avatar className="size-12">
      {src && (
        <AvatarImage
          src={src}
          alt=""
          onError={() => setStage((prev) => (prev === 'gravatar' ? 'favicon' : 'initials'))}
        />
      )}
      <AvatarFallback className="text-sm">{initials({ name, email })}</AvatarFallback>
    </Avatar>
  );
}

export interface ContactPaneProps {
  email: string;
  onClose: () => void;
}

/**
 * Sidebar contact-insights pane: avatar, name/email/company, aggregate stats,
 * and the 5 most recent conversations — all sourced from
 * GET /v1/mail/contacts/{email} (the local mirror, backend-canonicalized).
 * Loading, error, and "never talked to this person" states are shown
 * honestly; nothing here is fabricated when the API has no data.
 */
export function ContactPane({ email, onClose }: ContactPaneProps) {
  const router = useRouter();
  const { openCompose } = useCompose();
  const { data, isLoading, isError } = useContact(email);

  const contact = data?.contact ?? null;
  const company = contact ? companyFromDomain(contact.domain) : '';

  return (
    <aside className="flex h-full w-72 shrink-0 flex-col border-l">
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-2.5">
        <h2 className="text-sm font-semibold">Contact</h2>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          aria-label="Close contact pane"
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>

      <ScrollArea className="min-h-0 flex-1">
        <div className="flex flex-col gap-4 p-4">
          {isLoading && (
            <div className="flex flex-col gap-3">
              <Skeleton className="size-12 rounded-full" />
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-3 w-1/2" />
            </div>
          )}

          {!isLoading && isError && (
            <p className="text-muted-foreground text-sm">
              Couldn&apos;t load contact info. Please try again.
            </p>
          )}

          {!isLoading && !isError && !contact && (
            <p className="text-muted-foreground text-sm">No conversations with this contact yet.</p>
          )}

          {!isLoading && !isError && contact && (
            <>
              <div className="flex flex-col items-start gap-2">
                <ContactAvatar email={contact.email} domain={contact.domain} name={contact.name} />
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold">{contact.name ?? contact.email}</p>
                  <p className="text-muted-foreground truncate text-xs">{contact.email}</p>
                  {company && <p className="text-muted-foreground truncate text-xs">{company}</p>}
                </div>
              </div>

              <p className="text-muted-foreground text-xs">
                {`${contact.threadCount} conversations · ${contact.messageCount} messages · last ${relativeTime(contact.lastMessageAt)}`}
              </p>

              <div className="flex flex-col gap-1.5">
                <Button
                  size="sm"
                  className="justify-start gap-2"
                  onClick={() =>
                    openCompose({ to: [{ name: contact.name, email: contact.email }] })
                  }
                >
                  <Mail className="size-3.5" />
                  Compose to
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  className="justify-start gap-2"
                  onClick={() => dispatchSearchAttachmentsFrom(contact.email)}
                >
                  <Paperclip className="size-3.5" />
                  Search attachments from
                </Button>
              </div>

              {contact.recentThreads.length > 0 && (
                <div className="flex flex-col gap-1">
                  <h3 className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
                    Recent conversations
                  </h3>
                  <ul className="flex flex-col gap-0.5">
                    {contact.recentThreads.map((thread) => (
                      <li key={thread.id}>
                        <button
                          type="button"
                          className="hover:bg-accent/50 flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left"
                          onClick={() => router.push(`/mail?t=${encodeURIComponent(thread.id)}`)}
                        >
                          <span className="w-full truncate text-xs font-medium">
                            {thread.subject}
                          </span>
                          <span className="text-muted-foreground w-full truncate text-[0.6875rem]">
                            {formatListTime(thread.lastMessageAt)}
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </>
          )}
        </div>
      </ScrollArea>
    </aside>
  );
}
