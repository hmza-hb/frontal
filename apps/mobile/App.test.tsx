import { render, screen } from "@testing-library/react-native";
import App from "./App";

test("shows the product name", () => {
  render(<App />);
  expect(screen.getByText("Frontal")).toBeOnTheScreen();
});