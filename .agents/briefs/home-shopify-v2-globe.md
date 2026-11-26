# Brief: quả địa cầu phát sáng cho trang chủ (bổ sung vòng 2)

Ngày: 2026-11-26. Người giao: Claude Code (manager). Coder: agy, một phiên riêng, chạy **song song** với phiên agy đang làm `.agents/briefs/home-shopify-v2.md`.

## 1. Mục tiêu

Chủ dự án gửi ảnh khối "Rock steady. Blazing fast." của shopify.com/vn và nhắn: *"tôi thấy phần này khá ấn tượng, bạn clone lại được thì tốt"*.

Ảnh tham chiếu:
- `.agents/briefs/home-shopify-v2/ref/user-globe.png`: ảnh chủ dự án chụp;
- `shopify-1440-09.jpg`, `shopify-390-09.jpg` cùng thư mục.

Hãy mở xem trước khi code.

Nhiệm vụ: dựng **component hình quả địa cầu** giống Shopify về phong cách:
- nền vũ trụ tối có sao lấp lánh;
- quả cầu xanh navy/teal, lục địa vẽ bằng chấm lấm tấm, viền khí quyển phát sáng;
- đèn đô thị màu cam/vàng;
- các vòng cung trắng phát sáng chạy qua lại giữa các điểm;
- một vòng cung lớn vút lên khỏi quả cầu.

Phiên agy khác sẽ đặt component này vào trang, ở khối S11.

## 2. Ranh giới với phiên song song (bắt buộc)

**Chỉ được tạo, sửa** các đường dẫn sau:
- `web/src/features/platform/PlatformGlobe.tsx` (mới)
- `web/src/features/platform/PlatformGlobe.module.css` (mới)
- `web/src/features/platform/globeRenderer.ts` (mới): code vẽ thuần, không React
- `web/src/features/platform/globeData.ts` (mới): danh sách thành phố, vòng cung
- `web/public/globe/**` (mới): dữ liệu chấm lục địa
- `tools/globe-dots/**` (mới): script sinh dữ liệu + `README.md` ngắn ghi nguồn và giấy phép
- `.agents/briefs/home-shopify-v2/ref/agy-globe/**`: trang thử, ảnh tự chụp (đã gitignore)

**Cấm sửa mọi file khác**, đặc biệt:
- `PlatformHome.*`, `tokens.css`, `PlatformHeader.*`, `PlatformFooter.*`, `SiteDirectory*`, `StorefrontStageMini.*`, `showcaseTemplates.ts`: phiên kia đang sửa;
- `package.json`, `postcss.config.mjs`, `web/CLAUDE.md`, `.claude/`, `docs/`: một phiên khác đang sửa.

Không checkout, restore, stash, clean hay xoá file nào mình không tự tạo. Không chạy `npm install`.

Màu trong canvas đặt làm hằng số ở `globeRenderer.ts`. Màu trong `PlatformGlobe.module.css` dùng token `--home-*` mà phiên kia đang thêm vào `tokens.css`:
- `--home-band`, `--home-navy`, `--home-pine`, `--home-teal-deep`, `--home-ink`, `--home-ink-2`, `--home-ink-3`, `--home-accent`, `--home-glow`;
- `--home-radius-card`, `--home-radius-media`.

Nếu thiếu token nào thì ghi vào báo cáo, không tự thêm vào `tokens.css`.

## 3. Điều cấm (trung thực, bản quyền, bản đồ)

