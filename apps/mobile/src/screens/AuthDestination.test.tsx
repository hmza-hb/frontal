import { fireEvent, render, screen } from "@testing-library/react-native";
import { AuthDestination } from "./AuthDestination";

const idleProps = {
  email: "",
  password: "",
  errorMessage: null,
  busy: false,
  onEmailChange: jest.fn(),
  onPasswordChange: jest.fn(),
  onGoogle: jest.fn(),
  onEmailSignIn: jest.fn(),
  onGuest: jest.fn(),
};

test("renders actions without handlers", () => {
  render(<AuthDestination />);
  expect(screen.getByTestId("auth-destination")).toBeOnTheScreen();
  expect(screen.getByTestId("auth-google")).toBeOnTheScreen();
});

test("exposes google, email, and guest actions", () => {
  render(<AuthDestination {...idleProps} />);
  fireEvent.press(screen.getByTestId("auth-google"));
  fireEvent.press(screen.getByTestId("auth-guest"));
  fireEvent.press(screen.getByTestId("auth-email-submit"));
  expect(idleProps.onGoogle).toHaveBeenCalled();
  expect(idleProps.onGuest).toHaveBeenCalled();
  expect(idleProps.onEmailSignIn).toHaveBeenCalled();
  expect(screen.getByTestId("auth-email")).toBeOnTheScreen();
  expect(screen.getByTestId("auth-password")).toBeOnTheScreen();
});
