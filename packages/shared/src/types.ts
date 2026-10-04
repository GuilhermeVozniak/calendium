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

/** Mirrors backend/internal/domain/subscription.go. Expiry is never a status: it is computed from time. */
export type SubscriptionStatus = 'trialing' | 'active' | 'past_due' | 'paused' | 'canceled' | 'none';

export interface Subscription {
  status: SubscriptionStatus;
  plan: 'annual';
  priceUsd: 50;
  currentPeriodEnd: string | null;
  cancelAtPeriodEnd: boolean;
  trialEndsAt: string | null;
}

/** `details.reason` of a 402 payment_required response (domain.DenialReason). */
export type PaymentRequiredReason = 'trial_ended' | 'past_due' | 'canceled' | 'paused' | 'none';

/** Structured `error.details` of a 402 response. */
export interface PaymentRequiredDetails {
  reason: PaymentRequiredReason;
  trialEndsAt?: string;
  currentPeriodEnd?: string;
}

/**
 * Temporary Paddle customer-portal links (POST /v1/billing/portal). Never
 * cache them. cancelUrl/updatePaymentUrl are empty strings when the user has
 * no provider subscription yet.
 */
export interface BillingPortalUrls {
  overviewUrl: string;
  cancelUrl: string;
  updatePaymentUrl: string;
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
  /** Rich HTML signature appended at send (M2.5). */
  signatureHtml: string;
  /** Addresses auto-BCC'd on every send from this account (M2.5). */
  autoBcc: string[];
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
  /** Emoji reactions on this message (M2.5). */
  reactions: Reaction[];
}

/** A lightweight emoji reaction on a message (M2.5). */
export interface Reaction {
  id: string;
  messageId: string;
  emoji: string;
  /** "local" = stored only; "sent" = also delivered as a tiny threaded reply. */
  delivery: 'local' | 'sent';
  createdAt: string;
}

/** A stored reaction plus the tiny-reply draft id when it was also queued for delivery. */
export interface ReactionResult {
  reaction: Reaction;
  draftId: string | null;
}

/**
 * One teammate's read/reply state on a shared conversation (M2.7 team read
 * statuses). Served only for members who opted in via shareReadStatuses;
 * conversations are correlated across accounts by the RFC 5322 Message-ID of
 * the thread's earliest message.
 */
