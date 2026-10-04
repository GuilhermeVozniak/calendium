import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/lib/auth-client', () => ({ getAccessToken: async () => 'tok' }));
const actingAs: { id: string | null } = { id: null };
vi.mock('@/lib/act-as', () => ({ getActingAs: () => actingAs.id }));

import { getApiClient, onPaymentRequired } from './api';

function respond(status: number, body: unknown = {}) {
  return vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

const PAYMENT_REQUIRED = { error: { code: 'payment_required', message: 'x', details: { reason: 'trial_ended' } } };

beforeEach(() => {
  actingAs.id = null;
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
