import * as React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiRequestError, type Place } from '@calendium/shared';

import { LocationField } from './location-field';

const useInstanceMock = vi.fn();
vi.mock('@/lib/use-instance', () => ({
  useInstance: () => useInstanceMock(),
}));

const autocompletePlacesApiMock = vi.fn();
vi.mock('@/lib/places-data', () => ({
  autocompletePlacesApi: (...args: unknown[]) => autocompletePlacesApiMock(...args),
}));

const PLACES: Place[] = [
  {
    name: 'Alexanderplatz',
    address: 'Alexanderplatz, Mitte, Berlin, Germany',
    lat: 52.5219,
    lon: 13.4132,
  },
  { name: 'Alexandra Palace', address: 'Alexandra Palace, London, UK', lat: 51.594, lon: -0.13 },
];

function withMaps(maps: boolean | 'loading') {
  useInstanceMock.mockReturnValue(
    maps === 'loading' ? { data: undefined } : { data: { features: { maps } } }
  );
}

/** Stateful wrapper: LocationField is controlled by its parent. */
function Harness({
  onChange = () => {},
  initial = '',
}: {
  onChange?: (value: string, coords: { lat: number; lon: number } | null) => void;
  initial?: string;
}) {
  const [value, setValue] = React.useState(initial);
  return (
    <LocationField
      value={value}
      debounceMs={0}
      onChange={(next, coords) => {
        setValue(next);
        onChange(next, coords);
      }}
    />
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  autocompletePlacesApiMock.mockResolvedValue(PLACES);
});

describe('LocationField', () => {
  it('renders suggestions for a query and picking one reports its coordinates', async () => {
    withMaps(true);
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);

    fireEvent.change(screen.getByLabelText('Location'), { target: { value: 'Alexander' } });

    expect(await screen.findByText('Alexanderplatz')).toBeInTheDocument();
    expect(screen.getByText('Alexandra Palace')).toBeInTheDocument();
    expect(autocompletePlacesApiMock).toHaveBeenCalledWith('Alexander');

    await userEvent.click(screen.getByText('Alexanderplatz'));
    expect(onChange).toHaveBeenLastCalledWith('Alexanderplatz, Mitte, Berlin, Germany', {
      lat: 52.5219,
      lon: 13.4132,
    });
    // Picking fills the input and closes the list.
    expect(screen.getByLabelText('Location')).toHaveValue(
      'Alexanderplatz, Mitte, Berlin, Germany'
    );
    await waitFor(() =>
      expect(screen.queryByText('Alexandra Palace')).not.toBeInTheDocument()
    );
  });

  it('reports null coordinates for free-typed text', async () => {
    withMaps(true);
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    fireEvent.change(screen.getByLabelText('Location'), {
      target: { value: 'that nice place' },
    });
    expect(onChange).toHaveBeenLastCalledWith('that nice place', null);
  });

  it('never queries for under-length input', async () => {
    withMaps(true);
    render(<Harness />);
    fireEvent.change(screen.getByLabelText('Location'), { target: { value: 'Al' } });
    await new Promise((r) => setTimeout(r, 20));
    expect(autocompletePlacesApiMock).not.toHaveBeenCalled();
  });

  it('degrades to a plain input when the instance does not advertise maps', async () => {
    withMaps(false);
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);

    const input = screen.getByLabelText('Location');
    fireEvent.change(input, { target: { value: 'Alexanderplatz' } });
    expect(onChange).toHaveBeenLastCalledWith('Alexanderplatz', null);
    await new Promise((r) => setTimeout(r, 20));
    expect(autocompletePlacesApiMock).not.toHaveBeenCalled();
  });

  it('degrades to a plain input while instance discovery is still loading', () => {
    withMaps('loading');
    render(<Harness />);
    expect(screen.getByLabelText('Location')).toBeInTheDocument();
  });

  it('stops querying for good after the server answers 501', async () => {
    withMaps(true);
    autocompletePlacesApiMock.mockRejectedValue(
      new ApiRequestError(501, 'not_implemented', 'maps are not available on this instance')
    );
    render(<Harness />);

    fireEvent.change(screen.getByLabelText('Location'), { target: { value: 'Alexander' } });
    await waitFor(() => expect(autocompletePlacesApiMock).toHaveBeenCalled());

    // Still usable as a plain input, with no further vendor traffic.
    autocompletePlacesApiMock.mockClear();
    fireEvent.change(screen.getByLabelText('Location'), {
      target: { value: 'Alexanderplatz Berlin' },
    });
    await new Promise((r) => setTimeout(r, 20));
    expect(autocompletePlacesApiMock).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Location')).toHaveValue('Alexanderplatz Berlin');
  });

  it('keeps working as a text field after a transient network failure', async () => {
    withMaps(true);
    autocompletePlacesApiMock.mockRejectedValue(new Error('offline'));
    render(<Harness />);
    fireEvent.change(screen.getByLabelText('Location'), { target: { value: 'Alexander' } });
    await waitFor(() => expect(autocompletePlacesApiMock).toHaveBeenCalled());
    expect(screen.queryByText('Alexanderplatz')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Location')).toHaveValue('Alexander');
  });
});
