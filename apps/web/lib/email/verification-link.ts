/** Where every verification link lands: success, "already used" and "expired + resend" (spec: Verification). */
export const VERIFY_EMAIL_PATH = '/verify-email';

/**
 * The link to email for a verification token. Better Auth builds it with the
 * request's `callbackURL`, which on /sign-in/email is the POST-SIGN-IN
 * destination (web passes `?next=` or /mail so a successful sign-in can
 * navigate there; desktop and mobile pass none, giving `/`). The link that
 * sign-in sends to an unverified account (`sendOnSignIn`) must land on
 * /verify-email instead, or an expired link bounces through /mail to /signin
 * and the user never sees the resend state. Sign-up and resend already pass
 * their own callback and are left alone.
 */
export function verificationLink(url: string, request?: Request): string {
  if (!request) return url;
  try {
    if (!new URL(request.url).pathname.endsWith('/sign-in/email')) return url;
    const link = new URL(url);
    link.searchParams.set('callbackURL', VERIFY_EMAIL_PATH);
    return link.toString();
  } catch {
    return url;
  }
}
