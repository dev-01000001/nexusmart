import { expect, type Page } from "@playwright/test";
import { snapshotMailpitIds, waitForOtp } from "./mailpit";

export interface BuyerAccount {
  fullName: string;
  email: string;
  phone: string;
  password: string;
  streetAddress: string;
}

export interface DeliveredSiteOrderFixture {
  orderId: string;
  orderCode: string;
  orderItemId: string;
  productSlug: string;
  productName: string;
  trackingCode: string;
}

export function getGatewayUrl(): string {
  return (process.env.GATEWAY_URL?.trim() || "http://127.0.0.1:28080").replace(/\/+$/, "");
}

function getPaymentWebhookSecret(): string {
  return process.env.PAYMENT_WEBHOOK_SECRET?.trim() || "dev-only-payment-webhook-hmac-secret";
}

function getShippingWebhookSecret(): string {
  return process.env.SHIPPING_WEBHOOK_SECRET?.trim() || "dev-only-shipping-webhook-hmac-secret";
}

function getSellerCredentials(): { email: string; password: string } {
  return {
    email: process.env.SEED_SELLER_EMAIL?.trim() || "seller@nexusmart.local",
    password: process.env.SEED_SELLER_PASSWORD?.trim() || "SellerDemo123!",
  };
}

export function createUniqueBuyer(prefix = "buyer"): BuyerAccount {
  const stamp = Date.now().toString(36);
  const rand = Math.random().toString(36).slice(2, 6);
  const id = `${stamp}${rand}`;
  const digits = String(
    ((Date.now() * 13 + Math.floor(Math.random() * 1_000_000)) % 80_000_000) + 10_000_000
  ).padStart(8, "0");

  return {
    fullName: `Playwright ${prefix} ${id}`,
    email: `pw-${prefix}-${id}@nexusmart.local`,
    phone: `09${digits}`,
    password: `PwE2E!${id.slice(-6)}Aa1`,
    streetAddress: `Số 42 Đường Bạch Đằng ${id.slice(-4)}`,
  };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function hmacSha256Hex(secret: string, message: string): Promise<string> {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey(
    "raw",
    enc.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  );
  const sigBuf = await crypto.subtle.sign("HMAC", key, enc.encode(message));
  return Array.from(new Uint8Array(sigBuf))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

/** Asserts the current page has no unintended horizontal overflow at the active viewport (390 or 1440). */
export async function assertNoHorizontalOverflow(page: Page, label: string): Promise<void> {
  const metrics = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }));
  expect(
    metrics.scrollWidth,
    `${label}: horizontal overflow detected (${metrics.scrollWidth}px > ${metrics.innerWidth}px)`
  ).toBeLessThanOrEqual(metrics.innerWidth + 1);
}

/**
 * Verifies authenticated buyer navigation links work on both desktop (1440) and mobile (390).
 */
export async function assertAuthenticatedSiteNav(page: Page): Promise<void> {
  const width = page.viewportSize()?.width ?? 1440;
  if (width >= 1024) {
    await expect(page.locator('header a[href="/don-hang"]')).toBeVisible();
    await expect(page.locator('header a[href="/tai-khoan"]')).toBeVisible();
    return;
  }
  const menuBtn = page.getByRole("button", { name: "Mở trình đơn cửa hàng" });
  await expect(menuBtn).toBeVisible();
  await menuBtn.click();
  const drawer = page.getByRole("dialog");
  await expect(drawer.locator('a[href="/don-hang"]').first()).toBeVisible();
  await expect(drawer.locator('a[href="/tai-khoan"]').first()).toBeVisible();
  const closeBtn = drawer.getByRole("button", { name: /Đóng/i }).first();
  if (await closeBtn.isVisible().catch(() => false)) {
    await closeBtn.click();
  } else {
    await page.keyboard.press("Escape");
  }
}

/**
 * Registers a fresh buyer via `/dang-ky`, verifies Mailpit email OTP, confirms authenticated nav,
 * and optionally verifies phone OTP on `/tai-khoan#xac-minh-sdt` (required for COD).
 */
