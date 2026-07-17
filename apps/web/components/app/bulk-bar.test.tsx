import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { BulkBar } from './bulk-bar';

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
});
