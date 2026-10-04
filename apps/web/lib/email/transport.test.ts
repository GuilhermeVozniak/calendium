import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const createTransportMock = vi.hoisted(() => vi.fn());
vi.mock('nodemailer', () => ({ default: { createTransport: createTransportMock } }));

import { _resetTransportForTests, isMailConfigured, sendMail } from '@/lib/email/transport';

const SMTP_KEYS = ['SMTP_HOST', 'SMTP_PORT', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM', 'SMTP_SECURE'];
const MAIL = { to: 'ada@example.test', subject: 'Hi', html: '<p>Hi</p>', text: 'Hi' };

beforeEach(() => {
  for (const k of SMTP_KEYS) vi.stubEnv(k, '');
  _resetTransportForTests();
  createTransportMock.mockReset();
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe('transport', () => {
  it('is unconfigured without SMTP_HOST: sendMail throws and no transport is ever built', async () => {
    expect(isMailConfigured()).toBe(false);
    await expect(sendMail(MAIL)).rejects.toThrow('email is not configured (SMTP_HOST unset)');
    expect(createTransportMock).not.toHaveBeenCalled();
  });

  it('builds the transport lazily from SMTP_* and sends from SMTP_FROM', async () => {
    vi.stubEnv('SMTP_HOST', 'smtp.example.test');
    vi.stubEnv('SMTP_FROM', 'Calendium <noreply@example.test>');
    vi.stubEnv('SMTP_USER', 'u');
    vi.stubEnv('SMTP_PASS', 'p');
    const sendMailMock = vi.fn().mockResolvedValue({ messageId: 'x' });
    createTransportMock.mockReturnValue({ sendMail: sendMailMock });

    expect(isMailConfigured()).toBe(true);
    expect(createTransportMock).not.toHaveBeenCalled();

    await sendMail({ ...MAIL, replyTo: 'owner@acme.test' });
    expect(createTransportMock).toHaveBeenCalledWith({
      host: 'smtp.example.test',
      port: 587,
      secure: false,
      requireTLS: true,
      auth: { user: 'u', pass: 'p' },
    });
    expect(sendMailMock).toHaveBeenCalledWith({
      from: 'Calendium <noreply@example.test>',
      to: 'ada@example.test',
      subject: 'Hi',
      html: '<p>Hi</p>',
      text: 'Hi',
      replyTo: 'owner@acme.test',
    });

    await sendMail(MAIL);
    expect(createTransportMock).toHaveBeenCalledTimes(1);
    expect(sendMailMock.mock.calls[1]?.[0]).not.toHaveProperty('replyTo');
  });

  it('omits auth and requireTLS for an unauthenticated loopback relay', async () => {
    vi.stubEnv('SMTP_HOST', '127.0.0.1');
    vi.stubEnv('SMTP_PORT', '11025');
    vi.stubEnv('SMTP_FROM', 'noreply@example.test');
    createTransportMock.mockReturnValue({ sendMail: vi.fn().mockResolvedValue({}) });
    await sendMail(MAIL);
    expect(createTransportMock).toHaveBeenCalledWith({ host: '127.0.0.1', port: 11025, secure: false, requireTLS: false });
  });

  it('logs email.send_failed with the recipient domain only and rethrows', async () => {
    vi.stubEnv('SMTP_HOST', 'smtp.example.test');
    vi.stubEnv('SMTP_FROM', 'noreply@example.test');
    const boom = new Error('535 bad credentials');
    createTransportMock.mockReturnValue({ sendMail: vi.fn().mockRejectedValue(boom) });
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    await expect(sendMail({ ...MAIL, subject: 'Secret subject', text: 'secret body' })).rejects.toBe(boom);
    expect(errorSpy).toHaveBeenCalledWith('email.send_failed', { domain: 'example.test', error: '535 bad credentials' });
    const logged = JSON.stringify(errorSpy.mock.calls);
    expect(logged).not.toContain('secret');
    expect(logged).not.toContain('ada@');
  });
});
