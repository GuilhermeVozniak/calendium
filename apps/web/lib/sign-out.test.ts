import { beforeEach, describe, expect, it, vi } from 'vitest';

const signOutMock = vi.fn();
const invalidateMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  signOut: (...args: unknown[]) => signOutMock(...args),
  invalidateAccessToken: () => invalidateMock(),
}));

const clearActingAsMock = vi.fn();
vi.mock('@/lib/act-as', () => ({
  clearActingAs: () => clearActingAsMock(),
}));

const clearOfflineStateMock = vi.fn();
vi.mock('@/lib/offline/queue', () => ({
  clearOfflineState: () => clearOfflineStateMock(),
}));

const resetTourSessionMock = vi.fn();
vi.mock('@/lib/tour-state', () => ({
  resetTourSession: () => resetTourSessionMock(),
}));

import { performSignOut } from './sign-out';

describe('performSignOut', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('ends the session, clears acting-as, detaches tour scope, and clears offline state', async () => {
    await performSignOut();
    expect(signOutMock).toHaveBeenCalledTimes(1);
    expect(invalidateMock).toHaveBeenCalledTimes(1);
    expect(clearActingAsMock).toHaveBeenCalledTimes(1);
    expect(resetTourSessionMock).toHaveBeenCalledTimes(1);
    expect(clearOfflineStateMock).toHaveBeenCalledTimes(1);
  });

  it('signs out before scrubbing local state', async () => {
    const order: string[] = [];
    signOutMock.mockImplementation(() => order.push('signOut'));
    clearActingAsMock.mockImplementation(() => order.push('clearActingAs'));
    resetTourSessionMock.mockImplementation(() => order.push('resetTourSession'));
    clearOfflineStateMock.mockImplementation(() => order.push('clearOfflineState'));
    await performSignOut();
    expect(order).toEqual(['signOut', 'clearActingAs', 'resetTourSession', 'clearOfflineState']);
  });
});
