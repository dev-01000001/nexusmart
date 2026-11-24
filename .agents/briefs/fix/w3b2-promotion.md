# 3b-2 — Agent `promotion` (A7)

- **Sở hữu**: `services/promotion/**`. Đọc `w2-rules.md`, `w3b-plan.md` §4 (F9, F9-bis), `w3b-design-a7.md` §2.1, `docs/contracts.md` §4.11.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: không cần migration mới trong 3b-2.

## Phạm vi bắt buộc
1. **Thay kiểm tra `admin|super_admin` sang `auth.HasCapability` (`pkg/common/auth`, F9)**:
   - `ListAvailableVouchers` (khi `include_inactive=true` hoặc xem chế độ admin): `auth.CapVouchersRead` (`super_admin`, `admin`, `cskh`, `accountant`, `ops`).
   - `CreateVoucher` (`scope=PLATFORM|FREESHIP`), `UpdateVoucher`, `SetVoucherStatus`, `DeleteVoucher` (trên voucher nền tảng / quyền admin): `auth.CapVouchersWrite` (`super_admin`, `admin`, `ops`). Không phá quyền `seller` trên voucher `SHOP` của chính mình.
   - `ListAllFlashSales`, `ListFlashSaleItems` (admin view): `auth.CapFlashSalesRead` (`super_admin`, `admin`, `moderator`, `ops`). Giữ quyền `seller` xem danh sách flash sale / món của chính shop mình.
   - `CreateFlashSale`: `auth.CapFlashSalesWrite` (`super_admin`, `admin`, `ops`). Lưu ý: `moderator` KHÔNG được tạo campaign flash sale (`PermissionDenied`).
   - `ApproveFlashSaleItem`: `auth.CapFlashSalesItemApprove` (`super_admin`, `admin`, `moderator`, `ops`) — đúng ghi chú `‡` của ma trận a7 §2.1.
   - `CreditCoins`: chấp nhận `auth.HasCapability(claims, auth.CapCoinsCredit)` (`super_admin`, `admin`, `accountant` — F9-bis, không gồm `cskh`/`ops`) HOẶC internal service token (giữ nguyên đường `order` thưởng xu tự động; nhớ `HasCapability` tự từ chối service token nên kiểm service token riêng).
2. **Giữ nguyên các bất biến hiện có**: không đụng logic Redis Lua, Postgres `CommitReservation`, `ReservePromotion` (D3 chỉ `order-service`), và consumer `QueueSubscribe` (E1/E2).

## Tiêu chí nghiệm thu & Test bắt buộc (gồm ca tấn công)
- Table-driven RBAC test (F9): mọi RPC admin của `promotion` × 6 vai trò nội bộ + `buyer` + `seller` (dương & âm):
  - `moderator`: `ApproveFlashSaleItem` & `ListAllFlashSales`/`ListFlashSaleItems` → OK; `CreateFlashSale`, `CreateVoucher`, `CreditCoins` → `PermissionDenied`.
  - `cskh`: `ListAvailableVouchers` (admin) → OK; `CreditCoins`, `CreateVoucher`, `CreateFlashSale`, `ApproveFlashSaleItem` → `PermissionDenied` (F9-bis).
  - `accountant`: `ListAvailableVouchers` (admin), `CreditCoins` → OK; `CreateVoucher`, `CreateFlashSale`, `ApproveFlashSaleItem` → `PermissionDenied`.
  - `ops`: tất cả voucher/flash-sale admin → OK; `CreditCoins` → `PermissionDenied` (F9-bis).
  - Service token vẫn dùng được cho `CreditCoins`, `ReservePromotion`, `ReleasePromotion*`, và bị chặn trên mọi RPC `internalRPCs = false`.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `go test -race ./...` (Postgres thật qua `bash scripts/test-db.sh --run`, 0 SKIP) và test RBAC mới `-race -count=5`.
