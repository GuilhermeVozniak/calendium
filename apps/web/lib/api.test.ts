import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const suspended = vi.hoisted(() => ({ current: false }));
vi.mock('@/lib/auth-client', () => ({
  getAccessToken: async () => 'tok',
  accessTokens: undefined,
  isApiSuspended: () => suspended.current,
}));
const actingAs: { id: string | null } = { id: null };
vi.mock('@/lib/act-as', () => ({ getActingAs: () => actingAs.id }));

import { getApiClient, onPaymentRequired } from './api';

function respond(status: number, body: unknown = {}) {
  return vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

const PAYMENT_REQUIRED = { error: { code: 'payment_required', message: 'x', details: { reason: 'trial_ended' } } };

beforeEach(() => {
  actingAs.id = null;
  suspended.current = false;
});

describe('getApiClient — account deletion pause', () => {
  it('refuses every request without touching the network while the API is suspended', async () => {
    const fetchMock = respond(200, {});
    vi.stubGlobal('fetch', fetchMock);
    suspended.current = true;
    await expect(getApiClient().getMe()).rejects.toThrow(/paused/);
    expect(fetchMock).not.toHaveBeenCalled();
    suspended.current = false;
    await getApiClient().getMe();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('getApiClient — 402 notifications', () => {
  it('notifies payment-required listeners on any 402 response', async () => {
    vi.stubGlobal('fetch', respond(402, PAYMENT_REQUIRED));
    const listener = vi.fn();
    const off = onPaymentRequired(listener);
    await expect(getApiClient().getMe()).rejects.toMatchObject({ status: 402 });
    expect(listener).toHaveBeenCalledTimes(1);
    off();
  });

  it('does not notify for other statuses', async () => {
    vi.stubGlobal('fetch', respond(500, { error: { code: 'boom', message: 'x' } }));
    const listener = vi.fn();
    const off = onPaymentRequired(listener);
    await expect(getApiClient().getMe()).rejects.toMatchObject({ status: 500 });
    expect(listener).not.toHaveBeenCalled();
    off();
  });

  it('stops notifying after unsubscribe', async () => {
    vi.stubGlobal('fetch', respond(402, PAYMENT_REQUIRED));
    const listener = vi.fn();
    onPaymentRequired(listener)();
    await expect(getApiClient().getMe()).rejects.toMatchObject({ status: 402 });
    expect(listener).not.toHaveBeenCalled();
  });

  it('also notifies through the acting-as client', async () => {
    actingAs.id = 'principal-1';
    vi.stubGlobal('fetch', respond(402, PAYMENT_REQUIRED));
    const listener = vi.fn();
    const off = onPaymentRequired(listener);
    await expect(getApiClient().listThreads({})).rejects.toMatchObject({ status: 402 });
    expect(listener).toHaveBeenCalledTimes(1);
    off();
  });
});
