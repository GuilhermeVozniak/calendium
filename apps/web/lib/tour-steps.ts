import { MOD_KEY } from '@/lib/shortcuts';

/**
 * Declarative step list for the concierge tour. `target` names a
 * `data-tour="..."` anchor rendered by the app shell/pages; the engine
 * (components/app/onboarding-tour.tsx) skips any step whose anchor is missing
 * or hidden, so steps can safely point at responsive or page-scoped UI.
 */
export interface TourStep {
  id: string;
  /** `data-tour` attribute value the popover anchors to. */
  target: string;
  title: string;
  body: string;
  /** Page the anchor lives on; the tour navigates there when needed. */
  page: 'mail' | 'calendar' | 'any';
  /** Display-ready shortcut hint, e.g. "H" or "⇧T". */
  shortcut?: string;
}

export const tourSteps: TourStep[] = [
  {
    id: 'split-inbox',
    target: 'split-inbox',
    title: 'Your inbox, split',
    body: 'Calendium sorts mail into Important, VIP, Team, News and more — each split is its own focused queue.',
    page: 'mail',
  },
  {
    id: 'triage',
    target: 'triage',
    title: 'Triage at speed',
    body: 'Move through threads with J and K, archive with E. Inbox zero is a few keystrokes away.',
    page: 'mail',
    shortcut: 'J K E',
  },
  {
    id: 'command-palette',
    target: 'command-palette',
    title: 'Every action, one keystroke',
    body: 'The command palette runs everything — search, mail, calendar, AI — without touching the mouse.',
    page: 'mail',
    shortcut: `${MOD_KEY} K`,
  },
  {
    id: 'snooze',
    target: 'snooze',
    title: 'Snooze for later',
    body: 'Press H to snooze a thread; it comes back to the top of your inbox exactly when you want it.',
    page: 'mail',
    shortcut: 'H',
  },
  {
    id: 'cal-today',
    target: 'cal-today',
    title: 'Your calendar',
    body: 'Jump back to today with T. D, W, M, Q and Y switch between day, week, month, quarter and year views.',
    page: 'calendar',
    shortcut: 'T',
  },
  {
    id: 'task-rail',
    target: 'task-rail',
    title: 'Tasks beside your week',
    body: 'Toggle the task rail with ⇧T and drag tasks onto the grid to give them a real time slot.',
    page: 'calendar',
    shortcut: '⇧T',
  },
  {
    id: 'share-availability',
    target: 'share-availability',
    title: 'Share availability',
    body: 'Press S to pick free slots straight off your calendar and paste them into any email.',
    page: 'calendar',
    shortcut: 'S',
  },
  {
    id: 'settings',
    target: 'settings',
    title: 'Make it yours',
    body: 'Connect Todoist, HubSpot, calendar subscriptions and more under Settings → Integrations.',
    page: 'any',
  },
];
