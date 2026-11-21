# Swarm D – Order, Payment, Shipping, Promotion làm sâu (`order-depth`)

- **Ngày hoàn thành:** `2026-11-21`
- **Hợp đồng tham chiếu:** `.agents/briefs/m-d-order-depth.md`, `.agents/briefs/m-all-contract.md`, `docs/spec.md` (hàng *"Luồng mua hàng"*, *"Thanh toán"*, *"Vận chuyển"*, *"Kiểm thử"*), `AGENTS.md`
- **Phạm vi sở hữu đã cập nhật:**
  - `proto/nexusmart/{order,payment,shipping}/v1/*.proto` + mã sinh tương ứng trong `gen/go/nexusmart/{order,payment,shipping}/v1/`
  - `services/order/`
  - `services/payment/`
  - `services/shipping/`
  - `services/promotion/`
  - File dùng chung (chỉ thêm mới): `services/gateway/internal/router/router.go` (4 dòng mount 1 dòng), `services/gateway/internal/router/handlers_d.go`, `services/gateway/internal/router/router_d_test.go`, bổ sung case enum/trường mới ở `handlers_order.go`, `handlers_payment.go`, `handlers_shipping.go`, và biến giả ZaloPay/GHTK trong `.env.example`.

---

## 1. Tóm tắt hạng mục đã triển khai

### 1.1 Trả hàng hoàn tiền & Khiếu nại (`services/order`, `services/gateway`, `proto`)
- **Máy trạng thái đơn hàng mở rộng** (`services/order/internal/statemachine/statemachine.go`):
  - Bổ sung đầy đủ các trạng thái mới `RETURN_APPROVED`, `RETURN_REJECTED`, `RETURNING`, `REFUNDED` cùng phương thức thanh toán `ZALOPAY`.
  - Luồng chuyển trạng thái chặt chẽ:
    - `DELIVERED -> RETURN_REQUESTED`
    - `RETURN_REQUESTED -> RETURN_APPROVED | RETURN_REJECTED`
    - `RETURN_REJECTED -> RETURN_APPROVED` (khi admin phân xử khiếu nại chấp thuận hoàn/đổi)
    - `RETURN_APPROVED -> RETURNING | REFUNDED` (hoàn ngay hoặc qua vận đơn hoàn)
    - `RETURNING -> RETURNED`
    - `RETURNED -> REFUNDED`
- **CSDL & Persistence** (`services/order/migrations/0004_returns_and_complaints.sql`, `queries/orders.sql`, `internal/db/`, `internal/store/`):
  - Mở rộng bảng `return_requests` (danh sách `items_json`, `refund_amount_vnd`, `seller_note`, `reverse_tracking_code`, `reverse_label_url`, `refund_id`, `updated_at`) và tạo bảng `complaints` (`return_request_id`, `order_id`, `buyer_id`, `shop_id`, `reason`, `evidence_urls_json`, `status`, `resolution`, `admin_note`, `resolved_by`, `created_at`, `resolved_at`).
  - Sinh mã `sqlc` và triển khai đầy đủ trên cả `PostgresStore` lẫn `MemoryStore`.
- **Saga Hoàn tiền & Khiếu nại** (`services/order/internal/service/returns.go`, `lifecycle.go`, `checkout.go`, `handlers.go`):
  - `RequestReturn`: kiểm tra quyền sở hữu buyer, cửa sổ 7 ngày từ `delivered_at`, hợp lệ số lượng từng item theo đơn, phát outbox `order.return_requested`.
  - `ApproveReturn`: seller sở hữu gian hàng (hoặc admin) duyệt hoàn tiền ngay (`immediate_refund=true`) hoặc tạo vận đơn hoàn qua `ShippingService.CreateReturnShipment` (`RETURNING`), phát outbox `order.return_approved`.
  - `ConfirmReturnReceived` & `HandleShipmentUpdated`: khi người bán xác nhận đã nhận hàng hoàn hoặc nhận sự kiện `shipment.updated` với `direction=return` và trạng thái `returned`, chạy **Refund Saga idempotent**:
    1. Gọi `PaymentService.CreateRefund` (ghi sổ cái ghi kép, idempotency key `return-refund:<return_request_id>`).
    2. Hoàn voucher/xu: gọi `PromotionService.ReleasePromotion` khi toàn bộ đơn trong `checkout_group_id` đã đóng, đồng thời phát outbox `order.refunded` chứa `coins_discount_vnd` và `all_group_orders_closed` để `promotion` thu hồi xu thưởng và hoàn xu tỷ lệ idempotent.
    3. Cập nhật nguyên tử đơn -> `REFUNDED`, yêu cầu trả hàng -> `REFUNDED`, ghi `order_status_history` và outbox `order.refunded`.
  - `RejectReturn` -> tự động leo thang tạo khiếu nại `OPEN` cho admin (`UC-S10` -> `UC-A05`); hỗ trợ thêm `CreateComplaint` để người mua bổ sung lý do/bằng chứng idempotent.
  - `ListComplaints` & `ResolveComplaint`: chỉ admin/super_admin, hỗ trợ quyết định `REFUND` (có hoặc không kèm vận đơn hoàn), `REPLACE`, `DISMISS`.
