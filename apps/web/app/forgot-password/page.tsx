'use client';

import * as React from 'react';
import Link from 'next/link';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { useInstance } from '@/lib/use-instance';

const BACK_TO_SIGN_IN = (
  <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
    Back to sign in
  </Link>
);

/**
 * Self-service password reset entry. The confirmation is deliberately the
 * same whether or not the address exists (Better Auth answers generically
 * and in constant time). When the server reports `features.email === false`
 * (self-host without SMTP) the page explains the administrator path instead
 * — any other settled instance state (unreachable, pre-piece-2) shows the
 * form. Nothing renders while the instance is still loading, so a no-email
 * server never flashes the form first.
 */
export default function ForgotPasswordPage() {
  const { data: instance, isError } = useInstance();
  const emailDisabled = instance?.features?.email === false;
  const [email, setEmail] = React.useState('');
  const [sent, setSent] = React.useState(false);
  const [pending, setPending] = React.useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.requestPasswordReset({ email, redirectTo: '/reset-password' }, retry.fetchOptions);
      if (error) {
        toast.error(describeAuthError(error, { fallback: 'Something went wrong. Try again.', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      setSent(true);
    } finally {
      setPending(false);
    }
  }

  if (!instance && !isError) return <main className="min-h-svh" aria-busy="true" />;

  if (emailDisabled) {
    return (
      <AuthShell title="Password reset by email isn't available on this server">
        <p className="text-muted-foreground text-sm">
          This Calendium instance has no outgoing email configured. Ask your administrator to reset your password; on the server they run:
        </p>
        <pre className="bg-muted overflow-x-auto rounded-md p-3 text-xs">
          docker compose exec web node apps/web/scripts/reset-password.mjs &lt;your email&gt;
        </pre>
        <p className="text-muted-foreground text-sm">
          It prints a temporary password and signs out every device. Sign in with it, then change it in Settings → Account.
        </p>
        {BACK_TO_SIGN_IN}
      </AuthShell>
    );
  }

  if (sent) {
    return (
      <AuthShell title="Check your inbox">
        <p className="text-muted-foreground text-center text-sm" role="status" aria-live="polite">
          If an account exists for that address, we sent a link.
        </p>
        <p className="text-muted-foreground text-center text-xs">The link expires in 1 hour.</p>
        {BACK_TO_SIGN_IN}
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Forgot your password?" description="Enter your email and we'll send you a link to choose a new one.">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={pending} />
        </div>
        <Button type="submit" size="lg" className="w-full" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          Send reset link
        </Button>
      </form>
      {BACK_TO_SIGN_IN}
    </AuthShell>
  );
}
