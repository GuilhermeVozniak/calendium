import { describe, expect, it } from 'vitest';
import { buildInstantIntro } from './mail-transforms';
import type { EmailAddress, Message, Thread } from './types';

function addr(email: string, name: string | null = null): EmailAddress {
  return { email, name };
}

function makeThread(overrides: Partial<Thread> = {}): Thread {
  return {
    id: 'thread_1',
    accountId: 'acc_1',
    subject: 'Introduction',
    snippet: '',
    participants: [],
    labelIds: [],
    split: 'important',
    messageCount: 1,
    unread: false,
    starred: false,
    lastMessageAt: '2026-07-19T09:00:00Z',
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
    ...overrides,
  };
}

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg_1',
    threadId: 'thread_1',
    accountId: 'acc_1',
    from: addr('alice@example.com', 'Alice Smith'),
    to: [],
    cc: [],
    bcc: [],
    subject: 'Introduction',
    bodyHtml: '<p>Bob, meet Carol!</p>',
    bodyText: 'Bob, meet Carol!',
    attachments: [],
    sentAt: '2026-07-19T09:00:00Z',
    isDraft: false,
    openedAt: null,
    reactions: [],
    ...overrides,
  };
}

const ME = 'bob@example.com';

describe('buildInstantIntro', () => {
  it('classic 3-party intro: reply-all thanking the introducer, moving them to BCC', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com', 'Bob Jones')],
      cc: [addr('carol@example.com', 'Carol White')],
      subject: 'Introduction',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result).toEqual({
      to: [addr('carol@example.com', 'Carol White')],
      bcc: [addr('alice@example.com', 'Alice Smith')],
      subject: 'Re: Introduction',
      body: 'Thanks Alice! (moving you to BCC)\n\n',
      threadId: 'thread_1',
    });
  });

  it('CC-only introducer: third party found even when reached via cc, not to', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('carol@example.com', 'Carol White')],
      cc: [addr('bob@example.com', 'Bob Jones')],
      subject: 'Introduction',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result).not.toBeNull();
    expect(result?.to).toEqual([addr('carol@example.com', 'Carol White')]);
    expect(result?.bcc).toEqual([addr('alice@example.com', 'Alice Smith')]);
  });

  it('dedupes recipients case-insensitively across from/to/cc', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('Carol@Example.com', 'Carol White')],
      cc: [addr('carol@example.com', 'Carol Dup'), addr('CAROL@EXAMPLE.COM')],
      subject: 'Introduction',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result?.to).toEqual([addr('Carol@Example.com', 'Carol White')]);
  });

  it('"Re:" idempotence: does not double-prefix an already-"Re:" subject', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
      subject: 'Re: Introduction',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result?.subject).toBe('Re: Introduction');
  });

  it('"Re:" idempotence is case-insensitive ("RE:" is treated as already prefixed)', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
      subject: 'RE: Introduction',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result?.subject).toBe('RE: Introduction');
  });

  it('returns null when I sent the latest message', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
      sentAt: '2026-07-19T09:00:00Z',
    });
    const myReply = makeMessage({
      id: 'msg_2',
      from: addr('bob@example.com', 'Bob Jones'),
      to: [addr('alice@example.com'), addr('carol@example.com')],
      sentAt: '2026-07-19T10:00:00Z',
    });

    const result = buildInstantIntro(thread, [intro, myReply], ME);

    expect(result).toBeNull();
  });

  it('returns null for a two-party thread (only the introducer and me — no third participant)', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com')],
      cc: [],
      subject: 'Just us',
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result).toBeNull();
  });

  it('returns null when there are no messages', () => {
    const thread = makeThread();

    const result = buildInstantIntro(thread, [], ME);

    expect(result).toBeNull();
  });

  it('name fallback: uses the email local part when the introducer has no display name', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', null),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result?.body).toBe('Thanks alice! (moving you to BCC)\n\n');
  });

  it('first name is the text before the first space of a multi-word display name', () => {
    const thread = makeThread();
    const intro = makeMessage({
      from: addr('alice@example.com', 'Alice van der Berg'),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
    });

    const result = buildInstantIntro(thread, [intro], ME);

    expect(result?.body).toBe('Thanks Alice! (moving you to BCC)\n\n');
  });

  it('picks the chronologically latest message regardless of array order', () => {
    const thread = makeThread();
    const older = makeMessage({
      id: 'msg_older',
      from: addr('bob@example.com', 'Bob Jones'),
      to: [addr('alice@example.com')],
      sentAt: '2026-07-19T08:00:00Z',
    });
    const latest = makeMessage({
      id: 'msg_latest',
      from: addr('alice@example.com', 'Alice Smith'),
      to: [addr('bob@example.com')],
      cc: [addr('carol@example.com')],
      sentAt: '2026-07-19T09:00:00Z',
    });

    const result = buildInstantIntro(thread, [latest, older], ME);

    expect(result?.bcc).toEqual([addr('alice@example.com', 'Alice Smith')]);
    expect(result?.to).toEqual([addr('carol@example.com')]);
  });
});