- **Gateway REST routes** (`services/gateway/internal/router/handlers_d.go`):
  - `POST /api/v1/orders/{id}/returns`, `GET /api/v1/orders/{id}/returns`, `POST /api/v1/orders/{id}/complaints`
  - `GET /api/v1/seller/returns`, `POST /api/v1/seller/returns/{id}/approve`, `POST /api/v1/seller/returns/{id}/reject`, `POST /api/v1/seller/returns/{id}/confirm-received`
  - `GET /api/v1/admin/complaints`, `POST /api/v1/admin/complaints/{id}/resolve` (chấp nhận cả `resolution` lẫn alias `decision` từ Web BFF theo `QA-02`: `REFUND_BUYER`, `PARTIAL_REFUND`, `APPROVE_RETURN`, `REJECT_COMPLAINT`)
  - `GET /api/v1/admin/payments/reconciliation-reports`
  - `POST /webhooks/zalopay`

### 1.2 Payment Rust (`services/payment`)
- **Adapter ví sandbox thứ ba — ZaloPay v2** (`src/zalopay.rs`, `migrations/0003_zalopay.sql`, `src/config.rs`, `src/service.rs`, `src/http.rs`, `src/grpc.rs`):
  - Hai chế độ: `ZALOPAY_MODE=mock` (mặc định, tự fallback sang mock nếu `ZALOPAY_KEY1`/`ZALOPAY_KEY2` trống hoặc là placeholder) và `ZALOPAY_MODE=sandbox` gọi `/v2/create`, `/v2/query`, `/v2/refund` qua HTTP có timeout, retry backoff, circuit breaker.
  - Endpoint `POST /webhooks/zalopay`: xác thực constant-time `HMAC-SHA256(key2, data)`, chống replay nghiêm ngặt theo `server_time`/`X-Timestamp` (cửa sổ 300s), idempotent theo `provider_tx_id` (gửi lặp 10 lần chỉ ghi sổ cái đúng 1 lần).
  - Hỗ trợ hết hạn (`scan_expired_payments`), hủy thanh toán (`cancel_payment`), hoàn tiền trước và sau đối soát (`build_settled_refund_tx` trong `src/ledger.rs` đảm bảo cân bằng Nợ = Có trên mọi tài khoản).
- **Đối soát hằng ngày & Báo cáo bền vững** (`src/service.rs`, `src/workers.rs`, `src/grpc.rs`, `src/http.rs`):
  - `run_reconciliation`: gộp feed VietQR + ZaloPay, phân loại `matched`, `missing_local`, `missing_provider`, `amount_mismatch`, tự động auto-heal giao dịch đã thanh toán ở phía nhà cung cấp nhưng mất webhook (`auto_healed_count`), lưu bảng `reconciliation_runs` & `reconciliation_items`.
  - `ListReconciliationReports` (gRPC + HTTP `/internal/reconciliation/reports`, chỉ admin).
- **Số dư người bán (`GetSellerBalance`)**:
  - Tính chính xác `pending_vnd`, `available_vnd`, `total_earned_vnd` từ sổ cái ghi kép, bao gồm bút toán đảo khi hoàn tiền đơn đã đối soát/giải ngân.

