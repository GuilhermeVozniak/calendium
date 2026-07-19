'use client';

import * as React from 'react';
import type { Comment } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { getApiClient } from '@/lib/api';
import { formatListTime } from '@/lib/mail-utils';

interface CommentsPanelProps {
  threadId: string;
  onClose: () => void;
}

/**
 * Team comments side panel for a thread (M2.7). Comments piggyback on the
 * thread being visible to the team (you own it, or an unrevoked team share
 * exists) — a 404 from the API means "not visible to this team" and renders
 * a neutral explainer, never an existence oracle. Bodies are plain user text
 * rendered escaped (React text nodes); @email mentions are resolved
 * server-side.
 */
export function CommentsPanel({ threadId, onClose }: CommentsPanelProps) {
  const api = getApiClient();
  const queryClient = useQueryClient();
  const [body, setBody] = React.useState('');

  const meQuery = useQuery({ queryKey: ['me'], queryFn: () => api.getMe() });
  const teamsQuery = useQuery({ queryKey: ['teams'], queryFn: () => api.listTeams() });
  const teams = teamsQuery.data ?? [];
  const [teamId, setTeamId] = React.useState('');
  React.useEffect(() => {
    if (!teamId && teams.length > 0) setTeamId(teams[0]!.id);
  }, [teams, teamId]);

  const commentsQuery = useQuery<Comment[], Error>({
    queryKey: ['thread-comments', threadId, teamId],
    queryFn: async () => (await api.listComments(threadId, teamId)).comments,
    enabled: teamId !== '',
    retry: false,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['thread-comments', threadId, teamId] });

  const addMutation = useMutation({
    mutationFn: (text: string) => api.addComment(threadId, { teamId, body: text }),
    onSuccess: () => {
      setBody('');
      void invalidate();
    },
    onError: () => toast.error('Could not post your comment.'),
  });

  const deleteMutation = useMutation({
    mutationFn: (commentId: string) => api.deleteComment(commentId),
    onSuccess: () => void invalidate(),
    onError: () => toast.error('Could not delete the comment.'),
  });

  const myId = meQuery.data?.id ?? null;
  const comments = commentsQuery.data ?? [];
  const notVisible =
    commentsQuery.error instanceof ApiRequestError && commentsQuery.error.status === 404;

  function submit() {
    const text = body.trim();
    if (!text || !teamId) return;
    addMutation.mutate(text);
  }

  return (
    <aside className="flex w-80 shrink-0 flex-col border-l">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2.5">
        <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">Team comments</h2>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          aria-label="Close comments"
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
      </div>

      {teams.length === 0 && !teamsQuery.isPending ? (
        <p className="text-muted-foreground px-3 py-6 text-sm">
          Join a team to discuss conversations together.
        </p>
      ) : (
        <>
          {teams.length > 1 && (
            <div className="flex flex-col gap-1 border-b px-3 py-2">
              <Label htmlFor="comments-team">Team</Label>
              <select
                id="comments-team"
                className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm shadow-xs"
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

          <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
            {commentsQuery.isPending && teamId !== '' ? (
              <div className="flex justify-center py-6">
                <Loader2 className="text-muted-foreground size-4 animate-spin" />
              </div>
            ) : notVisible ? (
              <p className="text-muted-foreground py-6 text-sm">
                Comments open up once this conversation is shared with the team.
              </p>
            ) : comments.length === 0 ? (
              <p className="text-muted-foreground py-6 text-sm">
                No comments yet — start the discussion.
              </p>
            ) : (
              <ul className="flex flex-col gap-3 py-1">
                {comments.map((comment) => {
                  const mine = myId !== null && comment.authorId === myId;
                  return (
                    <li key={comment.id} className="group rounded-md border px-2.5 py-2">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="text-xs font-medium" title={comment.authorId}>
                          {mine ? 'You' : `Teammate ${comment.authorId.slice(0, 8)}`}
                        </span>
                        <span className="text-muted-foreground shrink-0 text-xs">
                          {formatListTime(comment.createdAt)}
                        </span>
                      </div>
                      <p className="mt-1 text-sm whitespace-pre-wrap">{comment.body}</p>
                      {mine && (
                        <div className="mt-1 flex justify-end">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-6 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                            aria-label="Delete comment"
                            disabled={deleteMutation.isPending}
                            onClick={() => deleteMutation.mutate(comment.id)}
                          >
                            <Trash2 className="size-3.5" />
                          </Button>
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          <div className="flex shrink-0 flex-col gap-2 border-t px-3 py-2.5">
            <Textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="Comment for your team… use @email to mention"
              aria-label="Comment"
              rows={2}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  submit();
                }
              }}
            />
            <Button
              type="button"
              size="sm"
              className="w-fit self-end"
              disabled={!body.trim() || !teamId || addMutation.isPending}
              onClick={submit}
            >
              {addMutation.isPending ? <Loader2 className="animate-spin" /> : null}
              Comment
            </Button>
          </div>
        </>
      )}
    </aside>
  );
}
