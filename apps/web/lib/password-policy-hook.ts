import { APIError, createAuthMiddleware, getSessionFromCtx } from 'better-auth/api';

import { passwordPolicyError } from '@/lib/auth-env';

/**
 * The body field each password-setting endpoint actually stores. The hook
 * runs on the RAW body, before the endpoint's schema drops unknown keys, so
 * it must read exactly this field — never "whichever of password/newPassword
 * is present", which a decoy `password` next to `newPassword` would satisfy.
 */
const PASSWORD_FIELD: Record<string, 'password' | 'newPassword'> = {
  '/sign-up/email': 'password',
  '/change-password': 'newPassword',
  '/reset-password': 'newPassword',
};

/**
 * Password policy, enforced server-side on every password-setting route (the
 * pages mirror it for instant feedback). The email the policy checks against
 * comes from the request body (sign-up), the session (change-password) or the
 * reset token's verification row (reset-password; the token may arrive in
 * the body or as `?token=`, exactly as the endpoint accepts it).
 */
export const passwordPolicyHook = createAuthMiddleware(async (ctx) => {
  const field = PASSWORD_FIELD[ctx.path];
  if (!field) return;
  const body = (ctx.body ?? {}) as Record<string, unknown>;
  const candidate = body[field];
  const password = typeof candidate === 'string' ? candidate : '';
  let email: string | null = null;
  if (ctx.path === '/sign-up/email') {
    email = typeof body.email === 'string' ? body.email : null;
  } else if (ctx.path === '/change-password') {
    email = (await getSessionFromCtx(ctx))?.user.email ?? null;
  } else {
    const query = (ctx.query ?? {}) as Record<string, unknown>;
    const token = typeof body.token === 'string' && body.token ? body.token : typeof query.token === 'string' ? query.token : '';
    if (token) {
      const verification = await ctx.context.internalAdapter.findVerificationValue(`reset-password:${token}`);
      if (verification) email = (await ctx.context.internalAdapter.findUserById(verification.value))?.email ?? null;
    }
  }
  const violation = passwordPolicyError(password, email);
  if (violation) throw new APIError('BAD_REQUEST', { message: violation.message, code: violation.code });
});
