'use client';

import type { Message } from '@calendium/shared';
import { MoreHorizontal } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { logCrmEmail, useCrmContext } from '@/lib/use-crm';

export interface MessageCrmMenuProps {
  message: Message;
  /** Thread subject — messages carry no subject of their own. */
  subject: string;
  /** The external counterpart's address (the CRM contact to log against). */
  contactEmail: string | null;
  direction: 'inbound' | 'outbound';
}

/**
 * Per-message overflow menu with the explicit "Log to HubSpot" action
 * (M2.8). Logging is ALWAYS a deliberate per-message click — mail is never
 * exported to the CRM automatically or in bulk. The menu renders only when
 * the user actually has a connected HubSpot (via /v1/crm/context, deduped
 * with the contact pane's query).
 */
export function MessageCrmMenu({ message, subject, contactEmail, direction }: MessageCrmMenuProps) {
  const { data } = useCrmContext(contactEmail);
  const hasHubSpot = (data ?? []).some((ctx) => ctx.vendor === 'hubspot');
  if (!contactEmail || !hasHubSpot) return null;

  async function logToHubSpot() {
    try {
      await logCrmEmail({
        contactEmail: contactEmail as string,
        subject,
        bodyText: message.bodyText,
        sentAt: message.sentAt,
        direction,
      });
      toast.success('Logged to HubSpot');
    } catch {
      toast.error('Could not log to HubSpot.');
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          aria-label="Message actions"
        >
          <MoreHorizontal className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => void logToHubSpot()}>Log to HubSpot</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
