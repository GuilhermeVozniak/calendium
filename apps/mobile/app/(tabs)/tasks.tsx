import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { mockListTasks, mockSetTaskCompleted, withMockFallback } from '@/lib/mock';
import { cn } from '@/lib/utils';
import type { Task } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckIcon } from 'lucide-react-native';
import { ActivityIndicator, FlatList, Pressable, RefreshControl, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

/**
 * Mobile task list with in-place check-off (M2.8 Task 3b) — the platform
 * mirror of the web calendar's task rail. Reads /v1/tasks through the shared
 * ApiClient; explicit demo mode serves the lib/mock.ts fixtures. Check-off is
 * optimistic (the row flips before the request settles) with rollback on a
 * real failure, mirroring the web rail's use-tasks.ts pattern.
 */

function dueLabel(task: Task): string | null {
  if (!task.due) return null;
  const due = new Date(task.due);
  const now = new Date();
  const sameDay =
    due.getFullYear() === now.getFullYear() &&
    due.getMonth() === now.getMonth() &&
    due.getDate() === now.getDate();
  if (!task.allDayDue && sameDay) {
    return `Today, ${due.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`;
  }
  return due.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

type Row =
  | { key: string; kind: 'header'; label: string }
  | { key: string; kind: 'task'; task: Task };

export default function TasksScreen() {
  const insets = useSafeAreaInsets();
  const queryClient = useQueryClient();

  const tasksQuery = useQuery({
    queryKey: ['tasks'],
    queryFn: () =>
      withMockFallback(
        () => api.listTasks(),
        () => mockListTasks()
      ),
  });

  const toggle = useMutation({
    mutationFn: (task: Task) =>
      withMockFallback(
        () => (task.completedAt ? api.reopenTask(task.id) : api.completeTask(task.id)),
        () => mockSetTaskCompleted(task.id, task.completedAt === null)
      ),
    onMutate: async (task) => {
      await queryClient.cancelQueries({ queryKey: ['tasks'] });
      const previous = queryClient.getQueryData<Task[]>(['tasks']);
      queryClient.setQueryData<Task[]>(['tasks'], (prev) =>
        prev?.map((t) =>
          t.id === task.id
            ? { ...t, completedAt: task.completedAt ? null : new Date().toISOString() }
            : t
        )
      );
      return { previous };
    },
    onError: (_err, _task, context) => {
      if (context?.previous) queryClient.setQueryData(['tasks'], context.previous);
    },
    onSuccess: (updated) => {
      queryClient.setQueryData<Task[]>(['tasks'], (prev) =>
        prev?.map((t) => (t.id === updated.id ? updated : t))
      );
    },
  });

  const tasks = tasksQuery.data ?? [];
  const open = tasks
    .filter((t) => t.completedAt === null)
    .sort((a, b) => {
      const dueA = a.due ? new Date(a.due).getTime() : Number.POSITIVE_INFINITY;
      const dueB = b.due ? new Date(b.due).getTime() : Number.POSITIVE_INFINITY;
      if (dueA !== dueB) return dueA - dueB;
      return a.position - b.position;
    });
  const completed = tasks.filter((t) => t.completedAt !== null);

  const rows: Row[] = [];
  for (const task of open) rows.push({ key: task.id, kind: 'task', task });
  if (completed.length > 0) {
    rows.push({ key: 'header-completed', kind: 'header', label: `Completed (${completed.length})` });
    for (const task of completed) rows.push({ key: task.id, kind: 'task', task });
  }

  return (
    <View className="flex-1 bg-background" style={{ paddingTop: insets.top }}>
      <View className="border-b border-border px-4 pb-3 pt-2">
        <Text className="text-2xl font-bold">Tasks</Text>
      </View>
      {tasksQuery.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator size="large" />
        </View>
      ) : tasksQuery.isError ? (
        <View className="flex-1 items-center justify-center gap-2 px-6">
          <Text className="text-center font-medium">Couldn't load your tasks</Text>
          <Pressable
            testID="tasks-retry"
            onPress={() => void tasksQuery.refetch()}
            className="rounded-md border border-border px-3 py-1.5">
            <Text className="text-sm">Retry</Text>
          </Pressable>
        </View>
      ) : (
        <FlatList
          data={rows}
          keyExtractor={(row) => row.key}
          className="flex-1"
          refreshControl={
            <RefreshControl
              refreshing={tasksQuery.isRefetching}
              onRefresh={() => void tasksQuery.refetch()}
            />
          }
          ListEmptyComponent={
            <View className="items-center px-6 py-16">
              <Text className="text-muted-foreground">No tasks yet.</Text>
            </View>
          }
          renderItem={({ item }) =>
            item.kind === 'header' ? (
              <Text className="px-4 pb-1 pt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {item.label}
              </Text>
            ) : (
              <TaskRow task={item.task} onToggle={(task) => toggle.mutate(task)} />
            )
          }
        />
      )}
    </View>
  );
}

function TaskRow({ task, onToggle }: { task: Task; onToggle: (task: Task) => void }) {
  const completed = task.completedAt !== null;
  const due = dueLabel(task);
  return (
    <View className="flex-row items-start gap-3 border-b border-border px-4 py-3">
      <Pressable
        testID={`task-toggle-${task.id}`}
        accessibilityRole="checkbox"
        accessibilityState={{ checked: completed }}
        accessibilityLabel={completed ? `Reopen "${task.title}"` : `Complete "${task.title}"`}
        onPress={() => onToggle(task)}
        className={cn(
          'mt-0.5 h-5 w-5 items-center justify-center rounded-full border',
          completed ? 'border-primary bg-primary' : 'border-muted-foreground'
        )}>
        {completed ? <CheckIcon size={12} color="#ffffff" /> : null}
      </Pressable>
      <View className="flex-1">
        <Text className={cn('text-base', completed && 'text-muted-foreground line-through')}>
          {task.title}
        </Text>
        {due ? <Text className="text-xs text-muted-foreground">{due}</Text> : null}
      </View>
    </View>
  );
}
