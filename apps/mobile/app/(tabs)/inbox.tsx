import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { relativeTime } from '@/lib/format';
import { isDemoMode, mockThreadPage, withMockFallback } from '@/lib/mock';
import { cn } from '@/lib/utils';
import type { InboxSplit, Page, Thread, ThreadAction } from '@calendium/shared';
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { ArchiveIcon, ClockIcon, InboxIcon, SquarePenIcon, StarIcon, XIcon } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, FlatList, Pressable, RefreshControl, ScrollView, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

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
  const [selected, setSelected] = React.useState<Thread | null>(null);

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
    onError: (error, _vars, context) => {
      // Demo mode has no backend, so the optimistic update stands; otherwise a
      // real rejection (paywall, validation, server error) must not look like
      // success — revert and tell the user.
      if (isDemoMode()) return;
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
    onError: (error, _vars, context) => {
      if (isDemoMode()) return;
      if (context?.previous) {
        queryClient.setQueryData(['threads', context.split], context.previous);
      }
      Alert.alert('Could not snooze', error instanceof Error ? error.message : 'Please try again.');
    },
  });

  const promptSnooze = (thread: Thread) => {
    setSelected(null);
    const laterToday = new Date(Date.now() + 3 * 60 * 60 * 1000);
    const tomorrow = new Date();
    tomorrow.setDate(tomorrow.getDate() + 1);
    tomorrow.setHours(8, 0, 0, 0);
    const nextWeek = new Date();
    nextWeek.setDate(nextWeek.getDate() + 7);
    nextWeek.setHours(8, 0, 0, 0);
    Alert.alert('Snooze until', undefined, [
      {
        text: 'Later today',
        onPress: () => snoozeMutation.mutate({ thread, until: laterToday.toISOString() }),
      },
      {
        text: 'Tomorrow 8 AM',
        onPress: () => snoozeMutation.mutate({ thread, until: tomorrow.toISOString() }),
      },
      {
        text: 'Next week',
        onPress: () => snoozeMutation.mutate({ thread, until: nextWeek.toISOString() }),
      },
      { text: 'Cancel', style: 'cancel' },
    ]);
  };

  const openThread = (thread: Thread) => {
    setSelected(null);
    // Read state is recorded server-side by the thread screen (POST .../open)
    // and reconciled back into this list, so we no longer fake `unread` here —
    // the dot reflects real data only.
    router.push({ pathname: '/thread/[id]', params: { id: thread.id } });
  };

  return (
    <View className="flex-1 bg-background" style={{ paddingTop: insets.top }}>
      {/* Header */}
      <View className="flex-row items-center justify-between px-4 pb-2 pt-1">
        <Text variant="h3">Inbox</Text>
        <Button
          size="icon"
          variant="ghost"
          className="rounded-full"
          onPress={() => router.push('/compose')}>
          <Icon as={SquarePenIcon} className="size-5" />
        </Button>
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
            <View className="items-center gap-2 px-8 pt-24">
              <Icon as={InboxIcon} className="size-8 text-muted-foreground" />
              <Text className="text-center text-sm text-muted-foreground">
                {threadsQuery.isError
                  ? "Couldn't load this inbox."
                  : `Inbox zero — nothing in ${SPLITS.find((s) => s.key === split)?.label ?? split}.`}
              </Text>
            </View>
          }
          renderItem={({ item }) => (
            <ThreadRow
              thread={item}
              highlighted={selected?.id === item.id}
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
    </View>
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
