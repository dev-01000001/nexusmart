import { expect, test } from "@playwright/test";
import {
  assertNoHorizontalOverflow,
  createUniqueBuyer,
  registerBuyerViaUi,
  sellerAcceptReturnViaGateway,
  setupDeliveredSiteOrder,
} from "../helpers/site-api";

test.describe("Storefront Buyer Return Request Flow", () => {
  test("request return with external evidence URL on delivered order and view return label after seller accepts", async ({
    page,
  }) => {
    const buyer = createUniqueBuyer("return");
    const evidenceUrl = "https://example.com/pw-return-proof.jpg";
    const returnDetail = "Sản phẩm còn nguyên tem mác, cần đổi trả do nhầm kích cỡ khi nhận hàng.";

    await registerBuyerViaUi(page, buyer);
    const fixture = await setupDeliveredSiteOrder(page, buyer);

    await test.step("1. Open delivered order and submit return request via modal sheet", async () => {
      await page.goto(`/don-hang/${encodeURIComponent(fixture.orderId)}`);
      await expect(page.locator("h1")).toContainText(/Chi tiết đơn/i);
      await assertNoHorizontalOverflow(page, "don-hang delivered before return");

      await page.getByRole("button", { name: "Yêu cầu đổi trả" }).click();

      const dialog = page.getByRole("dialog", { name: "Yêu cầu đổi trả" });
      await expect(dialog).toBeVisible();

      await dialog.locator("#return-note").fill(returnDetail);
      await dialog.locator("#return-evidence-url").fill(evidenceUrl);
      await dialog.getByRole("button", { name: "Thêm", exact: true }).click();
      await expect(dialog.getByText(evidenceUrl)).toBeVisible();

      await dialog.getByRole("button", { name: "Gửi yêu cầu đổi trả" }).click();
      await expect(dialog).toBeHidden({ timeout: 15_000 });
    });

    await test.step("2. Verify return status & external evidence link (example.com) on /don-hang/<id>", async () => {
      const returnSection = page.getByRole("region", { name: "Yêu cầu đổi trả" });
      await expect(returnSection).toBeVisible();
      await expect(returnSection).toContainText(returnDetail);

      const evidenceLink = returnSection.getByRole("link", { name: /Bằng chứng 1 \(example\.com\)/i });
      await expect(evidenceLink).toBeVisible();
      await expect(evidenceLink).toHaveAttribute("rel", /nofollow/);
      await assertNoHorizontalOverflow(page, "don-hang with pending return");
    });

    await test.step("3. Seller accepts return -> buyer reloads and sees return tracking code & label link", async () => {
      const accepted = await sellerAcceptReturnViaGateway(fixture.orderId);
      expect(accepted.returnId.length).toBeGreaterThan(0);

      await page.reload();
      const returnSection = page.getByRole("region", { name: "Yêu cầu đổi trả" });
      await expect(returnSection).toBeVisible();

      if (accepted.returnTrackingCode) {
        await expect(returnSection).toContainText(accepted.returnTrackingCode);
        const labelLink = returnSection.getByRole("link", { name: /Mở phiếu hoàn hàng/i });
        await expect(labelLink).toBeVisible();
        await expect(labelLink).toHaveAttribute(
          "href",
          new RegExp(`/api/shipping/labels/${accepted.returnTrackingCode}.*order_id=`)
        );
      }
      await assertNoHorizontalOverflow(page, "don-hang with accepted return");
    });
  });
});
