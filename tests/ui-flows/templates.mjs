// Full 8-template Site Studio + real-browser COD checkout flow verifier (Spec §15 item 6).
// Uses ONLY dedicated catalog shop (seller-catalog@nexusmart.local / "Kho Đại Lý NexusMart").
// NEVER modifies any of the 4 seed sites (bach-dang, sai-gon, da-nang, ha-noi).
// Locator policy: role + visible Vietnamese text (getByRole, getByLabel, getByText) for all interactions.
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import {
  assert,
  CREDS,
  rootOrigin,
  SEED_SITES,
  waitAnyVisible,
} from "./lib.mjs";

export const CATALOG_SELLER = {
  email: process.env.CATALOG_SELLER_EMAIL || "seller-catalog@nexusmart.local",
  password: process.env.CATALOG_SELLER_PASSWORD || "SellerDemo123!",
};

export const SITE_TEMPLATES = [
  { id: "minimal-fashion", name: "Thời trang tối giản", expectedMode: "light" },
  { id: "editorial", name: "Tạp chí Editorial", expectedMode: "light" },
  { id: "dense-grid", name: "Lưới dày hiệu suất", expectedMode: "light" },
  { id: "craft-atelier", name: "Xưởng thủ công", expectedMode: "light" },
  { id: "sport-active", name: "Thể thao năng động", expectedMode: "dark" },
  { id: "luxury-maison", name: "Cao cấp Maison", expectedMode: "dark" },
  { id: "one-page-launch", name: "Một trang (Landing)", expectedMode: "light" },
  { id: "multi-sku-catalog", name: "Catalog nhiều SKU", expectedMode: "light" },
];

const OUT_DIR = path.resolve(
  process.env.TEMPLATES_OUT_DIR || ".runs/shadcn-redesign/p1-4/s9/templates",
);
const RATE_LIMIT_WAIT_MS = Number(process.env.RATE_LIMIT_WAIT_MS || 61_000);
const ORDER_CODE_RE = /NM-\d{8}-[A-F0-9]{8}/;
const RATE_LIMIT_TEXT_RE =
  /Bạn thao tác (hơi |quá )?nhanh|quá nhiều yêu cầu|Too Many Requests/i;

function relPath(absPath) {
  return path.relative(process.cwd(), absPath).split(path.sep).join("/");
}

function isForbiddenSeedHost(hostname) {
  const lower = String(hostname || "").toLowerCase();
  if (lower.includes("hanoi-knit.verified.test")) return true;
  return SEED_SITES.some(
    (s) => lower === `${s}.nexusmart.localhost` || lower.startsWith(`${s}.`),
  );
}

function createErrorTracker(page) {
  const state = {
    step: "init",
    consoleErrors: [],
    pageErrors: [],
    httpErrors: [],
    rateLimitEvents: [],
    last429At: 0,
  };

  const onConsole = (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    state.consoleErrors.push({
      step: state.step,
      text: text.slice(0, 400),
      ts: Date.now(),
    });
  };

  const onPageError = (err) => {
    state.pageErrors.push({
      step: state.step,
      text: String(err?.message || err).slice(0, 400),
      ts: Date.now(),
    });
  };

  const onResponse = (res) => {
    const status = res.status();
    const url = res.url();
    if (status === 429) {
      state.last429At = Date.now();
      state.rateLimitEvents.push({ step: state.step, url, ts: Date.now() });
    } else if (status >= 500) {
      state.httpErrors.push({ step: state.step, status, url, ts: Date.now() });
    }
  };

  page.on("console", onConsole);
  page.on("pageerror", onPageError);
  page.on("response", onResponse);

  return {
    setStep(step) {
      state.step = step;
    },
    getStep() {
      return state.step;
    },
    sawRecent429(withinMs = 15_000) {
      return Date.now() - state.last429At <= withinMs;
    },
    discardRateLimitNoise(sinceTs) {
      const keep = (item) =>
        item.ts < sinceTs ||
        !/429|Too Many Requests|status of (429|500|502|503)/i.test(item.text);
      state.consoleErrors = state.consoleErrors.filter(keep);
      state.httpErrors = state.httpErrors.filter(
        (item) => item.ts < sinceTs || item.status < 429,
      );
    },
    snapshot() {
      return {
        consoleErrors: [...state.consoleErrors],
        pageErrors: [...state.pageErrors],
        httpErrors: [...state.httpErrors],
        rateLimitCount: state.rateLimitEvents.length,
      };
    },
    detach() {
      page.off("console", onConsole);
      page.off("pageerror", onPageError);
      page.off("response", onResponse);
    },
  };
}

async function waitForRateLimitCooldown(page, reason, tracker, sinceTs) {
  console.log(
    `[rate-limit] ${reason} -> chờ ${Math.round(RATE_LIMIT_WAIT_MS / 1000)}s rồi thử lại...`,
  );
  if (tracker) tracker.discardRateLimitNoise(sinceTs);
  await page.waitForTimeout(RATE_LIMIT_WAIT_MS);
}

async function pageShowsRateLimit(page) {
  const txt = await page
    .evaluate(() => document.body?.innerText?.slice(0, 1500) || "")
    .catch(() => "");
  return RATE_LIMIT_TEXT_RE.test(txt);
}

