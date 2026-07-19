import { describe, expect, it, vi } from 'vitest';

import { ApiClient, ApiRequestError } from './client';
import type { CrmContext } from './types';

// Small standalone fetch fake (mirrors client.test.ts's harness shape) so the
// CRM surface tests own their fixtures without touching the shared file.
function makeClient(responses: Array<{ status: number; body?: unknown }>) {
  const calls: Array<{
    url: string;
    method: string;
    body?: unknown;
    headers: Record<string, string>;
  }> = [];
  let i = 0;
  const fetchFn = vi.fn(async (input: string | URL, init: RequestInit = {}) => {
    const spec = responses[Math.min(i, responses.length - 1)];
    i += 1;
    calls.push({
      url: String(input),
      method: init.method ?? 'GET',
      body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
      headers: (init.headers ?? {}) as Record<string, string>,
    });
    return new Response(spec.body === undefined ? null : JSON.stringify(spec.body), {
      status: spec.status,
    });
  });
  const client = new ApiClient({
    baseUrl: 'https://api.test',
    getAccessToken: async () => 'tok',
    fetch: fetchFn as unknown as typeof fetch,
  });
  return { client, calls };
}

const CONTEXT: CrmContext = {
  vendor: 'hubspot',
  contact: {
    id: '301',
    email: 'ada@northwind.com',
    name: 'Ada Lovelace',
    company: 'Northwind',
    title: 'CTO',
    phone: '+1 555 0100',
    owner: '7',
    vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-1/301',
  },
  deals: [
    {
      id: '9001',
      name: 'FY27 Renewal',
      stage: 'contractsent',
      amount: 1200.5,
      closeDate: '2026-08-01T00:00:00Z',
      vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-3/9001',
    },
  ],
};

describe('getCrmContext', () => {
  it('GETs /v1/crm/context with the email URL-encoded and returns the vendor contexts', async () => {
    const { client, calls } = makeClient([{ status: 200, body: [CONTEXT] }]);
    const result = await client.getCrmContext('ada+crm@northwind.com');
    expect(calls[0].method).toBe('GET');
    expect(calls[0].url).toBe('https://api.test/v1/crm/context?email=ada%2Bcrm%40northwind.com');
    expect(calls[0].headers.Authorization).toBe('Bearer tok');
    expect(result).toEqual([CONTEXT]);
  });

  it('returns an empty array untouched when no CRM is connected', async () => {
    const { client } = makeClient([{ status: 200, body: [] }]);
    await expect(client.getCrmContext('a@b.c')).resolves.toEqual([]);
  });

  it('surfaces the 501 not_implemented error when the instance has no CRM configured', async () => {
    const { client } = makeClient([
      { status: 501, body: { error: { code: 'not_implemented', message: 'nope' } } },
    ]);
    await expect(client.getCrmContext('a@b.c')).rejects.toMatchObject({
      status: 501,
      code: 'not_implemented',
    });
    await expect(client.getCrmContext('a@b.c')).rejects.toBeInstanceOf(ApiRequestError);
  });
});

describe('logCrmEmail', () => {
  it('POSTs the log payload verbatim and resolves undefined on 204', async () => {
    const { client, calls } = makeClient([{ status: 204 }]);
    const input = {
      contactEmail: 'ada@northwind.com',
      subject: 'Renewal terms',
      bodyText: 'Attached the redlines.',
      sentAt: '2026-07-18T09:30:00Z',
      direction: 'outbound' as const,
    };
    await expect(client.logCrmEmail(input)).resolves.toBeUndefined();
    expect(calls[0].method).toBe('POST');
    expect(calls[0].url).toBe('https://api.test/v1/crm/log');
    expect(calls[0].body).toEqual(input);
  });
});