export async function registerBuyerViaUi(
  page: Page,
  buyer: BuyerAccount,
  options: { verifyPhone?: boolean } = {}
): Promise<void> {
  await page.goto("/dang-ky");

  await expect(page.locator("#reg-name")).toBeVisible();
  await page.locator("#reg-name").fill(buyer.fullName);
  await page.locator("#reg-email").fill(buyer.email);
  await page.locator("#reg-phone").fill(buyer.phone);
  await page.locator("#reg-pass").fill(buyer.password);
  await page.locator("#reg-confirm").fill(buyer.password);

  await page.getByRole("button", { name: "Tạo tài khoản" }).click();

  const otpInput = page.locator("#otp-code");
  await expect(otpInput).toBeVisible();

  const otpCode = await waitForOtp(buyer.email);
  expect(otpCode).toMatch(/^\d{6}$/);

  await otpInput.fill(otpCode);
  await page.getByRole("button", { name: "Xác thực và hoàn tất" }).click();

  await page.waitForURL((url) => !url.pathname.startsWith("/dang-ky"), {
    timeout: 25_000,
    waitUntil: "commit",
  });

  if (new URL(page.url()).pathname.startsWith("/dang-nhap")) {
    await expect(page.locator("#login-email")).toBeVisible();
    await page.locator("#login-email").fill(buyer.email);
    await page.locator("#login-password").fill(buyer.password);
    await page.getByRole("button", { name: "Đăng nhập" }).click();
    await page.waitForURL((url) => !url.pathname.startsWith("/dang-nhap"), {
      timeout: 25_000,
      waitUntil: "commit",
    });
  }

  await assertAuthenticatedSiteNav(page);

  if (options.verifyPhone) {
    const seenIds = await snapshotMailpitIds(buyer.email);
    await page.goto("/tai-khoan#xac-minh-sdt");
    const phoneSection = page.locator("#xac-minh-sdt");
    await expect(phoneSection).toBeVisible();
    const phoneInput = page.locator("#acc-otp-phone");
    const verifiedBadge = phoneSection.getByText("Đã xác minh", { exact: true });
    await expect(phoneInput.or(verifiedBadge)).toBeVisible({ timeout: 15_000 });
    if (await phoneInput.isVisible()) {
      await phoneInput.fill(buyer.phone);
      await phoneSection.getByRole("button", { name: "Gửi mã OTP" }).click();
      const codeInput = page.locator("#acc-otp-code");
      await expect(codeInput).toBeVisible();
      const phoneOtp = await waitForOtp(buyer.email, { seenIds });
      expect(phoneOtp).toMatch(/^\d{6}$/);
      await codeInput.fill(phoneOtp);
      await phoneSection.getByRole("button", { name: "Xác minh SĐT" }).click();
    }
    await expect(verifiedBadge).toBeVisible();
  }
}

/**
 * Waits for the checkout address stage to finish loading (either the inline new-address form
 * `#addr-recipient` for a buyer with no saved addresses, or the "Thêm địa chỉ mới" button),
 * explicitly selects the first real ward option (since `#addr-ward` defaults to the empty
 * placeholder "Chọn phường/xã"), saves the address, and verifies it is selected.
 */
export async function fillAndSaveCheckoutAddress(page: Page, buyer: BuyerAccount): Promise<void> {
  const recipientInput = page.locator("#addr-recipient");
  const addAddressBtn = page.getByRole("button", { name: "Thêm địa chỉ mới" });

  await expect(recipientInput.or(addAddressBtn)).toBeVisible({ timeout: 15_000 });
  if (!(await recipientInput.isVisible())) {
    await addAddressBtn.click();
    await expect(recipientInput).toBeVisible();
  }

  const provinceSelect = page.locator("#addr-province");
  const wardSelect = page.locator("#addr-ward");

  await expect(provinceSelect).toBeEnabled();
  await expect(provinceSelect).toHaveValue(/.+/);

  await expect(wardSelect).toBeEnabled();
  const firstRealWardOption = wardSelect.locator('option[value]:not([value=""])').first();
  await expect(firstRealWardOption).toBeAttached({ timeout: 15_000 });
  const chosenWard = (await firstRealWardOption.getAttribute("value")) ?? "";
  expect(chosenWard.length, "first non-empty ward option must have a value").toBeGreaterThan(0);
  await wardSelect.selectOption(chosenWard);
  await expect(wardSelect).toHaveValue(chosenWard);

  await recipientInput.fill(buyer.fullName);
  await page.locator("#addr-phone").fill(buyer.phone);
  await page.locator("#addr-street").fill(buyer.streetAddress);

  await page.getByRole("button", { name: "Lưu và giao tới địa chỉ này" }).click();

  const addressStage = page.getByRole("region", { name: "Địa chỉ nhận hàng" });
  await expect(addressStage).toBeVisible({ timeout: 15_000 });
  const addressSummaryBtn = addressStage
    .locator('button[aria-expanded="false"][aria-controls="stage-address-body"]')
    .filter({ hasText: buyer.streetAddress })
    .first();
  await expect(addressSummaryBtn).toBeVisible({ timeout: 15_000 });
  await expect(addressSummaryBtn).toContainText(buyer.fullName);
  await expect(addressSummaryBtn).toContainText(buyer.streetAddress);
  await expect(addressStage.getByRole("radiogroup")).toHaveCount(0);
}

