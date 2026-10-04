'use client';

import * as React from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { authClient, useSession } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';

const PROVIDER_LABELS: Record<string, string> = { google: 'Google', apple: 'Apple' };

/** Better Auth's list-accounts row (the fields this section reads). */
interface LinkedAccount {
  id: string;
  providerId: string;
}

/**
 * Settings → Account (piece 2): change the password of an email/password
 * account. `listAccounts` decides: a `credential` row shows the form, a
 * social-only user is told how they sign in. The policy is checked here for
 * instant feedback and enforced again by the server hook.
 */
export function AccountSection() {
  const { data: session } = useSession();
  const email = session?.user.email ?? '';
  const accountsQuery = useQuery({
    queryKey: ['auth-accounts'],
    queryFn: async (): Promise<LinkedAccount[]> => {
      const { data, error } = await authClient.listAccounts();
      if (error) throw new Error(error.message ?? 'Could not load sign-in methods');
      return (data ?? []) as LinkedAccount[];
    },
  });
  const accounts = accountsQuery.data ?? [];
  const hasPassword = accounts.some((a) => a.providerId === 'credential');
  const socialProviders = accounts.filter((a) => a.providerId !== 'credential').map((a) => PROVIDER_LABELS[a.providerId] ?? a.providerId);

  const [current, setCurrent] = React.useState('');
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [formError, setFormError] = React.useState<string | null>(null);

  const change = useMutation({
    mutationFn: async () => {
      const violation = passwordPolicyError(next, email);
      if (violation) throw new Error(violation.message);
      if (next !== confirm) throw new Error("Passwords don't match.");
      const retry = captureRetryAfter();
      const { error } = await authClient.changePassword(
        { currentPassword: current, newPassword: next, revokeOtherSessions: true },
        retry.fetchOptions
      );
      if (error) {
        if (error.code === 'INVALID_PASSWORD') throw new Error('Current password is incorrect.');
        throw new Error(describeAuthError(error, { fallback: 'Could not update your password. Try again.', retryAfter: retry.value }));
      }
    },
    onSuccess: () => {
      toast.success('Password updated. Other devices were signed out.');
      setCurrent('');
      setNext('');
      setConfirm('');
      setFormError(null);
    },
    onError: (err) => setFormError(err instanceof Error ? err.message : 'Could not update your password. Try again.'),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Account</CardTitle>
        <CardDescription>
          Signed in as <span className="text-foreground font-medium">{email}</span>.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {accountsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : accountsQuery.isError ? (
          <p className="text-destructive text-sm">Could not load your sign-in methods. Reload to try again.</p>
        ) : hasPassword ? (
          <form
            className="flex max-w-sm flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              setFormError(null);
              change.mutate();
            }}>
            <div className="grid gap-1.5">
              <Label htmlFor="account-current-password">Current password</Label>
              <Input id="account-current-password" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} disabled={change.isPending} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="account-new-password">New password</Label>
              <Input id="account-new-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={next} onChange={(e) => setNext(e.target.value)} disabled={change.isPending} />
              <p className="text-muted-foreground text-xs">At least {PASSWORD_MIN_LENGTH} characters, and not your email address.</p>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="account-confirm-password">Confirm new password</Label>
              <Input id="account-confirm-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={change.isPending} />
            </div>
            {formError && (
              <p className="text-destructive text-sm" role="alert">
                {formError}
              </p>
            )}
            <div>
              <Button type="submit" disabled={change.isPending}>
                {change.isPending && <Loader2 className="animate-spin" />}
                Update password
              </Button>
            </div>
          </form>
        ) : (
          <p className="text-muted-foreground text-sm">
            You sign in with {socialProviders.length > 0 ? socialProviders.join(' and ') : 'a linked provider'}. Password sign-in isn't set up for this account.
          </p>
        )}
      </CardContent>
      {hasPassword && (
        <CardFooter className="border-t pt-6">
          <p className="text-muted-foreground text-xs">Changing your password signs out every other device.</p>
        </CardFooter>
      )}
    </Card>
  );
}
