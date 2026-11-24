import { authDestinationTitle, productName } from "./branding";

test("names the product", () => {
  expect(productName()).toBe("Frontal");
});

test("names the auth handoff", () => {
  expect(authDestinationTitle()).toBe("Sign in");
});
