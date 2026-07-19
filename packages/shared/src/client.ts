import type {
  AiAskRequest,
  AiAskResponse,
  AiClassifier,
  AiComposeRequest,
  AiComposeResponse,
  AiEditAction,
  AiEventProposal,
  AttachmentHit,
  AvailabilitySlot,
  Booking,
  BookingLink,
  BookingLinkInput,
  BookingRequest,
  BulkAction,
  BulkActionResult,
  BusyInterval,
  Calendar,
  CalendarSet,
  CalendarSetInput,
  ClassifierInput,
  ConnectedAccount,
  ContactSummary,
  DevicePlatform,
  Draft,
  DraftInput,
  Event,
  EventInput,
  EventPatch,
  EventTemplate,
  EventTemplateInput,
  InstanceInfo,
  Label,
  MeetingPoll,
  Message,
  NotificationDevice,
  OpenEvent,
  Page,
  PollBallot,
  PollInput,
  Provider,
  PublicBookingPage,
  PublicPoll,
  ReactionResult,
  RsvpStatus,
  SendSuggestion,
  Snippet,
  Subscription,
  Team,
  TeamInvitation,
  TeamMember,
  TeamRole,
  Thread,
  ThreadAction,
  TimeProposal,
  TimeProposalInput,
  UnsubscribeResult,
  User,
  UserPreferences,
  UserPrefs,
  UserSettings,
} from './types';

/**
 * Fetches the public instance descriptor (GET /v1/instance) from a bare server
 * URL, without auth. Usable before an ApiClient exists — client apps call this
 * during server discovery to learn a server's Better Auth base URL + capabilities.
 */
export async function fetchInstance(
  baseUrl: string,
  fetchImpl?: typeof fetch
): Promise<InstanceInfo> {
  const doFetch = fetchImpl ?? fetch;
  const base = baseUrl.replace(/\/+$/, '');
  const res = await doFetch(`${base}/v1/instance`, {
    headers: { Accept: 'application/json' },
  });
  const json = await res.json().catch(() => null);
  if (!res.ok) {
    const code = json?.error?.code ?? 'unknown';
    const message = json?.error?.message ?? `Request failed with status ${res.status}`;
    throw new ApiRequestError(res.status, code, message);
  }
  return json as InstanceInfo;
}

export interface ApiClientOptions {
  baseUrl: string;
  /** Returns the current Better Auth access token (JWT) or null when signed out. */
  getAccessToken: () => Promise<string | null>;
  fetch?: typeof fetch;
}

export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string
  ) {
    super(message);
    this.name = 'ApiRequestError';
  }
}

/**
 * Typed client for the Calendium REST API (see docs/architecture.md).
 * Used by web, desktop, and mobile apps.
 */
