'use client';

import * as React from 'react';
import type { AiEventProposal } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { EventDialog } from '@/components/app/event-dialog';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { fetchCalendars } from '@/lib/calendar-data';
import { aiErrorMessage, runProposeEvent } from '@/lib/use-mail';

/**
 * Thread action ("Create event with AI"): fetches an AI-proposed event from
 * the thread (proposeEvent), then hands it to the existing EventDialog as
 * `defaults` so the user reviews/edits before the real createEvent call —
 * this component never creates the event itself.
 */
export function ProposeEventDialog({
  threadId,
  open,
  onOpenChange,
}: {
  threadId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [proposal, setProposal] = React.useState<AiEventProposal | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [eventDialogOpen, setEventDialogOpen] = React.useState(false);

  const calendarsQuery = useQuery({ queryKey: ['calendars'], queryFn: fetchCalendars, enabled: open });

  // A ref keeps the effect below scoped to [open, threadId] — onOpenChange is
  // a new closure each render, and reacting to it too would refire the fetch
  // whenever the parent re-renders for unrelated reasons.
  const onOpenChangeRef = React.useRef(onOpenChange);
  onOpenChangeRef.current = onOpenChange;

  React.useEffect(() => {
    if (!open || !threadId) {
      setProposal(null);
      setEventDialogOpen(false);
      return;
    }
    setProposal(null);
    setLoading(true);
    let cancelled = false;
    runProposeEvent(threadId)
      .then((res) => {
        if (cancelled) return;
        setProposal(res);
        setEventDialogOpen(true);
      })
      .catch((err) => {
        if (cancelled) return;
        toast.error(aiErrorMessage(err));
        onOpenChangeRef.current(false);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, threadId]);

  if (!open) return null;

  if (loading || !proposal) {
    return (
      <Dialog open onOpenChange={(next) => !next && onOpenChange(false)}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="sr-only">Proposing event</DialogTitle>
          <DialogDescription className="sr-only">
            AI is drafting a calendar event from this conversation
          </DialogDescription>
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <Loader2 className="text-muted-foreground size-6 animate-spin" />
            <p className="text-muted-foreground text-sm">Proposing an event from this thread…</p>
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <EventDialog
      open={eventDialogOpen}
      onOpenChange={(next) => {
        setEventDialogOpen(next);
        if (!next) onOpenChange(false);
      }}
      calendars={calendarsQuery.data ?? []}
      event={null}
      defaults={{
        title: proposal.title,
        start: proposal.start,
        end: proposal.end,
        location: proposal.location,
        description: proposal.notes,
        attendeeEmails: proposal.attendees,
      }}
    />
  );
}
