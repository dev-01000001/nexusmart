// Functional flows for Admin portal (/admin/**).
// Strictly uses role + visible Vietnamese text locators only.
import {
  assert,
  CREDS,
  gotoPage,
  loginViaBrowser,
  rootOrigin,
  waitAnyVisible,
} from "./lib.mjs";

export async function buildAdminFlows({ browser, port }) {
  const origin = rootOrigin(port);
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  await loginViaBrowser(page, `${origin}/dang-nhap`, CREDS.admin);

  const flows = [
    {
      name: "admin:shops-filter-detail-confirm-cancel",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/gian-hang`);
        await page
          .getByRole("heading", { level: 1, name: /gian hàng/i })
          .waitFor({ state: "visible" });

        // Filter to active shops so we can open detail and trigger confirm dialog without mutating data
        const activeTab = page
          .getByRole("tab", { name: /Đang hoạt động/i })
          .first();
        await activeTab.waitFor({ state: "visible", timeout: 8_000 });
        await activeTab.click();
        await page.waitForTimeout(500);
        assert(
          (await activeTab.getAttribute("aria-selected")) === "true",
          "Tab Đang hoạt động chưa chọn",
        );

        const searchInput = page
          .getByRole("searchbox", { name: /Tìm kiếm hồ sơ gian hàng/i })
          .first();
        await searchInput.fill("Shop");
        await page.waitForTimeout(450);
        await searchInput.fill("");
        await page.waitForTimeout(450);

        const queueList = page
          .getByRole("list", { name: /Danh sách hồ sơ gian hàng/i })
          .first();
        const shopBtn = (await queueList.isVisible().catch(() => false))
          ? queueList.getByRole("button").first()
          : page
              .getByRole("table")
              .getByRole("row")
              .nth(1)
              .getByRole("button")
              .first()
              .or(page.getByRole("table").getByRole("row").nth(1));
        await shopBtn.waitFor({ state: "visible", timeout: 8_000 });
        await shopBtn.click();
        await page.waitForTimeout(400);

        // Fill suspension reason and open confirmation dialog, then cancel immediately
        const reasonInput = page
          .getByLabel(/Đề xuất tạm khóa gian hàng/i)
          .first();
        await reasonInput.waitFor({ state: "visible", timeout: 8_000 });
        await reasonInput.fill("QA-S9 kiểm thử mở hộp xác nhận rồi huỷ");

        const reqBtn = page
          .getByRole("button", { name: /Yêu cầu khóa gian hàng/i })
          .first();
        await reqBtn.click();

        const confirmDialog = await waitAnyVisible(
          [
            page.getByRole("alertdialog"),
            page.getByRole("dialog", { name: /Xác nhận/i }),
          ],
          6_000,
        );
        const cancelBtn = confirmDialog
          .getByRole("button", { name: /^Hủy$|^Huỷ$/i })
          .first();
        await cancelBtn.waitFor({ state: "visible", timeout: 5_000 });
        await cancelBtn.click();
        await page.waitForTimeout(300);
        assert(
          !(await confirmDialog.isVisible().catch(() => false)),
          "Hộp thoại xác nhận chưa đóng sau khi huỷ",
        );
        return "lọc + chi tiết + mở xác nhận rồi huỷ";
      },
    },
    {
      name: "admin:moderation-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/kiem-duyet`);
        await page
          .getByRole("heading", { level: 1, name: /Kiểm duyệt/i })
          .waitFor({ state: "visible" });
        const kwTab = page
          .getByRole("tab", { name: /Từ khoá cấm|Từ khóa cấm/i })
          .first();
        await kwTab.waitFor({ state: "visible", timeout: 8_000 });
        await kwTab.click();
        await page.waitForTimeout(400);
        assert(
          (await kwTab.getAttribute("aria-selected")) === "true",
          "Tab Từ khoá cấm chưa chọn",
        );

        const prodTab = page.getByRole("tab", { name: /Sản phẩm/i }).first();
        await prodTab.click();
        await page.waitForTimeout(400);
        assert(
          (await prodTab.getAttribute("aria-selected")) === "true",
          "Tab Sản phẩm chưa chọn lại",
        );
        return "Sản phẩm <-> Từ khoá cấm";
      },
    },
    {
      name: "admin:config-7-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/cau-hinh`);
        await page
          .getByRole("heading", { level: 1, name: /Cấu hình/i })
          .waitFor({ state: "visible" });
        const tabLabels = [
          /Cổng thanh toán/i,
          /Phê duyệt 2 người/i,
          /Biểu phí/i,
          /Hãng vận chuyển/i,
          /Danh mục/i,
          /Người dùng/i,
          /Voucher sàn & Xu/i,
        ];
        for (const label of tabLabels) {
          const tab = page.getByRole("tab", { name: label }).first();
          await tab.waitFor({ state: "visible", timeout: 8_000 });
          await tab.click();
          await page.waitForTimeout(350);
          assert(
            (await tab.getAttribute("aria-selected")) === "true",
            `Tab cấu hình ${label} chưa được chọn`,
          );
        }
        return "đủ 7 tab cấu hình";
      },
    },
    {
      name: "admin:reconciliation-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/doi-soat`);
        await page
          .getByRole("heading", { level: 1, name: /Đối soát/i })
          .waitFor({ state: "visible" });
        const tabs = [
          /Sổ cái/i,
          /COD/i,
          /Giao dịch lệch/i,
          /Hoàn tiền/i,
          /Đơn cần rà soát/i,
        ];
        for (const label of tabs) {
          const tab = page.getByRole("tab", { name: label }).first();
          await tab.waitFor({ state: "visible", timeout: 8_000 });
          await tab.click();
          await page.waitForTimeout(350);
          assert(
            (await tab.getAttribute("aria-selected")) === "true",
            `Tab đối soát ${label} chưa được chọn`,
          );
        }
        return "5 tab đối soát";
      },
    },
    {
      name: "admin:audit-log-filter",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/audit-log`);
        await page
          .getByRole("heading", { level: 1, name: /Nhật ký kiểm toán/i })
          .waitFor({ state: "visible" });
        const moduleSelect = page.getByLabel(/Phân hệ/i).first();
        await moduleSelect.waitFor({ state: "visible", timeout: 8_000 });
        const optCount = await moduleSelect.getByRole("option").count();
        if (optCount > 1) {
          await moduleSelect.selectOption({ index: 1 });
        }
        const applyBtn = page
          .getByRole("button", { name: /Áp dụng bộ lọc/i })
          .first();
        await applyBtn.click();
        await page.waitForTimeout(600);

        const resetBtn = page.getByRole("button", { name: /Đặt lại/i }).first();
        await resetBtn.click();
        await page.waitForTimeout(500);
        return "lọc phân hệ + đặt lại";
      },
    },
    {
      name: "admin:flash-sale-open-form",
      fn: async () => {
        await gotoPage(page, `${origin}/admin/flash-sale`);
        await page
          .getByRole("heading", { level: 1, name: /Flash sale/i })
          .waitFor({ state: "visible" });
        const openBtn = page
          .getByRole("button", { name: /Mở khung giờ|Tạo khung giờ/i })
          .first();
        await openBtn.waitFor({ state: "visible", timeout: 8_000 });
        await openBtn.click();

        const dialog = page.getByRole("dialog").first();
        await dialog.waitFor({ state: "visible", timeout: 6_000 });
        await dialog
          .getByText(/Mở khung giờ Flash sale|Khung giờ/i)
          .first()
          .waitFor({ state: "visible" });

        const cancelBtn = dialog
          .getByRole("button", { name: /^Hủy$|^Huỷ$|^Đóng$/i })
          .first();
        if (await cancelBtn.isVisible().catch(() => false)) {
          await cancelBtn.click();
        } else {
          await page.keyboard.press("Escape");
        }
        await page.waitForTimeout(300);
        return "mở form khung giờ + đóng";
      },
    },
  ];

  return { flows, cleanup: () => ctx.close() };
}
