#!/usr/bin/env node
// Phase 5 QA (S9d): capture open overlays (Radix Dialog/Sheet/Popover/Command/Dropdown/Tooltip/Sonner),
// keyboard Tab focus rings, and non-reduced-motion Reveal animation frames at fixed viewport filenames
// so `.agents/tools/ui-diff.sh <dir-base> <dir-cand>` can pixel-compare across ports.
//
// Usage:
//   flock /tmp/nm-web-browser.lock systemd-run --user --scope --slice=nm3b.slice -p MemoryMax=2G \
//     node tests/ui-flows/states.mjs --port 43102 --out .runs/shadcn-redesign/p5/s9d/run1 [--only <pattern>]
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import {
  CREDS,
  launchBrowser,
  rootOrigin,
  siteOrigin,
  waitAnyVisible,
} from "./lib.mjs";

const argv = process.argv.slice(2);
const opt = (name, def = "") => {
  const i = argv.indexOf(`--${name}`);
  return i === -1 ? def : (argv[i + 1] ?? def);
};

const port = opt("port");
const outDir = opt("out");
const only = opt("only");

if (!port || !outDir) {
  console.error(
    "Usage: node tests/ui-flows/states.mjs --port <p> --out <dir> [--only <pattern>]",
  );
  process.exit(2);
}

const OUT_ABS = path.resolve(outDir);
await fs.mkdir(OUT_ABS, { recursive: true });

const DESKTOP_VP = { width: 1440, height: 900 };
const MOBILE_VP = { width: 390, height: 844 };
const RATE_LIMIT_WAIT_MS = Number(process.env.RATE_LIMIT_WAIT_MS || 61_000);
const RATE_LIMIT_RE =
  /Bạn thao tác (hơi |quá )?nhanh|quá nhiều yêu cầu|Too Many Requests|Chưa kiểm tra được/i;

const records = [];

function shouldRun(name) {
  return !only || name.toLowerCase().includes(only.toLowerCase());
}

function recordOk(name, category, note = "") {
  records.push({ status: "OK", name, category, note });
  console.log(`OK   [${category}] ${name}${note ? ` — ${note}` : ""}`);
}

function recordSkip(name, category, reason) {
  records.push({ status: "SKIP", name, category, note: reason });
  console.log(`SKIP [${category}] ${name}: ${reason}`);
}

function recordFail(name, category, err) {
  const msg = (err?.message || String(err)).replace(/\s+/g, " ").slice(0, 260);
  records.push({ status: "FAIL", name, category, note: msg });
  console.log(`FAIL [${category}] ${name}: ${msg}`);
}

async function pageShowsRateLimit(page) {
  const txt = await page
    .evaluate(() => document.body?.innerText?.slice(0, 1500) || "")
    .catch(() => "");
  return RATE_LIMIT_RE.test(txt);
}

async function waitFontsAndImages(page) {
  await page
    .evaluate(async () => {
      if (document.fonts?.ready) {
        await Promise.race([
          document.fonts.ready,
          new Promise((r) => setTimeout(r, 3_000)),
        ]);
      }
      const imgs = Array.from(document.images);
      await Promise.all(
        imgs.map((img) => {
          if (img.complete) return Promise.resolve();
          return new Promise((resolve) => {
            const t = setTimeout(resolve, 4_000);
            img.addEventListener(
              "load",
              () => {
                clearTimeout(t);
                resolve();
              },
              { once: true },
            );
            img.addEventListener(
              "error",
              () => {
                clearTimeout(t);
                resolve();
              },
              { once: true },
            );
          });
        }),
      );
    })
    .catch(() => {});
}

async function gotoSafe(page, url) {
  let saw429 = false;
  const onRes = (r) => {
    if (r.status() === 429) saw429 = true;
  };
  page.on("response", onRes);
  try {
    let lastErr;
    for (let attempt = 0; attempt < 4; attempt++) {
      saw429 = false;
      try {
        const res = await page.goto(url, {
          waitUntil: "domcontentloaded",
          timeout: 60_000,
        });
        const status = res?.status() ?? 0;
        await page
          .waitForLoadState("networkidle", { timeout: 15_000 })
          .catch(() => {});
        await page.waitForTimeout(350);

        const rateLimited =
          status === 429 ||
          (saw429 && status >= 500) ||
          (await pageShowsRateLimit(page));
        if (rateLimited && attempt < 3) {
          console.log(
            `[rate-limit] GET ${url} (HTTP ${status}) -> chờ ${Math.round(RATE_LIMIT_WAIT_MS / 1000)}s rồi thử lại...`,
          );
          await page.waitForTimeout(RATE_LIMIT_WAIT_MS);
          continue;
        }
        if (
          (status === 500 || status === 502 || status === 503) &&
          attempt < 2
        ) {
          await page.waitForTimeout(attempt === 0 ? 5_000 : 12_000);
          continue;
        }
        if (status >= 400 || status === 0) {
          throw new Error(`GET ${url} -> HTTP ${status}`);
        }
        await waitFontsAndImages(page);
        return res;
      } catch (err) {
        lastErr = err;
        if (attempt < 2 && /Timeout/i.test(err?.message || "")) {
          await page.waitForTimeout(4_000);
          continue;
        }
        throw err;
      }
    }
    throw lastErr;
  } finally {
    page.off("response", onRes);
  }
}

