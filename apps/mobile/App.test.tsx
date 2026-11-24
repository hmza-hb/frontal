import { act, render, screen } from "@testing-library/react-native";
import App from "./App";
import { splashHoldMs } from "./src/launch/launchScreen";

jest.mock("./src/launch/nativeSplash", () => ({
  preventNativeSplashAutoHide: jest.fn(),
  hideNativeSplash: jest.fn(() => Promise.resolve()),
}));

test("starts on splash then hands off to auth", () => {
  jest.useFakeTimers();
  render(<App />);
  expect(screen.getByTestId("splash-screen")).toBeOnTheScreen();
  expect(screen.getByText("Frontal")).toBeOnTheScreen();
  act(() => {
    jest.advanceTimersByTime(splashHoldMs);
  });
  expect(screen.getByTestId("auth-destination")).toBeOnTheScreen();
  expect(screen.getByText("Sign in")).toBeOnTheScreen();
  expect(screen.queryByTestId("splash-screen")).toBeNull();
  jest.useRealTimers();
});
