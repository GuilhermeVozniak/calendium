import { describe, expect, it } from 'vitest';

import { deepLinkTo, isSafeDownloadUrl, normalizeDeepLink } from './links';

describe('isSafeDownloadUrl', () => {
  it('accepts https URLs with a host', () => {
    expect(isSafeDownloadUrl('https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0')).toBe(true);
    expect(isSafeDownloadUrl('HTTPS://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0')).toBe(true);
    expect(isSafeDownloadUrl('https://mirror.example:8443/calendium')).toBe(true);
  });

  it.each([
    'http://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0',
    'javascript:alert(1)',
    'ms-settings:privacy',
    'calendium://auth/callback?ott=x',
    'data:text/html,hi',
    'file:///etc/passwd',
    'https://',
    '/releases/tag/v1.3.0',
    'not a url',
    '',
  ])('rejects %j', (url) => {
    expect(isSafeDownloadUrl(url)).toBe(false);
  });
});

describe('normalizeDeepLink / deepLinkTo', () => {
  it('lower-cases only the calendium scheme and keeps the rest verbatim', () => {
    expect(normalizeDeepLink('CALENDIUM://auth/callback?ott=AbC')).toBe('calendium://auth/callback?ott=AbC');
    expect(normalizeDeepLink('Calendium://accounts/connected')).toBe('calendium://accounts/connected');
    expect(normalizeDeepLink('calendium://auth')).toBe('calendium://auth');
  });

  it.each(['https://calendium.app/auth', 'calendiumx://auth', 'javascript:calendium://auth', 'calendium:/auth', ''])(
    'rejects the non-calendium link %j',
    (url) => {
      expect(normalizeDeepLink(url)).toBeNull();
      expect(deepLinkTo(url, 'auth')).toBeNull();
    }
  );

  it('matches a route regardless of scheme case', () => {
    expect(deepLinkTo('CALENDIUM://auth/callback?ott=x', 'auth')).toBe('calendium://auth/callback?ott=x');
    expect(deepLinkTo('calendium://accounts/connected', 'accounts')).toBe('calendium://accounts/connected');
    expect(deepLinkTo('calendium://accounts/connected', 'auth')).toBeNull();
  });
});
