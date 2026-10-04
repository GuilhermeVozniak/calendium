'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Loader2, MailCheck } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';

/**
 * Landing page of the emailed verification link. Better Auth verifies the
 * token at /api/auth/verify-email, sets the session cookie
 * (autoSignInAfterVerification) and redirects here; on a bad or expired
 * token it redirects with ?error=TOKEN_EXPIRED|INVALID_TOKEN instead.
 *
 * Success is read from the callback's result — the session it created and
 * its `emailVerified` — never assumed from the URL: a link that was already
 * used redirects here WITHOUT signing in, and a direct visit has nothing to
 * report, so both get the neutral "sign in to continue" variant.
 */
export default function VerifyEmailPage() {
  const router = useRouter();
  const { data: session, isPending } = authClient.useSession();
  const [error, setError] = React.useState<string | null | undefined>(undefined);
  const [email, setEmail] = React.useState('');
  const [resent, setResent] = React.useState(false);
  const [pending, setPending] = React.useState(false);

  React.useEffect(() => {
    setError(new URLSearchParams(window.location.search).get('error'));
  }, []);

  async function resend(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error: sendError } = await authClient.sendVerificationEmail({ email, callbackURL: '/verify-email' }, retry.fetchOptions);
      if (sendError) {
        toast.error(describeAuthError(sendError, { fallback: 'Could not send the email. Try again.', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      setResent(true);
    } finally {
      setPending(false);
    }
  }

  if (error === undefined || isPending) {
    return (
      <AuthShell title="Verifying…">
        <Loader2 className="text-muted-foreground mx-auto size-5 animate-spin" role="img" aria-label="Loading" />
      </AuthShell>
    );
  }

  if (error) {
    return (
      <AuthShell
        title="This link has expired or was already used"
        description="Verification links work once and expire after 24 hours. Enter your email to get a new one."
        focusKey={resent ? 'resent' : 'expired'}>
        {resent ? (
          <p className="text-muted-foreground text-center text-sm" role="status" aria-live="polite">
            If an account exists for that address, we sent a new link.
          </p>
        ) : (
          <form onSubmit={resend} className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">Email</Label>
              <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={pending} />
            </div>
            <Button type="submit" size="lg" className="w-full" disabled={pending}>
              {pending ? <Loader2 className="animate-spin" /> : null}
              Send a new link
            </Button>
          </form>
        )}
        <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
          Back to sign in
        </Link>
      </AuthShell>
    );
  }

  const continueButton = (
    <Button size="lg" className="w-full" onClick={() => router.replace('/mail')}>
      Continue
    </Button>
  );

  if (session?.user.emailVerified) {
    return (
      <AuthShell title="Email verified" description="Your address is confirmed and you are signed in.">
        <MailCheck className="text-muted-foreground mx-auto size-8" aria-hidden />
        {continueButton}
      </AuthShell>
    );
  }

  if (session) {
    return (
      <AuthShell title="Your email isn't verified yet" description="Open the link we emailed you to confirm your address.">
        {continueButton}
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Sign in to continue" description="This verification link was already used, or you opened this page directly. Sign in to continue.">
      <Button asChild size="lg" className="w-full">
        <Link href="/signin">Sign in</Link>
      </Button>
    </AuthShell>
  );
}
