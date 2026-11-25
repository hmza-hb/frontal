import { AuthPort } from "./authPort";
import { createMemoryAuth } from "./memoryAuth";
import { SupabaseAuthConfig, supabaseAuthConfigFromEnv } from "./supabaseAuthConfig";

export function createAuthPort(
  env: Record<string, string | undefined>,
  supabaseAuth: (config: SupabaseAuthConfig) => AuthPort,
): AuthPort {
  const config = supabaseAuthConfigFromEnv(env);
  if (config === null) {
    return createMemoryAuth({ cloudSignIn: false }).auth;
  }
  return supabaseAuth(config);
}
