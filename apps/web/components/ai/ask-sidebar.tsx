'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import type { AiAskResponse } from '@calendium/shared';
import { Loader2, MessageSquareText, Send, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { ScrollArea } from '@/components/ui/scroll-area';
import { useShortcuts } from '@/lib/shortcuts';
import { useInstance } from '@/lib/use-instance';
import { aiErrorMessage, runAiAskCited } from '@/lib/use-mail';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Context — small store so the sidebar (and its in-flight Q&A) survives
// navigating between routes; it's mounted once at the app shell root
// (apps/web/app/(app)/layout.tsx) rather than per-page.
// ---------------------------------------------------------------------------

interface AskSidebarContextValue {
  open: boolean;
  toggle: () => void;
  openSidebar: () => void;
  close: () => void;
}

const AskSidebarContext = React.createContext<AskSidebarContextValue | null>(null);

export function useAskSidebar(): AskSidebarContextValue {
  const ctx = React.useContext(AskSidebarContext);
  if (!ctx) throw new Error('useAskSidebar must be used within an <AskSidebarProvider>');
  return ctx;
}

export function AskSidebarProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = React.useState(false);
  const aiEnabled = useInstance().data?.features.ai ?? false;

  useShortcuts([
    {
      keys: 'mod+j',
      allowInInput: true,
      description: 'Toggle Ask AI',
      enabled: aiEnabled,
      handler: () => setOpen((o) => !o),
    },
  ]);

  const value = React.useMemo<AskSidebarContextValue>(
    () => ({
      open: open && aiEnabled,
      toggle: () => setOpen((o) => !o),
      openSidebar: () => setOpen(true),
      close: () => setOpen(false),
    }),
    [open, aiEnabled]
  );

  return <AskSidebarContext.Provider value={value}>{children}</AskSidebarContext.Provider>;
}

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

/**
 * Persistent right-hand "Ask AI" sidebar. Always mounted while the server has
 * AI enabled (only its width collapses when closed) so an in-flight
 * question/answer survives toggling the panel or navigating to another route.
 */
export function AskSidebarPanel() {
  const { open, close } = useAskSidebar();
  const aiEnabled = useInstance().data?.features.ai ?? false;
  const router = useRouter();

  const [question, setQuestion] = React.useState('');
  const [result, setResult] = React.useState<AiAskResponse | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  if (!aiEnabled) return null;

  async function ask() {
    const q = question.trim();
    if (!q) return;
    setBusy(true);
    setError(null);
    try {
      const res = await runAiAskCited(q);
      setResult(res);
    } catch (err) {
      setError(aiErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className={cn(
        'flex shrink-0 flex-col overflow-hidden border-l transition-[width] duration-150 ease-out',
        open ? 'w-80' : 'w-0 border-l-0'
      )}
      aria-hidden={!open}
      data-testid="ask-sidebar"
      data-state={open ? 'open' : 'closed'}
    >
      <div className="flex w-80 min-w-80 flex-1 flex-col">
        <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2.5">
          <MessageSquareText className="size-4" />
          <span className="text-sm font-semibold">Ask AI</span>
          <Button
            variant="ghost"
            size="icon"
            className="ml-auto size-7"
            onClick={close}
            aria-label="Close Ask AI"
          >
            <X className="size-4" />
          </Button>
        </div>

        <div className="flex shrink-0 items-center gap-2 border-b p-2.5">
          <Input
            value={question}
            onChange={(event) => setQuestion(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                void ask();
              }
            }}
            placeholder="Ask about your mailbox…"
            className="h-8"
            disabled={busy}
            aria-label="Ask a question"
          />
          <Button
            size="icon"
            className="size-8 shrink-0"
            onClick={() => void ask()}
            disabled={busy || !question.trim()}
            aria-label="Ask"
          >
            {busy ? <Loader2 className="animate-spin" /> : <Send className="size-3.5" />}
          </Button>
        </div>

        <ScrollArea className="min-h-0 flex-1">
          <div className="flex flex-col gap-3 p-3">
            {error && <p className="text-destructive text-sm">{error}</p>}
            {!error && !result && !busy && (
              <p className="text-muted-foreground text-sm">
                Ask a question about your mailbox — e.g. "What did Priya say about the incident?"
              </p>
            )}
            {result && (
              <>
                <p className="text-sm leading-relaxed whitespace-pre-wrap">{result.answer}</p>
                {result.sources.length > 0 && (
                  <div className="flex flex-col gap-1.5">
                    <p className="text-muted-foreground text-xs font-medium">Sources</p>
                    {result.sources.map((source) => (
                      <button
                        key={`${source.threadId}-${source.messageId ?? source.snippet}`}
                        type="button"
                        onClick={() => router.push(`/mail?t=${source.threadId}`)}
                        className="hover:bg-accent/50 flex flex-col items-start gap-0.5 rounded-md border px-2.5 py-2 text-left"
                      >
                        <span className="truncate text-xs font-medium">{source.subject}</span>
                        <span className="text-muted-foreground line-clamp-2 text-xs">
                          {source.snippet}
                        </span>
                      </button>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </ScrollArea>

        <div className="text-muted-foreground shrink-0 border-t px-3 py-1.5 text-xs">
          <Kbd size="sm">⌘J</Kbd> to toggle
        </div>
      </div>
    </div>
  );
}
