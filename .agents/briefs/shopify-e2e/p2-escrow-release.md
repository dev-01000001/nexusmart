# Swarm P2 — Giải ngân sớm cho admin + 2 sửa nhỏ sau nghiệm thu đợt 1

Đọc trước: `.agents/briefs/shopify-e2e/common.md`. Phần P/M1 đã commit ở `3ecdb871`. Swarm M2 đang sửa `web/src/components/app/**`,
`web/src/features/seller/ProductFormView*`, `web/src/features/seller/site/**`, `web/src/app/api/seller/media/**`, `web/src/app/media/**`,
`web/src/middleware.ts`, `web/src/lib/site/**`: KHÔNG đụng các file đó.

## Việc 1 — Admin giải ngân sớm (đã có RPC `PaymentService.ReleaseEscrow`, capability `escrow.release`, chưa có REST/UI)
Vì sao: tiền đơn hoàn tất bị giữ `PAYMENT_SELLER_HOLD_HOURS` (72 h) rồi mới vào `seller_available`; kế toán không có cách nào trên web để giải
ngân sớm một đơn (chỉ gọi được gRPC, như `tests/e2e/29_seller_payouts_test.go:118`). `release_escrow` (`services/payment/src/service.rs` ~4600)
đã xử lý đủ: từ chối khi allocation còn `held`/`awaiting_collection` (Conflict), bỏ qua nếu đã `released`, chặn khi có refund chờ duyệt.
- REST: `POST /api/v1/admin/orders/{id}/escrow/release` (`escrow.release` = super_admin, admin, accountant; dùng
  `middleware.RequireCapability(auth.CapEscrowRelease)` hoặc tên hằng hiện có), body rỗng → gọi `ReleaseEscrow{order_id: id}` bằng JWT admin →
  `200 {"released":bool,"released_amount_vnd":int,"already_released":bool}`; `400` id không phải UUID; `409 FAILED_PRECONDITION` khi đơn chưa
  hoàn tất (map từ Conflict hiện có); ghi audit như các route admin tiền khác (`middleware/audit.go`). Test route + quyền (moderator/cskh/ops bị
  403, buyer/seller 403).
- BFF: `web/src/app/api/admin/orders/[id]/escrow-release/route.ts` (POST, theo mẫu `web/src/app/api/admin/payouts/[id]/decision/route.ts`).
- UI `/admin/doi-soat`: thẻ "Giải ngân sớm" (chỉ hiện với vai trò có `escrow.release` theo `web/src/lib/admin-rbac.ts`): ô "Mã đơn hàng" (UUID),
  nút "Giải ngân" → AlertDialog "Xác nhận giải ngân sớm" (nói rõ tiền chuyển ngay vào số dư khả dụng của người bán, không chờ hết thời gian
  giữ) → toast "Đã giải ngân <số tiền>₫" / "Đơn này đã được giải ngân trước đó" / lỗi tiếng Việt cho 409 ("Đơn chưa hoàn tất nên chưa thể giải
  ngân"). Đặt cạnh tab "Chi trả người bán" bạn đã làm. Nhãn ổn định để test bám: tiêu đề "Giải ngân sớm", ô có label "Mã đơn hàng", nút "Giải ngân".
- `docs/contracts.md`: thêm dòng route vào bảng admin payment.

## Việc 2 — Upload ảnh treo 33 s rồi đóng kết nối không trả lỗi
Hiện tại khi S3 lỗi, `pkg/common/storage/s3.go` thử 3 lần × `ResponseHeaderTimeout` 10 s (≈33 s) > `WriteTimeout` 30 s của gateway
(`services/gateway/cmd/gateway/main.go:165`) ⇒ client nhận "Empty reply", không có 503 JSON. Sửa: đặt deadline cho mỗi lời gọi storage trong
handler media (`services/gateway/internal/router/handlers_media.go`, ví dụ `context.WithTimeout(r.Context(), 15*time.Second)` cho Put/Head/Get)
để luôn trả `503` JSON trong thời hạn. Test: storage giả treo → handler trả 503 trước deadline.

## Việc 3 — Tài liệu cũ
`docs/contracts.md` dòng ~174 và ~187 còn ghi `payout_locked_until` "currently informational / not enforced anywhere yet because no seller payout
API exists" — nay `RequestSellerPayout` đã kiểm (412 `BANK_ACCOUNT_NOT_READY`). Sửa câu cho đúng.

## Được sửa
`services/gateway/**`, `pkg/common/storage/**`, `docs/contracts.md`, `web/src/app/api/admin/orders/**`, `web/src/app/(admin)/admin/doi-soat/**`,
`web/src/features/admin/AdminReconciliation*`, `web/src/lib/admin-rbac.ts` (chỉ nếu thiếu hằng), test tương ứng. Không sửa `services/payment/**`
(trừ khi phát hiện ReleaseEscrow có lỗi — dừng và báo).

## Kiểm (phải xanh, dán kết quả)
`cd services/gateway && go test -race ./...`, `cd pkg/common && go test -race ./...`, `gofmt -l`, `golangci-lint run ./...` (gateway, pkg/common);
`cd web && npm run lint && npm run format:check && npm run test:unit` (KHÔNG build web: M2 đang build).
