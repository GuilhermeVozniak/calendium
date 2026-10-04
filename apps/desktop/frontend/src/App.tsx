import type { InboxSplit } from '@calendium/shared';
import { subscriptionDenialReason } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import {
  Archive,
  AtSign,
  CalendarDays,
  Clock,
  Inbox,
  type LucideIcon,
  Newspaper,
  PenSquare,
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
import { Toaster } from '@/ui/toaster';
import { api, onPaymentRequired, orMock } from '@/lib/api';
import { openCompose } from '@/lib/compose';
import { mockSearch, mockSubscription } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import {
  desktop,
  globalShortcutsEnabled,
  isDesktop,
  onGlobalShortcut,
  setGlobalShortcutsEnabled,
  wailsRuntime,
} from '@/lib/wails';
import { toast } from '@/lib/toast';
import {
  AUTO_JOINED_EVENT,
  pushAutoJoinSettings,
  TRAY_ACTION_EVENT,
  useTrayFeed,
} from '@/lib/tray';
import { CalendarView, emitFocusDate } from '@/views/CalendarView';
import { ComposeHost } from '@/views/ComposeView';
import { emitFocusThread, emitMailAction, InboxView } from '@/views/InboxView';
import { PaywallView } from '@/views/PaywallView';
import { SettingsView } from '@/views/SettingsView';
import { UpdateBanner } from '@/views/UpdateBanner';

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
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');

  // --- Tray + auto-join (M2.6 Tasks 10-11) -------------------------------
  // Feed the Go host's menu-bar tray with upcoming events (Task 10).
  useTrayFeed();
  // Handle tray menu actions, push the persisted auto-join setting to the
  // host on startup, and toast when the host actually auto-joined a meeting.
  useEffect(() => {
    if (!isDesktop) return;
    pushAutoJoinSettings();
    const offAction = wailsRuntime.EventsOn(TRAY_ACTION_EVENT, (...data: unknown[]) => {
      const action = data[0];
      // Mirror the global-shortcut handler: a tray click is an explicit
      // "take me to the app", so surface the window before acting.
      wailsRuntime.WindowShow();
      if (action === 'open-calendar') setView('calendar');
      else if (action === 'compose') openCompose();
    });
    const offJoined = wailsRuntime.EventsOn(AUTO_JOINED_EVENT, (...data: unknown[]) => {
      const ev = data[0] as { title?: string } | undefined;
      toast({ title: `Joined ${ev?.title ?? 'meeting'}` });
    });
    return () => {
      offAction();
      offJoined();
    };
  }, []);
  // --- end tray + auto-join ----------------------------------------------

  // Global ⌘K / Ctrl+K opens the palette; C composes a new message. Both are
  // reachable from anywhere (ignoring typing targets for the C shortcut).
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((open) => !open);
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      ) {
        return;
      }
      if (e.key.toLowerCase() === 'c') {
        e.preventDefault();
        openCompose();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  // --- Task 9: system-wide global shortcuts (host hotkeys → UI) ---
  // On boot, push the persisted Settings toggle to the Go host (it cannot
  // read localStorage itself); then bring the window forward and open the
  // composer / command palette whenever a registered hotkey fires — even
  // while Calendium is unfocused.
  useEffect(() => {
    if (isDesktop) void setGlobalShortcutsEnabled(globalShortcutsEnabled());
    return onGlobalShortcut((action) => {
      wailsRuntime.WindowShow();
      if (action === 'compose') openCompose();
      else setPaletteOpen(true);
    });
  }, []);
  // --- end Task 9 ---

  // Reset the query when the palette closes; debounce it for search.
  useEffect(() => {
    if (!paletteOpen) setQuery('');
  }, [paletteOpen]);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), 200);
    return () => clearTimeout(t);
  }, [query]);

  const { data: searchResults } = useQuery({
    queryKey: ['search', debounced],
    enabled: paletteOpen && debounced.length >= 2,
    queryFn: () =>
      orMock(
        () => api.search(debounced),
        () => mockSearch(debounced)
      ),
  });

  const { config, demoMode } = useServerConfig();

  // The Go host's daily release check starts paused (it cannot read the demo
  // flag in localStorage); resume it here outside demo mode and keep it
  // paused in demo, which never dials out (apps/desktop/update.go).
  useEffect(() => {
    void desktop.SetUpdateChecksEnabled(!demoMode);
  }, [demoMode]);

  const billingEnabled = config?.features?.billing ?? false;
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: () =>
      orMock(
        () => api.getSubscription(),
        () => mockSubscription()
      ),
    enabled: billingEnabled,
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  });
  // Any 402 from the API client means the server now denies access (e.g. the
  // trial ended mid-session): re-check right away instead of waiting for the
  // poll. cancelRefetch:false joins an in-flight fetch, so a burst of 402s
  // (or one from the subscription endpoint itself) can never loop.
  const queryClient = useQueryClient();
  useEffect(
    () =>
      onPaymentRequired(() => {
        void queryClient.invalidateQueries({ queryKey: ['subscription'] }, { cancelRefetch: false });
      }),
    [queryClient]
  );
  // Fail open on fetch errors; only a fetched subscription that denies access paywalls.
  const paywallReason =
    billingEnabled && subscriptionQuery.data ? subscriptionDenialReason(subscriptionQuery.data) : null;

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

      {/* Above the body so it shows on the paywall screen too; hidden in demo. */}
      {!demoMode && <UpdateBanner />}

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
          {paywallReason ? (
            <PaywallView reason={paywallReason} />
          ) : (
            <>
              {view === 'inbox' && <InboxView split={split} />}
              {view === 'calendar' && <CalendarView />}
              {view === 'settings' && <SettingsView />}
            </>
          )}
        </main>
      </div>

      <CommandDialog open={paletteOpen} onOpenChange={setPaletteOpen}>
        <CommandInput
          placeholder="Search mail, events, or jump to…"
          value={query}
          onValueChange={setQuery}
        />
        <CommandList>
          <CommandEmpty>No results found.</CommandEmpty>
          {searchResults &&
            (searchResults.threads.length > 0 || searchResults.events.length > 0) && (
              <>
                <CommandGroup heading="Search results">
                  {searchResults.threads.slice(0, 6).map((thread) => (
                    <CommandItem
                      key={thread.id}
                      value={`${debounced} ${thread.subject} ${thread.participants[0]?.name ?? ''}`}
                      onSelect={() =>
                        runPalette(() => {
                          goToSplit(thread.split);
                          emitFocusThread(thread.id);
                        })
                      }
                    >
                      <Inbox />
                      <span className="truncate">{thread.subject}</span>
                      <span className="ml-auto truncate text-xs text-muted-foreground">
                        {thread.participants[0]?.name ?? thread.participants[0]?.email}
                      </span>
                    </CommandItem>
                  ))}
                  {searchResults.events.slice(0, 6).map((event) => (
                    <CommandItem
                      key={event.id}
                      value={`${debounced} ${event.title}`}
                      onSelect={() =>
                        runPalette(() => {
                          setView('calendar');
                          emitFocusDate(event.start);
                        })
                      }
                    >
                      <CalendarDays />
                      <span className="truncate">{event.title}</span>
                      <span className="ml-auto shrink-0 truncate text-xs tabular-nums text-muted-foreground">
                        {format(new Date(event.start), 'MMM d, HH:mm')}
                      </span>
                    </CommandItem>
                  ))}
                </CommandGroup>
                <CommandSeparator />
              </>
            )}
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
          <CommandGroup heading="Mail">
            <CommandItem onSelect={() => runPalette(() => openCompose())}>
              <PenSquare /> New message
              <CommandShortcut>C</CommandShortcut>
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
              <CommandShortcut>H</CommandShortcut>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </CommandDialog>

      <ComposeHost />
      <Toaster />
    </div>
  );
}
