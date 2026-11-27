# P5b — Xoá component cũ, CSS Module còn lại, trang `/design`; toast theo theme site

Đọc trước: `common.md` (phase 5). Các file dưới đây không còn trang nào dùng sau phase 1–4 (Claude đã `rg`), nên xoá không đổi hình.
Vẫn phải tự chứng minh bằng `rg` trước khi xoá từng file.

## Việc
1. **Component cũ** trong `web/src/components/`: `Button`, `Price`, `ProductCard`, `StateView`, `SvgChart` (`.tsx` + `.module.css`),
   `modalFocus.ts` + `modalFocusUtils.ts` + `modalFocus.test.mjs` (chỉ file sắp xoá dùng). Barrel `web/src/components/index.ts`: bỏ export
   đã xoá; nếu sau đó không còn ai import `@/components` thì xoá luôn barrel. **Giữ:** `Icons.tsx`, `Reveal.tsx`, `revealUtils*`,
   `PageTransition.tsx`, `SessionProvider.tsx`, `app/`, `ui/`.
2. **Primitive cũ của khu:** `features/admin/{AdminUi.tsx,StatusPill.tsx,adminShared.module.css}`,
   `features/seller/{SellerUiPrimitives.tsx,SellerCommon.module.css}`.
3. **CSS Module không còn ai import:** `features/account/AuthForms.module.css`, `features/commerce/commerce.module.css`,
   `features/site/site.module.css`, `features/site/siteTemplates.module.css`. Sau bước này `git ls-files 'web/**/*.module.css'` phải rỗng.
4. **Trang duyệt `/design`:** xoá `web/src/app/(design)/**`; bỏ `"/design"` khỏi `ROOT_PROTECTED_PREFIXES` ở `web/src/lib/site/host.ts:346`
   (sửa test middleware/host nếu có bám). Kiểm `/design` trả 404 trên 43110.
5. **Mã chỉ phục vụ file đã xoá:** `rg` tìm export chỉ còn được import bởi file vừa xoá (helper trong `lib/`, kiểu, hằng). Xoá nếu chắc chắn,
   liệt kê từng file trong báo cáo. Không chắc thì để lại và ghi.
6. **Toast trên site mẫu tối:** `web/src/features/site/shell/SiteToasterInner.tsx` đang hiện toast nền trắng trên mẫu tối
   (`sport-active`, `luxury-maison`; xem `.runs/shadcn-redesign/p1-4/s9/templates/luxury-maison/order-confirm-390.png`). Cho Sonner theo
   theme của site (`data-site-theme-mode` trên gốc site do `SiteThemeRoot.tsx` gắn; đọc được từ context hoặc DOM), dùng màu `--ui-*` của
   site. Không sửa `SiteThemeRoot.tsx` (của P5a); cần thêm gì ở đó thì ghi `CẦN P5A: …` vào progress.

## Test được sửa (luật trong `common.md` §1)
- `web/scripts/audit-fixes.test.mjs:885` (ProductCard), `web/scripts/w2b-web.test.mjs:153-155` (Price, ProductCard),
  `web/scripts/w3a-web.test.mjs:1285` (Price), `web/scripts/w3b-web.test.mjs:136` (ProductCard) và `:917-920` (`adminShared.module.css`):
  tìm bất biến mà đoạn đó bảo vệ (vd. giá gốc gạch ngang, nhãn giảm giá, ô chọn rộng 100% trong panel chi tiết) và kiểm nó trên component
  mới đang làm việc đó (`components/app/Money`, `features/site/SiteProductCard`, trang admin tương ứng…).
- `web/src/components/app/navMatch.test.mjs:34`: chỉ sửa chú thích nhắc tới file `(design)` đã xoá.
- **Không sửa** `web/src/styles/ui-vars.test.mjs` (của P5a). Nó đọc `site.module.css`, `siteTemplates.module.css` và `(design)/design/layout.tsx`
  nên sẽ đỏ sau khi bạn xoá: ghi `TEST VỠ (P5a): ui-vars.test.mjs:<dòng>` vào progress, P5a viết lại.

## Phạm vi ghi
Chỉ các file liệt kê ở trên (xoá hoặc sửa). **Cấm** mọi file trong phạm vi P5a (`p5a-global-css.md`), `tests/**`.

## Xong khi
- `git ls-files 'web/**/*.module.css'` rỗng; `rg -n 'from "@/components"$|components/(Button|Price|ProductCard|StateView|SvgChart)"' web/src` rỗng;
  `/design` 404.
- `tsc` 0 lỗi toàn `web/src` (bỏ qua `.next*/types`), `npm run lint` 0, `npm run test:unit` chỉ còn đỏ ở `ui-vars.test.mjs` (P5a).
- So pixel (`common.md` §3) cho admin, seller, site `bach-dang` + `sai-gon`: không có loại (c). Ảnh toast trên `sai-gon` (thêm vào giỏ) trước/sau.
- Ghi `.runs/shadcn-redesign/p5/signals/p5b.done` (một dòng: đã xoá gì), rồi báo cáo theo `common.md` §4.
