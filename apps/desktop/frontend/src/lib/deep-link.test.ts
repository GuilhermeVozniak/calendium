import { beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({
  handlers: [] as Array<(url: string) => void>,
  take: vi.fn<(route: string) => Promise<string>>(),
  order: [] as string[],
}));

vi.mock('./wails', () => ({
  desktop: {
    TakePendingDeepLink: (route: string) => {
      bridge.order.push('take');
      return bridge.take(route);
    },
  },
  onDeepLink: (handler: (url: string) => void) => {
    bridge.order.push('subscribe');
    bridge.handlers.push(handler);
    return () => {
      bridge.handlers = bridge.handlers.filter((h) => h !== handler);
    };
  },
}));

import { subscribeDeepLink } from './deep-link';

const flush = () => new Promise((r) => setTimeout(r, 0));

beforeEach(() => {
  bridge.handlers = [];
  bridge.order = [];
  bridge.take.mockReset().mockResolvedValue('');
});

describe('subscribeDeepLink', () => {
  it('subscribes to the event before taking the pending link for its route', async () => {
    subscribeDeepLink('auth', vi.fn());
    await flush();
    expect(bridge.order).toEqual(['subscribe', 'take']);
    expect(bridge.take).toHaveBeenCalledWith('auth');
  });

  it('delivers a pending cold-start link, scheme normalized', async () => {
    bridge.take.mockResolvedValue('CALENDIUM://auth/callback?ott=AbC');
    const handler = vi.fn();
    subscribeDeepLink('auth', handler);
    await flush();
    expect(handler).toHaveBeenCalledWith('calendium://auth/callback?ott=AbC');
  });

  it.each(['', 'https://evil.example/auth?ott=x', 'calendium://accounts/connected'])(
    'drops a pending %j that does not target the route',
    async (pending) => {
      bridge.take.mockResolvedValue(pending);
      const handler = vi.fn();
      subscribeDeepLink('auth', handler);
      await flush();
      expect(handler).not.toHaveBeenCalled();
    }
  );

  it('routes warm events through the same validation', () => {
    const handler = vi.fn();
    subscribeDeepLink('accounts', handler);
    for (const h of bridge.handlers) {
      h('calendium://auth/callback?ott=x');
      h('calendiumx://accounts/connected');
      h('CALENDIUM://accounts/connected?status=ok');
    }
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith('calendium://accounts/connected?status=ok');
  });

  it('ignores a pending link that resolves after unsubscribe', async () => {
    let resolve: (url: string) => void = () => {};
    bridge.take.mockReturnValue(new Promise((r) => (resolve = r)));
    const handler = vi.fn();
    const unsubscribe = subscribeDeepLink('auth', handler);
    unsubscribe();
    resolve('calendium://auth/callback?ott=x');
    await flush();
    expect(handler).not.toHaveBeenCalled();
    expect(bridge.handlers).toHaveLength(0);
  });

  it('swallows a failing binding', async () => {
    bridge.take.mockRejectedValue(new Error('binding gone'));
    const handler = vi.fn();
    subscribeDeepLink('auth', handler);
    await flush();
    expect(handler).not.toHaveBeenCalled();
  });
});