async function gwJson<T>(
  method: string,
  path: string,
  opts: { token?: string; body?: unknown } = {}
): Promise<T> {
  const headers: Record<string, string> = { accept: "application/json" };
  if (opts.token) headers.authorization = `Bearer ${opts.token}`;
  let payload: string | undefined;
  if (opts.body !== undefined) {
    headers["content-type"] = "application/json";
    payload = JSON.stringify(opts.body);
  }
  const res = await fetch(`${getGatewayUrl()}${path}`, {
    method,
    headers,
    body: payload,
    signal: AbortSignal.timeout(20_000),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`Gateway ${method} ${path} failed (${res.status}): ${text.slice(0, 400)}`);
  }
  return (text ? JSON.parse(text) : {}) as T;
}

export async function payVietQrViaGateway(params: {
  paymentId: string;
  checkoutGroupId: string;
  amountVnd: number;
}): Promise<void> {
  const body = JSON.stringify({
    provider: "vietqr_mock",
    provider_tx_id: `TXN-PW-${crypto.randomUUID()}`,
    payment_id: params.paymentId,
    order_group_id: params.checkoutGroupId,
    amount_vnd: params.amountVnd,
    status: "SUCCEEDED",
    reference: `NEXUSMART ${params.checkoutGroupId}`,
    occurred_at: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  });
  const sig = await hmacSha256Hex(getPaymentWebhookSecret(), body);
  const res = await fetch(`${getGatewayUrl()}/webhooks/payment`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-signature": sig,
      "x-timestamp": String(Math.floor(Date.now() / 1000)),
    },
    body,
    signal: AbortSignal.timeout(20_000),
  });
  if (!res.ok) {
    throw new Error(`VietQR webhook failed (${res.status}): ${await res.text()}`);
  }
}

async function postShippingWebhook(
  carrierCode: string,
  payload: Record<string, unknown>
): Promise<void> {
  const body = JSON.stringify(payload);
  const sig = await hmacSha256Hex(getShippingWebhookSecret(), body);
  const res = await fetch(
    `${getGatewayUrl()}/webhooks/shipping/${encodeURIComponent(carrierCode || "mock_express")}`,
    {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-signature": sig,
        "x-timestamp": String(Math.floor(Date.now() / 1000)),
      },
      body,
      signal: AbortSignal.timeout(20_000),
    }
  );
  if (!res.ok) {
    throw new Error(`Shipping webhook (${carrierCode}) failed (${res.status}): ${await res.text()}`);
  }
}

async function loginSeededSellerToken(): Promise<string> {
  const creds = getSellerCredentials();
  const login = await gwJson<{ access_token: string }>("POST", "/api/v1/auth/login", {
    body: { email: creds.email, password: creds.password },
  });
  return login.access_token;
}

export async function shipAndDeliverOrderViaGateway(params: {
  orderId: string;
  carrierCode?: string;
  serviceCode?: string;
}): Promise<{ trackingCode: string }> {
  const sellerToken = await loginSeededSellerToken();

  // Confirm if still CONFIRMED (ignore 409 if already PREPARING).
  await fetch(`${getGatewayUrl()}/api/v1/seller/orders/${encodeURIComponent(params.orderId)}/confirm`, {
    method: "POST",
    headers: { authorization: `Bearer ${sellerToken}`, "content-type": "application/json" },
    body: "{}",
    signal: AbortSignal.timeout(20_000),
  });

  const shipRes = await gwJson<{
    tracking_code?: string;
    order?: { tracking_code?: string; carrier_code?: string };
  }>("POST", `/api/v1/seller/orders/${encodeURIComponent(params.orderId)}/ship`, {
    token: sellerToken,
    body: {
      carrier_code: params.carrierCode || "mock_express",
      service_code: params.serviceCode || "STANDARD",
    },
  });

  let trackingCode = shipRes.tracking_code || shipRes.order?.tracking_code || "";
  const carrier = shipRes.order?.carrier_code || params.carrierCode || "mock_express";

  const deadline = Date.now() + 25_000;
  while (!trackingCode && Date.now() < deadline) {
    await sleep(500);
    const ord = await gwJson<{ order?: { tracking_code?: string } }>(
      "GET",
      `/api/v1/seller/orders/${encodeURIComponent(params.orderId)}`,
      { token: sellerToken }
    );
    trackingCode = ord.order?.tracking_code || "";
  }
  if (!trackingCode) {
    throw new Error(`Timed out waiting for tracking_code on order ${params.orderId}`);
  }

  const now = new Date().toISOString();
  await postShippingWebhook(carrier, {
    event_id: `pw-deliv-${crypto.randomUUID()}`,
    tracking_code: trackingCode,
    order_id: params.orderId,
    raw_status: "delivering",
    description: "Đang giao hàng Playwright E2E",
    occurred_at: now,
  });
  await postShippingWebhook(carrier, {
    event_id: `pw-done-${crypto.randomUUID()}`,
    tracking_code: trackingCode,
    order_id: params.orderId,
    raw_status: "delivered",
    description: "Giao hàng thành công Playwright E2E",
    occurred_at: now,
  });

  return { trackingCode };
}

