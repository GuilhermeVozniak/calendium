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
