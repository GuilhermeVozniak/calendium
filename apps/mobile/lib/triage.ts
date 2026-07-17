export interface SnoozePreset {
  label: string;
  until: Date;
}

/** Canonical snooze presets shared by inbox swipe/long-press and the thread reader. */
export function snoozePresets(now: Date = new Date()): SnoozePreset[] {
  const laterToday = new Date(now.getTime() + 3 * 60 * 60 * 1000);
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(8, 0, 0, 0);
  const nextWeek = new Date(now);
  nextWeek.setDate(nextWeek.getDate() + 7);
  nextWeek.setHours(8, 0, 0, 0);
  return [
    { label: 'Later today', until: laterToday },
    { label: 'Tomorrow 8 AM', until: tomorrow },
    { label: 'Next week', until: nextWeek },
  ];
}