export class ApiClient {
  constructor(private readonly opts: ApiClientOptions) {}

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const token = await this.opts.getAccessToken();
    const doFetch = this.opts.fetch ?? fetch;
    const res = await doFetch(`${this.opts.baseUrl}${path}`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 204) return undefined as T;
    const json = await res.json().catch(() => null);
    if (!res.ok) {
      const code = json?.error?.code ?? 'unknown';
      const message = json?.error?.message ?? `Request failed with status ${res.status}`;
      throw new ApiRequestError(res.status, code, message);
    }
    return json as T;
  }

  // --- Instance discovery (public, no auth) ---
  /** Public instance descriptor — Better Auth base URL + enabled features. */
  getInstance() {
    return fetchInstance(this.opts.baseUrl, this.opts.fetch);
  }

  // --- Me & billing ---
  getMe() {
    return this.request<User>('GET', '/v1/me');
  }
  getSubscription() {
    return this.request<Subscription>('GET', '/v1/billing/subscription');
  }
  /** Creates a Stripe Checkout session; redirect the browser to the returned URL. */
  createCheckoutSession(successUrl: string, cancelUrl: string) {
    return this.request<{ url: string }>('POST', '/v1/billing/checkout', {
      successUrl,
      cancelUrl,
    });
  }
  /** Creates a Stripe billing-portal session for managing/canceling the plan. */
  createBillingPortalSession(returnUrl: string) {
    return this.request<{ url: string }>('POST', '/v1/billing/portal', { returnUrl });
  }

  // --- Connected accounts ---
  listAccounts() {
    return this.request<ConnectedAccount[]>('GET', '/v1/accounts');
  }
  /** Starts the provider OAuth flow; open the returned URL in a browser. */
  connectAccount(provider: Provider, redirectUrl: string) {
    return this.request<{ url: string }>('POST', `/v1/accounts/connect/${provider}`, {
      redirectUrl,
    });
  }
  /** Replaces the account's VIP-sender list (addresses routed to the "vip" split). */
  setVipSenders(accountId: string, vipSenders: string[]) {
    return this.request<ConnectedAccount>('PUT', `/v1/accounts/${accountId}/vip-senders`, {
      vipSenders,
    });
  }
  /** Replaces the account's rich (HTML) signature, appended at send. */
  setSignature(accountId: string, signatureHtml: string) {
    return this.request<ConnectedAccount>('PUT', `/v1/accounts/${accountId}/signature`, {
      signatureHtml,
    });
  }
  /** Replaces the account's auto-BCC list, applied on every send. */
  setAutoBcc(accountId: string, autoBcc: string[]) {
    return this.request<ConnectedAccount>('PUT', `/v1/accounts/${accountId}/auto-bcc`, {
      autoBcc,
    });
  }
  disconnectAccount(accountId: string) {
    return this.request<void>('DELETE', `/v1/accounts/${accountId}`);
  }

  // --- Mail ---
  listThreads(params: {
    split?: string;
    /** Cross-split pseudo-view: 'starred' | 'snoozed' | 'sent' (never a labelId). */
    view?: string;
    labelId?: string;
    q?: string;
    cursor?: string;
    limit?: number;
    /** Scope the list to one connected account (multi-account switching). */
    accountId?: string;
  }) {
    const qs = new URLSearchParams();
    if (params.split) qs.set('split', params.split);
    if (params.view) qs.set('view', params.view);
    if (params.labelId) qs.set('labelId', params.labelId);
    if (params.q) qs.set('q', params.q);
    if (params.cursor) qs.set('cursor', params.cursor);
    if (params.limit) qs.set('limit', String(params.limit));
    if (params.accountId) qs.set('accountId', params.accountId);
    return this.request<Page<Thread>>('GET', `/v1/mail/threads?${qs}`);
  }
  getThread(threadId: string) {
    return this.request<{ thread: Thread; messages: Message[] }>(
      'GET',
      `/v1/mail/threads/${threadId}`
    );
  }
  actOnThread(threadId: string, action: ThreadAction) {
    return this.request<Thread>('POST', `/v1/mail/threads/${threadId}/actions`, { action });
  }
  /** Records that the owner opened the thread (sets real read state); idempotent. */
  markThreadOpened(threadId: string) {
    return this.request<void>('POST', `/v1/mail/threads/${threadId}/open`);
  }
  snoozeThread(threadId: string, until: string) {
    return this.request<Thread>('POST', `/v1/mail/threads/${threadId}/snooze`, { until });
  }
  setThreadReminder(threadId: string, remindAt: string | null) {
    return this.request<Thread>('POST', `/v1/mail/threads/${threadId}/reminder`, { remindAt });
  }
  listDrafts() {
    return this.request<Draft[]>('GET', '/v1/mail/drafts');
  }
  getDraft(draftId: string) {
    return this.request<Draft>('GET', `/v1/mail/drafts/${draftId}`);
  }
  saveDraft(draft: Partial<Draft> & { accountId: string }) {
    return this.request<Draft>('POST', '/v1/mail/drafts', draft);
  }
  /**
   * Full replace of a draft (PUT semantics): pass the entire DraftInput, since
   * omitted fields blank the stored value. Clearing scheduledAt before the
   * grace elapses is undo-send.
   */
  updateDraft(draftId: string, input: DraftInput) {
    return this.request<Draft>('PUT', `/v1/mail/drafts/${draftId}`, input);
  }
  deleteDraft(draftId: string) {
    return this.request<void>('DELETE', `/v1/mail/drafts/${draftId}`);
  }
  /** Sends a draft immediately, or at `scheduledAt` when set (Send Later). */
  sendDraft(draftId: string) {
    return this.request<Message>('POST', `/v1/mail/drafts/${draftId}/send`);
  }
  /**
   * Cancels a queued send within the undo-send grace window, returning the
   * reverted draft. Throws ApiRequestError(409, 'conflict') when the message
   * has already been delivered.
   */
  unsendDraft(draftId: string) {
    return this.request<Draft>('POST', `/v1/mail/drafts/${draftId}/unsend`);
  }
  listSnippets() {
    return this.request<Snippet[]>('GET', '/v1/mail/snippets');
  }
  createSnippet(snippet: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml'>) {
    return this.request<Snippet>('POST', '/v1/mail/snippets', snippet);
  }
  updateSnippet(snippetId: string, patch: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml'>) {
    return this.request<Snippet>('PUT', `/v1/mail/snippets/${snippetId}`, patch);
  }
  deleteSnippet(snippetId: string) {
    return this.request<void>('DELETE', `/v1/mail/snippets/${snippetId}`);
  }
  listLabels() {
    return this.request<Label[]>('GET', '/v1/mail/labels');
  }
  /** Adds (add=true) or removes a label on a thread. */
  setThreadLabel(threadId: string, labelId: string, add: boolean) {
    return this.request<Thread>('POST', `/v1/mail/threads/${threadId}/labels`, { labelId, add });
  }
  /** Bulk archive/read/label… up to 200 threads; label actions need labelId. */
  bulkThreadAction(input: { threadIds: string[]; action: BulkAction; labelId?: string }) {
    return this.request<BulkActionResult>('POST', '/v1/mail/threads/bulk-actions', input);
  }
  /** Clears a pending snooze (undo of snooze). */
  unsnoozeThread(threadId: string) {
    return this.request<Thread>('DELETE', `/v1/mail/threads/${threadId}/snooze`);
  }
  /** Executes unsubscribe server-side; method "link" returns a URL to open. */
  unsubscribeThread(threadId: string) {
    return this.request<UnsubscribeResult>('POST', `/v1/mail/threads/${threadId}/unsubscribe`);
  }
  /** Get Me To Zero: archives inbox mail older than the RFC 3339 cutoff. */
  archiveOlderThan(olderThan: string) {
    return this.request<{ archivedCount: number }>('POST', '/v1/mail/threads/zero', { olderThan });
  }
  getPrefs() {
    return this.request<UserPrefs>('GET', '/v1/prefs');
  }
  updatePrefs(prefs: UserPrefs) {
    return this.request<UserPrefs>('PUT', '/v1/prefs', prefs);
  }
  /** Cross-device user preferences (named theme, M2.6 Task 13). */
  getPreferences() {
    return this.request<UserPreferences>('GET', '/v1/me/preferences');
  }
  updatePreferences(prefs: UserPreferences) {
    return this.request<UserPreferences>('PUT', '/v1/me/preferences', prefs);
  }

  // --- Mail (M2.5) — Recent Opens, Smart Send, attachments, contacts, reactions ---
  /** Recent Opens feed: sent messages the recipient has opened, newest first. */
  listOpens(params: { cursor?: string; limit?: number }) {
    const qs = new URLSearchParams();
    if (params.cursor) qs.set('cursor', params.cursor);
    if (params.limit) qs.set('limit', String(params.limit));
    return this.request<Page<OpenEvent>>('GET', `/v1/mail/opens?${qs}`);
  }
  /** Smart Send recommendation for a recipient; throws ApiRequestError(404) when history is too thin. */
  getSendSuggestion(email: string) {
    const qs = new URLSearchParams({ email });
    return this.request<SendSuggestion>('GET', `/v1/mail/send-suggestion?${qs}`);
  }
  searchAttachments(params: {
    q?: string;
    contact?: string;
    threadId?: string;
    cursor?: string;
    limit?: number;
  }) {
    const qs = new URLSearchParams();
    if (params.q) qs.set('q', params.q);
    if (params.contact) qs.set('contact', params.contact);
    if (params.threadId) qs.set('threadId', params.threadId);
    if (params.cursor) qs.set('cursor', params.cursor);
    if (params.limit) qs.set('limit', String(params.limit));
    return this.request<Page<AttachmentHit>>('GET', `/v1/mail/attachments?${qs}`);
  }
  /** URL for streaming/iframe use; fetch it yourself with the bearer token. */
  attachmentContentPath(attachmentId: string): string {
    return `/v1/mail/attachments/${attachmentId}/content`;
  }
  getContact(email: string) {
    return this.request<ContactSummary>('GET', `/v1/mail/contacts/${encodeURIComponent(email)}`);
  }
  reactToMessage(messageId: string, emoji: string, sendReply?: boolean) {
    return this.request<ReactionResult>('POST', `/v1/mail/messages/${messageId}/reactions`, {
      emoji,
      sendReply: sendReply ?? false,
    });
  }
  removeReaction(messageId: string, emoji: string) {
    return this.request<void>(
      'DELETE',
      `/v1/mail/messages/${messageId}/reactions/${encodeURIComponent(emoji)}`
    );
  }

  // --- Calendar ---
  listCalendars() {
    return this.request<Calendar[]>('GET', '/v1/calendars');
  }
  updateCalendar(calendarId: string, patch: Partial<Pick<Calendar, 'isVisible' | 'color'>>) {
    return this.request<Calendar>('PATCH', `/v1/calendars/${calendarId}`, patch);
  }
  listEvents(from: string, to: string, calendarIds?: string[]) {
    const qs = new URLSearchParams({ from, to });
    if (calendarIds?.length) qs.set('calendarIds', calendarIds.join(','));
    return this.request<Event[]>('GET', `/v1/events?${qs}`);
  }
  createEvent(input: EventInput) {
    return this.request<Event>('POST', '/v1/events', input);
  }
  updateEvent(eventId: string, patch: EventPatch) {
    return this.request<Event>('PATCH', `/v1/events/${eventId}`, patch);
  }
  deleteEvent(eventId: string) {
    return this.request<void>('DELETE', `/v1/events/${eventId}`);
  }
  rsvp(eventId: string, response: RsvpStatus) {
    return this.request<Event>('POST', `/v1/events/${eventId}/rsvp`, { response });
  }
  /** Free slots for "share availability" composing flows. */
  getAvailability(from: string, to: string, durationMinutes: number) {
    const qs = new URLSearchParams({ from, to, duration: String(durationMinutes) });
    return this.request<AvailabilitySlot[]>('GET', `/v1/availability?${qs}`);
  }
  listEventTemplates() {
    return this.request<EventTemplate[]>('GET', '/v1/event-templates');
  }
  createEventTemplate(input: EventTemplateInput) {
    return this.request<EventTemplate>('POST', '/v1/event-templates', input);
  }
  updateEventTemplate(id: string, input: EventTemplateInput) {
    return this.request<EventTemplate>('PUT', `/v1/event-templates/${id}`, input);
  }
  deleteEventTemplate(id: string) {
    return this.request<void>('DELETE', `/v1/event-templates/${id}`);
  }
  useEventTemplate(id: string) {
    return this.request<void>('POST', `/v1/event-templates/${id}/use`);
  }
  listCalendarSets() {
    return this.request<CalendarSet[]>('GET', '/v1/calendar-sets');
  }
  createCalendarSet(input: CalendarSetInput) {
    return this.request<CalendarSet>('POST', '/v1/calendar-sets', input);
  }
  updateCalendarSet(id: string, input: CalendarSetInput) {
    return this.request<CalendarSet>('PUT', `/v1/calendar-sets/${id}`, input);
  }
  deleteCalendarSet(id: string) {
    return this.request<void>('DELETE', `/v1/calendar-sets/${id}`);
  }

  // --- Search ---
  search(q: string) {
    return this.request<{ threads: Thread[]; events: Event[] }>(
      'GET',
      `/v1/search?q=${encodeURIComponent(q)}`
    );
  }

  // --- AI ---
  // The backend exposes a single POST /v1/ai/compose route whose `action`
  // selects compose/reply/summarize/ask; the helpers below mirror it.
  aiCompose(req: AiComposeRequest) {
    return this.request<AiComposeResponse>('POST', '/v1/ai/compose', req);
  }
  /** Summarize a thread (action='summarize'); prompt is optional. */
  aiSummarize(req: Omit<AiComposeRequest, 'action'>) {
    return this.aiCompose({ ...req, action: 'summarize' });
  }
  /** Ask a question about a thread/draft (action='ask'). */
  aiAsk(req: Omit<AiComposeRequest, 'action'>) {
    return this.aiCompose({ ...req, action: 'ask' });
  }

  // --- AI (M2.3) ---
  /** Cited Q&A over the mailbox (or one thread when req.threadId is set); used by the AI sidebar. */
  aiAskCited(req: AiAskRequest) {
    return this.request<AiAskResponse>('POST', '/v1/ai/ask', req);
  }
  /** AI-generated quick-reply suggestions for a thread. */
  getInstantReplies(threadId: string) {
    return this.request<{ replies: string[] }>(
      'GET',
      `/v1/mail/threads/${threadId}/instant-replies`
    );
  }
  /** Improves/shortens/simplifies/fixes grammar on, or changes the tone of, an existing draft in place. */
  aiEditDraft(action: AiEditAction, draftId: string, tone?: string) {
    return this.aiCompose({ action, draftId, prompt: '', tone });
  }
  /** Instant Event AI: proposes a calendar event derived from a thread. */
  proposeEvent(threadId: string) {
    return this.request<AiEventProposal>('POST', '/v1/ai/event-proposal', { threadId });
  }
  listClassifiers() {
    return this.request<AiClassifier[]>('GET', '/v1/classifiers');
  }
  createClassifier(input: ClassifierInput) {
    return this.request<AiClassifier>('POST', '/v1/classifiers', input);
  }
  updateClassifier(id: string, input: ClassifierInput) {
    return this.request<AiClassifier>('PATCH', `/v1/classifiers/${id}`, input);
  }
  deleteClassifier(id: string) {
    return this.request<void>('DELETE', `/v1/classifiers/${id}`);
  }

  // --- Push devices ---
  registerDevice(platform: DevicePlatform, token: string) {
    return this.request<NotificationDevice>('POST', '/v1/devices', { platform, token });
  }
  unregisterDevice(deviceId: string) {
    return this.request<void>('DELETE', `/v1/devices/${deviceId}`);
  }

  // --- Scheduling (M2.4) — owner-authenticated surface ---
  // The public booking/poll routes are exposed as standalone fetchers below
  // (fetchPublicBookingPage, fetchPublicSlots, createPublicBooking,
  // fetchPublicPoll, votePublicPoll), since booking/poll visitor pages load
  // before any ApiClient (with a signed-in getAccessToken) can exist.
  listBookingLinks() {
    return this.request<BookingLink[]>('GET', '/v1/booking-links');
  }
  createBookingLink(input: BookingLinkInput) {
    return this.request<BookingLink>('POST', '/v1/booking-links', input);
  }
  updateBookingLink(id: string, input: BookingLinkInput) {
    return this.request<BookingLink>('PUT', `/v1/booking-links/${id}`, input);
  }
  deleteBookingLink(id: string) {
    return this.request<void>('DELETE', `/v1/booking-links/${id}`);
  }
  listBookings() {
    return this.request<Booking[]>('GET', '/v1/bookings');
  }
  cancelBooking(id: string) {
    return this.request<void>('POST', `/v1/bookings/${id}/cancel`);
  }
  listPolls() {
    return this.request<MeetingPoll[]>('GET', '/v1/polls');
  }
  createPoll(input: PollInput) {
    return this.request<MeetingPoll>('POST', '/v1/polls', input);
  }
  confirmPoll(id: string, optionId: string) {
    return this.request<MeetingPoll>('POST', `/v1/polls/${id}/confirm`, { optionId });
  }
  deletePoll(id: string) {
    return this.request<void>('DELETE', `/v1/polls/${id}`);
  }
  /** Propose-new-time: an invitee counter-proposes a time for an existing event. */
  proposeTime(eventId: string, input: TimeProposalInput) {
    return this.request<TimeProposal>('POST', `/v1/events/${eventId}/propose-time`, input);
  }
  listProposals(eventId: string) {
    return this.request<TimeProposal[]>('GET', `/v1/events/${eventId}/proposals`);
  }
  /** Accepts a proposal: the organizer patches the event to the proposed time. */
  acceptProposal(eventId: string, proposalId: string) {
    return this.request<Event>('POST', `/v1/events/${eventId}/proposals/${proposalId}/accept`);
  }
  declineProposal(eventId: string, proposalId: string) {
    return this.request<void>('POST', `/v1/events/${eventId}/proposals/${proposalId}/decline`);
  }
  /** Guest free/busy for the Find-a-Time grid. */
  getFreeBusy(emails: string[], from: string, to: string) {
    return this.request<Record<string, BusyInterval[]>>('POST', '/v1/freebusy', {
      emails,
      from,
      to,
    });
  }
  getSettings() {
    return this.request<UserSettings>('GET', '/v1/settings');
  }
  updateSettings(s: UserSettings) {
    return this.request<UserSettings>('PUT', '/v1/settings', s);
  }

  // --- Teams (M2.7) ---
  // Authorization is decided server-side: a team the caller is not a member
  // of 404s (existence is never leaked), and role-gated calls 403.
  listTeams() {
    return this.request<Team[]>('GET', '/v1/teams');
  }
  createTeam(name: string) {
    return this.request<Team>('POST', '/v1/teams', { name });
  }
  /** The team plus its member list; caller must be a member. */
  getTeam(teamId: string) {
    return this.request<{ team: Team; members: TeamMember[] }>(
      'GET',
      `/v1/teams/${encodeURIComponent(teamId)}`
    );
  }
  renameTeam(teamId: string, name: string) {
    return this.request<Team>('PATCH', `/v1/teams/${encodeURIComponent(teamId)}`, { name });
  }
  /** Owner-only; removes the team and all memberships, shares, and comments. */
  deleteTeam(teamId: string) {
    return this.request<void>('DELETE', `/v1/teams/${encodeURIComponent(teamId)}`);
  }
  /** Admin+; only owners may grant or revoke the owner role. Demoting the last owner 409s. */
  setMemberRole(teamId: string, userId: string, role: TeamRole) {
    return this.request<TeamMember>(
      'PATCH',
      `/v1/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`,
      { role }
    );
  }
  /** Toggles the CALLER's own read-status sharing opt-in for this team. */
  setShareReadStatuses(teamId: string, share: boolean) {
    return this.request<TeamMember>(
      'PUT',
      `/v1/teams/${encodeURIComponent(teamId)}/read-status-sharing`,
      { share }
    );
  }
  /** Admins remove members, owners remove anyone; pass your own userId to leave. */
  removeMember(teamId: string, userId: string) {
    return this.request<void>(
      'DELETE',
      `/v1/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(userId)}`
    );
  }
  /** Admin+; emails an invite link through the inviter's own connected account. */
  invite(teamId: string, email: string, role: TeamRole) {
    return this.request<TeamInvitation>(
      'POST',
      `/v1/teams/${encodeURIComponent(teamId)}/invitations`,
      { email, role }
    );
  }
  listInvitations(teamId: string) {
    return this.request<TeamInvitation[]>(
      'GET',
      `/v1/teams/${encodeURIComponent(teamId)}/invitations`
    );
  }
  revokeInvitation(teamId: string, invitationId: string) {
    return this.request<void>(
      'DELETE',
      `/v1/teams/${encodeURIComponent(teamId)}/invitations/${encodeURIComponent(invitationId)}`
    );
  }
  /**
   * Redeems the raw token from an emailed invite link for the signed-in
   * user, returning the joined team. Unknown/expired/revoked tokens throw
   * ApiRequestError(404, 'not_found') — they are indistinguishable.
   */
  acceptInvitation(token: string) {
    return this.request<Team>('POST', '/v1/invitations/accept', { token });
  }
}

