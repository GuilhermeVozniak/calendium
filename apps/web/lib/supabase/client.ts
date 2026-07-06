import { createBrowserClient } from '@supabase/ssr';

import { env } from '@/lib/env';

/**
 * Browser Supabase client. `createBrowserClient` returns a per-page singleton,
 * so this is safe to call from any component or hook.
 */
export function createClient() {
  return createBrowserClient(env.supabaseUrl, env.supabaseAnonKey);
}
