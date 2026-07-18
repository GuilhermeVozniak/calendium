import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  TIME_TRAVEL_STORAGE_KEY,
  TimeTravelPicker,
  getStoredTimeTravelZone,
  setStoredTimeTravelZone,
} from '@/components/app/time-travel';

beforeEach(() => {
  window.localStorage.clear();
});

describe('getStoredTimeTravelZone / setStoredTimeTravelZone', () => {
  it('returns null when nothing is stored', () => {
    expect(getStoredTimeTravelZone()).toBeNull();
  });

  it('round-trips a valid zone through localStorage', () => {
    setStoredTimeTravelZone('Asia/Tokyo');
    expect(getStoredTimeTravelZone()).toBe('Asia/Tokyo');
    expect(window.localStorage.getItem(TIME_TRAVEL_STORAGE_KEY)).toBe('Asia/Tokyo');
  });

  it('clears the stored zone when set to null', () => {
    setStoredTimeTravelZone('Asia/Tokyo');
    setStoredTimeTravelZone(null);
    expect(getStoredTimeTravelZone()).toBeNull();
    expect(window.localStorage.getItem(TIME_TRAVEL_STORAGE_KEY)).toBeNull();
  });

  it('drops an invalid IANA zone id already in storage', () => {
    window.localStorage.setItem(TIME_TRAVEL_STORAGE_KEY, 'Not/AZone');
    expect(getStoredTimeTravelZone()).toBeNull();
  });
});

describe('TimeTravelPicker', () => {
  it('renders a "Time Travel" trigger button when inactive', () => {
    render(<TimeTravelPicker active={null} onChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: /time travel/i })).toBeInTheDocument();
    expect(screen.queryByTestId('time-travel-chip')).not.toBeInTheDocument();
  });

  it('filters the city list as the search query changes', async () => {
    const user = userEvent.setup();
    render(<TimeTravelPicker active={null} onChange={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: /time travel/i }));
    const search = await screen.findByPlaceholderText('Jump to a city…');

    await user.type(search, 'New_York');
    await waitFor(() => expect(screen.getByText('America/New York')).toBeInTheDocument());
    expect(screen.queryByText('Asia/Tokyo')).not.toBeInTheDocument();

    await user.clear(search);
    await user.type(search, 'Tokyo');
    await waitFor(() => expect(screen.getByText('Asia/Tokyo')).toBeInTheDocument());
    expect(screen.queryByText('America/New York')).not.toBeInTheDocument();
  });

  it('calls onChange with the selected zone and closes the popover', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TimeTravelPicker active={null} onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: /time travel/i }));
    const search = await screen.findByPlaceholderText('Jump to a city…');
    await user.type(search, 'Tokyo');
    await user.click(await screen.findByText('Asia/Tokyo'));

    expect(onChange).toHaveBeenCalledWith('Asia/Tokyo');
    await waitFor(() =>
      expect(screen.queryByPlaceholderText('Jump to a city…')).not.toBeInTheDocument()
    );
  });

  it('renders an exit chip with the city + GMT offset when a zone is active', () => {
    render(<TimeTravelPicker active="Asia/Tokyo" onChange={vi.fn()} />);
    const chip = screen.getByTestId('time-travel-chip');
    expect(chip).toHaveTextContent('Time Travel: Tokyo');
    expect(screen.getByRole('button', { name: 'Exit Time Travel' })).toBeInTheDocument();
  });

  it('exits Time Travel when the chip\'s exit button is clicked', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TimeTravelPicker active="Asia/Tokyo" onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: 'Exit Time Travel' }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it('clears on Escape while a zone is active', () => {
    const onChange = vi.fn();
    render(<TimeTravelPicker active="Asia/Tokyo" onChange={onChange} />);

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it('does not react to Escape when no zone is active', () => {
    const onChange = vi.fn();
    render(<TimeTravelPicker active={null} onChange={onChange} />);

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onChange).not.toHaveBeenCalled();
  });

  it('supports an externally controlled open state (for keyboard/palette triggers)', async () => {
    const onOpenChange = vi.fn();
    const { rerender } = render(
      <TimeTravelPicker active={null} onChange={vi.fn()} open={false} onOpenChange={onOpenChange} />
    );
    expect(screen.queryByPlaceholderText('Jump to a city…')).not.toBeInTheDocument();

    rerender(
      <TimeTravelPicker active={null} onChange={vi.fn()} open onOpenChange={onOpenChange} />
    );
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Jump to a city…')).toBeInTheDocument()
    );
  });
});
