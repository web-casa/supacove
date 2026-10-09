import { defineConfig } from "@playwright/test";

// E2E runs against the FINAL embedded artifact: the helper in the Makefile
// starts bin/supacove on a free port with a throwaway data dir and exports
// SB_E2E_BASE_URL / SB_E2E_TOKEN / SB_E2E_ADMIN before invoking this config.
export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  retries: 0,
  use: {
    baseURL: process.env.SB_E2E_BASE_URL ?? "http://127.0.0.1:36470",
    // The specs assert English UI strings; pin the locale so a zh-default
    // runner cannot flip the console language mid-assertion.
    locale: "en-US",
    viewport: { width: 1440, height: 900 },
    // Local runs reuse the system Chromium; CI uses the Playwright-managed
    // browser installed by `npx playwright install chromium`.
    launchOptions: process.env.SB_E2E_CHROMIUM
      ? { executablePath: process.env.SB_E2E_CHROMIUM, args: ["--no-sandbox"] }
      : undefined,
  },
});
