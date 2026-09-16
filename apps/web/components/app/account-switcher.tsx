'use client';

import { Check, ChevronsUpDown, Inbox } from 'lucide-react';

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { MOD_KEY } from '@/lib/shortcuts';
import { useActiveAccount } from '@/lib/use-accounts';
import { cn } from '@/lib/utils';

/**
 * Sidebar inbox-scope switcher (M2.6 task 12): "All accounts" plus one row per
 * connected account, each badged with its mod+1..9 shortcut. Renders nothing
 * until at least one account is connected — no fabricated account rows.
 */
export function AccountSwitcher() {
  const { accounts, activeAccountId, setActiveAccountId } = useActiveAccount();
  if (accounts.length === 0) return null;
  const active = accounts.find((a) => a.id === activeAccountId) ?? null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="hover:bg-accent/60 flex w-full items-center gap-2 rounded-md p-2 text-left transition-colors"
        >
          <Inbox className="text-muted-foreground size-4 shrink-0" />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm">
              {active ? active.email : 'All accounts'}
            </span>
          </span>
          <ChevronsUpDown className="text-muted-foreground size-4 shrink-0" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-64">
        <DropdownMenuLabel className="text-muted-foreground text-xs">
          Inbox scope
        </DropdownMenuLabel>
        <DropdownMenuItem className="gap-2" onSelect={() => setActiveAccountId(null)}>
          <Check className={cn('size-4', activeAccountId !== null && 'invisible')} />
          <span className="flex-1 truncate">All accounts</span>
          <Kbd size="sm">{`${MOD_KEY}0`}</Kbd>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {accounts.map((account, index) => (
          <DropdownMenuItem
            key={account.id}
            className="gap-2"
            onSelect={() => setActiveAccountId(account.id)}
          >
            <Check className={cn('size-4', account.id !== activeAccountId && 'invisible')} />
            <span className="flex-1 truncate">{account.email}</span>
            {index < 9 && <Kbd size="sm">{`${MOD_KEY}${index + 1}`}</Kbd>}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