async function gotoSafe(page, url, tracker = null) {
  let lastErr;
  for (let attempt = 0; attempt < 4; attempt++) {
    const attemptTs = Date.now();
    try {
      const res = await page.goto(url, {
        waitUntil: "domcontentloaded",
        timeout: 60_000,
      });
      const status = res?.status() ?? 0;
      await page
        .waitForLoadState("networkidle", { timeout: 15_000 })
        .catch(() => {});
      await page.waitForTimeout(300);

      const rateLimited =
        status === 429 ||
        (tracker?.sawRecent429(10_000) && status >= 500) ||
        (await pageShowsRateLimit(page));
      if (rateLimited && attempt < 3) {
        await waitForRateLimitCooldown(
          page,
          `GET ${url} (HTTP ${status})`,
          tracker,
          attemptTs,
        );
        continue;
      }
      if ((status === 502 || status === 503 || status === 500) && attempt < 2) {
        await page.waitForTimeout(attempt === 0 ? 5_000 : 12_000);
        continue;
      }
      if (status >= 400 || status === 0) {
        throw new Error(`GET ${url} -> HTTP ${status}`);
      }
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
      await waitForRateLimitCooldown(
        page,
        `login ${creds.email} @ ${page.url()}`,
        null,
        Date.now(),
      );
      continue;
    }
    break;
  }
  throw new Error(
    `Login ${creds.email} @ ${page.url()} -> HTTP ${lastResult?.status}: ${lastResult?.text}`,
  );
}

async function chooseSelectOption(page, selectLoc, index = 1) {
  const tag = await selectLoc.evaluate((el) => el.tagName).catch(() => "");
  if (tag === "SELECT") {
    const count = await selectLoc.getByRole("option").count();
    if (count > index) {
      await selectLoc.selectOption({ index });
      return true;
    }
    return false;
  }
  await selectLoc.click();
  const opt = page.getByRole("option").nth(index);
  if (await opt.isVisible({ timeout: 4_000 }).catch(() => false)) {
    await opt.click();
    return true;
  }
  await page.keyboard.press("Escape");
  return false;
}

