// --- Mocks -------------------------------------------------------------
const mockCreateAuthClient = jest.fn();
jest.mock('better-auth/react', () => ({
  createAuthClient: (...args: unknown[]) => mockCreateAuthClient(...args),
}));

const mockExpoClient = jest.fn();
jest.mock('@better-auth/expo/client', () => ({
  expoClient: (...args: unknown[]) => mockExpoClient(...args),
}));

jest.mock('expo-secure-store', () => ({ __esModule: true, default: { SECURE_STORE_MOCK: true } }));

import {
  configureAuthClient,
  getAuthClient,
  getBetterAuthToken,
} from './auth-client';

const mockFetch = jest.fn();
(global as unknown as { fetch: typeof fetch }).fetch = mockFetch as unknown as typeof fetch;

/** A fresh fake Better Auth client instance, distinguishable by reference. */
function fakeClient(getCookie: () => string = () => '') {
  return { getCookie, __marker: Symbol('client') };
}

beforeEach(() => {
  mockCreateAuthClient.mockReset();
  mockExpoClient.mockReset();
  mockExpoClient.mockReturnValue({ __plugin: 'expo' });
  mockFetch.mockReset();
});

afterEach(() => {
  // Module-level `client`/`currentBaseUrl` state isn't exported for reset, so
  // drive it back to the initial (unconfigured) state via the public API.
  configureAuthClient('');
});

describe('configureAuthClient', () => {
  it('builds a new client via createAuthClient with the given baseURL', () => {
    const built = fakeClient();
    mockCreateAuthClient.mockReturnValue(built);

    const result = configureAuthClient('https://example.com/api/auth');

    expect(result).toBe(built);
    expect(getAuthClient()).toBe(built);
    expect(mockCreateAuthClient).toHaveBeenCalledTimes(1);
    expect(mockCreateAuthClient).toHaveBeenCalledWith(
      expect.objectContaining({ baseURL: 'https://example.com/api/auth' })
    );
  });

  it('configures the expo client plugin with the app scheme, storage prefix, and SecureStore', () => {
    mockCreateAuthClient.mockReturnValue(fakeClient());

    configureAuthClient('https://example.com/api/auth');

    expect(mockExpoClient).toHaveBeenCalledWith(
      expect.objectContaining({
        scheme: 'calendium',
        storagePrefix: 'calendium',
      })
    );
  });

  it('strips trailing slashes from the base URL before building', () => {
    mockCreateAuthClient.mockReturnValue(fakeClient());

    configureAuthClient('https://example.com/api/auth///');

    expect(mockCreateAuthClient).toHaveBeenCalledWith(
      expect.objectContaining({ baseURL: 'https://example.com/api/auth' })
    );
  });

  it('is a no-op (does not rebuild) when called again with the same base URL', () => {
    const built = fakeClient();
    mockCreateAuthClient.mockReturnValue(built);

    const first = configureAuthClient('https://example.com/api/auth');
    const second = configureAuthClient('https://example.com/api/auth');

    expect(first).toBe(built);
    expect(second).toBe(built);
    expect(mockCreateAuthClient).toHaveBeenCalledTimes(1);
  });

  it('treats a URL differing only by a trailing slash as unchanged (no rebuild)', () => {
    const built = fakeClient();
    mockCreateAuthClient.mockReturnValue(built);

    configureAuthClient('https://example.com/api/auth');
    configureAuthClient('https://example.com/api/auth/');

    expect(mockCreateAuthClient).toHaveBeenCalledTimes(1);
  });

  it('rebuilds when the base URL actually changes', () => {
    const first = fakeClient();
    const second = fakeClient();
    mockCreateAuthClient.mockReturnValueOnce(first).mockReturnValueOnce(second);

    const r1 = configureAuthClient('https://one.example.com/api/auth');
    const r2 = configureAuthClient('https://two.example.com/api/auth');

    expect(r1).toBe(first);
    expect(r2).toBe(second);
    expect(mockCreateAuthClient).toHaveBeenCalledTimes(2);
  });

  it('returns null and clears the client for an empty base URL', () => {
    mockCreateAuthClient.mockReturnValue(fakeClient());
    configureAuthClient('https://example.com/api/auth');

    const result = configureAuthClient('');

    expect(result).toBeNull();
    expect(getAuthClient()).toBeNull();
  });
});

describe('getBetterAuthToken', () => {
  it('returns null when no server has been configured', async () => {
    await expect(getBetterAuthToken()).resolves.toBeNull();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('mints a token: GETs {authBaseUrl}/token with Accept + Cookie headers, returns the token', async () => {
    mockCreateAuthClient.mockReturnValue(fakeClient(() => 'session=abc123'));
    configureAuthClient('https://example.com/api/auth');
    mockFetch.mockResolvedValue({ ok: true, json: async () => ({ token: 'jwt-token-value' }) });

    const token = await getBetterAuthToken();

    expect(token).toBe('jwt-token-value');
    expect(mockFetch).toHaveBeenCalledWith(
      'https://example.com/api/auth/token',
      expect.objectContaining({
        headers: expect.objectContaining({
          accept: 'application/json',
          Cookie: 'session=abc123',
        }),
      })
    );
  });

  it('omits the Cookie header when getCookie returns an empty string', async () => {
    mockCreateAuthClient.mockReturnValue(fakeClient(() => ''));
    configureAuthClient('https://example.com/api/auth');
    mockFetch.mockResolvedValue({ ok: true, json: async () => ({ token: 't' }) });

    await getBetterAuthToken();

    const headers = mockFetch.mock.calls[0][1].headers as Record<string, string>;
    expect(headers).not.toHaveProperty('Cookie');
    expect(headers.accept).toBe('application/json');
  });

  it('returns null when the response is not ok', async () => {
    mockCreateAuthClient.mockReturnValue(fakeClient(() => 'session=abc'));
    configureAuthClient('https://example.com/api/auth');
    mockFetch.mockResolvedValue({ ok: false, json: async () => ({ token: 'nope' }) });

    await expect(getBetterAuthToken()).resolves.toBeNull();
  });

  it('returns null when the JSON body has no token field', async () => {
    mockCreateAuthClient.mockReturnValue(fakeClient(() => 'session=abc'));
    configureAuthClient('https://example.com/api/auth');
    mockFetch.mockResolvedValue({ ok: true, json: async () => ({}) });

    await expect(getBetterAuthToken()).resolves.toBeNull();
  });

  it('returns null (rather than throwing) when fetch rejects', async () => {
    mockCreateAuthClient.mockReturnValue(fakeClient(() => 'session=abc'));
    configureAuthClient('https://example.com/api/auth');
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));

    await expect(getBetterAuthToken()).resolves.toBeNull();
  });

  it('returns null (rather than throwing) when getCookie itself throws', async () => {
    mockCreateAuthClient.mockReturnValue(
      fakeClient(() => {
        throw new Error('no cookie jar');
      })
    );
    configureAuthClient('https://example.com/api/auth');

    await expect(getBetterAuthToken()).resolves.toBeNull();
    expect(mockFetch).not.toHaveBeenCalled();
  });
});
