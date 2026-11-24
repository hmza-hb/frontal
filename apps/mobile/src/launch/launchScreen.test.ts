import { launchScreenAfterHold, splashHoldMs } from "./launchScreen";

test("stays on splash before the hold elapses", () => {
  expect(launchScreenAfterHold(0, splashHoldMs)).toBe("splash");
  expect(launchScreenAfterHold(splashHoldMs - 1, splashHoldMs)).toBe("splash");
});

test("hands off to auth when the hold elapses", () => {
  expect(launchScreenAfterHold(splashHoldMs, splashHoldMs)).toBe("auth");
  expect(launchScreenAfterHold(splashHoldMs + 1, splashHoldMs)).toBe("auth");
});
