import { expect, test } from "@playwright/test";

test("serves the placeholder", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Frontal")).toBeVisible();
});