- **Không vẽ biên giới quốc gia, không ghi tên nước hay địa danh** trên quả cầu. Chỉ vẽ chấm lục địa.
- Dữ liệu lục địa lấy từ **Natural Earth 110m land**, thuộc phạm vi công cộng. Tải `ne_110m_land.geojson` từ kho `nvkelso/natural-earth-vector` trên GitHub hoặc từ naturalearthdata.com. Ở mức 110m không có đảo nhỏ; không tự vẽ thêm hay bớt đảo nào.
- Không dùng texture, ảnh hay mã của Shopify. Tự vẽ bằng Canvas 2D.
- Hai thẻ chữ nhỏ trên hình chỉ nêu sự thật, **không số liệu sử dụng** (không "hàng triệu người bán", không "175 quốc gia"):
  - thẻ 1: dòng trên "34 TỈNH THÀNH", dòng dưới "TÍNH PHÍ GIAO HÀNG THEO TỪNG NƠI";
  - thẻ 2: dòng trên "COD VÀ VIETQR", dòng dưới "CÓ SẴN TRÊN MỌI CỬA HÀNG".
  - Kiểu chữ như Shopify: chữ hoa nhỏ 11–12px, dòng trên trắng, dòng dưới `--home-accent`, có icon nhỏ từ `@/components/Icons` (`IconTruck`, `IconWallet`). Nền thẻ tối trong suốt, viền mảnh, bo `--home-radius-card`.

## 4. Thiết kế hình

- **Bố cục.** Component chiếm khung do cha quyết định, rộng 100% và có `aspect-ratio`. Gợi ý: desktop cỡ 760×720, mobile vuông.
  - Quả cầu đường kính khoảng 78% chiều rộng khung, nằm lệch trái như Shopify.
  - Thẻ 1 ở mép trái, ngang giữa quả cầu. Thẻ 2 ở dưới bên phải quả cầu.
- **Góc nhìn.** Chiếu trực giao (orthographic), tâm quả cầu đặt ở khoảng vĩ độ 14°N, kinh độ 106°E, để Việt Nam nằm gần giữa và hơi lệch lên trên.
  - Có thể nghiêng nhẹ trục cho giống Shopify.
  - Không cần xoay liên tục. Được phép trôi rất chậm ±3° theo kinh độ, nhưng chỉ khi việc vẽ lại phần nền vẫn mượt.
- **Đại dương.** Đĩa gradient từ navy đậm ở rìa tối sang teal tối ở phía được chiếu sáng. Rìa có quầng khí quyển teal mờ (radial gradient ra ngoài đĩa).
- **Lục địa.** Chấm 1–1.6px, xanh lá/teal nhạt, lấm tấm như ảnh. Chấm mờ dần về rìa đĩa theo cosin góc nhìn. Mặt tối hơi tối hơn.
- **Đèn đô thị.**
  - Khoảng 120–200 thành phố lớn trên thế giới: chấm cam mờ, nhỏ.
  - Thủ phủ của 34 tỉnh thành Việt Nam (theo đơn vị hành chính từ 2025): chấm cam/vàng sáng hơn, có quầng.
  - Đèn nhấp nháy rất nhẹ.
- **Vòng cung.**
  - Khoảng 10–16 vòng cung trắng phát sáng, nối giữa các thành phố Việt Nam (Hà Nội, Hải Phòng, Huế, Đà Nẵng, Khánh Hòa, Lâm Đồng, TP.HCM, Cần Thơ…).
  - Độ cao vòng cung phóng đại để thấy rõ trên quả cầu: đỉnh cao khoảng 0.25–0.4 lần độ dài dây cung.
  - Mỗi vòng cung chạy theo chu kỳ: vẽ dần từ đầu tới cuối, đầu sáng kéo đuôi, rồi mờ đi; các vòng lệch pha nhau.
  - Thêm **một vòng cung lớn** xuất phát từ TP.HCM vút lên ra ngoài rìa trên của quả cầu, như vòng cung trên cùng trong ảnh Shopify.
- **Sao.** Khoảng 60–90 chấm trắng nhỏ trên toàn khung, vài chấm to hơn có quầng, nhấp nháy chậm. Làm bằng CSS (phần tử tuyệt đối hoặc nền) hay vẽ chung trong canvas đều được.
- **Chất lượng.** Đặt `devicePixelRatio` (tối đa 2) để không mờ trên màn retina.

## 5. Kỹ thuật

