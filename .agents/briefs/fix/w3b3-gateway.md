# 3b-3 — Agent `gateway` (A8 + B1: G1–G14)

- **Sở hữu**: `services/gateway/**`. Đọc `w2-rules.md`, `w3b-plan.md` §5 (**G1–G14**), `w3b-design-a8.md` §2.2, `w3b-design-b1.md` §2, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, `docker-compose.yml`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.

## Phạm vi bắt buộc
1. **Config & gRPC Client (`internal/config`, `internal/clients`)**: thêm `SEARCH_GRPC_ADDR` (`search:50059`), `SEARCH_FALLBACK_TIMEOUT_MS` (1500), `RATE_LIMIT_ORDER_LOOKUP_PER_MIN` (10), `RATE_LIMIT_ORDER_LOOKUP_CODE_PER_10MIN` (5), `PRODUCT_IMPORT_MAX_ROWS` (1000), `PRODUCT_IMPORT_MAX_BYTES` (1048576); dial `searchv1.NewSearchServiceClient` (dial lười không chặn khởi động nếu `search` chưa sẵn).
2. **Mã lỗi chuẩn (`internal/httpx/response.go`)**: bổ sung trích `error.code` chính xác khớp `docs/contracts.md` §7: `400` (`IMPORT_INVALID_HEADER`, `IMPORT_TOO_MANY_ROWS`, `IMPORT_FILE_TOO_LARGE`, `IMPORT_INVALID_UTF8`), `404` (`ORDER_LOOKUP_NOT_FOUND`), `409` (`ADDRESS_UPDATE_NOT_ALLOWED`, `ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS`, `ADDRESS_UPDATE_CARRIER_PICKED_UP`, `ADDRESS_FEE_INCREASE_PREPAID`, `ADDRESS_FEE_CONFIRMATION_REQUIRED`, `ADDRESS_FEE_CHANGED`, `COD_ALREADY_COLLECTED`, `IMPORT_JOB_NOT_READY`, `REINDEX_ALREADY_RUNNING`). Riêng `ADDRESS_FEE_CHANGED` trả HTTP `409` kèm cả `error` lẫn object `quote` (`old_shipping_fee_vnd`, `new_shipping_fee_vnd`, `fee_delta_vnd`, `new_total_payable_vnd`) từ báo giá mới (G2).
3. **Route A8 (`router.go`, `handlers_order.go`, `handlers_product.go`, `proxy.go`, `ratelimit.go`)**:
   - `POST /api/v1/orders/lookup` (Public): giới hạn **kép** trên Redis `10/phút/IP` (`rl:order_lookup:ip:<ip>`) VÀ `5/10 phút/order_code` (`rl:order_lookup:code:<normalized_code>`) → `429` + `Retry-After` (G7).
   - `PATCH /api/v1/orders/{id}/address` (`authed`): gọi `OrderService.UpdateOrderAddress`, chuyển tiếp JWT + `Idempotency-Key` (G1–G3).
   - Mở rộng `GET /api/v1/seller/orders`: chuyển tiếp `statuses`, `carrier_code`, `payment_method`, `keyword`, `due_before`, `include_status_counts` và trả thêm `status_counts` (G8).
   - `POST /api/v1/seller/orders/batch-ship` & `POST /api/v1/seller/orders/picking-list` (`seller`, G4).
   - `POST /api/v1/shipping/labels/batch` (`seller` hoặc nhân viên có `orders.read`/`carriers.read`): proxy qua `ShippingLabelProxy` tới `shipping:8087/labels/batch`, gỡ mọi header `X-User-*`, giữ `Authorization`, truyền nguyên luồng `application/pdf` (G5).
   - `GET /api/v1/seller/stock-history` & `GET /api/v1/seller/skus/{sku_id}/stock-history` (`seller`, G8).
   - CSV import (`seller`, G6): `GET /api/v1/seller/products/import/template` (UTF-8 BOM CSV), `POST /api/v1/seller/products/import` (`202`, nhận `text/csv` hoặc `multipart/form-data`, chặn thân > `PRODUCT_IMPORT_MAX_BYTES`), `GET .../import/{job_id}`, `GET .../import/{job_id}/rows`, `GET .../import/{job_id}/errors.csv` (UTF-8 BOM, chặn công thức CSV bằng tiền tố `'`), `POST .../import/{job_id}/confirm`.
4. **Route B1 (`handlers_search.go`, `router.go`)**:
   - `GET /api/v1/search` (`opt` — chuyển tiếp JWT nếu có để lưu lịch sử G14): gọi `SearchService.SearchProducts` với hạn `SEARCH_FALLBACK_TIMEOUT_MS` (1,5 s); nếu lỗi `Unavailable`/`DeadlineExceeded`/timeout hoặc `SearchClient == nil` → tự gọi `ProductService.ListProducts` bằng `PublicOutgoingContext` và trả cùng envelope với `degraded_mode: "product_fallback"` (G13; tuyệt đối không trả `commission_rate_bps`).
   - `GET /api/v1/search/suggest` (Public; lỗi `SearchService` → lùi về `ProductService.ListProducts(page_size=5)`).
   - `GET /api/v1/search/history`, `DELETE /api/v1/search/history`, `DELETE /api/v1/search/history/{id}` (`authed`, G14).
   - `POST /api/v1/admin/search/reindex` (`202`, `RequireCapability(CapSearchReindex)` + `AdminAudit`) và `GET /api/v1/admin/search/reindex/{job_id}` (`RequireCapability(CapSearchReindex)`).

## Tiêu chí đo được & Ca tấn công/đua bắt buộc
- Tấn công G7: lần thứ 11 cùng IP trong 1 phút hoặc lần thứ 6 cùng `order_code` từ IP khác trong 10 phút → `429 RATE_LIMIT_EXCEEDED` + `Retry-After`.
- Tấn công G6: `errors.csv` thêm tiền tố `'` cho `=CMD|...`, `+1+1`, `-2+3`, `@SUM`, `\t=1`, ` =1`; upload > 1 MiB bị chặn sạch.
- Tấn công G5 & RBAC: `POST /shipping/labels/batch` gỡ `X-User-Role`/`X-Shop-Id` giả mạo; ma trận 6 vai trò × `/admin/search/reindex` (`super_admin`/`admin`/`ops` → 202/200; `moderator`/`cskh`/`accountant`/buyer/seller → 403).
- Fallback G13: fake `SearchService` trả `Unavailable` hoặc ngủ 2 s → `GET /api/v1/search` vẫn trả `200` trong < 1,6 s với `degraded_mode: "product_fallback"` và không lộ `commission_rate_bps`.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `go test -race ./...` (gồm `b1_search_routes_test.go` và `a8_routes_test.go`).
- `search` tắt lúc gateway khởi động: gateway vẫn khoẻ. Từ khoá tìm kiếm không vào access log, span, audit, kể cả khi lỗi (G14); có test.