export async function sellerAcceptReturnViaGateway(orderId: string): Promise<{
  returnId: string;
  returnTrackingCode: string;
}> {
  const sellerToken = await loginSeededSellerToken();
  const list = await gwJson<{
    items?: Array<{ id: string; order_id: string; status: string }>;
  }>("GET", "/api/v1/seller/returns?page=1&page_size=50", { token: sellerToken });

  const target = (list.items ?? []).find((r) => r.order_id === orderId);
  if (!target) {
    throw new Error(`Return request for order ${orderId} not found in seller returns`);
  }

  const resp = await gwJson<{
    return_request?: { id: string; return_tracking_code?: string };
  }>("POST", `/api/v1/seller/returns/${encodeURIComponent(target.id)}/respond`, {
    token: sellerToken,
    body: {
      decision: "ACCEPT_RETURN",
      note: "Playwright E2E gian hàng đồng ý nhận lại sản phẩm",
      evidence_urls: [],
    },
  });

  return {
    returnId: target.id,
    returnTrackingCode: resp.return_request?.return_tracking_code || "",
  };
}

/**
 * Creates a self-contained DELIVERED VietQR order for the logged-in buyer on the current site host,
 * using the browser's authenticated BFF cookies + gateway payment/shipping webhooks.
 */
export async function setupDeliveredSiteOrder(
  page: Page,
  buyer: BuyerAccount
): Promise<DeliveredSiteOrderFixture> {
  const siteOrigin = new URL(page.url()).origin;
  const siteBareHost = new URL(page.url()).hostname;

  const siteRes = await gwJson<{ shop?: { id?: string }; site?: { shop_id?: string } }>(
    "GET",
    `/api/v1/sites/by-host?host=${encodeURIComponent(siteBareHost)}`
  );
  const shopId = siteRes.shop?.id || siteRes.site?.shop_id || "";
  if (!shopId) {
    throw new Error(`Unable to resolve shop_id for host ${siteBareHost}`);
  }

  const prodRes = await gwJson<{
    items?: Array<{
      id: string;
      slug: string;
      name: string;
      shop_id?: string;
      skus?: Array<{ id: string; is_active?: boolean; available_stock: number; price_vnd: number }>;
    }>;
  }>("GET", `/api/v1/products?shop_id=${encodeURIComponent(shopId)}&page=1&page_size=50`);

  let chosenProduct: { id: string; slug: string; name: string } | null = null;
  let chosenSku: { id: string; price_vnd: number } | null = null;
  for (const p of prodRes.items ?? []) {
    if (p.shop_id && p.shop_id !== shopId) continue;
    const sku = (p.skus ?? []).find(
      (k) => k.is_active !== false && k.available_stock >= 3 && k.price_vnd >= 50_000
    );
    if (sku) {
      chosenProduct = { id: p.id, slug: p.slug, name: p.name };
      chosenSku = { id: sku.id, price_vnd: sku.price_vnd };
      break;
    }
  }
  if (!chosenProduct || !chosenSku) {
    throw new Error(`No in-stock SKU available for shop ${shopId}`);
  }

  const locRes = await page.request.get(`${siteOrigin}/api/checkout/locations`);
  expect(locRes.status()).toBe(200);
  const locData = (await locRes.json()) as { provinces: Array<{ code: string; name: string }> };
  const province = locData.provinces.find((p) => p.code === "01") ?? locData.provinces[0];

  const wardRes = await page.request.get(
    `${siteOrigin}/api/checkout/locations?province_code=${encodeURIComponent(province.code)}`
  );
  expect(wardRes.status()).toBe(200);
  const wardData = (await wardRes.json()) as { wards: Array<{ code: string; name: string }> };
  const ward = wardData.wards[0];

  const addrRes = await page.request.post(`${siteOrigin}/api/checkout/addresses`, {
    headers: { origin: siteOrigin, "content-type": "application/json" },
    data: {
      recipient_name: buyer.fullName,
      recipient_phone: buyer.phone,
      province_code: province.code,
      province_name: province.name,
      ward_code: ward.code,
      ward_name: ward.name,
      street_address: buyer.streetAddress,
      is_default: true,
    },
  });
  expect(addrRes.status()).toBe(201);
  const addrData = (await addrRes.json()) as { address: { id: string } };

  const quoteRes = await page.request.post(`${siteOrigin}/api/checkout/shipping-quote`, {
    headers: { origin: siteOrigin, "content-type": "application/json" },
    data: {
      to_province_code: province.code,
      to_ward_code: ward.code,
      shops: [{ shop_id: shopId, weight_grams: 300, cod_amount_vnd: 0 }],
    },
  });
  expect(quoteRes.status()).toBe(200);
  const quoteData = (await quoteRes.json()) as {
    quotes_by_shop: Record<string, Array<{ carrier_code: string; service_code: string }>>;
  };
  const firstQuote = (quoteData.quotes_by_shop[shopId] ?? [])[0];
  expect(firstQuote).toBeTruthy();

  const coRes = await page.request.post(`${siteOrigin}/api/checkout`, {
    headers: {
      origin: siteOrigin,
      "content-type": "application/json",
      "idempotency-key": `pw-setup-${crypto.randomUUID()}`,
    },
    data: {
      address_id: addrData.address.id,
      from_cart: false,
      payment_method: "VIETQR",
      items: [{ sku_id: chosenSku.id, quantity: 1 }],
      shipping_choices: [
        {
          shop_id: shopId,
          carrier_code: firstQuote.carrier_code,
          service_code: firstQuote.service_code,
        },
      ],
    },
  });
  expect(coRes.status()).toBe(201);
  const coData = (await coRes.json()) as {
    payment_id: string;
    checkout_group_id: string;
    total_payable_vnd: number;
    orders: Array<{ id: string; order_code: string; items?: Array<{ id: string }> }>;
  };

  const order = coData.orders[0];
  await payVietQrViaGateway({
    paymentId: coData.payment_id,
    checkoutGroupId: coData.checkout_group_id,
    amountVnd: coData.total_payable_vnd,
  });

  // Poll until order reaches CONFIRMED before shipping.
  const confirmDeadline = Date.now() + 25_000;
  let confirmed = false;
  while (!confirmed && Date.now() < confirmDeadline) {
    const r = await page.request.get(`${siteOrigin}/api/orders/${encodeURIComponent(order.id)}`);
    if (r.status() === 200) {
      const body = (await r.json()) as { order?: { backendStatus?: string } };
      if (body.order?.backendStatus?.toUpperCase() === "CONFIRMED") {
        confirmed = true;
        break;
      }
    }
    await sleep(400);
  }
  expect(confirmed, `order ${order.id} did not reach CONFIRMED`).toBe(true);

  const { trackingCode } = await shipAndDeliverOrderViaGateway({
    orderId: order.id,
    carrierCode: firstQuote.carrier_code,
    serviceCode: firstQuote.service_code,
  });

  // Poll until buyer BFF observes DELIVERED.
  const deliverDeadline = Date.now() + 25_000;
  let delivered = false;
  while (!delivered && Date.now() < deliverDeadline) {
    const r = await page.request.get(`${siteOrigin}/api/orders/${encodeURIComponent(order.id)}`);
    if (r.status() === 200) {
      const body = (await r.json()) as { order?: { backendStatus?: string } };
      if (body.order?.backendStatus?.toUpperCase() === "DELIVERED") {
        delivered = true;
        break;
      }
    }
    await sleep(400);
  }
  expect(delivered, `order ${order.id} did not reach DELIVERED`).toBe(true);

  return {
    orderId: order.id,
    orderCode: order.order_code,
    orderItemId: order.items?.[0]?.id ?? "",
    productSlug: chosenProduct.slug,
    productName: chosenProduct.name,
    trackingCode,
  };
}
