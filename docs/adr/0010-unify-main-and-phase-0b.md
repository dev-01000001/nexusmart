# ADR-0010: Hợp nhất nhánh `main` (kèm `3b-3`) và nhánh `phase-0b`

- **Trạng thái**: Đã duyệt (chủ dự án, 2026-11-26)
- **Ngày**: 2026-11-25

## Bối cảnh

- Hai tuyến phát triển chạy song song và tách xa nhau trên kho mã nguồn:
  - **`main` + snapshot `3b-3` (`wip/w3b3-snapshot`, commit `d798940`)**: hoàn thiện lõi thương mại đã qua kiểm toán bảo mật và tài chính — mô hình tiền tệ/sổ cái ghi kép VND, hoàn tiền 2 bước, đối soát VietQR & file COD CSV, ZaloPay v2 (`mock`/`sandbox`), phê duyệt 2 người (tạm khóa shop & hoàn tiền lớn), phân quyền 6 vai trò nội bộ (`pkg/common/auth/capabilities.json`), luồng đổi trả 10 trạng thái, đánh giá sản phẩm, từ khóa cấm & danh mục cấm, nhập CSV sản phẩm, đóng gói/lên đơn lô & nhãn PDF A6, đổi địa chỉ trước lấy hàng, tra cứu đơn khách, và `services/search` bản `3b-3` (Meilisearch `v1.58.0` + `pgvector/pgvector:0.8.7-pg16` RRF).
  - **`origin/phase-0b` (commit `a98ea3d`)**: nền tảng đa cửa hàng độc lập theo tên miền phụ & tên miền riêng (`sites`, `site_versions`, `site_domains`, `site_templates` JSON v2 theo [ADR-0001](0001-site-template-format.md)), bỏ sàn chung trên giao diện web (redesign UI, BFF tường minh), `sales_channel` (`STOREFRONT`/`MARKETPLACE`), `services/chat`, `services/assistant`, `services/aiops`, stack observability (`deploy/observability/`) và triển khai Kubernetes/Helm/backup (`deploy/k8s/`, `deploy/helm/`, `tools/backup/`).
- Hệ thống chạy thử nghiệm/live đang mang **lịch sử cơ sở dữ liệu của `phase-0b`**, đặc biệt `services/payment` dùng `sqlx::migrate!` kiểm tra checksum tuyệt đối nên mọi file migration của `phase-0b` đã áp dụng không được thay đổi dù chỉ một byte.
- Chủ dự án yêu cầu gộp toàn bộ về một nhánh duy nhất, giữ trọn năng lực hai bên và chọn một thiết kế thống nhất tại các điểm giao nhau.

## Tiêu chí

1. **Không phá hủy DB `phase-0b` đang chạy**: nâng cấp tự động forward-only chạy sạch trên cả DB có lịch sử `phase-0b` lẫn DB trống mới dựng; không làm sai lệch checksum `sqlx`, không gây hoàn tiền hai lần.
2. **Bảo toàn toàn bộ cải tiến tài chính & bảo mật đã kiểm toán của `main`**: sổ cái, saga checkout, đổi trả, phân quyền 6 vai trò, chống giả mạo IP proxy.
3. **Thống nhất mô hình sản phẩm (bỏ sàn chung)**: toàn bộ mua sắm diễn ra khép kín trên site gian hàng độc lập, cô lập dữ liệu theo `shop_id`/`Host`.
4. **Duy trì một bộ hợp đồng duy nhất**: không giữ song song hai bộ API/RPC/consumer trùng chức năng cho cùng một miền nghiệp vụ.

## Quyết định

- **Quyết định**: Áp dụng 6 quyết định hợp nhất sau (**đã được chủ dự án duyệt ngày 2026-11-26** và đã merge vào `main`):

### 1. Đánh số lại migration của `main`/`3b-3` nối sau `phase-0b` (ngoại lệ một lần của `AGENTS.md` mục 9)