async function loginSafe(page, entryUrl, creds) {
  await gotoSafe(page, entryUrl);
  let lastResult = null;
  for (let attempt = 0; attempt < 3; attempt++) {
    const result = await page.evaluate(
      async ({ email, password }) => {
        const res = await fetch("/api/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email, password }),
        });
        return {
          ok: res.ok,
          status: res.status,
          text: (await res.text()).slice(0, 200),
        };
      },
      { email: creds.email, password: creds.password },
    );
    lastResult = result;
    if (result.ok) return;
    if (result.status === 429 && attempt < 2) {
      console.log(
        `[rate-limit] login ${creds.email} -> chờ ${Math.round(RATE_LIMIT_WAIT_MS / 1000)}s rồi thử lại...`,
      );
      await page.waitForTimeout(RATE_LIMIT_WAIT_MS);
      continue;
    }
    break;
  }
  throw new Error(
    `Login ${creds.email} @ ${page.url()} -> HTTP ${lastResult?.status}: ${lastResult?.text}`,
  );
}

async function waitFiniteAnimations(page) {
  await page
    .evaluate(async () => {
      if (typeof document.getAnimations !== "function") return;
      const anims = document.getAnimations().filter((a) => {
        const timing = a.effect?.getComputedTiming?.();
        return timing && Number.isFinite(timing.iterations);
      });
      await Promise.race([
        Promise.all(anims.map((a) => a.finished.catch(() => {}))),
        new Promise((r) => setTimeout(r, 1_500)),
      ]);
    })
    .catch(() => {});
}

async function syncFocusedInputCaret(container, page) {
  const input = container
    .getByRole("combobox")
    .or(container.getByRole("searchbox"))
    .or(container.getByRole("textbox"))
    .first();
  if (await input.isVisible().catch(() => false)) {
    await input.click();
    await waitFiniteAnimations(page);
    await page.waitForTimeout(100);
  }
}

async function snap(page, filename) {
  await page.screenshot({
    path: path.join(OUT_ABS, filename),
    fullPage: false,
    caret: "initial",
  });
}

async function runSafe(name, category, fn) {
  if (!shouldRun(name)) return;
  try {
    const result = await fn();
    if (result && typeof result === "object" && result.skip) {
      recordSkip(name, category, result.skip);
    } else {
      recordOk(name, category, typeof result === "string" ? result : "");
    }
  } catch (err) {
    recordFail(name, category, err);
  }
}

async function clearCartViaUi(page, origin) {
  await gotoSafe(page, `${origin}/gio-hang`);
  const emptyText = page
    .getByText(/Giỏ hàng đang trống|Chưa có sản phẩm/i)
    .first();
  if (await emptyText.isVisible().catch(() => false)) {
    return;
  }

  const clearAll = page
    .getByRole("button", {
      name: /Xóa giỏ hàng|Xoá giỏ hàng/i,
    })
    .first();
  if (await clearAll.isVisible().catch(() => false)) {
    await clearAll.click();
    const confirmAll = page
      .getByRole("button", { name: /^Xóa tất cả$|^Xoá tất cả$/i })
      .first();
    if (await confirmAll.isVisible({ timeout: 3_000 }).catch(() => false)) {
      await confirmAll.click();
    }
    await emptyText
      .waitFor({ state: "visible", timeout: 10_000 })
      .catch(() => {});
    await page.waitForTimeout(400);
    if (await emptyText.isVisible().catch(() => false)) {
      return;
    }
  }

  for (let i = 0; i < 6; i++) {
    const rmBtn = page.getByRole("button", { name: /^Xóa$|^Xoá$/i }).first();
    if (!(await rmBtn.isVisible().catch(() => false))) break;
    await rmBtn.click();
    await page.waitForTimeout(600);
  }
  await emptyText.waitFor({ state: "visible", timeout: 8_000 }).catch(() => {});
  await page.waitForTimeout(300);
}

