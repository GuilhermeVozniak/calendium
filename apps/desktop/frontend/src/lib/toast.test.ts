import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { dismissToast, errorMessage, getToasts, subscribeToasts, toast } from './toast';

// toast.ts is a module-level singleton store (not a class), so every test
// must leave it empty afterward or a later test would see leftover toasts.
afterEach(() => {
  for (const t of getToasts()) dismissToast(t.id);
});

describe('toast / getToasts', () => {
  it('adds a toast and returns its id', () => {
    const id = toast({ title: 'Saved' });
    expect(typeof id).toBe('string');
    expect(id.length).toBeGreaterThan(0);
    const toasts = getToasts();
    expect(toasts).toHaveLength(1);
    expect(toasts[0]).toMatchObject({ id, title: 'Saved' });
  });

  it('appends multiple toasts in call order', () => {
    const first = toast({ title: 'First' });
    const second = toast({ title: 'Second' });
    const ids = getToasts().map((t) => t.id);
    expect(ids).toEqual([first, second]);
  });

  it('assigns distinct ids to concurrently created toasts', () => {
    const a = toast({ title: 'A' });
    const b = toast({ title: 'B' });
    expect(a).not.toBe(b);
  });

  it('getToasts returns the same array reference until the next mutation', () => {
    toast({ title: 'Stable' });
    const snapshot1 = getToasts();
    const snapshot2 = getToasts();
    expect(snapshot1).toBe(snapshot2);
    toast({ title: 'Another' });
    const snapshot3 = getToasts();
    expect(snapshot3).not.toBe(snapshot1);
  });
});

describe('dismissToast', () => {
  it('removes the toast with the given id', () => {
    const id = toast({ title: 'Bye' });
    expect(getToasts()).toHaveLength(1);
    dismissToast(id);
    expect(getToasts()).toHaveLength(0);
  });

  it('is a no-op for an unknown id (does not notify or mutate the array ref)', () => {
    toast({ title: 'Stays' });
    const before = getToasts();
    dismissToast('not-a-real-id');
    expect(getToasts()).toBe(before);
    expect(getToasts()).toHaveLength(1);
  });
});

describe('subscribeToasts', () => {
  it('notifies subscribers on toast() and dismissToast()', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);
    const id = toast({ title: 'Hi' });
    expect(listener).toHaveBeenCalledTimes(1);
    dismissToast(id);
    expect(listener).toHaveBeenCalledTimes(2);
    unsubscribe();
  });

  it('stops notifying after unsubscribe', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);
    unsubscribe();
    toast({ title: 'Missed' });
    expect(listener).not.toHaveBeenCalled();
  });

  it('supports multiple independent subscribers', () => {
    const a = vi.fn();
    const b = vi.fn();
    const unsubA = subscribeToasts(a);
    const unsubB = subscribeToasts(b);
    toast({ title: 'Broadcast' });
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    unsubA();
    unsubB();
  });
});

describe('auto-dismiss timer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('auto-dismisses after the default 5000ms', () => {
    const id = toast({ title: 'Auto' });
    expect(getToasts()).toHaveLength(1);
    vi.advanceTimersByTime(4999);
    expect(getToasts().find((t) => t.id === id)).toBeDefined();
    vi.advanceTimersByTime(1);
    expect(getToasts().find((t) => t.id === id)).toBeUndefined();
  });

  it('auto-dismisses after a custom durationMs', () => {
    const id = toast({ title: 'Quick', durationMs: 1000 });
    vi.advanceTimersByTime(999);
    expect(getToasts().find((t) => t.id === id)).toBeDefined();
    vi.advanceTimersByTime(1);
    expect(getToasts().find((t) => t.id === id)).toBeUndefined();
  });

  it('does not auto-dismiss when durationMs <= 0', () => {
    const id = toast({ title: 'Sticky', durationMs: 0 });
    vi.advanceTimersByTime(100_000);
    expect(getToasts().find((t) => t.id === id)).toBeDefined();
  });

  it('notifies subscribers exactly once for the auto-dismiss', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);
    toast({ title: 'Timed', durationMs: 500 });
    expect(listener).toHaveBeenCalledTimes(1); // the toast() call itself
    vi.advanceTimersByTime(500);
    expect(listener).toHaveBeenCalledTimes(2); // the auto-dismiss
    unsubscribe();
  });

  it('clears the pending timer when manually dismissed early', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToasts(listener);
    const id = toast({ title: 'Manual', durationMs: 5000 });
    dismissToast(id);
    listener.mockClear();
    // If the timer weren't cleared, this would fire a second, spurious
    // dismiss notification for an id that's already gone.
    vi.advanceTimersByTime(5000);
    expect(listener).not.toHaveBeenCalled();
    unsubscribe();
  });
});

describe('errorMessage', () => {
  it('returns the message of an Error instance', () => {
    expect(errorMessage(new Error('Network down'))).toBe('Network down');
  });

  it('falls back for an Error with an empty message', () => {
    expect(errorMessage(new Error(''))).toBe('Something went wrong. Please try again.');
  });

  it('falls back for a non-Error value (string)', () => {
    expect(errorMessage('boom')).toBe('Something went wrong. Please try again.');
  });

  it('falls back for a non-Error value (undefined/object)', () => {
    expect(errorMessage(undefined)).toBe('Something went wrong. Please try again.');
    expect(errorMessage({ message: 'not an Error instance' })).toBe(
      'Something went wrong. Please try again.'
    );
  });
});
