import { render, screen } from "@testing-library/react-native";
import { AuthDestination } from "./AuthDestination";

test("shows the auth handoff without account actions", () => {
  render(<AuthDestination />);
  expect(screen.getByTestId("auth-destination")).toBeOnTheScreen();
  expect(screen.getByText("Sign in")).toBeOnTheScreen();
  expect(screen.queryByText("Continue with Google")).toBeNull();
  expect(screen.queryByText("Continue as guest")).toBeNull();
});
