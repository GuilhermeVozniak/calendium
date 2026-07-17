import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Label } from '@calendium/shared';

import { LabelPicker } from './label-picker';

const labels: Label[] = [
  { id: 'lbl_updates', accountId: 'acc', name: 'Updates', kind: 'user', color: null },
  { id: 'lbl_travel', accountId: 'acc', name: 'Travel', kind: 'user', color: null },
];

describe('LabelPicker', () => {
  it('lists labels and picks one with add=true when not active', async () => {
    const onPick = vi.fn();
    render(
      <LabelPicker open onOpenChange={() => {}} labels={labels} activeLabelIds={new Set()} onPick={onPick} />
    );
    await userEvent.click(screen.getByText('Updates'));
    expect(onPick).toHaveBeenCalledWith(labels[0], true);
  });

  it('picks with add=false when the label is already active', async () => {
    const onPick = vi.fn();
    render(
      <LabelPicker
        open
        onOpenChange={() => {}}
        labels={labels}
        activeLabelIds={new Set(['lbl_travel'])}
        onPick={onPick}
      />
    );
    await userEvent.click(screen.getByText('Travel'));
    expect(onPick).toHaveBeenCalledWith(labels[1], false);
  });

  it('filters labels as you type', async () => {
    render(
      <LabelPicker open onOpenChange={() => {}} labels={labels} activeLabelIds={new Set()} onPick={() => {}} />
    );
    await userEvent.type(screen.getByPlaceholderText('Label as…'), 'trav');
    expect(screen.queryByText('Updates')).not.toBeInTheDocument();
    expect(screen.getByText('Travel')).toBeInTheDocument();
  });
});
