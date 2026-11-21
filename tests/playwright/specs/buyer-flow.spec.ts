import { expect, test } from "@playwright/test";
import { waitForOtp } from "../helpers/mailpit";

interface BuyerAccount {
  fullName: string;
  email: string;
  phone: string;
  password: string;
  streetAddress: string;
}

function createUniqueBuyer(): BuyerAccount {
  const stamp = Date.now().toString(36);
  const rand = Math.random().toString(36).slice(2, 6);
  const id = `${stamp}${rand}`;
  const digits = String((Date.now() * 13 + Math.floor(Math.random() * 1_000_000)) % 80_000_000 + 10_000_000).padStart(
    8,
    "0"
  );

  return {
    fullName: `Playwright Buyer ${id}`,
    email: `pw-buyer-${id}@nexusmart.local`,
    phone: `09${digits}`,
    password: `PwE2E!${id.slice(-6)}Aa1`,
    streetAddress: `Số 42 Đường Bạch Đằng ${id.slice(-4)}`,
  };
}

test.describe("Storefront Buyer E2E Journey", () => {
  test("register with Mailpit OTP, search product, add to cart, checkout COD, and view order", async ({
    page,
  }) => {
    const buyer = createUniqueBuyer();
    let selectedProductName = "";
    let createdOrderId = "";
    let createdOrderCode = "";

    await test.step("1. Register buyer, verify Mailpit OTP, and confirm authenticated session", async () => {
      await page.goto("/dang-ky");

      await expect(page.locator("#reg-name")).toBeVisible();
      await page.locator("#reg-name").fill(buyer.fullName);
      await page.locator("#reg-email").fill(buyer.email);
      await page.locator("#reg-phone").fill(buyer.phone);
      await page.locator("#reg-pass").fill(buyer.password);
      await page.locator("#reg-confirm").fill(buyer.password);

      await page.getByRole("button", { name: "Tạo tài khoản" }).click();

      // RegisterForm switches in-place to the 6-digit OTP verification step.
      const otpInput = page.locator("#otp-code");
      await expect(otpInput).toBeVisible();

      const otpCode = await waitForOtp(buyer.email);
      expect(otpCode).toMatch(/^\d{6}$/);

      await otpInput.fill(otpCode);
      await page.getByRole("button", { name: "Xác thực và hoàn tất" }).click();

      // RegisterForm auto-logs in and redirects to /tai-khoan; if it falls back to /dang-nhap, sign in there.
      await page.waitForURL((url) => !url.pathname.startsWith("/dang-ky"), {
        timeout: 25_000,
        waitUntil: "commit",
      });

      const alreadyAuthed = await page
        .locator('header a[href="/tai-khoan"]')
        .isVisible()
        .catch(() => false);
      if (new URL(page.url()).pathname.startsWith("/dang-nhap") && !alreadyAuthed) {
        await expect(page.locator("#login-email")).toBeVisible();
        await page.locator("#login-email").fill(buyer.email);
        await page.locator("#login-password").fill(buyer.password);
        await page.getByRole("button", { name: "Đăng nhập" }).click();
        await Promise.race([
          page.waitForURL((url) => !url.pathname.startsWith("/dang-nhap"), {
            timeout: 25_000,
            waitUntil: "commit",
          }),
          page.locator('header a[href="/tai-khoan"]').waitFor({ state: "visible", timeout: 25_000 }),
        ]);
      }

      // Authenticated site header renders "Đơn hàng" (/don-hang) and "Tài khoản" (/tai-khoan).
      await expect(page.locator('header a[href="/don-hang"]')).toBeVisible();
      await expect(page.locator('header a[href="/tai-khoan"]')).toBeVisible();
    });

    await test.step("2. Search seeded storefront products on /tim-kiem and open a PDP", async () => {
      await page.goto("/tim-kiem");

      const searchForm = page.getByRole("main").getByRole("search");
      const searchInput = searchForm.getByRole("combobox");
      await expect(searchInput).toBeVisible();
      await searchInput.fill("cotton");
      await searchForm.getByRole("button", { name: "Tìm kiếm", exact: true }).click();

      // SiteSearchView filters in-place (updating heading & results) and/or syncs ?q=cotton.
      await expect(page.locator("h1")).toContainText(/cotton/i);

      const productCards = page.locator('article:has(a[href^="/san-pham/"])');
      await expect(productCards.first()).toBeVisible();

      // Prefer a regular non-flash-sale in-stock product (e.g. "Xám đá") for standard COD checkout.
      const inStockCards = productCards.filter({ hasNotText: "Tạm hết hàng" });
      const nonFlashCards = inStockCards.filter({ hasNotText: /Flash sale/i });
      const preferredNonFlashCards = nonFlashCards.filter({ hasText: /Xám đá/i });

      let chosenCard = productCards.first();
      if ((await preferredNonFlashCards.count()) > 0) {
        chosenCard = preferredNonFlashCards.first();
      } else if ((await nonFlashCards.count()) > 0) {
        chosenCard = nonFlashCards.first();
      } else if ((await inStockCards.count()) > 0) {
        chosenCard = inStockCards.first();
      }

      // Second link inside SiteProductCard is the product title link.
      const productTitleLink = chosenCard.locator('a[href^="/san-pham/"]').last();
      selectedProductName = (await productTitleLink.innerText()).trim();
      expect(selectedProductName.length).toBeGreaterThan(0);

      await productTitleLink.click();
      await expect(page).toHaveURL(/\/san-pham\/.+/);
      await expect(page.locator("h1")).toContainText(selectedProductName);
    });

    await test.step("3. Select an in-stock SKU, add to cart, and verify /gio-hang", async () => {
      // Pick an enabled size button when size options are rendered on SitePdpView.
      const sizeLabel = page.locator("span", { hasText: /^Kích cỡ:/ }).first();
      if (await sizeLabel.isVisible().catch(() => false)) {
        const enabledSizeBtn = sizeLabel.locator("..").locator("button:not([disabled])").first();
        if (await enabledSizeBtn.isVisible().catch(() => false)) {
          await enabledSizeBtn.click();
        }
      }

      await expect(page.getByText(/Còn .+ sản phẩm sẵn giao/)).toBeVisible();

      const addToCartBtn = page.getByRole("button", { name: /Thêm vào giỏ/i });
      await expect(addToCartBtn).toBeEnabled();
      await addToCartBtn.click();

      const openCartLink = page.getByRole("link", { name: "Mở giỏ hàng" });
      await expect(openCartLink).toBeVisible();
      await openCartLink.click();

      await expect(page).toHaveURL(/\/gio-hang\/?$/);
      await expect(page.locator("h1")).toContainText(/Giỏ hàng/i);
      const cartItemLink = page
        .locator('article ul li a[href^="/san-pham/"]')
        .filter({ hasText: selectedProductName })
        .last();
      await expect(cartItemLink).toBeVisible();
      await expect(cartItemLink).toContainText(selectedProductName);

      const summaryAside = page.locator('aside[aria-label="Tóm tắt đơn hàng và mã ưu đãi"]');
      await expect(summaryAside).toBeVisible();
      await expect(summaryAside).toContainText(/Tạm tính/);
      await expect(summaryAside).toContainText(/₫/);
    });

    await test.step("4. Proceed to /thanh-toan, add shipping address, select COD, and submit order", async () => {
      const checkoutLink = page.locator(
        'aside[aria-label="Tóm tắt đơn hàng và mã ưu đãi"] a[href="/thanh-toan"]'
      );
      await expect(checkoutLink).toBeVisible();
      await checkoutLink.click();

      await expect(page).toHaveURL(/\/thanh-toan\/?$/);
      await expect(page.locator("h1")).toContainText(/Thanh toán/i);

      // New buyer has no saved addresses so AddressForm opens automatically; open it if needed.
      const recipientInput = page.locator("#addr-recipient");
      if (!(await recipientInput.isVisible().catch(() => false))) {
        await page.getByRole("button", { name: "Thêm địa chỉ mới" }).click();
        await expect(recipientInput).toBeVisible();
      }

      const provinceSelect = page.locator("#addr-province");
      const wardSelect = page.locator("#addr-ward");

      // Wait for location dropdowns to finish loading from /api/checkout/locations.
      await expect(provinceSelect).toBeEnabled();
      await expect(provinceSelect).toHaveValue(/.+/);

      const chosenProvince = await provinceSelect.inputValue();
      await provinceSelect.selectOption(chosenProvince);

      await expect(wardSelect).toBeEnabled();
      await expect(wardSelect).toHaveValue(/.+/);
      const chosenWard = await wardSelect.inputValue();
      await wardSelect.selectOption(chosenWard);

      await recipientInput.fill(buyer.fullName);
      await page.locator("#addr-phone").fill(buyer.phone);
      await page.locator("#addr-street").fill(buyer.streetAddress);

      await page.getByRole("button", { name: "Lưu và giao tới địa chỉ này" }).click();

      // Saved address radio card appears and becomes selected.
      const addressGroup = page.getByRole("radiogroup", { name: "Chọn địa chỉ nhận hàng" });
      await expect(addressGroup).toBeVisible();
      const selectedAddressRadio = addressGroup.locator('[role="radio"][aria-checked="true"]').first();
      await expect(selectedAddressRadio).toContainText(buyer.streetAddress);

      // Select COD payment method (CheckoutClient defaults to VIETQR) and wait for COD shipping quote.
      const codRadio = page.getByRole("radio", { name: /Thanh toán khi nhận hàng \(COD\)/i });
      await expect(codRadio).toBeVisible();
      const codQuotePromise = page
        .waitForResponse(
          (res) =>
            res.url().includes("/api/checkout/shipping-quote") &&
            res.request().method() === "POST" &&
            res.ok(),
          { timeout: 15_000 }
        )
        .catch(() => null);
      await codRadio.click();
      await expect(codRadio).toHaveAttribute("aria-checked", "true");
      await codQuotePromise;

      // Wait for real shipping quote for the COD order to settle.
      const carrierGroup = page.getByRole("radiogroup", { name: /Chọn đơn vị vận chuyển/i });
      await expect(carrierGroup).toBeVisible();
      const activeCarrier = carrierGroup.locator('[role="radio"][aria-checked="true"]').first();
      await expect(activeCarrier).toBeVisible();
      await expect(activeCarrier).toContainText(/₫/);

      const reviewAside = page.locator('aside[aria-label="Chi tiết thanh toán"]');
      await expect(reviewAside).toContainText(/Phí vận chuyển/);

      const placeOrderBtn = reviewAside.getByRole("button", { name: /Đặt hàng.*COD/i });
      await expect(placeOrderBtn).toBeEnabled();
      await placeOrderBtn.click();

      // Fail fast if CheckoutClient renders a [role="alert"] error; otherwise wait for /don-hang redirect.
      const checkoutAlert = reviewAside.locator('[role="alert"]').first();
      const navOutcome = await Promise.race([
        page
          .waitForURL(/\/don-hang(?:\/[^/?#]+)?$/, { timeout: 30_000, waitUntil: "commit" })
          .then(() => ({ kind: "navigated" as const }))
          .catch((error: unknown) => ({ kind: "nav_error" as const, error })),
        checkoutAlert
          .waitFor({ state: "visible", timeout: 30_000 })
          .then(async () => ({
            kind: "alert" as const,
            message: (await checkoutAlert.innerText()).trim(),
          }))
          .catch(() => new Promise<never>(() => {})),
      ]);

      if (navOutcome.kind === "alert") {
        throw new Error(`Checkout failed with UI alert: ${navOutcome.message}`);
      }
      if (navOutcome.kind === "nav_error") {
        throw navOutcome.error;
      }
    });

    await test.step("5. Verify newly created COD order on /don-hang/<id> and /don-hang", async () => {
      const currentPath = new URL(page.url()).pathname;
      const detailMatch = currentPath.match(/^\/don-hang\/([^/]+)$/);

      if (detailMatch) {
        createdOrderId = decodeURIComponent(detailMatch[1]);
      } else {
        // Landed on /don-hang list; locate the first COD order and open its detail view.
        const firstOrderCard = page.locator('article:has(a[href^="/don-hang/"])').first();
        await expect(firstOrderCard).toBeVisible();
        await firstOrderCard.getByRole("link", { name: "Xem chi tiết" }).click();
        await expect(page).toHaveURL(/\/don-hang\/[^/?#]+$/);
        createdOrderId = decodeURIComponent(new URL(page.url()).pathname.replace(/^\/don-hang\//, ""));
      }

      // Assert order detail view (/don-hang/<id>)
      const detailHeading = page.locator("h1");
      await expect(detailHeading).toContainText(/Chi tiết đơn/i);
      const headingText = (await detailHeading.innerText()).trim();
      createdOrderCode = headingText.replace(/^Chi tiết đơn\s*/i, "").trim();
      expect(createdOrderCode.length).toBeGreaterThan(0);

      await expect(page.getByText("Thanh toán khi nhận hàng (COD)", { exact: true })).toBeVisible();
      await expect(
        page.getByText(/Chờ xác nhận|Đang chuẩn bị|Đang giao hàng|Đã giao/).first()
      ).toBeVisible();
      await expect(page.getByText(selectedProductName).first()).toBeVisible();
      await expect(page.getByText(buyer.streetAddress).first()).toBeVisible();
      await expect(page.getByText("Tổng thanh toán").first()).toBeVisible();

      // Navigate to /don-hang and confirm the newly created COD order is listed.
      await page.locator('nav[aria-label="Đường dẫn đơn hàng"] a[href="/don-hang"]').click();
      await expect(page).toHaveURL(/\/don-hang\/?$/);
      await expect(page.locator("h1")).toContainText(/Đơn hàng tại/i);

      const orderLinkSelector = `a[href="/don-hang/${createdOrderId}"]`;
      const orderCard = page.locator("article").filter({ has: page.locator(orderLinkSelector) }).first();
      await expect(orderCard).toBeVisible();
      await expect(orderCard).toContainText(createdOrderCode);
      await expect(orderCard).toContainText("Thanh toán khi nhận hàng (COD)");
      await expect(orderCard).toContainText(selectedProductName);
    });
  });
});
