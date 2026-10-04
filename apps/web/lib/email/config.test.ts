import { describe, expect, it } from 'vitest';

import { assertMailConfigForMode, isLoopback, readMailConfig } from '@/lib/email/config';

const FULL = { SMTP_HOST: 'smtp.example.test', SMTP_FROM: 'Calendium <noreply@example.test>' };
const CLOUD_ERROR =
  'SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true';

describe('readMailConfig', () => {
  it('is unconfigured when no SMTP_* identity variable is set', () => {
    expect(readMailConfig({})).toEqual({ configured: false });
    expect(readMailConfig({ SMTP_HOST: '', SMTP_FROM: '' })).toEqual({ configured: false });
    // The env templates ship port/secure pre-filled next to a blank host.
    expect(readMailConfig({ SMTP_PORT: '587', SMTP_SECURE: 'false' })).toEqual({ configured: false });
  });

  it('parses host+from with port 587, STARTTLS required off loopback, no auth', () => {
    expect(readMailConfig(FULL)).toEqual({
      configured: true,
      host: 'smtp.example.test',
      port: 587,
      secure: false,
      from: 'Calendium <noreply@example.test>',
      requireTLS: true,
    });
  });

  it('treats a blank SMTP_PORT / SMTP_SECURE as the defaults', () => {
    expect(readMailConfig({ ...FULL, SMTP_PORT: '', SMTP_SECURE: '' })).toMatchObject({ port: 587, secure: false });
  });

  it('trims whitespace around host and from', () => {
    expect(readMailConfig({ SMTP_HOST: ' smtp.example.test ', SMTP_FROM: ' a@b.test ' })).toMatchObject({
      host: 'smtp.example.test',
      from: 'a@b.test',
    });
  });

  it('does not require TLS for a loopback host', () => {
    expect(readMailConfig({ SMTP_HOST: '127.0.0.1', SMTP_FROM: 'a@b.test' })).toMatchObject({ requireTLS: false, secure: false });
    expect(readMailConfig({ SMTP_HOST: 'localhost', SMTP_FROM: 'a@b.test' })).toMatchObject({ requireTLS: false });
  });

  it('SMTP_SECURE=true means implicit TLS and no STARTTLS requirement', () => {
    expect(readMailConfig({ ...FULL, SMTP_SECURE: 'true', SMTP_PORT: '465' })).toMatchObject({ secure: true, requireTLS: false, port: 465 });
  });

  it('carries the auth pair when both are set', () => {
    expect(readMailConfig({ ...FULL, SMTP_USER: 'apikey', SMTP_PASS: 's3cret' })).toMatchObject({ user: 'apikey', pass: 's3cret' });
    expect(readMailConfig(FULL)).not.toHaveProperty('user');
  });

  it.each([
    [{ SMTP_HOST: 'h' }, 'SMTP_* is partially configured: SMTP_FROM'],
    [{ SMTP_FROM: 'a@b.test' }, 'SMTP_* is partially configured: SMTP_HOST'],
    [{ SMTP_USER: 'u' }, 'SMTP_* is partially configured: SMTP_HOST, SMTP_FROM, SMTP_PASS'],
    [{ ...FULL, SMTP_USER: 'u' }, 'SMTP_* is partially configured: SMTP_PASS'],
    [{ ...FULL, SMTP_PASS: 'p' }, 'SMTP_* is partially configured: SMTP_USER'],
    [{ ...FULL, SMTP_PORT: '0' }, 'SMTP_PORT must be an integer between 1 and 65535, got "0"'],
    [{ ...FULL, SMTP_PORT: '65536' }, 'SMTP_PORT must be an integer between 1 and 65535, got "65536"'],
    [{ ...FULL, SMTP_PORT: 'abc' }, 'SMTP_PORT must be an integer between 1 and 65535, got "abc"'],
    [{ ...FULL, SMTP_SECURE: 'yes' }, 'SMTP_SECURE must be true or false, got "yes"'],
    [{ ...FULL, SMTP_FROM: 'not-an-address' }, 'SMTP_FROM must be an email address or "Name <addr>", got "not-an-address"'],
  ])('rejects %j with %s', (env, message) => {
    expect(() => readMailConfig(env)).toThrow(message);
  });
});

describe('assertMailConfigForMode', () => {
  it('throws in cloud mode (SELF_HOSTED unset or false) without SMTP', () => {
    expect(() => assertMailConfigForMode({})).toThrow(CLOUD_ERROR);
    expect(() => assertMailConfigForMode({ SELF_HOSTED: 'false' })).toThrow(CLOUD_ERROR);
  });

  it('returns the unconfigured shape on self-host without SMTP', () => {
    expect(assertMailConfigForMode({ SELF_HOSTED: 'true' })).toEqual({ configured: false });
  });

  it('returns the parsed config in cloud mode with SMTP', () => {
    expect(assertMailConfigForMode(FULL)).toMatchObject({ configured: true, host: 'smtp.example.test' });
  });

  it('still reports a partial block before the mode check', () => {
    expect(() => assertMailConfigForMode({ SELF_HOSTED: 'true', SMTP_HOST: 'h' })).toThrow('SMTP_* is partially configured: SMTP_FROM');
  });
});

describe('isLoopback', () => {
  it.each(['localhost', 'LOCALHOST', '127.0.0.1', '::1', '[::1]'])('%s is loopback', (h) => {
    expect(isLoopback(h)).toBe(true);
  });
  it.each(['smtp.example.test', '127.0.0.1.evil', 'localhost.example', ''])('%s is not loopback', (h) => {
    expect(isLoopback(h)).toBe(false);
  });
});
