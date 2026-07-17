/**
 * Framework-agnostic triage primitives shared by web, desktop, and mobile:
 * the bulk range-selection model (x / shift+j / shift+k), the auto-advance
 * rule, and the undo-anything stack. Pure data + functions — no React.
 */

export interface Selection {
  readonly anchorId: string | null;
  readonly ids: ReadonlySet<string>;
}

export const EMPTY_SELECTION: Selection = { anchorId: null, ids: new Set() };

/** Toggle one conversation (the `x` key); the toggled row becomes the anchor. */
export function toggleSelected(sel: Selection, id: string): Selection {
  const ids = new Set(sel.ids);
  if (ids.has(id)) ids.delete(id);
  else ids.add(id);
  return { anchorId: id, ids };
}

/**
 * Extend the selection from the anchor to the cursor (shift+j / shift+k),
 * unioning with what is already selected (Superhuman range semantics). An
 * unknown anchor or cursor degrades to a plain toggle so keyboard flow never
 * dead-ends after a list refetch.
 */
export function extendSelection(
  sel: Selection,
  orderedIds: readonly string[],
  cursorId: string
): Selection {
  const anchor = sel.anchorId ?? cursorId;
  const a = orderedIds.indexOf(anchor);
  const c = orderedIds.indexOf(cursorId);
  if (a < 0 || c < 0) return toggleSelected(sel, cursorId);
  const [lo, hi] = a <= c ? [a, c] : [c, a];
  const ids = new Set(sel.ids);
  for (let i = lo; i <= hi; i++) ids.add(orderedIds[i]!);
  return { anchorId: anchor, ids };
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
