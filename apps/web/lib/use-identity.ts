'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';

import { authClient } from '@/lib/auth-client';
import { DEMO_MODE } from '@/lib/demo';
import { MOCK_ME } from '@/lib/mail-mock';
import { MOCK_SELF_EMAIL } from '@/lib/calendar-mock';
import { fetchAccounts } from '@/lib/settings-data';

/**
 * The set of email addresses that belong to the signed-in user — the Better
 * Auth session email plus every connected-account address. Used to detect
 * "me/you" in thread participant lines, reply recipients, and RSVP self-state,
 * instead of a hardcoded mock identity. In explicit demo mode the mock self
 * addresses are included so the sample dataset still resolves correctly.
 */
export function useSelfEmails(): ReadonlySet<string> {
  const { data: session } = authClient.useSession();
  const { data: accounts } = useQuery({
    queryKey: ['accounts'],
    queryFn: fetchAccounts,
    staleTime: 5 * 60_000,
  });
  const sessionEmail = session?.user?.email ?? null;

  return React.useMemo(() => {
    const set = new Set<string>();
    if (sessionEmail) set.add(sessionEmail.toLowerCase());
    for (const account of accounts ?? []) set.add(account.email.toLowerCase());
    if (DEMO_MODE) {
      set.add(MOCK_ME.email.toLowerCase());
      set.add(MOCK_SELF_EMAIL.toLowerCase());
    }
    return set;
  }, [sessionEmail, accounts]);
}
