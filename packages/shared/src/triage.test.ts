import { describe, expect, it } from 'vitest';

import {
  EMPTY_SELECTION,
  UndoStack,
  clearSelection,
  extendSelection,
  nextAfterRemoval,
  toggleSelected,
} from './triage';

const order = ['a', 'b', 'c', 'd', 'e'];

describe('selection model', () => {
  it('toggleSelected adds, then removes, and moves the anchor', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'b');
    expect([...sel.ids]).toEqual(['b']);
    expect(sel.anchorId).toBe('b');
    sel = toggleSelected(sel, 'b');
    expect(sel.ids.size).toBe(0);
    expect(sel.anchorId).toBe('b');
  });

  it('extendSelection selects the whole range from the anchor, both directions', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'b');
    sel = extendSelection(sel, order, 'd');
    expect([...sel.ids].sort()).toEqual(['b', 'c', 'd']);
    expect(sel.anchorId).toBe('b');
    sel = extendSelection(sel, order, 'a'); // reverse direction unions
    expect([...sel.ids].sort()).toEqual(['a', 'b', 'c', 'd']);
  });

  it('extendSelection with no anchor behaves like toggle', () => {
    const sel = extendSelection(EMPTY_SELECTION, order, 'c');
    expect([...sel.ids]).toEqual(['c']);
  });

  it('extendSelection tolerates ids missing from the current order', () => {
    const sel = extendSelection({ anchorId: 'zz', ids: new Set(['zz']) }, order, 'b');
    expect(sel.ids.has('b')).toBe(true);
  });

  it('clearSelection empties everything', () => {
    expect(clearSelection()).toEqual(EMPTY_SELECTION);
  });
});

describe('nextAfterRemoval (auto-advance)', () => {
  it('prefers the next item below', () => {
    expect(nextAfterRemoval(order, 'b')).toBe('c');
  });
  it('falls back to the previous item at the end of the list', () => {
    expect(nextAfterRemoval(order, 'e')).toBe('d');
  });
  it('returns null for a single-item list or unknown id', () => {
    expect(nextAfterRemoval(['only'], 'only')).toBeNull();
    expect(nextAfterRemoval(order, 'zz')).toBeNull();
  });
});

describe('UndoStack', () => {
  it('pops LIFO and reports size', () => {
    const stack = new UndoStack();
    stack.push({ label: 'one', undo: () => {} });
    stack.push({ label: 'two', undo: () => {} });
    expect(stack.size).toBe(2);
    expect(stack.pop()?.label).toBe('two');
    expect(stack.pop()?.label).toBe('one');
    expect(stack.pop()).toBeUndefined();
  });

  it('caps at 50 entries, dropping the oldest', () => {
    const stack = new UndoStack();
    for (let i = 0; i < 55; i++) stack.push({ label: `e${i}`, undo: () => {} });
    expect(stack.size).toBe(50);
    let last: string | undefined;
    while (stack.size > 0) last = stack.pop()?.label;
    expect(last).toBe('e5');
  });
});
