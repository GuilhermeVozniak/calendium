/**
 * Menu-bar tray feed + auto-join settings (M2.6 Tasks 10-11).
 *
 * The FRONTEND owns event data: it selects the next upcoming events from the
 * events API (shared `detectConference` extracts the join URL — no
 * reimplementation) and pushes them to the Go host via the bound
 * `SetUpcomingEvents` method on every refetch plus a 60s interval. The host
 * renders the tray menu/countdown and drives auto-join timers from that feed.
 */
import type { Event } from '@calendium/shared';
import { detectConference } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';

import { api, orMock } from '@/lib/api';
import { mockEvents } from '@/lib/mock';
import { desktop, isDesktop } from '@/lib/wails';

/** Host event: a tray menu row was clicked (payload "open-calendar" | "compose"). */
export const TRAY_ACTION_EVENT = 'tray-action';
/** Host event: auto-join opened a meeting (payload: the joined tray event). */
export const AUTO_JOINED_EVENT = 'auto-joined';

/** Mirrors Go's TrayEvent (apps/desktop/traystate.go). */
export interface TrayEventPayload {
  id: string;
  title: string;
  startAt: string;
  joinUrl: string;
}

export const TRAY_LOOKAHEAD_HOURS = 12;
export const TRAY_MAX_EVENTS = 5;
const HOUR_MS = 3_600_000;

/**
 * Selects the tray feed: events in the next 12h that haven't ended (in-progress
 * ones stay — the host shows "<title> now"), excluding all-day and cancelled
 * events, sorted by start, capped at 5, with the conferencing URL extracted
 * from the structured field or sniffed from location/description (shared
 * detectConference). `joinUrl` is empty when the event has no call link.
 */
export function selectTrayEvents(events: Event[], now: Date): TrayEventPayload[] {
  const horizon = now.getTime() + TRAY_LOOKAHEAD_HOURS * HOUR_MS;
  return events
    .filter((ev) => !ev.allDay && ev.status !== 'cancelled')
    .filter((ev) => {
      const start = new Date(ev.start).getTime();
      const end = new Date(ev.end).getTime();
      return end > now.getTime() && start <= horizon;
    })
    .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime())
    .slice(0, TRAY_MAX_EVENTS)
    .map((ev) => ({
      id: ev.id,
      title: ev.title,
      startAt: new Date(ev.start).toISOString(),
      joinUrl: detectConference(ev)?.url ?? '',
    }));
}

/**
 * Feeds the host tray: fetches the next 12h of events (refetching every 60s)
 * and pushes the selected feed via SetUpcomingEvents on every data change plus
 * a 60s re-push so host-side filtering stays fresh. No-ops in a plain browser.
 */
export function useTrayFeed(): void {
  const { data } = useQuery({
    queryKey: ['tray-events'],
    enabled: isDesktop,
    refetchInterval: 60_000,
    queryFn: () => {
      const now = new Date();
      const to = new Date(now.getTime() + TRAY_LOOKAHEAD_HOURS * HOUR_MS);
      return orMock(
        () => api.listEvents(now.toISOString(), to.toISOString()),
        () => mockEvents(now.toISOString(), to.toISOString())
      );
    },
  });

  useEffect(() => {
    if (!isDesktop || !data) return;
    const push = () => {
      void desktop
        .SetUpcomingEvents(JSON.stringify(selectTrayEvents(data, new Date())))
        .catch((e) => console.error('tray feed push failed', e));
    };
    push();
    const timer = setInterval(push, 60_000);
    return () => clearInterval(timer);
  }, [data]);
}

// ---------------------------------------------------------------------------
// Auto-join settings (Task 11) — persisted in localStorage, pushed to the Go
// host via the SetAutoJoin bound method on startup and on every change.
// ---------------------------------------------------------------------------

export type AutoJoinLeadSeconds = 0 | 30 | 60;

export interface AutoJoinSettings {
  enabled: boolean;
  leadSeconds: AutoJoinLeadSeconds;
}

export const AUTO_JOIN_STORAGE_KEY = 'calendium.auto-join';

const DEFAULT_AUTO_JOIN: AutoJoinSettings = { enabled: false, leadSeconds: 0 };

/** Loads the persisted setting; anything malformed falls back to disabled. */
export function loadAutoJoinSettings(): AutoJoinSettings {
  try {
    const raw = localStorage.getItem(AUTO_JOIN_STORAGE_KEY);
    if (!raw) return { ...DEFAULT_AUTO_JOIN };
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === 'object' &&
      parsed !== null &&
      typeof (parsed as AutoJoinSettings).enabled === 'boolean' &&
      [0, 30, 60].includes((parsed as AutoJoinSettings).leadSeconds)
    ) {
      const { enabled, leadSeconds } = parsed as AutoJoinSettings;
      return { enabled, leadSeconds };
    }
  } catch {
    // Malformed JSON — fall through to the default.
  }
  return { ...DEFAULT_AUTO_JOIN };
}

/** Persists the setting and pushes it to the host (real setting only — the
 * host never auto-joins unless this says so). */
export function saveAutoJoinSettings(settings: AutoJoinSettings): void {
  localStorage.setItem(AUTO_JOIN_STORAGE_KEY, JSON.stringify(settings));
  pushAutoJoinSettings(settings);
}

/** Pushes the given (or persisted) setting to the Go host. Browser no-op. */
export function pushAutoJoinSettings(settings?: AutoJoinSettings): void {
  if (!isDesktop) return;
  const s = settings ?? loadAutoJoinSettings();
  void desktop
    .SetAutoJoin(s.enabled, s.leadSeconds)
    .catch((e) => console.error('auto-join push failed', e));
}