- Giữ nguyên **từng byte** tất cả file migration đã áp dụng trên nhánh `phase-0b`.
- Đổi số và thích ứng các file migration của `main`/`3b-3` nối tiếp sau số cao nhất của `phase-0b`, giữ đúng thứ tự gốc của `main`, đảm bảo chạy idempotent trên cả DB trống lẫn DB mang lịch sử `phase-0b`. Việc đổi tên/thích ứng các file migration của `main` là **ngoại lệ có ghi nhận của quy tắc mục 9 trong `AGENTS.md`**, đã được chủ dự án duyệt cùng ADR này (2026-11-26).
- Các điều chỉnh dữ liệu quan trọng khi nâng cấp từ `phase-0b`:
  - `services/order/migrations/0008_returns_and_lifecycle.sql`: ánh xạ 4 trạng thái đổi trả cũ của `phase-0b` sang máy trạng thái 10 bước của `main` (không dùng nhánh `ELSE 'CLOSED'`), đóng các return của đơn `COMPLETED`/`CANCELLED` chưa hoàn thành `CLOSED`, giữ `REJECTED` còn khiếu nại `OPEN` thành `ESCALATED`, gỡ FK `ON DELETE CASCADE` ở `complaints` và giữ nguyên bảng `complaints` chỉ-đọc, bảo toàn `refund_amount_vnd`, chép `seller_note`/`reverse_tracking_code`/`reverse_label_url`, đồng bộ `orders.refunded_vnd` chống hoàn tiền hai lần.
  - `services/payment/migrations/0005_refund_settlement_and_reconciliation.sql`: thêm điều kiện `AND r.provider_refund_id IS NULL` khi đưa các khoản hoàn `completed` chưa có bút toán `refund_settle` về `pending` để không hạ các khoản hoàn ZaloPay đã chi của `phase-0b` (các khoản VietQR chuyển về `pending` cần kế toán rà tay trước khi settle, xem [docs/UPGRADE_FROM_PHASE_0B.md](../UPGRADE_FROM_PHASE_0B.md)).
  - `services/payment/migrations/0012_zalopay_and_method_configs.sql`: gỡ constraint/index cũ (`chk_payments_method`, `idx_payments_pending_online_expiry`) và backfill `pay_url` từ `qr_url` cho giao dịch ZaloPay cũ của `phase-0b`.
  - `services/product/migrations/000013_release_legacy_flash_sale_reservations.sql`: bỏ cơ chế khóa tồn kho trước của `phase-0b` (giữ cột/bảng từ `000002` để tương thích schema), giải phóng toàn bộ dòng `ACTIVE` ở `flash_sale_stock_reservations` và đặt `skus.flash_sale_reserved_stock = 0`; xóa hai RPC `ReserveFlashSaleStock`/`ReleaseFlashSaleStock`.
  - `services/shipping/internal/store/store.go`: bổ sung bảng `schema_migrations` có cơ chế bootstrap nhận diện DB `phase-0b` qua cột `shipments.direction` để không chạy lại `000002_return_shipments.sql`; chuẩn hóa `direction` về `'FORWARD'`/`'RETURN'`; giữ adapter GHTK và nâng chuẩn resilience theo `main`.

#### Bảng ánh xạ migration cũ (`main`/`3b-3`) → mới (sau gộp) theo từng service

*(Các file của `phase-0b` được giữ nguyên tên và nội dung ở các số đầu; bảng dưới liệt kê toàn bộ file đổi số hoặc bổ sung mới khi gộp.)*

