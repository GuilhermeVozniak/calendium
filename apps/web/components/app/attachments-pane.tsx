'use client';

import * as React from 'react';
import {
  Download,
  File,
  FileSpreadsheet,
  FileText,
  Image as ImageIcon,
  Loader2,
  Lock,
  Paperclip,
  Search,
  X,
} from 'lucide-react';

import type { AttachmentHit } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { toast } from 'sonner';

import { CONTACT_SEARCH_ATTACHMENTS_EVENT } from '@/components/app/contact-pane';
import { useCheckoutMutation } from '@/components/app/paywall';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import { formatListTime } from '@/lib/mail-utils';
import { fetchAttachmentBlob, useAttachmentSearch } from '@/lib/use-mail';

/**
 * Attachment quick access + inline preview (M2.5, Task 16). Search input
 * debounces into `useAttachmentSearch`; results are grouped by thread; a
 * click either opens an inline preview dialog (PDF via native `<iframe>`,
 * images via `<img>`) or downloads the file, depending on mime type. Real
 * bytes only — the preview/download always comes from the server's (or, in
 * DEMO_MODE-fallback, the fixture's) actual content bytes, never fabricated.
 */

// ---------------------------------------------------------------------------
// Provider — mounted once in the app shell (apps/web/app/(app)/layout.tsx) so
// any surface (command palette today; a future thread-view "view all
// attachments from {sender}" affordance) can open the pane pre-filtered
// without needing the mail list mounted.
// ---------------------------------------------------------------------------

export interface AttachmentsPaneFilter {
  contact?: string;
  threadId?: string;
}

interface AttachmentsPaneContextValue {
  open: (filter?: AttachmentsPaneFilter) => void;
}

const AttachmentsPaneContext = React.createContext<AttachmentsPaneContextValue | null>(null);

export function useAttachmentsPane(): AttachmentsPaneContextValue {
  const ctx = React.useContext(AttachmentsPaneContext);
  if (!ctx) throw new Error('useAttachmentsPane must be used within an <AttachmentsPaneProvider>');
  return ctx;
}

