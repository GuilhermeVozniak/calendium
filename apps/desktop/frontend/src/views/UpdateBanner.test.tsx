import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({
  GetUpdateStatus: vi.fn(),
  OpenExternal: vi.fn(),
  listeners: [] as Array<(info: unknown) => void>,
}));

vi.mock('@/lib/wails', () => ({
  desktop: { GetUpdateStatus: bridge.GetUpdateStatus, OpenExternal: bridge.OpenExternal },
  onUpdateAvailable: (handler: (info: unknown) => void) => {
    bridge.listeners.push(handler);
    return () => {
      bridge.listeners = bridge.listeners.filter((h) => h !== handler);
    };
  },
}));

import { UpdateBanner } from './UpdateBanner';

const NONE = { available: false, current: '1.2.3', latest: '', url: '' };
const NEWER = {
  available: true,
  current: '1.2.3',
  latest: '1.3.0',
  url: 'https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0',
};

async function flush() {
  await act(async () => {});
}

beforeEach(() => {
  localStorage.clear();
  bridge.listeners = [];
  bridge.GetUpdateStatus.mockReset().mockResolvedValue(NONE);
  bridge.OpenExternal.mockReset();
});

describe('UpdateBanner', () => {
  it('renders nothing while no update is available', async () => {
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('shows the newer version from GetUpdateStatus (late mount) and Download opens the release', async () => {
    bridge.GetUpdateStatus.mockResolvedValue(NEWER);
    render(<UpdateBanner />);
    await flush();
    expect(screen.getByRole('status').textContent).toContain('Calendium 1.3.0 is available');
    await userEvent.click(screen.getByRole('button', { name: /download/i }));
    expect(bridge.OpenExternal).toHaveBeenCalledWith(NEWER.url);
  });

  it('shows when the host emits update-available after mount', async () => {
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
    act(() => {
      for (const l of bridge.listeners) l(NEWER);
    });
    expect(screen.getByRole('status').textContent).toContain('1.3.0');
  });

  it('Later hides the banner and persists the dismissed version', async () => {
    bridge.GetUpdateStatus.mockResolvedValue(NEWER);
    const first = render(<UpdateBanner />);
    await flush();
    await userEvent.click(screen.getByRole('button', { name: /later/i }));
    expect(screen.queryByRole('status')).toBeNull();
    expect(localStorage.getItem('calendium.update.dismissed')).toBe('1.3.0');
    first.unmount();
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('a newer version than the dismissed one re-shows', async () => {
    localStorage.setItem('calendium.update.dismissed', '1.3.0');
    bridge.GetUpdateStatus.mockResolvedValue({ ...NEWER, latest: '1.4.0' });
    render(<UpdateBanner />);
    await flush();
    expect(screen.getByRole('status').textContent).toContain('Calendium 1.4.0 is available');
  });

  it.each([
    'http://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0',
    'javascript:alert(1)',
    'ms-settings:privacy',
    'https://',
    '/releases/tag/v1.3.0',
  ])('never shows or opens a non-https Download URL (%j)', async (url) => {
    bridge.GetUpdateStatus.mockResolvedValue({ ...NEWER, url });
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
    act(() => {
      for (const l of bridge.listeners) l({ ...NEWER, url });
    });
    expect(screen.queryByRole('status')).toBeNull();
    expect(bridge.OpenExternal).not.toHaveBeenCalled();
  });

  it('a failing GetUpdateStatus leaves the banner hidden without an unhandled rejection', async () => {
    const unhandled = vi.fn();
    process.on('unhandledRejection', unhandled);
    try {
      bridge.GetUpdateStatus.mockRejectedValue(new Error('binding gone'));
      render(<UpdateBanner />);
      await flush();
      await new Promise((r) => setTimeout(r, 0));
      expect(screen.queryByRole('status')).toBeNull();
      expect(unhandled).not.toHaveBeenCalled();
    } finally {
      process.off('unhandledRejection', unhandled);
    }
  });

  it('unsubscribes on unmount', async () => {
    const view = render(<UpdateBanner />);
    await flush();
    expect(bridge.listeners).toHaveLength(1);
    view.unmount();
    expect(bridge.listeners).toHaveLength(0);
  });
});
