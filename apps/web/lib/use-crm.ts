import type { CrmContext, CrmEmailLogInput } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';

/**
 * CRM data layer (M2.8): contact context for the pane + explicit per-message
 * email logging. Same honesty policy as use-mail.ts — outside explicit demo
 * mode (lib/demo.ts) it never fabricates data; only when DEMO_MODE is on
 * does it fall back to the offline CRM fixture below.
 */

/** Demo CRM fixture — served ONLY when DEMO_MODE is on. */
export function getMockCrmContext(email: string): CrmContext[] {
  return [
    {
      vendor: 'hubspot',
      contact: {
        id: '301',
        email,
        name: 'Daniel Cho',
        company: 'Northwind',
        title: 'VP Procurement',
        phone: '+1 555 0100',
        owner: 'demo-owner',
        vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-1/301',
      },
      deals: [
        {
          id: '9001',
          name: 'FY27 Renewal',
          stage: 'Contract sent',
          amount: 48000,
          closeDate: '2026-08-01T00:00:00Z',
          vendorUrl: 'https://app.hubspot.com/contacts/424242/record/0-3/9001',
        },
      ],
    },
  ];
}

/**
 * CRM context for one email: one entry per connected CRM vendor. Empty array
 * (or an error — e.g. 501 when the instance has no CRM configured) makes
 * consumers hide their CRM surface entirely.
 */
export function useCrmContext(email: string | null) {
  return useQuery({
    queryKey: ['crm-context', email?.toLowerCase() ?? null],
    enabled: !!email,
    staleTime: 5 * 60 * 1000,
    retry: false,
    queryFn: async (): Promise<CrmContext[]> => {
      if (!email) return [];
      try {
        return await getApiClient().getCrmContext(email);
      } catch (err) {
        if (DEMO_MODE) return getMockCrmContext(email);
        throw err;
      }
    },
  });
}

/**
 * Log ONE email to the connected CRM — always an explicit user action from
 * the message overflow menu, never called automatically or in bulk.
 */
export async function logCrmEmail(input: CrmEmailLogInput): Promise<void> {
  try {
    await getApiClient().logCrmEmail(input);
  } catch (err) {
    if (!DEMO_MODE) throw err;
  }
}
