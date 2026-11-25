import { guestSession, signedInSession } from "../auth/session";
import { launchScreenAfterHold, splashHoldMs } from "./launchScreen";

test("stays on splash before the hold elapses", () => {
  expect(launchScreenAfterHold(0, splashHoldMs, null)).toBe("splash");
  expect(launchScreenAfterHold(splashHoldMs - 1, splashHoldMs, null)).toBe("splash");
});

test("stays on splash while restore is pending", () => {
  expect(launchScreenAfterHold(splashHoldMs, splashHoldMs, undefined)).toBe("splash");
});

test("hands off to auth when the hold elapses with no session", () => {
  expect(launchScreenAfterHold(splashHoldMs, splashHoldMs, null)).toBe("auth");
});

test("hands off to settings when a session is restored", () => {
  expect(launchScreenAfterHold(splashHoldMs, splashHoldMs, guestSession())).toBe("settings");
  expect(launchScreenAfterHold(splashHoldMs, splashHoldMs, signedInSession("1", "a@b.c"))).toBe("settings");
});
