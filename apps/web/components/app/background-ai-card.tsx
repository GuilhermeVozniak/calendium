'use client';

import type { UserSettings } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { Card, CardAction, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import { fetchSettings, updateSettingsApi } from '@/lib/scheduling-data';

/**
 * Settings → AI → "Background AI processing". Shares the ['scheduling-settings']
 * query with SchedulingSection: the switch is one field of the same document,
 * and PUT /v1/settings only writes it when the field is present.
 */
export function BackgroundAiCard() {
  const queryClient = useQueryClient();
  const settingsQuery = useQuery({ queryKey: ['scheduling-settings'], queryFn: fetchSettings });
  const toggle = useMutation({
    mutationFn: (aiBackground: boolean) => {
      // Read the freshest cached document (a Scheduling save may have
      // replaced it since this render) and flip only the switch.
      const current = queryClient.getQueryData<UserSettings>(['scheduling-settings']) ?? settingsQuery.data;
      return updateSettingsApi({ ...(current as UserSettings), aiBackground });
    },
    onSuccess: (s) => {
      queryClient.setQueryData<UserSettings>(['scheduling-settings'], s);
      toast.success(s.aiBackground ? 'Background AI processing is on' : 'Background AI processing is off');
    },
    onError: () => toast.error('Could not update background AI processing'),
  });
  const enabled = toggle.isPending ? toggle.variables : (settingsQuery.data?.aiBackground ?? true);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Background AI processing</CardTitle>
        <CardDescription>
          When new mail arrives, summarise it, draft a reply, suggest quick replies, run your classifiers, detect
          reminders and build your writing-style profile automatically. Turn this off and only the actions you trigger
          send a thread to the model.
        </CardDescription>
        <CardAction>
          <Switch
            aria-label="Background AI processing"
            checked={enabled}
            disabled={!settingsQuery.data || toggle.isPending}
            onCheckedChange={(checked) => toggle.mutate(checked)}
          />
        </CardAction>
      </CardHeader>
    </Card>
  );
}
