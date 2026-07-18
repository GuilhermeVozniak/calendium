import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { BulkBar } from './bulk-bar';
import { resetShortcutHints } from '@/lib/shortcut-hints';

const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    message: (...args: unknown[]) => toastMessage(...args),
  },
}));

function makeProps() {
  return {
    count: 3,
    onArchive: vi.fn(),
    onMarkRead: vi.fn(),
    onLabel: vi.fn(),
    onUnsubscribe: vi.fn(),
    onClear: vi.fn(),
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  resetShortcutHints();
});

describe('BulkBar', () => {
  it('renders nothing when the selection is empty', () => {
    const { container } = render(<BulkBar {...makeProps()} count={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the count and fires the action callbacks', async () => {
    const props = makeProps();
    render(<BulkBar {...props} />);
    expect(screen.getByText('3 selected')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /Archive/ }));
    expect(props.onArchive).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Mark read/ }));
    expect(props.onMarkRead).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Label/ }));
    expect(props.onLabel).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Clear/ }));
    expect(props.onClear).toHaveBeenCalledOnce();
  });

  it('teaches shortcut hints on button clicks', async () => {
    const props = makeProps();
    render(<BulkBar {...props} />);

    // Archive should teach with "E" key
    await userEvent.click(screen.getByRole('button', { name: /Archive/ }));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press E to Archive');

    // Second click on Archive should NOT re-toast (dedup)
    toastMessage.mockClear();
    await userEvent.click(screen.getByRole('button', { name: /Archive/ }));
    expect(toastMessage).not.toHaveBeenCalled();

    // Mark read should teach with "⇧I" key
    await userEvent.click(screen.getByRole('button', { name: /Mark read/ }));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press ⇧I to Mark read');

    // Second click on Mark read should NOT re-toast (dedup)
    toastMessage.mockClear();
    await userEvent.click(screen.getByRole('button', { name: /Mark read/ }));
    expect(toastMessage).not.toHaveBeenCalled();

    // Label should teach with "L" key
    await userEvent.click(screen.getByRole('button', { name: /Label/ }));
    expect(toastMessage).toHaveBeenCalledWith('Tip: press L to Label');

    // Second click on Label should NOT re-toast (dedup)
    toastMessage.mockClear();
    await userEvent.click(screen.getByRole('button', { name: /Label/ }));
    expect(toastMessage).not.toHaveBeenCalled();
  });
});
