import { describe, expect, it, vi } from 'vitest';

import { applySignature, htmlToText, onOpenCompose, openCompose, stripSignature, toHtml } from './compose';

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

describe('toHtml / htmlToText', () => {
  it('round-trips plain text through the minimal <p>...<br>...</p> shape', () => {
    const text = 'Line one\nLine two <tag> & such';
    expect(htmlToText(toHtml(text))).toBe(text);
  });

  it('returns an empty string for blank html', () => {
    expect(htmlToText('')).toBe('');
    expect(htmlToText('   ')).toBe('');
  });
});

describe('applySignature / stripSignature', () => {
  it('appends the signature below a delimiter when there is none yet', () => {
    const body = applySignature('Hey there,\n\nThanks!', toHtml('Ada Lovelace\nEngineer'));
    expect(body).toBe('Hey there,\n\nThanks!\n\n-- \nAda Lovelace\nEngineer');
  });

  it('swaps an existing signature for a new one without touching the message above it', () => {
    const withFirst = applySignature('Hi Grace,\n\nSounds good.', toHtml('Ada — Acme'));
    const withSecond = applySignature(withFirst, toHtml('Ada Lovelace — Acme Inc.'));
    expect(withSecond).toBe('Hi Grace,\n\nSounds good.\n\n-- \nAda Lovelace — Acme Inc.');
  });

  it('clears a signature when the account has none', () => {
    const withSignature = applySignature('Draft body', toHtml('Sig'));
    expect(applySignature(withSignature, '')).toBe('Draft body');
  });

  it('stripSignature removes only the trailing signature block', () => {
    const withSignature = applySignature('Body text', toHtml('Sig line'));
    expect(stripSignature(withSignature)).toBe('Body text');
  });

  it('is a no-op on a body with no signature and a blank account signature', () => {
    expect(applySignature('Plain body', '')).toBe('Plain body');
  });
});
