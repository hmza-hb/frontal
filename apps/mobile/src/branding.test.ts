import {
  authDestinationTitle,
  continueAsGuestLabel,
  continueWithEmailLabel,
  continueWithGoogleLabel,
  emailFieldLabel,
  passwordFieldLabel,
  productName,
  settingsDestinationTitle,
} from "./branding";

test("names the product", () => {
  expect(productName()).toBe("Frontal");
});

test("names auth and settings handoffs", () => {
  expect(authDestinationTitle()).toBe("Sign in");
  expect(settingsDestinationTitle()).toBe("Settings");
});

test("names auth actions", () => {
  expect(continueWithGoogleLabel()).toBe("Continue with Google");
  expect(continueWithEmailLabel()).toBe("Continue with email");
  expect(continueAsGuestLabel()).toBe("Continue as guest");
  expect(emailFieldLabel()).toBe("Email");
  expect(passwordFieldLabel()).toBe("Password");
});
