import { describe, expect, it } from 'vitest';

import {
  ACTION_INVERSE,
  EMPTY_SELECTION,
  extendSelection,
  inboxUndo,
  nextAfterRemoval,
  pruneSelection,
  toggleSelected,
  type Selection,
} from './triage';

describe('desktop triage facade', () => {
  it('re-exports a working selection model', () => {
    const sel = extendSelection(toggleSelected(EMPTY_SELECTION, 'a'), ['a', 'b', 'c'], 'c');
    expect([...sel.ids].sort()).toEqual(['a', 'b', 'c']);
  });

  it('advances past a removed row', () => {
    expect(nextAfterRemoval(['a', 'b'], 'a')).toBe('b');
  });

  it('maps every destructive action to an inverse', () => {
    expect(ACTION_INVERSE.archive).toBe('move_to_inbox');
    expect(ACTION_INVERSE.read).toBe('unread');
  });

  it('exposes a shared undo stack singleton', () => {
    inboxUndo.clear();
    inboxUndo.push({ label: 'x', undo: () => {} });
    expect(inboxUndo.size).toBe(1);
    inboxUndo.clear();
  });

  it('prunes stale ids and keeps valid ones', () => {
    const selection: Selection = {
      anchorId: 'a',
      ids: new Set(['a', 'b', 'c']),
      cursor: 'c',
      rangeIds: new Set(['b', 'c']),
    };
    const validIds = new Set(['a', 'b']); // 'c' is stale

    const result = pruneSelection(selection, validIds);

    expect(result.ids).toEqual(new Set(['a', 'b']));
    expect(result.rangeIds).toEqual(new Set(['b'])); // 'c' removed from range
    expect(result.anchorId).toBe('a'); // valid anchor preserved
    expect(result.cursor).toBeNull(); // stale cursor becomes null
  });

  it('nulls stale anchor but keeps valid ids', () => {
    const selection: Selection = {
      anchorId: 'x', // stale anchor
      ids: new Set(['x', 'a', 'b']),
      cursor: 'b',
      rangeIds: new Set(['a', 'b']),
    };
    const validIds = new Set(['a', 'b']);

    const result = pruneSelection(selection, validIds);

    expect(result.ids).toEqual(new Set(['a', 'b']));
    expect(result.anchorId).toBeNull(); // stale anchor nulled
    expect(result.cursor).toBe('b'); // valid cursor preserved
    expect(result.rangeIds).toEqual(new Set(['a', 'b']));
  });

  it('returns the original selection if all ids are valid', () => {
    const selection: Selection = {
      anchorId: 'a',
      ids: new Set(['a', 'b']),
      cursor: 'b',
      rangeIds: new Set(['a', 'b']),
    };
    const validIds = new Set(['a', 'b', 'c']); // all selection ids are valid

    const result = pruneSelection(selection, validIds);

    expect(result).toEqual(selection);
  });

  it('returns empty-like selection when all ids are stale', () => {
    const selection: Selection = {
      anchorId: 'a',
      ids: new Set(['a', 'b']),
      cursor: 'b',
      rangeIds: new Set(['a', 'b']),
    };
    const validIds = new Set(['x', 'y']); // all stale

    const result = pruneSelection(selection, validIds);

    expect(result.ids.size).toBe(0);
    expect(result.anchorId).toBeNull();
    expect(result.cursor).toBeNull();
    expect(result.rangeIds.size).toBe(0);
  });

  it('skips pruning if selection is already empty', () => {
    const selection: Selection = {
      anchorId: null,
      ids: new Set(),
      cursor: null,
      rangeIds: new Set(),
    };
    const validIds = new Set(['a', 'b']);

    const result = pruneSelection(selection, validIds);

    expect(result).toEqual(selection);
  });
});
