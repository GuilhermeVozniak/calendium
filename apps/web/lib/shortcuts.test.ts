import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  IS_MAC,
  MOD_KEY,
  accountSwitchShortcuts,
  isEditableTarget,
  useChords,
  useShortcuts,
} from '@/lib/shortcuts';

function keydown(
  target: EventTarget,
  init: KeyboardEventInit & { key: string },
): void {
  target.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }));
}

function openDialog(): HTMLElement {
  const dialog = document.createElement('div');
  dialog.setAttribute('role', 'dialog');
  dialog.setAttribute('data-state', 'open');
  document.body.appendChild(dialog);
  return dialog;
}

afterEach(() => {
  document.body.innerHTML = '';
});

describe('IS_MAC / MOD_KEY', () => {
  it('MOD_KEY reflects IS_MAC', () => {
    expect(typeof IS_MAC).toBe('boolean');
    expect(MOD_KEY).toBe(IS_MAC ? '⌘' : 'Ctrl');
  });
});

describe('isEditableTarget', () => {
  it('returns false for null', () => {
    expect(isEditableTarget(null)).toBe(false);
  });

  it('returns false for a plain element', () => {
    expect(isEditableTarget(document.createElement('div'))).toBe(false);
  });

  it.each(['input', 'textarea', 'select'])('returns true for a <%s>', (tag) => {
    expect(isEditableTarget(document.createElement(tag))).toBe(true);
  });

  it('returns true for a contentEditable element', () => {
    const div = document.createElement('div');
    // jsdom does not implement the contentEditable/isContentEditable
    // reflection, so stub the getter the same way a real browser would set it.
    Object.defineProperty(div, 'isContentEditable', { value: true, configurable: true });
    expect(isEditableTarget(div)).toBe(true);
  });

  it('returns false for a non-Element EventTarget', () => {
    expect(isEditableTarget(window)).toBe(false);
  });
});

