import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import {
  isApiUnreachable,
  mockCreateClassifier,
  mockDeleteClassifier,
  mockListClassifiers,
  mockUpdateClassifier,
  withMockFallback,
} from '@/lib/mock';
import type { AiClassifier, ClassifierInput, InboxSplit } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { ChevronLeftIcon, PlusIcon, Trash2Icon } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, Pressable, ScrollView, Switch, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const SPLIT_OPTIONS: InboxSplit[] = [
  'important',
  'vip',
  'team',
  'calendar',
  'news',
  'social',
  'other',
];

const SPLIT_LABEL: Record<InboxSplit, string> = {
  important: 'Important',
  vip: 'VIP',
  team: 'Team',
  calendar: 'Calendar',
  news: 'News',
  social: 'Social',
  other: 'Other',
};

interface FormState {
  id: string | null; // null = creating a new classifier
  name: string;
  prompt: string;
  targetSplit: InboxSplit | undefined;
  labelName: string;
  enabled: boolean;
}

const EMPTY_FORM: FormState = {
  id: null,
  name: '',
  prompt: '',
  targetSplit: undefined,
  labelName: '',
  enabled: true,
};

/**
 * Task 17: full CRUD for AI classifiers, kept to one simple screen (list +
 * inline add/edit form) rather than a read-only link out to the web app.
 * Mirrors the shared ApiClient's `/v1/classifiers` routes 1:1.
 */
