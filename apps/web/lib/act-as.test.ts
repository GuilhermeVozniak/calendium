import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ACT_AS_STORAGE_KEY,
  clearActingAs,
  getActingAs,
  setActingAs,
  subscribeActingAs,
} from './act-as';

afterEach(() => {
  clearActingAs();
});

describe('act-as store', () => {
  it('defaults to not acting', () => {
    expect(getActingAs()).toBeNull();
  });

  it('setActingAs persists the principal id and notifies subscribers', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeActingAs(listener);
    setActingAs('user_principal');
    expect(getActingAs()).toBe('user_principal');
    expect(window.localStorage.getItem(ACT_AS_STORAGE_KEY)).toBe('user_principal');
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it('clearActingAs removes the persisted key entirely (sign-out contract)', () => {
    setActingAs('user_principal');
    clearActingAs();
    expect(getActingAs()).toBeNull();
    expect(window.localStorage.getItem(ACT_AS_STORAGE_KEY)).toBeNull();
  });

  it('unsubscribed listeners are not notified again', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeActingAs(listener);
    unsubscribe();
    setActingAs('user_principal');
    expect(listener).not.toHaveBeenCalled();
  });
});
