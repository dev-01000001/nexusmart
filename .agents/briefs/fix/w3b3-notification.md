# 3b-3 — Agent `notification` (A8: G3 + B1 consumer safety)

- **Sở hữu**: `services/notification/**`. Đọc `w2-rules.md`, `w3b-plan.md` §5 (**G3**), `w3b-design-a8.md` §2.3, `docs/contracts.md` §5.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: không cần migration mới.

## Phạm vi bắt buộc
1. **Xử lý event `order.address_updated` (G3)** trong `services/notification/internal/service/notification.go`:
   - Khử trùng trong transaction `ProcessEventTx` theo `event_id` (`processed_events`) giống các event `order.*` khác.
   - Payload: `{"order_id","order_code","buyer_id","shop_id","old_fee_vnd","new_fee_vnd","tracking_code","shipment_recreated","shipment_queued","province_name","ward_name"}`. Kiểm bắt buộc có `order_id`, `shop_id` (thiếu → lỗi malformed vào DLQ).
   - Tra chủ gian hàng qua `UserService` (`userResolver`): gửi thông báo **in-app + email** cho **người bán** báo khách đã cập nhật địa chỉ giao hàng (chỉ ghi `province_name`/`ward_name`, thay đổi phí ship nếu có, và **nhắc in lại nhãn vận đơn mới** khi `shipment_recreated == true` hoặc `shipment_queued == true`).
   - Gửi thông báo **in-app** xác nhận đổi địa chỉ cho **người mua** (`buyer_id`).
   - Tuyệt đối không ghi số nhà hay SĐT người nhận vào tiêu đề/nội dung log hay DLQ.
2. **An toàn với event mới của B1 (`product.upserted`, `product.deleted`, `product.category_updated`)**: xác nhận nhánh `default:` trong consumer vẫn `Ack` sạch và không đẩy sang `dead_letter_events`/`NEXUSMART_DLQ`.

## Tiêu chí đo được & Ca tấn công/đua bắt buộc
- Idempotency: gửi lặp cùng `event_id` của `order.address_updated` → chỉ tạo 1 thông báo người bán + 1 thông báo người mua + 1 email (lần 2 trả `dup = true`).
- Nhắc in nhãn: `shipment_recreated=true` và `shipment_queued=true` đều sinh nội dung nhắc người bán in lại nhãn; `shipment_recreated=false && shipment_queued=false` không nhắc in lại nhãn.
- Payload thiếu `order_id`/`shop_id` → vào DLQ; `product.upserted` / `product.deleted` / `product.category_updated` → `Ack` sạch, 0 dòng DLQ.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `bash scripts/test-db.sh --run bash -c 'cd services/notification && go test -race ./...'` (0 SKIP) và test mới `-race -count=5`.
