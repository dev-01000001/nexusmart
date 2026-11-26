// Functional flows for all 4 seed storefront sites (bach-dang, sai-gon, da-nang, ha-noi).
// Strictly uses role + visible Vietnamese text locators only.
import {
  assert,
  CREDS,
  gotoPage,
  loginViaBrowser,
  SEED_SITES,
  siteOrigin,
  waitAnyVisible,
} from "./lib.mjs";

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
  // Radix / shadcn Select trigger (role="combobox")
  await selectLoc.click();
  const opt = page.getByRole("option").nth(index);
  if (await opt.isVisible({ timeout: 4_000 }).catch(() => false)) {
    await opt.click();
    return true;
  }
  await page.keyboard.press("Escape");
  return false;
}

async function addRegularProductToCart(page, canonicalOrigin) {
  await gotoPage(page, `${canonicalOrigin}/san-pham`);
  const articles = page.getByRole("article");
  const total = await articles.count();
  assert(total > 0, "Danh mục sản phẩm không có sản phẩm nào");

  // Prefer a product card without a Flash Sale badge so COD checkout is never blocked by flash-sale COD rule.
  let chosen = articles.first();
  for (let i = 0; i < Math.min(total, 8); i++) {
    const card = articles.nth(i);
    const txt = (await card.textContent()) || "";
    if (!/flash\s*sale/i.test(txt)) {
      chosen = card;
      break;
    }
  }
  await chosen.getByRole("link").first().click();
  await page.waitForURL(/\/san-pham\/[^/]+/, { timeout: 15_000 });
  await page
    .waitForLoadState("networkidle", { timeout: 10_000 })
    .catch(() => {});
  await page.waitForTimeout(300);

  // Pick an enabled variant chip (preferring a SKU with >= 2 stock so cart +/- works cleanly)
  const main = page.getByRole("main");
  const addBtn = page.getByRole("button", { name: /^Thêm vào giỏ$/i }).first();
  const pdpIncBtn = page
    .getByRole("button", { name: /Tăng số lượng/i })
    .first();
  const variantCandidates = [
    main.getByRole("radio", { checked: false }),
    main.getByRole("button", { pressed: false, name: /^(?!Xem ảnh)/i }),
  ];
  let pickedGoodVariant = false;
  for (const cand of variantCandidates) {
    const cnt = await cand.count().catch(() => 0);
    for (let i = 0; i < Math.min(cnt, 8); i++) {
      const btn = cand.nth(i);
      if (
        (await btn.isVisible().catch(() => false)) &&
        (await btn.isEnabled().catch(() => false))
      ) {
        const label = (await btn.textContent())?.trim() || "";
        if (
          label &&
          label.length <= 24 &&
          !/Xem ảnh|Thêm vào giỏ|Mua ngay/i.test(label)
        ) {
          await btn.click();
          await page.waitForTimeout(200);
          const canAdd = await addBtn.isEnabled().catch(() => false);
          const canInc = await pdpIncBtn.isEnabled().catch(() => true);
          if (canAdd && canInc) {
            pickedGoodVariant = true;
            break;
          }
        }
      }
    }
    if (pickedGoodVariant) break;
  }

  await addBtn.waitFor({ state: "visible", timeout: 10_000 });
  for (let attempt = 0; attempt < 2; attempt++) {
    await addBtn.click();
    try {
      const drawer = await waitAnyVisible(
        [
          page.getByRole("dialog", { name: /Đã thêm vào giỏ/i }),
          page
            .getByRole("dialog")
            .filter({ hasText: /Đã thêm vào giỏ|Mở giỏ hàng|Xem giỏ hàng/i }),
        ],
        8_000,
      );
      return drawer;
    } catch (err) {
      if (attempt === 0) {
        await page.waitForTimeout(6_000);
        continue;
      }
      throw err;
    }
  }
  throw new Error("Không mở được drawer xác nhận sau khi thêm vào giỏ.");
}