- `PlatformGlobe.tsx` là client component (`"use client"`), **không thêm thư viện** (không three.js, không d3).
- Không làm nặng First Load JS của `/`:
  - phần vẽ nằm trong `globeRenderer.ts`;
  - `PlatformGlobe` chỉ bắt đầu vẽ khi khung gần vào màn hình (`IntersectionObserver`, `rootMargin` khoảng 200px);
  - dữ liệu chấm lục địa nằm ở `web/public/globe/land-dots.json` (hoặc định dạng gọn hơn), chỉ `fetch` lúc bắt đầu vẽ;
  - phần JS thêm vào First Load phải dưới khoảng 4 kB gzip; có thể tách bằng `import()` động `globeRenderer` khi vào màn hình.
- Dữ liệu:
  - `tools/globe-dots/build.mjs` (Node 24, không dependency) đọc file GeoJSON truyền vào, lấy mẫu lưới đều theo diện tích (khoảng 1.2–1.6°, thưa dần theo cos vĩ độ), kiểm điểm trong đa giác bằng ray casting, ghi danh sách [vĩ độ, kinh độ] làm tròn 1 chữ số thập phân;
  - file kết quả dưới khoảng 40 kB khi gzip;
  - không commit file GeoJSON gốc, tải về thư mục tạm.
- Vẽ hiệu quả:
  - vẽ phần nền tĩnh (đại dương, lục địa, khí quyển) **một lần** vào canvas phụ, rồi mỗi khung hình chỉ chồng đèn và vòng cung lên;
  - dùng `requestAnimationFrame`, dừng khi khung ra khỏi màn hình hoặc tab ẩn (`document.hidden`);
  - xử lý resize bằng `ResizeObserver`.
- `prefers-reduced-motion: reduce`: vẽ một khung tĩnh, gồm vòng cung ở trạng thái đầy đủ và mờ vừa, không chạy vòng lặp.
- **Trước khi JS chạy, hoặc khi fetch lỗi:** hiện một quả cầu bằng CSS (radial-gradient navy/teal cộng quầng khí quyển) cùng kích thước, để không nhảy bố cục và vẫn đẹp khi không có dữ liệu.
- A11y:
  - canvas và các lớp trang trí mang `aria-hidden="true"`;
  - hai thẻ chữ là nội dung thật, để dạng danh sách `<ul>`, đọc được.
- API của component: `export function PlatformGlobe({ className }: { className?: string })`. Không prop nào khác trừ khi thật cần.
- Nếu dùng `mask-image` hay `backdrop-filter` thì viết kèm bản `-webkit-`, vì repo vừa thêm `postcss.config.mjs` và Next sẽ không tự thêm tiền tố nữa.

## 6. Tự kiểm

- Repo đang bị phiên khác sửa dở, nên **không chạy `npm run build`**. Kiểm như sau:
  - `cd web && npx eslint src/features/platform/PlatformGlobe.tsx src/features/platform/globeRenderer.ts src/features/platform/globeData.ts`
  - `npx prettier --check` trên đúng các file mới;
  - `npx tsc --noEmit -p .`: chỉ cần **không có lỗi trong file của bạn**; lỗi ở file khác thì ghi vào báo cáo.
- So ảnh bằng mắt:
  - dựng một trang thử HTML tĩnh trong `.agents/briefs/home-shopify-v2/ref/agy-globe/`, chạy `globeRenderer` (biên dịch file TS sang JS bằng `npx tsc` hoặc `npx esbuild` nếu có sẵn trong `node_modules`, ra thư mục đó) với dữ liệu thật, nền `rgb(3 30 28)`;
  - chụp bằng `node .agents/briefs/home-shopify-v2/ref/shot.cjs file://<đường-dẫn-trang> <tiền-tố> 1440`, và một lần ở 390;
  - đặt cạnh `user-globe.png`, sửa tới khi gần giống: lục địa lấm tấm, đèn cam, vòng cung sáng, khí quyển phát sáng. Việt Nam phải nhận ra được ở gần giữa.
- Không `make *`, không docker, không commit, không push.

## 7. Báo cáo khi xong

- File đã tạo.
- Dung lượng `land-dots` (thô và gzip).
- Ước lượng phần JS thêm vào First Load.
- Đường dẫn ảnh tự chụp.
- Cách phiên kia nên đặt component vào trang: kích thước, nền dải, chỗ đặt chữ.
- Chỗ lệch brief.
