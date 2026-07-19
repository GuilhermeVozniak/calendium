import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { relativeTime } from '@/lib/format';
import { isDemoMode, mockOpens, mockThreadPage, withMockFallback } from '@/lib/mock';
import { isNetworkError, queueIfOffline, useQueuedCount } from '@/lib/offline';
import { cn } from '@/lib/utils';
import type { InboxSplit, OpenEvent, Page, Thread, ThreadAction } from '@calendium/shared';
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import {
  ArchiveIcon,
  ClockIcon,
  CloudOffIcon,
  EllipsisVerticalIcon,
  EyeIcon,
  InboxIcon,
  SparklesIcon,
  SquarePenIcon,
  StarIcon,
  XIcon,
} from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, FlatList, Pressable, RefreshControl, ScrollView, View } from 'react-native';
import ReanimatedSwipeable, {
  type SwipeableMethods,
} from 'react-native-gesture-handler/ReanimatedSwipeable';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { nextAfterRemoval, snoozePresets, zeroCutoffs } from '@/lib/triage';

// All InboxSplit categories, in the order they surface in the split-inbox rail.
// Must stay in sync with the shared InboxSplit union so every backend-classified
// thread is reachable on mobile.
const SPLITS: { key: InboxSplit; label: string }[] = [
  { key: 'important', label: 'Important' },
  { key: 'vip', label: 'VIP' },
  { key: 'team', label: 'Team' },
  { key: 'calendar', label: 'Calendar' },
  { key: 'news', label: 'News' },
  { key: 'social', label: 'Social' },
  { key: 'other', label: 'Other' },
];

// The inbox list is cursor-paginated (useInfiniteQuery), so its cache holds
// `InfiniteData<Page<Thread>>`; helpers below map over every loaded page.
type ThreadsData = InfiniteData<Page<Thread>>;