export default function ClassifiersScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const queryClient = useQueryClient();
  const [form, setForm] = React.useState<FormState | null>(null);

  const listQuery = useQuery({
    queryKey: ['classifiers'],
    queryFn: () =>
      withMockFallback(
        () => api.listClassifiers(),
        () => mockListClassifiers()
      ),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['classifiers'] });

  const toInput = (f: FormState): ClassifierInput => ({
    name: f.name.trim(),
    prompt: f.prompt.trim(),
    targetSplit: f.targetSplit,
    labelName: f.labelName.trim() || undefined,
    enabled: f.enabled,
  });

  const saveMutation = useMutation({
    mutationFn: async (f: FormState) => {
      const input = toInput(f);
      if (f.id) {
        return withMockFallback(
          () => api.updateClassifier(f.id!, input),
          () => mockUpdateClassifier(f.id!, input)
        );
      }
      return withMockFallback(
        () => api.createClassifier(input),
        () => mockCreateClassifier(input)
      );
    },
    onSuccess: () => {
      invalidate();
      setForm(null);
    },
    onError: (error) => {
      Alert.alert(
        'Could not save classifier',
        isApiUnreachable(error)
          ? 'Reach the Calendium API to manage classifiers.'
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      );
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) =>
      withMockFallback(
        () => api.deleteClassifier(id),
        () => mockDeleteClassifier(id)
      ),
    onSuccess: () => {
      invalidate();
      setForm(null);
    },
    onError: (error) => {
      Alert.alert('Could not delete classifier', error instanceof Error ? error.message : 'Unknown error');
    },
  });

  const toggleEnabled = (classifier: AiClassifier) => {
    saveMutation.mutate({
      id: classifier.id,
      name: classifier.name,
      prompt: classifier.prompt,
      targetSplit: classifier.targetSplit,
      labelName: classifier.labelName ?? '',
      enabled: !classifier.enabled,
    });
  };

  const confirmDelete = (classifier: AiClassifier) => {
    Alert.alert('Delete classifier?', `"${classifier.name}" will stop being applied to new mail.`, [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Delete',
        style: 'destructive',
        onPress: () => deleteMutation.mutate(classifier.id),
      },
    ]);
  };

  const pickTargetSplit = (current: FormState) => {
    Alert.alert('Route to split', undefined, [
      { text: 'None', onPress: () => setForm({ ...current, targetSplit: undefined }) },
      ...SPLIT_OPTIONS.map((split) => ({
        text: SPLIT_LABEL[split],
        onPress: () => setForm({ ...current, targetSplit: split }),
      })),
      { text: 'Cancel', style: 'cancel' as const },
    ]);
  };

  const classifiers = listQuery.data ?? [];
  const saving = saveMutation.isPending;

  return (
    <View className="flex-1 bg-background" style={{ paddingTop: insets.top }}>
      <View className="flex-row items-center gap-1 border-b border-border px-2 pb-2 pt-1">
        <Button
          size="icon"
          variant="ghost"
          className="rounded-full"
          onPress={() => (router.canGoBack() ? router.back() : router.replace('/(tabs)/settings'))}>
          <Icon as={ChevronLeftIcon} className="size-6" />
        </Button>
        <Text className="flex-1 font-semibold">AI classifiers</Text>
        <Button
          size="icon"
          variant="ghost"
          className="rounded-full"
          onPress={() => setForm(EMPTY_FORM)}
          testID="add-classifier">
          <Icon as={PlusIcon} className="size-5" />
        </Button>
      </View>

      <ScrollView contentContainerClassName="gap-4 p-4" style={{ paddingBottom: insets.bottom }}>
        <Text className="text-xs text-muted-foreground">
          Natural-language rules applied to incoming mail: when a message matches, it can be routed
          to a split and/or tagged with a label.
        </Text>

        {listQuery.isLoading ? (
          <View className="items-center p-6">
            <ActivityIndicator />
          </View>
        ) : listQuery.isError ? (
          <Text className="text-sm text-muted-foreground">Couldn't load classifiers.</Text>
        ) : classifiers.length === 0 && !form ? (
          <Text className="text-sm text-muted-foreground">
            No classifiers yet. Tap + to create one.
          </Text>
        ) : (
          <View className="overflow-hidden rounded-lg border border-border bg-card">
            {classifiers.map((classifier, i) => (
              <View
                key={classifier.id}
                className={i > 0 ? 'border-t border-border' : undefined}>
                <Pressable
                  className="gap-1 p-4 active:bg-accent"
                  onPress={() =>
                    setForm({
                      id: classifier.id,
                      name: classifier.name,
                      prompt: classifier.prompt,
                      targetSplit: classifier.targetSplit,
                      labelName: classifier.labelName ?? '',
                      enabled: classifier.enabled,
                    })
                  }>
                  <View className="flex-row items-center justify-between gap-2">
                    <Text className="flex-1 text-sm font-medium" numberOfLines={1}>
                      {classifier.name}
                    </Text>
                    <Switch value={classifier.enabled} onValueChange={() => toggleEnabled(classifier)} />
                  </View>
                  <Text className="text-xs text-muted-foreground" numberOfLines={2}>
                    {classifier.prompt}
                  </Text>
                  <View className="flex-row items-center gap-2 pt-1">
                    {classifier.targetSplit ? (
                      <View className="rounded-full bg-secondary px-2 py-0.5">
                        <Text className="text-xs text-secondary-foreground">
                          {SPLIT_LABEL[classifier.targetSplit]}
                        </Text>
                      </View>
                    ) : null}
                    {classifier.labelName ? (
                      <View className="rounded-full bg-secondary px-2 py-0.5">
                        <Text className="text-xs text-secondary-foreground">
                          {classifier.labelName}
                        </Text>
                      </View>
                    ) : null}
                    <View className="flex-1" />
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 rounded-full"
                      onPress={() => confirmDelete(classifier)}
                      testID={`delete-${classifier.id}`}>
                      <Icon as={Trash2Icon} className="size-4 text-destructive" />
                    </Button>
                  </View>
                </Pressable>
              </View>
            ))}
          </View>
        )}

        {form && (
          <View className="gap-3 rounded-lg border border-border bg-card p-4">
            <Text className="font-semibold">{form.id ? 'Edit classifier' : 'New classifier'}</Text>

            <View className="gap-1.5">
              <Text className="text-xs font-medium text-muted-foreground">Name</Text>
              <Input
                value={form.name}
                onChangeText={(name) => setForm({ ...form, name })}
                placeholder="e.g. Receipts & invoices"
              />
            </View>

            <View className="gap-1.5">
              <Text className="text-xs font-medium text-muted-foreground">Match when…</Text>
              <Input
                value={form.prompt}
                onChangeText={(prompt) => setForm({ ...form, prompt })}
                placeholder="Describe the mail this should match, in plain language"
                multiline
                className="h-auto py-2.5"
                style={{ textAlignVertical: 'top' }}
              />
            </View>

            <View className="gap-1.5">
              <Text className="text-xs font-medium text-muted-foreground">Route to split</Text>
              <Button variant="outline" size="sm" className="self-start" onPress={() => pickTargetSplit(form)}>
                <Text>{form.targetSplit ? SPLIT_LABEL[form.targetSplit] : 'None'}</Text>
              </Button>
            </View>

            <View className="gap-1.5">
              <Text className="text-xs font-medium text-muted-foreground">Apply label (optional)</Text>
              <Input
                value={form.labelName}
                onChangeText={(labelName) => setForm({ ...form, labelName })}
                placeholder="e.g. Receipts"
              />
            </View>

            <View className="flex-row items-center justify-between">
              <Text className="text-sm font-medium">Enabled</Text>
              <Switch
                value={form.enabled}
                onValueChange={(enabled) => setForm({ ...form, enabled })}
              />
            </View>

            <View className="flex-row gap-2 pt-2">
              <Button variant="outline" className="flex-1" onPress={() => setForm(null)} disabled={saving}>
                <Text>Cancel</Text>
              </Button>
              <Button
                className="flex-1 flex-row gap-2"
                onPress={() => saveMutation.mutate(form)}
                disabled={saving || form.name.trim().length === 0 || form.prompt.trim().length === 0}>
                {saving ? <ActivityIndicator size="small" /> : null}
                <Text>Save</Text>
              </Button>
            </View>
          </View>
        )}
      </ScrollView>
    </View>
  );
}
