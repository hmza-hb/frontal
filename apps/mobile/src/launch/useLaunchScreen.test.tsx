import { act, renderHook } from "@testing-library/react-native";
import { splashHoldMs } from "./launchScreen";
import { useLaunchScreen } from "./useLaunchScreen";

jest.mock("./nativeSplash", () => ({
  hideNativeSplash: jest.fn(() => Promise.resolve()),
}));

test("returns splash until the hold elapses", () => {
  jest.useFakeTimers();
  const { result } = renderHook(() => useLaunchScreen());
  expect(result.current).toBe("splash");
  act(() => {
    jest.advanceTimersByTime(splashHoldMs - 1);
  });
  expect(result.current).toBe("splash");
  act(() => {
    jest.advanceTimersByTime(1);
  });
  expect(result.current).toBe("auth");
  jest.useRealTimers();
});