async function waitImagesSettled(page) {
  await page
    .evaluate(async () => {
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

async function measurePageLayout(page) {
  return page.evaluate(() => {
    const scrollW = document.documentElement.scrollWidth;
    const innerW = window.innerWidth;
    const themeRoot = document.querySelector("[data-site-archetype]");
    return {
      overflowPx: Math.max(0, Math.round(scrollW - innerW)),
      scrollWidth: Math.round(scrollW),
      innerWidth: Math.round(innerW),
      h1Count: document.querySelectorAll("h1").length,
      archetype: themeRoot?.getAttribute("data-site-archetype") || "",
      themeMode: themeRoot?.getAttribute("data-site-theme-mode") || "",
      url: window.location.href,
    };
  });
}

async function captureStepViews(page, tplId, stepKey, stepResults) {
  const dir = path.join(OUT_DIR, tplId);
  await fs.mkdir(dir, { recursive: true });

  // 1440px desktop capture
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.waitForTimeout(250);
  await waitImagesSettled(page);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(150);
  const m1440 = await measurePageLayout(page);
  const shot1440 = path.join(dir, `${stepKey}-1440.png`);
  await page.screenshot({ path: shot1440, fullPage: true });

  // 390px mobile capture
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(350);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(150);
  const m390 = await measurePageLayout(page);
  const shot390 = path.join(dir, `${stepKey}-390.png`);
  await page.screenshot({ path: shot390, fullPage: true });

  // Restore 1440px desktop viewport before continuing clicks
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.waitForTimeout(200);

  stepResults[stepKey] = {
    url: m1440.url,
    archetype: m1440.archetype,
    themeMode: m1440.themeMode,
    overflow1440: m1440.overflowPx,
    overflow390: m390.overflowPx,
    h1Count1440: m1440.h1Count,
    h1Count390: m390.h1Count,
    shot1440: relPath(shot1440),
    shot390: relPath(shot390),
  };
  return stepResults[stepKey];
}

async function ensureCatalogSiteStudioReady(sellerPage, rootUrl, setupLog) {
  await gotoSafe(sellerPage, `${rootUrl}/seller/site`);
  await sellerPage
    .getByRole("heading", { level: 1, name: /Site bán hàng/i })
    .waitFor({ state: "visible", timeout: 15_000 });

  // Safety guard: confirm we are inside the catalog shop, never a seed shop.
  const pageText = (await sellerPage.textContent("body")) || "";
  assert(
    !/bach-dang\.nexusmart|sai-gon\.nexusmart|da-nang\.nexusmart|ha-noi\.nexusmart|hanoi-knit\.verified\.test/i.test(
      pageText,
    ),
    "CẢNH BÁO AN TOÀN: Site Studio đang trỏ tới một trong 4 site seed — dừng ngay!",
  );

  // Check suspended state
  const suspendedAlert = sellerPage.getByText(
    /Site riêng đang bị tạm khóa bởi quản trị/i,
  );
  if (await suspendedAlert.isVisible().catch(() => false)) {
    throw new Error(
      "Site của Kho Đại Lý NexusMart đang bị tạm khóa — cần quản trị mở khóa trước khi xuất bản.",
    );
  }

  // If site has not been created yet ("Chưa tạo"), create the initial draft via Site Studio UI.
  const notCreatedBadge = sellerPage
    .getByRole("banner")
    .getByText(/^Chưa tạo$/i)
    .or(sellerPage.getByText(/^Chưa tạo$/i))
    .first();
  if (await notCreatedBadge.isVisible().catch(() => false)) {
    for (let attempt = 0; attempt < 3; attempt++) {
      const saveDraftBtn = sellerPage
        .getByRole("button", { name: /^Lưu nháp$/i })
        .first();
      await saveDraftBtn.waitFor({ state: "visible", timeout: 10_000 });
      const resPromise = sellerPage
        .waitForResponse(
          (r) =>
            r.url().includes("/api/seller/site/draft") &&
            r.request().method() === "PUT",
          { timeout: 20_000 },
        )
        .catch(() => null);
      await saveDraftBtn.click();
      const res = await resPromise;
      if (res?.status() === 429 && attempt < 2) {
        await waitForRateLimitCooldown(
          sellerPage,
          "PUT /api/seller/site/draft (tạo site lần đầu)",
          null,
          Date.now(),
        );
        continue;
      }
      assert(
        res && res.ok(),
        `Không thể lưu nháp khởi tạo site (HTTP ${res?.status() ?? "timeout"})`,
      );
      await sellerPage.waitForTimeout(600);
      setupLog.push(
        "Shop chưa có site (trạng thái 'Chưa tạo'): bấm 'Lưu nháp' trong Site Studio để khởi tạo site nháp với tên miền phụ gợi ý từ tên shop.",
      );
      break;
    }
  }
}

async function applyAndPublishTemplate(
  sellerPage,
  rootUrl,
  tpl,
  setupLog,
  tracker,
) {
  tracker.setStep("studio-apply");
  await gotoSafe(sellerPage, `${rootUrl}/seller/site`, tracker);
  await sellerPage
    .getByRole("heading", { level: 1, name: /Site bán hàng/i })
    .waitFor({ state: "visible", timeout: 15_000 });

  // Switch to "Mẫu" tab if not already active
  const tplTab = sellerPage.getByRole("tab", { name: /^Mẫu/i }).first();
  await tplTab.waitFor({ state: "visible", timeout: 10_000 });
  if ((await tplTab.getAttribute("aria-selected")) !== "true") {
    await tplTab.click();
    await sellerPage.waitForTimeout(300);
  }

  // Step 1: Apply template to draft
  for (let attempt = 0; attempt < 3; attempt++) {
    const attemptTs = Date.now();
    const tabpanel = sellerPage.getByRole("tabpanel").first();
    const headings = tabpanel.getByRole("heading", { level: 2 });
    const hCount = await headings.count();
    let targetIdx = -1;
    for (let i = 0; i < hCount; i++) {
      const txt = ((await headings.nth(i).textContent()) || "").trim();
      if (txt === tpl.name) {
        targetIdx = i;
        break;
      }
    }
    assert(
      targetIdx >= 0,
      `Không tìm thấy thẻ mẫu "${tpl.name}" trong tab Mẫu của Site Studio (đã quét ${hCount} thẻ)`,
    );

    const applyBtns = tabpanel.getByRole("button", {
      name: /^(Dùng mẫu này|Áp dụng lại mẫu gốc)$/i,
    });
    const btnCount = await applyBtns.count();
    assert(
      btnCount > targetIdx,
      `Số nút áp dụng mẫu (${btnCount}) không khớp vị trí thẻ "${tpl.name}" (${targetIdx})`,
    );
    await applyBtns.nth(targetIdx).click();

    const alertDialog = sellerPage.getByRole("alertdialog");
    await alertDialog.waitFor({ state: "visible", timeout: 10_000 });
    const confirmBtn = alertDialog.getByRole("button", {
      name: /Xác nhận áp dụng nháp/i,
    });

    const applyResPromise = sellerPage
      .waitForResponse(
        (r) =>
          r.url().includes("/api/seller/site/apply-template") &&
          r.request().method() === "POST",
        { timeout: 25_000 },
      )
      .catch(() => null);
    await confirmBtn.click();
    const applyRes = await applyResPromise;

    if (applyRes?.status() === 429 && attempt < 2) {
      await alertDialog
        .waitFor({ state: "hidden", timeout: 10_000 })
        .catch(() => {});
      await waitForRateLimitCooldown(
        sellerPage,
        `apply-template ${tpl.id}`,
        tracker,
        attemptTs,
      );
      continue;
    }

    assert(
      applyRes && applyRes.ok(),
      `Áp dụng mẫu "${tpl.name}" thất bại (HTTP ${applyRes?.status() ?? "timeout"})`,
    );
    await alertDialog.waitFor({ state: "hidden", timeout: 15_000 });
    await sellerPage
      .getByText(`Mẫu: ${tpl.name}`, { exact: false })
      .first()
      .waitFor({ state: "visible", timeout: 10_000 });
    await sellerPage.waitForTimeout(400);
    break;
  }

  // Step 2: Publish draft
  tracker.setStep("studio-publish");
  for (let attempt = 0; attempt < 3; attempt++) {
    const attemptTs = Date.now();
    const openPublishBtn = sellerPage
      .getByRole("button", { name: /^Xuất bản$/i })
      .first();
    await openPublishBtn.waitFor({ state: "visible", timeout: 10_000 });
    assert(
      await openPublishBtn.isEnabled(),
      `Nút Xuất bản bị khóa khi xuất bản mẫu "${tpl.name}"`,
    );
    await openPublishBtn.click();

    const publishDialog = sellerPage.getByRole("dialog", {
      name: /Xuất bản site bán hàng/i,
    });
    await publishDialog.waitFor({ state: "visible", timeout: 10_000 });

    const noteInput = publishDialog
      .getByLabel(/Ghi chú phiên bản xuất bản/i)
      .first();
    if (await noteInput.isVisible().catch(() => false)) {
      await noteInput.fill(`QA-S9 kiểm mẫu ${tpl.id}`);
    }

    const confirmPublishBtn = publishDialog
      .getByRole("button", { name: /Xác nhận xuất bản|Lưu nháp & Xuất bản/i })
      .first();
    const pubResPromise = sellerPage
      .waitForResponse(
        (r) =>
          r.url().includes("/api/seller/site/publish") &&
          r.request().method() === "POST",
        { timeout: 25_000 },
      )
      .catch(() => null);
    await confirmPublishBtn.click();
    const pubRes = await pubResPromise;

    if (pubRes?.status() === 429 && attempt < 2) {
      const closeBtn = publishDialog
        .getByRole("button", { name: /^Đóng$/i })
        .first();
      if (await closeBtn.isVisible().catch(() => false)) {
        await closeBtn.click();
      }
      await publishDialog
        .waitFor({ state: "hidden", timeout: 10_000 })
        .catch(() => {});
      await waitForRateLimitCooldown(
        sellerPage,
        `publish ${tpl.id}`,
        tracker,
        attemptTs,
      );
      continue;
    }

    assert(
      pubRes && pubRes.ok(),
      `Xuất bản mẫu "${tpl.name}" thất bại (HTTP ${pubRes?.status() ?? "timeout"})`,
    );
    await publishDialog.waitFor({ state: "hidden", timeout: 15_000 });
    await sellerPage.waitForTimeout(500);
    break;
  }

  // Extract live public storefront URL from "Mở site" link
  const openSiteLink = sellerPage
    .getByRole("link", { name: /Mở site/i })
    .first();
  await openSiteLink.waitFor({ state: "visible", timeout: 10_000 });
  const publicHref = (await openSiteLink.getAttribute("href")) || "";
  assert(
    publicHref.startsWith("http"),
    `Liên kết "Mở site" không hợp lệ: "${publicHref}"`,
  );
  const parsed = new URL(publicHref);
  assert(
    !isForbiddenSeedHost(parsed.hostname),
    `CẢNH BÁO AN TOÀN: Liên kết xuất bản trỏ tới site seed (${parsed.hostname})!`,
  );

  // Record first publish host once in setupLog
  const hostNote = `Site công khai của Kho Đại Lý NexusMart: ${parsed.origin}`;
  if (!setupLog.includes(hostNote)) {
    setupLog.push(hostNote);
  }
  return parsed.origin;
}

async function clearCartViaUi(page, siteBaseUrl, tracker) {
  tracker.setStep("cart-clear");
  await gotoSafe(page, `${siteBaseUrl}/gio-hang`, tracker);
  const clearAll = page
    .getByRole("button", {
      name: /Xóa giỏ hàng|Xoá giỏ hàng|Xóa tất cả|Xoá tất cả/i,
    })
    .first();
  if (await clearAll.isVisible().catch(() => false)) {
    await clearAll.click();
    await page.waitForTimeout(600);
    return;
  }
  for (let i = 0; i < 6; i++) {
    const rmBtn = page.getByRole("button", { name: /^Xóa$|^Xoá$/i }).first();
    if (!(await rmBtn.isVisible().catch(() => false))) break;
    await rmBtn.click();
    await page.waitForTimeout(500);
  }
}

async function navigateFromHomeToPdp(page, siteBaseUrl, tracker) {
  const main = page.getByRole("main");

  // 1. Try product cards (<article>) on homepage, skipping Flash Sale items.
  const articles = main.getByRole("article");
  const artCount = await articles.count().catch(() => 0);
  for (let i = 0; i < Math.min(artCount, 8); i++) {
    const card = articles.nth(i);
    const txt = (await card.textContent().catch(() => "")) || "";
    if (/flash\s*sale/i.test(txt)) continue;
    const link = card.getByRole("link").first();
    if (await link.isVisible().catch(() => false)) {
      const href = (await link.getAttribute("href").catch(() => "")) || "";
      if (/^\/san-pham\/[^/]+/.test(href)) {
        await link.click();
        await page.waitForURL(/\/san-pham\/[^/]+/, { timeout: 20_000 });
        await page
          .waitForLoadState("networkidle", { timeout: 15_000 })
          .catch(() => {});
        await page.waitForTimeout(300);
        return;
      }
    }
  }

  // 2. Try direct product links anywhere in main (covers MultiSkuDirectory & OnePageLaunchHero).
  const allLinks = main.getByRole("link");
  const linkCount = await allLinks.count().catch(() => 0);
  for (let i = 0; i < Math.min(linkCount, 24); i++) {
    const link = allLinks.nth(i);
    if (!(await link.isVisible().catch(() => false))) continue;
    const href = (await link.getAttribute("href").catch(() => "")) || "";
    const txt = (await link.textContent().catch(() => "")) || "";
    if (/^\/san-pham\/[^/?#]+/.test(href) && !/flash\s*sale/i.test(txt)) {
      await link.click();
      await page.waitForURL(/\/san-pham\/[^/]+/, { timeout: 20_000 });
      await page
        .waitForLoadState("networkidle", { timeout: 15_000 })
        .catch(() => {});
      await page.waitForTimeout(300);
      return;
    }
  }

  // 3. Fallback: open catalog /san-pham and pick the first non-flash-sale product.
  await gotoSafe(page, `${siteBaseUrl}/san-pham`, tracker);
  const catalogArticles = page.getByRole("main").getByRole("article");
  const total = await catalogArticles.count();
  assert(total > 0, "Danh mục /san-pham không có sản phẩm nào");
  let chosen = catalogArticles.first();
  for (let i = 0; i < Math.min(total, 10); i++) {
    const card = catalogArticles.nth(i);
    const txt = (await card.textContent().catch(() => "")) || "";
    if (!/flash\s*sale/i.test(txt)) {
      chosen = card;
      break;
    }
  }
  await chosen.getByRole("link").first().click();
  await page.waitForURL(/\/san-pham\/[^/]+/, { timeout: 20_000 });
  await page
    .waitForLoadState("networkidle", { timeout: 15_000 })
    .catch(() => {});
  await page.waitForTimeout(300);
}

async function addProductOnPdpAndOpenCart(page, siteBaseUrl, tracker) {
  const main = page.getByRole("main");
  const addBtn = page.getByRole("button", { name: /^Thêm vào giỏ$/i }).first();

  // If current variant is unavailable/disabled, pick an enabled variant radio/toggle.
  const canAddNow =
    (await addBtn.isVisible().catch(() => false)) &&
    (await addBtn.isEnabled().catch(() => false));
  if (!canAddNow) {
    const variantGroups = [
      main.getByRole("radio", { checked: false }),
      main.getByRole("button", { pressed: false }),
    ];
    for (const grp of variantGroups) {
      const cnt = await grp.count().catch(() => 0);
      for (let i = 0; i < Math.min(cnt, 10); i++) {
        const b = grp.nth(i);
        if (
          (await b.isVisible().catch(() => false)) &&
          (await b.isEnabled().catch(() => false))
        ) {
          const label = ((await b.textContent().catch(() => "")) || "").trim();
          if (
            label &&
            label.length <= 24 &&
            !/Xem ảnh|Thêm vào giỏ|Mua ngay/i.test(label)
          ) {
            await b.click();
            await page.waitForTimeout(200);
            if (
              (await addBtn.isVisible().catch(() => false)) &&
              (await addBtn.isEnabled().catch(() => false))
            ) {
              break;
            }
          }
        }
      }
      if (await addBtn.isEnabled().catch(() => false)) break;
    }
  }

  await addBtn.waitFor({ state: "visible", timeout: 10_000 });
  assert(
    await addBtn.isEnabled(),
    "Nút 'Thêm vào giỏ' bị vô hiệu hóa trên PDP",
  );

  let drawer = null;
  for (let attempt = 0; attempt < 3; attempt++) {
    const attemptTs = Date.now();
    await addBtn.click();
    try {
      drawer = await waitAnyVisible(
        [
          page.getByRole("dialog", { name: /Đã thêm vào giỏ/i }),
          page
            .getByRole("dialog")
            .filter({ hasText: /Đã thêm vào giỏ|Xem giỏ hàng|Mở giỏ hàng/i }),
        ],
        10_000,
      );
      break;
    } catch (err) {
      const rl =
        tracker.sawRecent429(12_000) || (await pageShowsRateLimit(page));
      if (rl && attempt < 2) {
        await waitForRateLimitCooldown(
          page,
          "Thêm vào giỏ trên PDP",
          tracker,
          attemptTs,
        );
        continue;
      }
      throw err;
    }
  }
  assert(drawer, "Không mở được drawer xác nhận sau khi bấm 'Thêm vào giỏ'");

  const viewCartLink = drawer
    .getByRole("link", { name: /Xem giỏ hàng|Mở giỏ hàng/i })
    .or(drawer.getByRole("button", { name: /Xem giỏ hàng|Mở giỏ hàng/i }))
    .first();
  await viewCartLink.waitFor({ state: "visible", timeout: 6_000 });
  await viewCartLink.click();
  await page.waitForURL(/\/gio-hang/, { timeout: 20_000 });
  await page
    .waitForLoadState("networkidle", { timeout: 15_000 })
    .catch(() => {});
  await page.waitForTimeout(350);
  if (await pageShowsRateLimit(page)) {
    await waitForRateLimitCooldown(page, "GET /gio-hang", tracker, Date.now());
    await gotoSafe(page, `${siteBaseUrl}/gio-hang`, tracker);
  }
}

async function completeCodCheckout(
  page,
  siteBaseUrl,
  tplId,
  stepResults,
  tracker,
) {
  tracker.setStep("checkout");
  // Click checkout button from cart if visible; fallback to direct navigation
  const toCheckout = page
    .getByRole("link", { name: /Tiến hành thanh toán|Thanh toán/i })
    .or(
      page.getByRole("button", {
        name: /Tiến hành thanh toán|Thanh toán/i,
      }),
    )
    .first();
  if (await toCheckout.isVisible().catch(() => false)) {
    await toCheckout.click();
    await page
      .waitForURL(/\/thanh-toan/, { timeout: 15_000 })
      .catch(async () => {
        await gotoSafe(page, `${siteBaseUrl}/thanh-toan`, tracker);
      });
    await page
      .waitForLoadState("networkidle", { timeout: 15_000 })
      .catch(() => {});
    await page.waitForTimeout(350);
    if (await pageShowsRateLimit(page)) {
      await waitForRateLimitCooldown(
        page,
        "GET /thanh-toan",
        tracker,
        Date.now(),
      );
      await gotoSafe(page, `${siteBaseUrl}/thanh-toan`, tracker);
    }
  } else {
    await gotoSafe(page, `${siteBaseUrl}/thanh-toan`, tracker);
  }

  await page
    .getByRole("heading", { level: 1, name: /Thanh toán/i })
    .waitFor({ state: "visible", timeout: 15_000 });

  // Ensure a real 2-level address (province/ward) is selected or created
  const editAddrBtn = page
    .getByRole("button", { name: /Sửa địa chỉ nhận hàng/i })
    .first();
  const addrGroup = page
    .getByRole("radiogroup", { name: /Chọn địa chỉ nhận hàng/i })
    .first();
  const hasExistingAddr =
    (await editAddrBtn.isVisible().catch(() => false)) ||
    (await addrGroup.isVisible().catch(() => false));

  if (!hasExistingAddr) {
    const addAddrBtn = page
      .getByRole("button", { name: /Thêm địa chỉ mới/i })
      .first();
    if (await addAddrBtn.isVisible().catch(() => false)) {
      await addAddrBtn.click();
    }
    await page
      .getByLabel(/Họ tên|Người nhận/i)
      .first()
      .fill("QA-S9 Nguyễn Minh Quân");
    await page
      .getByLabel(/Số điện thoại/i)
      .first()
      .fill(CREDS.buyer.phone);
    const prov = page
      .getByLabel(/Chọn Tỉnh hoặc Thành phố|Tỉnh \/ Thành phố/i)
      .first();
    await chooseSelectOption(page, prov, 1);
    await page.waitForTimeout(600);
    const ward = page.getByLabel(/Chọn Phường hoặc Xã|Phường \/ Xã/i).first();
    await chooseSelectOption(page, ward, 1);
    await page
      .getByLabel(/Địa chỉ chi tiết|Số nhà|Đường/i)
      .first()
      .fill("QA-S9 123 Lê Lợi");
    const saveAddrBtn = page
      .getByRole("button", { name: /Lưu địa chỉ|Sử dụng địa chỉ/i })
      .first();
    if (await saveAddrBtn.isVisible().catch(() => false)) {
      await saveAddrBtn.click();
      await page.waitForTimeout(700);
    }
  }

  // Open payment stage if collapsed and select COD
  const editPayBtn = page
    .getByRole("button", { name: /Sửa phương thức thanh toán/i })
    .first();
  if (await editPayBtn.isVisible().catch(() => false)) {
    await editPayBtn.click();
    await page.waitForTimeout(300);
  }
  const codRadio = page
    .getByRole("radio", { name: /Thanh toán khi nhận hàng|COD/i })
    .first();
  await codRadio.waitFor({ state: "visible", timeout: 10_000 });
  await codRadio.click();
  await page.waitForTimeout(600);

  // Capture checkout page screenshots (1440 + 390) with COD selected
  await captureStepViews(page, tplId, "checkout", stepResults);

  // Submit COD order
  const placeOrderBtn = page
    .getByRole("button", { name: /^Đặt hàng/i })
    .first();
  await placeOrderBtn.waitFor({ state: "visible", timeout: 10_000 });
  assert(
    await placeOrderBtn.isEnabled(),
    `Nút 'Đặt hàng' bị khóa trên trang thanh toán của mẫu ${tplId}`,
  );

  for (let attempt = 0; attempt < 3; attempt++) {
    const attemptTs = Date.now();
    await placeOrderBtn.click();
    try {
      await page.waitForURL(/\/don-hang/, { timeout: 25_000 });
      break;
    } catch (err) {
      const alertTxt =
        (await page
          .getByRole("alert")
          .first()
          .textContent()
          .catch(() => "")) || "";
      const rl =
        tracker.sawRecent429(15_000) ||
        RATE_LIMIT_TEXT_RE.test(alertTxt) ||
        /tạm bận|thử lại/i.test(alertTxt);
      if (rl && attempt < 2) {
        await waitForRateLimitCooldown(
          page,
          `Đặt hàng COD (${tplId}): ${alertTxt.trim()}`,
          tracker,
          attemptTs,
        );
        continue;
      }
      throw new Error(
        alertTxt
          ? `Đặt hàng COD báo lỗi: ${alertTxt.trim()}`
          : err?.message || String(err),
      );
    }
  }

  tracker.setStep("order-confirm");
  await page
    .waitForLoadState("networkidle", { timeout: 15_000 })
    .catch(() => {});
  await page.waitForTimeout(450);

  if (await pageShowsRateLimit(page)) {
    const curUrl = page.url();
    await waitForRateLimitCooldown(
      page,
      `Trang xác nhận đơn ${curUrl}`,
      tracker,
      Date.now(),
    );
    await gotoSafe(page, curUrl, tracker);
  }

  const mainTxt = (await page.getByRole("main").textContent()) || "";
  const match = mainTxt.match(ORDER_CODE_RE);
  assert(
    match,
    `Trang xác nhận đơn (${page.url()}) không thấy mã đơn NM- cho mẫu ${tplId}`,
  );
  const orderCode = match[0];

  await captureStepViews(page, tplId, "order-confirm", stepResults);
  return { orderCode, orderUrl: page.url() };
}

async function persistReport(port, setupLog, rowsById) {
  await fs.mkdir(OUT_DIR, { recursive: true });
  const jsonPath = path.join(OUT_DIR, "results.json");

  // Merge with any prior partial run so --only keeps other templates' rows intact.
  let existing = {};
  try {
    const raw = await fs.readFile(jsonPath, "utf8");
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed?.templates)) {
      for (const item of parsed.templates) {
        if (item?.id) existing[item.id] = item;
      }
    }
  } catch {
    // fresh file
  }

  const merged = SITE_TEMPLATES.map((t) => {
    return (
      rowsById[t.id] ||
      existing[t.id] || {
        id: t.id,
        name: t.name,
        expectedMode: t.expectedMode,
        status: "NOT_RUN",
      }
    );
  });

  const payload = {
    generatedAt: new Date().toISOString(),
    port: Number(port),
    seller: CATALOG_SELLER.email,
    buyer: CREDS.buyer.email,
    setupLog,
    templates: merged,
  };
  await fs.writeFile(jsonPath, JSON.stringify(payload, null, 2) + "\n", "utf8");

  const lines = [
    `# Báo cáo kiểm thử 8 mẫu site — S9c (port ${port})`,
    "",
    `- Thời điểm: ${payload.generatedAt}`,
    `- Shop thử nghiệm: Kho Đại Lý NexusMart (\`${CATALOG_SELLER.email}\`)`,
    `- Tài khoản mua COD: \`${CREDS.buyer.email}\``,
    "",
    "## Các bước chuẩn bị",
    ...(setupLog.length > 0
      ? setupLog.map((s) => `- ${s}`)
      : ["- Site của shop catalog đã sẵn sàng."]),
    "",
    "## Bảng tổng hợp 8 mẫu",
    "",
    "| # | Mẫu (`id`) | Lưu & xuất bản | Mã đơn COD | Lỗi console / pageerror | Tràn ngang max (390 / 1440) | Thư mục ảnh |",
    "|---|---|---|---|---|---|---|",
  ];

  const realBugs = [];

  merged.forEach((row, idx) => {
    const steps = row.steps || {};
    const stepList = Object.values(steps);
    const max390 = stepList.reduce(
      (m, s) => Math.max(m, s.overflow390 ?? 0),
      0,
    );
    const max1440 = stepList.reduce(
      (m, s) => Math.max(m, s.overflow1440 ?? 0),
      0,
    );
    const cErr = row.consoleErrors?.length ?? 0;
    const pErr = row.pageErrors?.length ?? 0;
    const savedCol =
      row.status === "NOT_RUN"
        ? "Chưa chạy"
        : row.savedAndPublished
          ? "Có"
          : "Không";
    const orderCol = row.orderCode ? `\`${row.orderCode}\`` : "—";
    const shotDir = relPath(path.join(OUT_DIR, row.id));
    lines.push(
      `| ${idx + 1} | ${row.name} (\`${row.id}\`) | ${savedCol} | ${orderCol} | ${cErr} / ${pErr} | ${max390}px / ${max1440}px | \`${shotDir}/\` |`,
    );

    if (row.issues && row.issues.length > 0) {
      for (const issue of row.issues) {
        realBugs.push(
          `- LỖI THẬT: ${row.id} (${row.name}) — ${issue.step} — ${issue.message}`,
        );
      }
    }
  });

  lines.push("", "## Chi tiết từng bước & tràn ngang");
  for (const row of merged) {
    if (row.status === "NOT_RUN") continue;
    lines.push(
      "",
      `### ${row.name} (\`${row.id}\`) — ${row.status}`,
      `- Archetype thực tế: \`${row.actualArchetype || "—"}\`, theme mode: \`${row.actualThemeMode || "—"}\` (kỳ vọng \`${row.expectedMode}\`)`,
      `- Mã đơn COD: ${row.orderCode ? `\`${row.orderCode}\` (${row.orderUrl})` : "không tạo được"}`,
      `- Số lần chờ rate-limit 429: ${row.rateLimitCount ?? 0}`,
    );
    for (const [stepKey, st] of Object.entries(row.steps || {})) {
      lines.push(
        `  - \`${stepKey}\`: overflow 390=${st.overflow390}px, 1440=${st.overflow1440}px, h1=${st.h1Count390}/${st.h1Count1440} — \`${st.shot390}\`, \`${st.shot1440}\``,
      );
    }
    if ((row.consoleErrors?.length ?? 0) > 0) {
      lines.push("  - Console errors:");
      for (const e of row.consoleErrors) {
        lines.push(`    - [${e.step}] ${e.text}`);
      }
    }
    if ((row.pageErrors?.length ?? 0) > 0) {
      lines.push("  - Page errors:");
      for (const e of row.pageErrors) {
        lines.push(`    - [${e.step}] ${e.text}`);
      }
    }
  }

  lines.push("", "## Ghi nhận lỗi (nếu có)");
  if (realBugs.length === 0) {
    lines.push(
      "- Không phát hiện lỗi chức năng, lỗi console/pageerror hay tràn ngang.",
    );
  } else {
    lines.push(...realBugs);
  }

  const mdPath = path.join(OUT_DIR, "report.md");
  await fs.writeFile(mdPath, lines.join("\n") + "\n", "utf8");
}