export interface TeamThreadActivity {
  teamId: string;
  userId: string;
  conversationKey: string;
  openedAt: string | null;
  repliedAt: string | null;
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
  /** Team scope (M2.7 team snippets): set = shared with that team's members; null/absent = personal. */
  teamId?: string | null;
  /**
   * The snippet's author (F2). Personal snippets only reach their owner and
   * team snippets only reach that team's members, so this never crosses
   * team scope.
   */
  authorId?: string;
  /**
   * Whether the REQUESTING user may edit/delete this snippet (author, or
   * admin+ on team snippets). Computed server-side per request — exact
   * parity with the server's authorization; the server still enforces.
   */
  canDelete?: boolean;
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

/** Named UI theme (curated token palettes); 'neutral' is the default. */
export type ThemeName = 'neutral' | 'ocean' | 'forest' | 'sunset';

/** The allowed named-theme set, shared by pickers and client-side validation. */
export const THEME_NAMES: readonly ThemeName[] = ['neutral', 'ocean', 'forest', 'sunset'];

/**
 * Canonical `--primary` token per named theme (light/dark), verbatim from the
 * CSS token blocks (apps/web/app/globals.css / apps/desktop styles.css) and
 * apps/mobile/lib/theme.ts. Pickers use these for swatches so previews derive
 * from the real token values.
 */
export const THEME_SWATCHES: Record<ThemeName, { light: string; dark: string }> = {
  neutral: { light: 'hsl(0 0% 9%)', dark: 'hsl(0 0% 98%)' },
  ocean: { light: 'hsl(217 72% 46%)', dark: 'hsl(213 80% 66%)' },
  forest: { light: 'hsl(158 55% 34%)', dark: 'hsl(152 45% 60%)' },
  sunset: { light: 'hsl(24 82% 48%)', dark: 'hsl(27 90% 62%)' },
};

/** Per-user cross-device preferences (GET/PUT /v1/me/preferences). */
export interface UserPreferences {
  theme: ThemeName;
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
// Scheduling (M2.4) — mirrors backend/internal/domain/scheduling.go and
// backend/internal/port/driving.go field-for-field.
// ---------------------------------------------------------------------------

/** One weekly recurring open window, in the owning entity's time zone. */
export interface AvailabilityWindow {
  /** 0=Sunday … 6=Saturday */
  weekday: number;
  /** "09:00" (24h HH:MM) */
  start: string;
  /** "17:00", must be > start */
  end: string;
}

/** A personal Calendly-style scheduling page: /book/{slug} → live slots → visitor books. */
export interface BookingLink {
  id: string;
  slug: string;
  title: string;
  description: string | null;
  /** Target (writable) calendar. */
  calendarId: string;
  durationMinutes: number;
  /** IANA name; windows interpreted here. */
  timeZone: string;
  windows: AvailabilityWindow[];
  bufferBeforeMin: number;
  bufferAfterMin: number;
  /** 0 = unlimited confirmed bookings/day. */
  dailyLimit: number;
  minNoticeMin: number;
  /** 0 = default 60. */
  maxAdvanceDays: number;
  respectWorkingHours: boolean;
  addConferencing: boolean;
  active: boolean;
  createdAt: string;
}

/** Create/update booking-link payload; mirrors port.BookingLinkInput. */
export type BookingLinkInput = Omit<BookingLink, 'id' | 'createdAt' | 'description'> & {
  description?: string;
};

/** The slot-hold lifecycle: hold → confirmed | cancelled. */
export type BookingStatus = 'hold' | 'confirmed' | 'cancelled';

/** One visitor reservation against a BookingLink. */
export interface Booking {
  id: string;
  linkId: string;
  status: BookingStatus;
  start: string;
  end: string;
  inviteeName: string;
  inviteeEmail: string;
  inviteeTimeZone: string;
  note: string | null;
  /** Mirrored event once confirmed. */
  eventId: string | null;
  createdAt: string;
}

/** The public GET /v1/public/booking/{slug} document — no owner PII beyond display name. */
export interface PublicBookingPage {
  slug: string;
  title: string;
  description: string | null;
  ownerName: string;
  durationMinutes: number;
  /** Owner's link TZ (for "times shown in…" hints). */
  timeZone: string;
}

/** The public booking payload. */
export interface BookingRequest {
  start: string;
  inviteeName: string;
  inviteeEmail: string;
  inviteeTimeZone: string;
  note?: string;
}

/** The meeting-poll lifecycle. */
export type PollStatus = 'open' | 'confirmed' | 'cancelled';

/** One voter's answer for one option. */
export type PollVoteChoice = 'yes' | 'no' | 'if_needed';

/** One candidate slot on a poll. */
export interface PollOption {
  id: string;
  start: string;
  end: string;
}

/** Aggregated votes for one option. */
export interface PollTally {
  yes: number;
  no: number;
  ifNeeded: number;
}

/** Proposes candidate slots invitees vote on via a public link. */
export interface MeetingPoll {
  id: string;
  /** Unguessable public URL token (32 hex chars). */
  token: string;
  title: string;
  description: string | null;
  calendarId: string;
  durationMinutes: number;
  options: PollOption[];
  status: PollStatus;
  winnerOptionId: string | null;
  eventId: string | null;
  createdAt: string;
}

/** Create-poll payload; mirrors port.PollInput. */
export interface PollInput {
  title: string;
  description?: string;
  calendarId: string;
  durationMinutes: number;
  /** IDs assigned server-side. */
  options: Array<{ start: string; end: string }>;
}

/** The public poll document, incl. anonymized tallies. */
export interface PublicPoll {
  token: string;
  title: string;
  description: string | null;
  organizerName: string;
  durationMinutes: number;
  status: PollStatus;
  options: PollOption[];
  /** optionID → tally */
  tallies: Record<string, PollTally>;
  winnerOptionId: string | null;
}

/** One public voter's submission. */
export interface PollBallot {
  voterEmail: string;
  voterName: string;
  /** optionID → choice */
  choices: Record<string, PollVoteChoice>;
}

/** The propose-new-time lifecycle. */
export type ProposalStatus = 'pending' | 'accepted' | 'declined' | 'superseded';

/** An invitee's counter-proposed time for an existing event. */
export interface TimeProposal {
  id: string;
  eventId: string;
  proposerEmail: string;
  proposerName: string;
  start: string;
  end: string;
  note: string | null;
  status: ProposalStatus;
  createdAt: string;
}

/** Propose-new-time payload; mirrors port.TimeProposalInput. */
export interface TimeProposalInput {
  start: string;
  end: string;
  note?: string;
}

/** One busy span from a provider free/busy query. */
export interface BusyInterval {
  start: string;
  end: string;
}

/** Per-user scheduling preferences (working hours in timeZone, displayed location). */
export interface UserSettings {
  timeZone: string;
  workingHours: AvailabilityWindow[];
  workingLocation: string;
  /**
   * Settings → AI → "Background AI processing". Off skips every automatic
   * AI job at sync (summaries, quick replies, auto drafts, classifiers,
   * writing-style profile, reminder detection); on-demand actions are
   * unaffected. Default true. Omitting it on PUT keeps the stored value.
   */
  aiBackground: boolean;
}

// ---------------------------------------------------------------------------
// M2.5 — Recent Opens, Smart Send, attachment quick-access, contact summary
// (mirrors backend/internal/domain/mail.go field-for-field).
// ---------------------------------------------------------------------------

/** One row of the Recent Opens feed: a sent message a recipient has opened, newest first. */
export interface OpenEvent {
  messageId: string;
  threadId: string;
  accountId: string;
  subject: string;
  recipients: EmailAddress[];
  openedAt: string;
  sentAt: string;
}

/** An attachment search result with message context. */
export interface AttachmentHit extends Attachment {
  messageId: string;
  threadId: string;
  threadSubject: string;
  from: EmailAddress;
  sentAt: string;
}

/** The Smart Send recommendation for one recipient, inferred from historical open times. */
export interface SendSuggestion {
  email: string;
  suggestedAt: string;
  /** Inferred UTC offset in hours, [-12, 13]. */
  utcOffsetHours: number;
  /** 0..1 share of opens near the peak. */
  confidence: number;
  sampleSize: number;
}

/** Aggregates everything the local mirror knows about a sender. */
export interface ContactSummary {
  email: string;
  /** From the most recent message. */
  name: string | null;
  domain: string;
  threadCount: number;
  messageCount: number;
  lastMessageAt: string | null;
  /** Newest 5 threads. */
  recentThreads: Thread[];
}

// ---------------------------------------------------------------------------
// Teams & collaboration (M2.7)
// ---------------------------------------------------------------------------

/** Member privileges, ordered owner > admin > member. */
export type TeamRole = 'owner' | 'admin' | 'member';

/**
 * A collaboration group. Membership grants NOTHING by itself: every
 * collaborative surface (shares, comments, read statuses, calendars,
 * availability) requires its own explicit opt-in (privacy default).
 */
export interface Team {
  id: string;
  name: string;
  createdBy: string;
  createdAt: string;
}

/** A user's membership in a team. */
export interface TeamMember {
  teamId: string;
  userId: string;
  role: TeamRole;
  /**
   * Opts this member's thread open/reply activity into the team's
   * read-status indicators. Privacy default: false.
   */
  shareReadStatuses: boolean;
  joinedAt: string;
  /**
   * Display-identity enrichment (F2), resolved server-side ONLY on member
   * rosters the caller can already see by membership. Empty when
   * unresolvable — fall back to the id; nothing is fabricated.
   */
  name?: string;
  email?: string;
}

/** Lifecycle of an email invitation. */
export type TeamInvitationStatus = 'pending' | 'accepted' | 'revoked' | 'expired';

/** How a team invitation went out (response-only, mirrors domain.InvitationDelivery). */
export type TeamInvitationDelivery = 'mailbox' | 'smtp' | 'link';

/**
 * An email invitation to join a team. The raw token appears once, inside the
 * emailed invite link; the API never returns it.
 */
export interface TeamInvitation {
  id: string;
  teamId: string;
  email: string;
  role: TeamRole;
  invitedBy: string;
  status: TeamInvitationStatus;
  expiresAt: string;
  createdAt: string;
  /**
   * Response-only: the inviter's connected mailbox, the instance SMTP sender,
   * or a link to share by hand (self-host without either).
   */
  delivery?: TeamInvitationDelivery;
  /** Present only when delivery === 'link': the accept URL the inviter must share. */
  inviteUrl?: string;
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

// --- Integrations (M2.8 Task 9) — mirrors backend/internal/domain/integration.go ---

/** External per-user integration vendors (todo/CRM tools). */
export type IntegrationVendor = 'todoist' | 'hubspot';

/**
 * A per-user vendor OAuth grant (one per vendor). OAuth tokens are stored
 * encrypted server-side and never serialized — this shape carries no token
 * and no owner id.
 */
export interface IntegrationConnection {
  id: string;
  vendor: IntegrationVendor;
  /** Vendor login / portal label; may be empty when the vendor doesn't reveal one. */
  externalAccount: string;
  status: 'active' | 'error';
  lastError: string | null;
  createdAt: string;
}

/**
 * Optional vendor integrations configured on a server (M2.8): only vendors
 * whose config is present are advertised; unwired vendors' endpoints 501.
 */
export interface InstanceCapabilities {
  todoist: boolean;
  hubspot: boolean;
  maps: boolean;
  weather: boolean;
}

/** Capabilities a server advertises so clients can adapt their UI. */
export interface InstanceFeatures {
  /** Paddle billing is wired (true exactly in cloud mode; false on self-hosted instances). */
  billing: boolean;
  google: boolean;
  microsoft: boolean;
  ai: boolean;
  push: boolean;
  /**
   * An SMTP sender is configured (piece 2). Current servers always send it;
   * typed optional (like `maps`) so pre-piece-2 servers and fixtures stay
   * valid. Only an explicit `false` switches the web forgot-password page
   * to the administrator instructions.
   */
  email?: boolean;
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
   * Public web app origin (PUBLIC_WEB_URL, no trailing slash). Desktop builds
   * its billing link from it (`${webUrl}/settings?tab=billing`); the mobile
   * apps show no billing links (store rules).
   */
  webUrl: string;
  /**
   * Undo-send grace window in seconds (UNDO_SEND_SECONDS, default 15): show a
   * post-send Undo affordance for this long.
   */
  undoSendSeconds: number;
  /** Web Push application server key; present only when web push is configured. */
  vapidPublicKey?: string;
  features: InstanceFeatures;
  /**
   * Vendor integration flags (M2.8). Current servers always send it; typed
   * optional so pre-M2.8 servers and fixtures stay valid.
   */
  capabilities?: InstanceCapabilities;
}

// ---------------------------------------------------------------------------
// Shared conversations & team comments (M2.7) — mirrors
// backend/internal/domain/threadshare.go, backend/internal/domain/collab.go
// and port.ShareThreadInput / port.SharedThreadView / port.CommentInput.
// ---------------------------------------------------------------------------

/** Who may open a thread-share link: team members only, or anyone holding it. */
export type ShareAudience = 'team' | 'external';

/**
 * A tokenized live link to a mail thread. The raw token appears exactly once,
 * in the create response (ThreadShareCreated.token); the API never returns it
 * again — only its hash is stored server-side.
 */
export interface ThreadShare {
  id: string;
  threadId: string;
  createdBy: string;
  audience: ShareAudience;
  /** Scopes a team-audience share; null for external shares. */
  teamId: string | null;
  revokedAt: string | null;
  expiresAt: string | null;
  createdAt: string;
}

/** Create-share payload; mirrors port.ShareThreadInput. */
export interface ShareThreadInput {
  audience: ShareAudience;
  /** Required when audience is 'team'. */
  teamId?: string;
  /** Optional link expiry (RFC 3339); omitted/null = until revoked. */
  expiresAt?: string | null;
}

/**
 * POST /v1/mail/threads/{id}/share response: the stored share plus the raw
 * link token. SECRET: the token appears here exactly once — surface it to the
 * user immediately and never log it or send it to analytics.
 */
export interface ThreadShareCreated {
  share: ThreadShare;
  token: string;
}

/**
 * The read-only projection served to share viewers (GET
 * /v1/shared/threads/{token}): thread metadata + messages with recipients'
 * Bcc stripped and no labels/split/snooze state. Unknown, revoked, and
 * expired tokens are uniformly 404 (no oracle).
 */
export interface SharedThreadView {
  subject: string;
  audience: ShareAudience;
  messages: Message[];
  updatedAt: string;
}

/** A team comment on a mail thread; bodies are plain user text (escaped render-side). */
export interface Comment {
  id: string;
  threadId: string;
  teamId: string;
  authorId: string;
  body: string;
  /** Team-member user IDs resolved from @email tokens at write time. */
  mentions: string[];
  createdAt: string;
  updatedAt: string;
  /**
   * Author display identity (name, else email) resolved server-side within
   * team scope (F2). Empty when unresolvable — fall back to the id.
   */
  authorName?: string;
}

/** Add-comment payload; mirrors port.CommentInput. */
export interface CommentInput {
  teamId: string;
  body: string;
}

// ---------------------------------------------------------------------------
// EA delegation (M2.7 Task 15) — mirrors backend/internal/domain/delegation.go
// and audit.go field-for-field.
// ---------------------------------------------------------------------------

/**
 * One unit of access an assistant may exercise on behalf of a principal.
 * Scopes are explicit and enumerated — no wildcard, no implied scope.
 */
export type DelegationScope = 'mail_read' | 'mail_write' | 'calendar_read' | 'calendar_write';

/** Every delegation scope, for grant-dialog pickers. */
export const DELEGATION_SCOPES: readonly DelegationScope[] = [
  'mail_read',
  'mail_write',
  'calendar_read',
  'calendar_write',
];

/**
 * Grant lifecycle: a grant authorizes nothing until the assistant accepts it
 * (pending → active) and nothing again once either party revokes it (terminal).
 */
export type DelegationStatus = 'pending' | 'active' | 'revoked';

/** An explicit principal→assistant grant, limited to `scopes`. */
export interface Delegation {
  id: string;
  principalId: string;
  assistantId: string;
  scopes: DelegationScope[];
  status: DelegationStatus;
  createdAt: string;
  acceptedAt: string | null;
  revokedAt: string | null;
  /**
   * Display-identity enrichment (F2): both parties already share the
   * grant, so no new information leaks. Empty when unresolvable — fall
   * back to the id; nothing is fabricated.
   */
  principalName?: string;
  principalEmail?: string;
  assistantName?: string;
  assistantEmail?: string;
}

/** GET /v1/delegations — the caller's grants, split by side. */
export interface DelegationList {
  /** Grants the caller gave (they are the principal). */
  asPrincipal: Delegation[];
  /** Grants the caller received (they are the assistant). */
  asAssistant: Delegation[];
}

/**
 * One append-only audit-log row: a mutation one user performed on another
 * user's resources (every delegated mutation is recorded).
 */
export interface AuditEntry {
  id: string;
  /** Who really acted — the assistant on delegated requests. */
  actorId: string;
  /** Whose account was acted upon. */
  principalId: string;
  /** e.g. "POST /v1/mail/threads/{id}/actions". */
  action: string;
  resourceType: string;
  resourceId: string;
  metadata: Record<string, unknown> | null;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Team booking links (M2.7 Task 14) — declaration merging augments the M2.4
// scheduling interfaces above (appended here so parallel tasks merge cleanly).
// ---------------------------------------------------------------------------

export interface BookingLink {
  /**
   * Team scope: set = collective team link whose slots intersect the
   * creator's and every listed member's availability, inviting all members
   * on each confirmed booking. Null/absent = personal link.
   */
  teamId?: string | null;
  /** Team member user IDs included in the collective intersection (creator implicit). */
  memberUserIds?: string[];
}

// ---------------------------------------------------------------------------
// Shared calendars & team availability (M2.7 Tasks 12–13) — mirrors
// backend/internal/domain/calendar_sharing.go and port/calendar_sharing.go.
// ---------------------------------------------------------------------------

/** Shared-calendar access levels, ordered free_busy < reader < editor. */
export type CalendarPermission = 'free_busy' | 'reader' | 'editor';

/**
 * A grant of one calendar to one user or one whole team — exactly one of
 * granteeUserId / granteeTeamId is set. Sharing is a local layer over the
 * mirrored calendars; provider-level ACLs are never touched.
 */
export interface CalendarShare {
  id: string;
  calendarId: string;
  granteeUserId?: string;
  granteeTeamId?: string;
  permission: CalendarPermission;
  createdBy: string;
  createdAt: string;
}

/**
 * POST /v1/calendars/{id}/shares payload. Exactly one grantee field must be
 * set; permission defaults server-side to 'free_busy' (the privacy-preserving
 * minimum) when omitted.
 */
export interface CalendarShareInput {
  granteeUserId?: string;
  granteeTeamId?: string;
  permission?: CalendarPermission;
}

/**
 * One member row of GET /v1/teams/{id}/availability. Busy blocks are opaque
 * start/end intervals — free_busy privacy: the server never sends titles or
 * details, and clients must not try to backfill them from other caches.
 */
export interface MemberAvailability {
  userId: string;
  busy: AvailabilitySlot[];
  /** False = the member has not opted in by sharing a calendar with the team. */
  shared: boolean;
  /**
   * Display identity from the team roster (F2) — team scope only. Empty
   * when unresolvable; fall back to the id.
   */
  name?: string;
  email?: string;
}

// Declaration-merged augmentations (add-only): TypeScript merges these into
// the Calendar / Event interfaces declared earlier in this file.
export interface Calendar {
  /** Set only on calendars shared TO the viewer: their effective permission. */
  sharedPermission?: CalendarPermission;
}
export interface Event {
  /** True when the event was redacted for a free_busy viewer (title "Busy", details zeroed). */
  freeBusyOnly?: boolean;
}

/**
 * Local-only doc/note attached to an event (M2.8 Task 4). Lives only in
 * Calendium (never written to the provider), so it survives provider syncs;
 * deleting the event deletes the note. bodyMd is markdown treated as plain
 * text by clients; links are absolute http(s) doc URLs (Notion, GDoc, ...).
 */
export interface EventNote {
  eventId: string;
  bodyMd: string;
  links: string[];
  updatedAt: string;
}

// ---------------------------------------------------------------------------
// M2.8 Task 5 — Calendar automation preferences
// (mirrors backend/internal/domain/prefs.go CalendarPrefs field-for-field).
// ---------------------------------------------------------------------------

/** Routing profile for travel buffers and leave-by alerts. */
export type TravelMode = 'driving' | 'walking' | 'transit';

/**
 * Per-user calendar automation preferences (GET/PATCH /v1/prefs/calendar):
 * FocusGuard, auto buffers, OOO auto-decline, travel buffers / leave alerts,
 * and weather. Defaults (UTC, Mon–Fri 09:00–17:00, everything off) are
 * served when the user never saved the document.
 */
export interface CalendarPrefs {
  timeZone: string;
  /** Working days, 0=Sunday … 6=Saturday. */
  workDays: number[];
  /** Minutes after midnight in timeZone; default 540 (09:00). */
  workdayStartMinutes: number;
  /** Minutes after midnight in timeZone; default 1020 (17:00). */
  workdayEndMinutes: number;
  /** Weekly focus-time goal in minutes; 0 = FocusGuard off. */
  focusGoalMinutesPerWeek: number;
  focusAutoDecline: boolean;
  focusDeclineMessage: string;
  /** Buffer added around meetings; 0 = off, otherwise 5..30. */
  autoBufferMinutes: number;
  oooAutoDecline: boolean;
  oooDeclineMessage: string;
  travelBuffers: boolean;
  travelMode: TravelMode;
  leaveAlerts: boolean;
  homeLat: number | null;
  homeLon: number | null;
  weatherEnabled: boolean;
}

/**
 * PATCH /v1/prefs/calendar payload — omitted fields are unchanged
 * (server-side nil-means-unchanged). Sending null for homeLat/homeLon is
 * treated as omitted, not as clearing.
 */
export type CalendarPrefsPatch = Partial<CalendarPrefs>;

// ---------------------------------------------------------------------------
// Tasks (M2.8) — mirrors backend/internal/domain/task.go field-for-field.
// ---------------------------------------------------------------------------

/** Where a task originates: created in Calendium, or mirrored from a provider todo. */
export type TaskSource = 'local' | 'todoist';

/**
 * A first-class todo. `due` carries deadline semantics; the scheduled pair
 * carries timeblock semantics — when both are set the task renders on the
 * calendar grid between scheduledStart and scheduledEnd and in the rail's
 * due grouping. External tasks mirror a provider todo and write completion
 * through to the provider server-side.
 */
export interface Task {
  id: string;
  title: string;
  notes: string | null;
  /** RFC 3339, or null when the task has no deadline. */
  due: string | null;
  /** Date-only due: render in the all-day lane, no hour. */
  allDayDue: boolean;
  scheduledStart: string | null;
  scheduledEnd: string | null;
  /** Non-null exactly when the task is checked off. */
  completedAt: string | null;
  source: TaskSource;
  /** Deep link into the source app for mirrored tasks. */
  sourceUrl: string | null;
  /** Rail sort key: lower first, fractional so drag-reorder never rewrites neighbors. */
  position: number;
  createdAt: string;
  updatedAt: string;
}

/** POST /v1/tasks payload (mirrors domain.TaskInput). Created tasks are always source 'local'. */
export interface TaskInput {
  title: string;
  notes?: string;
  due?: string;
  allDayDue?: boolean;
  scheduledStart?: string;
  scheduledEnd?: string;
  /** Omitted: the server appends after the current max position. */
  position?: number;
}

/**
 * PATCH /v1/tasks/{id} payload (mirrors domain.TaskPatch). Omitted fields are
 * left unchanged; an explicit `null` clears notes/due/scheduledStart/
 * scheduledEnd (JSON.stringify drops `undefined` keys, so the distinction
 * survives the wire).
 */
export interface TaskPatch {
  title?: string;
  notes?: string | null;
  due?: string | null;
  allDayDue?: boolean;
  scheduledStart?: string | null;
  scheduledEnd?: string | null;
  position?: number;
}

// ---------------------------------------------------------------------------
// Weather (M2.8 Task 13) — mirrors backend/internal/domain/weather.go and
// the `capabilities` block of GET /v1/instance.
// ---------------------------------------------------------------------------

/** One day of forecast for one location (GET /v1/weather), best-effort calendar decoration. */
export interface DayForecast {
  /** YYYY-MM-DD in the requested time zone. */
  date: string;
  /** WMO weather interpretation code. */
  code: number;
  highCelsius: number;
  lowCelsius: number;
  /** Max precipitation probability for the day, 0..100. */
  precipChance: number;
}

// ---------------------------------------------------------------------------
// Maps & location autocomplete (M2.8 Task 11) — mirrors domain.Place and the
// event geo fields in backend/internal/domain/calendar.go.
// ---------------------------------------------------------------------------

/** One suggestion from GET /v1/places/autocomplete (Nominatim-backed). */
export interface Place {
  name: string;
  address: string;
  lat: number;
  lon: number;
}

export interface Event {
  /** Set when the location was picked from autocomplete; null for free-typed text. */
  locationLat?: number | null;
  locationLon?: number | null;
}
export interface EventInput {
  /** Coordinates of an autocomplete-picked location; omit for free-typed text. */
  locationLat?: number;
  locationLon?: number;
}
export interface EventPatch {
  /** Omitted = coordinates unchanged (free-typed edits keep what was stored). */
  locationLat?: number;
  locationLon?: number;
}
export interface InstanceFeatures {
  /** Maps provider configured (location autocomplete + travel times). */
  maps?: boolean;
}

// ---------------------------------------------------------------------------
// CRM integrations (M2.8) — mirrors backend/internal/domain/crm.go.
// ---------------------------------------------------------------------------

/** A CRM-side person record resolved by email address. */
export interface CrmContact {
  id: string;
  email: string;
  name: string;
  company: string;
  title: string;
  phone: string;
  owner: string;
  /** Deep link into the CRM record. */
  vendorUrl: string;
}

/** A deal/opportunity associated with a CRM contact. */
export interface CrmDeal {
  id: string;
  name: string;
  stage: string;
  amount: number | null;
  closeDate: string | null;
  vendorUrl: string;
}

/** Everything the contact pane shows for one email address, per vendor. */
export interface CrmContext {
  vendor: IntegrationVendor;
  /** Null = the address is not in this CRM. */
  contact: CrmContact | null;
  deals: CrmDeal[];
}

/**
 * One email engagement to record on the CRM contact's timeline. Logging is
 * always an explicit per-message user action ("Log to HubSpot") — mail is
 * never exported to a CRM implicitly or in bulk.
 */
export interface CrmEmailLogInput {
  contactEmail: string;
  subject: string;
  bodyText: string;
  /** RFC 3339. */
  sentAt: string;
  direction: 'inbound' | 'outbound';
}

// ---------------------------------------------------------------------------
// Interesting-calendar ICS subscriptions (M2.8 Task 15)
// ---------------------------------------------------------------------------

export interface Event {
  /**
   * Set when the event is a read-only mirror of an ICS feed subscription.
   * Subscription events cannot be edited, RSVP'd, or deleted, and never
   * count as busy time for availability.
   */
  subscriptionId?: string;
}

/** A user-added "interesting calendar" ICS feed (https only, read-only). */
export interface CalendarSubscription {
  id: string;
  url: string;
  name: string;
  color: string;
  isVisible: boolean;
  /** Last fetch ATTEMPT; `lastError` (not this) is what signals staleness. */
  lastFetchedAt: string | null;
  /** Non-null when the last refresh failed; the previous event set is kept. */
  lastError: string | null;
  createdAt: string;
}

export interface CalendarSubscriptionInput {
  /** Must be an absolute https URL. */
  url: string;
  /** Optional label; the feed's X-WR-CALNAME (else its host) fills in. */
  name?: string;
  color?: string;
}

export interface CalendarSubscriptionPatch {
  name?: string;
  color?: string;
  isVisible?: boolean;
}

// ---------------------------------------------------------------------------
// Time insights (M2.8 Task 17) — mirrors backend/internal/domain/insights.go
// field-for-field. Computed server-side from the local mirror only.
// ---------------------------------------------------------------------------

/** One "top person": meetings shared with them and minutes spent, in range. */
export interface PersonStat {
  email: string;
  name: string;
  meetings: number;
  minutes: number;
}

/** One day's meeting-vs-focus split for the per-day mini bars. */
export interface DayStat {
  /** YYYY-MM-DD. */
  date: string;
  meetingMinutes: number;
  focusMinutes: number;
}

/**
 * Aggregated time analytics for [from, to) (GET /v1/insights/time). Meetings
 * are events with >= 2 attendees not declined/cancelled; focus is managed
 * focus blocks plus focus-titled events; task minutes are scheduled task
 * blocks. The range is capped at 92 days server-side.
 */
export interface TimeInsights {
  /** RFC 3339. */
  from: string;
  /** RFC 3339. */
  to: string;
  meetingMinutes: number;
  focusMinutes: number;
  taskMinutes: number;
  meetingCount: number;
  /** Weekly focus goal scaled to the range; 0 when FocusGuard is off. */
  focusGoalMinutes: number;
  /** Top 5 by minutes, the user's own addresses excluded. */
  topPeople: PersonStat[];
  byDay: DayStat[];
}
