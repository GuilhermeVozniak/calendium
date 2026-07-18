import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { mockAskCited, withMockFallback } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import { ApiRequestError, type AiAskResponse, type AiSource } from '@calendium/shared';
import { useMutation } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { SearchIcon, SparklesIcon, XIcon } from 'lucide-react-native';
import * as React from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

/** Honest, specific copy for the daily AI cap (backend returns 429 for it). */
function askErrorMessage(error: unknown): string {
  if (error instanceof ApiRequestError && error.status === 429) {
    return "You've reached today's AI limit — try again tomorrow.";
  }
  return error instanceof Error ? error.message : "Couldn't get an answer. Please try again.";
}

/**
 * Task 17: Ask AI as a modal reached from the tab bar's search/AI button
 * (mobile has no persistent sidebar). Same cited Q&A contract as the other
 * clients (`aiAskCited`) — answers cite real threads, and tapping a source
 * opens that thread.
 */
export default function AskAiScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { config } = useServerConfig();
  const aiEnabled = config?.features?.ai ?? false;
  const [question, setQuestion] = React.useState('');
  const [result, setResult] = React.useState<AiAskResponse | null>(null);

  const ask = useMutation({
    mutationFn: (q: string) =>
      withMockFallback(
        () => api.aiAskCited({ question: q }),
        () => mockAskCited(q)
      ),
    onSuccess: (res) => setResult(res),
  });

  const submit = () => {
    const q = question.trim();
    if (!q) return;
    setResult(null);
    ask.mutate(q);
  };

  const openSource = (source: AiSource) => {
    router.back();
    router.push({ pathname: '/thread/[id]', params: { id: source.threadId } });
  };

  return (
    <KeyboardAvoidingView
      className="flex-1 bg-background"
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      style={{ paddingTop: Math.max(insets.top, 12) }}>
      <View className="flex-row items-center justify-between px-3 pb-2">
        <Button size="icon" variant="ghost" className="rounded-full" onPress={() => router.back()}>
          <Icon as={XIcon} className="size-5" />
        </Button>
        <Text className="font-semibold">Ask AI</Text>
        <View className="size-10" />
      </View>

      {!aiEnabled ? (
        <View className="flex-1 items-center justify-center px-8">
          <Text className="text-center text-sm text-muted-foreground">
            AI features are disabled on this server.
          </Text>
        </View>
      ) : (
        <ScrollView
          className="flex-1"
          contentContainerClassName="gap-4 px-4 pb-8"
          keyboardShouldPersistTaps="handled">
          <View className="flex-row items-center gap-2">
            <Input
              value={question}
              onChangeText={setQuestion}
              placeholder="Ask about your mailbox…"
              className="flex-1"
              returnKeyType="search"
              onSubmitEditing={submit}
              editable={!ask.isPending}
            />
            <Button
              size="icon"
              className="rounded-full"
              onPress={submit}
              disabled={ask.isPending || question.trim().length === 0}
              testID="ask-submit">
              {ask.isPending ? (
                <ActivityIndicator size="small" color="white" />
              ) : (
                <Icon as={SearchIcon} className="size-4 text-primary-foreground" />
              )}
            </Button>
          </View>

          {ask.isPending ? (
            <View className="items-center gap-2 p-6">
              <ActivityIndicator />
              <Text className="text-sm text-muted-foreground">Asking…</Text>
            </View>
          ) : ask.isError ? (
            <Text className="text-sm text-muted-foreground">{askErrorMessage(ask.error)}</Text>
          ) : result ? (
            <View className="gap-4">
              <View className="flex-row items-start gap-2 rounded-lg border border-border bg-card p-4">
                <Icon as={SparklesIcon} className="mt-0.5 size-4 text-muted-foreground" />
                <Text className="flex-1 text-sm leading-6">{result.answer}</Text>
              </View>

              {result.sources.length > 0 && (
                <View className="gap-2">
                  <Text className="px-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                    Sources
                  </Text>
                  <View className="overflow-hidden rounded-lg border border-border bg-card">
                    {result.sources.map((source, i) => (
                      <Pressable
                        key={`${source.threadId}_${source.messageId ?? source.subject}`}
                        onPress={() => openSource(source)}
                        className={`gap-1 p-4 active:bg-accent ${i > 0 ? 'border-t border-border' : ''}`}>
                        <Text className="text-sm font-medium" numberOfLines={1}>
                          {source.subject}
                        </Text>
                        <Text className="text-xs text-muted-foreground" numberOfLines={2}>
                          {source.snippet}
                        </Text>
                      </Pressable>
                    ))}
                  </View>
                </View>
              )}
            </View>
          ) : null}
        </ScrollView>
      )}
    </KeyboardAvoidingView>
  );
}