| Service | File giữ nguyên từ `phase-0b` | File cũ trên `main`/`3b-3` (`HEAD`) | File mới trong nhánh gộp | Ghi chú |
| --- | --- | --- | --- | --- |
| `user` | `0001_init.sql`, `0002_sites.sql`, `0003_domains_cloudflare.sql`, `0004_site_templates.sql` | `0002_add_shop_slug.sql` | `services/user/migrations/0005_add_shop_slug.sql` | Backfill slug ưu tiên subdomain của site |
| `user` | — | `0003_cod_rejections.sql` | `services/user/migrations/0006_cod_rejections.sql` | Đổi số +3 |
| `user` | — | `0004_password_resets_and_lock_reason.sql` | `services/user/migrations/0007_password_resets_and_lock_reason.sql` | Đổi số +3 |
| `user` | — | `0005_shop_bank_accounts.sql` | `services/user/migrations/0008_shop_bank_accounts.sql` | Đổi số +3 |
| `user` | — | `0006_shop_verified_owner_name_and_otp_publish.sql` | `services/user/migrations/0009_shop_verified_owner_name_and_otp_publish.sql` | Đổi số +3 |
| `user` | — | `0007_shop_bank_account_encryption.sql` | `services/user/migrations/0010_shop_bank_account_encryption.sql` | Đổi số +3 |
| `user` | — | `0008_phone_canonical_verified_unique.sql` | `services/user/migrations/0011_phone_canonical_verified_unique.sql` | Đổi số +3 |
| `user` | — | `0009_governance_approvals_activity.sql` | `services/user/migrations/0012_governance_approvals_activity.sql` | Đổi số +3 |
| `user` | — | `0010_pickup_address_guard_index.sql` | `services/user/migrations/0013_pickup_address_guard_index.sql` | Đổi số +3 |
| `product` | `000001_init.sql`, `000002_flash_sale_reservations.sql`, `000003_outbox_events.sql` | `000002_stock_history_restock_idempotency.sql` | `services/product/migrations/000004_stock_history_restock_idempotency.sql` | Đổi số +2 |
| `product` | — | `000003_add_product_slug.sql` | `services/product/migrations/000005_add_product_slug.sql` | Đổi số +2 |
| `product` | — | `000004_product_search_text.sql` | `services/product/migrations/000006_product_search_text.sql` | Đổi số +2 |
| `product` | — | `000005_w2b_catalog_outbox_price_fee.sql` | `services/product/migrations/000007_w2b_catalog_outbox_price_fee.sql` | Đổi số +2; tương thích bảng `outbox_events` có sẵn từ `000003` |
| `product` | — | `000006_stock_release_tombstones.sql` | `services/product/migrations/000008_stock_release_tombstones.sql` | Đổi số +2 |
| `product` | — | `000007_w3a_outbox_lease_and_sku_index.sql` | `services/product/migrations/000009_w3a_outbox_lease_and_sku_index.sql` | Đổi số +2 |
| `product` | — | `000008_w3b_reviews_and_moderation.sql` | `services/product/migrations/000010_w3b_reviews_and_moderation.sql` | Đổi số +2 |
| `product` | — | `000009_shop_suspended.sql` | `services/product/migrations/000011_shop_suspended.sql` | Đổi số +2 |
| `product` | — | `000010_w3b3_stock_import_and_index.sql` | `services/product/migrations/000012_w3b3_stock_import_and_index.sql` | Đổi số +2 |
| `product` | — | *(mới cho đợt gộp)* | `services/product/migrations/000013_release_legacy_flash_sale_reservations.sql` | Dọn dòng `ACTIVE` của `flash_sale_stock_reservations` và đặt `flash_sale_reserved_stock = 0` |
| `promotion` | `001_init.sql`, `002_flash_sale_window_status.sql` | `002_partial_release.sql` | `services/promotion/internal/store/migrations/003_partial_release.sql` | Đổi số +1 |
| `promotion` | — | `003_w2b_promotions.sql` | `services/promotion/internal/store/migrations/004_w2b_promotions.sql` | Đổi số +1 |
| `promotion` | — | `004_w3a_per_user_usage_and_sync.sql` | `services/promotion/internal/store/migrations/005_w3a_per_user_usage_and_sync.sql` | Đổi số +1 |
| `promotion` | — | `005_dead_letter_events.sql` | `services/promotion/internal/store/migrations/006_dead_letter_events.sql` | Đổi số +1 |
| `promotion` | — | `006_shop_suspension_state.sql` | `services/promotion/internal/store/migrations/007_shop_suspension_state.sql` | Đổi số +1 |
| `order` | `0001_init.sql`, `0002_sales_channel.sql`, `0003_orders_storefront_index.sql`, `0004_returns_and_complaints.sql` | `0002_checkout_groups_in_progress_idx.sql` | `services/order/migrations/0005_checkout_groups_in_progress_idx.sql` | Đổi số +3 |
| `order` | — | `0003_checkout_request_hash_and_refunded_vnd.sql` | `services/order/migrations/0006_checkout_request_hash_and_refunded_vnd.sql` | Đổi số +3 |
| `order` | — | `0004_retry_idempotency.sql` | `services/order/migrations/0007_retry_idempotency.sql` | Đổi số +3 |
| `order` | — | `0005_returns_and_lifecycle.sql` | `services/order/migrations/0008_returns_and_lifecycle.sql` | Đổi số +3; viết lại chuyển dữ liệu `phase-0b` không mất dữ liệu |
| `order` | — | `0006_w2a_review_fixes.sql` | `services/order/migrations/0009_w2a_review_fixes.sql` | Đổi số +3 |
| `order` | — | `0007_w2a_indep_review_fixes.sql` | `services/order/migrations/0010_w2a_indep_review_fixes.sql` | Đổi số +3 |
| `order` | — | `0008_w2a_review2_fixes.sql` | `services/order/migrations/0011_w2a_review2_fixes.sql` | Đổi số +3 |
| `order` | — | `0009_pending_shipment_cancel_ttl.sql` | `services/order/migrations/0012_pending_shipment_cancel_ttl.sql` | Đổi số +3 |
| `order` | — | `0010_w2b_order.sql` | `services/order/migrations/0013_w2b_order.sql` | Đổi số +3 |
| `order` | — | `0011_w2b_order_round4.sql` | `services/order/migrations/0014_w2b_order_round4.sql` | Đổi số +3 |
| `order` | — | `0012_w2b_order_round5.sql` | `services/order/migrations/0015_w2b_order_round5.sql` | Đổi số +3 |
| `order` | — | `0013_w2b_order_round6.sql` | `services/order/migrations/0016_w2b_order_round6.sql` | Đổi số +3 |
| `order` | — | `0014_outbox_lease.sql` | `services/order/migrations/0017_outbox_lease.sql` | Đổi số +3 |
| `order` | — | `0015_cod_rate_index_and_ship_job.sql` | `services/order/migrations/0018_cod_rate_index_and_ship_job.sql` | Đổi số +3 |
| `order` | — | `0016_online_methods.sql` | `services/order/migrations/0019_online_methods.sql` | Đổi số +3 |
| `order` | — | `0017_return_refund_approval.sql` | `services/order/migrations/0020_return_refund_approval.sql` | Đổi số +3 |
| `order` | — | `0018_w3b3_address_and_filters.sql` | `services/order/migrations/0021_w3b3_address_and_filters.sql` | Đổi số +3 |
| `payment` | `0001_init.sql`, `0002_sales_channel.sql`, `0003_zalopay.sql` | `0002_payment_money_model.sql` | `services/payment/migrations/0004_payment_money_model.sql` | Đổi số +2 |
| `payment` | — | `0003_refund_settlement_and_reconciliation.sql` | `services/payment/migrations/0005_refund_settlement_and_reconciliation.sql` | Đổi số +2; thêm `AND r.provider_refund_id IS NULL` |
| `payment` | — | `0004_reconciliation_queue_unique_tx.sql` | `services/payment/migrations/0006_reconciliation_queue_unique_tx.sql` | Đổi số +2 |
| `payment` | — | `0005_reconciliation_fixes.sql` | `services/payment/migrations/0007_reconciliation_fixes.sql` | Đổi số +2 |
| `payment` | — | `0006_trim_provider_tx_ids.sql` | `services/payment/migrations/0008_trim_provider_tx_ids.sql` | Đổi số +2 |
| `payment` | — | `0007_payment_fee_configs.sql` | `services/payment/migrations/0009_payment_fee_configs.sql` | Đổi số +2 |
| `payment` | — | `0008_outbox_lease_and_fee_config_index.sql` | `services/payment/migrations/0010_outbox_lease_and_fee_config_index.sql` | Đổi số +2 |
| `payment` | — | `0009_cod_reconciliation_and_dlq.sql` | `services/payment/migrations/0011_cod_reconciliation_and_dlq.sql` | Đổi số +2 |
| `payment` | — | `0010_zalopay_and_method_configs.sql` | `services/payment/migrations/0012_zalopay_and_method_configs.sql` | Đổi số +2; bỏ constraint/index `0003_zalopay`, backfill `pay_url` |
| `payment` | — | `0011_refund_two_person_approval.sql` | `services/payment/migrations/0013_refund_two_person_approval.sql` | Đổi số +2 |
| `payment` | — | `0012_zalopay_late_payment_sweep.sql` | `services/payment/migrations/0014_zalopay_late_payment_sweep.sql` | Đổi số +2 |
| `payment` | — | `0013_p4_refund_lease_and_audit.sql` | `services/payment/migrations/0015_p4_refund_lease_and_audit.sql` | Đổi số +2 |
| `payment` | — | `0014_zalopay_last_query_state.sql` | `services/payment/migrations/0016_zalopay_last_query_state.sql` | Đổi số +2 |
| `payment` | — | `0015_w3b3_cod_fee_adjustments.sql` | `services/payment/migrations/0017_w3b3_cod_fee_adjustments.sql` | Đổi số +2 |
| `shipping` | `000001_init.sql`, `000002_return_shipments.sql` | `000002_shipping_ops.sql` | `services/shipping/migrations/000003_shipping_ops.sql` | Đổi số +1; chuẩn hóa `direction` `'FORWARD'`/`'RETURN'` + bootstrap `schema_migrations` |
| `shipping` | — | `000003_return_recreate_index.sql` | `services/shipping/migrations/000004_return_recreate_index.sql` | Đổi số +1 |
| `shipping` | — | `000004_shop_freeship_settings.sql` | `services/shipping/migrations/000005_shop_freeship_settings.sql` | Đổi số +1 |
| `shipping` | — | `000005_outbox_lease.sql` | `services/shipping/migrations/000006_outbox_lease.sql` | Đổi số +1 |
| `shipping` | — | `000006_w3b_wards_and_shipment_jobs.sql` | `services/shipping/migrations/000007_w3b_wards_and_shipment_jobs.sql` | Đổi số +1 |
| `shipping` | — | `000007_w3b3_shipment_label_fields.sql` | `services/shipping/migrations/000008_w3b3_shipment_label_fields.sql` | Đổi số +1 |
| `notification` | `000001_init.sql`, `000002_order_storefront.sql` | `000002_order_participants_checkout_group.sql` | `services/notification/migrations/000003_order_participants_checkout_group.sql` | Đổi số +1 |
| `notification` | — | `000003_shop_owners_and_dedup.sql` | `services/notification/migrations/000004_shop_owners_and_dedup.sql` | Đổi số +1 |
| `notification` | — | `000004_notification_dedup_and_indexes.sql` | `services/notification/migrations/000005_notification_dedup_and_indexes.sql` | Đổi số +1 |
| `search` | `000001_init.sql` | `0001_init.sql` | `services/search/migrations/000002_w3b3_search_schema.sql` | Gộp schema `3b-3` sau `000001_init.sql` của `phase-0b` |
| `chat` | `000001_init.sql` | *(không có trên `main`)* | `services/chat/migrations/000001_init.sql` | Giữ nguyên từ `phase-0b` |
| `assistant` | `000001_init.sql` | *(không có trên `main`)* | `services/assistant/migrations/000001_init.sql` | Giữ nguyên từ `phase-0b` |

