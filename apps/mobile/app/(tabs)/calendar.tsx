import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import { api } from '@/lib/api';
import { getJoinInfo } from '@/lib/calendar-join';
import { monthGridDays } from '@/lib/calendar-month-grid';
import { tickerDays } from '@/lib/calendar-ticker';
import { applyTemplate } from '@/lib/calendar-templates';
import {
  addDays,
  dayKey,
  endOfDay,
  formatDayTitle,
  formatTime,
  formatTimeRange,
  isSameDay,
  startOfDay,
  startOfWeek,
} from '@/lib/format';
import { isDemoMode, mockCalendars, mockEventTemplates, mockEvents, withMockFallback } from '@/lib/mock';
import { cn } from '@/lib/utils';
import type { Event, EventInput, EventTemplate } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  CalendarDaysIcon,
  CalendarIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ClockIcon,
  LayoutTemplateIcon,
  MapPinIcon,
  PlusIcon,
  UsersIcon,
  VideoIcon,
  XIcon,
} from 'lucide-react-native';
import * as React from 'react';
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  RefreshControl,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const WEEKDAY_LETTERS = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];

export default function CalendarScreen() {
  const insets = useSafeAreaInsets();
  const queryClient = useQueryClient();
  const [selectedDay, setSelectedDay] = React.useState(() => startOfDay(new Date()));
  const [weekStart, setWeekStart] = React.useState(() => startOfWeek(new Date()));
  const [selectedEvent, setSelectedEvent] = React.useState<Event | null>(null);
  const [quickAdd, setQuickAdd] = React.useState('');
  const [templateSheetOpen, setTemplateSheetOpen] = React.useState(false);
  const [draft, setDraft] = React.useState<Partial<EventInput> | null>(null);
  const [monthPickerOpen, setMonthPickerOpen] = React.useState(false);
  const [monthGridAnchor, setMonthGridAnchor] = React.useState(() => startOfDay(new Date()));

  const calendarsQuery = useQuery({
    queryKey: ['calendars'],
    queryFn: () =>
      withMockFallback(
        () => api.listCalendars(),
        () => mockCalendars
      ),
  });

  const templatesQuery = useQuery({
    queryKey: ['event-templates'],
    queryFn: () =>
      withMockFallback(
        () => api.listEventTemplates(),
        () => mockEventTemplates
      ),
  });

  const eventsQuery = useQuery({
    queryKey: ['events', dayKey(selectedDay)],
    queryFn: () => {
      const from = startOfDay(selectedDay).toISOString();
      const to = endOfDay(selectedDay).toISOString();
      return withMockFallback(
        () => api.listEvents(from, to),
        () => mockEvents(from, to)
      );
    },
  });

  const quickAddMutation = useMutation({
    mutationFn: (input: EventInput) => api.createEvent(input),
    onMutate: (input) => {
      const optimistic: Event = {
        id: `local_${Date.now()}`,
        calendarId: input.calendarId,
        title: input.title,
        description: input.description ?? null,
        location: input.location ?? null,
        start: input.start,
        end: input.end,
        allDay: input.allDay ?? false,
        recurrenceRule: null,
        attendees: [],
        conferencing: null,
        status: 'confirmed',
        visibility: 'default',
        reminderMinutes: input.reminderMinutes ?? [],
      };
      queryClient.setQueryData<Event[]>(['events', dayKey(new Date(input.start))], (events) =>
        [...(events ?? []), optimistic].sort((a, b) => a.start.localeCompare(b.start))
      );
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['events'] }),
    onError: (error, input) => {
      if (isDemoMode()) {
        // Demo mode: keep the optimistic event (there is no backend).
        return;
      }
      // A real failure must surface and roll back — never a silent fake add.
      Alert.alert('Could not add event', error instanceof Error ? error.message : 'Unknown error');
      queryClient.invalidateQueries({ queryKey: ['events', dayKey(new Date(input.start))] });
    },
  });

  const weekDays = React.useMemo(() => tickerDays(weekStart), [weekStart]);
  const monthGrid = React.useMemo(() => monthGridDays(monthGridAnchor), [monthGridAnchor]);
  const selectedJoinInfo = selectedEvent ? getJoinInfo(selectedEvent) : null;

  const monthTitle = selectedDay.toLocaleDateString(undefined, {
    month: 'long',
    year: 'numeric',
  });

  const calendarFor = (calendarId: string) =>
    calendarsQuery.data?.find((c) => c.id === calendarId);

  const goToToday = () => {
    const today = startOfDay(new Date());
    setSelectedDay(today);
    setWeekStart(startOfWeek(today));
  };

  const selectDay = (day: Date) => {
    setSelectedDay(day);
    setSelectedEvent(null);
  };

  const jumpToDay = (day: Date) => {
    selectDay(day);
    setWeekStart(startOfWeek(day));
    setMonthPickerOpen(false);
  };

  const openMonthPicker = () => {
    setMonthGridAnchor(selectedDay);
    setMonthPickerOpen(true);
  };

  const submitQuickAdd = () => {
    const parsed = parseQuickAdd(quickAdd, selectedDay);
    if (!parsed) return;
    const calendarId =
      calendarsQuery.data?.find((c) => c.canWrite)?.id ?? calendarsQuery.data?.[0]?.id ?? 'cal_work';
    quickAddMutation.mutate({
      calendarId,
      title: parsed.title,
      start: parsed.start.toISOString(),
      end: parsed.end.toISOString(),
    });
    setQuickAdd('');
  };

  const openTemplateSheet = () => {
    setDraft(null);
    setTemplateSheetOpen(true);
  };

  const closeTemplateSheet = () => {
    setTemplateSheetOpen(false);
    setDraft(null);
  };

  const pickTemplate = (template: EventTemplate) => {
    setDraft(applyTemplate(template, defaultEventTime(selectedDay)));
  };

  const submitDraft = () => {
    if (!draft?.title?.trim() || !draft.start || !draft.end) return;
    const calendarId =
      draft.calendarId ??
      calendarsQuery.data?.find((c) => c.canWrite)?.id ??
      calendarsQuery.data?.[0]?.id ??
      'cal_work';
    quickAddMutation.mutate({
      ...draft,
      calendarId,
      title: draft.title,
      start: draft.start,
      end: draft.end,
    } as EventInput);
    closeTemplateSheet();
  };

  return (
    <KeyboardAvoidingView
      className="flex-1 bg-background"
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      style={{ paddingTop: insets.top }}>
      {/* Header */}
      <View className="flex-row items-center justify-between px-4 pb-1 pt-1">
        <Text variant="h3">Calendar</Text>
        <Button size="sm" variant="ghost" onPress={goToToday}>
          <Text>Today</Text>
        </Button>
      </View>

      {/* Week navigation */}
      <View className="flex-row items-center justify-between px-4 pb-1">
        <Pressable
          onPress={openMonthPicker}
          testID="open-month-picker"
          className="flex-row items-center gap-1.5">
          <Text className="text-sm font-medium text-muted-foreground">{monthTitle}</Text>
          <Icon as={CalendarDaysIcon} className="size-3.5 text-muted-foreground" />
        </Pressable>
        <View className="flex-row">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 rounded-full"
            onPress={() => setWeekStart((d) => addDays(d, -7))}>
            <Icon as={ChevronLeftIcon} className="size-5" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="size-8 rounded-full"
            onPress={() => setWeekStart((d) => addDays(d, 7))}>
            <Icon as={ChevronRightIcon} className="size-5" />
          </Button>
        </View>
      </View>

      {/* Horizontal week day-picker (DayTicker strip) */}
      <View className="flex-row border-b border-border px-2 pb-2">
        {weekDays.map((day, i) => {
          const selected = isSameDay(day, selectedDay);
          const today = isSameDay(day, new Date());
          return (
            <Pressable
              key={dayKey(day)}
              testID={`ticker-day-${dayKey(day)}`}
              accessibilityState={{ selected }}
              onPress={() => selectDay(day)}
              className="flex-1 items-center gap-1 py-1">
              <Text className="text-[11px] font-medium text-muted-foreground">
                {WEEKDAY_LETTERS[i]}
              </Text>
              <View
                className={cn(
                  'size-8 items-center justify-center rounded-full',
                  selected && 'bg-primary'
                )}>
                <Text
                  className={cn(
                    'text-sm',
                    selected
                      ? 'font-semibold text-primary-foreground'
                      : today
                        ? 'font-semibold text-foreground'
                        : 'text-muted-foreground'
                  )}>
                  {day.getDate()}
                </Text>
              </View>
            </Pressable>
          );
        })}
      </View>

      {/* Day agenda */}
      {eventsQuery.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator size="large" />
        </View>
      ) : (
        <FlatList
          data={eventsQuery.data ?? []}
          keyExtractor={(e) => e.id}
          contentContainerClassName="pb-4"
          ItemSeparatorComponent={() => <View className="ml-4 h-px bg-border" />}
          refreshControl={
            <RefreshControl
              refreshing={eventsQuery.isRefetching}
              onRefresh={() => eventsQuery.refetch()}
            />
          }
          ListHeaderComponent={
            <Text className="px-4 pb-1 pt-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {formatDayTitle(selectedDay)}
            </Text>
          }
          ListEmptyComponent={
            <View className="items-center gap-2 px-8 pt-20">
              <Icon as={CalendarIcon} className="size-8 text-muted-foreground" />
              <Text className="text-center text-sm text-muted-foreground">
                {eventsQuery.isError
                  ? "Couldn't load this day."
                  : 'No events — enjoy the free time.'}
              </Text>
            </View>
          }
          renderItem={({ item }) => (
            <EventRow
              event={item}
              color={calendarFor(item.calendarId)?.color ?? '#6b7280'}
              onPress={() => setSelectedEvent(item)}
            />
          )}
        />
      )}

      {/* Quick add */}
      <View
        className="flex-row items-center gap-2 border-t border-border px-4 pt-2"
        style={{ paddingBottom: Math.max(insets.bottom / 2, 8) }}>
        <Button
          size="icon"
          variant="outline"
          onPress={openTemplateSheet}
          accessibilityLabel="New event from template">
          <Icon as={LayoutTemplateIcon} className="size-5" />
        </Button>
        <Input
          className="flex-1"
          value={quickAdd}
          onChangeText={setQuickAdd}
          placeholder='Quick add — e.g. "Coffee with Ana 3pm"'
          returnKeyType="done"
          onSubmitEditing={submitQuickAdd}
        />
        <Button size="icon" onPress={submitQuickAdd} disabled={!quickAdd.trim()}>
          <Icon as={PlusIcon} className="size-5 text-primary-foreground" />
        </Button>
      </View>

      {/* Event detail sheet */}
      {selectedEvent && (
        <View className="absolute inset-0">
          <Pressable className="flex-1 bg-black/40" onPress={() => setSelectedEvent(null)} />
          <View
            className="gap-3 rounded-t-2xl border-t border-border bg-card px-5 pt-5"
            style={{ paddingBottom: Math.max(insets.bottom, 20) }}>
            <View className="flex-row items-center justify-between">
              <View className="flex-row items-center gap-2">
                <View
                  className="size-2.5 rounded-full"
                  style={{
                    backgroundColor: calendarFor(selectedEvent.calendarId)?.color ?? '#6b7280',
                  }}
                />
                <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  {calendarFor(selectedEvent.calendarId)?.name ?? 'Calendar'}
                </Text>
              </View>
              <Button
                size="icon"
                variant="ghost"
                className="size-8 rounded-full"
                onPress={() => setSelectedEvent(null)}>
                <Icon as={XIcon} className="size-4 text-muted-foreground" />
              </Button>
            </View>

            <Text variant="h4">{selectedEvent.title}</Text>

            <View className="flex-row items-center gap-2">
              <Icon as={ClockIcon} className="size-4 text-muted-foreground" />
              <Text className="text-sm text-muted-foreground">
                {selectedEvent.allDay
                  ? `${formatDayTitle(new Date(selectedEvent.start))} · All day`
                  : `${formatDayTitle(new Date(selectedEvent.start))} · ${formatTimeRange(selectedEvent.start, selectedEvent.end)}`}
              </Text>
            </View>

            {selectedEvent.location && (
              <View className="flex-row items-center gap-2">
                <Icon as={MapPinIcon} className="size-4 text-muted-foreground" />
                <Text className="text-sm text-muted-foreground">{selectedEvent.location}</Text>
              </View>
            )}

            {selectedEvent.attendees.length > 0 && (
              <View className="flex-row items-center gap-2">
                <Icon as={UsersIcon} className="size-4 text-muted-foreground" />
                <Text className="text-sm text-muted-foreground">
                  {selectedEvent.attendees.length} attendee
                  {selectedEvent.attendees.length === 1 ? '' : 's'}
                </Text>
              </View>
            )}

            {selectedJoinInfo && (
              <Button
                className="mt-1 flex-row gap-2"
                onPress={() =>
                  Linking.openURL(selectedJoinInfo.url).catch(() =>
                    Alert.alert('Could not open the meeting link')
                  )
                }>
                <Icon as={VideoIcon} className="size-4 text-primary-foreground" />
                <Text>{selectedJoinInfo.label}</Text>
              </Button>
            )}
          </View>
        </View>
      )}

      {/* Template picker sheet (new event from a saved template) */}
      {templateSheetOpen && (
        <View className="absolute inset-0">
          <Pressable className="flex-1 bg-black/40" onPress={closeTemplateSheet} />
          <View
            className="gap-3 rounded-t-2xl border-t border-border bg-card px-5 pt-5"
            style={{ paddingBottom: Math.max(insets.bottom, 20) }}>
            <View className="flex-row items-center justify-between">
              <Text variant="h4">New event</Text>
              <Button
                size="icon"
                variant="ghost"
                className="size-8 rounded-full"
                onPress={closeTemplateSheet}>
                <Icon as={XIcon} className="size-4 text-muted-foreground" />
              </Button>
            </View>

            <View className="flex-row flex-wrap gap-2">
              {(templatesQuery.data ?? []).map((t) => (
                <Pressable
                  key={t.id}
                  testID={`template-chip-${t.id}`}
                  onPress={() => pickTemplate(t)}
                  className={cn(
                    'rounded-full border border-border px-3 py-1.5',
                    draft?.title === t.title && 'border-primary bg-primary/10'
                  )}>
                  <Text className="text-sm">{t.name}</Text>
                </Pressable>
              ))}
              {(templatesQuery.data?.length ?? 0) === 0 && (
                <Text className="text-sm text-muted-foreground">
                  {templatesQuery.isError ? "Couldn't load templates." : 'No saved templates yet.'}
                </Text>
              )}
            </View>

            {draft && (
              <>
                <Input
                  testID="draft-title-input"
                  value={draft.title ?? ''}
                  onChangeText={(v) => setDraft((d) => (d ? { ...d, title: v } : d))}
                  placeholder="Title"
                />
                {draft.start && draft.end && (
                  <View className="flex-row items-center gap-2">
                    <Icon as={ClockIcon} className="size-4 text-muted-foreground" />
                    <Text className="text-sm text-muted-foreground">
                      {draft.allDay
                        ? `${formatDayTitle(new Date(draft.start))} · All day`
                        : `${formatDayTitle(new Date(draft.start))} · ${formatTimeRange(draft.start, draft.end)}`}
                    </Text>
                  </View>
                )}
                <Button onPress={submitDraft} disabled={!draft.title?.trim()}>
                  <Text>Add event</Text>
                </Button>
              </>
            )}
          </View>
        </View>
      )}

      {/* Month mini-grid for date jumping */}
      {monthPickerOpen && (
        <View className="absolute inset-0">
          <Pressable className="flex-1 bg-black/40" onPress={() => setMonthPickerOpen(false)} />
          <View
            className="gap-3 rounded-t-2xl border-t border-border bg-card px-5 pt-5"
            style={{ paddingBottom: Math.max(insets.bottom, 20) }}>
            <View className="flex-row items-center justify-between">
              <Text variant="h4">
                {monthGridAnchor.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}
              </Text>
              <View className="flex-row">
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 rounded-full"
                  onPress={() =>
                    setMonthGridAnchor((m) => new Date(m.getFullYear(), m.getMonth() - 1, 1))
                  }>
                  <Icon as={ChevronLeftIcon} className="size-5" />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 rounded-full"
                  onPress={() =>
                    setMonthGridAnchor((m) => new Date(m.getFullYear(), m.getMonth() + 1, 1))
                  }>
                  <Icon as={ChevronRightIcon} className="size-5" />
                </Button>
              </View>
            </View>
            <View className="flex-row flex-wrap">
              {monthGrid.map((day) => {
                const inMonth = day.getMonth() === monthGridAnchor.getMonth();
                const selected = isSameDay(day, selectedDay);
                return (
                  <Pressable
                    key={dayKey(day)}
                    testID={`month-grid-day-${dayKey(day)}`}
                    onPress={() => jumpToDay(day)}
                    style={{ width: '14.2857%' }}
                    className="items-center py-1.5">
                    <View
                      className={cn(
                        'size-7 items-center justify-center rounded-full',
                        selected && 'bg-primary'
                      )}>
                      <Text
                        className={cn(
                          'text-xs',
                          !inMonth && 'text-muted-foreground/50',
                          selected && 'font-semibold text-primary-foreground'
                        )}>
                        {day.getDate()}
                      </Text>
                    </View>
                  </Pressable>
                );
              })}
            </View>
          </View>
        </View>
      )}
    </KeyboardAvoidingView>
  );
}

