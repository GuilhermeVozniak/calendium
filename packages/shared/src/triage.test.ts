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
    // Range-replacement semantics (updated from the old always-union behavior):
    // extending from anchor 'b' to 'a' replaces the range with [a, b] — 'c' and
    // 'd' were only ever part of the anchor->cursor range, so they're released
    // rather than unioned in.
    sel = extendSelection(sel, order, 'a');
    expect([...sel.ids].sort()).toEqual(['a', 'b']);
  });

  it('extendSelection with no anchor behaves like toggle', () => {
    const sel = extendSelection(EMPTY_SELECTION, order, 'c');
    expect([...sel.ids]).toEqual(['c']);
  });

  it('extendSelection tolerates ids missing from the current order', () => {
    const sel = extendSelection(
      { anchorId: 'zz', ids: new Set(['zz']), cursor: 'zz', rangeIds: new Set(['zz']) },
      order,
      'b'
    );
    expect(sel.ids.has('b')).toBe(true);
  });

  it('extendSelection overshoot-then-retreat releases the overshot ids', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'c'); // anchor c (index 2)
    sel = extendSelection(sel, order, 'e'); // overshoot to e (index 4)
    expect([...sel.ids].sort()).toEqual(['c', 'd', 'e']);
    sel = extendSelection(sel, order, 'd'); // retreat to d (index 3)
    expect([...sel.ids].sort()).toEqual(['c', 'd']); // 'e' released
    expect(sel.anchorId).toBe('c');
    expect(sel.cursor).toBe('d');
    expect([...sel.rangeIds].sort()).toEqual(['c', 'd']);
  });

  it('preserves toggled ids outside the range when the range shrinks', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'a'); // toggled, outside any range
    sel = toggleSelected(sel, 'c'); // new anchor c (index 2)
    sel = extendSelection(sel, order, 'e'); // range -> c,d,e
    sel = extendSelection(sel, order, 'd'); // retreat -> c,d ('e' released)
    expect([...sel.ids].sort()).toEqual(['a', 'c', 'd']);
  });

  it('extending across the anchor replaces the range on the new side', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'c'); // anchor c (index 2)
    sel = extendSelection(sel, order, 'e'); // range -> c,d,e
    sel = extendSelection(sel, order, 'a'); // cross the anchor to a (index 0)
    expect([...sel.ids].sort()).toEqual(['a', 'b', 'c']); // d,e released
    expect(sel.anchorId).toBe('c');
    expect(sel.cursor).toBe('a');
    expect([...sel.rangeIds].sort()).toEqual(['a', 'b', 'c']);
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
