'use client';

import * as React from 'react';
import type { EmailAddress } from '@calendium/shared';
import { X } from 'lucide-react';

import { Badge } from '@/components/ui/badge';

/**
 * Extracted from compose.tsx (M2.5 Task 13) so the settings surface (signature
 * / auto-BCC editor) can reuse the exact same recipient-chip input instead of
 * duplicating the parse/commit logic. compose.tsx re-exports both symbols so
 * its own imports (and compose.test.tsx, which exercises this behavior
 * indirectly through the rendered "To"/"Cc"/"Bcc" rows) keep working unchanged.
 */

export const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function parseAddress(raw: string): EmailAddress | null {
  const s = raw.trim();
  // "Display Name" <email> | Name <email> | <email>
  const angle = s.match(/^(?:"?([^"<]*?)"?\s*)?<([^\s<>]+@[^\s<>]+)>$/);
  if (angle) {
    const email = angle[2]!.toLowerCase();
    if (!EMAIL_RE.test(email)) return null;
    const name = angle[1]?.trim();
    return { name: name ? name : null, email };
  }
  // bare email — reject stray angle brackets (valid only inside a matched <...> pair, handled above)
  if (!s.includes('<') && !s.includes('>') && EMAIL_RE.test(s)) {
    return { name: null, email: s.toLowerCase() };
  }
  return null;
}

export function ChipsRow({
  label,
  chips,
  onChange,
  autoFocus,
  trailing,
}: {
  label: string;
  chips: EmailAddress[];
  onChange: (chips: EmailAddress[]) => void;
  autoFocus?: boolean;
  trailing?: React.ReactNode;
}) {
  const [draft, setDraft] = React.useState('');
  const inputRef = React.useRef<HTMLInputElement>(null);

  function commit(): boolean {
    const parsed = parseAddress(draft);
    if (!parsed) return false;
    if (!chips.some((c) => c.email === parsed.email)) onChange([...chips, parsed]);
    setDraft('');
    return true;
  }

  function onKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',' || event.key === 'Tab') {
      if (draft.trim()) {
        if (commit()) event.preventDefault();
        else if (event.key !== 'Tab') event.preventDefault();
      }
    } else if (event.key === 'Backspace' && draft === '' && chips.length > 0) {
      onChange(chips.slice(0, -1));
    }
  }

  return (
    <div
      className="flex min-h-9 cursor-text items-center gap-1.5 border-b px-4 py-1.5"
      onClick={() => inputRef.current?.focus()}
    >
      <span className="text-muted-foreground w-8 shrink-0 text-xs">{label}</span>
      <div className="flex flex-1 flex-wrap items-center gap-1">
        {chips.map((chip) => (
          <Badge key={chip.email} variant="secondary" className="gap-1 pr-1 font-normal">
            {chip.name ?? chip.email}
            <button
              type="button"
              className="hover:text-foreground text-muted-foreground rounded-sm"
              onClick={() => onChange(chips.filter((c) => c.email !== chip.email))}
              aria-label={`Remove ${chip.email}`}
            >
              <X className="size-3" />
            </button>
          </Badge>
        ))}
        <input
          ref={inputRef}
          value={draft}
          autoFocus={autoFocus}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={onKeyDown}
          onBlur={() => draft.trim() && commit()}
          className="placeholder:text-muted-foreground min-w-24 flex-1 bg-transparent py-0.5 text-sm outline-none"
          placeholder={chips.length === 0 ? 'name@example.com' : undefined}
        />
      </div>
      {trailing}
    </div>
  );
}
