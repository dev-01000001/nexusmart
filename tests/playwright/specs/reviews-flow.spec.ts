import { expect, test } from "@playwright/test";
import {
  assertNoHorizontalOverflow,
  createUniqueBuyer,
  registerBuyerViaUi,
  setupDeliveredSiteOrder,
} from "../helpers/site-api";

test.describe("Storefront Verified Review Flow", () => {
  test("confirm order receipt, submit a 5-star review with image, and see it on the site PDP", async ({
    page,
  }) => {
    const buyer = createUniqueBuyer("review");
    const reviewComment = `Vải cotton mát, đường may kỹ, đóng gói cẩn thận (${Date.now().toString(36)}).`;

    await registerBuyerViaUi(page, buyer);
    const fixture = await setupDeliveredSiteOrder(page, buyer);

    await test.step("1. Open delivered order on site and confirm receipt (DELIVERED -> COMPLETED)", async () => {
      await page.goto(`/don-hang/${encodeURIComponent(fixture.orderId)}`);
      await expect(page.locator("h1")).toContainText(/Chi tiết đơn/i);
      await assertNoHorizontalOverflow(page, "don-hang delivered before review");

      const confirmBtn = page.getByRole("button", { name: "Đã nhận được hàng" });
      await expect(confirmBtn).toBeVisible();
      await confirmBtn.click();

      await expect(page.getByRole("region", { name: "Đánh giá sản phẩm" })).toBeVisible({
        timeout: 20_000,
      });
    });

    await test.step("2. Submit 5-star verified review with safe image on /don-hang/<id>", async () => {
      const reviewSection = page.getByRole("region", { name: "Đánh giá sản phẩm" });
      const fiveStarBtn = reviewSection.getByRole("radio", { name: "5 ★" }).first();
      await fiveStarBtn.click();
      await expect(fiveStarBtn).toHaveAttribute("aria-checked", "true");

      const commentInput = fixture.orderItemId
        ? page.locator(`#rev-comment-${fixture.orderItemId}`)
        : reviewSection.locator("textarea").first();
      await expect(commentInput).toBeVisible();
      await commentInput.fill(reviewComment);

      const imgInput = fixture.orderItemId
        ? page.locator(`#rev-img-${fixture.orderItemId}`)
        : reviewSection.locator('input[type="text"]').first();
      await imgInput.fill("/products/ao-thun-cotton-trang.png");
      await reviewSection.getByRole("button", { name: "Thêm ảnh" }).click();

      await reviewSection.getByRole("button", { name: "Gửi đánh giá" }).click();

      await expect(reviewSection.getByText("5/5 ★").first()).toBeVisible({ timeout: 15_000 });
      await expect(reviewSection.getByText(reviewComment)).toBeVisible();
    });

    await test.step("3. Verify review appears publicly on site product detail page", async () => {
      await page.goto(`/san-pham/${encodeURIComponent(fixture.productSlug)}`);
      await expect(page.locator("h1")).toContainText(fixture.productName);
      await assertNoHorizontalOverflow(page, "san-pham PDP after review");

      const reviewsSummary = page.locator("summary", { hasText: "Đánh giá khách hàng" }).first();
      await expect(reviewsSummary).toBeVisible();
      const detailsEl = reviewsSummary.locator("..");
      const isOpen = await detailsEl.evaluate((el) => (el as HTMLDetailsElement).open);
      if (!isOpen) {
        await reviewsSummary.click();
      }

      await expect(detailsEl.getByText(reviewComment)).toBeVisible({ timeout: 15_000 });
    });
  });
});