export default function InboxScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const queryClient = useQueryClient();
  const [split, setSplit] = React.useState<InboxSplit>('important');
  const queuedCount = useQueuedCount();
  const [selected, setSelected] = React.useState<Thread | null>(null);
  const [opensOpen, setOpensOpen] = React.useState(false);

  const threadsQuery = useInfiniteQuery({
    queryKey: ['threads', split],
    queryFn: ({ pageParam }) =>
      withMockFallback(
        () => api.listThreads({ split, limit: 50, cursor: pageParam ?? undefined }),
        () => mockThreadPage(split)
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });

  const threads = React.useMemo(
    () => threadsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [threadsQuery.data]
  );

  // Recent Opens sheet (M2.5): sent messages the recipient has opened,
  // newest first. Only fetched once the sheet is actually opened.
  const opensQuery = useInfiniteQuery({
    queryKey: ['opens'],
    enabled: opensOpen,
    queryFn: ({ pageParam }) =>
      withMockFallback(
        () => api.listOpens({ limit: 20, cursor: pageParam ?? undefined }),
        () => mockOpens()
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  });
  const opens = React.useMemo(
    () => opensQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [opensQuery.data]
  );

  const updateList = React.useCallback(
    (listSplit: InboxSplit, updater: (threads: Thread[]) => Thread[]) => {
      queryClient.setQueryData<ThreadsData>(['threads', listSplit], (data) =>
        data
          ? { ...data, pages: data.pages.map((page) => ({ ...page, items: updater(page.items) })) }
          : data
      );
    },
    [queryClient]
  );

  const actMutation = useMutation({
    mutationFn: ({ thread, action }: { thread: Thread; action: ThreadAction }) =>
      api.actOnThread(thread.id, action),
    onMutate: ({ thread, action }) => {
      const previous = queryClient.getQueryData<ThreadsData>(['threads', split]);
      if (action === 'archive') {
        updateList(split, (items) => items.filter((t) => t.id !== thread.id));
      } else if (action === 'star' || action === 'unstar') {
        updateList(split, (items) =>
          items.map((t) => (t.id === thread.id ? { ...t, starred: action === 'star' } : t))
        );
      }
      return { previous, split };
    },
    onError: (error, { thread, action }, context) => {
      // Demo mode has no backend, so the optimistic update stands; otherwise a
      // real rejection (paywall, validation, server error) must not look like
      // success — revert and tell the user. A NETWORK failure is different:
      // the action is durably queued for replay, so the optimistic state stays
      // as an honest pending change (surfaced by the header's queued badge).
      if (isDemoMode()) return;
      if (isNetworkError(error)) {
        void queueIfOffline(error, { kind: 'thread_action', threadId: thread.id, action });
        return;
      }
      if (context?.previous) {
        queryClient.setQueryData(['threads', context.split], context.previous);
      }
      Alert.alert('Action failed', error instanceof Error ? error.message : 'Please try again.');
    },
  });

  const snoozeMutation = useMutation({
    mutationFn: ({ thread, until }: { thread: Thread; until: string }) =>
      api.snoozeThread(thread.id, until),
    onMutate: ({ thread }) => {
      const previous = queryClient.getQueryData<ThreadsData>(['threads', split]);
      updateList(split, (items) => items.filter((t) => t.id !== thread.id));
      return { previous, split };
    },
    onError: (error, { thread, until }, context) => {
      if (isDemoMode()) return;
      if (isNetworkError(error)) {
        // Offline: keep the optimistic removal as a queued pending change.
        void queueIfOffline(error, { kind: 'thread_snooze', threadId: thread.id, until });
        return;
      }
      if (context?.previous) {
        queryClient.setQueryData(['threads', context.split], context.previous);
      }
      Alert.alert('Could not snooze', error instanceof Error ? error.message : 'Please try again.');
    },
  });

  const promptSnooze = (thread: Thread) => {
    setSelected(null);
    Alert.alert('Snooze until', undefined, [
      ...snoozePresets().map((preset) => ({
        text: preset.label,
        onPress: () => snoozeMutation.mutate({ thread, until: preset.until.toISOString() }),
      })),
      { text: 'Cancel', style: 'cancel' as const },
    ]);
  };

  // "Get Me To Zero": bulk-archives every split's cached threads older than the
  // chosen cutoff. Optimistic removal mirrors actMutation/snoozeMutation above;
  // demo mode has no backend, so the local count stands as the real outcome.
  const zeroMutation = useMutation({
    mutationFn: (iso: string) => api.archiveOlderThan(iso),
    onMutate: (iso) => {
      const previous: Partial<Record<InboxSplit, ThreadsData | undefined>> = {};
      let count = 0;
      const cutoff = Date.parse(iso);
      for (const s of SPLITS) {
        previous[s.key] = queryClient.getQueryData<ThreadsData>(['threads', s.key]);
        updateList(s.key, (items) =>
          items.filter((t) => {
            const stale = Date.parse(t.lastMessageAt) < cutoff;
            if (stale) count++;
            return !stale;
          })
        );
      }
      return { previous, count };
    },
    onSuccess: (result) => {
      // The optimistic removal above is a client-side date-cutoff guess; the
      // server's real inbox-membership rules (and any other device's writes)
      // can differ, so reconcile every split's cache against the server
      // instead of trusting the optimistic state as final (mirrors web's
      // getMeToZero in lib/use-mail.ts).
      void queryClient.invalidateQueries({ queryKey: ['threads'] });
      Alert.alert(
        'Get Me To Zero',
        `Archived ${result.archivedCount} conversation${result.archivedCount === 1 ? '' : 's'}.`
      );
    },
    onError: (error, _iso, context) => {
      if (isDemoMode()) {
        Alert.alert(
          'Get Me To Zero',
          `Archived ${context?.count ?? 0} conversation${context?.count === 1 ? '' : 's'}.`
        );
        return;
      }
      if (context?.previous) {
        for (const s of SPLITS) {
          const data = context.previous[s.key];
          if (data) queryClient.setQueryData(['threads', s.key], data);
        }
      }
      Alert.alert('Could not run Get Me To Zero', error instanceof Error ? error.message : 'Please try again.');
    },
  });

  const promptGetMeToZero = () => {
    Alert.alert('Get Me To Zero', undefined, [
      ...zeroCutoffs().map((cutoff) => ({
        text: cutoff.label,
        onPress: () => zeroMutation.mutate(cutoff.iso),
      })),
      { text: 'Cancel', style: 'cancel' as const },
    ]);
  };

  const openThread = (thread: Thread) => {
    setSelected(null);
    // Read state is recorded server-side by the thread screen (POST .../open)
    // and reconciled back into this list, so we no longer fake `unread` here —
    // the dot reflects real data only.
    // nextId lets the thread screen auto-advance straight to the next
    // conversation after an archive, instead of dropping back to this list.
    const nextId = nextAfterRemoval(
      threads.map((t) => t.id),
      thread.id
    );
    router.push({
      pathname: '/thread/[id]',
      params: nextId ? { id: thread.id, nextId } : { id: thread.id },
    });
  };

  return (
    <View className="flex-1 bg-background" style={{ paddingTop: insets.top }}>
      {/* Header */}
      <View className="flex-row items-center justify-between px-4 pb-2 pt-1">
        <View className="flex-row items-center gap-2">
          <Text variant="h3">Inbox</Text>
          {queuedCount > 0 && (
            // Real pending state: offline actions durably queued, not yet
            // confirmed by the server. Clears only when replay succeeds.
            <View
              testID="outbox-badge"
              className="flex-row items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5">
              <Icon as={CloudOffIcon} className="size-3.5 text-amber-600" />
              <Text className="text-xs font-medium text-amber-600">{queuedCount} queued</Text>
            </View>
          )}
        </View>
        <View className="flex-row items-center gap-1">
          <Button
            size="icon"
            variant="ghost"
            className="rounded-full"
            onPress={() => setOpensOpen(true)}
            testID="open-opens-sheet">
            <Icon as={EyeIcon} className="size-5" />
          </Button>
          <Button size="icon" variant="ghost" className="rounded-full" onPress={promptGetMeToZero}>
            <Icon as={EllipsisVerticalIcon} className="size-5" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="rounded-full"
            onPress={() => router.push('/compose')}>
            <Icon as={SquarePenIcon} className="size-5" />
          </Button>
        </View>
      </View>

      {/* Split chips */}
      <View>
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerClassName="gap-2 px-4 pb-3">
          {SPLITS.map((s) => {
            const active = split === s.key;
            return (
              <Pressable
                key={s.key}
                onPress={() => setSplit(s.key)}
                className={cn(
                  'h-8 flex-row items-center rounded-full border px-3.5',
                  active ? 'border-primary bg-primary' : 'border-border bg-background active:bg-accent'
                )}>
                <Text
                  className={cn(
                    'text-[13px] font-medium',
                    active ? 'text-primary-foreground' : 'text-muted-foreground'
                  )}>
                  {s.label}
                </Text>
              </Pressable>
            );
          })}
        </ScrollView>
      </View>

      {/* Thread list */}
      {threadsQuery.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator size="large" />
        </View>
      ) : (
        <FlatList
          data={threads}
          keyExtractor={(t) => t.id}
          contentContainerClassName="pb-24"
          ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />}
          refreshControl={
            <RefreshControl
              refreshing={threadsQuery.isRefetching && !threadsQuery.isFetchingNextPage}
              onRefresh={() => threadsQuery.refetch()}
            />
          }
          onEndReachedThreshold={0.5}
          onEndReached={() => {
            if (threadsQuery.hasNextPage && !threadsQuery.isFetchingNextPage) {
              threadsQuery.fetchNextPage();
            }
          }}
          ListFooterComponent={
            threadsQuery.isFetchingNextPage ? (
              <View className="py-6">
                <ActivityIndicator />
              </View>
            ) : null
          }
          ListEmptyComponent={
            threadsQuery.isError ? (
              <View className="items-center gap-2 px-8 pt-24">
                <Icon as={InboxIcon} className="size-8 text-muted-foreground" />
                <Text className="text-center text-sm text-muted-foreground">
                  Couldn't load this inbox.
                </Text>
              </View>
            ) : (
              <View className="items-center gap-2 px-8 pt-24">
                <Icon as={SparklesIcon} className="size-8 text-muted-foreground" />
                <Text className="text-center text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  You're at Inbox Zero
                </Text>
                <Text className="text-center text-sm text-muted-foreground">
                  Nothing in {SPLITS.find((s) => s.key === split)?.label ?? split}. Enjoy the quiet.
                </Text>
              </View>
            )
          }
          renderItem={({ item }) => (
            <SwipeableThreadRow
              thread={item}
              highlighted={selected?.id === item.id}
              onArchive={() => actMutation.mutate({ thread: item, action: 'archive' })}
              onSnooze={() => promptSnooze(item)}
              onPress={() => openThread(item)}
              onLongPress={() => setSelected(item)}
            />
          )}
        />
      )}

      {/* Long-press action bar (swipe-free triage) */}
      {selected && (
        <View className="absolute inset-x-4 bottom-4 flex-row items-center gap-1 rounded-lg border border-border bg-card p-2 shadow-lg shadow-black/20">
          <View className="flex-1 pl-2">
            <Text variant="small" numberOfLines={1}>
              {selected.subject}
            </Text>
          </View>
          <Button
            size="icon"
            variant="ghost"
            onPress={() => {
              actMutation.mutate({ thread: selected, action: 'archive' });
              setSelected(null);
            }}>
            <Icon as={ArchiveIcon} className="size-5" />
          </Button>
          <Button size="icon" variant="ghost" onPress={() => promptSnooze(selected)}>
            <Icon as={ClockIcon} className="size-5" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            onPress={() => {
              actMutation.mutate({
                thread: selected,
                action: selected.starred ? 'unstar' : 'star',
              });
              setSelected(null);
            }}>
            <Icon as={StarIcon} className={cn('size-5', selected.starred && 'text-amber-500')} />
          </Button>
          <Button size="icon" variant="ghost" onPress={() => setSelected(null)}>
            <Icon as={XIcon} className="size-5 text-muted-foreground" />
          </Button>
        </View>
      )}

      {/* Recent Opens sheet (M2.5): a full-screen overlay, matching the
          absolute-overlay convention already used above for the long-press
          action bar, rather than introducing React Native's Modal API. */}
      {opensOpen && (
        <View className="absolute inset-0 bg-background" style={{ paddingTop: insets.top }}>
          <View className="flex-row items-center justify-between border-b border-border px-4 pb-2 pt-1">
            <Text variant="h3">Recent opens</Text>
            <Button
              size="icon"
              variant="ghost"
              className="rounded-full"
              onPress={() => setOpensOpen(false)}
              testID="close-opens-sheet">
              <Icon as={XIcon} className="size-5" />
            </Button>
          </View>
          {opensQuery.isLoading ? (
            <View className="flex-1 items-center justify-center">
              <ActivityIndicator size="large" />
            </View>
          ) : (
            <FlatList
              testID="opens-list"
              data={opens}
              keyExtractor={(o) => o.messageId}
              contentContainerClassName="pb-8"
              ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />}
              onEndReachedThreshold={0.5}
              onEndReached={() => {
                if (opensQuery.hasNextPage && !opensQuery.isFetchingNextPage) {
                  opensQuery.fetchNextPage();
                }
              }}
              ListFooterComponent={
                opensQuery.isFetchingNextPage ? (
                  <View className="py-6">
                    <ActivityIndicator />
                  </View>
                ) : null
              }
              ListEmptyComponent={
                <View className="items-center gap-2 px-8 pt-24">
                  <Icon as={EyeIcon} className="size-8 text-muted-foreground" />
                  <Text className="text-center text-sm text-muted-foreground">
                    {opensQuery.isError ? "Couldn't load recent opens." : 'No opens yet.'}
                  </Text>
                </View>
              }
              renderItem={({ item }: { item: OpenEvent }) => (
                <View className="gap-1 px-4 py-3">
                  <Text numberOfLines={1} className="text-sm font-medium">
                    {item.subject}
                  </Text>
                  <Text className="text-xs text-muted-foreground" numberOfLines={1}>
                    Opened {relativeTime(item.openedAt)} by{' '}
                    {item.recipients.map((r) => r.name ?? r.email).join(', ') || 'recipient'}
                  </Text>
                </View>
              )}
            />
          )}
        </View>
      )}
    </View>
  );
}