### 1.3 Shipping Go (`services/shipping`)
- **Adapter GHTK bên cạnh GHN và MockExpress** (`internal/carrier/ghtk.go`, `internal/config/config.go`, `cmd/server/main.go`):
  - Triển khai chung interface `Carrier` (`Quote`, `CreateOrder`, `CancelOrder`, `TrackOrder`) bọc `NewResilientCarrier` (timeout, retry backoff, circuit breaker).
  - Tự động dùng chế độ mock khi không có `GHTK_API_TOKEN` thật; tạo mã vận đơn `GHTK-<10HEX>`, tính phí tuyến `ROAD`/`XFAST`, chuẩn hóa mã trạng thái số và chuỗi của GHTK; hỗ trợ webhook chuẩn GHTK tại `internal/webhook/handler.go`.
- **Bảng mapping địa chỉ cũ (63 tỉnh, 3 cấp) sang mới (34 tỉnh, 2 cấp)** (`internal/address/data.go`):
  - Hỗ trợ tra cứu trực tiếp và fallback thông minh (chuẩn hóa viết tắt `TP.`, `Q.`, `P.`, `TX.`, `H.`, tỉnh sáp nhập như Bình Dương -> TP.HCM mã `79`, v.v.).
- **Vận đơn hoàn (`CreateReturnShipment`) & Migration** (`migrations/000002_return_shipments.sql`, `internal/service/`, `internal/store/`, `internal/grpcserver/`):
  - Thêm cột `direction` (`forward`/`return`), `return_request_id`, ràng buộc duy nhất `(order_id, direction)`.
  - `CreateReturnShipment` idempotent theo `order_id`, `COD = 0`, đổi chiều địa chỉ người mua -> kho người bán; `GetShipment` hỗ trợ tham số `direction`.
- **Job polling dự phòng webhook** (`internal/poller/`):
  - Kiểm thử đầy đủ các kịch bản cập nhật trạng thái khi webhook bị rớt, bỏ qua vận đơn kết thúc, và chống trùng lặp outbox (`coverage 92.9%`).

### 1.4 Promotion Go (`services/promotion`)
- **Consumer bền vững `order.refunded`** (`internal/events/consumer.go`, `internal/store/`, `internal/service/promotion.go`, `cmd/server/main.go`):
  - Đăng ký consumer bền vững `promotion-order-refunded` cùng giao dịch nguyên tử `processed_events` + sổ cái xu (`coin_transactions`).
  - Thu hồi xu thưởng `order_completed_reward` khi đơn bị hoàn tiền (chặn không âm số dư), hoàn xu theo tỷ lệ `coins_discount_vnd` với hoàn từng phần trong nhóm nhiều gian hàng, hoặc giải phóng toàn bộ reservation voucher/xu/flash-sale khi `all_group_orders_closed = true`.
  - Quá 5 lần giao thất bại định tuyến sang `dlq.order.refunded` kèm `traceparent`.
  - Giữ nguyên toàn bộ ràng buộc bảo mật trong `grpc_trust_test.go`.

### 1.5 Contract Test gRPC & Khắc phục liên swarm (`issues.md`)
- **Contract Test gRPC mức wire** (`services/order/internal/service/contract_grpc_test.go`):
  - Chạy qua `bufconn` + `auth.GRPCUnaryServerInterceptor` thật xác minh wire contract hai chiều `Order <-> Payment`, `Order <-> Shipping` (`CreateReturnShipment` chuyển tiếp JWT seller/admin), và `Order <-> Promotion` (`ReservePromotion` / `ReleasePromotion`).
