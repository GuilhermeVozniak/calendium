import type {
  AiAskRequest,
  AiAskResponse,
  AiClassifier,
  AiComposeRequest,
  AiComposeResponse,
  AiEditAction,
  AiEventProposal,
  AttachmentHit,
  AuditEntry,
  Delegation,
  DelegationList,
  DelegationScope,
  AvailabilitySlot,
  Booking,
  BookingLink,
  BookingLinkInput,
  BookingRequest,
  BulkAction,
  BulkActionResult,
  BusyInterval,
  Calendar,
  CalendarPermission,
  CalendarPrefs,
  CalendarPrefsPatch,
  CalendarSet,
  CalendarSetInput,
  CalendarShare,
  CalendarShareInput,
  ClassifierInput,
  Comment,
  CommentInput,
  ConnectedAccount,
  ContactSummary,
  DayForecast,
  DevicePlatform,
  Draft,
  DraftInput,
  Event,
  EventInput,
  EventNote,
  EventPatch,
  EventTemplate,
  EventTemplateInput,
  InstanceInfo,
  IntegrationConnection,
  IntegrationVendor,
  Label,
  MeetingPoll,
  MemberAvailability,
  Message,
  NotificationDevice,
  OpenEvent,
  Page,
  Place,
  PollBallot,
  PollInput,
  Provider,
  PublicBookingPage,
  PublicPoll,
  ReactionResult,
  RsvpStatus,
  SendSuggestion,
  SharedThreadView,
  ShareThreadInput,
  Snippet,
  Subscription,
  Task,
  TaskInput,
  TaskPatch,
  Team,
  TeamInvitation,
  TeamMember,
  TeamRole,
  TeamThreadActivity,
  Thread,
  ThreadAction,
  ThreadShare,
  ThreadShareCreated,
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

  // --- Integrations (M2.8) ---
  /** The caller's per-user vendor integrations (Todoist/HubSpot). */
  listIntegrations() {
    return this.request<IntegrationConnection[]>('GET', '/v1/integrations');
  }
  /**
   * Starts the vendor OAuth flow; open the returned URL in a browser. Throws
   * ApiRequestError(501, 'not_implemented') when the vendor is not
   * configured on this server — gate on InstanceInfo.capabilities first.
   */
  connectIntegration(vendor: IntegrationVendor, redirectUrl: string) {
    return this.request<{ url: string }>('POST', `/v1/integrations/connect/${vendor}`, {
      redirectUrl,
    });
  }
  /** Disconnects an integration; Todoist also purges its mirrored tasks server-side. */
  disconnectIntegration(connectionId: string) {
    return this.request<void>('DELETE', `/v1/integrations/${encodeURIComponent(connectionId)}`);
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
  /** Teammate read/reply indicators for the thread's conversation (M2.7; opted-in members only). */
  teamThreadActivity(threadId: string) {
    return this.request<TeamThreadActivity[]>('GET', `/v1/mail/threads/${threadId}/team-activity`);
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
  /** teamId scopes the new snippet to a team (create only — scope is immutable afterwards). */
  createSnippet(snippet: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml' | 'teamId'>) {
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
  /** Calendar automation preferences (M2.8): FocusGuard, buffers, OOO, travel, weather. */
  getCalendarPrefs() {
    return this.request<CalendarPrefs>('GET', '/v1/prefs/calendar');
  }
  /** Partial update; omitted fields are unchanged. Returns the merged document. */
  updateCalendarPrefs(patch: CalendarPrefsPatch) {
    return this.request<CalendarPrefs>('PATCH', '/v1/prefs/calendar', patch);
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

  // --- Shared conversations & team comments (M2.7) ---
  /**
   * Creates a live share link for a thread. The response carries the raw
   * link token exactly once — show it to the user immediately; it is never
   * returned again (and must never be logged or sent to analytics).
   */
  shareThread(threadId: string, input: ShareThreadInput) {
    return this.request<ThreadShareCreated>(
      'POST',
      `/v1/mail/threads/${encodeURIComponent(threadId)}/share`,
      input
    );
  }
  listThreadShares(threadId: string) {
    return this.request<ThreadShare[]>(
      'GET',
      `/v1/mail/threads/${encodeURIComponent(threadId)}/shares`
    );
  }
  /** Revokes a share link; viewers holding it see a uniform 404 afterwards. */
  revokeThreadShare(threadId: string, shareId: string) {
    return this.request<void>(
      'DELETE',
      `/v1/mail/threads/${encodeURIComponent(threadId)}/shares/${encodeURIComponent(shareId)}`
    );
  }
  /**
   * The read-only share view for a raw link token. Works signed out (external
   * shares); when a session exists the bearer identifies the viewer for
   * team-audience membership checks. Unknown/revoked/expired tokens throw
   * ApiRequestError(404, 'not_found') — indistinguishable by design.
   */
  getSharedThread(token: string) {
    return this.request<SharedThreadView>(
      'GET',
      `/v1/shared/threads/${encodeURIComponent(token)}`
    );
  }
  /** Team comments on a thread; caller must be a member of teamId. */
  listComments(threadId: string, teamId: string) {
    const qs = new URLSearchParams({ teamId });
    return this.request<{ comments: Comment[] }>(
      'GET',
      `/v1/mail/threads/${encodeURIComponent(threadId)}/comments?${qs}`
    );
  }
  addComment(threadId: string, input: CommentInput) {
    return this.request<Comment>(
      'POST',
      `/v1/mail/threads/${encodeURIComponent(threadId)}/comments`,
      input
    );
  }
  /** Author only (team admins may delete, not edit). */
  updateComment(commentId: string, body: string) {
    return this.request<Comment>('PATCH', `/v1/comments/${encodeURIComponent(commentId)}`, {
      body,
    });
  }
  /** The author, or a team admin+ (soft delete). */
  deleteComment(commentId: string) {
    return this.request<void>('DELETE', `/v1/comments/${encodeURIComponent(commentId)}`);
  }

  // --- EA delegation (M2.7 Task 15) ---
  // A principal grants an assistant scoped access to their mail/calendar; the
  // assistant then acts as the principal via `actAs`. Authorization is decided
  // server-side on EVERY request (fail closed — revocation is immediate), and
  // every delegated mutation is audit-logged with the real actor.

  /** Grants `assistantEmail` (an existing user) the given scopes; starts pending. */
  createDelegation(assistantEmail: string, scopes: DelegationScope[]) {
    return this.request<Delegation>('POST', '/v1/delegations', { assistantEmail, scopes });
  }
  listDelegations() {
    return this.request<DelegationList>('GET', '/v1/delegations');
  }
  /** Assistant accepts a pending grant (pending → active). */
  acceptDelegation(delegationId: string) {
    return this.request<Delegation>(
      'POST',
      `/v1/delegations/${encodeURIComponent(delegationId)}/accept`
    );
  }
  /** Either party revokes; the grant stops authorizing immediately. */
  revokeDelegation(delegationId: string) {
    return this.request<void>('DELETE', `/v1/delegations/${encodeURIComponent(delegationId)}`);
  }
  /** The caller's delegated-mutation audit log (principal-only server-side), newest first. */
  listDelegationAudit(limit?: number) {
    const qs = limit !== undefined ? `?limit=${limit}` : '';
    return this.request<{ entries: AuditEntry[] }>('GET', `/v1/delegations/audit${qs}`);
  }

  /**
   * Returns a NEW client that acts as `principalUserId`: every request to a
   * delegable route (mail, search, events, calendars, availability — mirroring
   * the backend's delegationScopeForRoute map) carries the X-Calendium-Act-As
   * header. Non-delegable routes (teams, billing, accounts, devices,
   * delegations, …) are sent WITHOUT the header — the backend rejects act-as
   * on them outright. The original client is untouched and never sends the
   * header, so acting is always an explicit, per-instance choice.
   */
  actAs(principalUserId: string): ApiClient {
    const innerFetch = this.opts.fetch;
    const wrapped = (async (input: string | URL | Request, init?: RequestInit) => {
      const doFetch = innerFetch ?? fetch;
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      let pathname: string;
      try {
        pathname = new URL(url, 'http://relative.invalid').pathname;
      } catch {
        pathname = url;
      }
      if (!isDelegablePath(pathname)) return doFetch(input as string, init);
      return doFetch(input as string, {
        ...init,
        headers: {
          ...((init?.headers ?? {}) as Record<string, string>),
          [ACT_AS_HEADER]: principalUserId,
        },
      });
    }) as typeof fetch;
    return new ApiClient({ ...this.opts, fetch: wrapped });
  }

  // --- Shared calendars & team availability (M2.7 Tasks 12–13) ---
  // Share management is owner-only server-side (non-owners 404 — existence
  // is never leaked). Shared-calendar reads themselves arrive through the
  // existing listCalendars/listEvents (annotated with `sharedPermission` /
  // redacted `freeBusyOnly` events); there is no separate listing endpoint.
  listCalendarShares(calendarId: string) {
    return this.request<CalendarShare[]>(
      'GET',
      `/v1/calendars/${encodeURIComponent(calendarId)}/shares`
    );
  }
  /** Grants a user or a whole team access to one of the caller's calendars. */
  shareCalendar(calendarId: string, input: CalendarShareInput) {
    return this.request<CalendarShare>(
      'POST',
      `/v1/calendars/${encodeURIComponent(calendarId)}/shares`,
      input
    );
  }
  updateCalendarShare(calendarId: string, shareId: string, permission: CalendarPermission) {
    return this.request<CalendarShare>(
      'PATCH',
      `/v1/calendars/${encodeURIComponent(calendarId)}/shares/${encodeURIComponent(shareId)}`,
      { permission }
    );
  }
  revokeCalendarShare(calendarId: string, shareId: string) {
    return this.request<void>(
      'DELETE',
      `/v1/calendars/${encodeURIComponent(calendarId)}/shares/${encodeURIComponent(shareId)}`
    );
  }
  /**
   * Per-member opaque busy blocks for a team in [from, to) (RFC 3339, span
   * ≤ 35 days). Caller must be a member (404 otherwise); members who have
   * not shared a calendar with the team come back `shared: false` with an
   * empty list — busy blocks never carry titles or details.
   */
  teamAvailability(teamId: string, from: string, to: string) {
    const qs = new URLSearchParams({ from, to });
    return this.request<MemberAvailability[]>(
      'GET',
      `/v1/teams/${encodeURIComponent(teamId)}/availability?${qs}`
    );
  }

  // --- Event notes (M2.8 Task 4) ---
  /**
   * The event's local-only note. A missing note comes back as an empty note
   * (200, never 404), so callers need no special case.
   */
  getEventNote(eventId: string) {
    return this.request<EventNote>('GET', `/v1/events/${encodeURIComponent(eventId)}/note`);
  }
  /**
   * Replaces the event's note wholesale; an empty body with no links deletes
   * it server-side. Links must be absolute http(s) URLs.
   */
  putEventNote(eventId: string, bodyMd: string, links: string[]) {
    return this.request<EventNote>('PUT', `/v1/events/${encodeURIComponent(eventId)}/note`, {
      bodyMd,
      links,
    });
  }

  // --- Tasks (M2.8) ---
  // First-class tasks: local todos plus mirrored external provider todos.
  // All authorization is server-side and user-scoped (another user's task is
  // a plain 404); complete/reopen are idempotent.

  /**
   * Lists the caller's tasks. `from`/`to` (RFC 3339) select tasks whose
   * scheduled block overlaps [from, to) — the calendar-grid query;
   * `dueFrom`/`dueTo` select by due date — the rail's due grouping;
   * `unscheduled` restricts to rail tasks without a timeblock. Completed
   * tasks are excluded unless `includeCompleted`.
   */
  listTasks(query?: {
    from?: string;
    to?: string;
    dueFrom?: string;
    dueTo?: string;
    unscheduled?: boolean;
    includeCompleted?: boolean;
  }) {
    const qs = new URLSearchParams();
    if (query?.from) qs.set('from', query.from);
    if (query?.to) qs.set('to', query.to);
    if (query?.dueFrom) qs.set('dueFrom', query.dueFrom);
    if (query?.dueTo) qs.set('dueTo', query.dueTo);
    if (query?.unscheduled) qs.set('unscheduled', '1');
    if (query?.includeCompleted) qs.set('includeCompleted', '1');
    const search = qs.toString();
    return this.request<Task[]>('GET', `/v1/tasks${search ? `?${search}` : ''}`);
  }
  createTask(input: TaskInput) {
    return this.request<Task>('POST', '/v1/tasks', input);
  }
  /**
   * Partial update: omitted fields are left unchanged, an explicit `null`
   * clears a clearable field (notes, due, scheduledStart, scheduledEnd).
   */
  updateTask(taskId: string, patch: TaskPatch) {
    return this.request<Task>('PATCH', `/v1/tasks/${encodeURIComponent(taskId)}`, patch);
  }
  /** Checks the task off (idempotent). External tasks write through to their provider. */
  completeTask(taskId: string) {
    return this.request<Task>('POST', `/v1/tasks/${encodeURIComponent(taskId)}/complete`);
  }
  /** Clears completion (idempotent). */
  reopenTask(taskId: string) {
    return this.request<Task>('POST', `/v1/tasks/${encodeURIComponent(taskId)}/reopen`);
  }
  deleteTask(taskId: string) {
    return this.request<void>('DELETE', `/v1/tasks/${encodeURIComponent(taskId)}`);
  }

  // --- Weather (M2.8 Task 13) ---
  /**
   * Up to 14 days of daily forecast for a location (GET /v1/weather).
   * Throws ApiRequestError(501, 'not_implemented') when the server has no
   * weather vendor configured — callers treat that as "hide the weather UI".
   */
  getWeather(lat: number, lon: number, tz: string, days = 7): Promise<DayForecast[]> {
    const qs = new URLSearchParams({
      lat: String(lat),
      lon: String(lon),
      tz,
      days: String(days),
    });
    return this.request<DayForecast[]>('GET', `/v1/weather?${qs}`);
  }

  // --- Places (M2.8 Task 11): location autocomplete ---
  /**
   * Up to 5 place suggestions for a partial location query (min 3 chars).
   * Throws ApiRequestError(501, 'not_implemented') when the server has no
   * maps provider configured — callers degrade to a plain text input.
   */
  autocompletePlaces(q: string) {
    return this.request<Place[]>('GET', `/v1/places/autocomplete?q=${encodeURIComponent(q)}`);
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

// ---------------------------------------------------------------------------
// EA delegation (M2.7 Task 15) — act-as plumbing shared by ApiClient#actAs and
// app-side wrappers.
// ---------------------------------------------------------------------------

/**
 * Request header carrying the principal user id an assistant acts for. MUST
 * match `actAsHeader` in backend/internal/adapter/in/httpapi/delegation.go.
 */
export const ACT_AS_HEADER = 'X-Calendium-Act-As';

/**
 * Whether a request path may carry the act-as header — the client-side mirror
 * of the backend's delegationScopeForRoute route groups. Anything else
 * (teams, billing, accounts, devices, delegations, …) rejects act-as outright
 * server-side, so the header must never be attached there.
 *
 * Team/collaboration sub-surfaces that sit under the delegable mail/calendar
 * prefixes (thread shares, comments, team activity, snippets, calendar-share
 * management) are denied too — the backend 403s them under act-as (an
 * assistant must never mint share tokens or self-grant shares on the
 * principal's behalf), so the header is never attached there either.
 */
export function isDelegablePath(pathname: string): boolean {
  const path = pathname.split('?')[0];
  if (
    path === '/v1/mail/snippets' ||
    path.startsWith('/v1/mail/snippets/') ||
    /^\/v1\/mail\/threads\/[^/]+\/(share$|shares($|\/)|comments$|team-activity$)/.test(path) ||
    /^\/v1\/calendars\/[^/]+\/shares($|\/)/.test(path)
  ) {
    return false;
  }
  return (
    path === '/v1/search' ||
    path === '/v1/mail' ||
    path.startsWith('/v1/mail/') ||
    path === '/v1/events' ||
    path.startsWith('/v1/events/') ||
    path === '/v1/calendars' ||
    path.startsWith('/v1/calendars/') ||
    path === '/v1/availability'
  );
}
