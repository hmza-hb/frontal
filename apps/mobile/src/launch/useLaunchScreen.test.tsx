import { act, renderHook } from "@testing-library/react-native";
import { guestSession } from "../auth/session";
import { splashHoldMs } from "./launchScreen";
import { useLaunchScreen } from "./useLaunchScreen";

jest.mock("./nativeSplash", () => ({
  hideNativeSplash: jest.fn(() => Promise.resolve()),
}));

test("returns splash until the hold elapses then auth", async () => {
  jest.useFakeTimers();
  const { result } = renderHook(() => useLaunchScreen());
  expect(result.current).toBe("splash");
  await act(async () => {
    jest.advanceTimersByTime(splashHoldMs);
  });
  expect(result.current).toBe("auth");
  jest.useRealTimers();
});

test("restores a guest onto settings", async () => {
  jest.useFakeTimers();
  const restoreSession = jest.fn(async () => guestSession());
  const { result } = renderHook(() => useLaunchScreen(restoreSession));
  await act(async () => {
    jest.advanceTimersByTime(splashHoldMs);
  });
  expect(result.current).toBe("settings");
  jest.useRealTimers();
});
