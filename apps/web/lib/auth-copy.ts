/**
 * User-facing wording for Better Auth client errors, shared by the sign-in,
 * forgot/reset/verify pages and Settings → Account so every surface says the
 * same thing for the same failure.
 */

export interface AuthClientError {
  status?: number;
  code?: string;
  message?: string;
}

export const VERIFY_FIRST_MESSAGE = 'Verify your email first — we sent a new link.';
export const EMAIL_SEND_FAILED_MESSAGE = "We couldn't send the email. Try again in a minute.";
export const RESEND_COOLDOWN_SECONDS = 60;

/** 429 copy: N comes from Better Auth's X-Retry-After header, else the 60 s window. */
export function rateLimitMessage(retryAfter: string | null | undefined): string {
  const n = Number(retryAfter);
  const seconds = Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60;
  return `Too many attempts, try again in ${seconds} s`;
}

export function describeAuthError(
  error: AuthClientError | null | undefined,
  opts: { fallback: string; retryAfter?: string | null; sendsMail?: boolean }
): string {
  if (!error) return opts.fallback;
  if (error.status === 429) return rateLimitMessage(opts.retryAfter);
  if (error.code === 'EMAIL_NOT_VERIFIED') return VERIFY_FIRST_MESSAGE;
  if (error.status === 500 && opts.sendsMail) return EMAIL_SEND_FAILED_MESSAGE;
  return error.message || opts.fallback;
}

/**
 * Better Auth's client only hands back `{status, code, message}`; the
 * X-Retry-After header is reachable through a per-call `onError` hook. Pass
 * `capture.fetchOptions` as the second argument of any auth call and read
 * `capture.value` afterwards.
 */
export function captureRetryAfter() {
  let value: string | null = null;
  return {
    fetchOptions: {
      onError: (ctx: { response: Response }) => {
        value = ctx.response.headers.get('x-retry-after');
      },
    },
    get value(): string | null {
      return value;
    },
  };
}