### 2. Bỏ sàn chung trên giao diện web (`web/`)

- Thực thi chốt của chủ dự án ngày `2026-11-25`: bỏ toàn bộ trang sàn thương mại đa gian hàng ở host gốc và xóa proxy catch-all `/api/gw/*`.
- Host gốc (`<SITE_ROOT_DOMAIN>`) chỉ còn trang giới thiệu nền tảng + danh bạ site đang xuất bản (`(shop)`), Kênh người bán `(seller)` và Cổng quản trị `(admin)`. Mọi Buyer API trả `404` trên host gốc; `/gian-hang/[slug]` và `/san-pham/[slug]` chuyển hướng `308` sang site đang xuất bản.
- Toàn bộ trải nghiệm mua sắm của `main` (đánh giá PDP, tính phí ship, chương trình giảm giá shop, giỏ hàng báo SKU không khả dụng, chọn phương thức thanh toán + kiểm COD + ZaloPay + SSE chờ thanh toán, đổi trả/giao lại/hoàn tiền, OTP SĐT, cài đặt thông báo, quên/đặt lại mật khẩu) được đưa vào site gian hàng độc lập `(site)/s/[host]/**` thông qua BFF tường minh `/api/*`.
- Voucher sàn và Xu ở Admin giữ chế độ chỉ-đọc trên web (không xóa API backend).

