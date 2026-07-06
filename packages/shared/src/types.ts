// Calendium shared domain types.
// These mirror the Go domain model in backend/internal/domain and the REST API
// contract documented in docs/architecture.md. Keep the three in sync.

export type Provider = 'google' | 'microsoft';

export interface User {
  id: string;
  email: string;
  name: string | null;
  avatarUrl: string | null;
  createdAt: string;
}

export type SubscriptionStatus =
  | 'trialing'
  | 'active'
  | 'past_due'
  | 'canceled'
  | 'expired'
  | 'none';

export interface Subscription {
  status: SubscriptionStatus;
  plan: 'annual';
  priceUsd: 50;
  currentPeriodEnd: string | null;
  cancelAtPeriodEnd: boolean;
  trialEndsAt: string | null;
}

export type AccountStatus = 'active' | 'syncing' | 'reauth_required' | 'disconnected';

/** A connected Google / Microsoft account providing both mail and calendar. */
export interface ConnectedAccount {
  id: string;
  provider: Provider;
  email: string;
  status: AccountStatus;
  scopes: string[];
  /** Sender addresses classified into the 'vip' split at ingest. */
  vipSenders: string[];
  lastSyncedAt: string | null;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Mail
// ---------------------------------------------------------------------------

/** Superhuman-style split-inbox categories. */
export type InboxSplit = 'important' | 'vip' | 'team' | 'calendar' | 'news' | 'social' | 'other';

export interface EmailAddress {
  name: string | null;
  email: string;
}

export interface Label {
  id: string;
  accountId: string;
  name: string;
  kind: 'system' | 'user';
  color: string | null;
}

export interface Thread {
  id: string;
  accountId: string;
  subject: string;
  snippet: string;
  participants: EmailAddress[];
  labelIds: string[];
  split: InboxSplit;
  messageCount: number;
  unread: boolean;
  starred: boolean;
  lastMessageAt: string;
  snoozedUntil: string | null;
  /** Follow-up reminder: resurface if nobody replies by this time. */
  remindAt: string | null;
}

export interface Attachment {
  id: string;
  filename: string;
  mimeType: string;
  sizeBytes: number;
}

export interface Message {
  id: string;
  threadId: string;
  accountId: string;
  from: EmailAddress;
  to: EmailAddress[];
  cc: EmailAddress[];
  bcc: EmailAddress[];
  subject: string;
  bodyHtml: string;
  bodyText: string;
  attachments: Attachment[];
  sentAt: string;
  isDraft: boolean;
  /** Read-status tracking (Superhuman read receipts). */
  openedAt: string | null;
}

export interface Draft {
  id: string;
  accountId: string;
  threadId: string | null;
  to: EmailAddress[];
  cc: EmailAddress[];
  bcc: EmailAddress[];
  subject: string;
  bodyHtml: string;
  /** When set, the backend sends the message at this time (Send Later). */
  scheduledAt: string | null;
  /** Worker delivery attempts; the draft is dead-lettered after the retry cap. */
  sendAttempts: number;
  /** Most recent delivery failure, when a scheduled send has failed. */
  lastError: string | null;
  updatedAt: string;
}

/** Reusable canned response with an optional keyboard shortcut (Superhuman snippets). */
export interface Snippet {
  id: string;
  name: string;
  shortcut: string | null;
  bodyHtml: string;
  usageCount: number;
}

export type ThreadAction =
  | 'archive'
  | 'trash'
  | 'star'
  | 'unstar'
  | 'read'
  | 'unread'
  | 'spam'
  | 'move_to_inbox';

// ---------------------------------------------------------------------------
// Calendar
// ---------------------------------------------------------------------------

export interface Calendar {
  id: string;
  accountId: string;
  name: string;
  color: string;
  timeZone: string;
  isPrimary: boolean;
  isVisible: boolean;
  canWrite: boolean;
}

export type RsvpStatus = 'accepted' | 'declined' | 'tentative' | 'needs_action';

export interface Attendee {
  email: string;
  name: string | null;
  response: RsvpStatus;
  organizer: boolean;
  optional: boolean;
}

export interface Conferencing {
  provider: 'meet' | 'zoom' | 'teams' | 'other';
  url: string;
}

export interface Event {
  id: string;
  calendarId: string;
  title: string;
  description: string | null;
  location: string | null;
  start: string;
  end: string;
  allDay: boolean;
  /** RFC 5545 RRULE, when recurring. */
  recurrenceRule: string | null;
  attendees: Attendee[];
  conferencing: Conferencing | null;
  status: 'confirmed' | 'tentative' | 'cancelled';
  visibility: 'default' | 'public' | 'private';
  reminderMinutes: number[];
}

export interface EventInput {
  calendarId: string;
  title: string;
  description?: string;
  location?: string;
  start: string;
  end: string;
  allDay?: boolean;
  recurrenceRule?: string;
  attendeeEmails?: string[];
  addConferencing?: boolean;
  reminderMinutes?: number[];
}

/**
 * Partial event update — mirrors EventPatch in backend/internal/domain/calendar.go.
 * The backend cannot move an event between calendars or toggle conferencing via
 * PATCH, so (unlike EventInput) it has no `calendarId` or `addConferencing`.
 */
export interface EventPatch {
  title?: string;
  description?: string;
  location?: string;
  start?: string;
  end?: string;
  allDay?: boolean;
  recurrenceRule?: string;
  attendeeEmails?: string[];
  reminderMinutes?: number[];
}

/** Shareable availability (Superhuman/Vimcal "share availability" flow). */
export interface AvailabilitySlot {
  start: string;
  end: string;
}

// ---------------------------------------------------------------------------
// Notifications & devices
// ---------------------------------------------------------------------------

export type DevicePlatform = 'ios' | 'android' | 'web' | 'macos' | 'windows' | 'linux';

export interface NotificationDevice {
  id: string;
  platform: DevicePlatform;
  /** APNs device token, FCM registration token, or Web Push subscription JSON. */
  token: string;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// AI (OpenRouter-backed)
// ---------------------------------------------------------------------------

export type AiAction = 'compose' | 'reply' | 'summarize' | 'ask';

export interface AiComposeRequest {
  action: AiAction;
  /** Free-form instruction, e.g. "polite decline, propose next week". */
  prompt: string;
  threadId?: string;
  draftId?: string;
}

export interface AiComposeResponse {
  text: string;
  model: string;
}

// ---------------------------------------------------------------------------
// API envelopes
// ---------------------------------------------------------------------------

export interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
}

// ---------------------------------------------------------------------------
// Instance discovery (open-core self-host vs cloud)
// ---------------------------------------------------------------------------

/** How a Calendium server is run: a self-hosted instance or Calendium Cloud. */
export type InstanceMode = 'self_host' | 'cloud';

/** Capabilities a server advertises so clients can adapt their UI. */
export interface InstanceFeatures {
  /** Stripe billing is available (false on self-hosted instances). */
  billing: boolean;
  google: boolean;
  microsoft: boolean;
  ai: boolean;
  push: boolean;
}

/**
 * Public, unauthenticated instance descriptor served at GET /v1/instance.
 * A client that only knows the server URL fetches this to self-configure:
 * it learns the Supabase project to authenticate against and which features
 * are enabled. `supabaseUrl`/`supabaseAnonKey` may be "" when not configured.
 */
export interface InstanceInfo {
  name: string;
  mode: InstanceMode;
  version: string;
  supabaseUrl: string;
  supabaseAnonKey: string;
  features: InstanceFeatures;
}
