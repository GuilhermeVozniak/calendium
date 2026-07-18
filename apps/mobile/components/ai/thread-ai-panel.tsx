import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import { ApiRequestError } from '@calendium/shared';
import { SparklesIcon } from 'lucide-react-native';
import { ActivityIndicator, Pressable, ScrollView, View } from 'react-native';

/** Honest, specific copy for the daily AI cap (backend returns 429 for it). */
function repliesErrorMessage(error: unknown): string {
  if (error instanceof ApiRequestError && error.status === 429) {
    return "You've reached today's AI limit — try again tomorrow.";
  }
  return "Couldn't load suggested replies.";
}

export interface ThreadAiPanelProps {
  /** Server + build gate (config.features.ai); the panel renders nothing when false. */
  enabled: boolean;
  /** AI-generated thread summary (Thread.summary); absent until generated server-side. */
  summary?: string;
  repliesLoading: boolean;
  repliesError?: unknown;
  replies: string[];
  /** Prefills the reply composer with the tapped suggestion. */
  onSelectReply: (text: string) => void;
}

/**
 * Task 17: AI surfacing on the thread screen — a summary line (from the
 * already-fetched `thread.summary`, no extra request) plus tappable
 * instant-reply chips (GET .../instant-replies). Renders nothing when AI is
 * disabled for this server, and nothing at all once loaded if there is
 * neither a summary nor any replies (never fabricates content).
 */
export function ThreadAiPanel({
  enabled,
  summary,
  repliesLoading,
  repliesError,
  replies,
  onSelectReply,
}: ThreadAiPanelProps) {
  if (!enabled) return null;

  const hasReplies = replies.length > 0;
  const showReplyStatus = repliesLoading || !!repliesError || hasReplies;
  if (!summary && !showReplyStatus) return null;

  return (
    <View className="gap-2 border-b border-border bg-muted/30 px-4 py-3">
      {summary ? (
        <View className="flex-row items-start gap-2">
          <Icon as={SparklesIcon} className="mt-0.5 size-4 text-muted-foreground" />
          <Text className="flex-1 text-sm text-muted-foreground">{summary}</Text>
        </View>
      ) : null}

      {repliesLoading ? (
        <View className="flex-row items-center gap-2">
          <ActivityIndicator size="small" />
          <Text className="text-xs text-muted-foreground">Loading suggested replies…</Text>
        </View>
      ) : repliesError ? (
        <Text className="text-xs text-muted-foreground">{repliesErrorMessage(repliesError)}</Text>
      ) : hasReplies ? (
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerClassName="gap-2">
          {replies.map((reply) => (
            <Pressable
              key={reply}
              onPress={() => onSelectReply(reply)}
              className="rounded-full border border-border bg-background px-3 py-1.5 active:bg-accent">
              <Text className="text-xs" numberOfLines={1}>
                {reply}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
      ) : null}
    </View>
  );
}
