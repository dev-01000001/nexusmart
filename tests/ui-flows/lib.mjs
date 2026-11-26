// Shared helpers for NexusMart functional UI flow checks (works on both old 43101 & new 43110).
// Locator policy (s9-qa.md §3): ONLY role + Vietnamese visible text (getByRole, getByLabel, getByText).
import process from "node:process";

export const DEFAULT_PW_MODULE =
  process.env.PLAYWRIGHT_MODULE ||
  "/home/tai/.npm/_npx/9833c18b2d85bc59/node_modules/playwright/index.mjs";

export const MAILPIT_URL = (
  process.env.MAILPIT_URL || "http://127.0.0.1:48025"
).replace(/\/+$/, "");

export const CREDS = {
  admin: {
    email: process.env.ADMIN_EMAIL || "admin@nexusmart.local",
    password: process.env.ADMIN_PASSWORD || "AdminDevPassword123!",
  },
  seller: {
    email: process.env.SELLER_EMAIL || "seller@nexusmart.local",
    password: process.env.SELLER_PASSWORD || "SellerDemo123!",
  },
  buyer: {
    email: process.env.BUYER_EMAIL || "buyer@nexusmart.local",
    password: process.env.BUYER_PASSWORD || "BuyerDemo123!",
    phone: process.env.BUYER_PHONE || "0912486305",
  },
};

export const SEED_SITES = ["bach-dang", "sai-gon", "da-nang", "ha-noi"];

export function rootOrigin(port) {
  return `http://nexusmart.localhost:${port}`;
}

export function siteOrigin(site, port) {
  return `http://${site}.nexusmart.localhost:${port}`;
}

export async function launchBrowser() {
  const { chromium } = await import(DEFAULT_PW_MODULE);
  return chromium.launch({
    channel: "chrome",
    // ha-noi redirects 308 to its verified custom primary_host (hanoi-knit.verified.test).
    args: ["--host-resolver-rules=MAP hanoi-knit.verified.test [::1]"],
  });
}

export async function createContext(browser) {
  return browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
}

/**
 * Login through the browser page's own fetch() so Chromium's host-resolver-rules apply
 * (needed for custom domains such as hanoi-knit.verified.test) and cookies land on the
 * final canonical origin.
 */
export async function loginViaBrowser(page, entryUrl, { email, password }) {
  await gotoPage(page, entryUrl);
  let lastResult = null;
  for (let attempt = 0; attempt < 2; attempt++) {
    const result = await page.evaluate(
      async ({ email: e, password: p }) => {
        const res = await fetch("/api/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email: e, password: p }),
        });
        return {
          ok: res.ok,
          status: res.status,
          text: (await res.text()).slice(0, 200),
        };
      },
      { email, password },
    );
    lastResult = result;
    if (result.ok) return;
    if (result.status === 429 && attempt === 0) {
      await page.waitForTimeout(13_000);
      continue;
    }
    break;
  }
  throw new Error(
    `Login ${email} @ ${page.url()} -> HTTP ${lastResult?.status}: ${lastResult?.text}`,
  );
}

export async function gotoPage(page, url) {
  let lastErr;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const res = await page.goto(url, {
        waitUntil: "domcontentloaded",
        timeout: 60_000,
      });
      const status = res?.status() ?? 0;
      if (
        (status === 429 ||
          status === 503 ||
          status === 502 ||
          status === 500) &&
        attempt < 2
      ) {
        await page.waitForTimeout(attempt === 0 ? 5_000 : 12_000);
        continue;
      }
      if (status >= 400 || status === 0) {
        throw new Error(`GET ${url} -> HTTP ${status}`);
      }
      await page
        .waitForLoadState("networkidle", { timeout: 15_000 })
        .catch(() => {});
      await page.waitForTimeout(250);
      return res;
    } catch (err) {
      lastErr = err;
      if (attempt < 2 && /Timeout/i.test(err?.message || "")) {
        await page.waitForTimeout(3_000);
        continue;
      }
      throw err;
    }
  }
  throw lastErr;
}

/**
 * Special error class to mark a flow as KNOWN (unpassable on old baseline due to missing data / pre-existing issue).
 */
export class KnownIssueError extends Error {
  constructor(message) {
    super(message);
    this.name = "KnownIssueError";
  }
}

export async function runFlowList(flows, { only } = {}) {
  const results = [];
  for (const flow of flows) {
    if (only && !flow.name.includes(only)) continue;
    const t0 = Date.now();
    try {
      const note = await flow.fn();
      const ms = Date.now() - t0;
      results.push({ name: flow.name, status: "PASS", note: note || "", ms });
      console.log(
        `PASS ${flow.name}${note ? ` (${note})` : ""} [${(ms / 1000).toFixed(1)}s]`,
      );
    } catch (err) {
      const ms = Date.now() - t0;
      if (err instanceof KnownIssueError) {
        results.push({
          name: flow.name,
          status: "KNOWN",
          reason: err.message,
          ms,
        });
        console.log(
          `KNOWN ${flow.name} — ${err.message} [${(ms / 1000).toFixed(1)}s]`,
        );
      } else {
        const reason = (err?.message || String(err))
          .replace(/\s+/g, " ")
          .slice(0, 260);
        results.push({ name: flow.name, status: "FAIL", reason, ms });
        console.log(
          `FAIL ${flow.name} — ${reason} [${(ms / 1000).toFixed(1)}s]`,
        );
      }
    }
    // Gentle pacing between flows avoids tripping the shared gateway rate limiter.
    await new Promise((r) => setTimeout(r, 800));
  }
  return results;
}

export function assert(cond, message) {
  if (!cond) throw new Error(message);
}

/**
 * Wait for first visible locator among candidates (handles minor label variations between old & new UI).
 */
export async function waitAnyVisible(locators, timeout = 10_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    for (const loc of locators) {
      try {
        if (await loc.first().isVisible()) return loc.first();
      } catch {
        // ignore transient detachment
      }
    }
    await new Promise((r) => setTimeout(r, 150));
  }
  throw new Error("Không thấy phần tử mong đợi hiển thị đúng hạn.");
}
