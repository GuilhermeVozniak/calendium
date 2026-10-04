'use client';

import * as React from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  beginAccountDeletion,
  endAccountDeletion,
  leaveAfterAccountDeletion,
  scrubLocalStateAfterDeletion,
} from '@/lib/account-deletion';
import { authClient } from '@/lib/auth-client';

export interface BlockingTeam {
  id: string;
  name: string;
}

export interface DeleteUserError {
  status?: number;
  code?: string;
  message?: string;
  details?: { teams?: BlockingTeam[] };
}

export function normalizeEmail(value: string): string {
  return value.trim().toLowerCase();
}

/** Better Auth's freshness refusals: deleteUser without a password on a session older than freshAge (400 SESSION_EXPIRED), or the fresh-session middleware (403 SESSION_NOT_FRESH). */
const STALE_SESSION_CODES = new Set(['SESSION_EXPIRED', 'SESSION_NOT_FRESH']);

const PROVIDER_LABELS: Record<string, string> = { google: 'Google', apple: 'Apple', microsoft: 'Microsoft' };

export function providerLabel(providerId: string): string {
  return PROVIDER_LABELS[providerId] ?? providerId.charAt(0).toUpperCase() + providerId.slice(1);
}

/** How the user can prove it's them again: re-enter the password, or re-run the provider's sign-in. */
export type Reauth = 'password' | 'social' | null;

export interface DeleteErrorContext {
  hasCredential?: boolean | null;
  socialProvider?: string | null;
}

/**
 * Maps Better Auth's deleteUser error (incl. the Go API's owns_teams envelope
 * relayed by beforeDelete) to dialog copy. A stale session is a 400 too, so
 * the freshness codes are checked before the generic 400 → "incorrect
 * password" mapping (Track D I1).
 */
export function describeDeleteError(
  error: DeleteUserError,
  ctx: DeleteErrorContext = {}
): { message: string; teams: BlockingTeam[]; reauth: Reauth } {
  const teams = Array.isArray(error.details?.teams) ? error.details.teams : [];
  if (error.code === 'owns_teams' || teams.length > 0) {
    return { message: 'Transfer ownership of these teams (or remove their other members) first:', teams, reauth: null };
  }
  if ((error.code && STALE_SESSION_CODES.has(error.code)) || error.status === 403) {
    if (ctx.hasCredential) {
      return { message: 'For your security, enter your password and try again.', teams: [], reauth: 'password' };
    }
    if (ctx.socialProvider) {
      const label = providerLabel(ctx.socialProvider);
      return {
        message: `For your security, confirm it's you with ${label}, then delete your account again.`,
        teams: [],
        reauth: 'social',
      };
    }
    return { message: 'For your security, sign in again and then retry.', teams: [], reauth: null };
  }
  if (error.status === 401) {
    return { message: 'Your session has ended. Sign in again and then retry.', teams: [], reauth: null };
  }
  if (error.code === 'INVALID_PASSWORD' || error.status === 400) {
    return { message: 'That password is incorrect.', teams: [], reauth: null };
  }
  if (error.status === 429) {
    return { message: 'Too many attempts. Wait a minute and try again.', teams: [], reauth: null };
  }
  return { message: 'Could not delete your account. Try again.', teams: [], reauth: null };
}

/** Better Auth's list-accounts row (the field this dialog reads). */
interface LinkedAccount {
  providerId: string;
}

/**
 * Danger-zone confirmation: explains what goes, requires the typed email and
 * (credential accounts only) the password, then calls Better Auth's
 * deleteUser. The deletion flag goes up and all API activity is paused
 * BEFORE the call so the (app) layout does not bounce the now session-less
 * shell to /signin and nothing from this tab races the purge; on success the
 * browser state is scrubbed without any network call (the session and the
 * Go user are gone) and the page hard-navigates to /goodbye.
 */
