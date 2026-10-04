import { describe, expect, it } from 'vitest';

import {
  EMAIL_SEND_FAILED_MESSAGE,
  VERIFY_FIRST_MESSAGE,
  captureRetryAfter,
  describeAuthError,
  rateLimitMessage,
} from '@/lib/auth-copy';

describe('rateLimitMessage', () => {
  it('uses the X-Retry-After value when present and 60 s otherwise', () => {
    expect(rateLimitMessage('42')).toBe('Too many attempts, try again in 42 s');
    expect(rateLimitMessage('0.4')).toBe('Too many attempts, try again in 1 s');
    expect(rateLimitMessage(null)).toBe('Too many attempts, try again in 60 s');
    expect(rateLimitMessage('soon')).toBe('Too many attempts, try again in 60 s');
  });
});

describe('describeAuthError', () => {
  it('maps 429, EMAIL_NOT_VERIFIED and mail-route 500s, else uses the server message or the fallback', () => {
    expect(describeAuthError({ status: 429 }, { fallback: 'f', retryAfter: '7' })).toBe('Too many attempts, try again in 7 s');
    expect(describeAuthError({ status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' }, { fallback: 'f' })).toBe(VERIFY_FIRST_MESSAGE);
    expect(describeAuthError({ status: 500, message: 'Internal' }, { fallback: 'f', sendsMail: true })).toBe(EMAIL_SEND_FAILED_MESSAGE);
    expect(describeAuthError({ status: 500, message: 'Internal' }, { fallback: 'f' })).toBe('Internal');
    expect(describeAuthError({ status: 400, message: 'Password must not contain your email address.' }, { fallback: 'f' })).toBe('Password must not contain your email address.');
    expect(describeAuthError({ status: 400 }, { fallback: 'f' })).toBe('f');
    expect(describeAuthError(null, { fallback: 'f' })).toBe('f');
  });
});

describe('captureRetryAfter', () => {
  it('records the header from a Better Auth onError context', () => {
    const capture = captureRetryAfter();
    expect(capture.value).toBeNull();
    capture.fetchOptions.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '12' } }) });
    expect(capture.value).toBe('12');
  });
});