### 3. Dịch vụ tìm kiếm (`services/search`) lấy nền `3b-3` + cô lập theo `shop_id`

- Chọn mã nguồn `services/search` của sóng `3b-3` (theo [ADR-0004](0004-search-service-hybrid-vector.md): Meilisearch `getmeili/meilisearch:v1.58.0` trên volume mới `meilisearch_v1_58_data` + `pgvector/pgvector:0.8.7-pg16`, xếp hạng RRF, chuẩn hóa tiếng Việt `pkg/common/textnorm`, outbox `product.upserted`/`product.deleted`/`product.category_updated`, ẩn tức thì khi shop tạm khóa qua `user.shop_status_changed`).
- Bổ sung lọc `shop_id` cho `Suggest` và `HybridSearch` để cô lập kết quả trong từng site gian hàng.
- Chuyển RPC `SimilarProducts` từ bản `phase-0b` sang dùng `SearchProductHit` chuẩn của `3b-3`; giữ lại chart `deploy/helm/search/`.
- Lịch sử tìm kiếm người dùng (`/api/v1/search/history`) giữ ở gateway/backend nhưng **chưa mở UI trên `(site)`** trong đợt gộp này.

### 4. Luồng đổi trả theo `main`

- Giữ duy nhất máy trạng thái đổi trả 10 bước của `main` (`POST/GET /api/v1/orders/{id}/return`, `/api/v1/returns/*`, `/api/v1/seller/returns/{id}/{respond,confirm-received,dispute}`, `/api/v1/admin/returns/{id}/resolve`).
- Bỏ 8 route cũ của `phase-0b`: `POST/GET /api/v1/orders/{id}/returns`, `POST /api/v1/orders/{id}/complaints`, `POST /api/v1/seller/returns/{id}/approve|reject`, `GET /api/v1/admin/complaints`, `POST /api/v1/admin/complaints/{id}/resolve`, và `GET /api/v1/admin/payments/reconciliation-reports` (thay bằng `/api/v1/admin/reconciliation/runs` và `/queue`).
- Bỏ consumer `order.refunded` của `phase-0b` ở `services/promotion`; việc hoàn/thu hồi xu đi qua `ReleasePromotionShop`.
- Bảng `complaints` được giữ chỉ-đọc trong `order_db` để tra cứu lịch sử; khiếu nại **không kèm đổi trả** hiện chưa có kênh riêng.

