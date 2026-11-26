# S3b — Trang chủ nền tảng: chuyển sang Tailwind, giữ y hình (phase 3, đợt 2)

Đọc trước: `common.md` (cả §1 "Import từng file, không qua barrel"), spec §1 dòng "Không thiết kế lại trang chủ nền tảng",
§8.4 (dòng `PlatformHeader`/`PlatformFooter` và trang chủ), §15.
Mục tiêu: trang chủ `/` của host gốc **giống hệt** bản đang chạy ở cổng 43101 (commit `c265058`), nhưng không còn CSS Module.
Không thiết kế lại, không đổi bố cục, chữ, màu, gradient, chuyển động.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(shop)/(public)/layout.tsx`, `web/src/app/(shop)/(public)/page.tsx`
- `web/src/features/platform/`:
  - `PlatformHome.tsx` + `.module.css`, `PlatformGlobe.tsx` + `.module.css`;
  - `SiteDirectorySection.tsx` + `.module.css`, `SiteDirectoryDoors.tsx`;
  - `StorefrontStageMini.tsx` + `.module.css`, `SubdomainPreview.tsx` + `.module.css`.
- File mới chỉ trong thư mục con mới `web/src/features/platform/home/`.
- **Chỉ đọc:** `directory.server.ts`, `directoryModel.ts`, `globeData.ts`, `globeRenderer.ts`, `legacy*.ts`,
  `showcaseTemplates.ts`, `subdomain.ts`, `VietQrSvg.tsx`, `PlatformHeader.tsx`, `PlatformFooter.tsx` (đã chuyển ở đợt 1),
  mọi file trong `web/public/` (sprite SVG của bức tường công cụ giữ nguyên).

## Ngoại lệ chỉ cho cây trang chủ (vì spec buộc giữ đúng màu và chuyển động)
- Được dùng giá trị tuỳ ý của Tailwind để chép **đúng** giá trị đang có: `bg-[#…]`, `bg-[linear-gradient(…)]`,
  `shadow-[…]`, `text-[clamp(…)]`, `[mask-image:…]`… Không làm tròn, không đổi sang token gần giống.
- Biến CSS đang có của trang chủ (`--home-*` hoặc tương tự) được giữ, đặt bằng `style` trên phần tử gốc như
  `(public)/layout.tsx` đang làm với `--home-font`.
- **Keyframes:** gom đúng các `@keyframes` đang có (khoảng 7 cái) vào **một** file mới
  `web/src/features/platform/home/home-keyframes.css`, chỉ import ở `(public)/layout.tsx`. Đổi tên có tiền tố `nm-home-`.
  File này chỉ chứa `@keyframes`, không có selector nào. Dùng qua `animate-[nm-home-…_…]`.
- Không bọc trang chủ trong `UiScope`: lớp reset của nó sẽ đổi pixel. Tailwind đã có từ `@/styles/ui.css` ở
  `(shop)/layout.tsx`, giống cách đợt 1 làm header/footer.
- Selector phức tạp (`:global`, `nth-child`, `::before`…): dùng biến thể Tailwind (`before:`, `[&>li:nth-child(2)]:`,
  `group-hover:`…). Nếu thật sự không diễn đạt được, ghi `CẦN NGOẠI LỆ: <selector> — lý do` vào progress và dừng ở chỗ đó để
  Claude quyết, không tự thêm CSS.

## Giữ nguyên hành vi
- Phân trang danh bạ `?trang=`, ô xem trước tên miền phụ, mọi liên kết CTA, tab/carousel đang có, quả cầu (canvas,
  `globeRenderer.ts`), nạp lazy đang có.
- SEO, `metadata`, `dynamic = "force-dynamic"`.
- Chuyển động khi không bật reduce-motion vẫn chạy như cũ; khi bật reduce-motion vẫn tắt như cũ.

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng; 5 file `.module.css` của cây trang chủ đã xoá.
- `rg -n 'from "@/components/app"|from "@/features/platform"' <phạm vi>` rỗng (import thẳng file).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi; `platform.test.mjs` xanh (assertion bám CSS Module cũ: viết lại theo
  bất biến, ghi `TEST VIẾT LẠI`; test trong `web/scripts` vỡ: ghi `TEST VỠ`, không sửa).
- **So pixel** (bắt buộc), trang `/` trên 43110 so với 43101:
  - Chromium, `reducedMotion: "reduce"`, `caret: "initial"`, ảnh **viewport** (không `fullPage`), khổ 390 / 768 / 1440,
    cao 900;
  - chụp ở mọi vị trí cuộn `0, 900, 1800, …` đến cuối trang (đợi 1,5 giây sau mỗi lần cuộn);
  - so bằng `ffmpeg … blend=all_mode=difference,signalstats` (`YAVG`). Che vùng 72×72 góc dưới trái ở ảnh 43110 (huy
    hiệu dev của Next) trước khi so.
  - Mục tiêu `YAVG = 0` ở mọi ảnh. Khác 0 thì sửa. Chỉ được chấp nhận khi đã thử mà không hết, kèm ảnh vùng khác và lý
    do; Claude quyết.
  - Icon ☰ ở đầu trang 390/768 đã khác từ đợt 1 (lucide), không tính.
- Thêm một lần không bật reduce-motion: chụp 3 khung cách nhau 0,5 giây ở đầu trang 1440 trên cả hai cổng, tự xem chuyển
  động có giống nhau không.
- Luồng: `tests/ui-flows/run.mjs --port 43110 --area platform` 5/5; tự bấm phân trang danh bạ, ô tên miền phụ, các CTA.
- Báo cáo theo `common.md` §5, kèm bảng `YAVG` từng ảnh.
- Claude đo First Load JS của `/` trên bản build: không vượt 143 kB (mốc 124 kB + 15%).
