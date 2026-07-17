import type { Event, Thread } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { calendarMock } from '@/lib/calendar-mock';
import { DEMO_MODE } from '@/lib/demo';
import { getMockThreads } from '@/lib/mail-mock';

export interface SearchData {
  threads: Thread[];
  events: Event[];
}

/**
 * Unified search (GET /v1/search) over mail threads and calendar events.
 * Demo mode filters the local mocks instead (honesty policy: mock data is
 * served ONLY behind the explicit demo flag; real errors propagate).
 */
export async function fetchSearch(q: string): Promise<SearchData> {
  try {
    return await getApiClient().search(q);
  } catch (err) {
    if (!DEMO_MODE) throw err;
    const needle = q.toLowerCase();
    const from = new Date();
    from.setDate(from.getDate() - 90);
    const to = new Date();
    to.setDate(to.getDate() + 90);
    return {
      threads: getMockThreads({ q }).items.slice(0, 6),
      events: calendarMock
        .listEvents(from.toISOString(), to.toISOString())
        .filter((e) => e.title.toLowerCase().includes(needle))
        .slice(0, 6),
    };
  }
}
