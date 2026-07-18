import type {
  AiComposeRequest,
  AiComposeResponse,
  AvailabilitySlot,
  BulkAction,
  BulkActionResult,
  Calendar,
  CalendarSet,
  CalendarSetInput,
  ConnectedAccount,
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
  Message,
  NotificationDevice,
  Page,
  Provider,
  RsvpStatus,
  Snippet,
  Subscription,
  Thread,
  ThreadAction,
  UnsubscribeResult,
  User,
  UserPrefs,
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
  }) {
    const qs = new URLSearchParams();
    if (params.split) qs.set('split', params.split);
    if (params.view) qs.set('view', params.view);
    if (params.labelId) qs.set('labelId', params.labelId);
    if (params.q) qs.set('q', params.q);
    if (params.cursor) qs.set('cursor', params.cursor);
    if (params.limit) qs.set('limit', String(params.limit));
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

  // --- Push devices ---
  registerDevice(platform: DevicePlatform, token: string) {
    return this.request<NotificationDevice>('POST', '/v1/devices', { platform, token });
  }
  unregisterDevice(deviceId: string) {
    return this.request<void>('DELETE', `/v1/devices/${deviceId}`);
  }
}
