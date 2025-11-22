// import 'react-native-url-polyfill/auto';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { createClient } from '@supabase/supabase-js';

export const envErrorMessage = 'Missing environment variable';
if (!process.env.EXPO_PUBLIC_SUPABASE_URL) {
  throw new Error(`${envErrorMessage}: EXPO_PUBLIC_SUPABASE_URL`);
}

if (!process.env.EXPO_PUBLIC_SUPABASE_ANON_KEY) {
  throw new Error(`${envErrorMessage}: EXPO_PUBLIC_SUPABASE_ANON_KEY`);
}

export const supabase = createClient(
  process.env.EXPO_PUBLIC_SUPABASE_URL,
  process.env.EXPO_PUBLIC_SUPABASE_ANON_KEY,
  {
    auth: {
      storage: AsyncStorage,
      autoRefreshToken: true,
      persistSession: true,
      detectSessionInUrl: false,
    },
  }
);
