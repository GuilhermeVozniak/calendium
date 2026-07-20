import { beforeEach, describe, expect, it, vi } from 'vitest';

const signOutMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  signOut: (...args: unknown[]) => signOutMock(...args),
}));

const clearActingAsMock = vi.fn();
vi.mock('@/lib/act-as', () => ({
  clearActingAs: () => clearActingAsMock(),
}));

const clearOfflineStateMock = vi.fn();
vi.mock('@/lib/offline/queue', () => ({
  clearOfflineState: () => clearOfflineStateMock(),
}));

const clearTourStateMock = vi.fn();
vi.mock('@/lib/tour-state', () => ({
  clearTourState: () => clearTourStateMock(),
}));

import { performSignOut } from './sign-out';

describe('performSignOut', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('ends the session, clears acting-as, tour state, and offline state', async () => {
    await performSignOut();
    expect(signOutMock).toHaveBeenCalledTimes(1);
    expect(clearActingAsMock).toHaveBeenCalledTimes(1);
    expect(clearTourStateMock).toHaveBeenCalledTimes(1);
    expect(clearOfflineStateMock).toHaveBeenCalledTimes(1);
  });

  it('signs out before scrubbing local state', async () => {
    const order: string[] = [];
    signOutMock.mockImplementation(() => order.push('signOut'));
    clearActingAsMock.mockImplementation(() => order.push('clearActingAs'));
    clearTourStateMock.mockImplementation(() => order.push('clearTourState'));
    clearOfflineStateMock.mockImplementation(() => order.push('clearOfflineState'));
    await performSignOut();
    expect(order).toEqual(['signOut', 'clearActingAs', 'clearTourState', 'clearOfflineState']);
  });
});
