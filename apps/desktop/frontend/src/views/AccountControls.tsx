import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, Trash2 } from 'lucide-react';
import { useState } from 'react';

import { api, orMock } from '@/lib/api';
import { getAuthClient, resumeApi, signOutLocally, suspendApi } from '@/lib/auth';
import { mockUser } from '@/lib/mock';
import { clearOfflineState } from '@/lib/offline';
import { billingWebOrigin, useServerConfig } from '@/lib/server-config';
import { toast } from '@/lib/toast';
import { desktop } from '@/lib/wails';
import { Button } from '@/ui/button';
import { Input } from '@/ui/input';

type DeleteError = {
  status?: number;
  code?: string;
  message?: string;
  details?: { teams?: { name: string }[] };
};

/**
 * Account lifecycle controls: "Download my data" opens the web account page
 * (the export streams a zip to the browser), "Delete account" runs in-app
 * through the server's Better Auth client with an inline confirm (typed email
 * + password for credential accounts). Rendered by Settings → Account and by
 * the paywall's "Account & data", so export and deletion stay reachable
 * without an active subscription.
 */
export function AccountControls() {
  const queryClient = useQueryClient();
  const { config, demoMode } = useServerConfig();
  const { data: user } = useQuery({
    queryKey: ['me'],
    queryFn: () =>
      orMock(
        () => api.getMe(),
        () => mockUser
      ),
  });

  const [deleting, setDeleting] = useState(false);
  const [typedEmail, setTypedEmail] = useState('');
  const [deletePassword, setDeletePassword] = useState('');
  const [hasCredential, setHasCredential] = useState<boolean | null>(null);
  const [accountsError, setAccountsError] = useState(false);
  // Social-only account whose session is too old to delete in-app.
  const [reauthInBrowser, setReauthInBrowser] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);

  function openWebAccount(): boolean {
    // The advertised web URL, else the Better Auth origin.
    const origin = billingWebOrigin(config);
    if (!origin) {
      toast({ title: 'No web URL for this server', variant: 'destructive' });
      return false;
    }
    desktop.OpenExternal(`${origin}/settings?tab=account`);
    return true;
  }

  function openDataExport() {
    if (demoMode) {
      toast({ title: 'Not available in demo' });
      return;
    }
    openWebAccount();
  }

  async function startDelete() {
    if (demoMode) {
      toast({ title: 'Not available in demo' });
      return;
    }
    const c = getAuthClient();
    if (!c) {
      // No Better Auth client (server not configured): the web app owns the flow.
      openWebAccount();
      return;
    }
    setDeleting(true);
    setTypedEmail('');
    setDeletePassword('');
    setReauthInBrowser(false);
    await loadAccounts();
  }

  async function loadAccounts() {
    const c = getAuthClient();
    if (!c) return;
    setHasCredential(null);
    setAccountsError(false);
    try {
      // Better Auth's list-accounts rows carry the provider as `providerId`.
      const { data, error } = await c.listAccounts();
      if (error || !data) throw new Error('listAccounts failed');
      setHasCredential(data.some((a) => a.providerId === 'credential'));
    } catch {
      // Never guess social-only: a credential user would get no password
      // field and a misleading error. Offer a retry instead.
      setAccountsError(true);
    }
  }

  function showDeleteError(err: DeleteError) {
    const teams = err.details?.teams ?? [];
    if (err.code === 'owns_teams' || teams.length > 0) {
      toast({
        title: 'Transfer your teams first',
        description: teams.map((t) => t.name).join(', '),
        variant: 'destructive',
      });
    } else if (err.code === 'SESSION_EXPIRED' || err.status === 403) {
      // Re-authentication required (Better Auth 400 SESSION_EXPIRED: the
      // session is older than freshAge and no password was sent).
      if (hasCredential) {
        setDeletePassword('');
        toast({
          title: 'Enter your password',
          description: 'For your security, enter your password to delete your account.',
          variant: 'destructive',
        });
      } else {
        // Social sign-in runs in the browser: the web account page re-runs
        // the provider sign-in and then retries the delete.
        setReauthInBrowser(true);
      }
    } else if (err.code === 'INVALID_PASSWORD' || (hasCredential && (err.status === 400 || err.status === 401))) {
      toast({ title: 'Incorrect password', variant: 'destructive' });
    } else if ((err.status ?? 0) >= 500 && err.message) {
      toast({ title: 'Could not delete your account', description: err.message, variant: 'destructive' });
    } else {
      toast({ title: 'Could not delete your account', description: 'Try again.', variant: 'destructive' });
    }
  }

  async function runDelete() {
    const c = getAuthClient();
    if (!c) return;
    setDeleteBusy(true);
    try {
      // Pause every Go API request (and drop the cached JWT) BEFORE deleting:
      // a background query carrying a JWT minted for this user would pass
      // requireAuth → EnsureUser and re-create the purged users row.
      suspendApi();
      await queryClient.cancelQueries();
      const { error } = await c.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        resumeApi();
        showDeleteError(error as DeleteError);
        return;
      }
    } catch {
      resumeApi();
      toast({ title: 'Could not delete your account', description: 'Try again.', variant: 'destructive' });
      return;
    } finally {
      setDeleteBusy(false);
    }
    // The account is gone: forget it locally with no further request (the API
    // stays paused until the next sign-in), then drop cached mail and the
    // queued outbox. The session store flip returns the app to sign-in.
    queryClient.clear();
    await clearOfflineState().catch(() => undefined);
    signOutLocally();
    toast({ title: 'Account deleted' });
  }

  const email = (user?.email ?? '').trim().toLowerCase();
  const emailMatches = email !== '' && typedEmail.trim().toLowerCase() === email;

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 border-t p-3">
        <Button variant="outline" size="sm" onClick={openDataExport}>
          <Download /> Download my data
        </Button>
        {!deleting && (
          <Button variant="outline" size="sm" className="text-destructive" onClick={() => void startDelete()}>
            <Trash2 /> Delete account
          </Button>
        )}
      </div>
      {deleting && (
        <div className="flex flex-col gap-2 border-t p-3">
          <p className="text-xs text-muted-foreground">
            This permanently deletes your account: mirrored mail and calendars, drafts, snippets, tasks, settings and
            any team where you are the only member. Type your email to confirm
            {hasCredential ? ' and enter your password' : ''}. There is no undo.
          </p>
          {accountsError && (
            <div className="flex items-center gap-2" role="alert">
              <p className="text-xs text-destructive">Couldn’t check how you sign in. Check your connection and try again.</p>
              <Button variant="outline" size="sm" onClick={() => void loadAccounts()}>
                Retry
              </Button>
            </div>
          )}
          {reauthInBrowser && (
            <div className="flex flex-col gap-2" role="alert">
              <p className="text-xs text-muted-foreground">
                For your security, confirm it’s you: sign in with your provider again in your browser, where your
                account deletion continues.
              </p>
              <Button variant="outline" size="sm" className="self-start" onClick={() => openWebAccount()}>
                Continue in browser
              </Button>
            </div>
          )}
          <Input
            aria-label="Type your email to confirm"
            placeholder={user?.email ?? ''}
            value={typedEmail}
            onChange={(e) => setTypedEmail(e.target.value)}
          />
          {hasCredential && (
            <Input
              aria-label="Your password"
              type="password"
              placeholder="Password"
              value={deletePassword}
              onChange={(e) => setDeletePassword(e.target.value)}
            />
          )}
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setDeleting(false)} disabled={deleteBusy}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => void runDelete()}
              disabled={
                deleteBusy || hasCredential === null || !emailMatches || (hasCredential && deletePassword.length === 0)
              }
            >
              Delete my account
            </Button>
          </div>
        </div>
      )}
    </>
  );
}
