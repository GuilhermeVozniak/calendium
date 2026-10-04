'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';

const INVALID_LINK_TOAST = 'This link is invalid or has expired. Request a new one.';

/**
 * Landing page of the emailed reset link. Better Auth validates the token at
 * /api/auth/reset-password/<token> and redirects here with ?token= (valid)
 * or ?error=INVALID_TOKEN. The query is read after mount (this is a client
 * page rendered on the server too) so there is no hydration mismatch.
 */
export default function ResetPasswordPage() {
  const router = useRouter();
  const [params, setParams] = React.useState<{ token: string | null; error: string | null } | null>(null);
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [formError, setFormError] = React.useState<string | null>(null);
  const [pending, setPending] = React.useState(false);
  const [invalid, setInvalid] = React.useState(false);

  React.useEffect(() => {
    const sp = new URLSearchParams(window.location.search);
    setParams({ token: sp.get('token'), error: sp.get('error') });
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    // The email is unknown on this page; the server hook also checks the
    // local-part rule against the token's user.
    const violation = passwordPolicyError(next, null);
    if (violation) {
      setFormError(violation.message);
      return;
    }
    if (next !== confirm) {
      setFormError("Passwords don't match.");
      return;
    }
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.resetPassword({ newPassword: next, token: params?.token ?? '' }, retry.fetchOptions);
      if (error) {
        if (error.code === 'INVALID_TOKEN') {
          toast.error(INVALID_LINK_TOAST);
          setInvalid(true);
          return;
        }
        setFormError(describeAuthError(error, { fallback: 'Could not update your password. Try again.', retryAfter: retry.value }));
        return;
      }
      toast.success('Password updated. Sign in with your new password.');
      router.replace('/signin');
    } finally {
      setPending(false);
    }
  }

  if (!params) {
    return (
      <AuthShell title="Reset your password">
        <Loader2 className="text-muted-foreground mx-auto size-5 animate-spin" aria-label="Loading" />
      </AuthShell>
    );
  }

  if (invalid || params.error || !params.token) {
    return (
      <AuthShell title="This link is invalid or has expired" description="Reset links work once and expire after 1 hour.">
        <Button asChild size="lg" className="w-full">
          <Link href="/forgot-password">Request a new link</Link>
        </Button>
        <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
          Back to sign in
        </Link>
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Choose a new password" description="All your other sessions will be signed out.">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="new-password">New password</Label>
          <Input id="new-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={next} onChange={(e) => setNext(e.target.value)} disabled={pending} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="confirm-password">Confirm new password</Label>
          <Input id="confirm-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={pending} />
        </div>
        {formError && (
          <p className="text-destructive text-sm" role="alert">
            {formError}
          </p>
        )}
        <Button type="submit" size="lg" className="w-full" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          Update password
        </Button>
      </form>
    </AuthShell>
  );
}
