import type { InboxSplit } from '@calendium/shared';
import {
  Archive,
  AtSign,
  CalendarDays,
  Clock,
  Inbox,
  type LucideIcon,
  Newspaper,
  Search,
  Settings,
  Star,
  Users,
  Zap,
} from 'lucide-react';
import { useEffect, useState } from 'react';

import { Button } from '@/ui/button';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from '@/ui/command';
import { Kbd } from '@/ui/kbd';
import { CalendarView } from '@/views/CalendarView';
import { emitMailAction, InboxView } from '@/views/InboxView';
import { SettingsView } from '@/views/SettingsView';

type View = 'inbox' | 'calendar' | 'settings';

const SPLITS: { id: InboxSplit; label: string; icon: LucideIcon }[] = [
  { id: 'important', label: 'Important', icon: Zap },
  { id: 'vip', label: 'VIP', icon: Star },
  { id: 'team', label: 'Team', icon: Users },
  { id: 'calendar', label: 'Calendar', icon: CalendarDays },
  { id: 'news', label: 'News', icon: Newspaper },
  { id: 'social', label: 'Social', icon: AtSign },
  { id: 'other', label: 'Other', icon: Archive },
];

function RailItem({
  icon: Icon,
  label,
  active,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        'flex w-full select-none items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors ' +
        (active
          ? 'bg-accent font-medium text-accent-foreground'
          : 'text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground')
      }
    >
      <Icon className="size-4 shrink-0" />
      {label}
    </button>
  );
}

export default function App() {
  const [view, setView] = useState<View>('inbox');
  const [split, setSplit] = useState<InboxSplit>('important');
  const [paletteOpen, setPaletteOpen] = useState(false);

  // Global ⌘K / Ctrl+K — the palette is reachable from anywhere.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((open) => !open);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const goToSplit = (s: InboxSplit) => {
    setSplit(s);
    setView('inbox');
  };

  const runPalette = (fn: () => void) => {
    setPaletteOpen(false);
    fn();
  };

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Titlebar — draggable region under the hidden-inset macOS titlebar. */}
      <header className="titlebar-drag flex h-10 shrink-0 items-center border-b pl-20 pr-3">
        <span className="select-none text-[13px] font-semibold tracking-tight">Calendium</span>
        <div className="no-drag ml-auto">
          <Button
            variant="outline"
            size="sm"
            className="w-56 justify-start gap-2 font-normal text-muted-foreground"
            onClick={() => setPaletteOpen(true)}
          >
            <Search />
            Search or jump to…
            <Kbd className="ml-auto">⌘K</Kbd>
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* Left rail: splits, calendar, settings. */}
        <aside className="flex w-52 shrink-0 flex-col gap-4 overflow-y-auto border-r p-2">
          <nav className="flex flex-col gap-0.5">
            <div className="px-2 pb-1 pt-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Inbox
            </div>
            {SPLITS.map((s) => (
              <RailItem
                key={s.id}
                icon={s.icon}
                label={s.label}
                active={view === 'inbox' && split === s.id}
                onClick={() => goToSplit(s.id)}
              />
            ))}
          </nav>
          <nav className="mt-auto flex flex-col gap-0.5 border-t pt-2">
            <RailItem
              icon={CalendarDays}
              label="Calendar"
              active={view === 'calendar'}
              onClick={() => setView('calendar')}
            />
            <RailItem
              icon={Settings}
              label="Settings"
              active={view === 'settings'}
              onClick={() => setView('settings')}
            />
          </nav>
        </aside>

        <main className="min-w-0 flex-1">
          {view === 'inbox' && <InboxView split={split} />}
          {view === 'calendar' && <CalendarView />}
          {view === 'settings' && <SettingsView />}
        </main>
      </div>

      <CommandDialog open={paletteOpen} onOpenChange={setPaletteOpen}>
        <CommandInput placeholder="Type a command or search…" />
        <CommandList>
          <CommandEmpty>No results found.</CommandEmpty>
          <CommandGroup heading="Go to">
            <CommandItem onSelect={() => runPalette(() => setView('inbox'))}>
              <Inbox /> Inbox
            </CommandItem>
            <CommandItem onSelect={() => runPalette(() => setView('calendar'))}>
              <CalendarDays /> Calendar
            </CommandItem>
            <CommandItem onSelect={() => runPalette(() => setView('settings'))}>
              <Settings /> Settings
            </CommandItem>
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Splits">
            {SPLITS.map((s) => (
              <CommandItem key={s.id} onSelect={() => runPalette(() => goToSplit(s.id))}>
                <s.icon /> {s.label}
              </CommandItem>
            ))}
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Conversation">
            <CommandItem onSelect={() => runPalette(() => emitMailAction('archive'))}>
              <Archive /> Archive
              <CommandShortcut>E</CommandShortcut>
            </CommandItem>
            <CommandItem onSelect={() => runPalette(() => emitMailAction('star'))}>
              <Star /> Star / unstar
              <CommandShortcut>S</CommandShortcut>
            </CommandItem>
            <CommandItem onSelect={() => runPalette(() => emitMailAction('snooze'))}>
              <Clock /> Snooze 3 hours
              <CommandShortcut>Z</CommandShortcut>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </div>
  );
}