export function AttachmentsPaneProvider({ children }: { children: React.ReactNode }) {
  const [isOpen, setIsOpen] = React.useState(false);
  const [filter, setFilter] = React.useState<AttachmentsPaneFilter>({});
  const [session, setSession] = React.useState(0);

  const open = React.useCallback((next?: AttachmentsPaneFilter) => {
    setFilter(next ?? {});
    setSession((s) => s + 1);
    setIsOpen(true);
  }, []);

  // Contact pane's "Search attachments from" quick action dispatches this
  // CustomEvent (see contact-pane.tsx) because the two features shipped from
  // isolated worktrees; the provider is the stable end of the bridge.
  React.useEffect(() => {
    const onSearchFrom = (e: Event) => {
      const contact = (e as CustomEvent<{ contact?: string }>).detail?.contact;
      if (contact) open({ contact });
    };
    window.addEventListener(CONTACT_SEARCH_ATTACHMENTS_EVENT, onSearchFrom);
    return () => window.removeEventListener(CONTACT_SEARCH_ATTACHMENTS_EVENT, onSearchFrom);
  }, [open]);

  const value = React.useMemo(() => ({ open }), [open]);

  return (
    <AttachmentsPaneContext.Provider value={value}>
      {children}
      <Dialog open={isOpen} onOpenChange={setIsOpen}>
        <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl" showCloseButton>
          <DialogTitle className="sr-only">Attachments</DialogTitle>
          <DialogDescription className="sr-only">
            Search, preview, and download attachments across your mailbox
          </DialogDescription>
          <AttachmentsPane key={session} initialFilter={filter} />
        </DialogContent>
      </Dialog>
    </AttachmentsPaneContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

/** Formats a byte count as a human-readable size (e.g. "2.4 MB"). */
export function humanSize(sizeBytes: number): string {
  if (!Number.isFinite(sizeBytes) || sizeBytes < 0) return '—';
  if (sizeBytes < 1024) return `${sizeBytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = sizeBytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unitIndex]}`;
}

function isPdf(mimeType: string): boolean {
  return mimeType === 'application/pdf';
}

function isImage(mimeType: string): boolean {
  return mimeType.startsWith('image/');
}

function isPreviewable(mimeType: string): boolean {
  return isPdf(mimeType) || isImage(mimeType);
}

/** Maps a failed attachment fetch to friendly copy — 402 gets the paywall message, everything else a generic retry prompt. */
function attachmentErrorMessage(err: unknown): string {
  if (err instanceof ApiRequestError && err.status === 402) {
    return 'Attachments are part of the paid Calendium plan.';
  }
  return 'Could not load the attachment. Please try again.';
}

function MimeIcon({ mimeType }: { mimeType: string }) {
  if (isPdf(mimeType)) return <FileText aria-hidden className="size-4 shrink-0 text-red-600 dark:text-red-400" />;
  if (isImage(mimeType)) return <ImageIcon aria-hidden className="size-4 shrink-0 text-blue-500" />;
  if (mimeType.includes('spreadsheet') || mimeType.includes('csv')) {
    return <FileSpreadsheet aria-hidden className="size-4 shrink-0 text-green-600 dark:text-green-400" />;
  }
  return <File aria-hidden className="text-muted-foreground size-4 shrink-0" />;
}

// ---------------------------------------------------------------------------
// Preview state
// ---------------------------------------------------------------------------

type PreviewState =
  | { status: 'loading'; hit: AttachmentHit }
  | { status: 'ready'; hit: AttachmentHit; blobUrl: string; filename: string; mimeType: string }
  | { status: 'error'; hit: AttachmentHit; message: string };

// ---------------------------------------------------------------------------
// Pane — search input, filter chips, grouped result list, preview dialog.
// Exported standalone (not just via the provider) so it can be rendered and
// tested without the surrounding Dialog chrome.
// ---------------------------------------------------------------------------

export function AttachmentsPane({ initialFilter = {} }: { initialFilter?: AttachmentsPaneFilter }) {
  const [query, setQuery] = React.useState('');
  const [debounced, setDebounced] = React.useState('');
  const [contact, setContact] = React.useState(initialFilter.contact);
  const [threadId, setThreadId] = React.useState(initialFilter.threadId);
  const [preview, setPreview] = React.useState<PreviewState | null>(null);
  const [downloadingId, setDownloadingId] = React.useState<string | null>(null);
  const checkout = useCheckoutMutation();

  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), 250);
    return () => clearTimeout(t);
  }, [query]);

  // Revokes an in-flight preview's blob URL on unmount (dialog closed via the
  // parent's own unmount/session remount rather than closePreview() below —
  // e.g. AttachmentsPaneProvider's `key={session}` swap, or navigating away
  // mid-preview). Without this, a 'ready' preview's object URL leaks for the
  // life of the page since closePreview() never runs in that path. A ref
  // (kept in sync every render) rather than `preview` itself in the deps
  // array so this cleanup fires only on true unmount, not on every preview
  // state change.
  const previewRef = React.useRef<PreviewState | null>(null);
  React.useEffect(() => {
    previewRef.current = preview;
  }, [preview]);
  React.useEffect(() => {
    return () => {
      if (previewRef.current?.status === 'ready') {
        URL.revokeObjectURL(previewRef.current.blobUrl);
      }
    };
  }, []);

  const { data, isLoading, isError, error, fetchNextPage, hasNextPage, isFetchingNextPage } =
    useAttachmentSearch({ q: debounced || undefined, contact, threadId });

  const hits = React.useMemo(() => data?.pages.flatMap((p) => p.page.items) ?? [], [data]);

  const groups = React.useMemo(() => {
    const order: string[] = [];
    const byThread = new Map<string, AttachmentHit[]>();
    for (const hit of hits) {
      if (!byThread.has(hit.threadId)) {
        byThread.set(hit.threadId, []);
        order.push(hit.threadId);
      }
      byThread.get(hit.threadId)!.push(hit);
    }
    return order.map((id) => ({
      threadId: id,
      subject: byThread.get(id)![0]!.threadSubject,
      hits: byThread.get(id)!,
    }));
  }, [hits]);

  async function openPreview(hit: AttachmentHit) {
    setPreview({ status: 'loading', hit });
    try {
      const result = await fetchAttachmentBlob(hit.id);
      setPreview({ status: 'ready', hit, ...result });
    } catch (err) {
      setPreview({ status: 'error', hit, message: attachmentErrorMessage(err) });
    }
  }

  async function downloadHit(hit: AttachmentHit) {
    setDownloadingId(hit.id);
    try {
      const { blobUrl, filename } = await fetchAttachmentBlob(hit.id);
      const a = document.createElement('a');
      a.href = blobUrl;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(blobUrl);
    } catch (err) {
      toast.error(attachmentErrorMessage(err));
    } finally {
      setDownloadingId(null);
    }
  }

  function handleRowClick(hit: AttachmentHit) {
    if (isPreviewable(hit.mimeType)) {
      void openPreview(hit);
    } else {
      void downloadHit(hit);
    }
  }

  function closePreview() {
    setPreview((current) => {
      if (current?.status === 'ready') URL.revokeObjectURL(current.blobUrl);
      return null;
    });
  }

  const paywalled = isError && error instanceof ApiRequestError && error.status === 402;
  const hasFilters = Boolean(contact || threadId);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-col gap-2 border-b p-4">
        <div className="flex items-center gap-2">
          <Paperclip aria-hidden className="text-muted-foreground size-4 shrink-0" />
          <span className="text-sm font-medium">Attachments</span>
        </div>
        <div className="relative">
          <Search
            aria-hidden
            className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
          />
          <Input
            autoFocus
            placeholder="Search attachments…"
            aria-label="Search attachments"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="pl-8"
          />
        </div>
        {hasFilters && (
          <div className="flex flex-wrap gap-1.5">
            {contact && (
              <Badge variant="secondary" className="gap-1">
                From: {contact}
                <button
                  type="button"
                  onClick={() => setContact(undefined)}
                  aria-label="Clear contact filter"
                >
                  <X className="size-3" />
                </button>
              </Badge>
            )}
            {threadId && (
              <Badge variant="secondary" className="gap-1">
                This conversation
                <button
                  type="button"
                  onClick={() => setThreadId(undefined)}
                  aria-label="Clear conversation filter"
                >
                  <X className="size-3" />
                </button>
              </Badge>
            )}
          </div>
        )}
      </div>

      <ScrollArea className="min-h-0 flex-1">
        <div className="p-2">
          {isLoading && (
            <div className="text-muted-foreground flex items-center justify-center gap-2 p-8 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Searching…
            </div>
          )}

          {paywalled && (
            <div className="flex flex-col items-center gap-3 p-8 text-center">
              <Lock aria-hidden className="text-muted-foreground size-6" />
              <p className="text-sm font-medium">Attachments are part of the paid plan</p>
              <p className="text-muted-foreground text-sm">
                Subscribe to search, preview, and download attachments across your mailbox.
              </p>
              <Button size="sm" onClick={() => checkout.mutate()} disabled={checkout.isPending}>
                Subscribe — $50/year
              </Button>
            </div>
          )}

          {!isLoading && isError && !paywalled && (
            <p className="text-muted-foreground p-8 text-center text-sm">
              Could not search attachments. Please try again.
            </p>
          )}

          {!isLoading && !isError && groups.length === 0 && (
            <p className="text-muted-foreground p-8 text-center text-sm">
              {debounced || hasFilters
                ? 'No attachments match your search.'
                : 'No attachments found yet.'}
            </p>
          )}

          {groups.map((group) => (
            <div key={group.threadId} className="mb-2">
              <p className="text-muted-foreground truncate px-2 py-1.5 text-xs font-medium">
                {group.subject}
              </p>
              {group.hits.map((hit) => (
                <button
                  key={hit.id}
                  type="button"
                  onClick={() => handleRowClick(hit)}
                  disabled={downloadingId === hit.id}
                  className="hover:bg-accent flex w-full items-center gap-2.5 rounded-md px-2 py-2 text-left text-sm disabled:opacity-60"
                >
                  <MimeIcon mimeType={hit.mimeType} />
                  <span className="min-w-0 flex-1 truncate">{hit.filename}</span>
                  <span className="text-muted-foreground shrink-0 text-xs">
                    {humanSize(hit.sizeBytes)}
                  </span>
                  <span className="text-muted-foreground hidden shrink-0 truncate text-xs sm:inline">
                    {hit.from.name ?? hit.from.email}
                  </span>
                  <span className="text-muted-foreground shrink-0 text-xs">
                    {formatListTime(hit.sentAt)}
                  </span>
                  {downloadingId === hit.id ? (
                    <Loader2 className="size-3.5 shrink-0 animate-spin" />
                  ) : !isPreviewable(hit.mimeType) ? (
                    <Download aria-hidden className="text-muted-foreground size-3.5 shrink-0" />
                  ) : null}
                </button>
              ))}
            </div>
          ))}

          {hasNextPage && (
            <div className="flex justify-center p-2">
              <Button
                variant="outline"
                size="sm"
                disabled={isFetchingNextPage}
                onClick={() => fetchNextPage()}
              >
                {isFetchingNextPage ? 'Loading…' : 'Load more'}
              </Button>
            </div>
          )}
        </div>
      </ScrollArea>

      <Dialog
        open={preview !== null}
        onOpenChange={(open) => {
          if (!open) closePreview();
        }}
      >
        <DialogContent className="gap-2 sm:max-w-4xl">
          <DialogTitle>{preview?.hit.filename ?? 'Preview'}</DialogTitle>
          <DialogDescription className="sr-only">Attachment preview</DialogDescription>
          {preview?.status === 'loading' && (
            <div className="text-muted-foreground flex h-[40vh] items-center justify-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading preview…
            </div>
          )}
          {preview?.status === 'error' && (
            <p className="text-muted-foreground p-8 text-center text-sm">{preview.message}</p>
          )}
          {preview?.status === 'ready' && isPdf(preview.mimeType) && (
            // sandbox="" (all permissions denied — no scripts, forms, popups,
            // same-origin) is the tightest possible setting; the browser's
            // native PDF viewer renders the blob independently of the iframe
            // document's script permissions, so preview still works.
            <iframe
              title={preview.filename}
              src={preview.blobUrl}
              sandbox=""
              className="h-[80vh] w-full"
            />
          )}
          {preview?.status === 'ready' && isImage(preview.mimeType) && (
            // biome-ignore lint/performance/noImgElement: inline preview of an in-memory blob URL, not an optimizable remote asset next/image could handle.
            <img
              src={preview.blobUrl}
              alt={preview.filename}
              className="max-h-[80vh] w-full object-contain"
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
