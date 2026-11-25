import { cloudSignInNotConfigured } from "./authPort";
import { createAuthPort } from "./createAuthPort";
import { createMemoryAuth } from "./memoryAuth";

test("guest works without supabase env", async () => {
  const auth = createAuthPort({}, () => {
    throw new Error("supabase should not be created");
  });
  const result = await auth.continueAsGuest();
  expect(result.ok).toBe(true);
  const google = await auth.signInWithGoogle();
  expect(google).toEqual({ ok: false, message: cloudSignInNotConfigured });
});

test("uses supabase auth when env is present", async () => {
  const memory = createMemoryAuth({ cloudSignIn: true });
  const auth = createAuthPort(
    {
      EXPO_PUBLIC_SUPABASE_URL: "https://example.supabase.co",
      EXPO_PUBLIC_SUPABASE_ANON_KEY: "anon",
    },
    () => memory.auth,
  );
  const google = await auth.signInWithGoogle();
  expect(google.ok).toBe(true);
});
