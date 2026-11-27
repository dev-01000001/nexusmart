// Functional flows for Seller portal (/seller/**).
// Strictly uses role + visible Vietnamese text locators only.
import {
  assert,
  CREDS,
  gotoPage,
  loginViaBrowser,
  rootOrigin,
  waitAnyVisible,
} from "./lib.mjs";

const QA_PRODUCT_NAME = "QA-S9-San-pham-kiem-thu";

async function cleanupQaProducts(page, origin) {
  await gotoPage(page, `${origin}/seller/san-pham`);
  const searchBox = page.getByRole("searchbox", {
    name: /Tìm sản phẩm hoặc mã SKU/i,
  });
  await searchBox.waitFor({ state: "visible", timeout: 10_000 });
  await searchBox.fill("QA-S9-");
  await page.waitForTimeout(900);

  for (let attempt = 0; attempt < 5; attempt++) {
    const deletableRow = page
      .getByRole("row")
      .filter({ hasText: /QA-S9-/i })
      .filter({ has: page.getByRole("button", { name: /^Xóa$|^Xoá$/i }) })
      .first();
    if (!(await deletableRow.isVisible().catch(() => false))) break;
    await deletableRow
      .getByRole("button", { name: /^Xóa$|^Xoá$/i })
      .first()
      .click({ timeout: 10_000 });
    const confirmBtn = await waitAnyVisible(
      [
        page.getByRole("button", { name: /Xác nhận xóa|Xác nhận xoá/i }),
        page
          .getByRole("alertdialog")
          .getByRole("button", { name: /^Xóa$|^Xoá$|Xác nhận/i }),
        page
          .getByRole("dialog")
          .getByRole("button", { name: /^Xóa$|^Xoá$|Xác nhận/i }),
      ],
      6_000,
    );
    await confirmBtn.click({ timeout: 10_000 });
    await page.waitForTimeout(1_000);
  }

  const clearBtn = page
    .getByRole("button", { name: /Xóa bộ lọc|Xoá bộ lọc/i })
    .first();
  if (await clearBtn.isVisible().catch(() => false)) {
    await clearBtn.click();
    await page.waitForTimeout(400);
  } else {
    await searchBox.fill("");
    await page.waitForTimeout(400);
  }
}

