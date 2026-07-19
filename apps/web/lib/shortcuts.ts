'use client';

import * as React from 'react';

/**
 * Tiny declarative hotkey hook — no dependencies.
 *
 *   useShortcuts([
 *     { keys: 'j', handler: next },
 *     { keys: 'shift+i', handler: markRead },
 *     { keys: 'mod+k', handler: togglePalette, allowInInput: true },
 *   ]);
 *
 * `mod` maps to ⌘ on macOS and Ctrl elsewhere. By default shortcuts are
 * suppressed while an input/textarea/contenteditable has focus (Superhuman
 * behavior); opt in per binding with `allowInInput`.
 */
export interface Shortcut {
  /** Key combo, e.g. `"j"`, `"shift+i"`, `"mod+k"`, `"enter"`, `"escape"`, `"/"`. */
  keys: string;
  handler: (event: KeyboardEvent) => void;
  /** Fire even when an editable element has focus. */
  allowInInput?: boolean;
  /** Set false to keep the binding declared but inert. */
  enabled?: boolean;
  description?: string;
}

export const IS_MAC =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform);

/** Display glyph for the `mod` key on the current platform. */
export const MOD_KEY = IS_MAC ? '⌘' : 'Ctrl';

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
}

interface Parsed {
  key: string;
  mod: boolean;
  shift: boolean;
  alt: boolean;
}

function parse(keys: string): Parsed {
  const parts = keys.toLowerCase().split('+');
  const key = parts.pop() ?? '';
  return {
    key,
    mod: parts.includes('mod'),
    shift: parts.includes('shift'),
    alt: parts.includes('alt'),
  };
}

function matches(event: KeyboardEvent, parsed: Parsed): boolean {
  if (event.key.toLowerCase() !== parsed.key) return false;
  const mod = IS_MAC ? event.metaKey : event.ctrlKey;
  if (mod !== parsed.mod) return false;
  if (event.altKey !== parsed.alt) return false;
  // Punctuation keys ("?", "/", ";") already encode shift in event.key, so
  // only enforce the shift modifier for letters and named keys.
  const shiftIsMeaningful = /^[a-z]$/.test(parsed.key) || parsed.key.length > 1;
  if (shiftIsMeaningful && event.shiftKey !== parsed.shift) return false;
  return true;
}

export function useShortcuts(shortcuts: Shortcut[]): void {
  const ref = React.useRef(shortcuts);
  ref.current = shortcuts;

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented) return;
      const inInput = isEditableTarget(event.target);
      // While a dialog (compose, palette, snooze…) is open, only bindings that
      // opted into `allowInInput` may fire — keeps j/k/e from hitting the list
      // behind the overlay.
      const dialogOpen = !!document.querySelector('[role="dialog"][data-state="open"]');
      for (const shortcut of ref.current) {
        if (shortcut.enabled === false) continue;
        if ((inInput || dialogOpen) && !shortcut.allowInInput) continue;
        if (matches(event, parse(shortcut.keys))) {
          event.preventDefault();
          shortcut.handler(event);
          return;
        }
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);
}

// ---------------------------------------------------------------------------
// Account switching (mod+1..9 / mod+0)
// ---------------------------------------------------------------------------

/**
 * Bindings for multi-account switching: mod+1..9 selects the nth connected
 * account, mod+0 clears back to "all accounts". Digits past the account count
 * are left unbound, so e.g. mod+5 with three accounts is a no-op. Uses the
 * default editable-target guard — nothing fires while an
 * input/textarea/contenteditable has focus.
 */
export function accountSwitchShortcuts(
  accountIds: string[],
  setActiveAccountId: (id: string | null) => void
): Shortcut[] {
  const shortcuts: Shortcut[] = accountIds.slice(0, 9).map((id, index) => ({
    keys: `mod+${index + 1}`,
    description: `Switch to account ${index + 1}`,
    handler: () => setActiveAccountId(id),
  }));
  shortcuts.push({
    keys: 'mod+0',
    description: 'Show all accounts',
    handler: () => setActiveAccountId(null),
  });
  return shortcuts;
}

// ---------------------------------------------------------------------------
// Chord sequences (Gmail/Superhuman "G then I", "G then C")
// ---------------------------------------------------------------------------

export interface Chord {
  /** Space-separated key sequence, e.g. `"g i"` or `"g c"` (single letters). */
  keys: string;
  handler: () => void;
  enabled?: boolean;
  description?: string;
}

/** How long (ms) a chord prefix stays armed waiting for the second key. */
const CHORD_TIMEOUT = 1200;

/**
 * Registers multi-key chord sequences. The first key arms a prefix; a matching
 * second key within the timeout fires the handler. Modifier keys, editable
 * targets, and open dialogs cancel the prefix. This hook must be mounted BEFORE
 * any single-key `useShortcuts` in the same tree so its `preventDefault` on a
 * completed chord suppresses a colliding single-key binding (e.g. "c").
 */
export function useChords(chords: Chord[]): void {
  const ref = React.useRef(chords);
  ref.current = chords;

  React.useEffect(() => {
    let prefix: string | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;

    function reset() {
      prefix = null;
      if (timer) clearTimeout(timer);
      timer = undefined;
    }

    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented) return;
      if (event.metaKey || event.ctrlKey || event.altKey) {
        reset();
        return;
      }
      if (isEditableTarget(event.target)) {
        reset();
        return;
      }
      if (document.querySelector('[role="dialog"][data-state="open"]')) {
        reset();
        return;
      }
      const key = event.key.toLowerCase();
      const active = ref.current.filter((c) => c.enabled !== false);

      if (prefix) {
        const combo = `${prefix} ${key}`;
        const match = active.find((c) => c.keys.toLowerCase() === combo);
        reset();
        if (match) {
          event.preventDefault();
          match.handler();
          return;
        }
        // Fall through: this key may itself start a new chord.
      }

      if (active.some((c) => c.keys.toLowerCase().split(' ')[0] === key)) {
        event.preventDefault();
        prefix = key;
        timer = setTimeout(reset, CHORD_TIMEOUT);
      }
    }

    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      reset();
    };
  }, []);
}
