// Client tests for the M2.8 per-user integrations surface (Task 9). Kept in
// their own file (not client.test.ts) so parallel milestone tasks don't
// collide in one giant spec.
import { describe, expect, it, vi } from 'vitest';
import { ApiClient, ApiRequestError } from './client';
import type { IntegrationConnection } from './types';

interface RecordedRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

interface FakeResponseSpec {
  status: number;
  body?: unknown;
}

function createFakeFetch(responses: FakeResponseSpec[] = [{ status: 200, body: {} }]) {
  const calls: RecordedRequest[] = [];
  let callIndex = 0;
  const fetchFn = vi.fn(async (input: string | URL, init: RequestInit = {}) => {
    const headers: Record<string, string> = {};
    if (init.headers) {
      for (const [key, value] of Object.entries(init.headers as Record<string, string>)) {
        headers[key] = value;
      }
    }
    let body: unknown;
    if (typeof init.body === 'string') {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    calls.push({ url: String(input), method: init.method ?? 'GET', headers, body });
    const spec = responses[Math.min(callIndex, responses.length - 1)];
    callIndex++;
    const responseBody =
      spec.status === 204 || spec.body === undefined ? null : JSON.stringify(spec.body);
    return new Response(responseBody, {
      status: spec.status,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  return { fetchFn, calls };
}

const BASE_URL = 'https://api.calendium.test';
const TOKEN = 'test-access-token';

function makeClient(responses?: FakeResponseSpec[]) {
  const { fetchFn, calls } = createFakeFetch(responses);
  const client = new ApiClient({
    baseUrl: BASE_URL,
    getAccessToken: vi.fn(async () => TOKEN),
    fetch: fetchFn as unknown as typeof fetch,
  });
  return { client, calls };
}

const CONNECTION: IntegrationConnection = {
  id: 'conn1',
  vendor: 'todoist',
  externalAccount: 'person@example.com',
  status: 'active',
  lastError: null,
  createdAt: '2026-07-19T12:00:00Z',
};

describe('listIntegrations', () => {
  it('GETs /v1/integrations with the bearer token', async () => {
    const { client, calls } = makeClient([{ status: 200, body: [CONNECTION] }]);
    const result = await client.listIntegrations();
    expect(calls[0].url).toBe(`${BASE_URL}/v1/integrations`);
    expect(calls[0].method).toBe('GET');
    expect(calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(result).toEqual([CONNECTION]);
  });

  it('throws ApiRequestError(501, not_implemented) when the surface is unwired', async () => {
    const { client } = makeClient([
      {
        status: 501,
        body: { error: { code: 'not_implemented', message: 'This feature is not yet available.' } },
      },
    ]);
    const err = await client.listIntegrations().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).status).toBe(501);
    expect((err as ApiRequestError).code).toBe('not_implemented');
  });
});

describe('connectIntegration', () => {
  it('POSTs the redirectUrl to /v1/integrations/connect/{vendor}', async () => {
    const { client, calls } = makeClient([
      { status: 200, body: { url: 'https://app.hubspot.com/oauth/authorize?state=abc' } },
    ]);
    const result = await client.connectIntegration('hubspot', 'http://localhost:3000/settings');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/integrations/connect/hubspot`);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].body).toEqual({ redirectUrl: 'http://localhost:3000/settings' });
    expect(result.url).toContain('hubspot.com');
  });
});

describe('disconnectIntegration', () => {
  it('DELETEs /v1/integrations/{id} and resolves undefined on 204', async () => {
    const { client, calls } = makeClient([{ status: 204 }]);
    const result = await client.disconnectIntegration('conn1');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/integrations/conn1`);
    expect(calls[0].method).toBe('DELETE');
    expect(result).toBeUndefined();
  });

  it('URL-encodes the connection id', async () => {
    const { client, calls } = makeClient([{ status: 204 }]);
    await client.disconnectIntegration('weird/id');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/integrations/weird%2Fid`);
  });
});
