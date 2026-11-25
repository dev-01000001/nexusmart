# Swarm D2 – Sửa lỗ hổng callback ZaloPay (2026-11-21)
Hợp đồng: `m-all-contract.md`. Báo cáo trước: `docs/reports/d-order-depth.md`.

## Sở hữu
`services/payment/`, `deploy/helm/payment/`. CẤM mọi thư mục khác (A, E, H, G2, K đang chạy).

## Lỗi (High, Claude Code review)
- `deploy/helm/payment/values-production.yaml` không đặt `ZALOPAY_MODE`, nên `services/payment/src/config.rs:182` mặc định `mock`.
- Ở mock, `services/payment/src/zalopay.rs:306-317` dùng key2 là hằng trong repo (`zalopay-mock-key2-dev`) hoặc placeholder từ `.env.example`.
- `/webhooks/zalopay` công khai qua gateway (`handlers_d.go:17`). Kịch bản: người mua tạo đơn, ký callback `{data:{app_trans_id, zp_trans_id, amount=<đúng số tiền>, server_time=now, embed_data:{payment_id}}}` bằng key công khai → đơn được đánh dấu đã thanh toán mà không trả tiền.

## Yêu cầu
1. Không bao giờ chấp nhận callback ký bằng key nằm trong repo (hằng mock, placeholder theo `is_placeholder_secret`) khi chạy cấu hình production. Chọn cách đơn giản nhất, ví dụ: production values đặt `ZALOPAY_MODE=sandbox` và payment từ chối khởi động nếu môi trường production mà mode/key là mock hoặc placeholder; hoặc route callback trả 503 ở mock trừ khi bật cờ dev tường minh chỉ có trong compose/e2e.
2. Kiểm tra cùng mẫu lỗi ở VietQR webhook và webhook chung (`webhook_security.rs`); sửa nếu có.
3. Compose, e2e và mock hiện có vẫn chạy (không phá `tests/e2e`).
4. Test: callback ký bằng `zalopay-mock-key2-dev` bị từ chối ở cấu hình production; cấu hình dev vẫn nhận.

## Gate
`cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`, `cargo test`, `helm lint deploy/helm/payment` và `helm template ... -f values-production.yaml | kubeconform -strict -ignore-missing-schemas`. Ghi `docs/reports/d2-payment-fix.md` (lệnh + đầu ra thật).