export async function buildSellerFlows({ browser, port }) {
  const origin = rootOrigin(port);
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  await loginViaBrowser(page, `${origin}/dang-nhap`, CREDS.seller);

  const flows = [
    {
      name: "seller:orders-filter",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/don-hang`);
        await page
          .getByRole("heading", { level: 1, name: /Đơn hàng/i })
          .waitFor({ state: "visible" });
        const groups = [
          /Đã xác nhận/i,
          /Đang chuẩn bị/i,
          /Đang giao/i,
          /Đã giao \/ Hoàn tất/i,
          /Chờ TT \/ Hủy \/ Trả|Chờ TT \/ Huỷ \/ Trả/i,
          /Tất cả/i,
        ];
        for (const label of groups) {
          const tab = page.getByRole("tab", { name: label }).first();
          await tab.waitFor({ state: "visible", timeout: 8_000 });
          await tab.click();
          await page.waitForTimeout(350);
          const selected = await tab.getAttribute("aria-selected");
          assert(
            selected === "true",
            `Tab ${label} không chuyển aria-selected=true`,
          );
        }
        return "6 nhóm lọc";
      },
    },
    {
      name: "seller:orders-quick-view",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/don-hang`);
        const firstOrderBtn = page
          .getByRole("button", { name: /^NM-/ })
          .first();
        await firstOrderBtn.waitFor({ state: "visible", timeout: 10_000 });
        const orderCode = (await firstOrderBtn.textContent())?.trim() || "";
        await firstOrderBtn.click();
        const dialog = page.getByRole("dialog").first();
        await dialog.waitFor({ state: "visible", timeout: 8_000 });
        await dialog
          .getByRole("link", { name: /Mở trang chi tiết|Chi tiết/i })
          .first()
          .waitFor({ state: "visible" });
        assert(
          (await dialog.textContent())?.includes(orderCode.slice(0, 12)),
          `Xem nhanh không hiện mã đơn ${orderCode}`,
        );
        await page.keyboard.press("Escape");
        await page.waitForTimeout(300);
        return orderCode;
      },
    },
    {
      name: "seller:order-detail",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/don-hang`);
        const detailLink = page
          .getByRole("link", { name: /^Chi tiết$/i })
          .first();
        await detailLink.waitFor({ state: "visible", timeout: 10_000 });
        await detailLink.click();
        await page.waitForURL(/\/seller\/don-hang\/[0-9a-f-]+/i, {
          timeout: 15_000,
        });
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });
        const bodyText = await page.getByRole("main").textContent();
        assert(
          bodyText && /NM-/.test(bodyText),
          "Trang chi tiết đơn không hiển thị mã đơn NM-",
        );
        return page.url().split("/").pop();
      },
    },
    {
      name: "seller:products-filter-low-stock",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/san-pham`);
        await page
          .getByRole("heading", { level: 1, name: /Sản phẩm/i })
          .waitFor({ state: "visible" });
        const lowStockTab = page
          .getByRole("tab", { name: /Hết \/ Sắp hết|Sắp hết/i })
          .first();
        await lowStockTab.waitFor({ state: "visible", timeout: 8_000 });
        await lowStockTab.click();
        await page.waitForTimeout(450);
        assert(
          (await lowStockTab.getAttribute("aria-selected")) === "true",
          "Tab sắp hết chưa được chọn",
        );

        const allTab = page.getByRole("tab", { name: /Tất cả/i }).first();
        await allTab.click();
        await page.waitForTimeout(350);

        const searchBox = page.getByRole("searchbox", {
          name: /Tìm sản phẩm hoặc mã SKU/i,
        });
        await searchBox.fill("Boxer");
        await page.waitForTimeout(600);
        await page
          .getByRole("row")
          .filter({ hasText: /Boxer/i })
          .or(page.getByRole("button", { name: /Boxer/i }))
          .first()
          .waitFor({ state: "visible", timeout: 8_000 });
        await searchBox.fill("");
        await page.waitForTimeout(300);
        return "lọc sắp hết + tìm Boxer";
      },
    },
    {
      name: "seller:products-edit-stock",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/san-pham`);
        const expandBtn = page
          .getByRole("button", { name: /Sửa tồn/i })
          .first();
        await expandBtn.waitFor({ state: "visible", timeout: 10_000 });
        await expandBtn.click();

        const stockInput = page
          .getByRole("spinbutton", { name: /Tồn kho/i })
          .first();
        await stockInput.waitFor({ state: "visible", timeout: 8_000 });
        const originalVal = Number(await stockInput.inputValue());
        assert(
          Number.isFinite(originalVal),
          `Giá trị tồn kho không hợp lệ: ${originalVal}`,
        );
        const nextVal = originalVal + 1;

        const saveBtn = page
          .getByRole("button", { name: /Lưu tồn|^Lưu$/i })
          .first();
        await stockInput.fill(String(nextVal));
        await saveBtn.click();
        await page.waitForTimeout(900);

        // Reload to confirm persistence
        await gotoPage(page, `${origin}/seller/san-pham`);
        await page
          .getByRole("button", { name: /Sửa tồn/i })
          .first()
          .click();
        const reloadedInput = page
          .getByRole("spinbutton", { name: /Tồn kho/i })
          .first();
        await reloadedInput.waitFor({ state: "visible", timeout: 8_000 });
        const afterSave = Number(await reloadedInput.inputValue());
        assert(
          afterSave === nextVal,
          `Tồn kho sau reload là ${afterSave}, mong đợi ${nextVal}`,
        );

        // Restore original stock value
        await reloadedInput.fill(String(originalVal));
        await page
          .getByRole("button", { name: /Lưu tồn|^Lưu$/i })
          .first()
          .click();
        await page.waitForTimeout(900);
        const restored = Number(await reloadedInput.inputValue());
        assert(restored === originalVal, `Chưa trả tồn kho về ${originalVal}`);
        return `${originalVal} -> ${nextVal} -> ${originalVal}`;
      },
    },
    {
      name: "seller:products-create-draft",
      fn: async () => {
        await cleanupQaProducts(page, origin);
        await gotoPage(page, `${origin}/seller/san-pham/moi`);
        await page
          .getByRole("heading", { level: 1, name: /Thêm sản phẩm/i })
          .waitFor({ state: "visible" });

        await page.getByLabel(/Tên sản phẩm/i).fill(QA_PRODUCT_NAME);
        const catSelect = page.getByLabel(/Ngành hàng/i);
        const catOpts = await catSelect.getByRole("option").allTextContents();
        if (catOpts.length > 1) {
          await catSelect.selectOption({ index: 1 });
        }
        await page
          .getByRole("textbox", { name: /Mô tả/i })
          .first()
          .fill(
            "Sản phẩm kiểm thử tự động QA-S9 cho bộ kiểm chức năng giao diện.",
          );
        const galleryBtn = page
          .getByRole("button", { name: /Thư viện ảnh mẫu/i })
          .first();
        if (await galleryBtn.isVisible().catch(() => false)) {
          await galleryBtn.click();
          const sampleBtn = page
            .getByRole("button", { name: /Thêm Mẫu minh họa 1/i })
            .first();
          await sampleBtn.waitFor({ state: "visible", timeout: 5_000 });
          await sampleBtn.click();
        }
        const skuRow = page
          .getByRole("row")
          .filter({ has: page.getByRole("textbox", { name: /^Mã SKU/i }) })
          .first();
        await skuRow.waitFor({ state: "visible", timeout: 8_000 });
        await skuRow
          .getByRole("spinbutton", { name: /^Đơn giá/i })
          .fill("199000");
        await skuRow.getByRole("spinbutton", { name: /^Tồn kho/i }).fill("10");
        await skuRow
          .getByRole("spinbutton", { name: /^Khối lượng/i })
          .fill("250");

        const submitBtn = page
          .getByRole("button", {
            name: /Đăng bán sản phẩm|Lưu nháp|Tạo sản phẩm/i,
          })
          .first();
        await submitBtn.click();
        await page.waitForURL(/\/seller\/san-pham(?:$|\?|\/(?!moi))/, {
          timeout: 20_000,
        });
        await page.waitForTimeout(600);

        // Verify created product is visible on list, then clean up via UI delete
        await gotoPage(page, `${origin}/seller/san-pham`);
        const searchBox = page.getByRole("searchbox", {
          name: /Tìm sản phẩm hoặc mã SKU/i,
        });
        await searchBox.fill(QA_PRODUCT_NAME);
        await page.waitForTimeout(700);
        await page
          .getByText(QA_PRODUCT_NAME)
          .first()
          .waitFor({ state: "visible", timeout: 10_000 });

        await cleanupQaProducts(page, origin);
        return "tạo + xác nhận + dọn sạch";
      },
    },
    {
      name: "seller:promotions-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/khuyen-mai`);
        await page
          .getByRole("heading", { level: 1, name: /Khuyến mãi/i })
          .waitFor({ state: "visible" });
        for (const label of [
          /Voucher/i,
          /Giảm giá shop|Chương trình giảm giá/i,
          /Flash sale/i,
        ]) {
          const tab = page.getByRole("tab", { name: label }).first();
          await tab.waitFor({ state: "visible", timeout: 8_000 });
          await tab.click();
          await page.waitForTimeout(300);
          assert(
            (await tab.getAttribute("aria-selected")) === "true",
            `Tab ${label} chưa kích hoạt`,
          );
        }
        return "3 tab khuyến mãi";
      },
    },
    {
      name: "seller:finance-ledger",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/tai-chinh`);
        await page
          .getByRole("heading", { level: 1, name: /Tài chính/i })
          .waitFor({ state: "visible" });
        const ledgerTab = page.getByRole("tab", { name: /Sổ cái/i }).first();
        if (await ledgerTab.isVisible().catch(() => false)) {
          await ledgerTab.click();
          await page.waitForTimeout(300);
        }
        await page
          .getByText(/Sổ cái/i)
          .first()
          .waitFor({ state: "visible", timeout: 8_000 });
        const pendingBtn = page
          .getByRole("button", { name: /Đang tạm giữ/i })
          .first();
        if (await pendingBtn.isVisible().catch(() => false)) {
          await pendingBtn.click();
          await page.waitForTimeout(300);
          await page
            .getByRole("button", { name: /^Tất cả$/i })
            .first()
            .click();
        }
        return "sổ cái + lọc tài khoản";
      },
    },
    {
      name: "seller:interaction-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/tuong-tac`);
        await page
          .getByRole("heading", { level: 1, name: /Tương tác/i })
          .waitFor({ state: "visible" });
        const notifTab = page.getByRole("tab", { name: /Thông báo/i }).first();
        await notifTab.waitFor({ state: "visible", timeout: 8_000 });
        await notifTab.click();
        await page.waitForTimeout(400);
        assert(
          (await notifTab.getAttribute("aria-selected")) === "true",
          "Tab Thông báo chưa chọn",
        );

        const reviewTab = page.getByRole("tab", { name: /Đánh giá/i }).first();
        await reviewTab.click();
        await page.waitForTimeout(400);
        assert(
          (await reviewTab.getAttribute("aria-selected")) === "true",
          "Tab Đánh giá chưa chọn",
        );
        return "Đánh giá <-> Thông báo";
      },
    },
    {
      name: "seller:site-studio-tabs",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/site`);
        await page
          .getByRole("heading", { level: 1, name: /Site/i })
          .waitFor({ state: "visible" });
        const candidateTabs = [
          /Giao diện & Bố cục|Giao diện/i,
          /Khối & Block|Bố cục|Nội dung/i,
          /Phiên bản/i,
          /Thư viện mẫu|Mẫu/i,
        ];
        let switched = 0;
        for (const label of candidateTabs) {
          const tab = page.getByRole("tab", { name: label }).first();
          if (await tab.isVisible().catch(() => false)) {
            await tab.click();
            await page.waitForTimeout(300);
            switched++;
          }
        }
        assert(switched >= 2, `Chỉ đổi được ${switched} tab trong Site Studio`);
        return `đổi ${switched} chế độ, không lưu`;
      },
    },
    {
      name: "seller:profile",
      fn: async () => {
        await gotoPage(page, `${origin}/seller/ho-so`);
        await page
          .getByRole("heading", { level: 1, name: /Hồ sơ/i })
          .waitFor({ state: "visible" });
        const shipTab = page.getByRole("tab", { name: /Vận chuyển/i }).first();
        if (await shipTab.isVisible().catch(() => false)) {
          await shipTab.click();
          await page.waitForTimeout(300);
        }
        await page
          .getByText(/Vận chuyển|Gian hàng|Email đăng nhập/i)
          .first()
          .waitFor({ state: "visible" });
        return "mở hồ sơ + tab";
      },
    },
    {
      name: "seller:cmd-k",
      fn: async () => {
        await gotoPage(page, `${origin}/seller`);
        await page
          .getByRole("navigation")
          .first()
          .waitFor({ state: "visible", timeout: 15_000 });
        await page.keyboard.press("Control+k");
        const dialog = page.getByRole("dialog").first();
        await dialog.waitFor({ state: "visible", timeout: 6_000 });
        const cmdInput = dialog
          .getByRole("combobox")
          .or(dialog.getByRole("textbox"))
          .first();
        await cmdInput.waitFor({ state: "visible", timeout: 5_000 });
        await cmdInput.fill("Tài chính");
        await page.waitForTimeout(250);
        const option = dialog
          .getByRole("option", { name: /Tài chính/i })
          .first();
        await option.waitFor({ state: "visible", timeout: 5_000 });
        await option.click();
        await page.waitForURL(/\/seller\/tai-chinh/, { timeout: 10_000 });
        const heading = page.getByRole("heading", {
          level: 1,
          name: /Tài chính/i,
        });
        if (!(await heading.isVisible({ timeout: 8_000 }).catch(() => false))) {
          // Transient gateway 429 can render an error StateView without h1; retry once after backoff.
          await page.waitForTimeout(6_000);
          await gotoPage(page, `${origin}/seller/tai-chinh`);
        }
        await heading.waitFor({ state: "visible", timeout: 15_000 });
        return "Ctrl+K -> /seller/tai-chinh";
      },
    },
  ];

  return { flows, cleanup: () => ctx.close() };
}
