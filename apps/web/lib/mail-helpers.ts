import type { Thread } from '@calendium/shared';

/**
 * Compute the set of label IDs shared by all threads in the list.
 * Used for bulk operations: a label is only marked active if ALL selected threads have it.
 */
export function sharedLabelIds(threads: Thread[]): Set<string> {
  if (threads.length === 0) return new Set();
  if (threads.length === 1) return new Set(threads[0]!.labelIds);

  const firstLabelIds = new Set(threads[0]!.labelIds);
  for (let i = 1; i < threads.length; i++) {
    const thread = threads[i]!;
    for (const labelId of firstLabelIds) {
      if (!thread.labelIds.includes(labelId)) {
        firstLabelIds.delete(labelId);
      }
    }
  }
  return firstLabelIds;
}
