import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { isApiUnreachable, isDemoMode, MOCK_ACCOUNT_ID, mockAccounts, mockAiCompose, withMockFallback } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import { ApiRequestError } from '@calendium/shared';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { SendIcon, SparklesIcon, XIcon } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, KeyboardAvoidingView, Platform, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

export default function ComposeScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { config } = useServerConfig();
  const aiEnabled = config?.features?.ai ?? false;
  const [to, setTo] = React.useState('');
  const [subject, setSubject] = React.useState('');
  const [body, setBody] = React.useState('');
  const [aiLoading, setAiLoading] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ['accounts'],
    queryFn: () =>
      withMockFallback(
        () => api.listAccounts(),
        () => mockAccounts
      ),
  });

  const canSend = to.trim().length > 0 && (subject.trim().length > 0 || body.trim().length > 0);

  const send = useMutation({
    mutationFn: async () => {
      // Never send against a fabricated account on a real backend (it would
      // 404); the mock id is only valid in the offline demo.
      const accountId = accountsQuery.data?.[0]?.id ?? (isDemoMode() ? MOCK_ACCOUNT_ID : undefined);
      if (!accountId) {
        throw new ApiRequestError(
          400,
          'no_account',
          'Connect an email account in Settings before sending.'
        );
      }
      const recipients = to
        .split(/[,;\s]+/)
        .filter(Boolean)
        .map((email) => ({ name: null, email }));
      const draft = await api.saveDraft({
        accountId,
        to: recipients,
        subject,
        bodyHtml: body
          .split('\n')
          .map((line) => `<p>${line}</p>`)
          .join(''),
      });
      return api.sendDraft(draft.id);
    },
    onSuccess: () => router.back(),
    onError: (error) => {
      if (isApiUnreachable(error)) {
        // Nothing is queued yet, so tell the truth and keep the draft on-screen
        // (no router.back) so the user can tap Send again once back online.
        Alert.alert(
          "Couldn't reach the server",
          'Your draft is kept here — tap Send to try again once you are back online.'
        );
      } else {
        Alert.alert('Could not send', error instanceof Error ? error.message : 'Unknown error');
      }
    },
  });

  const aiAssist = async () => {
    const prompt = body.trim() || subject.trim() || 'Write a short, friendly email';
    setAiLoading(true);
    try {
      const res = await withMockFallback(
        () => api.aiCompose({ action: 'compose', prompt }),
        () => mockAiCompose({ action: 'compose', prompt })
      );
      setBody(res.text);
    } catch (error) {
      Alert.alert('AI assist failed', error instanceof Error ? error.message : 'Unknown error');
    } finally {
      setAiLoading(false);
    }
  };

  return (
    <KeyboardAvoidingView
      className="flex-1 bg-background"
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      style={{ paddingTop: Math.max(insets.top, 12) }}>
      {/* Header */}
      <View className="flex-row items-center justify-between px-3 pb-2">
        <Button size="icon" variant="ghost" className="rounded-full" onPress={() => router.back()}>
          <Icon as={XIcon} className="size-5" />
        </Button>
        <Text className="font-semibold">New message</Text>
        <Button
          size="sm"
          className="flex-row gap-1.5"
          onPress={() => send.mutate()}
          disabled={!canSend || send.isPending}>
          {send.isPending ? (
            <ActivityIndicator size="small" />
          ) : (
            <Icon as={SendIcon} className="size-4 text-primary-foreground" />
          )}
          <Text>Send</Text>
        </Button>
      </View>

      {/* Fields */}
      <View className="flex-1 gap-3 px-4" style={{ paddingBottom: Math.max(insets.bottom, 16) }}>
        <Input
          value={to}
          onChangeText={setTo}
          placeholder="To"
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="email-address"
        />
        <Input value={subject} onChangeText={setSubject} placeholder="Subject" />
        <Input
          value={body}
          onChangeText={setBody}
          placeholder="Write your message…"
          multiline
          className="h-auto flex-1 py-2.5"
          style={{ textAlignVertical: 'top' }}
        />
        <View className="flex-row items-center justify-between">
          {aiEnabled ? (
            <Button
              variant="outline"
              size="sm"
              className="flex-row gap-2"
              onPress={aiAssist}
              disabled={aiLoading}>
              {aiLoading ? (
                <ActivityIndicator size="small" />
              ) : (
                <Icon as={SparklesIcon} className="size-4" />
              )}
              <Text>AI draft</Text>
            </Button>
          ) : (
            <View />
          )}
          <Text className="text-xs text-muted-foreground" numberOfLines={1}>
            {accountsQuery.data?.[0]?.email ?? ''}
          </Text>
        </View>
      </View>
    </KeyboardAvoidingView>
  );
}
