/**
 * Framework-agnostic triage primitives shared by web, desktop, and mobile:
 * the bulk range-selection model (x / shift+j / shift+k), the auto-advance
 * rule, and the undo-anything stack. Pure data + functions — no React.
 */

export interface Selection {
  readonly anchorId: string | null;
  readonly ids: ReadonlySet<string>;
  /** Last range endpoint reached via extendSelection (the "cursor"). */
  readonly cursor: string | null;
  /** Ids currently part of the live anchor->cursor range (a subset of `ids`). */
  readonly rangeIds: ReadonlySet<string>;
}

export const EMPTY_SELECTION: Selection = {
  anchorId: null,
  ids: new Set(),
  cursor: null,
  rangeIds: new Set(),
};

/** Toggle one conversation (the `x` key); the toggled row becomes the anchor. */
export function toggleSelected(sel: Selection, id: string): Selection {
  const ids = new Set(sel.ids);
  const selecting = !ids.has(id);
  if (selecting) ids.add(id);
  else ids.delete(id);

  // A freshly toggled-on id becomes a brand new anchor/range of its own, so a
  // later extendSelection only ever grows or shrinks *this* range. Toggling
  // off just drops the id from the live range without disturbing the anchor
  // or cursor of whatever range is still in effect.
  if (selecting) {
    return { anchorId: id, ids, cursor: id, rangeIds: new Set([id]) };
  }
  const rangeIds = new Set(sel.rangeIds);
  rangeIds.delete(id);
  return { anchorId: id, ids, cursor: sel.cursor, rangeIds };
}

/**
 * Extend the selection from the anchor to the cursor (shift+j / shift+k).
 * The contiguous anchor->cursor range tracks the cursor exactly: retreating
 * after an overshoot releases the ids that are no longer in range, while ids
 * selected via toggleSelected outside the range are preserved (file-manager /
 * Superhuman semantics). An unknown anchor or cursor degrades to a plain
 * toggle so keyboard flow never dead-ends after a list refetch.
 */
export function extendSelection(
  sel: Selection,
  orderedIds: readonly string[],
  targetId: string
): Selection {
  const anchor = sel.anchorId ?? targetId;
  const a = orderedIds.indexOf(anchor);
  const c = orderedIds.indexOf(targetId);
  if (a < 0 || c < 0) return toggleSelected(sel, targetId);
  const lo = Math.min(a, c);
  const hi = Math.max(a, c);
  const newRange = new Set(orderedIds.slice(lo, hi + 1));

  const ids = new Set(sel.ids);
  for (const id of sel.rangeIds) ids.delete(id);
  for (const id of newRange) ids.add(id);

  return { anchorId: anchor, ids, cursor: targetId, rangeIds: newRange };
}

export function clearSelection(): Selection {
  return EMPTY_SELECTION;
}

/**
 * Auto-advance: the conversation to open after `removedId` leaves the list —
 * the one below it, else the one above, else null (list emptied).
 */
export function nextAfterRemoval(
  orderedIds: readonly string[],
  removedId: string
): string | null {
  const i = orderedIds.indexOf(removedId);
  if (i < 0) return null;
  return orderedIds[i + 1] ?? orderedIds[i - 1] ?? null;
}

export interface UndoEntry {
  label: string;
  undo: () => void | Promise<void>;
}

const UNDO_CAP = 50;

/** LIFO stack behind "undo anything" (Z). Capped so it cannot leak forever. */
export class UndoStack {
  private entries: UndoEntry[] = [];

  push(entry: UndoEntry): void {
    this.entries.push(entry);
    if (this.entries.length > UNDO_CAP) this.entries.shift();
  }

  pop(): UndoEntry | undefined {
    return this.entries.pop();
  }

  clear(): void {
    this.entries = [];
  }

  get size(): number {
    return this.entries.length;
  }
}
