// import 'react-native-url-polyfill/auto';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { createClient, type SupabaseClient } from '@supabase/supabase-js';

/**
 * Supabase is the identity provider on every platform (docs/architecture.md).
 * Its project URL + anon key now come from runtime server discovery
 * (lib/server-config): the client points at whichever Calendium server the
 * user connected to. `EXPO_PUBLIC_SUPABASE_*` still seeds a default so the
 * client exists before discovery finishes; it is no longer required.
 */

const authOptions = {
  storage: AsyncStorage,
  autoRefreshToken: true,
  persistSession: true,
  detectSessionInUrl: false,
} as const;

function build(url: string, anonKey: string): SupabaseClient | null {
  if (!url || !anonKey) return null;
  return createClient(url, anonKey, { auth: authOptions });
}

let currentUrl = process.env.EXPO_PUBLIC_SUPABASE_URL ?? '';
let currentAnonKey = process.env.EXPO_PUBLIC_SUPABASE_ANON_KEY ?? '';
let client: SupabaseClient | null = build(currentUrl, currentAnonKey);

/** The current Supabase client, or null until a server has been configured. */
export function getSupabase(): SupabaseClient | null {
  return client;
}

/**
 * (Re)build the Supabase client from runtime server config. Returns the new
 * client (or null when creds are absent). No-op when the creds are unchanged,
 * so we keep a single client instance and its AsyncStorage-backed session.
 */
export function configureSupabase(url: string, anonKey: string): SupabaseClient | null {
  if (url === currentUrl && anonKey === currentAnonKey) return client;
  currentUrl = url;
  currentAnonKey = anonKey;
  client = build(url, anonKey);
  return client;
}
