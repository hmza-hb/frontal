import { supabaseAuthConfigFromEnv } from "./supabaseAuthConfig";

test("reads url and anon key", () => {
  expect(
    supabaseAuthConfigFromEnv({
      EXPO_PUBLIC_SUPABASE_URL: "https://example.supabase.co",
      EXPO_PUBLIC_SUPABASE_ANON_KEY: "anon",
    }),
  ).toEqual({ url: "https://example.supabase.co", anonKey: "anon" });
});

test("returns null when either value is missing", () => {
  expect(supabaseAuthConfigFromEnv({})).toBeNull();
  expect(supabaseAuthConfigFromEnv({ EXPO_PUBLIC_SUPABASE_URL: "https://example.supabase.co" })).toBeNull();
  expect(supabaseAuthConfigFromEnv({ EXPO_PUBLIC_SUPABASE_ANON_KEY: "anon" })).toBeNull();
});