### 5. Kênh bán hàng mặc định `STOREFRONT`

- Ở gateway `POST /api/v1/checkout` (`services/gateway/internal/router/sales_channel.go`): khi client bỏ trống `sales_channel`, hệ thống mặc định là `STOREFRONT` và **bắt buộc** có `storefront_shop_id` (thiếu trả `400 INVALID_ARGUMENT`).
- Gateway vẫn chấp nhận `sales_channel = "MARKETPLACE"` khi gửi **tường minh** (với `storefront_shop_id` rỗng) để bảo toàn saga đa gian hàng đã kiểm toán và bộ E2E của `main`; lớp web/BFF **không bao giờ** gửi `MARKETPLACE` (luôn ép `STOREFRONT` + `storefront_shop_id` suy từ `Host`).
- Các màn Admin/Seller và API báo cáo/đối soát giữ bộ lọc `sales_channel` chỉ-đọc cho dữ liệu lịch sử `MARKETPLACE`.

### 6. Mạng nội bộ `bff` (`172.31.250.0/28`) cho kết nối `web` → `gateway`

- `docker-compose.yml` tạo mạng Docker cô lập `bff` (`internal: true`, subnet `172.31.250.0/28`), gắn `gateway` với alias `gateway-bff` và cấu hình `web` gọi `GATEWAY_URL=http://gateway-bff:8080`.
- `services/gateway` sử dụng `services/gateway/internal/middleware/clientip.go` của `main`, đọc `TRUSTED_PROXIES` (mặc định `172.31.250.0/28`, có fallback `TRUSTED_PROXY_CIDRS`), nhờ đó chỉ tin `X-Forwarded-For` đi từ container `web` và chặn hoàn toàn giả mạo IP từ ngoài host hoặc container khác.
- Cổng mặc định thống nhất: `GATEWAY_PORT=8080`, `WEB_PORT=3300`.

