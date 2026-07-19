'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { formatDistanceToNow } from 'date-fns';
import { Plus, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';

import type { Delegation, DelegationScope, DelegationStatus } from '@calendium/shared';
import { ApiRequestError, DELEGATION_SCOPES } from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
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
import { Skeleton } from '@/components/ui/skeleton';
import { getApiClient } from '@/lib/api';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Human labels for the four delegation scopes (backend delegation.go enum). */
export const SCOPE_LABELS: Record<DelegationScope, string> = {
  mail_read: 'Read mail',
  mail_write: 'Send & manage mail',
  calendar_read: 'Read calendar',
  calendar_write: 'Manage calendar',
};

function StatusBadge({ status }: { status: DelegationStatus }) {
  if (status === 'active') return <Badge variant="secondary">Active</Badge>;
  if (status === 'pending') return <Badge variant="outline">Pending</Badge>;
  return (
    <Badge variant="outline" className="text-muted-foreground">
      Revoked
    </Badge>
  );
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiRequestError ? err.message : fallback;
}

function GrantRow({
  delegation,
  who,
  children,
}: {
  delegation: Delegation;
  who: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border p-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{who}</span>
          <StatusBadge status={delegation.status} />
        </div>
        <div className="mt-1 flex flex-wrap gap-1">
          {delegation.scopes.map((scope) => (
            <Badge key={scope} variant="outline" className="text-xs">
              {SCOPE_LABELS[scope]}
            </Badge>
          ))}
        </div>
      </div>
      {children}
    </div>
  );
}

/**
 * Settings "Delegation" section (M2.7 Task 15): grant/accept/revoke EA
 * delegations plus the principal's delegated-activity audit table. Grants and
 * audit entries render server data only — the API exposes user ids, so ids are
 * what we show; nothing is fabricated client-side.
 */
export function DelegationSection() {
  const queryClient = useQueryClient();
  const delegationsQuery = useQuery({
    queryKey: ['delegations'],
    queryFn: () => getApiClient().listDelegations(),
  });
  const auditQuery = useQuery({
    queryKey: ['delegation-audit'],
    queryFn: () => getApiClient().listDelegationAudit(50),
  });

  const [createOpen, setCreateOpen] = React.useState(false);
  const [assistantEmail, setAssistantEmail] = React.useState('');
  const [scopes, setScopes] = React.useState<DelegationScope[]>([]);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['delegations'] });
  };

  const create = useMutation({
    mutationFn: () => getApiClient().createDelegation(assistantEmail.trim(), scopes),
    onSuccess: () => {
      invalidate();
      toast.success('Delegation created — the assistant must accept it before acting');
      setCreateOpen(false);
      setAssistantEmail('');
      setScopes([]);
    },
    onError: (err) => toast.error(errorMessage(err, 'Could not create the delegation')),
  });
  const accept = useMutation({
    mutationFn: (id: string) => getApiClient().acceptDelegation(id),
    onSuccess: () => {
      invalidate();
      toast.success('Delegation accepted');
    },
    onError: (err) => toast.error(errorMessage(err, 'Could not accept the delegation')),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => getApiClient().revokeDelegation(id),
    onSuccess: () => {
      invalidate();
      toast.success('Delegation revoked');
    },
    onError: (err) => toast.error(errorMessage(err, 'Could not revoke the delegation')),
  });

  const asPrincipal = delegationsQuery.data?.asPrincipal ?? [];
  const asAssistant = delegationsQuery.data?.asAssistant ?? [];
  const entries = auditQuery.data?.entries ?? [];

  const toggleScope = (scope: DelegationScope) =>
    setScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]
    );

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Delegation</CardTitle>
          <CardDescription>
            Let an assistant act on your mail and calendar with exactly the scopes you grant.
            Every delegated change is audit-logged with who really made it.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus />
              New grant
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {delegationsQuery.isLoading && (
            <>
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </>
          )}
          {delegationsQuery.isError && (
            <p className="text-destructive text-sm">Could not load delegations.</p>
          )}
          {delegationsQuery.isSuccess && asPrincipal.length === 0 && asAssistant.length === 0 && (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              No delegations yet. Grant an assistant scoped access to get started.
            </p>
          )}
          {asPrincipal.length > 0 && (
            <p className="text-xs font-medium text-muted-foreground">Assistants you granted</p>
          )}
          {asPrincipal.map((d) => (
            <GrantRow key={d.id} delegation={d} who={d.assistantId}>
              {d.status !== 'revoked' && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-muted-foreground hover:text-destructive shrink-0"
                  onClick={() => revoke.mutate(d.id)}
                  disabled={revoke.isPending}
                  aria-label={`Revoke delegation for ${d.assistantId}`}
                >
                  Revoke
                </Button>
              )}
            </GrantRow>
          ))}
          {asAssistant.length > 0 && (
            <p className="mt-2 text-xs font-medium text-muted-foreground">You act for</p>
          )}
          {asAssistant.map((d) => (
            <GrantRow key={d.id} delegation={d} who={d.principalId}>
              {d.status === 'pending' && (
                <Button
                  size="sm"
                  className="shrink-0"
                  onClick={() => accept.mutate(d.id)}
                  disabled={accept.isPending}
                  aria-label={`Accept delegation from ${d.principalId}`}
                >
                  Accept
                </Button>
              )}
              {d.status !== 'revoked' && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-muted-foreground hover:text-destructive shrink-0"
                  onClick={() => revoke.mutate(d.id)}
                  disabled={revoke.isPending}
                  aria-label={`${d.status === 'pending' ? 'Decline' : 'Revoke'} delegation from ${d.principalId}`}
                >
                  {d.status === 'pending' ? 'Decline' : 'Revoke'}
                </Button>
              )}
            </GrantRow>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="size-4" />
            Delegated activity
          </CardTitle>
          <CardDescription>
            Every change an assistant made on your account — newest first, append-only.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {auditQuery.isLoading && <Skeleton className="h-10 w-full" />}
          {auditQuery.isError && (
            <p className="text-destructive text-sm">Could not load the audit log.</p>
          )}
          {auditQuery.isSuccess && entries.length === 0 && (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              No delegated activity yet.
            </p>
          )}
          {entries.map((entry) => (
            <div key={entry.id} className="flex items-center gap-3 rounded-lg border p-3 text-sm">
              <div className="min-w-0 flex-1">
                <span className="block truncate font-mono text-xs">{entry.action}</span>
                <span className="text-muted-foreground mt-0.5 block truncate text-xs">
                  by {entry.actorId} · {entry.resourceType}
                  {entry.resourceId ? ` ${entry.resourceId}` : ''}
                </span>
              </div>
              <span className="text-muted-foreground shrink-0 text-xs">
                {formatDistanceToNow(new Date(entry.createdAt), { addSuffix: true })}
              </span>
            </div>
          ))}
        </CardContent>
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>New delegation grant</DialogTitle>
            <DialogDescription>
              The assistant must already have a Calendium account and accept the grant before
              they can act for you.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="delegation-email">Assistant email</Label>
              <Input
                id="delegation-email"
                type="email"
                value={assistantEmail}
                onChange={(e) => setAssistantEmail(e.target.value)}
                placeholder="assistant@example.com"
              />
            </div>
            <fieldset className="grid gap-2">
              <legend className="pb-1 text-sm font-medium">Scopes</legend>
              {DELEGATION_SCOPES.map((scope) => (
                <label key={scope} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="accent-primary size-4"
                    checked={scopes.includes(scope)}
                    onChange={() => toggleScope(scope)}
                  />
                  {SCOPE_LABELS[scope]}
                </label>
              ))}
            </fieldset>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => create.mutate()}
              disabled={
                !EMAIL_RE.test(assistantEmail.trim()) || scopes.length === 0 || create.isPending
              }
            >
              Create grant
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
