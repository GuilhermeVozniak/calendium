import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import {
  isApiUnreachable,
  isDemoMode,
  MOCK_ACCOUNT_ID,
  mockAccounts,
  mockAiCompose,
  mockAiEditDraft,
  mockSendSuggestion,
  withMockFallback,
} from '@/lib/mock';
import { formatSendSuggestion, htmlToPlainText } from '@/lib/mail-extras';
import { useServerConfig } from '@/lib/server-config';
import { ApiRequestError, type AiEditAction } from '@calendium/shared';
import { useQuery, useMutation } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { SendIcon, SparklesIcon, Wand2Icon, XIcon } from 'lucide-react-native';
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
  const [aiEditLoading, setAiEditLoading] = React.useState(false);
  // Set once an AI edit action creates a backing draft server-side, so the
  // next edit (and the eventual Send) reuse it instead of creating another.
  const [draftId, setDraftId] = React.useState<string | null>(null);

  const accountsQuery = useQuery({
    queryKey: ['accounts'],
    queryFn: () =>
      withMockFallback(
        () => api.listAccounts(),
        () => mockAccounts
      ),
  });

  // Signature auto-append (M2.5): once the sending account resolves, append
  // its plain-text signature (htmlToPlainText strips the stored, possibly
  // rich, signatureHtml — mobile compose is plain-text only) exactly once per
  // compose session, never again on later body edits.
  const signatureAppendedRef = React.useRef(false);
  React.useEffect(() => {
    const account = accountsQuery.data?.[0];
    if (!account || signatureAppendedRef.current) return;
    signatureAppendedRef.current = true;
    const signature = htmlToPlainText(account.signatureHtml).trim();
    if (!signature) return;
    setBody((current) => (current ? `${current}\n\n${signature}` : signature));
  }, [accountsQuery.data]);

  // Smart Send (M2.5): once a recipient with a well-formed email address is
  // entered, look up their inferred best-open-time. Resolution-gated — the
  // line only renders once the request actually resolves, and a 404 (too
  // little open history) is a normal, silent "no suggestion" outcome, never
  // an error toast.
  const firstRecipientEmail = to.split(/[,;\s]+/).find((token) => /^\S+@\S+\.\S+$/.test(token));
  const suggestionQuery = useQuery({
    queryKey: ['send-suggestion', firstRecipientEmail],
    enabled: Boolean(firstRecipientEmail),
    retry: false,
    queryFn: () =>
      withMockFallback(
        () => api.getSendSuggestion(firstRecipientEmail!),
        () => mockSendSuggestion(firstRecipientEmail!)
      ),
  });

  const canSend = to.trim().length > 0 && (subject.trim().length > 0 || body.trim().length > 0);

  // Never send against a fabricated account on a real backend (it would
  // 404); the mock id is only valid in the offline demo.
  const resolveAccountId = (): string => {
    const accountId = accountsQuery.data?.[0]?.id ?? (isDemoMode() ? MOCK_ACCOUNT_ID : undefined);
    if (!accountId) {
      throw new ApiRequestError(
        400,
        'no_account',
        'Connect an email account in Settings before sending.'
      );
    }
    return accountId;
  };

  const recipientList = () =>
    to
      .split(/[,;\s]+/)
      .filter(Boolean)
      .map((email) => ({ name: null, email }));

  const bodyToHtml = () =>
    body
      .split('\n')
      .map((line) => `<p>${line}</p>`)
      .join('');

  const send = useMutation({
    mutationFn: async () => {
      const accountId = resolveAccountId();
      const input = {
        accountId,
        threadId: null,
        to: recipientList(),
        cc: [],
        bcc: [],
        subject,
        bodyHtml: bodyToHtml(),
        scheduledAt: null,
      };
      // Reuse the draft an AI edit already created server-side instead of
      // saving a duplicate.
      const draft = draftId ? await api.updateDraft(draftId, input) : await api.saveDraft(input);
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

  // Full DraftInput for the composer's current fields — used both to lazily
  // create the AI-backed draft and to sync it before a chained AI edit.
  const currentDraftInput = () => ({
    accountId: resolveAccountId(),
    threadId: null,
    to: recipientList(),
    cc: [],
    bcc: [],
    subject,
    bodyHtml: bodyToHtml(),
    scheduledAt: null,
  });

  // Runs one of the composer's AI edit actions against the draft body.
  // aiEditDraft always edits an existing draft server-side, so the first edit
  // lazily saves one (reused by later edits and by Send).
  const doAiEdit = async (action: AiEditAction, tone?: string) => {
    setAiEditLoading(true);
    try {
      let id = draftId;
      if (!id) {
        const draft = await api.saveDraft(currentDraftInput());
        id = draft.id;
        setDraftId(id);
      } else {
        // aiEditDraft reads the draft's STORED body server-side, not the local
        // `body` state. Without this, chaining two edits (e.g. Improve then
        // Shorten) would silently re-run the second action against the
        // pre-Improve text instead of the first edit's result.
        await api.updateDraft(id, currentDraftInput());
      }
      const res = await withMockFallback(
        () => api.aiEditDraft(action, id!, tone),
        () => mockAiEditDraft(action, body, tone)
      );
      setBody(res.text);
    } catch (error) {
      if (error instanceof ApiRequestError && error.status === 429) {
        Alert.alert(
          "Daily AI limit reached",
          "You've used today's AI budget — try again tomorrow."
        );
      } else {
        Alert.alert('AI edit failed', error instanceof Error ? error.message : 'Unknown error');
      }
    } finally {
      setAiEditLoading(false);
    }
  };

  const openToneSheet = () => {
    Alert.alert('Change tone', undefined, [
      { text: 'Friendly', onPress: () => doAiEdit('change_tone', 'friendly') },
      { text: 'Formal', onPress: () => doAiEdit('change_tone', 'formal') },
      { text: 'Direct', onPress: () => doAiEdit('change_tone', 'direct') },
      { text: 'Casual', onPress: () => doAiEdit('change_tone', 'casual') },
      { text: 'Cancel', style: 'cancel' },
    ]);
  };

  const openAiEditSheet = () => {
    Alert.alert('Edit with AI', undefined, [
      { text: 'Improve', onPress: () => doAiEdit('improve') },
      { text: 'Shorten', onPress: () => doAiEdit('shorten') },
      { text: 'Simplify', onPress: () => doAiEdit('simplify') },
      { text: 'Fix grammar', onPress: () => doAiEdit('fix_grammar') },
      { text: 'Change tone…', onPress: openToneSheet },
      { text: 'Cancel', style: 'cancel' },
    ]);
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
        {suggestionQuery.data && (
          <Text className="px-1 text-xs text-muted-foreground">
            {formatSendSuggestion(suggestionQuery.data)}
          </Text>
        )}
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
            <View className="flex-row gap-2">
              <Button
                variant="outline"
                size="sm"
                className="flex-row gap-2"
                onPress={aiAssist}
                disabled={aiLoading || aiEditLoading}>
                {aiLoading ? (
                  <ActivityIndicator size="small" />
                ) : (
                  <Icon as={SparklesIcon} className="size-4" />
                )}
                <Text>AI draft</Text>
              </Button>
              {body.trim().length > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-row gap-2"
                  onPress={openAiEditSheet}
                  disabled={aiLoading || aiEditLoading}>
                  {aiEditLoading ? (
                    <ActivityIndicator size="small" />
                  ) : (
                    <Icon as={Wand2Icon} className="size-4" />
                  )}
                  <Text>Edit with AI</Text>
                </Button>
              )}
            </View>
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
