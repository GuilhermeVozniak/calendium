/**
 * Thin desktop facade over the shared triage core (packages/shared/src/triage.ts)
 * plus the undo singleton and inverse map. Desktop has no TanStack Query for
 * mutations — it mutates its local `threads` state arrays directly (see
 * views/InboxView.tsx) — so this module only re-exports the pure selection /
 * auto-advance primitives and owns the one stateful piece: the undo stack.
 */
import {
  EMPTY_SELECTION,
  UndoStack,
  clearSelection,
  extendSelection,
  nextAfterRemoval,
  toggleSelected,
  type Selection,
  type ThreadAction,
} from '@calendium/shared';

export { EMPTY_SELECTION, clearSelection, extendSelection, nextAfterRemoval, toggleSelected };
export type { Selection };

/** Module singleton backing the InboxView "undo anything" (Z) shortcut. */
export const inboxUndo = new UndoStack();

export const ACTION_INVERSE: Partial<Record<ThreadAction, ThreadAction>> = {
  archive: 'move_to_inbox',
  trash: 'move_to_inbox',
  spam: 'move_to_inbox',
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  move_to_inbox: 'archive',
};
