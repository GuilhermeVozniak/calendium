'use client';

import * as React from 'react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import {
  CalendarDays,
  CalendarRange,
  Check,
  ChevronsUpDown,
  Clock,
  Crown,
  FileText,
  Inbox,
  Layers,
  LogOut,
  Monitor,
  Moon,
  Newspaper,
  PenLine,
  Plus,
  Send,
  Settings,
  Star,
  Sun,
  Users,
  WifiOff,
} from 'lucide-react';

import { CommandPalette } from '@/components/app/command-palette';
import { ComposeProvider, useCompose } from '@/components/app/compose';
import { useTheme } from '@/components/theme-provider';
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Kbd } from '@/components/ui/kbd';
import { TooltipProvider } from '@/components/ui/tooltip';
import { authClient, signOut } from '@/lib/auth-client';
import { useShortcuts } from '@/lib/shortcuts';
import { useApiOnline } from '@/lib/use-mail';
import { cn } from '@/lib/utils';

type SessionUser = typeof authClient.$Infer.Session.user;

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const { data: session, isPending } = authClient.useSession();
  const user = session?.user ?? null;

  React.useEffect(() => {
    if (!isPending && !session) router.replace('/signin');
  }, [isPending, session, router]);

  if (isPending || !session) return <Splash />;

  return (
    <ComposeProvider>
      <TooltipProvider>
        <div className="bg-background flex h-svh overflow-hidden">
          <React.Suspense fallback={<div className="w-60 shrink-0 border-r" />}>
            <SideRail user={user} />
          </React.Suspense>
          <div className="flex min-w-0 flex-1 flex-col">
            <OfflineBanner />
            <main className="min-h-0 flex-1">{children}</main>
          </div>
        </div>
        <CommandPalette />
        <GlobalShortcuts />
      </TooltipProvider>
    </ComposeProvider>
  );
}

function Splash() {
  return (
    <div className="flex min-h-svh items-center justify-center">
      <div className="bg-primary text-primary-foreground flex size-12 animate-pulse items-center justify-center rounded-xl">
        <CalendarRange className="size-6" />
      </div>
    </div>
  );
}

/** App-wide single-key shortcuts that need the compose context. */
function GlobalShortcuts() {
  const { openCompose } = useCompose();
  const pathname = usePathname();
  useShortcuts([
    {
      keys: 'c',
      description: 'Compose',
      handler: () => openCompose(),
      // The calendar page binds 'c' to "new event"; don't double-fire there.
      enabled: pathname !== '/calendar',
    },
  ]);
  return null;
}

