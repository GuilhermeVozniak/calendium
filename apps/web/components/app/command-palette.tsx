'use client';

import * as React from 'react';
import { usePathname, useRouter } from 'next/navigation';
import {
  Archive,
  BellRing,
  CalendarDays,
  Clock,
  FileText,
  Inbox,
  LogOut,
  MailOpen,
  Monitor,
  Moon,
  PenLine,
  RotateCcw,
  Search,
  Send,
  Settings,
  Star,
  Sun,
} from 'lucide-react';

import { useQuery } from '@tanstack/react-query';

import { useCompose } from '@/components/app/compose';
import { useTheme } from '@/components/theme-provider';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { dispatchMailCommand, queueMailCommand, type MailCommand } from '@/lib/mail-utils';
import { signOut } from '@/lib/auth-client';
import { fetchSearch } from '@/lib/search-data';
import { MOD_KEY, useShortcuts } from '@/lib/shortcuts';

/** ⌘K command palette — every Calendium action, one keystroke away. */
export function CommandPalette() {
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState('');
  const [debounced, setDebounced] = React.useState('');
  const router = useRouter();
  const pathname = usePathname();
  const { openCompose } = useCompose();
  const { setTheme, resolvedTheme } = useTheme();

  // Reset the query when the palette closes; debounce it for live search.
  React.useEffect(() => {
    if (!open) {
      setQuery('');
      setDebounced('');
    }
  }, [open]);
  React.useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), 200);
    return () => clearTimeout(t);
  }, [query]);

  const { data: searchResults } = useQuery({
    queryKey: ['palette-search', debounced],
    enabled: open && debounced.length >= 2,
    queryFn: () => fetchSearch(debounced),
  });

  useShortcuts([
    {
      keys: 'mod+k',
      allowInInput: true,
      description: 'Toggle command palette',
      handler: () => setOpen((o) => !o),
    },
  ]);

  function run(action: () => void) {
    setOpen(false);
    action();
  }

  /** Mail-scoped commands need the inbox mounted to have something to act on. */
  function runMailCommand(command: MailCommand) {
    run(() => {
      if (pathname !== '/mail') {
        // Queue the command so the mail page runs it once mounted — a fixed
        // timeout would drop it if the inbox listener isn't attached yet.
        queueMailCommand(command);
        router.push('/mail');
      } else {
        dispatchMailCommand(command);
      }
    });
  }

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      title="Command palette"
      description="Type a command or search…"
    >
      <CommandInput
        placeholder="Type a command or search…"
        value={query}
        onValueChange={setQuery}
      />
      <CommandList>
        <CommandEmpty>No results found.</CommandEmpty>

        {searchResults && (searchResults.threads.length > 0 || searchResults.events.length > 0) && (
          <>
            <CommandGroup heading="Search results">
              {searchResults.threads.slice(0, 6).map((thread) => (
                <CommandItem
                  key={thread.id}
                  value={`${debounced} ${thread.subject} ${thread.participants[0]?.name ?? ''}`}
                  onSelect={() => run(() => router.push(`/mail?t=${thread.id}`))}
                >
                  <Inbox />
                  <span className="truncate">{thread.subject}</span>
                  <span className="text-muted-foreground ml-auto truncate text-xs">
                    {thread.participants[0]?.name ?? thread.participants[0]?.email}
                  </span>
                </CommandItem>
              ))}
              {searchResults.events.slice(0, 6).map((event) => (
                <CommandItem
                  key={event.id}
                  value={`${debounced} ${event.title}`}
                  onSelect={() =>
                    run(() => router.push(`/calendar?d=${encodeURIComponent(event.start)}`))
                  }
                >
                  <CalendarDays />
                  <span className="truncate">{event.title}</span>
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandSeparator />
          </>
        )}

        <CommandGroup heading="Mail">
          <CommandItem onSelect={() => run(() => openCompose())}>
            <PenLine />
            Compose
            <Kbd size="sm" className="ml-auto">
              C
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('search')}>
            <Search />
            Search mail
            <Kbd size="sm" className="ml-auto">
              /
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('archive')}>
            <Archive />
            Archive conversation
            <Kbd size="sm" className="ml-auto">
              E
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('snooze')}>
            <Clock />
            Snooze…
            <Kbd size="sm" className="ml-auto">
              H
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('reminder')}>
            <BellRing />
            Set follow-up reminder…
            <KbdGroup size="sm" keys={['⇧', 'H']} className="ml-auto" />
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('undo')}>
            <RotateCcw />
            Undo last action
            <Kbd size="sm" className="ml-auto">
              Z
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('star')}>
            <Star />
            Star / unstar
            <Kbd size="sm" className="ml-auto">
              S
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('mark-read')}>
            <MailOpen />
            Mark read
            <KbdGroup size="sm" keys={['⇧', 'I']} className="ml-auto" />
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Navigate">
          <CommandItem onSelect={() => run(() => router.push('/mail'))}>
            <Inbox />
            Go to Inbox
            <KbdGroup size="sm" keys={['G', 'I']} className="ml-auto" />
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/mail?view=starred'))}>
            <Star />
            Go to Starred
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/mail?view=snoozed'))}>
            <Clock />
            Go to Snoozed
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/mail?view=sent'))}>
            <Send />
            Go to Sent
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/mail?view=drafts'))}>
            <FileText />
            Go to Drafts
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/calendar'))}>
            <CalendarDays />
            Go to Calendar
            <KbdGroup size="sm" keys={['G', 'C']} className="ml-auto" />
          </CommandItem>
          <CommandItem onSelect={() => run(() => router.push('/settings'))}>
            <Settings />
            Go to Settings
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Appearance">
          <CommandItem onSelect={() => run(() => setTheme(resolvedTheme === 'dark' ? 'light' : 'dark'))}>
            {resolvedTheme === 'dark' ? <Sun /> : <Moon />}
            Toggle theme
          </CommandItem>
          <CommandItem onSelect={() => run(() => setTheme('light'))}>
            <Sun />
            Light theme
          </CommandItem>
          <CommandItem onSelect={() => run(() => setTheme('dark'))}>
            <Moon />
            Dark theme
          </CommandItem>
          <CommandItem onSelect={() => run(() => setTheme('system'))}>
            <Monitor />
            System theme
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Account">
          <CommandItem
            onSelect={() =>
              run(async () => {
                await signOut();
                router.replace('/signin');
              })
            }
          >
            <LogOut />
            Sign out
          </CommandItem>
        </CommandGroup>
      </CommandList>
      <div className="text-muted-foreground flex items-center gap-2 border-t px-3 py-2 text-xs">
        <Kbd size="sm">{MOD_KEY}</Kbd>
        <Kbd size="sm">K</Kbd>
        <span>to toggle · arrows to navigate · enter to run</span>
      </div>
    </CommandDialog>
  );
}
