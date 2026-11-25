export type SupabaseAuthConfig = {
  url: string;
  anonKey: string;
};

export function supabaseAuthConfigFromEnv(env: Record<string, string | undefined>): SupabaseAuthConfig | null {
  const url = env.EXPO_PUBLIC_SUPABASE_URL;
  const anonKey = env.EXPO_PUBLIC_SUPABASE_ANON_KEY;
  if (url === undefined || anonKey === undefined || url.length === 0 || anonKey.length === 0) {
    return null;
  }
  return { url, anonKey };
}
