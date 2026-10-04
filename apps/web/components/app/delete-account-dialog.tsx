'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
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
import { authClient } from '@/lib/auth-client';
import { performSignOut } from '@/lib/sign-out';

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

/** Maps Better Auth's deleteUser error (incl. the Go API's owns_teams envelope relayed by beforeDelete) to dialog copy. */
export function describeDeleteError(error: DeleteUserError): { message: string; teams: BlockingTeam[] } {
  const teams = Array.isArray(error.details?.teams) ? error.details.teams : [];
  if (error.code === 'owns_teams' || teams.length > 0) {
    return { message: 'Transfer ownership of these teams (or remove their other members) first:', teams };
  }
  if ((error.code && STALE_SESSION_CODES.has(error.code)) || error.status === 403) {
    return { message: 'For your security, sign in again and then retry.', teams: [] };
  }
  if (error.code === 'INVALID_PASSWORD' || error.status === 400 || error.status === 401) {
    return { message: 'That password is incorrect.', teams: [] };
  }
  if (error.status === 429) {
    return { message: 'Too many attempts. Wait a minute and try again.', teams: [] };
  }
  return { message: 'Could not delete your account. Try again.', teams: [] };
}

/** Better Auth's list-accounts row (the field this dialog reads). */
interface LinkedAccount {
  providerId: string;
}

/**
 * Danger-zone confirmation: explains what goes, requires the typed email and
 * (credential accounts only) the password, then calls Better Auth's
 * deleteUser. On success it leaves the app shell FIRST (/goodbye is outside
 * the session-gated layout, which would otherwise bounce to /signin) and
 * then runs the shared sign-out routine to scrub local state.
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
  const router = useRouter();
  const [typed, setTyped] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [hasCredential, setHasCredential] = React.useState<boolean | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [teams, setTeams] = React.useState<BlockingTeam[]>([]);

  React.useEffect(() => {
    if (!open) return;
    let active = true;
    setTyped('');
    setPassword('');
    setError(null);
    setTeams([]);
    setHasCredential(null);
    authClient
      .listAccounts()
      .then(({ data }) => {
        const rows = (data ?? []) as LinkedAccount[];
        if (active) setHasCredential(rows.some((a) => a.providerId === 'credential'));
      })
      .catch(() => {
        if (active) setHasCredential(false);
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
    try {
      const { error: err } = await authClient.deleteUser(hasCredential ? { password } : {});
      if (err) {
        const described = describeDeleteError(err as DeleteUserError);
        setError(described.message);
        setTeams(described.teams);
        return;
      }
      router.replace('/goodbye');
      await performSignOut().catch(() => undefined);
    } catch {
      setError('Could not delete your account. Try again.');
    } finally {
      setSubmitting(false);
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
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
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
