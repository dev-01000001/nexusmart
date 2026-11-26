# S9c — QA đợt 3: 8 mẫu site, mỗi mẫu đi trọn luồng COD (tiêu chí 6 của spec §15)

Đọc trước: `common.md`, spec §15 mục 6, `tests/ui-flows/lib.mjs` (S9 đã viết).
Mọi khu đã commit. Bản build production của HEAD chạy ở cổng **43120** (`next start`, nối gateway audit 48080). Nếu 43120
không trả 200 thì dùng dev 43110.

## Mục tiêu
Lưu thử **từng mẫu trong 8 mẫu** từ Site Studio, rồi với mỗi mẫu đi trọn luồng trên trình duyệt thật:
xem trang chủ site → trang sản phẩm → thêm vào giỏ → thanh toán COD → thấy trang xác nhận đơn.

## Dữ liệu dùng
- Người bán: `seller-catalog@nexusmart.local` (mật khẩu như các seller seed, xem `tools/seed/seed.mjs` `SELLER_DEMO_PASSWORD`),
  shop "Kho Đại Lý NexusMart", **chưa có site xuất bản**. Chỉ dùng shop này.
- **Cấm** đổi mẫu, nháp hay trạng thái xuất bản của 4 site seed (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`).
- Người mua: `buyer@nexusmart.local` (SĐT đã xác minh, đủ điều kiện COD).
- Shop chưa có site: tạo site qua đúng luồng UI của người bán (Site Studio / onboarding). Nếu cần quản trị duyệt (sản phẩm, site,
  tên miền phụ) thì dùng `admin@nexusmart.local` qua UI quản trị. Ghi lại từng bước đã làm.
- Shop thiếu sản phẩm còn hàng thì tạo một sản phẩm thử qua UI người bán (giá số nguyên VND), ghi lại.

## Phạm vi ghi
- `tests/ui-flows/templates.mjs` (luồng mới, dùng lại `lib.mjs`; thêm `--area templates` vào `run.mjs` nếu gọn), ảnh và báo cáo trong
  `.runs/shadcn-redesign/p1-4/s9/templates/`.
- Cấm sửa mã sản phẩm (`web/src/**`), test `web/scripts`, file khác của `tests/ui-flows/` trừ phần đăng ký area trong `run.mjs`.

## Cách chạy
- Mọi lần mở trình duyệt bọc `flock /tmp/nm-web-browser.lock`, chạy trong `systemd-run --user --scope --slice=nm3b.slice -p MemoryMax=2G`.
- Gateway có giới hạn tần suất: gặp 429 hoặc trang "Bạn thao tác hơi nhanh" thì chờ 60 giây rồi thử lại, không tính là lỗi mẫu.
- Mỗi mẫu: chụp 390 và 1440 cho trang chủ, PDP, giỏ, thanh toán, xác nhận đơn; ghi lỗi console, pageerror, tràn ngang.

## Kết thúc
- Sau khi chạy xong 8 mẫu, đưa shop catalog về **không có site xuất bản** nếu UI cho phép (gỡ xuất bản). Không làm được thì ghi rõ
  trạng thái để lại.
- Đơn COD tạo ra: để nguyên, liệt kê mã đơn.

## Xong khi
- Bảng 8 dòng: mẫu, lưu được (có/không), mã đơn COD, số lỗi console, ảnh. Mẫu nào hỏng thì ghi
  `LỖI THẬT: <mẫu> — <bước> — <thông báo lỗi / ảnh> — <file mã nghi ngờ:dòng>` và đi tiếp mẫu sau, không sửa mã.
- `node tests/ui-flows/run.mjs --port 43120 --area templates` chạy lại được (nếu đã đăng ký area).
- Báo cáo theo `common.md` §5.
