'use client';

import * as React from 'react';
import { usePathname, useRouter } from 'next/navigation';
import {
  Archive,
  BellRing,
  CalendarCheck2,
  CalendarDays,
  CalendarPlus,
  Clock,
  FileText,
  Inbox,
  LayoutTemplate,
  ListChecks,
  LogOut,
  MailOpen,
  MessageSquareText,
  Monitor,
  Moon,
  PanelRight,
  PenLine,
  Plus,
  RotateCcw,
  Search,
  Send,
  Settings,
  Sparkles,
  Star,
  Sun,
  Tag,
} from 'lucide-react';

import { useQuery } from '@tanstack/react-query';

import { useAskSidebar } from '@/components/ai/ask-sidebar';
import { useCompose } from '@/components/app/compose';
import { useTheme } from '@/components/theme-provider';
import { dispatchAiEditCommand } from '@/components/compose/ai-edit-menu';
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
import { dispatchCalendarCommand, queueCalendarCommand, type CalendarCommand } from '@/lib/calendar-commands';
import { fetchCalendarSets } from '@/lib/set-data';
import type { CalendarView } from '@/lib/calendar-views';
import { VIEW_KEYS } from '@/lib/calendar-views';
import { dispatchMailCommand, queueMailCommand, type MailCommand } from '@/lib/mail-utils';
import { signOut } from '@/lib/auth-client';
import { fetchSearch } from '@/lib/search-data';
import { MOD_KEY, useShortcuts } from '@/lib/shortcuts';
import { teachShortcut } from '@/lib/shortcut-hints';
import { fetchEventTemplates } from '@/lib/template-data';
import { useInstance } from '@/lib/use-instance';

/** Palette label per view, keyed off the same VIEW_KEYS map the calendar page
 * binds its d/w/m/q/y/a shortcuts from - deriving both here keeps the two
 * from drifting apart. */
const VIEW_LABELS: Record<CalendarView, string> = {
  day: 'Day view',
  week: 'Week view',
  month: 'Month view',
  quarter: 'Quarter view',
  year: 'Year view',
  ticker: 'Ticker view',
};

const VIEW_ITEMS = (Object.entries(VIEW_KEYS) as [string, CalendarView][]).map(([key, view]) => ({
  key: key.toUpperCase(),
  view,
  label: VIEW_LABELS[view],
}));

