'use client';

import * as React from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, UserCog, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';
import { setActingAs, useActingAs } from '@/lib/act-as';
import { getApiClient } from '@/lib/api';
import { cn } from '@/lib/utils';

/**
 * Switch identity: set/clear acting-as and drop every cached query — the
 * cache must never mix the assistant's own data with the principal's.
 */
function useSwitchActing() {
  const queryClient = useQueryClient();
  return React.useCallback(
    (principalId: string | null) => {
      setActingAs(principalId);
      queryClient.clear();
    },
    [queryClient]
  );
}

/**
 * Full-width banner badging the whole UI while acting for a principal
 * (M2.7 Task 15). Renders nothing when not acting. The principal is shown by
 * user id — the only identity the delegation API exposes (server data only).
 */
export function ActingBanner() {
  const acting = useActingAs();
  const switchActing = useSwitchActing();
  if (!acting) return null;
  return (
    <div className="bg-primary text-primary-foreground flex shrink-0 items-center gap-2 px-4 py-1.5 text-xs">
      <UserCog className="size-3.5 shrink-0" />
      <span className="min-w-0 flex-1 truncate">
        <span className="font-medium">Acting for {acting}</span> — mail and calendar actions run
        on their account and are audit-logged.
      </span>
      <Button
        size="sm"
        variant="secondary"
        className="h-6 px-2 text-xs"
        onClick={() => switchActing(null)}
      >
        <X className="size-3" />
        Stop acting
      </Button>
    </div>
  );
}

/**
 * Act-as switcher rows for the account menu: one row per ACTIVE grant where
 * the signed-in user is the assistant, plus "Stop acting" while acting.
 * Renders nothing when there is nothing to act for and no acting state.
 */
export function ActAsMenuItems() {
  const acting = useActingAs();
  const switchActing = useSwitchActing();
  const delegationsQuery = useQuery({
    queryKey: ['delegations'],
    queryFn: () => getApiClient().listDelegations(),
  });
  const actable = (delegationsQuery.data?.asAssistant ?? []).filter(
    (d) => d.status === 'active'
  );
  if (actable.length === 0 && !acting) return null;
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuLabel className="text-muted-foreground text-xs">Act as</DropdownMenuLabel>
      {actable.map((d) => (
        <DropdownMenuItem
          key={d.id}
          className="gap-2"
          onSelect={() => switchActing(d.principalId)}
        >
          <Check className={cn('size-4', d.principalId !== acting && 'invisible')} />
          <span className="truncate">{d.principalId}</span>
        </DropdownMenuItem>
      ))}
      {acting && (
        <DropdownMenuItem className="gap-2" onSelect={() => switchActing(null)}>
          <X className="size-4" />
          Stop acting
        </DropdownMenuItem>
      )}
    </>
  );
}
