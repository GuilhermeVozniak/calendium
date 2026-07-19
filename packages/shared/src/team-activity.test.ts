// Tests for ApiClient.teamThreadActivity (M2.7 Task 10 — team read statuses).
import { describe, expect, it, vi } from 'vitest';

import { ApiClient } from './client';
import type { TeamThreadActivity } from './types';

const rows: TeamThreadActivity[] = [
  {
    teamId: 'team1',
    userId: 'mate',
    conversationKey: '<k@x>',
    openedAt: '2026-07-18T10:00:00Z',
    repliedAt: null,
  },
];

function makeClient(status = 200, body: unknown = rows) {
  const fetchFn = vi.fn(
    async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
  );
  const client = new ApiClient({
    baseUrl: 'https://api.test',
    getAccessToken: async () => 'tok',
    fetch: fetchFn as unknown as typeof fetch,
  });
  return { client, fetchFn };
}

describe('teamThreadActivity', () => {
  it('GETs /v1/mail/threads/{id}/team-activity with the bearer token', async () => {
    const { client, fetchFn } = makeClient();
    const got = await client.teamThreadActivity('t42');
    expect(got).toEqual(rows);
    expect(fetchFn).toHaveBeenCalledTimes(1);
    const [url, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('https://api.test/v1/mail/threads/t42/team-activity');
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok');
  });

  it('throws ApiRequestError with the server error code on failure', async () => {
    const { client } = makeClient(404, { error: { code: 'not_found', message: 'nope' } });
    await expect(client.teamThreadActivity('ghost')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    });
  });
});
