const mockGetBetterAuthToken = jest.fn();
jest.mock('@/lib/auth-client', () => ({
  getBetterAuthToken: (...args: unknown[]) => mockGetBetterAuthToken(...args),
}));

import { api, configureApi, onPaymentRequired } from './api';

const mockFetch = jest.fn();
(global as unknown as { fetch: typeof fetch }).fetch = mockFetch as unknown as typeof fetch;

beforeEach(() => {
  mockGetBetterAuthToken.mockReset();
  mockGetBetterAuthToken.mockResolvedValue(null);
  mockFetch.mockReset();
  mockFetch.mockResolvedValue({ status: 200, ok: true, json: async () => ({}) });
});

afterEach(() => {
  // configureApi mutates module-scoped state; drive it back to the default
  // base URL so other tests in this file (and in this run) start clean.
  configureApi('');
});

describe('default configuration', () => {
  it('defaults the base URL to http://localhost:8080 when EXPO_PUBLIC_API_URL is unset', async () => {
    await api.getInstance();
    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8080/v1/instance',
      expect.anything()
    );
  });

  it('resolves the access token via getBetterAuthToken() from @/lib/auth-client', async () => {
    mockGetBetterAuthToken.mockResolvedValue('the-jwt');
    await api.getMe();

    expect(mockGetBetterAuthToken).toHaveBeenCalledTimes(1);
    const [, init] = mockFetch.mock.calls[0];
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer the-jwt');
  });

  it('omits the Authorization header when there is no token', async () => {
    mockGetBetterAuthToken.mockResolvedValue(null);
    await api.getMe();

    const [, init] = mockFetch.mock.calls[0];
    expect(init.headers as Record<string, string>).not.toHaveProperty('Authorization');
  });
});

describe('configureApi', () => {
  it('re-points every subsequent request at the new base URL', async () => {
    configureApi('https://self-hosted.example.com');
    await api.getInstance();
    expect(mockFetch).toHaveBeenCalledWith(
      'https://self-hosted.example.com/v1/instance',
      expect.anything()
    );
  });

  it('strips trailing slashes from the configured base URL', async () => {
    configureApi('https://self-hosted.example.com///');
    await api.getMe();
    const [url] = mockFetch.mock.calls[0];
    expect(url).toBe('https://self-hosted.example.com/v1/me');
  });

  it('falls back to the default base URL when given an empty string', async () => {
    configureApi('https://self-hosted.example.com');
    configureApi('');
    await api.getInstance();
    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8080/v1/instance',
      expect.anything()
    );
  });

  it('keeps the shared client pointed at the global fetch', async () => {
    await api.getInstance();
    expect(mockFetch).toHaveBeenCalledTimes(1);
  });

  it('mutates the single shared client in place (same `api` singleton, new target)', async () => {
    configureApi('https://one.example.com');
    await api.getMe();
    expect(mockFetch.mock.calls[0][0]).toBe('https://one.example.com/v1/me');

    configureApi('https://two.example.com');
    await api.getMe();
    expect(mockFetch.mock.calls[1][0]).toBe('https://two.example.com/v1/me');
  });
});

// Mid-session lapse (mirrors apps/web/lib/api.ts): every 402 the shared client
// receives is reported so the tabs billing gate re-checks the subscription.
describe('onPaymentRequired', () => {
  const paymentRequired = {
    status: 402,
    ok: false,
    json: async () => ({ error: { code: 'payment_required', message: 'x', details: { reason: 'trial_ended' } } }),
  };

  it('notifies listeners on any 402 response', async () => {
    mockFetch.mockResolvedValue(paymentRequired);
    const listener = jest.fn();
    const off = onPaymentRequired(listener);
    await expect(api.getMe()).rejects.toMatchObject({ status: 402 });
    expect(listener).toHaveBeenCalledTimes(1);
    off();
  });

  it('stays quiet for other statuses and after unsubscribing', async () => {
    const listener = jest.fn();
    const off = onPaymentRequired(listener);
    await api.getMe();
    off();
    mockFetch.mockResolvedValue(paymentRequired);
    await expect(api.getMe()).rejects.toMatchObject({ status: 402 });
    expect(listener).not.toHaveBeenCalled();
  });
});
