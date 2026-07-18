import { beforeEach, describe, expect, it, vi } from 'vitest';

const message = vi.fn();
vi.mock('sonner', () => ({ toast: { message: (...args: unknown[]) => message(...args) } }));

import { resetShortcutHints, teachShortcut } from './shortcut-hints';

describe('teachShortcut', () => {
  beforeEach(() => {
    message.mockClear();
    resetShortcutHints();
  });

  it('toasts the shortcut for the action', () => {
    teachShortcut('archive', 'E', 'Archive');
    expect(message).toHaveBeenCalledTimes(1);
    expect(String(message.mock.calls[0]![0])).toContain('E');
    expect(String(message.mock.calls[0]![0])).toContain('Archive');
  });

  it('fires at most once per action per session', () => {
    teachShortcut('archive', 'E', 'Archive');
    teachShortcut('archive', 'E', 'Archive');
    expect(message).toHaveBeenCalledTimes(1);
    teachShortcut('label', 'L', 'Label');
    expect(message).toHaveBeenCalledTimes(2);
  });
});