export function DeleteAccountDialog({
  open,
  onOpenChange,
  email,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  email: string;
}) {
  const queryClient = useQueryClient();
  const [typed, setTyped] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [hasCredential, setHasCredential] = React.useState<boolean | null>(null);
  const [socialProvider, setSocialProvider] = React.useState<string | null>(null);
  const [lookupFailed, setLookupFailed] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [teams, setTeams] = React.useState<BlockingTeam[]>([]);
  const [reauth, setReauth] = React.useState<Reauth>(null);

  React.useEffect(() => {
    if (!open) return;
    let active = true;
    setTyped('');
    setPassword('');
    setError(null);
    setTeams([]);
    setReauth(null);
    setHasCredential(null);
    setSocialProvider(null);
    setLookupFailed(false);
    authClient
      .listAccounts()
      .then(({ data, error: lookupError }) => {
        if (!active) return;
        if (lookupError || !Array.isArray(data)) {
          setLookupFailed(true);
          return;
        }
        const rows = data as LinkedAccount[];
        setHasCredential(rows.some((a) => a.providerId === 'credential'));
        setSocialProvider(rows.find((a) => a.providerId !== 'credential')?.providerId ?? null);
      })
      .catch(() => {
        // Guessing would either hide the password field from a credential
        // account or demand one from a social-only account; say so instead.
        if (active) setLookupFailed(true);
      });
    return () => {
      active = false;
    };
  }, [open]);

  const emailMatches = email !== '' && normalizeEmail(typed) === normalizeEmail(email);
  const canConfirm = emailMatches && hasCredential !== null && (!hasCredential || password.length > 0) && !submitting;

  async function confirm() {
    setSubmitting(true);
    setError(null);
    setTeams([]);
    setReauth(null);
    let deleted = false;
    try {
      // Pause every query, poll and stream first: the purge must never race
      // a request from this tab.
      await beginAccountDeletion(queryClient);
      const { error: err } = await authClient.deleteUser(hasCredential ? { password } : {});
      if (err) {
        const described = describeDeleteError(err as DeleteUserError, { hasCredential, socialProvider });
        setError(described.message);
        setTeams(described.teams);
        setReauth(described.reauth);
        return;
      }
      deleted = true;
    } catch {
      setError('Could not delete your account. Try again.');
    } finally {
      if (!deleted) {
        endAccountDeletion();
        setSubmitting(false);
      }
    }
    if (!deleted) return;
    // Success: no authenticated request from here on. Keep the spinner up
    // until the hard navigation replaces the page.
    await scrubLocalStateAfterDeletion(queryClient).catch(() => undefined);
    leaveAfterAccountDeletion();
  }

  async function reauthenticate() {
    if (!socialProvider) return;
    setSubmitting(true);
    try {
      const { error: err } = await authClient.signIn.social({
        provider: socialProvider,
        callbackURL: '/settings?tab=account',
      });
      if (err) throw new Error(err.message ?? 'Sign-in failed');
      // Success: the browser is on its way to the provider.
    } catch {
      setSubmitting(false);
      setError(`Could not start sign-in with ${providerLabel(socialProvider)}. Try again.`);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !submitting && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete your account</DialogTitle>
          <DialogDescription>
            This permanently deletes your Calendium account: connected mailboxes and their mirrored mail and
            calendars, drafts, snippets, templates, booking links, polls, tasks, notes, settings, and any team where
            you are the only member. Teams you share stay with their other members. An active subscription is
            cancelled immediately. Your mail stays with Google or Microsoft. There is no undo.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (canConfirm) void confirm();
          }}>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="delete-account-email">Type your email ({email}) to confirm</Label>
            <Input
              id="delete-account-email"
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              disabled={submitting}
            />
          </div>
          {hasCredential && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="delete-account-password">Your password</Label>
              <Input
                id="delete-account-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={submitting}
              />
            </div>
          )}
          {lookupFailed && (
            <p role="alert" className="text-destructive text-sm">
              Could not check how you sign in. Close this dialog and try again.
            </p>
          )}
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          {reauth === 'social' && socialProvider && (
            <Button type="button" variant="outline" onClick={() => void reauthenticate()} disabled={submitting}>
              Continue with {providerLabel(socialProvider)}
            </Button>
          )}
          {teams.length > 0 && (
            <ul className="list-disc pl-5 text-sm">
              {teams.map((t) => (
                <li key={t.id}>{t.name}</li>
              ))}
            </ul>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={!canConfirm}>
              {submitting && <Loader2 className="animate-spin" />}
              Delete my account
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
