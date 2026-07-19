'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import type {
  InstanceCapabilities,
  IntegrationConnection,
  IntegrationVendor,
} from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { getApiClient } from '@/lib/api';

interface VendorMeta {
  vendor: IntegrationVendor;
  name: string;
  description: string;
}

const VENDORS: VendorMeta[] = [
  {
    vendor: 'todoist',
    name: 'Todoist',
    description: 'Mirror your Todoist tasks into the calendar task rail.',
  },
  {
    vendor: 'hubspot',
    name: 'HubSpot',
    description: 'CRM context on contacts and one-click email logging.',
  },
];

/**
 * Settings » Integrations: per-user vendor OAuth connections (M2.8 Task 9).
 * Vendors are shown only when the server advertises them in
 * InstanceInfo.capabilities — an unadvertised vendor's endpoints answer 501.
 */
export function IntegrationsSection({
  capabilities,
}: {
  capabilities: InstanceCapabilities | undefined;
}) {
  const queryClient = useQueryClient();
  const vendors = VENDORS.filter((m) => capabilities?.[m.vendor]);

  const connectionsQuery = useQuery({
    queryKey: ['integrations'],
    queryFn: () => getApiClient().listIntegrations(),
    enabled: vendors.length > 0,
  });

  const connect = useMutation({
    mutationFn: (vendor: IntegrationVendor) =>
      getApiClient().connectIntegration(
        vendor,
        `${window.location.origin}/settings?tab=integrations`
      ),
    onSuccess: ({ url }) => window.location.assign(url),
    onError: () => toast.error('Could not start the connect flow - is the API server running?'),
  });

  const disconnect = useMutation({
    mutationFn: (id: string) => getApiClient().disconnectIntegration(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<IntegrationConnection[]>(['integrations'], (prev) =>
        prev?.filter((c) => c.id !== id)
      );
      toast.success('Integration disconnected');
    },
    onError: () => toast.error('Could not disconnect the integration'),
  });

  if (vendors.length === 0) return null;

  const connections = connectionsQuery.data ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Integrations</CardTitle>
        <CardDescription>
          Connect external tools to Calendium. Each connection is yours alone and can be
          revoked here at any time.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {connectionsQuery.isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : (
          vendors.map((meta) => {
            const connection = connections.find((c) => c.vendor === meta.vendor);
            return (
              <div
                key={meta.vendor}
                className="flex items-center justify-between gap-4 rounded-lg border p-4"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{meta.name}</span>
                    {connection && (
                      <Badge variant={connection.status === 'active' ? 'secondary' : 'destructive'}>
                        {connection.status === 'active' ? 'Connected' : 'Error'}
                      </Badge>
                    )}
                  </div>
                  <p className="mt-0.5 truncate text-sm text-muted-foreground">
                    {connection
                      ? connection.status === 'error' && connection.lastError
                        ? connection.lastError
                        : connection.externalAccount || meta.description
                      : meta.description}
                  </p>
                </div>
                {connection ? (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={`Disconnect ${meta.name}`}
                    disabled={disconnect.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Disconnect ${meta.name}? ${
                            meta.vendor === 'todoist'
                              ? 'Mirrored Todoist tasks will be removed from Calendium.'
                              : 'CRM context will no longer appear.'
                          }`
                        )
                      ) {
                        disconnect.mutate(connection.id);
                      }
                    }}
                  >
                    Disconnect
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    aria-label={`Connect ${meta.name}`}
                    disabled={connect.isPending}
                    onClick={() => connect.mutate(meta.vendor)}
                  >
                    Connect
                  </Button>
                )}
              </div>
            );
          })
        )}
      </CardContent>
    </Card>
  );
}
