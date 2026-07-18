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
  /** Set the first time the owner opens the thread (POST .../open); real read state. */
  openedAt: string | null;
  snoozedUntil: string | null;
  /** Follow-up reminder: resurface if nobody replies by this time. */
  remindAt: string | null;
  /** Parsed List-Unsubscribe targets from the newest message (RFC 2369/8058). */
  unsubscribeMailto: string | null;
  unsubscribeUrl: string | null;
  unsubscribeOneClick: boolean;
  /** AI-generated thread summary; absent until generated. */
  summary?: string;
  /** Cache of AI-generated reply suggestions. */
  instantReplies?: string[];
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
  /** Marks this draft as AI-generated (e.g., auto-reply, auto-draft). */
  aiGenerated: boolean;
}

/**
 * Full create/update draft payload — mirrors port.DraftInput in
 * backend/internal/port/driving.go field-for-field. PUT /v1/mail/drafts/{id}
 * is a FULL replace: every field is written, so callers must send the whole
 * draft (omitted fields blank the stored value). Clearing scheduledAt before
 * the grace elapses is undo-send.
 */
export interface DraftInput {
  accountId: string;
  threadId: string | null;
  to: EmailAddress[];
  cc: EmailAddress[];
  bcc: EmailAddress[];
  subject: string;
  bodyHtml: string;
  scheduledAt: string | null;
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

export type BulkAction = ThreadAction | 'label' | 'unlabel';

export interface BulkActionResult {
  threads: Thread[];
  failedIds: string[];
}

export type UnsubscribeMethod = 'one_click' | 'mailto' | 'link';

/** "one_click"/"mailto" completed server-side; "link" = open url in a browser. */
export interface UnsubscribeResult {
  method: UnsubscribeMethod;
  url?: string;
}

/** Per-user layout preferences, shared across devices (GET/PUT /v1/prefs). */
export interface UserPrefs {
  splitOrder: InboxSplit[];
}

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

/** Saved event default set ("1:1", "Focus block") applied at creation time. */
export interface EventTemplate {
  id: string;
  name: string;
  title: string;
  description: string;
  location: string;
  durationMinutes: number;
  allDay: boolean;
  calendarId: string | null;
  attendeeEmails: string[];
  addConferencing: boolean;
  reminderMinutes: number[];
  recurrenceRule: string | null;
  usageCount: number;
}

/** Create/update event template payload (full replace on update). */
export interface EventTemplateInput {
  name: string;
  title: string;
  description?: string;
  location?: string;
  durationMinutes: number;
  allDay?: boolean;
  calendarId?: string | null;
  attendeeEmails?: string[];
  addConferencing?: boolean;
  reminderMinutes?: number[];
  recurrenceRule?: string | null;
}

/** Named group of calendars toggled together ("Work", "Home"). */
export interface CalendarSet {
  id: string;
  name: string;
  calendarIds: string[];
  position: number;
}

/** Create/update calendar set payload (full replace on update). */
export interface CalendarSetInput {
  name: string;
  calendarIds: string[];
  position: number;
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

export type AiAction =
  | 'compose'
  | 'reply'
  | 'summarize'
  | 'ask'
  | 'improve'
  | 'shorten'
  | 'simplify'
  | 'fix_grammar'
  | 'change_tone';

/** The subset of AiAction driven by ApiClient#aiEditDraft (edit an existing draft in place). */
export type AiEditAction = 'improve' | 'shorten' | 'simplify' | 'fix_grammar' | 'change_tone';

export interface AiComposeRequest {
  action: AiAction;
  /** Free-form instruction, e.g. "polite decline, propose next week". */
  prompt: string;
  threadId?: string;
  draftId?: string;
  /** Target tone; only meaningful when action is 'change_tone'. */
  tone?: string;
}

export interface AiComposeResponse {
  text: string;
  model: string;
}

/** POST /v1/ai/ask — cited question answering over the mailbox (or one thread). */
export interface AiAskRequest {
  question: string;
  /** Scope to one thread; omitted = whole mailbox. */
  threadId?: string;
}

/** One citation backing an AiAskResponse answer. */
export interface AiSource {
  threadId: string;
  messageId?: string;
  subject: string;
  snippet: string;
}

export interface AiAskResponse {
  answer: string;
  model: string;
  sources: AiSource[];
}

/** Instant Event AI's proposed calendar event, derived from a thread. */
export interface AiEventProposal {
  title: string;
  attendees: string[];
  start: string;
  end: string;
  location?: string;
  notes?: string;
}

/**
 * A user-defined natural-language mail classifier applied at ingest — mirrors
 * domain.AiClassifier in backend/internal/domain/ai.go. When it matches, the
 * thread is routed to targetSplit (if set) and tagged with labelName (if set).
 */
export interface AiClassifier {
  id: string;
  name: string;
  prompt: string;
  targetSplit?: InboxSplit;
  labelName?: string;
  enabled: boolean;
}

/** Create/update classifier payload (full replace on update); mirrors port.ClassifierInput. */
export interface ClassifierInput {
  name: string;
  prompt: string;
  targetSplit?: InboxSplit;
  labelName?: string;
  enabled: boolean;
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
 * it learns the Better Auth base URL to authenticate against and which
 * features are enabled. Clients build their Better Auth client against
 * `authBaseUrl` (e.g. `${authBaseUrl}/jwks`, sign-in endpoints, etc.).
 */
export interface InstanceInfo {
  name: string;
  mode: InstanceMode;
  version: string;
  /** Better Auth base URL, e.g. https://app.calendium.com/api/auth. */
  authBaseUrl: string;
  /** Enabled sign-in methods, e.g. ["email", "google", "apple"]. */
  authProviders: string[];
  /**
   * Undo-send grace window in seconds (UNDO_SEND_SECONDS, default 15): show a
   * post-send Undo affordance for this long.
   */
  undoSendSeconds: number;
  /** Web Push application server key; present only when web push is configured. */
  vapidPublicKey?: string;
  features: InstanceFeatures;
}
