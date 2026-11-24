import { expect, test } from "@playwright/test";

test("hands off from splash to auth", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("splash-screen").or(page.getByTestId("auth-destination"))).toBeVisible();
  await expect(page.getByTestId("auth-destination")).toBeVisible();
  await expect(page.getByText("Sign in")).toBeVisible();
  await expect(page.getByText("Continue with Google")).toHaveCount(0);
  await expect(page.getByText("Continue as guest")).toHaveCount(0);
});
