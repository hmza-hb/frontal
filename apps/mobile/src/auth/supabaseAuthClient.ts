import { createClient } from "@supabase/supabase-js";
import { AuthClient, AuthUser } from "./supabaseAuth";
import { SupabaseAuthConfig } from "./supabaseAuthConfig";

function authUser(id: string, email: string | undefined): AuthUser {
  return { id, email };
}

export function createSupabaseAuthClient(config: SupabaseAuthConfig): AuthClient {
  const client = createClient(config.url, config.anonKey);
  return {
    restoreUser: async () => {
      const { data } = await client.auth.getSession();
      const user = data.session?.user;
      if (user === undefined) {
        return null;
      }
      return authUser(user.id, user.email);
    },
    signInWithPassword: async (email: string, password: string) => {
      const { data, error } = await client.auth.signInWithPassword({ email, password });
      if (error !== null) {
        return { user: null, error: error.message };
      }
      if (data.user === null) {
        return { user: null, error: null };
      }
      return { user: authUser(data.user.id, data.user.email), error: null };
    },
    signInWithGoogle: async () => {
      const { error } = await client.auth.signInWithOAuth({ provider: "google" });
      if (error !== null) {
        return { user: null, error: error.message };
      }
      return { user: null, error: "Google sign-in needs a configured provider" };
    },
  };
}
