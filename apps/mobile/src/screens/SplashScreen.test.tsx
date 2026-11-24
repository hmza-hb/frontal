import { render, screen } from "@testing-library/react-native";
import { SplashScreen } from "./SplashScreen";

test("shows the product name on a launch screen", () => {
  render(<SplashScreen />);
  expect(screen.getByTestId("splash-screen")).toBeOnTheScreen();
  expect(screen.getByText("Frontal")).toBeOnTheScreen();
});