describe('useShortcuts', () => {
  it('fires the handler for a matching single key on a neutral target', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'j', handler }]));
    keydown(document.body, { key: 'j' });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('ignores non-matching keys', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'j', handler }]));
    keydown(document.body, { key: 'k' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('requires the shift modifier for letter combos', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'shift+i', handler }]));
    keydown(document.body, { key: 'i', shiftKey: false });
    expect(handler).not.toHaveBeenCalled();
    keydown(document.body, { key: 'i', shiftKey: true });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('matches "mod" against the platform modifier', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'mod+k', handler }]));
    // Set both so the assertion holds regardless of the jsdom-reported platform.
    keydown(document.body, { key: 'k', metaKey: true, ctrlKey: true });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('suppresses shortcuts while an editable element has focus', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'j', handler }]));
    const input = document.createElement('input');
    document.body.appendChild(input);
    keydown(input, { key: 'j' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('fires in an editable element when allowInInput is set', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'mod+k', handler, allowInInput: true }]));
    const input = document.createElement('input');
    document.body.appendChild(input);
    keydown(input, { key: 'k', metaKey: true, ctrlKey: true });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('suppresses shortcuts while a dialog is open', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'j', handler }]));
    openDialog();
    keydown(document.body, { key: 'j' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('still fires allowInInput shortcuts while a dialog is open', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'mod+k', handler, allowInInput: true }]));
    openDialog();
    keydown(document.body, { key: 'k', metaKey: true, ctrlKey: true });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('skips a shortcut explicitly disabled via enabled:false', () => {
    const handler = vi.fn();
    renderHook(() => useShortcuts([{ keys: 'j', handler, enabled: false }]));
    keydown(document.body, { key: 'j' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('only fires the first matching binding', () => {
    const first = vi.fn();
    const second = vi.fn();
    renderHook(() =>
      useShortcuts([
        { keys: 'j', handler: first },
        { keys: 'j', handler: second },
      ]),
    );
    keydown(document.body, { key: 'j' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();
  });

  it('removes its listener on unmount', () => {
    const handler = vi.fn();
    const { unmount } = renderHook(() => useShortcuts([{ keys: 'j', handler }]));
    unmount();
    keydown(document.body, { key: 'j' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('picks up updated handlers without remounting', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ handler }) => useShortcuts([{ keys: 'j', handler }]), {
      initialProps: { handler: first },
    });
    rerender({ handler: second });
    keydown(document.body, { key: 'j' });
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});

describe('accountSwitchShortcuts', () => {
  const IDS = ['acc1', 'acc2', 'acc3'];

  it('mod+2 with three accounts selects the second', () => {
    const setActive = vi.fn();
    renderHook(() => useShortcuts(accountSwitchShortcuts(IDS, setActive)));
    keydown(document.body, { key: '2', metaKey: true, ctrlKey: true });
    expect(setActive).toHaveBeenCalledTimes(1);
    expect(setActive).toHaveBeenCalledWith('acc2');
  });

  it('mod+5 with three accounts is a no-op', () => {
    const setActive = vi.fn();
    renderHook(() => useShortcuts(accountSwitchShortcuts(IDS, setActive)));
    keydown(document.body, { key: '5', metaKey: true, ctrlKey: true });
    expect(setActive).not.toHaveBeenCalled();
  });

  it('mod+0 clears back to all accounts', () => {
    const setActive = vi.fn();
    renderHook(() => useShortcuts(accountSwitchShortcuts(IDS, setActive)));
    keydown(document.body, { key: '0', metaKey: true, ctrlKey: true });
    expect(setActive).toHaveBeenCalledTimes(1);
    expect(setActive).toHaveBeenCalledWith(null);
  });

  it('a bare digit without the modifier does nothing', () => {
    const setActive = vi.fn();
    renderHook(() => useShortcuts(accountSwitchShortcuts(IDS, setActive)));
    keydown(document.body, { key: '1' });
    expect(setActive).not.toHaveBeenCalled();
  });

  it('is suppressed while typing in an input', () => {
    const setActive = vi.fn();
    renderHook(() => useShortcuts(accountSwitchShortcuts(IDS, setActive)));
    const input = document.createElement('input');
    document.body.appendChild(input);
    keydown(input, { key: '1', metaKey: true, ctrlKey: true });
    expect(setActive).not.toHaveBeenCalled();
  });

  it('caps bindings at nine accounts', () => {
    const setActive = vi.fn();
    const many = Array.from({ length: 12 }, (_, i) => `acc${i + 1}`);
    const bindings = accountSwitchShortcuts(many, setActive);
    // 9 account bindings + mod+0.
    expect(bindings).toHaveLength(10);
  });
});

describe('useChords', () => {
  it('fires the handler after the full sequence', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    keydown(document.body, { key: 'i' });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('does not fire on the prefix key alone', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('does not fire when the second key does not complete any chord', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    keydown(document.body, { key: 'x' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('lets a failed prefix fall through and re-arm a new chord', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    keydown(document.body, { key: 'g' }); // "g g" doesn't match, but re-arms "g"
    keydown(document.body, { key: 'i' });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('resets the prefix when a modifier key is pressed', () => {
    vi.useFakeTimers();
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    keydown(document.body, { key: 'k', metaKey: true });
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('resets the prefix when focus moves to an editable element', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    const input = document.createElement('input');
    document.body.appendChild(input);
    keydown(input, { key: 'x' });
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('resets the prefix when a dialog opens', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    openDialog();
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('expires the prefix after the chord timeout', () => {
    vi.useFakeTimers();
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    vi.advanceTimersByTime(1300);
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('skips a chord explicitly disabled via enabled:false', () => {
    const handler = vi.fn();
    renderHook(() => useChords([{ keys: 'g i', handler, enabled: false }]));
    keydown(document.body, { key: 'g' });
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('cleans up its listener and pending timer on unmount', () => {
    vi.useFakeTimers();
    const handler = vi.fn();
    const { unmount } = renderHook(() => useChords([{ keys: 'g i', handler }]));
    keydown(document.body, { key: 'g' });
    unmount();
    keydown(document.body, { key: 'i' });
    expect(handler).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