function EventRow({
  event,
  color,
  onPress,
}: {
  event: Event;
  color: string;
  onPress: () => void;
}) {
  const joinInfo = getJoinInfo(event);
  return (
    <Pressable onPress={onPress} className="flex-row gap-3 px-4 py-2.5 active:bg-accent">
      <View className="w-1 rounded-full" style={{ backgroundColor: color }} />
      <View className="w-20">
        {event.allDay ? (
          <Text className="text-xs text-muted-foreground">All day</Text>
        ) : (
          <>
            <Text className="text-sm font-medium">{formatTime(event.start)}</Text>
            <Text className="text-xs text-muted-foreground">{formatTime(event.end)}</Text>
          </>
        )}
      </View>
      <View className="flex-1 gap-0.5">
        <Text numberOfLines={1} className="text-sm font-medium">
          {event.title}
        </Text>
        {(event.location || event.conferencing) && (
          <View className="flex-row items-center gap-1">
            <Icon
              as={event.conferencing ? VideoIcon : MapPinIcon}
              size={12}
              className="text-muted-foreground"
            />
            <Text numberOfLines={1} className="text-xs text-muted-foreground">
              {event.location ?? 'Video call'}
            </Text>
          </View>
        )}
        {joinInfo && (
          <Pressable
            testID={`join-button-${event.id}`}
            onPress={() =>
              Linking.openURL(joinInfo.url).catch(() =>
                Alert.alert('Could not open the meeting link')
              )
            }
            className="mt-1 flex-row items-center gap-1 self-start rounded-full bg-primary/10 px-2 py-0.5">
            <Icon as={VideoIcon} size={11} className="text-primary" />
            <Text className="text-[11px] font-medium text-primary">{joinInfo.label}</Text>
          </Pressable>
        )}
      </View>
    </Pressable>
  );
}

