import { expect, test } from "@playwright/test";

// Must match playwright.config.ts default; the runner script always injects
// SB_E2E_BASE_URL, these are fallbacks for manual runs only.
const base = process.env.SB_E2E_BASE_URL ?? "http://127.0.0.1:36470";
const token = process.env.SB_E2E_TOKEN ?? "";
const admin = process.env.SB_E2E_ADMIN ?? "admin";
const password = process.env.SB_E2E_PASSWORD ?? "E2e-Password-1234";

test.describe("embedded console", () => {
  test("bootstrap or login lands on the overview", async ({ page }) => {
    await page.goto(base + "/");
    if (token) {
      await page
        .getByText("Initialize a fresh instance with a CLI token")
        .click();
      await page.fill("#token", token);
      await page.fill("#username", admin);
      await page.fill("#password", password);
      await page.locator('button[type="submit"]').click();
    } else {
      await page.fill("#username", admin);
      await page.fill("#password", password);
      await page.locator('button[type="submit"]').click();
    }
    await expect(page.getByText("Protection status")).toBeVisible();
    await expect(page.getByRole("navigation")).toContainText("Overview");
  });

  test("SPA deep links reload onto the right view", async ({ page }) => {
    await page.goto(base + "/");
    await page.fill("#username", admin);
    await page.fill("#password", password);
    await page.locator('button[type="submit"]').click();
    await expect(page.getByText("Protection status")).toBeVisible();
    await page.goto(base + "/#/backups");
    await page.reload();
    await expect(page.getByText("Recent backups")).toBeVisible();
    await page.goto(base + "/#/notifications");
    await page.reload();
    await expect(page.getByText("Delivery log")).toBeVisible();
  });

  test("API error surfaces are not swallowed by the SPA fallback", async ({
    page,
  }) => {
    // Anonymous API calls must return JSON 401, never the index.html shell.
    const resp = await page.request.get(base + "/api/tasks");
    expect(resp.status()).toBe(401);
    const body = await resp.json();
    expect(body.code).toBe("unauthenticated");
  });

  test("language switch re-renders and persists", async ({ page }) => {
    await page.goto(base + "/");
    await page.fill("#username", admin);
    await page.fill("#password", password);
    await page.locator('button[type="submit"]').click();
    await expect(page.getByText("Protection status")).toBeVisible();
    await page.locator(".lang-opt", { hasText: "中文" }).click();
    await expect(page.getByText("保护状态").first()).toBeVisible();
    await page.reload();
    await expect(page.getByText("保护状态").first()).toBeVisible();
    await page.locator(".lang-opt", { hasText: "EN" }).click();
    await expect(page.getByText("Protection status").first()).toBeVisible();
  });

  test("failed registration shows a localized server message", async ({
    page,
  }) => {
    await page.goto(base + "/");
    await page.fill("#username", admin);
    await page.fill("#password", password);
    await page.locator('button[type="submit"]').click();
    // A fresh instance shows the empty-state CTA, not the panel button.
    await page.getByText("Register your first database").click();
    await page.getByText("Paste connection string").click();
    await page.fill("#db-name", "e2e-bad-" + Date.now());
    await page.fill(
      "#db-uri",
      "postgresql://u:wrong@127.0.0.1:1/nodb?sslmode=disable",
    );
    await page.getByText("Test & register").click();
    // server-side message, either language, must reach the user
    await expect(
      page.getByText(/connection test failed|连接测试失败/),
    ).toBeVisible({ timeout: 15_000 });
  });
});
