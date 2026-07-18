import { ThreadAiPanel } from '@/components/ai/thread-ai-panel';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { relativeTime } from '@/lib/format';
import { isDemoMode, mockInstantReplies, mockThreadDetail, withMockFallback } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import type { Message, Page, Thread, UnsubscribeResult } from '@calendium/shared';
import {
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';
import { useLocalSearchParams, useRouter } from 'expo-router';
import { ArchiveIcon, ChevronLeftIcon, ClockIcon, MailXIcon, SendIcon } from 'lucide-react-native';
import * as React from 'react';
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  ScrollView,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { canUnsubscribe, snoozePresets, unsubscribeMessage } from '@/lib/triage';

type ThreadDetail = { thread: Thread; messages: Message[] };
// The inbox list is cursor-paginated, so its cache is InfiniteData<Page<Thread>>.
type ThreadsData = InfiniteData<Page<Thread>>;

export default function ThreadScreen() {
  const params = useLocalSearchParams<{ id: string; nextId?: string }>();
  const threadId = typeof params.id === 'string' ? params.id : '';
  // Passed by the inbox list (nextAfterRemoval) so archive can advance straight
  // to the next conversation instead of dropping back to the list.
  const nextThreadId = typeof params.nextId === 'string' ? params.nextId : undefined;
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const queryClient = useQueryClient();
  const { config } = useServerConfig();
  const aiEnabled = config?.features?.ai ?? false;
  const [reply, setReply] = React.useState('');

  const detailQuery = useQuery({
    queryKey: ['thread', threadId],
    enabled: threadId.length > 0,
    queryFn: () =>
      withMockFallback(
        () => api.getThread(threadId),
        () => mockThreadDetail(threadId)
      ),
  });

  const goBack = () => {
    if (router.canGoBack()) {
      router.back();
    } else {
      router.replace('/(tabs)/inbox');
    }
  };

  // Auto-advance after archive: prefer the next thread id the inbox list
  // handed us (nextAfterRemoval), else fall back to the normal back navigation.
  const advanceAfterArchive = () => {
    if (nextThreadId) {
      router.replace({ pathname: '/thread/[id]', params: { id: nextThreadId } });
    } else {
      goBack();
    }
  };

  const removeFromInboxList = (thread: Thread) => {
    queryClient.setQueryData<ThreadsData>(['threads', thread.split], (data) =>
      data
        ? {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              items: page.items.filter((t) => t.id !== thread.id),
            })),
          }
        : data
    );
  };

  // Reflect a persisted "opened" (read) into both this detail and the inbox list
  // so the unread dot clears from real state, not a guess.
  const markThreadRead = React.useCallback(
    (thread: Thread) => {
      const openedAt = new Date().toISOString();
      queryClient.setQueryData<ThreadDetail>(['thread', thread.id], (data) =>
        data ? { ...data, thread: { ...data.thread, unread: false, openedAt } } : data
      );
      queryClient.setQueryData<ThreadsData>(['threads', thread.split], (data) =>
        data
          ? {
              ...data,
              pages: data.pages.map((page) => ({
                ...page,
                items: page.items.map((t) =>
                  t.id === thread.id ? { ...t, unread: false, openedAt } : t
                ),
              })),
            }
          : data
      );
    },
    [queryClient]
  );

  // Records the open server-side (POST .../open, idempotent); read state is then
  // reconciled from the real result.
  const markOpened = useMutation({
    mutationFn: (id: string) => api.markThreadOpened(id),
    onSuccess: () => {
      const thread = detailQuery.data?.thread;
      if (thread) markThreadRead(thread);
    },
  });

  // Fire once per thread as soon as it loads, when it isn't already read.
  const openedRef = React.useRef<string | null>(null);
  React.useEffect(() => {
    const thread = detailQuery.data?.thread;
    if (!thread || openedRef.current === thread.id) return;
    openedRef.current = thread.id;
    if (thread.openedAt && !thread.unread) return; // already read
    if (isDemoMode()) {
      markThreadRead(thread); // demo: reflect locally, no backend
      return;
    }
    markOpened.mutate(thread.id);
    // markOpened.mutate is stable; markThreadRead is memoized.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailQuery.data?.thread, markThreadRead]);

  const archive = async () => {
    const thread = detailQuery.data?.thread;
    if (!thread) return;
    const previous = queryClient.getQueryData<ThreadsData>(['threads', thread.split]);
    removeFromInboxList(thread);
    advanceAfterArchive();
    try {
      await api.actOnThread(thread.id, 'archive');
    } catch (error) {
      if (isDemoMode()) return; // demo: keep the optimistic removal
      if (previous) queryClient.setQueryData(['threads', thread.split], previous);
      Alert.alert('Could not archive', error instanceof Error ? error.message : 'Please try again.');
    }
  };

  const snooze = () => {
    const thread = detailQuery.data?.thread;
    if (!thread) return;
    const doSnooze = async (until: string) => {
      const previous = queryClient.getQueryData<ThreadsData>(['threads', thread.split]);
      removeFromInboxList(thread);
      goBack();
      try {
        await api.snoozeThread(thread.id, until);
      } catch (error) {
        if (isDemoMode()) return; // demo: keep the optimistic removal
        if (previous) queryClient.setQueryData(['threads', thread.split], previous);
        Alert.alert('Could not snooze', error instanceof Error ? error.message : 'Please try again.');
      }
    };
    Alert.alert('Snooze until', undefined, [
      ...snoozePresets().map((preset) => ({
        text: preset.label,
        onPress: () => doSnooze(preset.until.toISOString()),
      })),
      { text: 'Cancel', style: 'cancel' as const },
    ]);
  };

  // Shared tail of a successful (real or demo) unsubscribe: open the link for
  // "link" results, tell the user what happened, then offer to archive.
  const handleUnsubscribeResult = (res: UnsubscribeResult) => {
    if (res.method === 'link' && res.url) {
      void Linking.openURL(res.url);
    }
    Alert.alert('Unsubscribe', unsubscribeMessage(res), [
      {
        text: 'OK',
        onPress: () =>
          Alert.alert('Archive all from this sender?', undefined, [
            { text: 'Archive', onPress: () => void archive() },
            { text: 'Not now', style: 'cancel' },
          ]),
      },
    ]);
  };

  const unsubscribeMutation = useMutation({
    mutationFn: (id: string) => api.unsubscribeThread(id),
    onSuccess: handleUnsubscribeResult,
    onError: (error) => {
      const thread = detailQuery.data?.thread;
      if (isDemoMode() && thread) {
        // Demo mode only: simulate the result the same way the real backend
        // would pick a method, since there is no backend to ask.
        const res: UnsubscribeResult =
          thread.unsubscribeOneClick && thread.unsubscribeUrl
            ? { method: 'one_click' }
            : thread.unsubscribeMailto
              ? { method: 'mailto' }
              : { method: 'link', url: thread.unsubscribeUrl ?? undefined };
        handleUnsubscribeResult(res);
        return;
      }
      Alert.alert('Could not unsubscribe', error instanceof Error ? error.message : 'Please try again.');
    },
  });

  const appendMessage = (message: Message) => {
    queryClient.setQueryData<ThreadDetail>(['thread', threadId], (data) =>
      data ? { ...data, messages: [...data.messages, message] } : data
    );
  };

  const sendReply = useMutation({
    mutationFn: async () => {
      const data = detailQuery.data;
      if (!data) throw new Error('Thread not loaded yet');
      const lastInbound = [...data.messages].reverse().find((m) => !m.isDraft);
      const draft = await api.saveDraft({
        accountId: data.thread.accountId,
        threadId: data.thread.id,
        to: lastInbound ? [lastInbound.from] : data.thread.participants.slice(0, 1),
        subject: data.thread.subject.startsWith('Re:')
          ? data.thread.subject
          : `Re: ${data.thread.subject}`,
        bodyHtml: `<p>${reply.replace(/\n/g, '<br/>')}</p>`,
      });
      return api.sendDraft(draft.id);
    },
    onSuccess: (message) => {
      appendMessage(message);
      setReply('');
    },
    onError: (error) => {
      const data = detailQuery.data;
      if (isDemoMode() && data) {
        // Demo mode only: reflect the reply locally (there is no backend).
        appendMessage({
          id: `local_${Date.now()}`,
          threadId: data.thread.id,
          accountId: data.thread.accountId,
          from: { name: 'You', email: 'you@calendium.app' },
          to: data.thread.participants.slice(0, 1),
          cc: [],
          bcc: [],
          subject: data.thread.subject,
          bodyHtml: `<p>${reply.replace(/\n/g, '<br/>')}</p>`,
          bodyText: reply,
          attachments: [],
          sentAt: new Date().toISOString(),
          isDraft: false,
          openedAt: null,
        });
        setReply('');
      } else {
        // A real failure never looks like success — surface it, keep the draft.
        Alert.alert('Could not send reply', error instanceof Error ? error.message : 'Unknown error');
      }
    },
  });

  const thread = detailQuery.data?.thread;
  const messages = detailQuery.data?.messages ?? [];

  // AI instant-reply suggestions (Task 17); only fetched once AI is enabled
  // for this server and the thread has loaded.
  const repliesQuery = useQuery({
    queryKey: ['instant-replies', threadId],
    enabled: aiEnabled && threadId.length > 0 && !!thread,
    queryFn: () =>
      withMockFallback(
        () => api.getInstantReplies(threadId),
        () => mockInstantReplies(threadId)
      ),
  });

  return (
    <View className="flex-1 bg-background" style={{ paddingTop: insets.top }}>
      {/* Header */}
      <View className="flex-row items-center gap-1 border-b border-border px-2 pb-2 pt-1">
        <Button size="icon" variant="ghost" className="rounded-full" onPress={goBack}>
          <Icon as={ChevronLeftIcon} className="size-6" />
        </Button>
        <View className="flex-1">
          <Text className="font-semibold" numberOfLines={1}>
            {thread?.subject ?? 'Conversation'}
          </Text>
          {thread && (
            <Text className="text-xs text-muted-foreground" numberOfLines={1}>
              {thread.messageCount} message{thread.messageCount === 1 ? '' : 's'}
            </Text>
          )}
        </View>
        <Button size="icon" variant="ghost" className="rounded-full" onPress={archive}>
          <Icon as={ArchiveIcon} className="size-5" />
        </Button>
        <Button size="icon" variant="ghost" className="rounded-full" onPress={snooze}>
          <Icon as={ClockIcon} className="size-5" />
        </Button>
      </View>

      {thread && (
        <ThreadAiPanel
          enabled={aiEnabled}
          summary={thread.summary}
          repliesLoading={repliesQuery.isLoading}
          repliesError={repliesQuery.error}
          replies={repliesQuery.data?.replies ?? []}
          onSelectReply={setReply}
        />
      )}

      {thread && canUnsubscribe(thread) && (
        <Pressable
          onPress={() => unsubscribeMutation.mutate(thread.id)}
          disabled={unsubscribeMutation.isPending}
          className="flex-row items-center gap-2 border-b border-border bg-muted/30 px-4 py-2.5 active:bg-accent">
          <Icon as={MailXIcon} className="size-4 text-muted-foreground" />
          <Text className="flex-1 text-sm text-muted-foreground">Unsubscribe from this sender</Text>
        </Pressable>
      )}

      <KeyboardAvoidingView
        className="flex-1"
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        {/* Message stack */}
        {detailQuery.isLoading ? (
          <View className="flex-1 items-center justify-center">
            <ActivityIndicator size="large" />
          </View>
        ) : detailQuery.isError ? (
          <View className="flex-1 items-center justify-center px-8">
            <Text className="text-center text-sm text-muted-foreground">
              Couldn't load this conversation.
            </Text>
          </View>
        ) : (
          <ScrollView contentContainerClassName="gap-3 p-4">
            {messages.map((message) => (
              <View key={message.id} className="gap-2 rounded-lg border border-border bg-card p-4">
                <View className="flex-row items-baseline justify-between gap-2">
                  <Text className="flex-1 text-sm font-semibold" numberOfLines={1}>
                    {message.from.name ?? message.from.email}
                  </Text>
                  <Text className="text-xs text-muted-foreground">
                    {relativeTime(message.sentAt)}
                  </Text>
                </View>
                <Text className="text-xs text-muted-foreground" numberOfLines={1}>
                  to {message.to.map((t) => t.name ?? t.email).join(', ') || 'you'}
                </Text>
                <Text className="text-sm leading-6">{message.bodyText}</Text>
              </View>
            ))}
          </ScrollView>
        )}

        {/* Reply box */}
        <View
          className="flex-row items-end gap-2 border-t border-border bg-background px-4 pt-3"
          style={{ paddingBottom: Math.max(insets.bottom, 12) }}>
          <Input
            value={reply}
            onChangeText={setReply}
            placeholder="Reply…"
            multiline
            className="h-auto max-h-32 min-h-10 flex-1 rounded-2xl py-2.5"
          />
          <Button
            size="icon"
            className="rounded-full"
            onPress={() => sendReply.mutate()}
            disabled={!reply.trim() || sendReply.isPending || !detailQuery.data}>
            <Icon as={SendIcon} className="size-4 text-primary-foreground" />
          </Button>
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}