- **Đã xử lý toàn bộ lỗi ghi nhận cho Swarm D trong `docs/reports/issues.md`**:
  - `QA-02`: `handlers_d.go` chấp nhận alias `decision` (`REFUND_BUYER`, `PARTIAL_REFUND`, `APPROVE_RETURN`, `REJECT_COMPLAINT`) cùng `resolution`, có unit test tại `router_d_test.go`.
  - `PLT-03` & `PLT-04`: sửa `services/{order,shipping,promotion}/Dockerfile` bỏ hardcode `GOARCH=amd64`, bỏ `chown` lên binary thực thi, chạy bằng UID số `USER 10001:10001` (đồng nhất với `services/payment/Dockerfile`).
  - `OBS-02`, `OBS-04`, `OBS-09`, `OBS-10`, `OBS-14` (`services/payment`): bổ sung W3C OTel Extractor/Injector cho HTTP/gRPC/NATS/DLQ + `span.set_parent` + trả `X-Trace-Id`, gắn `service="payment"` và `trace_id`, bổ sung `process_cpu_seconds_total`/`process_resident_memory_bytes`, chỉ đếm HTTP 5xx ở `http_errors_total`, dùng full method name cho gRPC RED metrics, thêm bucket `10.0`, che credential trong `nats_url` và che hoàn toàn `mask_secret`.
  - `OBS-05` (`promotion`), `OBS-06` (`shipping`), `OBS-09`/`OBS-12`/`OBS-13` (`order`): đăng ký `ProcessCollector` & `GoCollector` cho `order`, đổi fatal log sang JSON logger, tạo `SpanKindProducer`/`SpanKindConsumer` và truyền `traceparent` sang DLQ và `*Context` logs.

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1 Protobuf (`buf lint` & scoped `buf generate`)
```text
buf lint --path proto/nexusmart/order --path proto/nexusmart/payment --path proto/nexusmart/shipping --path proto/nexusmart/promotion
-> exit 0 (0 lint issues)
```

### 2.2 Go Services (`gofmt`, `golangci-lint`, `go build`, `go test -race -cover`)
Chạy cho `order`, `shipping`, `promotion`, `gateway`:
```text
--- services/order ---
golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...   -> 0 issues
go build ./...                                                            -> exit 0
go test -race -count=1 -coverprofile=/tmp/d-order.cov ./...:
  ok  github.com/nexusmart/core/services/order/internal/clients        1.168s  coverage: 79.8% of statements
  ok  github.com/nexusmart/core/services/order/internal/config         1.010s  coverage: 100.0% of statements
  ok  github.com/nexusmart/core/services/order/internal/db             1.013s  coverage: 88.3% of statements
  ok  github.com/nexusmart/core/services/order/internal/events         1.049s  coverage: 65.3% of statements
  ok  github.com/nexusmart/core/services/order/internal/grpcserver     1.041s  coverage: 100.0% of statements
  ok  github.com/nexusmart/core/services/order/internal/service        1.258s  coverage: 86.6% of statements
  ok  github.com/nexusmart/core/services/order/internal/statemachine   1.025s  coverage: 100.0% of statements
  ok  github.com/nexusmart/core/services/order/internal/store          1.033s  coverage: 41.0% of statements
  >>> order TOTAL coverage:
  total:  (statements)  65.9%

--- services/shipping ---
golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...   -> 0 issues
go build ./...                                                            -> exit 0
go test -race -count=1 -coverprofile=/tmp/d-shipping.cov ./...:
  ok  github.com/nexusmart/core/services/shipping/internal/address     1.403s  coverage: 95.5% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/cache       1.017s  coverage: 91.2% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/carrier     1.216s  coverage: 77.2% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/config      1.013s  coverage: 100.0% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/events      1.050s  coverage: 75.2% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/grpcserver  1.036s  coverage: 91.5% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/poller      1.099s  coverage: 92.9% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/service     1.033s  coverage: 79.9% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/store       1.014s  coverage: 2.6% of statements
  ok  github.com/nexusmart/core/services/shipping/internal/webhook     1.020s  coverage: 92.8% of statements
  >>> shipping TOTAL coverage:
  total:  (statements)  62.9%

--- services/promotion ---
golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...   -> 0 issues
go build ./...                                                            -> exit 0
go test -race -count=1 -coverprofile=/tmp/d-promotion.cov ./...:
  ok  github.com/nexusmart/core/services/promotion/cmd/server          1.021s  coverage: 23.8% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/calculator 1.017s  coverage: 97.2% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/config     1.010s  coverage: 100.0% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/events     1.041s  coverage: 80.1% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/lua        1.916s  coverage: 92.5% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/service    1.730s  coverage: 88.0% of statements
  ok  github.com/nexusmart/core/services/promotion/internal/store      1.020s  coverage: 44.9% of statements
  >>> promotion TOTAL coverage:
  total:  (statements)  68.5%

--- services/gateway ---
golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...   -> 0 issues
go build ./...                                                            -> exit 0
go test -race -count=1 ./...:
  ok  github.com/nexusmart/core/services/gateway/internal/httpx        1.015s
  ok  github.com/nexusmart/core/services/gateway/internal/middleware   1.016s
  ok  github.com/nexusmart/core/services/gateway/internal/proxy        1.015s
  ok  github.com/nexusmart/core/services/gateway/internal/router       1.135s
```

