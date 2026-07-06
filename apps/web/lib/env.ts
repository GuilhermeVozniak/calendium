/**
 * Typed access to the NEXT_PUBLIC_* environment (see .env.example).
 * Getters keep the static `process.env.NEXT_PUBLIC_*` references Next.js needs
 * for build-time inlining while failing loudly at first use when unset.
 */

function required(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
  return value;
}

export const env = {
  get supabaseUrl(): string {
    return required('NEXT_PUBLIC_SUPABASE_URL', process.env.NEXT_PUBLIC_SUPABASE_URL);
  },
  get supabaseAnonKey(): string {
    return required('NEXT_PUBLIC_SUPABASE_ANON_KEY', process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY);
  },
  get apiUrl(): string {
    return required('NEXT_PUBLIC_API_URL', process.env.NEXT_PUBLIC_API_URL);
  },
};
