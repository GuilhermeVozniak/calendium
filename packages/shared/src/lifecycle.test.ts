import { describe, expect, it, vi } from 'vitest';
import { ApiClient, ApiRequestError } from './client';
import type { UserSettings, UserSettingsUpdate } from './types';

const BASE = 'https://api.test';

function makeClient(fetchFn: (...args: unknown[]) => Promise<Response>, getAccessToken = async () => 'tok') {
  return new ApiClient({
    baseUrl: BASE,
    getAccessToken,
    fetch: fetchFn as unknown as typeof fetch,
  });
}

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

describe('downloadExport', () => {
  it('GETs /v1/me/export with the bearer token and returns the raw zip blob (no JSON parsing)', async () => {
    const bytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04]);
    const fetchFn = vi.fn(
      async () => new Response(bytes, { status: 200, headers: { 'Content-Type': 'application/zip' } })
    );
    const blob = await makeClient(fetchFn).downloadExport();
    expect(blob).toBeInstanceOf(Blob);
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytes);
    const [url, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${BASE}/v1/me/export`);
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok');
    expect((init.headers as Record<string, string>).Accept).toBe('application/zip');
    expect(init.body).toBeUndefined();
  });

  it('maps 409 export_throttled to ApiRequestError with retryAfterSeconds from Retry-After', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'export_throttled', message: 'Try later' } }, { 'Retry-After': '1800' })
    );
    const err = await makeClient(fetchFn)
      .downloadExport()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    const typed = err as ApiRequestError;
    expect(typed.status).toBe(409);
    expect(typed.code).toBe('export_throttled');
    expect(typed.retryAfterSeconds).toBe(1800);
  });

  it('leaves retryAfterSeconds undefined when the header is missing or unparsable', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(500, { error: { code: 'internal', message: 'boom' } }, { 'Retry-After': 'soon' })
    );
    const err = (await makeClient(fetchFn)
      .downloadExport()
      .catch((e: unknown) => e)) as ApiRequestError;
    expect(err.code).toBe('internal');
    expect(err.retryAfterSeconds).toBeUndefined();
  });

  it('re-mints the access token once when the API rejects the JWT, like request()', async () => {
    let n = 0;
    const getAccessToken = vi.fn(async () => `tok${++n}`);
    const fetchFn = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(401, { error: { code: 'unauthorized', message: 'invalid or expired access token' } })
      )
      .mockResolvedValueOnce(new Response(new Uint8Array([1]), { status: 200 }));
    const blob = await makeClient(fetchFn, getAccessToken).downloadExport();
    expect(blob.size).toBe(1);
    expect(fetchFn).toHaveBeenCalledTimes(2);
    const [, second] = fetchFn.mock.calls[1] as unknown as [string, RequestInit];
    expect((second.headers as Record<string, string>).Authorization).toBe('Bearer tok2');
  });
});

describe('ApiRequestError.details', () => {
  it('request() carries error.details from the envelope', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(409, {
        error: { code: 'conflict', message: 'x', details: { teams: [{ id: 't1', name: 'Design' }] } },
      })
    );
    const err = (await makeClient(fetchFn)
      .getSettings()
      .catch((e: unknown) => e)) as ApiRequestError;
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.details).toEqual({ teams: [{ id: 't1', name: 'Design' }] });
  });

  it('is undefined when the envelope has no details', async () => {
    const fetchFn = vi.fn(async () => jsonResponse(404, { error: { code: 'not_found', message: 'x' } }));
    const err = (await makeClient(fetchFn)
      .getSettings()
      .catch((e: unknown) => e)) as ApiRequestError;
    expect(err.details).toBeUndefined();
  });
});

describe('UserSettings.aiBackground', () => {
  it('updateSettings PUTs the switch as part of the document', async () => {
    const doc: UserSettings = { timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: false };
    const fetchFn = vi.fn(async () => jsonResponse(200, doc));
    const got = await makeClient(fetchFn).updateSettings(doc);
    expect(got.aiBackground).toBe(false);
    const [, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toMatchObject({ aiBackground: false });
  });

  it('updateSettings accepts a document without the switch and sends no aiBackground (server keeps it)', async () => {
    const stored: UserSettings = { timeZone: 'UTC', workingHours: [], workingLocation: 'Office', aiBackground: false };
    const fetchFn = vi.fn(async () => jsonResponse(200, stored));
    const update: UserSettingsUpdate = { timeZone: 'UTC', workingHours: [], workingLocation: 'Office' };
    await makeClient(fetchFn).updateSettings(update);
    const [, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string)).not.toHaveProperty('aiBackground');
  });
});
