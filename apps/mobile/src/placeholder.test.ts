import { placeholderTitle } from "./placeholder";

test("names the product", () => {
  expect(placeholderTitle()).toBe("Frontal");
});