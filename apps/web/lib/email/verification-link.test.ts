// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { VERIFY_EMAIL_PATH, verificationLink } from '@/lib/email/verification-link';

const BASE = 'https://mail.example.com/api/auth';
const link = (callbackURL: string) => `${BASE}/verify-email?token=tok.en&callbackURL=${encodeURIComponent(callbackURL)}`;
const post = (path: string) => new Request(`https://mail.example.com/api/auth${path}`, { method: 'POST' });
const callbackOf = (url: string) => new URL(url).searchParams.get('callbackURL');

describe('verificationLink', () => {
  it('a verification link sent by web sign-in (sendOnSignIn) lands on /verify-email, not the post-sign-in destination', () => {
    const out = verificationLink(link('/mail'), post('/sign-in/email'));
    expect(VERIFY_EMAIL_PATH).toBe('/verify-email');
    expect(callbackOf(out)).toBe('/verify-email');
    expect(new URL(out).searchParams.get('token')).toBe('tok.en');
    expect(out.startsWith(`${BASE}/verify-email?`)).toBe(true);
  });

  it('a ?next= destination from sign-in is replaced too', () => {
    expect(callbackOf(verificationLink(link('/calendar?view=week'), post('/sign-in/email')))).toBe('/verify-email');
  });

  it('a sign-in that sent no callbackURL (desktop, mobile) also lands on /verify-email', () => {
    expect(callbackOf(verificationLink(link('/'), post('/sign-in/email')))).toBe('/verify-email');
  });

  it('sign-up and resend keep the callbackURL their client chose', () => {
    const mobile = 'https://app.example.com/verify-email';
    expect(verificationLink(link(mobile), post('/sign-up/email'))).toBe(link(mobile));
    expect(verificationLink(link('/verify-email'), post('/send-verification-email'))).toBe(link('/verify-email'));
  });

  it('without a request (server-side auth.api call) the link is unchanged', () => {
    expect(verificationLink(link('/mail'))).toBe(link('/mail'));
  });

  it('an unparseable link is returned unchanged', () => {
    expect(verificationLink('not a url', post('/sign-in/email'))).toBe('not a url');
  });
});
