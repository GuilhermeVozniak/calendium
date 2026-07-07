import { describe, expect, it, vi } from 'vitest';

import { onOpenCompose, openCompose } from './compose';

describe('openCompose / onOpenCompose', () => {
  it('delivers the default "new" intent to a subscriber', () => {
    const handler = vi.fn();
    const unsubscribe = onOpenCompose(handler);
    openCompose();
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith({ kind: 'new' });
    unsubscribe();
  });

  it('delivers an explicit reply intent with its thread/message payload', () => {
    const handler = vi.fn();
    const unsubscribe = onOpenCompose(handler);
    const intent = {
      kind: 'reply' as const,
      thread: { id: 'thr_1' } as unknown as import('@calendium/shared').Thread,
      message: { id: 'msg_1' } as unknown as import('@calendium/shared').Message,
    };
    openCompose(intent);
    expect(handler).toHaveBeenCalledWith(intent);
    unsubscribe();
  });

  it('delivers to multiple subscribers', () => {
    const a = vi.fn();
    const b = vi.fn();
    const unsubA = onOpenCompose(a);
    const unsubB = onOpenCompose(b);
    openCompose();
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    unsubA();
    unsubB();
  });

  it('stops delivering after unsubscribe', () => {
    const handler = vi.fn();
    const unsubscribe = onOpenCompose(handler);
    unsubscribe();
    openCompose();
    expect(handler).not.toHaveBeenCalled();
  });

  it('does not notify a handler unsubscribed by another handler running first', () => {
    const later = vi.fn();
    const early = vi.fn();
    const unsubLater = onOpenCompose(later);
    onOpenCompose(early);
    openCompose();
    expect(early).toHaveBeenCalledTimes(1);
    expect(later).toHaveBeenCalledTimes(1);
    unsubLater();
    early.mockClear();
    later.mockClear();
    openCompose();
    expect(early).toHaveBeenCalledTimes(1);
    expect(later).not.toHaveBeenCalled();
  });
});
