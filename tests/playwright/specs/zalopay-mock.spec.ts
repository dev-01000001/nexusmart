import { expect, test } from "@playwright/test";
import {
  assertNoHorizontalOverflow,
  createUniqueBuyer,
  fillAndSaveCheckoutAddress,
  registerBuyerViaUi,
} from "../helpers/site-api";

test.describe("Storefront ZaloPay Mock & Method Switching Flow", () => {
  test("checkout with ZaloPay, inspect waiting page + mock-zalopay notice, and switch to VietQR", async ({
    page,
  }) => {
    const buyer = createUniqueBuyer("zalopay");
    let waitUrl = "";

    await test.step("1. Register buyer and add an in-stock storefront product to cart", async () => {
      await registerBuyerViaUi(page, buyer);

      await page.goto("/san-pham");
      const firstCard = page
        .locator('article:has(a[href^="/san-pham/"])')
        .filter({ hasNotText: "Tạm hết hàng" })
        .first();
      await expect(firstCard).toBeVisible();
      await firstCard.locator('a[href^="/san-pham/"]').last().click();
      await expect(page).toHaveURL(/\/san-pham\/.+/);
      const addBtn = page.getByRole("button", { name: /Thêm vào giỏ/i });
      await expect(addBtn).toBeVisible();

      const sizeLabel = page.locator("span", { hasText: /^Kích cỡ:/ }).first();
      if (await sizeLabel.isVisible().catch(() => false)) {
        const enabledSizeBtn = sizeLabel.locator("..").locator("button:not([disabled])").first();
        if (await enabledSizeBtn.isVisible().catch(() => false)) {
          await enabledSizeBtn.click();
        }
      }

      await expect(addBtn).toBeEnabled();
      await addBtn.click();
      await expect(page.getByRole("link", { name: "Xem giỏ hàng" })).toBeVisible();
    });

    await test.step("2. Checkout on /thanh-toan with Ví điện tử ZaloPay", async () => {
      await page.goto("/thanh-toan");
      await expect(page.locator("h1")).toContainText(/Thanh toán/i);

      await fillAndSaveCheckoutAddress(page, buyer);

      const zpRadio = page.getByRole("radio", { name: /Ví điện tử ZaloPay/i });
      await expect(zpRadio).toBeVisible();
      await zpRadio.click();
      await expect(zpRadio).toHaveAttribute("aria-checked", "true");

      const reviewAside = page.locator('aside[aria-label="Chi tiết thanh toán"]');
      const placeOrderBtn = reviewAside.getByRole("button", { name: /Đặt hàng.*ZaloPay/i });
      await expect(placeOrderBtn).toBeEnabled();
      await placeOrderBtn.click();

      await expect(page).toHaveURL(/\/thanh-toan\/cho\/[^/?#]+(?:\?.*)?$/, { timeout: 30_000 });
      waitUrl = page.url();
    });

    await test.step("3. Verify ZaloPay waiting page and neutral /thanh-toan/mock-zalopay simulation notice", async () => {
      await expect(page.locator("h1")).toContainText("Thanh toán ZaloPay");
      await assertNoHorizontalOverflow(page, "thanh-toan/cho ZaloPay");

      const zpLink = page.getByRole("link", { name: /Thanh toán qua ZaloPay/i });
      await expect(zpLink).toBeVisible();
      const href = (await zpLink.getAttribute("href")) ?? "";
      expect(href).toMatch(/^\/thanh-toan\/mock-zalopay|^https:\/\/([a-z0-9-]+\.)?zalopay\.vn/i);

      if (href.startsWith("/thanh-toan/mock-zalopay")) {
        await page.goto(href);
        await expect(
          page.getByText("Trang mô phỏng, không phải trang ZaloPay thật.")
        ).toBeVisible();
        await assertNoHorizontalOverflow(page, "thanh-toan/mock-zalopay");
      }
    });

    await test.step("4. Switch payment method from ZaloPay to VietQR on the waiting page", async () => {
      await page.goto(waitUrl);
      const switchBtn = page.getByRole("button", { name: /Đổi sang VietQR/i });
      await expect(switchBtn).toBeVisible();
      await switchBtn.click();

      await expect(page.locator("h1")).toContainText("Thanh toán VietQR", { timeout: 15_000 });
      await expect(page).not.toHaveURL(waitUrl);
      await assertNoHorizontalOverflow(page, "thanh-toan/cho switched VietQR");
    });
  });
});
