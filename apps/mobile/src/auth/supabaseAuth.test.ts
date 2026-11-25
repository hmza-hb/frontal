import { emailPasswordRequired } from "./authPort";
import { AuthClient, AuthUser, createSupabaseAuth } from "./supabaseAuth";

function clientWith(user: AuthUser | null, error: string | null = null): AuthClient {
  return {
    restoreUser: async () => user,
    signInWithPassword: async () => ({ user, error }),
    signInWithGoogle: async () => ({ user, error }),
  };
}

test("guest does not use the auth client", async () => {
  const auth = createSupabaseAuth(clientWith(null, "should not run"));
  const result = await auth.continueAsGuest();
  expect(result.ok).toBe(true);
  expect(await auth.restoreSession()).toEqual(result.ok ? result.session : null);
});

test("email and google use the client and clear guest", async () => {
  const user = { id: "1", email: "a@b.c" };
  const auth = createSupabaseAuth(clientWith(user, null));
  await auth.continueAsGuest();
  const email = await auth.signInWithEmail("a@b.c", "secret");
  expect(email.ok).toBe(true);
  const google = await auth.signInWithGoogle();
  expect(google.ok).toBe(true);
  expect(await auth.restoreSession()).toEqual(google.ok ? google.session : null);
});

test("restores a signed-in user from the client", async () => {
  const auth = createSupabaseAuth(clientWith({ id: "1", email: "a@b.c" }));
  expect(await auth.restoreSession()).toEqual({ kind: "signedIn", userId: "1", email: "a@b.c" });
});

test("rejects empty email credentials without calling through", async () => {
  const auth = createSupabaseAuth(clientWith({ id: "1", email: "a@b.c" }, null));
  expect(await auth.signInWithEmail("", "")).toEqual({ ok: false, message: emailPasswordRequired });
});
