import { QueryClient } from '@tanstack/react-query';

/**
 * The app-wide react-query client. Kept in its own module (rather than inline in
 * context/providers) so non-React code — notably auth sign-out — can clear the
 * cache directly (queryClient.clear()) without depending on the provider tree.
 * This prevents one account's cached mail/calendar from bleeding into the next
 * sign-in on a shared device.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
    },
  },
});
