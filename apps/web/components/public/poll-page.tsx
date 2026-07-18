'use client';

import * as React from 'react';
import type { PollTally, PollVoteChoice, PublicPoll } from '@calendium/shared';
import { ApiRequestError, fetchPublicPoll, votePublicPoll } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { env } from '@/lib/env';

/**
 * localStorage key for the returning-voter convenience prefill (name/email
 * only — never choices, which always come fresh from the visible ballot).
 * Scoped per-browser, not per-poll: the same voter reusing the same browser
 * across different polls shouldn't have to retype their details.
 */
const VOTER_KEY = 'calendium.poll.voter';

interface StoredVoter {
  name: string;
  email: string;
}

function loadStoredVoter(): StoredVoter {
  if (typeof window === 'undefined') return { name: '', email: '' };
  try {
    const raw = window.localStorage.getItem(VOTER_KEY);
    if (!raw) return { name: '', email: '' };
    const parsed: unknown = JSON.parse(raw);
    const obj = parsed as Partial<StoredVoter> | null;
    return {
      name: typeof obj?.name === 'string' ? obj.name : '',
      email: typeof obj?.email === 'string' ? obj.email : '',
    };
  } catch {
    return { name: '', email: '' };
  }
}

function saveStoredVoter(voter: StoredVoter): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(VOTER_KEY, JSON.stringify(voter));
  } catch {
    // localStorage unavailable (private mode, etc.) — the ballot still
    // submits, it just won't be prefilled next time.
  }
}

function visitorTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

function allTimeZones(): string[] {
  try {
    const zones = Intl.supportedValuesOf('timeZone');
    return zones.length > 0 ? zones : [visitorTimeZone()];
  } catch {
    return [visitorTimeZone()];
  }
}

function formatOptionRange(startIso: string, endIso: string, zone: string): string {
  const start = new Date(startIso);
  const end = new Date(endIso);
  const dateFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  });
  const timeFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hour: 'numeric',
    minute: '2-digit',
  });
  return `${dateFmt.format(start)} · ${timeFmt.format(start)}–${timeFmt.format(end)}`;
}

const CHOICE_ORDER: PollVoteChoice[] = ['yes', 'if_needed', 'no'];
const CHOICE_LABELS: Record<PollVoteChoice, string> = {
  yes: 'Yes',
  if_needed: 'If needed',
  no: 'No',
};

function emptyTally(): PollTally {
  return { yes: 0, no: 0, ifNeeded: 0 };
}

function CenteredNotice({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mx-auto flex max-w-md flex-col items-center gap-2 px-4 py-24 text-center">
      <h1 className="text-lg font-semibold">{title}</h1>
      {description ? <p className="text-muted-foreground text-sm">{description}</p> : null}
    </div>
  );
}

interface BallotDraft {
  name: string;
  email: string;
  choices: Record<string, PollVoteChoice>;
}

/**
 * Public (no-auth) meeting-poll voting page: GET /v1/public/polls/{token} for
 * options + anonymized tallies, POST /v1/public/polls/{token}/votes for the
 * ballot. Honesty policy: the "thanks" state always re-renders tallies from
 * the mutation's response body, never an optimistic local guess, and 404/
 * 409/429/400 responses each get their own friendly (non-fabricated) state.
 */
