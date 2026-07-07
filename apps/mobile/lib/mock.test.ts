import { ApiRequestError } from '@calendium/shared';
import {
  isApiUnreachable,
  isDemoMode,
  mockEvents,
  mockThreadDetail,
  mockThreadPage,
  setDemoMode,
  withMockFallback,
} from './mock';

// setDemoMode is process-global mutable state (`let demoMode` module-level),
// so reset it after every test to avoid leaking demo mode into other suites.
afterEach(() => {
  setDemoMode(false);
});

describe('isApiUnreachable', () => {
  it('is false for an ApiRequestError (a real API-level error response)', () => {
    expect(isApiUnreachable(new ApiRequestError(404, 'not_found', 'nope'))).toBe(false);
  });

  it('is true for a network-ish error (anything that is not ApiRequestError)', () => {
    expect(isApiUnreachable(new TypeError('Failed to fetch'))).toBe(true);
    expect(isApiUnreachable(new Error('network error'))).toBe(true);
  });

  it('is true for non-Error thrown values', () => {
    expect(isApiUnreachable('some string')).toBe(true);
    expect(isApiUnreachable(undefined)).toBe(true);
    expect(isApiUnreachable(null)).toBe(true);
  });
});

describe('isDemoMode / setDemoMode', () => {
  it('defaults to false', () => {
    expect(isDemoMode()).toBe(false);
  });

  it('reflects the last value passed to setDemoMode', () => {
    setDemoMode(true);
    expect(isDemoMode()).toBe(true);
    setDemoMode(false);
    expect(isDemoMode()).toBe(false);
  });
});

describe('withMockFallback', () => {
  it('outside demo mode, is a pass-through to the real request (mock never called)', async () => {
    setDemoMode(false);
    const request = jest.fn().mockResolvedValue('real-data');
    const mock = jest.fn().mockReturnValue('mock-data');

    await expect(withMockFallback(request, mock)).resolves.toBe('real-data');
    expect(request).toHaveBeenCalledTimes(1);
    expect(mock).not.toHaveBeenCalled();
  });

  it('outside demo mode, propagates the request rejection (no fabricated success)', async () => {
    setDemoMode(false);
    const error = new Error('network down');
    const request = jest.fn().mockRejectedValue(error);
    const mock = jest.fn().mockReturnValue('mock-data');

    await expect(withMockFallback(request, mock)).rejects.toBe(error);
    expect(mock).not.toHaveBeenCalled();
  });

  it('in demo mode, serves the mock value and never calls the real request', async () => {
    setDemoMode(true);
    const request = jest.fn().mockResolvedValue('real-data');
    const mock = jest.fn().mockReturnValue('mock-data');

    await expect(withMockFallback(request, mock)).resolves.toBe('mock-data');
    expect(request).not.toHaveBeenCalled();
    expect(mock).toHaveBeenCalledTimes(1);
  });
});

// Light sanity coverage on the deterministic mock-data generators — not the
// primary focus of this pass, but cheap and pure given they only depend on
// lib/format.
describe('mock data generators (sanity)', () => {
  it('mockThreadPage filters by split', () => {
    const important = mockThreadPage('important');
    expect(important.items.length).toBeGreaterThan(0);
    expect(important.items.every((t) => t.split === 'important')).toBe(true);
    expect(important.nextCursor).toBeNull();
  });

  it('mockThreadDetail throws ApiRequestError(404) for an unknown thread id', () => {
    expect(() => mockThreadDetail('does-not-exist')).toThrow(ApiRequestError);
  });

  it('mockThreadDetail returns messages for a known thread id', () => {
    const page = mockThreadPage('important');
    const known = page.items[0];
    const detail = mockThreadDetail(known.id);
    expect(detail.thread.id).toBe(known.id);
    expect(detail.messages.length).toBeGreaterThan(0);
  });

  it('mockEvents only returns events within the requested range', () => {
    const from = new Date(2026, 0, 5).toISOString(); // Monday
    const to = new Date(2026, 0, 5, 23, 59, 59).toISOString();
    const events = mockEvents(from, to);
    expect(events.length).toBeGreaterThan(0);
    for (const e of events) {
      expect(new Date(e.start).getTime()).toBeLessThanOrEqual(new Date(to).getTime());
      expect(new Date(e.end).getTime()).toBeGreaterThanOrEqual(new Date(from).getTime());
    }
  });
});
