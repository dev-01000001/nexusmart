import fs from "node:fs";
import { defineConfig } from "@playwright/test";

const siteBaseUrl = (
  process.env.SITE_BASE_URL?.trim() || "http://bach-dang.nexusmart.localhost:3300"
).replace(/\/+$/, "");

const siteRootDomain = (
  process.env.SITE_ROOT_DOMAIN?.trim() || "nexusmart.localhost"
).toLowerCase();

// Preserve default Mailpit URL in process.env for helper consumers.
if (!process.env.MAILPIT_URL?.trim()) {
  process.env.MAILPIT_URL = "http://127.0.0.1:8025";
}

const CHROMIUM_CANDIDATES = [
  "/home/dev/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome",
  "/usr/bin/google-chrome",
];

function resolveChromiumExecutable(): string | undefined {
  const override = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?.trim();
  if (override) {
    return override;
  }
  for (const candidate of CHROMIUM_CANDIDATES) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
}

const chromiumExecutablePath = resolveChromiumExecutable();
const hostResolverRules = `MAP *.${siteRootDomain} 127.0.0.1,MAP ${siteRootDomain} 127.0.0.1`;

export default defineConfig({
  testDir: "./specs",
  outputDir: "./test-results",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  timeout: 90_000,
  expect: {
    timeout: 15_000,
  },
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  use: {
    baseURL: siteBaseUrl,
    viewport: { width: 1280, height: 800 },
    reducedMotion: "reduce",
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        browserName: "chromium",
        launchOptions: {
          ...(chromiumExecutablePath ? { executablePath: chromiumExecutablePath } : {}),
          args: [`--host-resolver-rules=${hostResolverRules}`],
        },
      },
    },
  ],
});