- **Phụ thuộc**: Ngoại lệ đổi số migration (`AGENTS.md` mục 9) đã được duyệt; vẫn cần rà soát danh sách hoàn tiền VietQR `completed → pending` của `payment 0005` trước khi triển khai production.
- **Chủ dự án quyết**: Đã duyệt (2026-11-26).

## Hệ quả

- **Tích cực**:
  - Một mã nguồn duy nhất hội tụ đầy đủ bảo mật/tài chính đã kiểm toán của `main` và nền tảng đa cửa hàng + Chat/Assistant/AIOps/Deploy của `phase-0b`.
  - Nâng cấp trực tiếp được trên DB live mang lịch sử `phase-0b` mà không làm vỡ checksum SQLx hay mất dữ liệu đổi trả/khiếu nại cũ.
  - Chặn giả mạo `X-Forwarded-For` ở gateway và cô lập hoàn toàn dữ liệu mua hàng/tìm kiếm giữa các cửa hàng.
- **Tiêu cực / Đánh đổi & Việc hoãn lại**:
  - DB dev cũ tạo theo lịch sử `main` (chưa có các migration `phase-0b` ở số đầu) cần dựng lại volume sạch vì số migration `main` đã tịnh tiến.
  - Sau nâng cấp từ `phase-0b` phải chạy `POST /api/v1/admin/search/reindex` một lần (vì Meilisearch đổi lên `v1.58.0` / volume mới `meilisearch_v1_58_data`), dọn consumer NATS cũ `search-product-indexer-v1`, và rà tay các khoản hoàn VietQR bị `payment 0005` chuyển về `pending`.
  - Chưa triển khai trong đợt gộp này: ZaloPay live mode, kênh/quyền riêng cho khiếu nại không kèm đổi trả, giao diện lịch sử tìm kiếm trên `(site)` (mục chuyển `assistant` sang `OrderService.GetSellerReport` đã hoàn tất ở đợt p2). Riêng `ChatService.ExchangeWSTicket` được giữ public có chủ ý vì gateway không giữ `SERVICE_JWT_SECRET`; vé ngẫu nhiên 256-bit, dùng một lần (Redis `GETDEL`), hết hạn ngắn (`CHAT_WS_TICKET_TTL`, mặc định `60s`).