/** Swipe right → archive; swipe left → snooze picker. Long-press bar still works. */
function SwipeableThreadRow({
  thread,
  highlighted,
  onArchive,
  onSnooze,
  onPress,
  onLongPress,
}: {
  thread: Thread;
  highlighted: boolean;
  onArchive: () => void;
  onSnooze: () => void;
  onPress: () => void;
  onLongPress: () => void;
}) {
  const swipeRef = React.useRef<SwipeableMethods>(null);
  return (
    <ReanimatedSwipeable
      ref={swipeRef}
      friction={2}
      leftThreshold={64}
      rightThreshold={64}
      overshootLeft={false}
      overshootRight={false}
      onSwipeableOpen={(direction) => {
        swipeRef.current?.close();
        if (direction === 'left') {
          // Left actions revealed (swipe right) → archive.
          onArchive();
        } else {
          onSnooze();
        }
      }}
      renderLeftActions={() => (
        <View className="w-20 items-center justify-center bg-green-600">
          <Icon as={ArchiveIcon} className="size-5 text-white" />
        </View>
      )}
      renderRightActions={() => (
        <View className="w-20 items-center justify-center bg-amber-500">
          <Icon as={ClockIcon} className="size-5 text-white" />
        </View>
      )}
    >
      <ThreadRow
        thread={thread}
        highlighted={highlighted}
        onPress={onPress}
        onLongPress={onLongPress}
      />
    </ReanimatedSwipeable>
  );
}

