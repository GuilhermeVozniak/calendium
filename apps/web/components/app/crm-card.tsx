'use client';

import { ExternalLink } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { useCrmContext } from '@/lib/use-crm';

const VENDOR_LABELS: Record<string, string> = { hubspot: 'HubSpot' };

function formatCloseDate(iso: string | null): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

/**
 * CRM section for the contact pane (M2.8): per connected vendor, the CRM-side
 * contact (name/company/title), associated deals with stage badges, and an
 * "Open in HubSpot" deep link. Rendered ONLY when the caller has a connected
 * CRM — loading, error (including 501 feature-off), and no-connection states
 * all collapse to nothing so the pane stays honest and quiet.
 */
export function CrmCard({ email }: { email: string }) {
  const { data } = useCrmContext(email);
  const contexts = data ?? [];
  if (contexts.length === 0) return null;

  return (
    <div className="flex flex-col gap-3" data-testid="crm-card">
      {contexts.map((ctx) => {
        const vendorLabel = VENDOR_LABELS[ctx.vendor] ?? ctx.vendor;
        return (
          <div key={ctx.vendor} className="flex flex-col gap-2">
            <h3 className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
              {vendorLabel}
            </h3>
            {!ctx.contact ? (
              <p className="text-muted-foreground text-sm">Not in {vendorLabel} yet.</p>
            ) : (
              <div className="flex flex-col gap-1">
                <p className="truncate text-sm font-medium">{ctx.contact.name || ctx.contact.email}</p>
                {(ctx.contact.title || ctx.contact.company) && (
                  <p className="text-muted-foreground truncate text-xs">
                    {[ctx.contact.title, ctx.contact.company].filter(Boolean).join(' · ')}
                  </p>
                )}
                <a
                  href={ctx.contact.vendorUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-primary inline-flex items-center gap-1 text-xs hover:underline"
                >
                  <ExternalLink className="size-3" />
                  Open in {vendorLabel}
                </a>
              </div>
            )}
            {ctx.deals.length > 0 && (
              <ul className="flex flex-col gap-1.5">
                {ctx.deals.map((deal) => {
                  const close = formatCloseDate(deal.closeDate);
                  return (
                    <li key={deal.id} className="flex flex-col gap-0.5 rounded-md border px-2 py-1.5">
                      <div className="flex items-center justify-between gap-2">
                        <a
                          href={deal.vendorUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="min-w-0 truncate text-xs font-medium hover:underline"
                        >
                          {deal.name}
                        </a>
                        <Badge variant="outline" className="shrink-0 text-[0.625rem]">
                          {deal.stage}
                        </Badge>
                      </div>
                      {(deal.amount != null || close) && (
                        <p className="text-muted-foreground text-[0.6875rem]">
                          {[
                            deal.amount != null ? deal.amount.toLocaleString() : null,
                            close ? `closes ${close}` : null,
                          ]
                            .filter(Boolean)
                            .join(' · ')}
                        </p>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        );
      })}
    </div>
  );
}