/**
 * Default start time when quick-add text or a template doesn't pin one: the
 * next full hour today, or 9 AM on other days.
 */
function defaultEventTime(day: Date): Date {
  const result = new Date(day);
  const now = new Date();
  if (isSameDay(day, now)) {
    result.setHours(now.getHours() + 1, 0, 0, 0);
  } else {
    result.setHours(9, 0, 0, 0);
  }
  return result;
}

/**
 * Parses quick-add text like "Coffee with Ana 3pm" or "Standup 9:30" into a
 * title + start/end on the given day. Without a time: next full hour today,
 * 9 AM on other days. Default duration 30 minutes.
 */
function parseQuickAdd(
  text: string,
  day: Date
): { title: string; start: Date; end: Date } | null {
  const trimmed = text.trim();
  if (!trimmed) return null;

  let title = trimmed;
  const start = new Date(day);
  let hasTime = false;

  const match = trimmed.match(/(?:\bat\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*$/i);
  // Require ":mm" or am/pm so a bare trailing number ("Review PR 42") stays in the title.
  if (match && match.index !== undefined && (match[2] || match[3])) {
    let hour = parseInt(match[1], 10);
    const minute = match[2] ? parseInt(match[2], 10) : 0;
    const meridiem = match[3]?.toLowerCase();
    if (meridiem === 'pm' && hour < 12) hour += 12;
    if (meridiem === 'am' && hour === 12) hour = 0;
    if (hour <= 23 && minute <= 59) {
      start.setHours(hour, minute, 0, 0);
      title = trimmed.slice(0, match.index).replace(/\s*\bat\s*$/i, '').trim() || trimmed;
      hasTime = true;
    }
  }

  if (!hasTime) {
    const defaultTime = defaultEventTime(day);
    start.setHours(defaultTime.getHours(), defaultTime.getMinutes(), 0, 0);
  }

  return { title, start, end: new Date(start.getTime() + 30 * 60_000) };
}
