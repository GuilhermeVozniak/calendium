'use client';

import * as React from 'react';
import type { AiEditAction } from '@calendium/shared';
import { ChevronDown, Loader2, Sparkles } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useShortcuts } from '@/lib/shortcuts';
import { aiErrorMessage, runAiEditDraft } from '@/lib/use-mail';

// ---------------------------------------------------------------------------
// Command bus — lets the command palette trigger an edit action even though
// it has no direct reference to whichever ComposeForm instance is mounted
// (same pattern as lib/mail-utils.ts's dispatchMailCommand). Only meaningful
// while the composer is open (AiEditMenu attaches its listener on mount); if
// nothing is listening the dispatch is a harmless no-op.
// ---------------------------------------------------------------------------

const AI_EDIT_COMMAND_EVENT = 'calendium:ai-edit-command';

export function dispatchAiEditCommand(action: AiEditAction): void {
  window.dispatchEvent(new CustomEvent<AiEditAction>(AI_EDIT_COMMAND_EVENT, { detail: action }));
}

function onAiEditCommand(handler: (action: AiEditAction) => void): () => void {
  const listener = (event: Event) => handler((event as CustomEvent<AiEditAction>).detail);
  window.addEventListener(AI_EDIT_COMMAND_EVENT, listener);
  return () => window.removeEventListener(AI_EDIT_COMMAND_EVENT, listener);
}

const EDIT_ACTIONS: { action: AiEditAction; label: string }[] = [
  { action: 'improve', label: 'Improve' },
  { action: 'shorten', label: 'Shorten' },
  { action: 'simplify', label: 'Simplify' },
  { action: 'fix_grammar', label: 'Fix grammar' },
];

/**
 * Composer toolbar dropdown: Improve / Shorten / Simplify / Fix grammar /
 * Change tone. Edits the draft in place via aiEditDraft — `ensureDraftId`
 * lazily persists the in-progress compose as a real draft on first use (AI
 * edit needs a draftId; a brand-new compose session doesn't have one yet).
 * `⌘E` toggles the menu; loading/error states are handled here so callers
 * only need to apply the returned text.
 */
export function AiEditMenu({
  ensureDraftId,
  onApplied,
}: {
  ensureDraftId: () => Promise<string>;
  onApplied: (text: string) => void;
}) {
  const [open, setOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [toneOpen, setToneOpen] = React.useState(false);
  const [tone, setTone] = React.useState('');

  async function run(action: AiEditAction, toneValue?: string) {
    setBusy(true);
    try {
      const draftId = await ensureDraftId();
      const res = await runAiEditDraft(action, draftId, toneValue);
      onApplied(res.text);
      setOpen(false);
      setToneOpen(false);
      setTone('');
      if (res.source === 'demo') toast.info('AI is offline — edited locally.');
    } catch (err) {
      toast.error(aiErrorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // A ref (rather than listing `run` as a dependency) keeps this subscription
  // mounted once for the component's lifetime instead of re-subscribing on
  // every render (run is a new closure each render).
  const runRef = React.useRef(run);
  runRef.current = run;
  React.useEffect(() => onAiEditCommand((action) => void runRef.current(action)), []);

  useShortcuts([
    {
      keys: 'mod+e',
      allowInInput: true,
      description: 'AI edit menu',
      handler: () => setOpen((o) => !o),
    },
  ]);

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1.5" disabled={busy}>
          {busy ? <Loader2 className="animate-spin" /> : <Sparkles />}
          Edit with AI
          <ChevronDown className="size-3 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>Edit with AI</DropdownMenuLabel>
        {EDIT_ACTIONS.map(({ action, label }) => (
          <DropdownMenuItem
            key={action}
            onSelect={(event) => {
              event.preventDefault();
              void run(action);
            }}
          >
            {label}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <Popover open={toneOpen} onOpenChange={setToneOpen}>
          <PopoverTrigger asChild>
            <DropdownMenuItem onSelect={(event) => event.preventDefault()}>Change tone…</DropdownMenuItem>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-64 p-2" onOpenAutoFocus={(event) => event.preventDefault()}>
            <p className="text-muted-foreground mb-1.5 text-xs">Target tone</p>
            <div className="flex gap-1.5">
              <Input
                value={tone}
                autoFocus
                onChange={(event) => setTone(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && tone.trim()) {
                    event.preventDefault();
                    void run('change_tone', tone.trim());
                  }
                }}
                placeholder="e.g. more formal"
                className="h-8"
              />
              <Button
                size="sm"
                className="h-8"
                disabled={!tone.trim() || busy}
                onClick={() => void run('change_tone', tone.trim())}
              >
                Apply
              </Button>
            </div>
          </PopoverContent>
        </Popover>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
