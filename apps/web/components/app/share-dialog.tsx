'use client';

import * as React from 'react';
import type { ShareAudience, ThreadShare } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Copy, Globe, Loader2, Users } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { getApiClient } from '@/lib/api';
import { formatFullTime } from '@/lib/mail-utils';

/** Window event the ⌘K palette dispatches to open the share dialog on the open thread. */
export const SHARE_THREAD_EVENT = 'calendium:share-thread';

export function dispatchShareThread(): void {
  window.dispatchEvent(new Event(SHARE_THREAD_EVENT));
}

interface ShareDialogProps {
  threadId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * "Share thread" dialog: create a live read-only link (external or
 * team-audience), surface the one-time raw token as a copyable URL, and list/
 * revoke existing shares. The raw token exists only in the create response —
 * it is rendered once, copyable, and never logged or sent to analytics.
 */
export function ShareDialog(props: ShareDialogProps) {
  // A closed dialog renders nothing and runs no queries — the hooks live in
  // the inner component so mounting ShareDialog alongside a thread is free.
  if (!props.open) return null;
  return <ShareDialogContent {...props} />;
}

function shareIsLive(share: ThreadShare): boolean {
  if (share.revokedAt) return false;
  return !share.expiresAt || new Date(share.expiresAt).getTime() > Date.now();
}

function ShareDialogContent({ threadId, open, onOpenChange }: ShareDialogProps) {
  const api = getApiClient();
  const queryClient = useQueryClient();

  const teamsQuery = useQuery({
    queryKey: ['teams'],
    queryFn: () => api.listTeams(),
  });
  const sharesQuery = useQuery({
    queryKey: ['thread-shares', threadId],
    queryFn: () => api.listThreadShares(threadId),
  });

  const teams = teamsQuery.data ?? [];
  const [audience, setAudience] = React.useState<ShareAudience>('external');
  const [teamId, setTeamId] = React.useState('');
  const [createdLink, setCreatedLink] = React.useState<string | null>(null);
  const [copied, setCopied] = React.useState(false);

  // Default the team picker to the first team once teams load.
  React.useEffect(() => {
    if (!teamId && teams.length > 0) setTeamId(teams[0]!.id);
  }, [teams, teamId]);

  const createMutation = useMutation({
    mutationFn: () =>
      api.shareThread(threadId, audience === 'team' ? { audience, teamId } : { audience }),
    onSuccess: (created) => {
      // The raw token appears exactly once, here. Surface it and forget it —
      // never log it or hand it to analytics.
      setCreatedLink(`${window.location.origin}/shared/${created.token}`);
      setCopied(false);
      void queryClient.invalidateQueries({ queryKey: ['thread-shares', threadId] });
    },
    onError: () => toast.error('Could not create the share link.'),
  });

  const revokeMutation = useMutation({
    mutationFn: (shareId: string) => api.revokeThreadShare(threadId, shareId),
    onSuccess: () => {
      toast.success('Link revoked');
      void queryClient.invalidateQueries({ queryKey: ['thread-shares', threadId] });
    },
    onError: () => toast.error('Could not revoke the link.'),
  });

  async function copyLink() {
    if (!createdLink) return;
    try {
      await navigator.clipboard.writeText(createdLink);
      setCopied(true);
      toast.success('Link copied');
    } catch {
      toast.error('Could not copy — select the link and copy it manually.');
    }
  }

  const canCreate = audience === 'external' || (audience === 'team' && teamId !== '');
  const shares = sharesQuery.data ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Share conversation</DialogTitle>
          <DialogDescription>
            Create a live, read-only link to this conversation. Viewers see messages only —
            never your labels, snoozes, or drafts.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label>Who can open it</Label>
            <div className="flex gap-2">
              <Button
                type="button"
                size="sm"
                variant={audience === 'external' ? 'default' : 'outline'}
                onClick={() => setAudience('external')}
              >
                <Globe />
                Anyone with the link
              </Button>
              <Button
                type="button"
                size="sm"
                variant={audience === 'team' ? 'default' : 'outline'}
                disabled={teams.length === 0}
                onClick={() => setAudience('team')}
              >
                <Users />
                Team members only
              </Button>
            </div>
            {teams.length === 0 && (
              <p className="text-muted-foreground text-xs">
                Join a team to create team-only links.
              </p>
            )}
          </div>

          {audience === 'team' && teams.length > 0 && (
            <div className="flex flex-col gap-1">
              <Label htmlFor="share-team">Team</Label>
              <select
                id="share-team"
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm shadow-xs"
                value={teamId}
                onChange={(e) => setTeamId(e.target.value)}
              >
                {teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name}
                  </option>
                ))}
              </select>
            </div>
          )}

          <Button
            type="button"
            className="w-fit"
            disabled={!canCreate || createMutation.isPending}
            onClick={() => createMutation.mutate()}
          >
            {createMutation.isPending ? <Loader2 className="animate-spin" /> : null}
            Create link
          </Button>

          {createdLink && (
            <div className="flex flex-col gap-2 rounded-md border p-3">
              <p className="text-sm font-medium">Your link is ready</p>
              <div className="flex items-center gap-2">
                <Input readOnly value={createdLink} aria-label="Share link" className="h-8 text-xs" />
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  aria-label="Copy link"
                  onClick={() => void copyLink()}
                >
                  {copied ? <Check /> : <Copy />}
                </Button>
              </div>
              <p className="text-muted-foreground text-xs">
                Copy it now — for security, this link is shown only once.
              </p>
            </div>
          )}

          {shares.length > 0 && (
            <div className="flex flex-col gap-2">
              <Label>Existing links</Label>
              <ul className="flex flex-col gap-2">
                {shares.map((share) => (
                  <li
                    key={share.id}
                    className="flex items-center justify-between gap-2 rounded-md border px-3 py-2"
                  >
                    <div className="min-w-0">
                      <p className="text-sm">
                        {share.audience === 'team' ? 'Team link' : 'External link'}
                      </p>
                      <p className="text-muted-foreground text-xs">
                        Created {formatFullTime(share.createdAt)}
                      </p>
                    </div>
                    {shareIsLive(share) ? (
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        disabled={revokeMutation.isPending}
                        onClick={() => revokeMutation.mutate(share.id)}
                      >
                        Revoke
                      </Button>
                    ) : (
                      <span className="text-muted-foreground text-xs">
                        {share.revokedAt ? 'Revoked' : 'Expired'}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
