import { expect, test } from "@playwright/test";
import {
  assertNoHorizontalOverflow,
  createUniqueBuyer,
  fillAndSaveCheckoutAddress,
  registerBuyerViaUi,
} from "../helpers/site-api";

test.describe("Storefront Buyer E2E Journey", () => {
  test("register with Mailpit OTP + phone OTP, search product, add to cart, checkout COD, and view order", async ({
    page,
  }) => {
    const buyer = createUniqueBuyer("cod");
    let selectedProductName = "";
    let createdOrderId = "";
    let createdOrderCode = "";

    await test.step("1. Register buyer, verify Mailpit email OTP & phone OTP for COD", async () => {
      await registerBuyerViaUi(page, buyer, { verifyPhone: true });
      await assertNoHorizontalOverflow(page, "tai-khoan");
    });

    await test.step("2. Search seeded storefront products on /tim-kiem and open a PDP", async () => {
      await page.goto("/tim-kiem");
      await assertNoHorizontalOverflow(page, "tim-kiem");

      const searchForm = page.getByRole("main").getByRole("search");
      const searchInput = searchForm.getByRole("combobox");
      await expect(searchInput).toBeVisible();
      await searchInput.fill("cotton");
      await searchForm.getByRole("button", { name: "Tìm kiếm", exact: true }).click();

      await expect(page.locator("h1")).toContainText(/cotton/i);

      const productCards = page.locator('article:has(a[href^="/san-pham/"])');
      await expect(productCards.first()).toBeVisible();

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

      const productTitleLink = chosenCard.locator('a[href^="/san-pham/"]').last();
      selectedProductName = (await productTitleLink.innerText()).trim();
      expect(selectedProductName.length).toBeGreaterThan(0);

      await productTitleLink.click();
      await expect(page).toHaveURL(/\/san-pham\/.+/);
      await expect(page.locator("h1")).toContainText(selectedProductName);
      await assertNoHorizontalOverflow(page, "san-pham PDP");
    });

    await test.step("3. Select an in-stock SKU, add to cart, and verify /gio-hang", async () => {
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

      const openCartLink = page.getByRole("link", { name: "Xem giỏ hàng" });
      await expect(openCartLink).toBeVisible();
      await openCartLink.click();

      await expect(page).toHaveURL(/\/gio-hang\/?$/);
      await expect(page.locator("h1")).toContainText(/Giỏ hàng/i);
      await assertNoHorizontalOverflow(page, "gio-hang");

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
      await assertNoHorizontalOverflow(page, "thanh-toan");

      await fillAndSaveCheckoutAddress(page, buyer);

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

      const shippingStage = page.getByRole("region", { name: "Giao hàng" });
      await expect(shippingStage).toBeVisible();
      const activeCarrierBtn = shippingStage
        .locator('button[aria-expanded="false"][aria-controls="stage-shipping-body"]')
        .filter({ hasText: /₫/ })
        .first();
      await expect(activeCarrierBtn).toBeVisible();
      await expect(activeCarrierBtn).toContainText(/₫/);
      await expect(shippingStage.getByRole("radiogroup")).toHaveCount(0);

      const reviewAside = page.locator('aside[aria-label="Chi tiết thanh toán"]');
      await expect(reviewAside).toContainText(/Phí vận chuyển/);

      const placeOrderBtn = reviewAside.getByRole("button", { name: /Đặt hàng.*COD/i });
      await expect(placeOrderBtn).toBeEnabled();
      await placeOrderBtn.click();

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
        const firstOrderCard = page.locator('article:has(a[href^="/don-hang/"])').first();
        await expect(firstOrderCard).toBeVisible();
        await firstOrderCard.getByRole("link", { name: "Xem chi tiết" }).click();
        await expect(page).toHaveURL(/\/don-hang\/[^/?#]+$/);
        createdOrderId = decodeURIComponent(new URL(page.url()).pathname.replace(/^\/don-hang\//, ""));
      }

      const detailHeading = page.locator("h1");
      await expect(detailHeading).toContainText(/Chi tiết đơn/i);
      await assertNoHorizontalOverflow(page, "don-hang detail");
      const headingText = (await detailHeading.innerText()).trim();
      createdOrderCode = headingText.replace(/^Chi tiết đơn\s*/i, "").trim();
      expect(createdOrderCode.length).toBeGreaterThan(0);

      await expect(page.getByText("Thanh toán khi nhận hàng (COD)", { exact: true })).toBeVisible();
      await expect(
        page.getByText(/Chờ xác nhận|Đang chuẩn bị|Đang giao hàng|Đã giao/).first()
      ).toBeVisible();
      await expect(page.getByText(selectedProductName).first()).toBeVisible();
      await expect(page.getByRole("complementary").getByText(buyer.streetAddress)).toBeVisible();
      await expect(page.getByRole("complementary").getByText("Tổng thanh toán")).toBeVisible();

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