async function clearCartViaUi(page, canonicalOrigin) {
  await gotoPage(page, `${canonicalOrigin}/gio-hang`);
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

async function buildSingleSiteFlows({ browser, port, site }) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();
  const entry = siteOrigin(site, port);
  await loginViaBrowser(page, `${entry}/`, CREDS.buyer);
  const canonicalOrigin = new URL(page.url()).origin;

  let lastCreatedOrderCode = "";

  const flows = [
    {
      name: `site:${site}:catalog-filter-sort-page`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/san-pham`);
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });
        const countBefore = await page.getByRole("article").count();
        assert(countBefore > 0, `Site ${site} không có sản phẩm ở /san-pham`);

        // Sort
        const sortControl = page
          .getByLabel(/Sắp xếp/i)
          .or(page.getByRole("combobox", { name: /Sắp xếp/i }))
          .first();
        if (await sortControl.isVisible().catch(() => false)) {
          await chooseSelectOption(page, sortControl, 1);
          await page.waitForTimeout(450);
        }

        // Filter by keyword if present
        const kwInput = page
          .getByRole("main")
          .getByRole("searchbox", { name: /Lọc từ khóa/i })
          .or(
            page
              .getByRole("main")
              .getByRole("textbox", { name: /Lọc từ khóa/i }),
          )
          .first();
        if (await kwInput.isVisible().catch(() => false)) {
          await kwInput.fill("Áo");
          await page.waitForTimeout(450);
          await kwInput.fill("");
          await page.waitForTimeout(350);
        }

        // Pagination if more than 1 page
        const nextBtn = page
          .getByRole("button", { name: /Trang sau/i })
          .first();
        if (
          (await nextBtn.isVisible().catch(() => false)) &&
          (await nextBtn.isEnabled().catch(() => false))
        ) {
          await nextBtn.click();
          await page.waitForTimeout(400);
          const prevBtn = page
            .getByRole("button", { name: /Trang trước/i })
            .first();
          if (await prevBtn.isEnabled().catch(() => false)) {
            await prevBtn.click();
            await page.waitForTimeout(300);
          }
          return `${countBefore} sp, sắp xếp + lọc + phân trang`;
        }
        return `${countBefore} sp, sắp xếp + lọc (1 trang)`;
      },
    },
    {
      name: `site:${site}:search`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/tim-kiem`);
        await page
          .getByRole("heading", { level: 1, name: /Tìm kiếm/i })
          .waitFor({ state: "visible" });
        const main = page.getByRole("main");
        const input = main
          .getByRole("combobox", { name: /Tìm kiếm/i })
          .or(main.getByRole("searchbox"))
          .or(main.getByRole("textbox"))
          .first();
        await input.waitFor({ state: "visible", timeout: 8_000 });
        await input.fill("Áo");
        await main
          .getByRole("button", { name: /^Tìm kiếm$|^Tìm$/i })
          .first()
          .click();
        await page.waitForTimeout(800);
        const matches = await main.getByRole("article").count();
        assert(matches > 0, `Tìm "Áo" trên ${site} không trả về sản phẩm nào`);
        return `"Áo" -> ${matches} kết quả`;
      },
    },
    {
      name: `site:${site}:pdp-variant-add-drawer`,
      fn: async () => {
        await clearCartViaUi(page, canonicalOrigin);
        const drawer = await addRegularProductToCart(page, canonicalOrigin);
        await drawer
          .getByRole("link", { name: /Mở giỏ hàng|Xem giỏ hàng|Thanh toán/i })
          .or(
            drawer.getByRole("button", {
              name: /Mở giỏ hàng|Xem giỏ hàng|Thanh toán/i,
            }),
          )
          .first()
          .waitFor({ state: "visible", timeout: 5_000 });
        await page.keyboard.press("Escape");
        await page.waitForTimeout(250);
        return "chọn biến thể + thêm giỏ + drawer xác nhận";
      },
    },
    {
      name: `site:${site}:cart-qty-remove`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/gio-hang`);
        const findEnabledInc = async () => {
          const allInc = page.getByRole("button", { name: /Tăng số lượng/i });
          const n = await allInc.count();
          for (let i = 0; i < n; i++) {
            const b = allInc.nth(i);
            if (
              (await b.isVisible().catch(() => false)) &&
              (await b.isEnabled().catch(() => false))
            ) {
              return i;
            }
          }
          return -1;
        };

        let targetIdx = await findEnabledInc();
        if (targetIdx === -1) {
          // If cart is empty or existing item is already at stock cap, add a fresh multi-stock item.
          await clearCartViaUi(page, canonicalOrigin);
          await addRegularProductToCart(page, canonicalOrigin);
          await gotoPage(page, `${canonicalOrigin}/gio-hang`);
          targetIdx = await findEnabledInc();
        }
        if (targetIdx >= 0) {
          await page
            .getByRole("button", { name: /Tăng số lượng/i })
            .nth(targetIdx)
            .click();
          await page.waitForTimeout(600);
          await page
            .getByRole("button", { name: /Giảm số lượng/i })
            .nth(targetIdx)
            .click();
          await page.waitForTimeout(600);
        } else {
          // Fallback if only a 1-stock SKU exists: verify decrement when qty > 1, then remove.
          const decBtn = page
            .getByRole("button", { name: /Giảm số lượng/i })
            .first();
          if (await decBtn.isEnabled().catch(() => false)) {
            await decBtn.click();
            await page.waitForTimeout(600);
          }
        }

        const rmButtons = page.getByRole("button", { name: /^Xóa$|^Xoá$/i });
        const countBefore = await rmButtons.count();
        assert(countBefore > 0, "Không thấy nút Xóa dòng sản phẩm trong giỏ");
        await rmButtons.first().click();

        const deadline = Date.now() + 10_000;
        let removed = false;
        while (Date.now() < deadline) {
          const emptyVisible = await page
            .getByText(/Giỏ hàng đang trống|Chưa có sản phẩm/i)
            .first()
            .isVisible()
            .catch(() => false);
          const countAfter = await rmButtons.count().catch(() => countBefore);
          if (emptyVisible || countAfter < countBefore) {
            removed = true;
            break;
          }
          await page.waitForTimeout(250);
        }
        assert(
          removed,
          `Sau khi bấm Xóa số dòng vẫn là ${countBefore} và giỏ không trống`,
        );
        return "tăng -> giảm -> xoá dòng giỏ";
      },
    },
    {
      name: `site:${site}:checkout-cod`,
      fn: async () => {
        await clearCartViaUi(page, canonicalOrigin);
        await addRegularProductToCart(page, canonicalOrigin);
        await gotoPage(page, `${canonicalOrigin}/thanh-toan`);
        await page
          .getByRole("heading", { level: 1, name: /Thanh toán/i })
          .waitFor({ state: "visible" });

        // Ensure real 2-level address (province/ward) is selected or created
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
          const ward = page
            .getByLabel(/Chọn Phường hoặc Xã|Phường \/ Xã/i)
            .first();
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
        } else {
          // Verify real province/ward is present in the address stage
          const mainTxt = (await page.getByRole("main").textContent()) || "";
          assert(
            /Phường|Xã|Thành phố|Tỉnh|Hà Nội|Hồ Chí Minh|Đà Nẵng/i.test(
              mainTxt,
            ),
            "Địa chỉ thanh toán không thấy thông tin tỉnh/phường thật",
          );
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

        const placeOrderBtn = page
          .getByRole("button", { name: /^Đặt hàng/i })
          .first();
        await placeOrderBtn.waitFor({ state: "visible", timeout: 10_000 });
        assert(
          await placeOrderBtn.isEnabled(),
          `Nút Đặt hàng bị khóa trên ${site}`,
        );
        for (let attempt = 0; attempt < 2; attempt++) {
          await placeOrderBtn.click();
          try {
            await page.waitForURL(/\/don-hang/, { timeout: 20_000 });
            break;
          } catch (err) {
            const alertTxt =
              (await page
                .getByRole("alert")
                .first()
                .textContent()
                .catch(() => "")) || "";
            if (attempt === 0 && /quá nhanh|tạm bận|thử lại/i.test(alertTxt)) {
              await page.waitForTimeout(7_000);
              continue;
            }
            throw new Error(
              alertTxt
                ? `Đặt hàng báo lỗi: ${alertTxt.trim()}`
                : err?.message || String(err),
            );
          }
        }
        await page
          .waitForLoadState("networkidle", { timeout: 15_000 })
          .catch(() => {});
        await page.waitForTimeout(400);
        const pageTxt = (await page.getByRole("main").textContent()) || "";
        const m = pageTxt.match(/NM-\d{8}-[A-F0-9]{8}/);
        assert(
          m,
          "Sau khi đặt hàng COD không thấy mã đơn NM- trên trang đơn hàng",
        );
        lastCreatedOrderCode = m[0];
        return `đơn COD ${lastCreatedOrderCode}`;
      },
    },
    {
      name: `site:${site}:orders-list`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/don-hang`);
        await page
          .getByRole("heading", { level: 1, name: /Đơn hàng/i })
          .waitFor({ state: "visible" });
        const mainTxt = (await page.getByRole("main").textContent()) || "";
        if (lastCreatedOrderCode) {
          assert(
            mainTxt.includes(lastCreatedOrderCode),
            `Không thấy đơn mới ${lastCreatedOrderCode} trong /don-hang`,
          );
          return `thấy đơn ${lastCreatedOrderCode}`;
        }
        const m = mainTxt.match(/NM-\d{8}-[A-F0-9]{8}/);
        assert(m, `Không thấy đơn hàng nào trong /don-hang của ${site}`);
        lastCreatedOrderCode = m[0];
        return `thấy đơn ${lastCreatedOrderCode}`;
      },
    },
    {
      name: `site:${site}:order-lookup`,
      fn: async () => {
        if (!lastCreatedOrderCode) {
          await gotoPage(page, `${canonicalOrigin}/don-hang`);
          const txt = (await page.getByRole("main").textContent()) || "";
          const m = txt.match(/NM-\d{8}-[A-F0-9]{8}/);
          assert(m, `Không tìm được mã đơn thật để tra cứu trên ${site}`);
          lastCreatedOrderCode = m[0];
        }
        await gotoPage(page, `${canonicalOrigin}/tra-cuu-don-hang`);
        await page
          .getByRole("heading", { level: 1, name: /Tra cứu đơn/i })
          .waitFor({ state: "visible" });
        await page.getByLabel(/Mã đơn hàng/i).fill(lastCreatedOrderCode);
        await page.getByLabel(/Số điện thoại/i).fill(CREDS.buyer.phone);
        await page
          .getByRole("button", { name: /Tra cứu đơn hàng/i })
          .first()
          .click();
        await page
          .getByText(lastCreatedOrderCode)
          .first()
          .waitFor({ state: "visible", timeout: 12_000 });
        await page
          .getByText(
            /Chờ xác nhận|Đã xác nhận|Đang chuẩn bị|Đang giao|Đã giao|Hoàn tất|Đã hủy|Đã huỷ|Trả hàng/i,
          )
          .first()
          .waitFor({ state: "visible", timeout: 8_000 });
        return `tra cứu ${lastCreatedOrderCode}`;
      },
    },
    {
      name: `site:${site}:account-4-tabs`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/tai-khoan`);
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });
        for (const label of [
          /Hồ sơ/i,
          /Sổ địa chỉ/i,
          /Mã giảm giá/i,
          /Yêu thích/i,
        ]) {
          const tab = page.getByRole("tab", { name: label }).first();
          await tab.waitFor({ state: "visible", timeout: 8_000 });
          await tab.click();
          await page.waitForTimeout(300);
          assert(
            (await tab.getAttribute("aria-selected")) === "true",
            `Tab tài khoản ${label} chưa chọn`,
          );
        }
        return "4 tab tài khoản";
      },
    },
    {
      name: `site:${site}:voucher-flash-sale`,
      fn: async () => {
        await gotoPage(page, `${canonicalOrigin}/voucher`);
        await page
          .getByRole("heading", { name: /Mã giảm giá|Voucher/i })
          .or(page.getByText(/Mã giảm giá|Voucher/i))
          .first()
          .waitFor({ state: "visible", timeout: 10_000 });
        await gotoPage(page, `${canonicalOrigin}/flash-sale`);
        await page
          .getByRole("heading", { name: /Flash sale/i })
          .or(page.getByText(/Flash sale/i))
          .first()
          .waitFor({ state: "visible", timeout: 10_000 });
        return "/voucher + /flash-sale";
      },
    },
  ];

  return { flows, cleanup: () => ctx.close() };
}

export async function buildSiteFlows({ browser, port, sites }) {
  const targets = sites && sites.length > 0 ? sites : SEED_SITES;
  const allFlows = [];
  const cleanups = [];
  for (const site of targets) {
    const built = await buildSingleSiteFlows({ browser, port, site });
    allFlows.push(...built.flows);
    cleanups.push(built.cleanup);
  }
  return {
    flows: allFlows,
    cleanup: async () => {
      for (const fn of cleanups) {
        await fn().catch(() => {});
      }
    },
  };
}