export async function buildTemplatesFlows({ browser, port }) {
  const rootUrl = rootOrigin(port);
  const sellerCtx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const buyerCtx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const sellerPage = await sellerCtx.newPage();
  const buyerPage = await buyerCtx.newPage();

  const setupLog = [];
  const rowsById = {};
  let buyerLoggedInOrigin = "";

  // Login catalog seller once
  await loginSafe(sellerPage, `${rootUrl}/`, CATALOG_SELLER);
  await ensureCatalogSiteStudioReady(sellerPage, rootUrl, setupLog);

  const flows = SITE_TEMPLATES.map((tpl) => ({
    name: `templates:${tpl.id}`,
    fn: async () => {
      const sellerTracker = createErrorTracker(sellerPage);
      const buyerTracker = createErrorTracker(buyerPage);
      const stepResults = {};
      const issues = [];
      let savedAndPublished = false;
      let orderCode = "";
      let orderUrl = "";
      let actualArchetype = "";
      let actualThemeMode = "";

      try {
        // 1. Apply template to draft & Publish via Site Studio UI
        const liveOrigin = await applyAndPublishTemplate(
          sellerPage,
          rootUrl,
          tpl,
          setupLog,
          sellerTracker,
        );
        savedAndPublished = true;

        // 2. Login buyer on the catalog storefront origin once
        if (buyerLoggedInOrigin !== liveOrigin) {
          buyerTracker.setStep("buyer-login");
          await loginSafe(buyerPage, `${liveOrigin}/`, CREDS.buyer);
          buyerLoggedInOrigin = liveOrigin;
        }

        // Clean cart before measuring the template flow
        await clearCartViaUi(buyerPage, liveOrigin, buyerTracker);

        // 3. Storefront Home (verify newly published archetype is active, allowing brief cache revalidation)
        buyerTracker.setStep("home");
        await gotoSafe(buyerPage, `${liveOrigin}/`, buyerTracker);
        let homeProbe = await measurePageLayout(buyerPage);
        for (let r = 0; r < 3 && homeProbe.archetype !== tpl.id; r++) {
          await buyerPage.waitForTimeout(2_000);
          await gotoSafe(buyerPage, `${liveOrigin}/`, buyerTracker);
          homeProbe = await measurePageLayout(buyerPage);
        }
        actualArchetype = homeProbe.archetype;
        actualThemeMode = homeProbe.themeMode;

        if (actualArchetype !== tpl.id) {
          issues.push({
            step: "home",
            message: `Archetype hiển thị là "${actualArchetype}", kỳ vọng "${tpl.id}" sau khi Xuất bản.`,
          });
        }
        if (actualThemeMode !== tpl.expectedMode) {
          issues.push({
            step: "home",
            message: `Theme mode hiển thị là "${actualThemeMode}", kỳ vọng "${tpl.expectedMode}".`,
          });
        }
        const homeMetrics = await captureStepViews(
          buyerPage,
          tpl.id,
          "home",
          stepResults,
        );
        if (homeMetrics.h1Count1440 !== 1) {
          issues.push({
            step: "home",
            message: `Trang chủ có ${homeMetrics.h1Count1440} thẻ h1 (kỳ vọng 1) — ảnh ${homeMetrics.shot1440}`,
          });
        }

        // 4. Navigate to PDP -> capture screenshots -> Add to cart -> confirm drawer -> Cart
        buyerTracker.setStep("pdp");
        await navigateFromHomeToPdp(buyerPage, liveOrigin, buyerTracker);
        if (await pageShowsRateLimit(buyerPage)) {
          const pdpUrl = buyerPage.url();
          await waitForRateLimitCooldown(
            buyerPage,
            `GET ${pdpUrl}`,
            buyerTracker,
            Date.now(),
          );
          await gotoSafe(buyerPage, pdpUrl, buyerTracker);
        }
        await buyerPage
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible", timeout: 15_000 });
        await captureStepViews(buyerPage, tpl.id, "pdp", stepResults);
        await addProductOnPdpAndOpenCart(buyerPage, liveOrigin, buyerTracker);

        // 5. Cart page -> verify item & capture screenshots
        buyerTracker.setStep("cart");
        await buyerPage
          .getByRole("heading", { level: 1, name: /Giỏ hàng/i })
          .waitFor({ state: "visible", timeout: 15_000 });
        const emptyCartVisible = await buyerPage
          .getByText(/Giỏ hàng đang trống/i)
          .first()
          .isVisible()
          .catch(() => false);
        assert(
          !emptyCartVisible,
          `Giỏ hàng trống sau khi bấm Thêm vào giỏ trên mẫu ${tpl.id}`,
        );
        await captureStepViews(buyerPage, tpl.id, "cart", stepResults);

        // 6. Checkout COD -> capture screenshots -> submit -> Order confirmation + screenshots
        const orderRes = await completeCodCheckout(
          buyerPage,
          liveOrigin,
          tpl.id,
          stepResults,
          buyerTracker,
        );
        orderCode = orderRes.orderCode;
        orderUrl = orderRes.orderUrl;

        // Check horizontal overflow across all 5 captured pages
        for (const [stepKey, st] of Object.entries(stepResults)) {
          if (st.overflow390 > 1) {
            issues.push({
              step: `${stepKey}@390`,
              message: `Tràn ngang ${st.overflow390}px ở 390px — ảnh ${st.shot390}`,
            });
          }
          if (st.overflow1440 > 1) {
            issues.push({
              step: `${stepKey}@1440`,
              message: `Tràn ngang ${st.overflow1440}px ở 1440px — ảnh ${st.shot1440}`,
            });
          }
        }
      } catch (err) {
        const failedStep = savedAndPublished
          ? buyerTracker.getStep()
          : sellerTracker.getStep();
        issues.push({
          step: failedStep,
          message: (err?.message || String(err)).replace(/\s+/g, " "),
        });
        throw err;
      } finally {
        const sSnap = sellerTracker.snapshot();
        const bSnap = buyerTracker.snapshot();
        sellerTracker.detach();
        buyerTracker.detach();

        const allConsoleErrors = [
          ...sSnap.consoleErrors.map((e) => ({
            ...e,
            step: `seller:${e.step}`,
          })),
          ...bSnap.consoleErrors.map((e) => ({
            ...e,
            step: `buyer:${e.step}`,
          })),
        ];
        const allPageErrors = [
          ...sSnap.pageErrors.map((e) => ({ ...e, step: `seller:${e.step}` })),
          ...bSnap.pageErrors.map((e) => ({ ...e, step: `buyer:${e.step}` })),
        ];

        for (const e of allConsoleErrors) {
          issues.push({
            step: e.step,
            message: `Console error: ${e.text}`,
          });
        }
        for (const e of allPageErrors) {
          issues.push({
            step: e.step,
            message: `Page error: ${e.text}`,
          });
        }

        const hasFailures =
          !savedAndPublished || !orderCode || issues.length > 0;
        rowsById[tpl.id] = {
          id: tpl.id,
          name: tpl.name,
          expectedMode: tpl.expectedMode,
          actualArchetype,
          actualThemeMode,
          savedAndPublished,
          orderCode,
          orderUrl,
          steps: stepResults,
          consoleErrors: allConsoleErrors,
          pageErrors: allPageErrors,
          rateLimitCount: sSnap.rateLimitCount + bSnap.rateLimitCount,
          issues,
          status: hasFailures ? "FAIL" : "PASS",
        };
        await persistReport(port, setupLog, rowsById);
      }

      const row = rowsById[tpl.id];
      if (row.issues.length > 0) {
        throw new Error(
          row.issues.map((i) => `[${i.step}] ${i.message}`).join(" | "),
        );
      }
      return `COD ${orderCode}, 0 lỗi console, 10 ảnh`;
    },
  }));

  return {
    flows,
    cleanup: async () => {
      await buyerCtx.close().catch(() => {});
      await sellerCtx.close().catch(() => {});
    },
  };
}