async function describeFocusedElement(page) {
  return page
    .evaluate(() => {
      const el = document.activeElement;
      if (!el || el === document.body) return "body";
      const tag = el.tagName.toLowerCase();
      const role = el.getAttribute("role") || "";
      const label =
        el.getAttribute("aria-label") ||
        (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
      return `${tag}${role ? `[role=${role}]` : ""}${label ? ` "${label}"` : ""}`;
    })
    .catch(() => "unknown");
}

async function captureTabFocus3(page, url, filename) {
  await page.setViewportSize(DESKTOP_VP);
  await gotoSafe(page, url);
  await page.waitForTimeout(450);
  await page.evaluate(() => {
    window.scrollTo(0, 0);
    if (document.activeElement && document.activeElement !== document.body) {
      document.activeElement.blur();
    }
  });
  await page.waitForTimeout(150);
  for (let i = 0; i < 3; i++) {
    await page.keyboard.press("Tab");
    await page.waitForTimeout(120);
  }
  await page.waitForTimeout(300);
  const focused = await describeFocusedElement(page);
  await snap(page, filename);
  return `Tab×3 -> ${focused}`;
}

async function runSellerStates(browser, rootUrl) {
  const needsSeller = [
    "seller-cmdk-1440",
    "seller-account-menu-1440",
    "seller-order-sheet-1440",
    "seller-sidebar-tooltip-1440",
    "seller-mobile-sheet-390",
    "focus-tab3-seller-1440",
  ].some(shouldRun);
  if (!needsSeller) return;

  const ctx = await browser.newContext({
    viewport: DESKTOP_VP,
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  try {
    await loginSafe(page, `${rootUrl}/dang-nhap`, CREDS.seller);

    // Load /seller ONCE on desktop and reuse it across ⌘K, account menu, tooltip, and Tab×3 focus
    // so we avoid bursting redundant SSR gateway calls on /seller.
    const needsSellerHomeDesktop = [
      "seller-cmdk-1440",
      "seller-account-menu-1440",
      "seller-sidebar-tooltip-1440",
      "focus-tab3-seller-1440",
    ].some(shouldRun);
    if (needsSellerHomeDesktop) {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${rootUrl}/seller`);
      await page.evaluate(() => {
        try {
          window.localStorage.removeItem("nm-seller-sidebar");
        } catch {
          // ignore
        }
      });
      await page.waitForTimeout(500);
    }

    await runSafe("focus-tab3-seller-1440", "static", async () => {
      await page.evaluate(() => {
        window.scrollTo(0, 0);
        if (
          document.activeElement &&
          document.activeElement !== document.body
        ) {
          document.activeElement.blur();
        }
      });
      await page.waitForTimeout(150);
      for (let i = 0; i < 3; i++) {
        await page.keyboard.press("Tab");
        await page.waitForTimeout(120);
      }
      await waitFiniteAnimations(page);
      await page.waitForTimeout(250);
      const focused = await describeFocusedElement(page);
      await snap(page, "focus-tab3-seller-1440.png");
      await page.evaluate(() => {
        if (
          document.activeElement &&
          document.activeElement !== document.body
        ) {
          document.activeElement.blur();
        }
      });
      return `Tab×3 -> ${focused}`;
    });

    await runSafe("seller-cmdk-1440", "static", async () => {
      const trigger = page
        .getByRole("button", { name: /Tìm kiếm hoặc mở thao tác nhanh/i })
        .first();
      if (!(await trigger.isVisible().catch(() => false))) {
        return {
          skip: "Không thấy nút mở CommandDialog (⌘K) ở topbar người bán",
        };
      }
      await trigger.click();
      const dialog = page.getByRole("dialog").first();
      await dialog.waitFor({ state: "visible", timeout: 8_000 });
      await waitFiniteAnimations(page);
      await page.waitForTimeout(450);
      await syncFocusedInputCaret(dialog, page);
      await snap(page, "seller-cmdk-1440.png");
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden", timeout: 5_000 }).catch(() => {});
      await page.waitForTimeout(200);
      return "CommandDialog ⌘K mở";
    });

    await runSafe("seller-account-menu-1440", "static", async () => {
      const trigger = page
        .getByRole("button", { name: /Menu tài khoản/i })
        .first();
      if (!(await trigger.isVisible().catch(() => false))) {
        return { skip: "Không thấy nút Menu tài khoản ở topbar người bán" };
      }
      await trigger.click();
      const menu = page.getByRole("menu").first();
      await menu.waitFor({ state: "visible", timeout: 8_000 });
      await waitFiniteAnimations(page);
      await page.waitForTimeout(450);
      await snap(page, "seller-account-menu-1440.png");
      await page.keyboard.press("Escape");
      await menu.waitFor({ state: "hidden", timeout: 5_000 }).catch(() => {});
      await page.waitForTimeout(200);
      return "DropdownMenu tài khoản mở";
    });

    await runSafe("seller-sidebar-tooltip-1440", "static", async () => {
      const toggleBtn = page
        .getByRole("button", { name: /Mở hoặc thu gọn menu điều hướng/i })
        .first();
      if (!(await toggleBtn.isVisible().catch(() => false))) {
        return { skip: "Không thấy nút thu gọn sidebar người bán" };
      }
      const ordersLink = page
        .getByRole("navigation", { name: /Điều hướng chính/i })
        .getByRole("link", { name: /Đơn hàng/i })
        .first();
      await toggleBtn.click();
      await page.waitForTimeout(500);

      await ordersLink.hover();
      const tooltip = page.getByRole("tooltip").first();
      if (!(await tooltip.isVisible({ timeout: 5_000 }).catch(() => false))) {
        await toggleBtn.click();
        await page.waitForTimeout(500);
        await ordersLink.hover();
        if (!(await tooltip.isVisible({ timeout: 5_000 }).catch(() => false))) {
          return {
            skip: "Tooltip không xuất hiện khi rê chuột vào sidebar thu gọn",
          };
        }
      }
      await waitFiniteAnimations(page);
      await page.waitForTimeout(400);
      await snap(page, "seller-sidebar-tooltip-1440.png");
      await page.mouse.move(700, 450);
      await toggleBtn.click();
      await page.waitForTimeout(350);
      return "Tooltip sidebar thu gọn (Đơn hàng)";
    });

    await runSafe("seller-order-sheet-1440", "static", async () => {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${rootUrl}/seller/don-hang`);
      await page
        .getByRole("heading", { level: 1, name: /Đơn hàng/i })
        .waitFor({ state: "visible", timeout: 10_000 });
      const firstOrderBtn = page.getByRole("button", { name: /^NM-/ }).first();
      if (
        !(await firstOrderBtn.isVisible({ timeout: 8_000 }).catch(() => false))
      ) {
        return {
          skip: "Không có đơn NM- trên /seller/don-hang để mở Sheet xem nhanh",
        };
      }
      const code = ((await firstOrderBtn.textContent()) || "").trim();
      await firstOrderBtn.click();
      const sheet = page.getByRole("dialog").first();
      await sheet.waitFor({ state: "visible", timeout: 8_000 });
      await sheet
        .getByRole("link", { name: /Mở trang chi tiết|Chi tiết/i })
        .first()
        .waitFor({ state: "visible", timeout: 8_000 });
      await waitFiniteAnimations(page);
      await page.waitForTimeout(450);
      await snap(page, "seller-order-sheet-1440.png");
      await page.keyboard.press("Escape");
      await sheet.waitFor({ state: "hidden", timeout: 5_000 }).catch(() => {});
      return `Sheet xem nhanh đơn ${code}`;
    });

    await runSafe("seller-mobile-sheet-390", "static", async () => {
      const mobilePage = await ctx.newPage();
      try {
        await mobilePage.setViewportSize(MOBILE_VP);
        await gotoSafe(mobilePage, `${rootUrl}/seller`);
        await mobilePage.waitForTimeout(450);
        const menuBtn = mobilePage
          .getByRole("button", { name: /Mở hoặc thu gọn menu điều hướng/i })
          .first();
        if (!(await menuBtn.isVisible().catch(() => false))) {
          return { skip: "Không thấy nút mở menu mobile 390 ở người bán" };
        }
        await menuBtn.click();
        const sheet = mobilePage.getByRole("dialog").first();
        await sheet.waitFor({ state: "visible", timeout: 8_000 });
        await waitFiniteAnimations(mobilePage);
        await mobilePage.waitForTimeout(450);
        await snap(mobilePage, "seller-mobile-sheet-390.png");
        return "Sheet menu 390 người bán";
      } finally {
        await mobilePage.close().catch(() => {});
      }
    });
  } finally {
    await ctx.close().catch(() => {});
  }
}

async function runAdminStates(browser, rootUrl) {
  const needsAdmin = [
    "admin-cmdk-1440",
    "admin-shop-sheet-1440",
    "admin-confirm-dialog-1440",
    "admin-mobile-sheet-390",
    "focus-tab3-admin-1440",
  ].some(shouldRun);
  if (!needsAdmin) return;

  const ctx = await browser.newContext({
    viewport: DESKTOP_VP,
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  try {
    await loginSafe(page, `${rootUrl}/dang-nhap`, CREDS.admin);

    await runSafe("admin-cmdk-1440", "static", async () => {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${rootUrl}/admin`);
      await page.waitForTimeout(500);
      const trigger = page
        .getByRole("button", { name: /Tìm kiếm hoặc mở thao tác nhanh/i })
        .first();
      if (!(await trigger.isVisible().catch(() => false))) {
        return {
          skip: "Không thấy nút mở CommandDialog (⌘K) ở topbar quản trị",
        };
      }
      await trigger.click();
      const dialog = page.getByRole("dialog").first();
      await dialog.waitFor({ state: "visible", timeout: 8_000 });
      await page.waitForTimeout(450);
      await syncFocusedInputCaret(dialog, page);
      await snap(page, "admin-cmdk-1440.png");
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden", timeout: 5_000 }).catch(() => {});
      return "CommandDialog ⌘K quản trị";
    });

    const needShopSheet =
      shouldRun("admin-shop-sheet-1440") ||
      shouldRun("admin-confirm-dialog-1440");
    if (needShopSheet) {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${rootUrl}/admin/gian-hang`);
      await page
        .getByRole("heading", { level: 1, name: /gian hàng/i })
        .waitFor({ state: "visible", timeout: 10_000 });
      const activeTab = page
        .getByRole("tab", { name: /Đang hoạt động/i })
        .first();
      if (await activeTab.isVisible().catch(() => false)) {
        await activeTab.click();
        await page.waitForTimeout(500);
      }

      const openFirstShopSheet = async () => {
        const queueList = page
          .getByRole("list", { name: /Danh sách hồ sơ gian hàng/i })
          .first();
        const detailBtn = page
          .getByRole("button", { name: /Xem chi tiết/i })
          .first();
        const tableFirstRow = page
          .getByRole("table", { name: /Danh sách hồ sơ gian hàng/i })
          .getByRole("row")
          .nth(1);
        const target = (await queueList.isVisible().catch(() => false))
          ? queueList.getByRole("button").first()
          : (await detailBtn.isVisible().catch(() => false))
            ? detailBtn
            : tableFirstRow
                .getByRole("button")
                .first()
                .or(tableFirstRow)
                .first();
        if (!(await target.isVisible({ timeout: 8_000 }).catch(() => false))) {
          return false;
        }
        await target.click();
        const reasonInput = page
          .getByLabel(/Đề xuất tạm khóa gian hàng/i)
          .first();
        await reasonInput.waitFor({ state: "visible", timeout: 10_000 });
        // Wait for async owner / bank-account detail fetches inside the Sheet to finish
        const sheet = page.getByRole("dialog").first();
        const loadingInside = sheet.getByText(/Đang tải/i).first();
        await loadingInside
          .waitFor({ state: "hidden", timeout: 12_000 })
          .catch(() => {});
        await waitFiniteAnimations(page);
        await page.waitForTimeout(450);
        return true;
      };

      let sheetOpened = false;
      await runSafe("admin-shop-sheet-1440", "static", async () => {
        sheetOpened = await openFirstShopSheet();
        if (!sheetOpened) {
          return {
            skip: "Không tìm thấy gian hàng đang hoạt động để mở Sheet",
          };
        }
        await snap(page, "admin-shop-sheet-1440.png");
        return "Sheet kiểm tra gian hàng mở";
      });

      await runSafe("admin-confirm-dialog-1440", "static", async () => {
        if (!sheetOpened) {
          sheetOpened = await openFirstShopSheet();
          if (!sheetOpened) {
            return {
              skip: "Không mở được Sheet gian hàng để gọi hộp thoại xác nhận",
            };
          }
        }
        const reasonInput = page
          .getByLabel(/Đề xuất tạm khóa gian hàng/i)
          .first();
        await reasonInput.fill("QA-S9d kiểm thử hộp xác nhận rồi Esc");
        const reqBtn = page
          .getByRole("button", { name: /Yêu cầu khóa gian hàng/i })
          .first();
        await reqBtn.click();
        const confirmDialog = await waitAnyVisible(
          [
            page.getByRole("alertdialog"),
            page.getByRole("dialog", { name: /Xác nhận/i }),
          ],
          8_000,
        );
        await page.waitForTimeout(450);
        await snap(page, "admin-confirm-dialog-1440.png");
        // Close with Escape — NEVER confirm!
        await page.keyboard.press("Escape");
        await confirmDialog
          .waitFor({ state: "hidden", timeout: 5_000 })
          .catch(() => {});
        await page.waitForTimeout(250);
        await page.keyboard.press("Escape");
        return "AlertDialog xác nhận mở rồi đóng bằng Esc";
      });
    }

    await runSafe("admin-mobile-sheet-390", "static", async () => {
      const mobilePage = await ctx.newPage();
      try {
        await mobilePage.setViewportSize(MOBILE_VP);
        await gotoSafe(mobilePage, `${rootUrl}/admin`);
        await mobilePage.waitForTimeout(450);
        const menuBtn = mobilePage
          .getByRole("button", { name: /Mở hoặc thu gọn menu điều hướng/i })
          .first();
        if (!(await menuBtn.isVisible().catch(() => false))) {
          return { skip: "Không thấy nút mở menu mobile 390 ở quản trị" };
        }
        await menuBtn.click();
        const sheet = mobilePage.getByRole("dialog").first();
        await sheet.waitFor({ state: "visible", timeout: 8_000 });
        await mobilePage.waitForTimeout(450);
        await snap(mobilePage, "admin-mobile-sheet-390.png");
        return "Sheet menu 390 quản trị";
      } finally {
        await mobilePage.close().catch(() => {});
      }
    });

    await runSafe("focus-tab3-admin-1440", "static", async () => {
      return captureTabFocus3(
        page,
        `${rootUrl}/admin`,
        "focus-tab3-admin-1440.png",
      );
    });
  } finally {
    await ctx.close().catch(() => {});
  }
}

async function runSingleSiteStates(browser, portNum, site) {
  const shotNames = [
    `site-${site}-search-dialog-1440`,
    `site-${site}-mobile-sheet-390`,
    `site-${site}-cart-sheet-toast-1440`,
    `site-${site}-checkout-location-popover-1440`,
  ];
  if (!shotNames.some(shouldRun)) return;

  const ctx = await browser.newContext({
    viewport: DESKTOP_VP,
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  const entry = siteOrigin(site, portNum);
  let cartMutated = false;

  try {
    await loginSafe(page, `${entry}/`, CREDS.buyer);
    const origin = new URL(page.url()).origin;

    // Ensure starting cart is empty so badge & cart state are deterministic across runs.
    await clearCartViaUi(page, origin);

    await runSafe(`site-${site}-search-dialog-1440`, "static", async () => {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${origin}/`);
      await page.waitForTimeout(450);
      const searchBtn = page
        .getByRole("button", { name: /^Tìm kiếm tại /i })
        .first();
      if (!(await searchBtn.isVisible().catch(() => false))) {
        return { skip: `Không thấy nút tìm kiếm trên header site ${site}` };
      }
      await searchBtn.click();
      const dialog = page.getByRole("dialog").first();
      await dialog.waitFor({ state: "visible", timeout: 10_000 });
      await waitFiniteAnimations(page);
      await page.waitForTimeout(450);
      await syncFocusedInputCaret(dialog, page);
      await snap(page, `site-${site}-search-dialog-1440.png`);
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden", timeout: 5_000 }).catch(() => {});
      return `CommandDialog tìm kiếm ${site}`;
    });

    await runSafe(`site-${site}-mobile-sheet-390`, "static", async () => {
      const mobilePage = await ctx.newPage();
      try {
        await mobilePage.setViewportSize(MOBILE_VP);
        await gotoSafe(mobilePage, `${origin}/`);
        await mobilePage.waitForTimeout(450);
        const menuBtn = mobilePage
          .getByRole("button", { name: /Mở trình đơn cửa hàng/i })
          .first();
        if (!(await menuBtn.isVisible().catch(() => false))) {
          return {
            skip: `Không thấy nút mở trình đơn mobile trên site ${site}`,
          };
        }
        await menuBtn.click();
        const sheet = mobilePage.getByRole("dialog").first();
        await sheet.waitFor({ state: "visible", timeout: 10_000 });
        await mobilePage.waitForTimeout(450);
        await snap(mobilePage, `site-${site}-mobile-sheet-390.png`);
        return `Sheet menu mobile ${site}`;
      } finally {
        await mobilePage.close().catch(() => {});
      }
    });

    const needCartOrCheckout =
      shouldRun(`site-${site}-cart-sheet-toast-1440`) ||
      shouldRun(`site-${site}-checkout-location-popover-1440`);

    if (needCartOrCheckout) {
      await page.setViewportSize(DESKTOP_VP);
      await gotoSafe(page, `${origin}/san-pham`);
      const articles = page.getByRole("main").getByRole("article");
      const total = await articles.count();
      let pickedCard = articles.first();
      for (let i = 0; i < Math.min(total, 10); i++) {
        const card = articles.nth(i);
        const txt = (await card.textContent().catch(() => "")) || "";
        if (!/flash\s*sale/i.test(txt)) {
          pickedCard = card;
          break;
        }
      }
      if (total > 0) {
        await pickedCard.getByRole("link").first().click();
        await page.waitForURL(/\/san-pham\/[^/]+/, { timeout: 20_000 });
        await page
          .waitForLoadState("networkidle", { timeout: 15_000 })
          .catch(() => {});
        await waitFontsAndImages(page);
        await page.waitForTimeout(400);

        // Select an available variant if "Thêm vào giỏ" is disabled
        const addBtn = page
          .getByRole("button", { name: /^Thêm vào giỏ$/i })
          .first();
        if (
          !(await addBtn.isVisible().catch(() => false)) ||
          !(await addBtn.isEnabled().catch(() => false))
        ) {
          const radios = page
            .getByRole("main")
            .getByRole("radio", { checked: false });
          const rCount = await radios.count().catch(() => 0);
          for (let i = 0; i < Math.min(rCount, 10); i++) {
            const r = radios.nth(i);
            if (
              (await r.isVisible().catch(() => false)) &&
              (await r.isEnabled().catch(() => false))
            ) {
              await r.click();
              await page.waitForTimeout(200);
              if (await addBtn.isEnabled().catch(() => false)) break;
            }
          }
        }

        if (await addBtn.isEnabled().catch(() => false)) {
          await addBtn.click();
          cartMutated = true;
          const drawer = await waitAnyVisible(
            [
              page.getByRole("dialog", { name: /Đã thêm vào giỏ/i }),
              page
                .getByRole("dialog")
                .filter({ hasText: /Đã thêm vào giỏ|Xem giỏ hàng/i }),
            ],
            10_000,
          );
          await page.waitForTimeout(500);

          await runSafe(
            `site-${site}-cart-sheet-toast-1440`,
            "static",
            async () => {
              await snap(page, `site-${site}-cart-sheet-toast-1440.png`);
              return `Sheet xác nhận giỏ + toast trên ${site}`;
            },
          );

          await page.keyboard.press("Escape");
          await drawer
            .waitFor({ state: "hidden", timeout: 5_000 })
            .catch(() => {});
        } else {
          await runSafe(
            `site-${site}-cart-sheet-toast-1440`,
            "static",
            async () => ({
              skip: `Không tìm thấy biến thể còn hàng trên PDP của ${site}`,
            }),
          );
        }
      } else {
        await runSafe(
          `site-${site}-cart-sheet-toast-1440`,
          "static",
          async () => ({
            skip: `Site ${site} không có sản phẩm ở /san-pham`,
          }),
        );
      }

      await runSafe(
        `site-${site}-checkout-location-popover-1440`,
        "static",
        async () => {
          if (!cartMutated) {
            return { skip: `Chưa thêm được sản phẩm vào giỏ trên ${site}` };
          }
          await page.setViewportSize(DESKTOP_VP);
          await gotoSafe(page, `${origin}/thanh-toan`);
          await page
            .getByRole("heading", { level: 1, name: /Thanh toán/i })
            .waitFor({ state: "visible", timeout: 15_000 });
          await page
            .waitForLoadState("networkidle", { timeout: 10_000 })
            .catch(() => {});
          await page.waitForTimeout(900);

          // Expand address editor and open new address form to access LocationCombobox Popover
          const editAddrBtn = page
            .getByRole("button", { name: /Sửa địa chỉ nhận hàng/i })
            .first();
          if (await editAddrBtn.isVisible().catch(() => false)) {
            await editAddrBtn.click();
            await page.waitForTimeout(350);
          }
          const addNewBtn = page
            .getByRole("button", { name: /Thêm địa chỉ mới/i })
            .first();
          if (await addNewBtn.isVisible().catch(() => false)) {
            await addNewBtn.click();
            await page.waitForTimeout(450);
          }

          const provinceTrigger = page
            .getByRole("combobox", { name: /Chọn Tỉnh hoặc Thành phố/i })
            .first();
          if (
            !(await provinceTrigger
              .isVisible({ timeout: 20_000 })
              .catch(() => false))
          ) {
            return {
              skip: `Không thấy combobox Tỉnh/Thành phố trên /thanh-toan (${site})`,
            };
          }

          // Wait until provinces are loaded and combobox is enabled
          const deadline = Date.now() + 10_000;
          while (
            Date.now() < deadline &&
            !(await provinceTrigger.isEnabled().catch(() => false))
          ) {
            await page.waitForTimeout(200);
          }
          if (!(await provinceTrigger.isEnabled().catch(() => false))) {
            return { skip: `Combobox Tỉnh/Thành phố bị khóa trên ${site}` };
          }

          await provinceTrigger.scrollIntoViewIfNeeded();
          await page.waitForTimeout(200);
          await provinceTrigger.click();
          const option = page.getByRole("option").first();
          await option.waitFor({ state: "visible", timeout: 8_000 });
          await waitFiniteAnimations(page);
          await page.waitForTimeout(450);
          const popSearch = page
            .getByRole("combobox", { name: /Tìm tỉnh/i })
            .first();
          if (await popSearch.isVisible().catch(() => false)) {
            await popSearch.click();
            await page.waitForTimeout(100);
          }
          await snap(page, `site-${site}-checkout-location-popover-1440.png`);
          await page.keyboard.press("Escape");
          return `Popover chọn tỉnh/phường mở trên ${site}`;
        },
      );
    }
  } finally {
    // Always restore buyer cart to clean empty state (never leave item behind, never place order).
    if (cartMutated) {
      try {
        const origin = new URL(page.url()).origin;
        await clearCartViaUi(page, origin);
      } catch {
        // best-effort cleanup
      }
    }
    await ctx.close().catch(() => {});
  }
}

async function runPlatformAndFocusStates(browser, rootUrl, portNum) {
  const needsPublic = [
    "platform-mobile-sheet-390",
    "platform-login-focus-1440",
    "focus-tab3-site-bach-dang-1440",
    "focus-tab3-login-1440",
  ].some(shouldRun);
  if (!needsPublic) return;

  const ctx = await browser.newContext({
    viewport: DESKTOP_VP,
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  try {
    await runSafe("platform-mobile-sheet-390", "static", async () => {
      const mobilePage = await ctx.newPage();
      try {
        await mobilePage.setViewportSize(MOBILE_VP);
        await gotoSafe(mobilePage, `${rootUrl}/`);
        await mobilePage.waitForTimeout(450);
        const menuBtn = mobilePage
          .getByRole("button", { name: /Mở trình đơn điều hướng/i })
          .first();
        if (!(await menuBtn.isVisible().catch(() => false))) {
          return {
            skip: "Không thấy nút mở trình đơn điều hướng ở header nền tảng 390",
          };
        }
        await menuBtn.click();
        const sheet = mobilePage.getByRole("dialog").first();
        await sheet.waitFor({ state: "visible", timeout: 8_000 });
        await mobilePage.waitForTimeout(450);
        await snap(mobilePage, "platform-mobile-sheet-390.png");
        return "Sheet menu 390 nền tảng";
      } finally {
        await mobilePage.close().catch(() => {});
      }
    });

    await runSafe("platform-login-focus-1440", "static", async () => {
      const loginPage = await ctx.newPage();
      try {
        await loginPage.setViewportSize(DESKTOP_VP);
        await gotoSafe(loginPage, `${rootUrl}/dang-nhap`);
        await loginPage.waitForTimeout(500);
        const emailInput = loginPage.getByLabel(/Email/i).first();
        if (!(await emailInput.isVisible().catch(() => false))) {
          return { skip: "Không thấy ô Email trên /dang-nhap" };
        }
        await emailInput.click();
        await waitFiniteAnimations(loginPage);
        await loginPage.waitForTimeout(250);
        await snap(loginPage, "platform-login-focus-1440.png");
        return "Ô Email trên /dang-nhap đang focus";
      } finally {
        await loginPage.close().catch(() => {});
      }
    });

    await runSafe("focus-tab3-site-bach-dang-1440", "static", async () => {
      const origin = siteOrigin("bach-dang", portNum);
      return captureTabFocus3(
        page,
        `${origin}/`,
        "focus-tab3-site-bach-dang-1440.png",
      );
    });

    await runSafe("focus-tab3-login-1440", "static", async () => {
      return captureTabFocus3(
        page,
        `${rootUrl}/dang-nhap`,
        "focus-tab3-login-1440.png",
      );
    });
  } finally {
    await ctx.close().catch(() => {});
  }
}

async function runMotionStates(browser, rootUrl, portNum) {
  const targets = [
    { key: "home", url: `${rootUrl}/`, label: "trang chủ /" },
    {
      key: "bach-dang",
      url: `${siteOrigin("bach-dang", portNum)}/`,
      label: "site bach-dang /",
    },
    {
      key: "da-nang",
      url: `${siteOrigin("da-nang", portNum)}/`,
      label: "site da-nang /",
    },
  ];

  const anyMotion = targets.some((t) =>
    [1, 2, 3].some((pos) =>
      ["t0000", "t0500", "t1200"].some((ts) =>
        shouldRun(`motion-${t.key}-pos${pos}-${ts}-1440`),
      ),
    ),
  );
  if (!anyMotion) return;

  for (const target of targets) {
    const targetNeeded = [1, 2, 3].some((pos) =>
      ["t0000", "t0500", "t1200"].some((ts) =>
        shouldRun(`motion-${target.key}-pos${pos}-${ts}-1440`),
      ),
    );
    if (!targetNeeded) continue;

    // Fresh context WITHOUT reduce-motion so Reveal transitions genuinely animate!
    const ctx = await browser.newContext({
      viewport: DESKTOP_VP,
      reducedMotion: "no-preference",
    });
    const page = await ctx.newPage();
    try {
      await gotoSafe(page, target.url);
      await page.waitForTimeout(600);

      // Compute 3 well-separated below-the-fold scroll positions containing [data-reveal]
      const scrollPlan = await page.evaluate(() => {
        const vh = window.innerHeight;
        const maxScroll = Math.max(
          0,
          document.documentElement.scrollHeight - vh,
        );
        const nodes = Array.from(document.querySelectorAll("[data-reveal]"))
          .map((el) => {
            const r = el.getBoundingClientRect();
            const absTop = Math.round(r.top + window.scrollY);
            return {
              absTop,
              height: Math.round(r.height),
              width: Math.round(r.width),
              kind: el.getAttribute("data-reveal") || "fade",
            };
          })
          .filter((n) => n.width > 0 && n.height > 0 && n.absTop >= vh - 50)
          .sort((a, b) => a.absTop - b.absTop);

        if (nodes.length === 0) return [];

        // Pick 3 spread-out anchors (earliest, middle, late) spaced >= 550px if possible
        const chosen = [];
        const addIfDistinct = (cand, minGap) => {
          const y = Math.min(maxScroll, Math.max(0, cand.absTop - 320));
          if (chosen.every((c) => Math.abs(c.y - y) >= minGap)) {
            chosen.push({ y, absTop: cand.absTop, kind: cand.kind });
            return true;
          }
          return false;
        };

        const idxCandidates = [
          0,
          Math.floor(nodes.length * 0.45),
          Math.floor(nodes.length * 0.8),
        ];
        for (const idx of idxCandidates) {
          if (nodes[idx]) addIfDistinct(nodes[idx], 550);
        }
        // Fill up to 3 positions with any remaining distinct Reveal anchors
        for (const minGap of [550, 350, 150]) {
          for (const n of nodes) {
            if (chosen.length >= 3) break;
            addIfDistinct(n, minGap);
          }
        }
        return chosen.sort((a, b) => a.y - b.y).slice(0, 3);
      });

      for (let i = 0; i < 3; i++) {
        const pos = i + 1;
        const step = scrollPlan[i];
        const frameNames = [
          { ms: 0, tag: "t0000" },
          { ms: 500, tag: "t0500" },
          { ms: 1200, tag: "t1200" },
        ];
        if (!step) {
          for (const f of frameNames) {
            const fname = `motion-${target.key}-pos${pos}-${f.tag}-1440`;
            if (shouldRun(fname)) {
              recordSkip(
                fname,
                "motion",
                `${target.label} không đủ 3 vùng [data-reveal] tách biệt dưới màn hình đầu`,
              );
            }
          }
          continue;
        }

        const t0 = Date.now();
        await page.evaluate((y) => window.scrollTo(0, y), step.y);

        for (const f of frameNames) {
          const fname = `motion-${target.key}-pos${pos}-${f.tag}-1440`;
          if (!shouldRun(fname)) continue;
          const remaining = f.ms - (Date.now() - t0);
          if (remaining > 0) {
            await page.waitForTimeout(remaining);
          }
          try {
            const elapsed = Date.now() - t0;
            await snap(page, `${fname}.png`);
            recordOk(
              fname,
              "motion",
              `y=${step.y}px reveal=${step.kind} elapsed=${elapsed}ms (khung chuyển động, có thể lệch nhỏ giữa 2 lần chạy)`,
            );
          } catch (err) {
            recordFail(fname, "motion", err);
          }
        }
      }
    } finally {
      await ctx.close().catch(() => {});
    }
  }
}

const tStart = Date.now();
const browser = await launchBrowser();
const rootUrl = rootOrigin(port);

try {
  await runSellerStates(browser, rootUrl);
  await runAdminStates(browser, rootUrl);
  await runSingleSiteStates(browser, port, "bach-dang");
  await runSingleSiteStates(browser, port, "sai-gon");
  await runPlatformAndFocusStates(browser, rootUrl, port);
  await runMotionStates(browser, rootUrl, port);
} finally {
  await browser.close().catch(() => {});
}

const okStatic = records.filter(
  (r) => r.status === "OK" && r.category === "static",
).length;
const okMotion = records.filter(
  (r) => r.status === "OK" && r.category === "motion",
).length;
const skipped = records.filter((r) => r.status === "SKIP").length;
const failed = records.filter((r) => r.status === "FAIL").length;
const elapsedSec = ((Date.now() - tStart) / 1000).toFixed(1);

const reportLines = [
  `# states.mjs report (port=${port})`,
  `time=${elapsedSec}s static_ok=${okStatic} motion_ok=${okMotion} skip=${skipped} fail=${failed} total=${records.length}`,
  "",
  "Ghi chú: các ảnh `motion-*` được chụp không bật reduce-motion ở mốc 0 / 0,5 / 1,2 giây sau khi cuộn tới vùng Reveal nên có thể có sai lệch nhỏ tự nhiên giữa hai lần chạy.",
  "",
  ...records.map(
    (r) =>
      `${r.status.padEnd(4)} [${r.category.padEnd(6)}] ${r.name}${r.note ? ` — ${r.note}` : ""}`,
  ),
  "",
];
await fs.writeFile(
  path.join(OUT_ABS, "report.txt"),
  reportLines.join("\n"),
  "utf8",
);
console.log(
  `\nSUMMARY port=${port} static_ok=${okStatic} motion_ok=${okMotion} skip=${skipped} fail=${failed} time=${elapsedSec}s out=${outDir}`,
);
if (failed > 0) {
  process.exitCode = 1;
}