function ThreadRow({
  thread,
  highlighted,
  onPress,
  onLongPress,
}: {
  thread: Thread;
  highlighted: boolean;
  onPress: () => void;
  onLongPress: () => void;
}) {
  const names = thread.participants
    .filter((p) => p.email !== 'you@calendium.app')
    .map((p) => p.name?.split(' ')[0] ?? p.email.split('@')[0])
    .slice(0, 3)
    .join(', ');

  return (
    <Pressable
      onPress={onPress}
      onLongPress={onLongPress}
      className={cn('flex-row items-center gap-3 px-4 py-2.5 active:bg-accent', highlighted && 'bg-accent')}>
      <View className={cn('size-2 rounded-full', thread.unread ? 'bg-primary' : 'bg-transparent')} />
      <View className="flex-1 gap-0.5">
        <View className="flex-row items-center gap-2">
          <Text
            numberOfLines={1}
            className={cn(
              'flex-1 text-sm',
              thread.unread ? 'font-semibold' : 'font-medium text-muted-foreground'
            )}>
            {names || thread.participants[0]?.email || 'Unknown'}
            {thread.messageCount > 1 ? `  · ${thread.messageCount}` : ''}
          </Text>
          {thread.starred && <Icon as={StarIcon} size={13} className="text-amber-500" />}
          <Text className="text-xs text-muted-foreground">{relativeTime(thread.lastMessageAt)}</Text>
        </View>
        <Text numberOfLines={1}>
          <Text className={cn('text-sm', thread.unread ? 'font-medium' : 'text-muted-foreground')}>
            {thread.subject}
          </Text>
          <Text className="text-sm text-muted-foreground">{`  —  ${thread.snippet}`}</Text>
        </Text>
      </View>
    </Pressable>
  );
}
