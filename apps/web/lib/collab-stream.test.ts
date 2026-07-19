import { describe, expect, it, vi } from 'vitest';

import { type CollabEvent, openCollabStream } from '@/lib/collab-stream';

const encoder = new TextEncoder();

/** Builds a 200 text/event-stream Response from scripted body chunks. */
function sseResponse(chunks: string[]): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

/** A fetch that never settles — parks the stream after scripted responses. */
function hangForever(): Promise<Response> {
  return new Promise<Response>(() => {});
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe('openCollabStream', () => {
  it('parses data frames into events, ignoring comments and split chunks', async () => {
    const ev = { topic: 'team:t1', type: 'comment.created', payload: { id: 'c1' } };
    const frame = `event: comment.created\ndata: ${JSON.stringify(ev)}\n\n`;
    const fetchFn = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        // keepalive comment, then a data frame split mid-JSON across chunks
        sseResponse([': connected\n\n', frame.slice(0, 30), frame.slice(30)])
      )
      .mockImplementation(hangForever);

    const events: CollabEvent[] = [];
    const close = openCollabStream((e) => events.push(e), {
      fetchFn,
      getToken: async () => 'tok-123',
      baseUrl: 'https://api.test',
      initialDelayMs: 1,
    });

    await vi.waitFor(() => expect(events).toHaveLength(1));
    expect(events[0]).toEqual(ev);
    expect(fetchFn).toHaveBeenCalledWith(
      'https://api.test/v1/collab/stream',
      expect.objectContaining({
        headers: expect.objectContaining({
          Accept: 'text/event-stream',
          Authorization: 'Bearer tok-123',
        }),
      })
    );
    close();
  });

  it('reconnects when the stream ends and keeps delivering', async () => {
    const first = { topic: 'user:u1', type: 'mention', payload: {} };
    const second = { topic: 'team:t1', type: 'share.updated', payload: { v: 2 } };
    const fetchFn = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(sseResponse([`data: ${JSON.stringify(first)}\n\n`]))
      .mockResolvedValueOnce(sseResponse([`data: ${JSON.stringify(second)}\n\n`]))
      .mockImplementation(hangForever);

    const events: CollabEvent[] = [];
    const close = openCollabStream((e) => events.push(e), {
      fetchFn,
      getToken: async () => null,
      baseUrl: 'https://api.test',
      initialDelayMs: 1,
    });

    await vi.waitFor(() => expect(events).toHaveLength(2));
    expect(events).toEqual([first, second]);
    expect(fetchFn.mock.calls.length).toBeGreaterThanOrEqual(2);
    close();
  });

  it('backs off after a failed connection instead of hot-looping', async () => {
    const fetchFn = vi.fn<typeof fetch>().mockRejectedValue(new Error('offline'));
    const close = openCollabStream(() => {}, {
      fetchFn,
      getToken: async () => null,
      baseUrl: 'https://api.test',
      initialDelayMs: 60_000,
    });

    await vi.waitFor(() => expect(fetchFn).toHaveBeenCalledTimes(1));
    await sleep(40); // long delay pending — no second attempt yet
    expect(fetchFn).toHaveBeenCalledTimes(1);
    close();
  });

  it('skips malformed frames without dying', async () => {
    const good = { topic: 'team:t1', type: 'comment.deleted', payload: null };
    const fetchFn = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        sseResponse(['data: {not-json\n\n', `data: ${JSON.stringify(good)}\n\n`])
      )
      .mockImplementation(hangForever);

    const events: CollabEvent[] = [];
    const close = openCollabStream((e) => events.push(e), {
      fetchFn,
      getToken: async () => null,
      baseUrl: 'https://api.test',
      initialDelayMs: 1,
    });

    await vi.waitFor(() => expect(events).toHaveLength(1));
    expect(events[0]).toEqual(good);
    close();
  });

  it('close() stops reconnecting', async () => {
    const fetchFn = vi.fn<typeof fetch>().mockImplementation(async () => sseResponse([]));
    const close = openCollabStream(() => {}, {
      fetchFn,
      getToken: async () => null,
      baseUrl: 'https://api.test',
      initialDelayMs: 1,
    });

    await vi.waitFor(() => expect(fetchFn.mock.calls.length).toBeGreaterThanOrEqual(1));
    close();
    await sleep(30);
    const after = fetchFn.mock.calls.length;
    await sleep(30);
    expect(fetchFn.mock.calls.length).toBe(after);
  });

  it('an external AbortSignal also closes the stream', async () => {
    const controller = new AbortController();
    const fetchFn = vi.fn<typeof fetch>().mockImplementation(async () => sseResponse([]));
    openCollabStream(() => {}, {
      signal: controller.signal,
      fetchFn,
      getToken: async () => null,
      baseUrl: 'https://api.test',
      initialDelayMs: 1,
    });

    await vi.waitFor(() => expect(fetchFn.mock.calls.length).toBeGreaterThanOrEqual(1));
    controller.abort();
    await sleep(30);
    const after = fetchFn.mock.calls.length;
    await sleep(30);
    expect(fetchFn.mock.calls.length).toBe(after);
  });
});