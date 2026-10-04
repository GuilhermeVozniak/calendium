import { useQuery } from '@tanstack/react-query';
import { Download, Trash2 } from 'lucide-react';
import { useState } from 'react';

import { api, orMock } from '@/lib/api';
import { clearStoredToken, getAuthClient, signOut } from '@/lib/auth';
import { mockUser } from '@/lib/mock';
import { clearOfflineState } from '@/lib/offline';
import { billingWebOrigin, useServerConfig } from '@/lib/server-config';
import { toast } from '@/lib/toast';
import { desktop } from '@/lib/wails';
import { Button } from '@/ui/button';
import { Input } from '@/ui/input';

type DeleteError = { status?: number; code?: string; details?: { teams?: { name: string }[] } };

/**
 * Account lifecycle controls: "Download my data" opens the web account page
 * (the export streams a zip to the browser), "Delete account" runs in-app
 * through the server's Better Auth client with an inline confirm (typed email
 * + password for credential accounts). Rendered by Settings → Account and by
 * the paywall's "Account & data", so export and deletion stay reachable
 * without an active subscription.
 */
export function AccountControls() {
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
    setHasCredential(null);
    setTypedEmail('');
    setDeletePassword('');
    try {
      // Better Auth's list-accounts rows carry the provider as `providerId`.
      const { data } = await c.listAccounts();
      setHasCredential(!!data?.some((a) => a.providerId === 'credential'));
    } catch {
      setHasCredential(false);
    }
  }

  async function runDelete() {
    const c = getAuthClient();
    if (!c) return;
    setDeleteBusy(true);
    try {
      const { error } = await c.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        const err = error as DeleteError;
        const teams = err.details?.teams ?? [];
        if (err.code === 'owns_teams' || teams.length > 0) {
          toast({
            title: 'Transfer your teams first',
            description: teams.map((t) => t.name).join(', '),
            variant: 'destructive',
          });
        } else if (err.code === 'INVALID_PASSWORD' || err.status === 400 || err.status === 401) {
          toast({ title: 'Incorrect password', variant: 'destructive' });
        } else if (err.status === 403) {
          toast({
            title: 'Sign in again',
            description: 'For your security, sign in again and then retry.',
            variant: 'destructive',
          });
        } else {
          toast({ title: 'Could not delete your account', description: 'Try again.', variant: 'destructive' });
        }
        return;
      }
      // The session is gone server-side; drop the local token, cached mail
      // and queued outbox, and return to sign-in.
      clearStoredToken();
      await signOut();
      await clearOfflineState().catch(() => undefined);
      toast({ title: 'Account deleted' });
    } catch {
      toast({ title: 'Could not delete your account', description: 'Try again.', variant: 'destructive' });
    } finally {
      setDeleteBusy(false);
    }
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