### 2.3 Rust Payment (`cargo fmt`, `cargo clippy`, Postgres 17 integration tests, `llvm-cov`)
Chạy với container PostgreSQL 17 tạm (`d-test-final-pg`, đã xóa sạch khi kết thúc):
```text
cd services/payment && cargo fmt --all -- --check                         -> exit 0
cd services/payment && cargo clippy --all-targets -- -D warnings          -> Finished dev profile (0 warnings)

SQL migrations trên PostgreSQL 17 thật:
  order migrations (4 files: 0001..0004) applied cleanly
  shipping migrations (2 files: 000001..000002) applied cleanly
  promotion migrations (services/promotion/internal/store/migrations/001..002) applied cleanly (xác minh lượt trước)

RUSTFLAGS="-C instrument-coverage" PAYMENT_TEST_DATABASE_URL="postgres://nexusmart:***@127.0.0.1:<port>/postgres" cargo test --quiet:
  unit tests (nexusmart-payment):               57 passed; 0 failed
  sales_channel_db_integration:                  1 passed; 0 failed (4.82s)
  zalopay_and_lifecycle_db_integration:          1 passed; 0 failed (40.17s)

llvm-cov report (services/payment/src/*.rs):
  Filename                      Regions    Missed     Cover   Functions  Missed  Executed     Lines    Missed     Cover
  ---------------------------------------------------------------------------------------------------------------------
  auth.rs                           438        21    95.21%          30       2    93.33%       230         5    97.83%
  config.rs                         397        14    96.47%          24       7    70.83%       219         7    96.80%
  domain.rs                         665        18    97.29%          36       2    94.44%       478         6    98.74%
  error.rs                          156         0   100.00%           6       0   100.00%       161         0   100.00%
  grpc.rs                          1558       105    93.26%          80      15    81.25%      1298        44    96.61%
  http.rs                          1087        95    91.26%          53       8    84.91%       777        52    93.31%
  ledger.rs                        1452        53    96.35%          49       5    89.80%      1167        22    98.11%
  main.rs                           181       181     0.00%          11      11     0.00%        98        98     0.00%
  metrics.rs                        412        15    96.36%          21       1    95.24%       233         3    98.71%
  service.rs                       2560       771    69.88%          85      12    85.88%      2302       613    73.37%
  telemetry.rs                      473       151    68.08%          42      12    71.43%       254        90    64.57%
  vietqr.rs                         826        60    92.74%          64       7    89.06%       579        39    93.26%
  webhook_security.rs               450        44    90.22%          31       5    83.87%       281        35    87.54%
  workers.rs                        568       470    17.25%          27      17    37.04%       351       291    17.09%
  zalopay.rs                       1312        84    93.60%          80      11    86.25%       906        72    92.05%
  ---------------------------------------------------------------------------------------------------------------------
  TOTAL                           12535      2082    83.39%         639     115    82.00%      9334      1377    85.25%
```

### 2.4 Kiểm chứng Docker Multi-stage & Non-root
```text
docker build -q -t d-build-order -f services/order/Dockerfile .         -> built OK (USER=10001:10001)
docker build -q -t d-build-shipping -f services/shipping/Dockerfile .   -> built OK (USER=10001:10001)
docker build -q -t d-build-promotion -f services/promotion/Dockerfile . -> built OK (USER=10001:10001)
services/payment/Dockerfile                                             -> multi-stage, USER 10001:10001
```
Toàn bộ container tạm (`d-test-*`), image tạm (`d-build-*`) và file tạm (`/tmp/d-*`) đã được dọn sạch sau khi kiểm tra.

---

## 3. Đối chiếu tiêu chí đo trong Spec & Brief