// ---------------------------------------------------------------------------
// Scheduling (M2.4) — standalone unauthenticated fetchers
//
// The public booking page and public poll page must work without a signed-in
// user (no Better Auth session, no ApiClient instance): a visitor opens
// /book/{slug} or /poll/{token} directly. These mirror the fetchInstance
// pattern above — a bare baseUrl + optional fetchImpl, no Authorization
// header ever sent.
// ---------------------------------------------------------------------------

async function publicRequest<T>(
  baseUrl: string,
  method: string,
  path: string,
  body: unknown,
  fetchImpl?: typeof fetch
): Promise<T> {
  const doFetch = fetchImpl ?? fetch;
  const base = baseUrl.replace(/\/+$/, '');
  const res = await doFetch(`${base}${path}`, {
    method,
    headers: {
      Accept: 'application/json',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const json = await res.json().catch(() => null);
  if (!res.ok) {
    const code = json?.error?.code ?? 'unknown';
    const message = json?.error?.message ?? `Request failed with status ${res.status}`;
    throw new ApiRequestError(res.status, code, message);
  }
  return json as T;
}

/** The public booking-link document: title, duration, owner display name — no auth. */
export function fetchPublicBookingPage(
  baseUrl: string,
  slug: string,
  fetchImpl?: typeof fetch
): Promise<PublicBookingPage> {
  return publicRequest(baseUrl, 'GET', `/v1/public/booking/${slug}`, undefined, fetchImpl);
}

/** Bookable start times for a booking link between from and to (RFC 3339). */
export function fetchPublicSlots(
  baseUrl: string,
  slug: string,
  from: string,
  to: string,
  fetchImpl?: typeof fetch
): Promise<AvailabilitySlot[]> {
  const qs = new URLSearchParams({ from, to });
  return publicRequest(baseUrl, 'GET', `/v1/public/booking/${slug}/slots?${qs}`, undefined, fetchImpl);
}

/** Books a slot on a public booking link (hold → confirm pipeline). */
export function createPublicBooking(
  baseUrl: string,
  slug: string,
  req: BookingRequest,
  fetchImpl?: typeof fetch
): Promise<Booking> {
  return publicRequest(baseUrl, 'POST', `/v1/public/booking/${slug}/bookings`, req, fetchImpl);
}

/** The public meeting-poll document: options plus anonymized tallies. */
export function fetchPublicPoll(
  baseUrl: string,
  token: string,
  fetchImpl?: typeof fetch
): Promise<PublicPoll> {
  return publicRequest(baseUrl, 'GET', `/v1/public/polls/${token}`, undefined, fetchImpl);
}

/** Records (or replaces) one voter's ballot; returns the refreshed public poll view. */
export function votePublicPoll(
  baseUrl: string,
  token: string,
  ballot: PollBallot,
  fetchImpl?: typeof fetch
): Promise<PublicPoll> {
  return publicRequest(baseUrl, 'POST', `/v1/public/polls/${token}/votes`, ballot, fetchImpl);
}
