import { guestSession, sessionSkipsAuth, signedInSession } from "./session";

test("guest has no cloud identity", () => {
  const session = guestSession();
  expect(session.kind).toBe("guest");
  expect(session.userId).toBeNull();
  expect(session.email).toBeNull();
});

test("signed-in keeps user id and email", () => {
  const session = signedInSession("user-1", "a@b.c");
  expect(session.kind).toBe("signedIn");
  expect(session.userId).toBe("user-1");
  expect(session.email).toBe("a@b.c");
});

test("any session skips auth", () => {
  expect(sessionSkipsAuth(null)).toBe(false);
  expect(sessionSkipsAuth(guestSession())).toBe(true);
  expect(sessionSkipsAuth(signedInSession("user-1", "a@b.c"))).toBe(true);
});
