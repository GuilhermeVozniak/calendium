import { describe, expect, it } from 'vitest';

import {
  ACTION_INVERSE,
  EMPTY_SELECTION,
  extendSelection,
  inboxUndo,
  nextAfterRemoval,
  toggleSelected,
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
});
