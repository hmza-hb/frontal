import { render, screen } from "@testing-library/react-native";
import { SettingsDestination } from "./SettingsDestination";

test("shows the settings handoff without preference toggles", () => {
  render(<SettingsDestination />);
  expect(screen.getByTestId("settings-destination")).toBeOnTheScreen();
  expect(screen.getByText("Settings")).toBeOnTheScreen();
  expect(screen.queryByText("Cloud sync")).toBeNull();
});