| Tiêu chí (`m-d-order-depth.md` & `docs/spec.md`) | Kết quả | Bằng chứng / Ghi chú |
|---|---|---|
| Trả hàng hoàn tiền: trạng thái mới (`RETURN_REQUESTED`, `RETURN_APPROVED`, `RETURN_REJECTED`, `RETURNING`, `RETURNED`, `REFUNDED`), Refund Saga (Payment sổ cái kép, Promotion hoàn voucher/xu, Shipping tạo vận đơn hoàn), khiếu nại lên admin khi seller từ chối | **Đạt** | `services/order/internal/statemachine/`, `internal/service/returns.go`, `migrations/0004_returns_and_complaints.sql`, `services/promotion/internal/events/consumer.go`, `services/shipping/migrations/000002_return_shipments.sql`, `services/gateway/internal/router/handlers_d.go` |
| Outbox events mới `order.return_requested`, `order.return_approved`, `order.refunded` + REST endpoints chuẩn hợp đồng | **Đạt** | Kiểm thử unit + contract + gateway router (`router_d_test.go`), căn chỉnh `QA-02` với Web BFF |
| Payment: adapter ZaloPay sandbox/mock, IPN/callback HMAC chống replay & idempotent 10x, job đối soát hằng ngày ra báo cáo, số dư người bán (`pending_vnd`, `available_vnd`) cân bằng sổ cái kép | **Đạt** | `services/payment/src/{zalopay,ledger,service,http,grpc}.rs`, 57 unit tests + 2 Postgres 17 DB integration suites pass |
| Shipping: adapter GHTK (mock khi không có key) bên cạnh GHN trên interface `Carrier` có timeout/retry/circuit breaker; bảng mapping địa chỉ cũ -> mới; job polling dự phòng webhook | **Đạt** | `services/shipping/internal/{carrier/ghtk.go,address/data.go,poller/,webhook/}`, toàn bộ 10 package pass (`-race`, `62.9%` coverage) |
| Contract test gRPC giữa `order` và `payment`, `shipping`, `promotion` | **Đạt** | `services/order/internal/service/contract_grpc_test.go` (wire-level qua `bufconn` + `auth.GRPCUnaryServerInterceptor`) |
| Coverage >= 60% cho `order`, `payment`, `promotion` | **Đạt** | `order`: **65.9%** (statements), `payment`: **85.25%** (lines, `llvm-cov`), `promotion`: **68.5%** (statements) — ngoài ra `shipping`: **62.9%** |
| Lint/format sạch (`gofmt`, `golangci-lint`, `cargo fmt`, `clippy`), Docker multi-stage non-root, OTel trace & JSON log che secret | **Đạt** | 0 lỗi lint/clippy; `USER 10001:10001` trên cả 4 service; đã khắc phục `OBS-02/04/05/06/09/10/12/13/14` và `PLT-03/04` |

---

## 4. Phần chưa làm ở Swarm D & lý do (theo đúng hợp đồng)

1. **Chưa chạy `make e2e`, `make e2e-web`, `make seed`, hay rebuild stack `docker-compose.yml` dùng chung:** Theo `.agents/briefs/m-all-contract.md`, việc dựng lại stack và chạy E2E tích hợp toàn hệ thống (bao gồm `tests/e2e/19_order_depth_test.go` và `tests/e2e-web/07_returns_seller_admin.test.mjs`) thuộc quyền sở hữu độc quyền của Swarm H (`qa-docs`).
2. **Chưa build lại Docker image cho `services/payment` cục bộ:** Do giới hạn dung lượng ổ đĩa dùng chung giữa 9 swarm (build Rust release trong Docker tốn thêm vài GB layer tạm). `services/payment/Dockerfile` đã có sẵn multi-stage + `USER 10001:10001` (không thay đổi phụ thuộc `Cargo.toml`/`Cargo.lock`), và mã nguồn đã được kiểm chứng đầy đủ bằng `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`, và `cargo test` trên PostgreSQL 17 thật. Swarm H sẽ build image chính thức khi rebuild stack.
3. **Ngưỡng tải 200 VU (`PLT-07`):** Mục tiêu p95 `< 300ms` đọc / `< 1s` tạo đơn ở 200 VU đồng thời phụ thuộc cấu hình multi-replica + HPA Kubernetes (do Swarm F phụ trách ở `deploy/helm/`) chứ không chỉ 1 container Postgres/Order đơn lẻ trong Docker Compose.
