import { cloudSignInNotConfigured, emailPasswordRequired } from "./authPort";
import { createMemoryAuth } from "./memoryAuth";

test("guest does not make a network call", async () => {
  const memory = createMemoryAuth({ cloudSignIn: true });
  const result = await memory.auth.continueAsGuest();
  expect(result.ok).toBe(true);
  if (result.ok) {
    expect(result.session.kind).toBe("guest");
  }
  expect(memory.networkCallCount()).toBe(0);
  expect(await memory.auth.restoreSession()).toEqual(result.ok ? result.session : null);
});

test("google and email sign-in restore a signed-in session", async () => {
  const memory = createMemoryAuth({ cloudSignIn: true });
  const google = await memory.auth.signInWithGoogle();
  expect(google.ok).toBe(true);
  const email = await memory.auth.signInWithEmail("a@b.c", "secret");
  expect(email.ok).toBe(true);
  if (email.ok) {
    expect(email.session.kind).toBe("signedIn");
    expect(email.session.email).toBe("a@b.c");
  }
  expect(memory.networkCallCount()).toBe(2);
});

test("cloud sign-in can be disabled", async () => {
  const memory = createMemoryAuth({ cloudSignIn: false });
  const google = await memory.auth.signInWithGoogle();
  const email = await memory.auth.signInWithEmail("a@b.c", "secret");
  expect(google).toEqual({ ok: false, message: cloudSignInNotConfigured });
  expect(email).toEqual({ ok: false, message: cloudSignInNotConfigured });
  expect(memory.networkCallCount()).toBe(0);
});

test("email requires both fields", async () => {
  const memory = createMemoryAuth({ cloudSignIn: true });
  const result = await memory.auth.signInWithEmail("", "");
  expect(result).toEqual({ ok: false, message: emailPasswordRequired });
  expect(memory.networkCallCount()).toBe(0);
});
