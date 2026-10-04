'use client';

import * as React from 'react';
import { ApiRequestError } from '@calendium/shared';
import { useMutation } from '@tanstack/react-query';
import { Download, Loader2, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { DeleteAccountDialog } from '@/components/app/delete-account-dialog';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { downloadExportApi, exportFilename, retryMessage, saveBlob } from '@/lib/account-data';
import { useSession } from '@/lib/auth-client';
import { DEMO_MODE } from '@/lib/demo';

const DEMO_TOAST = 'Not available in demo';

/**
 * Settings → Account: identity (or the sign-in/password card passed as
 * children — piece 2's Change-password slot), "Download my data" and the
 * Danger zone. Also rendered by the paywall ("Account & data") so a
 * paywalled user keeps export + delete. Demo mode renders every control but
 * never calls /v1/me/export or /api/auth/delete-user.
 */
export function AccountSection({ children }: { children?: React.ReactNode }) {
  const { data: session } = useSession();
  const email = session?.user.email ?? '';
  const name = session?.user.name ?? '';
  const [deleteOpen, setDeleteOpen] = React.useState(false);

  const download = useMutation({
    mutationFn: async () => {
      const blob = await downloadExportApi();
      saveBlob(blob, exportFilename());
    },
    onSuccess: () => toast.success('Your export is downloading'),
    onError: (err) => {
      if (err instanceof ApiRequestError && err.code === 'export_throttled') {
        toast.error(retryMessage(err.retryAfterSeconds ?? 3600));
        return;
      }
      toast.error('Could not prepare your export. Try again.');
    },
  });

  function onDownload() {
    if (DEMO_MODE) {
      toast.info(DEMO_TOAST);
      return;
    }
    download.mutate();
  }

  function onDelete() {
    if (DEMO_MODE) {
      toast.info(DEMO_TOAST);
      return;
    }
    setDeleteOpen(true);
  }

  return (
    <div className="flex flex-col gap-4">
      {children ?? (
        <Card>
          <CardHeader>
            <CardTitle>Account</CardTitle>
            <CardDescription>{name ? `${name} · ${email}` : email}</CardDescription>
          </CardHeader>
        </Card>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Your data</CardTitle>
          <CardDescription>
            Download a zip with your profile, mirrored mail and calendars, drafts, snippets, templates, tasks, booking
            links, polls and settings. One export per hour.
          </CardDescription>
          <CardAction>
            <Button size="sm" variant="outline" onClick={onDownload} disabled={download.isPending}>
              {download.isPending ? <Loader2 className="animate-spin" /> : <Download />}
              Download my data
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      <Card className="border-destructive/40">
        <CardHeader>
          <CardTitle>Danger zone</CardTitle>
          <CardDescription>
            Deleting your account removes your data from Calendium. Download a copy first if you want to keep it.
          </CardDescription>
          <CardAction>
            <Button size="sm" variant="destructive" onClick={onDelete}>
              <Trash2 />
              Delete account
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      {!DEMO_MODE && <DeleteAccountDialog open={deleteOpen} onOpenChange={setDeleteOpen} email={email} />}
    </div>
  );
}
