import { describe, expect, it } from 'vitest';

// Importing lib/auth constructs the Better Auth server; `db()` builds a pg
// Pool lazily (no connection), so this is safe without env vars — see
// auth.test.ts.
import { auth } from '@/lib/auth';

describe('Better Auth account deletion options', () => {
  it('enables deleteUser with before/after hooks and no email verification (piece 2 is not a dependency)', () => {
    const del = auth.options.user?.deleteUser;
    expect(del?.enabled).toBe(true);
    expect(typeof del?.beforeDelete).toBe('function');
    expect(typeof del?.afterDelete).toBe('function');
    expect((del as { sendDeleteAccountVerification?: unknown }).sendDeleteAccountVerification).toBeUndefined();
  });

  it('requires a session no older than 300 s for deleteUser on social-only accounts', () => {
    expect(auth.options.session?.freshAge).toBe(300);
  });

  it('keeps the piece-2 email/password settings alongside the deletion options', () => {
    expect(auth.options.emailAndPassword?.revokeSessionsOnPasswordReset).toBe(true);
    expect(auth.options.hooks?.before).toBeTypeOf('function');
  });
});
