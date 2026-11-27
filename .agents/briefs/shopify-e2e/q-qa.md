# Swarm Q — Test hành trình trọn vòng thành bộ kiểm cố định + chạy gate

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `tests/ui-flows/lib.mjs`, `tests/ui-flows/run.mjs`, `tests/ui-flows/templates.mjs`.
Swarm duy nhất được chạy `make e2e`, `make e2e-web` trong đợt này (manager báo khi được rebuild container).

## Việc 1 — `tests/ui-flows/journey.mjs`
Manager đã có kịch bản chạy tay `.runs/shopify-e2e/journey/journey.mjs` (14 bước, Chrome thật, có quay video + phụ đề): người bán đăng ký (OTP
Mailpit) → khai hồ sơ gian hàng → admin duyệt → đăng sản phẩm → admin duyệt → dựng + xuất bản site → khách đăng ký trên site, xác minh SĐT, mua
COD → người bán xác nhận + giao → webhook hãng VC (HMAC) → khách nhận + đánh giá → mua VietQR + webhook ngân hàng → giao → khách yêu cầu đổi trả →
người bán hoàn tiền ngay → (sau đợt 1) người bán khai TK ngân hàng + rút tiền → admin duyệt chi trả.
Chuyển thành flow cố định:
- Đăng ký vào `tests/ui-flows/run.mjs` dưới `--area journey`; dùng lại `lib.mjs` (`gotoPage`, `launchBrowser`, `MAILPIT_URL`…), locator chỉ
  role + chữ tiếng Việt (luật ở đầu `lib.mjs`).
- Mỗi bước đổi trạng thái phải soát bằng API gateway (shop ACTIVE, product APPROVED, order CONFIRMED/DELIVERED/COMPLETED, payment REFUNDED,
  số dư `seller/balance` trước/sau, payout COMPLETED, bút toán) — KHÔNG chỉ dò chữ trên trang (regex chữ từng khớp nhãn tab có sẵn ⇒ PASS giả).
- Không `.catch(() => {})` nuốt lỗi ở bước bắt buộc. Tài khoản/tên shop sinh mới mỗi lần chạy (hậu tố ngẫu nhiên).
- Tuỳ chọn `--video <dir>`: bật `recordVideo` + ghi `.srt` phụ đề từng bước như kịch bản gốc (giữ hàm `Seg`/`render.sh` của manager làm mẫu).
- Bỏ các chỗ kịch bản gốc phải vòng qua lỗi (F1 đăng nhập lại, xoá 3 SKU áo mẫu) SAU khi swarm W sửa xong; nếu lỗi còn thì flow phải FAIL rõ.

## Việc 2 — gate (khi manager báo stack đã dựng lại từ code mới)
`make e2e`, `make e2e-web`, `node tests/ui-flows/run.mjs --port 43000 --area journey` ×2 lần liên tiếp, và các area cũ `platform`, `seller`,
`admin`, `site` để bắt hồi quy. Test e2e-web/ui-flows hỏng do chữ UI đổi chủ đích (W) thì sửa test cho khớp, ghi rõ từng chỗ; hỏng do lỗi code thì
KHÔNG sửa code, báo `file:dòng` + kịch bản.

## Được sửa
`tests/ui-flows/**`, `tests/e2e-web/**`, `tests/e2e/**` (chỉ cập nhật test hỏng do thay đổi chủ đích + test mới của đợt), không sửa code sản phẩm.

## Báo cáo
Bảng: bộ test → số pass/fail/skip (dán dòng tổng thật), danh sách test đã sửa + lý do, lỗi code tìm thấy (`file:dòng`, cách tái hiện).
