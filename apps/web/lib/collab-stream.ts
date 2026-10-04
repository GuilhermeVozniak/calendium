import { getAccessToken, isApiSuspended, onApiSuspended } from '@/lib/auth-client';
import { env } from '@/lib/env';

/** One realtime collaboration notification (mirrors the Go port.CollabEvent). */
export interface CollabEvent {
  /** Delivery scope: `team:<teamID>` | `share:<shareID>` | `user:<userID>`. */
  topic: string;
  /** `comment.created` | `comment.deleted` | `activity.updated` | `share.updated` | `mention`. */
  type: string;
  payload: unknown;
}

export interface CollabStreamOptions {
  /** Aborting this signal closes the stream (same as the returned closer). */
  signal?: AbortSignal;
  /**
   * Override the SSE endpoint path (default: /v1/collab/stream). Used by the
   * shared-conversation page to scope the stream to one share:
   * `/v1/shared/threads/{token}/stream`. Same fetch-based auth: the token
   * never appears anywhere but the request itself.
   */
  path?: string;
  /** Test seams / overrides — default to the real fetch, auth token, and API URL. */
  fetchFn?: typeof fetch;
  getToken?: () => Promise<string | null>;
  baseUrl?: string;
  /** Reconnect backoff: initial delay, doubled per failure up to the cap. */
  initialDelayMs?: number;
  maxDelayMs?: number;
}

const STREAM_PATH = '/v1/collab/stream';

/**
 * Opens the realtime collaboration stream (GET /v1/collab/stream) and invokes
 * `onEvent` for every parsed SSE data frame. Uses `fetch` + `ReadableStream`
 * rather than `EventSource` because the API authenticates via the
 * `Authorization` header, which `EventSource` cannot send — so no token ever
 * appears in a URL. Reconnects on stream end or network failure with
 * exponential backoff (1s doubling to a 30s cap, reset after a successful
 * connection); dropped events are recovered by the app's normal re-fetches.
 * Returns a close function (idempotent).
 */
export function openCollabStream(
  onEvent: (ev: CollabEvent) => void,
  options: CollabStreamOptions = {}
): () => void {
  const controller = new AbortController();
  const { signal } = controller;
  if (options.signal) {
    if (options.signal.aborted) controller.abort();
    else options.signal.addEventListener('abort', () => controller.abort(), { once: true });
  }

  const fetchFn = options.fetchFn ?? fetch;
  const getToken = options.getToken ?? getAccessToken;
  const baseUrl = (options.baseUrl ?? env.apiUrl).replace(/\/+$/, '');
  const streamPath = options.path ?? STREAM_PATH;
  const initialDelay = options.initialDelayMs ?? 1_000;
  const maxDelay = options.maxDelayMs ?? 30_000;

  const dispatch = (data: string): void => {
    if (!data) return;
    try {
      const parsed: unknown = JSON.parse(data);
      if (parsed && typeof parsed === 'object') onEvent(parsed as CollabEvent);
    } catch {
      // Malformed frame — skip it; state converges via normal fetches.
    }
  };

  /** Reads the SSE body, accumulating `data:` lines and firing on blank lines. */
  const readStream = async (body: ReadableStream<Uint8Array>): Promise<void> => {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let data = '';
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) return;
        buffer += decoder.decode(value, { stream: true });
        let newline = buffer.indexOf('\n');
        while (newline !== -1) {
          const line = buffer.slice(0, newline).replace(/\r$/, '');
          buffer = buffer.slice(newline + 1);
          if (line === '') {
            dispatch(data);
            data = '';
          } else if (line.startsWith('data:')) {
            // Multi-line data concatenates with \n per the SSE spec.
            data += (data ? '\n' : '') + line.slice(5).replace(/^ /, '');
          }
          // `:` keepalive comments and `event:`/`id:` fields carry nothing the
          // client needs — the type is inside the JSON payload.
          newline = buffer.indexOf('\n');
        }
      }
    } finally {
      reader.releaseLock();
    }
  };

  /** Abortable sleep so close() never leaves a reconnect timer pending. */
  const sleep = (ms: number): Promise<void> =>
    new Promise((resolve) => {
      const done = (): void => {
        clearTimeout(timer);
        signal.removeEventListener('abort', done);
        resolve();
      };
      const timer = setTimeout(done, ms);
      signal.addEventListener('abort', done, { once: true });
    });

  const run = async (): Promise<void> => {
    let delay = initialDelay;
    while (!signal.aborted) {
      // Account deletion (suspendApi): no connection while paused, and the
      // open one is dropped (below) so nothing reaches the API mid-purge.
      if (!isApiSuspended()) {
        const attempt = new AbortController();
        const abortAttempt = (): void => attempt.abort();
        signal.addEventListener('abort', abortAttempt, { once: true });
        const offSuspend = onApiSuspended(abortAttempt);
        try {
          const token = await getToken();
          if (attempt.signal.aborted) throw new Error('suspended');
          const res = await fetchFn(`${baseUrl}${streamPath}`, {
            headers: {
              Accept: 'text/event-stream',
              ...(token ? { Authorization: `Bearer ${token}` } : {}),
            },
            signal: attempt.signal,
          });
          if (res.ok && res.body) {
            delay = initialDelay; // healthy connection resets the backoff
            await readStream(res.body);
          }
        } catch {
          // Network/auth failure — fall through to the backoff below.
        } finally {
          signal.removeEventListener('abort', abortAttempt);
          offSuspend();
        }
      }
      if (signal.aborted) return;
      await sleep(delay);
      delay = Math.min(delay * 2, maxDelay);
    }
  };

  void run();
  return () => controller.abort();
}