function OfflineBanner() {
  const online = useApiOnline();
  if (online) return null;
  return (
    <div className="bg-muted text-muted-foreground flex shrink-0 items-center gap-2 border-b px-4 py-1.5 text-xs">
      <WifiOff className="size-3.5 shrink-0" />
      <span>
        <span className="text-foreground font-medium">Offline demo mode</span> — the Calendium API
        is unreachable. Showing sample data; actions apply locally.
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Left rail
// ---------------------------------------------------------------------------

interface RailItem {
  label: string;
  href: string;
  icon: React.ComponentType<{ className?: string }>;
  isActive: (pathname: string, split: string | null, view: string | null) => boolean;
}

const SPLIT_ITEMS: RailItem[] = [
  {
    label: 'Important',
    href: '/mail?split=important',
    icon: Inbox,
    isActive: (p, s, v) => p === '/mail' && !v && (s === null || s === 'important'),
  },
  { label: 'VIP', href: '/mail?split=vip', icon: Crown, isActive: (p, s, v) => p === '/mail' && !v && s === 'vip' },
  { label: 'Team', href: '/mail?split=team', icon: Users, isActive: (p, s, v) => p === '/mail' && !v && s === 'team' },
  {
    label: 'Calendar',
    href: '/mail?split=calendar',
    icon: CalendarDays,
    isActive: (p, s, v) => p === '/mail' && !v && s === 'calendar',
  },
  { label: 'News', href: '/mail?split=news', icon: Newspaper, isActive: (p, s, v) => p === '/mail' && !v && s === 'news' },
  { label: 'Other', href: '/mail?split=other', icon: Layers, isActive: (p, s, v) => p === '/mail' && !v && s === 'other' },
];

const VIEW_ITEMS: RailItem[] = [
  { label: 'Starred', href: '/mail?view=starred', icon: Star, isActive: (p, _s, v) => p === '/mail' && v === 'starred' },
  { label: 'Snoozed', href: '/mail?view=snoozed', icon: Clock, isActive: (p, _s, v) => p === '/mail' && v === 'snoozed' },
  { label: 'Sent', href: '/mail?view=sent', icon: Send, isActive: (p, _s, v) => p === '/mail' && v === 'sent' },
  { label: 'Drafts', href: '/mail?view=drafts', icon: FileText, isActive: (p, _s, v) => p === '/mail' && v === 'drafts' },
];

function RailLink({ item }: { item: RailItem }) {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const active = item.isActive(pathname, searchParams.get('split'), searchParams.get('view'));
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      className={cn(
        'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors',
        active
          ? 'bg-accent text-accent-foreground font-medium'
          : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
      )}
    >
      <Icon className="size-4 shrink-0" />
      {item.label}
    </Link>
  );
}

function SideRail({ user }: { user: SessionUser | null }) {
  const { openCompose } = useCompose();
  const pathname = usePathname();

  return (
    <aside className="bg-background flex w-60 shrink-0 flex-col border-r">
      {/* Brand */}
      <div className="flex items-center gap-2 px-4 pt-4 pb-2">
        <div className="bg-primary text-primary-foreground flex size-6 items-center justify-center rounded-md">
          <CalendarRange className="size-3.5" />
        </div>
        <span className="text-sm font-semibold tracking-tight">Calendium</span>
      </div>

      {/* Compose */}
      <div className="px-3 pt-2 pb-3">
        <Button size="sm" className="w-full justify-between" onClick={() => openCompose()}>
          <span className="flex items-center gap-2">
            <PenLine />
            Compose
          </span>
          <Kbd size="sm" className="border-primary-foreground/30 bg-transparent text-inherit">
            C
          </Kbd>
        </Button>
      </div>

      {/* Nav */}
      <nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-3">
        <p className="text-muted-foreground px-2.5 pt-1 pb-1 text-[0.6875rem] font-medium tracking-wider uppercase">
          Inbox
        </p>
        {SPLIT_ITEMS.map((item) => (
          <RailLink key={item.label} item={item} />
        ))}
        <p className="text-muted-foreground px-2.5 pt-4 pb-1 text-[0.6875rem] font-medium tracking-wider uppercase">
          Mail
        </p>
        {VIEW_ITEMS.map((item) => (
          <RailLink key={item.label} item={item} />
        ))}

        <p className="text-muted-foreground px-2.5 pt-4 pb-1 text-[0.6875rem] font-medium tracking-wider uppercase">
          App
        </p>
        <Link
          href="/calendar"
          className={cn(
            'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors',
            pathname.startsWith('/calendar')
              ? 'bg-accent text-accent-foreground font-medium'
              : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
          )}
        >
          <CalendarDays className="size-4" />
          Calendar
        </Link>
        <Link
          href="/settings"
          className={cn(
            'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors',
            pathname.startsWith('/settings')
              ? 'bg-accent text-accent-foreground font-medium'
              : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'
          )}
        >
          <Settings className="size-4" />
          Settings
        </Link>
      </nav>

      {/* Account switcher (stub) */}
      <AccountSwitcher user={user} />
    </aside>
  );
}

function AccountSwitcher({ user }: { user: SessionUser | null }) {
  const router = useRouter();
  const { theme, setTheme } = useTheme();
  const email = user?.email ?? 'you@calendium.app';
  const name = user?.name || email;
  const avatarUrl = user?.image ?? undefined;

  return (
    <div className="border-t p-2">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="hover:bg-accent/60 flex w-full items-center gap-2.5 rounded-md p-2 text-left transition-colors"
          >
            <Avatar className="size-7">
              {avatarUrl ? <AvatarImage src={avatarUrl} alt={name} /> : null}
              <AvatarFallback className="text-xs">
                {email.slice(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{name}</span>
              <span className="text-muted-foreground block truncate text-xs">{email}</span>
            </span>
            <ChevronsUpDown className="text-muted-foreground size-4 shrink-0" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="w-60">
          <DropdownMenuLabel className="text-muted-foreground text-xs">Accounts</DropdownMenuLabel>
          <DropdownMenuItem className="gap-2">
            <Check className="size-4" />
            <span className="truncate">{email}</span>
          </DropdownMenuItem>
          <DropdownMenuItem disabled className="gap-2">
            <Plus className="size-4" />
            Add account
            <Badge variant="secondary" className="ml-auto">
              Soon
            </Badge>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuSub>
            <DropdownMenuSubTrigger className="gap-2">
              {theme === 'dark' ? (
                <Moon className="size-4" />
              ) : theme === 'light' ? (
                <Sun className="size-4" />
              ) : (
                <Monitor className="size-4" />
              )}
              Theme
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuRadioGroup
                value={theme}
                onValueChange={(value) => setTheme(value as 'light' | 'dark' | 'system')}
              >
                <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="system">System</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="gap-2"
            onSelect={async () => {
              await signOut();
              router.replace('/signin');
            }}
          >
            <LogOut className="size-4" />
            Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
