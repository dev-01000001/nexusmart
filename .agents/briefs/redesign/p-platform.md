# P – Trang nền tảng (root host)
Đọc `design.md`, `f-handoff.md`, `.taste-skill/redesign/platform/direction.json` (bố cục "product performs"; bỏ phần font/màu của nó).

## Sở hữu
`web/src/features/platform/**`, `web/src/app/(shop)/**` (trừ `template.tsx`), `web/src/app/not-found.tsx`. Form đăng nhập/đăng ký nằm ở `features/account/AuthForms*` (mảng C sở hữu): P chỉ dựng khung trang quanh form.

## Trang chủ: 6 mục, tổng ≤ 130 từ hiển thị (không kể tên/tagline cửa hàng, câu trả lời FAQ đang đóng)
1. **Hero = cánh cửa.** Trái trên: h1 ≤ 2 dòng, giọng `wide`, cỡ 5–6.5vw, gợi ý "Cửa hàng mang tên bạn" (được chọn câu hay hơn, ≤ 24 ký tự mỗi dòng). Một câu ≤ 12 từ. Ô nhập gắn liền địa chỉ: `[tên thương hiệu].nexusmartai.store` + nút "Mở gian hàng" (dùng lại logic `SubdomainPreview`/`subdomain.ts`, rootDomain từ prop, trạng thái hợp lệ/không báo gọn một dòng, `aria-live`). Phần còn lại của màn đầu là **cửa hàng sống** dựng bằng HTML (đánh dấu `data-ui`), tối thiểu 55% bề rộng desktop, sát mép phải và đáy: thanh địa chỉ đổi theo ô nhập, tên thương hiệu đổi theo, và cứ 4s (hoặc bấm các chấm màu) chuyển giữa 4 bộ mặt thật lấy từ `PLATFORM_TEMPLATES` (bảng màu + kiểu bố cục: sport, maison, atelier, editorial) bằng chuyển động `clip-path` + đổi màu sân khấu. Dùng ảnh `public/models|banners`. KHÔNG giá tiền, KHÔNG link sản phẩm. Dừng tự chạy khi hover/focus/reduced-motion, có nút tạm dừng.
2. **Danh bạ = dãy cánh cửa.** 4–6 cửa hàng đầu là các panel dọc cao ~78svh chạm mép hai bên, hover/focus mở rộng (grid-template-columns 700ms expo), tên cửa hàng giọng `tight` lớn, địa chỉ nhỏ, cả panel là link. Cửa hàng còn lại: danh sách chữ lớn từng hàng, hover hiện ảnh bìa neo theo hàng. Mobile: dải vuốt ngang snap. Ít hơn 4 cửa hàng vẫn phải đầy hàng. Giữ SSR, phân trang, trạng thái rỗng/lỗi. Không tagline dài (1 dòng, cắt).
3. **Câu chuyện giữ chân (pinned), 3 bước, mỗi bước một câu ≤ 5 từ**: chọn giao diện / đăng sản phẩm / nhận đơn. Bên phải một mảnh giao diện thật biến đổi theo bước (trình chọn mẫu; hàng sản phẩm kéo vào lưới; thẻ "Đơn mới" + mã VietQR vẽ khung, không số tiền). Sticky, ≤ 3 màn; reduced-motion/mobile xếp dọc.
4. **Video khung cửi** mở từ khung nhỏ ra toàn màn hình theo cuộn (`clip-path` + `view()`), một câu ≤ 6 từ ở trên.
5. **FAQ** 5 câu, ô đặc màu, `<details name>`, câu trả lời ≤ 25 từ. Tiêu đề sticky trái.
6. **CTA câu điền khuyết**: "Mở [tên thương hiệu] hôm nay." ô nhập nằm trong câu, chữ cuối là nút; rồi **footer** gọn + chữ NexusMart giọng `wide` chạy hết bề ngang.
Header: wordmark, 3 link, đăng nhập, một nút; trong suốt trên hero, ẩn khi cuộn xuống, hiện khi cuộn lên; mobile là sheet toàn màn mở bằng clip-path.

## Trang khác
- `/ve-nexusmart`: trang tuyên ngôn: 4 màn, mỗi màn 1 câu lớn + 1 ảnh lớn, bố cục mỗi màn khác nhau. ≤ 120 từ.
- `/tro-giup`: ô tìm câu hỏi lớn ở đầu lọc tức thì, nhóm câu hỏi thành tab, accordion. Không đoạn dẫn.
- `/dang-nhap`, `/dang-ky`: chia đôi: nửa ảnh/sân khấu màu đổi theo vai trò (mua/bán), nửa form tối giản; chuyển bước OTP trượt ngang.
- 404: chữ lớn giọng `poster`, một nút.

## Nghiệm thu
Bất biến mục 7 `design.md` (trang gốc) đạt: `curl` HTML `/` kiểm không giá, không link cấm. Lighthouse không bắt buộc nhưng CLS < 0.05, ảnh hero `priority`, video `preload="none|metadata"` + poster. Ảnh chụp 390/768/1440 mọi trang.
