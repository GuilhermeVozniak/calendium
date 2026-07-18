import type { Calendar, ConnectedAccount } from '@calendium/shared';

export interface AccountCalendarGroup {
  accountId: string;
  /** ConnectedAccount.email, or the raw accountId when no matching account is loaded. */
  email: string;
  calendars: Calendar[];
}

/**
 * Groups calendars by their owning connected account for the sidebar list
 * (Task 12 - multi-account overlay). Group order follows `accounts`; calendar
 * order within a group follows `calendars`. Calendars whose accountId has no
 * matching entry in `accounts` (e.g. a stale query cache) still get a group -
 * headed by the raw accountId - rather than being silently dropped.
 */
export function groupCalendarsByAccount(
  calendars: Calendar[],
  accounts: ConnectedAccount[]
): AccountCalendarGroup[] {
  const byAccountId = new Map<string, Calendar[]>();
  for (const calendar of calendars) {
    const group = byAccountId.get(calendar.accountId);
    if (group) group.push(calendar);
    else byAccountId.set(calendar.accountId, [calendar]);
  }

  const groups: AccountCalendarGroup[] = [];
  for (const account of accounts) {
    const group = byAccountId.get(account.id);
    if (!group) continue;
    groups.push({ accountId: account.id, email: account.email, calendars: group });
    byAccountId.delete(account.id);
  }
  // Anything left belongs to an accountId absent from `accounts`.
  for (const [accountId, group] of byAccountId) {
    groups.push({ accountId, email: accountId, calendars: group });
  }
  return groups;
}
