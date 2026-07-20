import type { Place } from '@calendium/shared';

import { getApiClient } from '@/lib/api';

/**
 * Place suggestions for the event-location autocomplete (M2.8 Task 11).
 * The server answers 501 (ApiRequestError with code 'not_implemented') when
 * no maps provider is configured — LocationField catches that and degrades
 * to a plain text input. No demo fallback: without a backend the affordance
 * simply stays hidden (features.maps is false).
 */
export async function autocompletePlacesApi(q: string): Promise<Place[]> {
  return getApiClient().autocompletePlaces(q);
}