/** ⌘K command palette — every Calendium action, one keystroke away. */
export function CommandPalette() {
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState('');
  const [debounced, setDebounced] = React.useState('');
  const router = useRouter();
  const pathname = usePathname();
  const { openCompose } = useCompose();
  const { setTheme, resolvedTheme } = useTheme();
  const { openSidebar } = useAskSidebar();
  const aiEnabled = useInstance().data?.features.ai ?? false;

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

  const { data: templates } = useQuery({
    queryKey: ['event-templates'],
    enabled: open,
    queryFn: fetchEventTemplates,
  });

  const { data: calendarSets } = useQuery({
    queryKey: ['calendar-sets'],
    enabled: open,
    queryFn: fetchCalendarSets,
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

  /** Calendar-scoped commands need the calendar page mounted to act on. */
  function runCalendarCommand(command: CalendarCommand) {
    run(() => {
      if (pathname !== '/calendar') {
        // Queue the command so the calendar page runs it once mounted — a
        // fixed timeout would drop it if the page's listener isn't attached
        // yet.
        queueCalendarCommand(command);
        router.push('/calendar');
      } else {
        dispatchCalendarCommand(command);
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
          <CommandItem onSelect={() => { teachShortcut('search', '/', 'Search'); runMailCommand('search'); }}>
            <Search />
            Search mail
            <Kbd size="sm" className="ml-auto">
              /
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('archive', 'E', 'Archive'); runMailCommand('archive'); }}>
            <Archive />
            Archive conversation
            <Kbd size="sm" className="ml-auto">
              E
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('snooze', 'H', 'Snooze'); runMailCommand('snooze'); }}>
            <Clock />
            Snooze…
            <Kbd size="sm" className="ml-auto">
              H
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('remind', '⇧H', 'Set follow-up reminder'); runMailCommand('reminder'); }}>
            <BellRing />
            Set follow-up reminder…
            <KbdGroup size="sm" keys={['⇧', 'H']} className="ml-auto" />
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('get-me-to-zero')}>
            <Inbox />
            Get Me To Zero
          </CommandItem>
          <CommandItem onSelect={() => runMailCommand('toggle-calendar-peek')}>
            <PanelRight />
            Toggle calendar peek
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('undo', 'Z', 'Undo'); runMailCommand('undo'); }}>
            <RotateCcw />
            Undo last action
            <Kbd size="sm" className="ml-auto">
              Z
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('star', 'S', 'Star'); runMailCommand('star'); }}>
            <Star />
            Star / unstar
            <Kbd size="sm" className="ml-auto">
              S
            </Kbd>
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('mark-read', '⇧I', 'Mark read'); runMailCommand('mark-read'); }}>
            <MailOpen />
            Mark read
            <KbdGroup size="sm" keys={['⇧', 'I']} className="ml-auto" />
          </CommandItem>
          <CommandItem onSelect={() => { teachShortcut('label', 'L', 'Label'); runMailCommand('label'); }}>
            <Tag />
            Label conversation…
            <Kbd size="sm" className="ml-auto">
              L
            </Kbd>
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

        <CommandGroup heading="Calendar">
          <CommandItem
            onSelect={() => {
              teachShortcut('cal-today', 'T', 'jump to today');
              runCalendarCommand({ type: 'today' });
            }}
          >
            <CalendarCheck2 />
            Go to today
            <Kbd size="sm" className="ml-auto">
              T
            </Kbd>
          </CommandItem>
          {VIEW_ITEMS.map(({ key, view, label }) => (
            <CommandItem
              key={view}
              onSelect={() => {
                teachShortcut(`cal-view-${view}`, key, `switch to ${label}`);
                runCalendarCommand({ type: 'view', view });
              }}
            >
              <CalendarDays />
              {label}
              <Kbd size="sm" className="ml-auto">
                {key}
              </Kbd>
            </CommandItem>
          ))}
          <CommandItem
            onSelect={() => {
              teachShortcut('cal-new-event', 'C', 'create a new event');
              runCalendarCommand({ type: 'new-event' });
            }}
          >
            <Plus />
            New event
            <Kbd size="sm" className="ml-auto">
              C
            </Kbd>
          </CommandItem>
          <CommandItem
            onSelect={() => {
              teachShortcut('cal-share-availability', 'S', 'share availability');
              runCalendarCommand({ type: 'share-availability' });
            }}
          >
            <Clock />
            Share availability
            <Kbd size="sm" className="ml-auto">
              S
            </Kbd>
          </CommandItem>
          {templates?.slice(0, 5).map((template) => (
            <CommandItem
              key={`cal-tpl-${template.id}`}
              value={`new event from template ${template.name} ${template.title}`}
              onSelect={() =>
                runCalendarCommand({ type: 'new-from-template', templateId: template.id })
              }
            >
              <LayoutTemplate />
              <span className="truncate">New event from template: {template.name}</span>
            </CommandItem>
          ))}
          {calendarSets?.map((set) => (
            <CommandItem
              key={`cal-set-${set.id}`}
              value={`apply calendar set ${set.name}`}
              onSelect={() => runCalendarCommand({ type: 'toggle-set', setId: set.id })}
            >
              <ListChecks />
              <span className="truncate">Apply calendar set: {set.name}</span>
            </CommandItem>
          ))}
        </CommandGroup>

        <CommandSeparator />

        {aiEnabled && (
          <>
            <CommandGroup heading="AI">
              <CommandItem onSelect={() => run(() => openSidebar())}>
                <MessageSquareText />
                Ask AI
                <KbdGroup size="sm" keys={[MOD_KEY, 'J']} className="ml-auto" />
              </CommandItem>
              <CommandItem
                onSelect={() => run(() => runMailCommand('propose-event'))}
              >
                <CalendarPlus />
                Create event with AI
              </CommandItem>
              <CommandItem onSelect={() => run(() => dispatchAiEditCommand('improve'))}>
                <Sparkles />
                AI: Improve draft
              </CommandItem>
              <CommandItem onSelect={() => run(() => dispatchAiEditCommand('shorten'))}>
                <Sparkles />
                AI: Shorten draft
              </CommandItem>
              <CommandItem onSelect={() => run(() => dispatchAiEditCommand('simplify'))}>
                <Sparkles />
                AI: Simplify draft
              </CommandItem>
              <CommandItem onSelect={() => run(() => dispatchAiEditCommand('fix_grammar'))}>
                <Sparkles />
                AI: Fix grammar in draft
              </CommandItem>
            </CommandGroup>
            <CommandSeparator />
          </>
        )}

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