export function PublicPollPage({ token }: { token: string }) {
  const queryClient = useQueryClient();
  const baseUrl = env.apiUrl;
  const queryKey = React.useMemo(() => ['public-poll', token] as const, [token]);

  const pollQuery = useQuery<PublicPoll, ApiRequestError>({
    queryKey,
    queryFn: () => fetchPublicPoll(baseUrl, token),
    retry: false,
  });

  const zones = React.useMemo(allTimeZones, []);
  const [zone, setZone] = React.useState<string>(visitorTimeZone);
  const [draft, setDraft] = React.useState<BallotDraft>({ name: '', email: '', choices: {} });
  const [submitted, setSubmitted] = React.useState(false);
  const [closedMidVote, setClosedMidVote] = React.useState(false);

  React.useEffect(() => {
    const stored = loadStoredVoter();
    if (stored.name || stored.email) {
      setDraft((prev) => ({ ...prev, name: stored.name, email: stored.email }));
    }
  }, []);

  const voteMutation = useMutation<
    PublicPoll,
    ApiRequestError,
    { voterName: string; voterEmail: string; choices: Record<string, PollVoteChoice> }
  >({
    mutationFn: (ballot) => votePublicPoll(baseUrl, token, ballot),
    onSuccess: (result, ballot) => {
      queryClient.setQueryData(queryKey, result);
      saveStoredVoter({ name: ballot.voterName, email: ballot.voterEmail });
      setSubmitted(true);
      setClosedMidVote(false);
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.status === 409) {
        setClosedMidVote(true);
        void queryClient.invalidateQueries({ queryKey });
      }
    },
  });

  if (pollQuery.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Loader2 className="text-muted-foreground size-6 animate-spin" />
      </div>
    );
  }

  if (pollQuery.isError) {
    const err = pollQuery.error;
    if (err instanceof ApiRequestError && err.status === 404) {
      return (
        <CenteredNotice
          title="Poll not found"
          description="This poll link is invalid or has been removed."
        />
      );
    }
    return (
      <CenteredNotice
        title="Something went wrong"
        description="We couldn't load this poll. Please try again in a moment."
      />
    );
  }

  const poll = pollQuery.data;
  const isConfirmed = poll.status === 'confirmed';
  const isCancelled = poll.status === 'cancelled';
  const isOpen = poll.status === 'open';
  const winnerOption = poll.options.find((o) => o.id === poll.winnerOptionId) ?? null;

  const allChosen = poll.options.every((o) => draft.choices[o.id]);
  const canSubmit = isOpen && draft.name.trim() !== '' && draft.email.trim() !== '' && allChosen;

  function setChoice(optionId: string, choice: PollVoteChoice) {
    setDraft((prev) => ({ ...prev, choices: { ...prev.choices, [optionId]: choice } }));
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    voteMutation.mutate({
      voterName: draft.name.trim(),
      voterEmail: draft.email.trim(),
      choices: draft.choices,
    });
  }

  function voteAgain() {
    setSubmitted(false);
  }

  const voteError = voteMutation.error;
  const voteErrorMessage = (() => {
    if (!voteError || closedMidVote) return null;
    if (voteError instanceof ApiRequestError) {
      if (voteError.status === 429) {
        return 'Too many attempts — please wait a moment and try again.';
      }
      if (voteError.status === 400) return voteError.message;
      if (voteError.status === 404) {
        return 'This poll link is invalid or has been removed.';
      }
    }
    return "Your vote couldn't be submitted. Please try again.";
  })();

  return (
    <div className="mx-auto max-w-2xl px-4 py-12">
      <Card>
        <CardHeader>
          <CardTitle className="text-2xl">{poll.title}</CardTitle>
          {poll.description ? <CardDescription>{poll.description}</CardDescription> : null}
          <p className="text-muted-foreground text-sm">
            {poll.durationMinutes}-minute meeting · organized by {poll.organizerName}
          </p>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          {isConfirmed ? (
            <div className="bg-muted rounded-md border p-4">
              <p className="font-medium">This poll is confirmed.</p>
              {winnerOption ? (
                <p className="text-muted-foreground text-sm">
                  {formatOptionRange(winnerOption.start, winnerOption.end, zone)}
                </p>
              ) : null}
            </div>
          ) : null}
          {isCancelled ? (
            <div className="bg-muted rounded-md border p-4">
              <p className="font-medium">This poll has been cancelled.</p>
            </div>
          ) : null}
          {closedMidVote ? (
            <div className="border-destructive rounded-md border p-4">
              <p className="font-medium">
                This poll just closed while you were voting — it can no longer accept votes.
              </p>
            </div>
          ) : null}

          {!submitted || !isOpen ? (
            <div className="flex flex-col gap-1">
              <Label htmlFor="poll-timezone">Time zone</Label>
              <select
                id="poll-timezone"
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm shadow-xs"
                value={zone}
                onChange={(e) => setZone(e.target.value)}>
                {zones.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </select>
              <p className="text-muted-foreground text-xs">Times shown in {zone}</p>
            </div>
          ) : null}

          <ul className="flex flex-col gap-3">
            {poll.options.map((option) => {
              const tally = poll.tallies[option.id] ?? emptyTally();
              const isWinner = poll.winnerOptionId === option.id;
              return (
                <li
                  key={option.id}
                  data-testid={`poll-option-${option.id}`}
                  className={`rounded-md border p-3 ${isWinner ? 'border-primary' : ''}`}>
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="font-medium">{formatOptionRange(option.start, option.end, zone)}</span>
                    <span className="text-muted-foreground text-xs">
                      {tally.yes} yes · {tally.ifNeeded} if needed · {tally.no} no
                    </span>
                  </div>
                  {isOpen && !submitted ? (
                    <div className="mt-2 flex gap-2">
                      {CHOICE_ORDER.map((choice) => (
                        <Button
                          key={choice}
                          type="button"
                          size="sm"
                          variant={draft.choices[option.id] === choice ? 'default' : 'outline'}
                          onClick={() => setChoice(option.id, choice)}>
                          {CHOICE_LABELS[choice]}
                        </Button>
                      ))}
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ul>

          {isOpen && submitted ? (
            <div className="flex flex-col gap-2 rounded-md border p-4">
              <p className="font-medium">Thanks — your vote was recorded.</p>
              <p className="text-muted-foreground text-sm">Tallies above reflect your vote.</p>
              <Button type="button" variant="outline" size="sm" className="w-fit" onClick={voteAgain}>
                Change your vote
              </Button>
            </div>
          ) : null}

          {isOpen && !submitted ? (
            <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
              <div className="flex flex-col gap-1">
                <Label htmlFor="voter-name">Your name</Label>
                <Input
                  id="voter-name"
                  value={draft.name}
                  onChange={(e) => setDraft((prev) => ({ ...prev, name: e.target.value }))}
                  required
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="voter-email">Your email</Label>
                <Input
                  id="voter-email"
                  type="email"
                  value={draft.email}
                  onChange={(e) => setDraft((prev) => ({ ...prev, email: e.target.value }))}
                  required
                />
              </div>
              {voteErrorMessage ? <p className="text-destructive text-sm">{voteErrorMessage}</p> : null}
              <Button type="submit" disabled={!canSubmit || voteMutation.isPending} className="w-fit">
                {voteMutation.isPending ? 'Submitting…' : 'Submit your vote'}
              </Button>
            </form>
          ) : null}
        </CardContent>
      </Card>
    </div>
  );
}
