# 3b-2 — Agent `shipping` (A7)

- **Sở hữu**: `services/shipping/**`. Đọc `w2-rules.md`, `w3b-plan.md` §4 (F9), `w3b-design-a7.md` §2.1, `docs/contracts.md` §4.11.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: không cần migration mới trong 3b-2.

## Phạm vi bắt buộc
1. **Thay kiểm tra `admin|super_admin` sang `auth.HasCapability` (`pkg/common/auth`, F9)**:
   - `AdminListCarriers`: `auth.CapCarriersRead` (`super_admin`, `admin`, `ops`).
   - `AdminUpdateCarrier`, `AdminTestCarrier`: `auth.CapCarriersWrite` (`super_admin`, `admin`, `ops`).
   - `GetShipment` & HTTP `/labels/{tracking_code}` (nhánh admin read): cho phép vai trò có `auth.CapOrdersRead` hoặc `auth.CapCarriersRead` (`super_admin`, `admin`, `cskh`, `accountant`, `ops`), từ chối `moderator` (trừ khi là chủ đơn/chủ shop — không xảy ra với nhân viên). Giữ nguyên nhánh `buyer` / `seller` / service token (`GetShipment` thôi; `/labels` vẫn cấm service token).
   - `GetShopShippingSettings` (nhánh full-settings admin): cho phép `auth.CapCarriersRead` hoặc `auth.CapShopsRead` (`super_admin`, `admin`, `ops` — hoặc giữ nguyên nhánh full settings cho `carriers.read` + `shops.review` + service token/chủ shop).
   - `CancelShipment`, `CreateReturnShipment`: giữ service token + `auth.CapOrdersReviewResolve` (`super_admin`, `admin`, `ops`).
2. **Không phá hành vi 3b-1**: giữ nguyên 3.321 phường (E15/E16), cache phí theo `cod_amount_vnd` (E17), job thử lại vận đơn (E18), GHN thiếu mã phường (0 request, fallback).

## Tiêu chí nghiệm thu & Test bắt buộc (gồm ca tấn công)
- Table-driven RBAC test (F9): mọi RPC admin của `shipping` × 6 vai trò nội bộ + `buyer` + `seller` (dương & âm):
  - `super_admin`, `admin`, `ops`: `AdminListCarriers`, `AdminUpdateCarrier`, `AdminTestCarrier` → OK.
  - `moderator`, `cskh`, `accountant`, `buyer`, `seller`: cả 3 RPC `Admin*Carrier` → `PermissionDenied`.
  - `cskh`, `accountant`, `ops`, `admin`, `super_admin`: `GetShipment` (admin view) → OK; `moderator` không sở hữu đơn → `PermissionDenied`.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `go test -race ./...` (Postgres thật qua `bash scripts/test-db.sh --run`, 0 SKIP) và test RBAC mới `-race -count=5`.
