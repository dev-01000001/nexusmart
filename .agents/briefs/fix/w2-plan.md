# Kế hoạch đợt 2 (chủ dự án duyệt chạy 2026-11-23)

Nền: đợt 1 đã merge vào `main` qua PR #3 (`44e6585`). Đợt 2 làm và commit thẳng trên `main` (chủ dự án quyết 2026-11-23); push hỏi riêng. Mô hình tiền ở `plan.md` §2 giữ nguyên, mục 2 dưới đây chỉ bổ sung. Không thêm hạ tầng mới: ADR 0004–0008 chưa được duyệt (AGENTS.md luật 11).

Chia hai phần, mỗi phần nghiệm thu và commit riêng:
- **2a**: thu hồi phiên tức thời, trả hàng và hoàn tiền đầu-cuối, vận đơn, thời gian giữ tiền, giao dịch không khớp, job tự hoàn tất/tự huỷ, thông báo cho người bán, quên mật khẩu.
- **2b**: quản lý khuyến mãi, giảm giá của shop, giá flash trong giỏ, catalog, báo cáo, cấu hình phí, tài khoản ngân hàng seller, quan sát, gỡ mock ở web, dữ liệu mẫu.

Ngoài đợt 2 (ghi để không ai làm): tải ảnh bằng chứng lên kho đối tượng (ADR 0005, hiện chỉ nhận URL); Grafana/Alertmanager/Telegram (ADR 0007); đăng nhập Google thật (cần credential); đổi hàng (tạo đơn thay thế) trong UC-A05; UC-PAY03 đối soát COD theo file hãng; UC mức Thấp.

## 1. Luật trạng thái mới (2a)

### 1.1 Vận đơn và đơn hàng (SHP-B3, UC-SHIP03/04/07)
- `POST /seller/orders/{id}/ship` tạo vận đơn, đơn `CONFIRMED|PREPARING → PREPARING`, có `tracking_code`. KHÔNG còn chuyển thẳng `SHIPPING`.
- Đơn `PREPARING → SHIPPING` (phát `order.shipped`) khi nhận `shipment.updated` chiều `FORWARD` có trạng thái `delivering|delivered|failed|returning|returned`. Nếu đơn còn `CONFIRMED|PREPARING` mà nhận `delivered` hoặc `returned` thì đi qua `SHIPPING` rồi tới trạng thái đích trong cùng transaction (đủ hai dòng timeline, đủ event).
- `picked` của hãng map sang `delivering` (hãng đã cầm hàng, hết huỷ được); `ready_to_pick|picking` giữ `picking` (còn huỷ được).
- Huỷ đơn đã có `tracking_code`: `order` gọi `ShippingService.CancelShipment` (đồng bộ) trước; hãng đã lấy hàng → 409, đơn giữ nguyên. Consumer `order.cancelled` ở shipping giữ làm lưới an toàn. Vận đơn đã huỷ không chặn tạo vận đơn mới cho cùng đơn.
- Đơn `CONFIRMED` quá `ORDER_SELLER_CONFIRM_HOURS` (mặc định 48) mà người bán chưa xác nhận: tự huỷ theo đường huỷ chuẩn (hoàn tiền, trả tồn, trả khuyến mãi), `cancel_reason = "seller_confirm_timeout"`.
- Đơn `DELIVERED` quá `ORDER_AUTO_COMPLETE_DAYS` (mặc định 7) không có yêu cầu trả hàng: tự `COMPLETED`, cùng đường code với `BuyerConfirmReceived` (event, xu).

### 1.2 Yêu cầu trả hàng (UC-B09, S10, A05, SHIP08)
Trạng thái `return_requests.status`:

| Từ | Sự kiện | Tới | Hạn kế tiếp |
|---|---|---|---|
| (tạo) | Người mua yêu cầu, đơn `DELIVERED`, trong 7 ngày | `PENDING` | 3 ngày, quá hạn tự chấp nhận kiểu trả hàng về (`AWAITING_SHIPMENT`) |
| `PENDING` | Seller `ACCEPT_REFUND` (không cần trả hàng) | `REFUNDING` | — |
| `PENDING` | Seller `ACCEPT_RETURN` | `AWAITING_SHIPMENT` + tạo vận đơn `RETURN` | 5 ngày, quá hạn `CLOSED` |
| `PENDING` | Seller `REJECT` (bắt buộc lý do) | `REJECTED` | 3 ngày, quá hạn `CLOSED` |
| `REJECTED` | Người mua mở khiếu nại | `ESCALATED` | — |
| `AWAITING_SHIPMENT` | `shipment.updated` chiều RETURN = `delivering` | `IN_TRANSIT` | — |
| `AWAITING_SHIPMENT`/`IN_TRANSIT` | `shipment.updated` RETURN = `delivered` | `RECEIVED` | 3 ngày, quá hạn tự xác nhận (`REFUNDING`) |
| `RECEIVED` | Seller xác nhận nhận hàng | `REFUNDING` | — |
| `RECEIVED` | Seller báo hàng trả không đúng (bắt buộc lý do) | `ESCALATED` | — |
| `ESCALATED` | Admin `REQUEST_EVIDENCE` | `EVIDENCE_REQUESTED` | 3 ngày, quá hạn `CLOSED` |
| `EVIDENCE_REQUESTED` | Người mua bổ sung bằng chứng | `ESCALATED` | — |
| `ESCALATED` | Admin `REFUND` | `REFUNDING` | — |
| `ESCALATED` | Admin `RETURN_AND_REFUND` (chỉ khi chưa từng nhận hàng trả) | `AWAITING_SHIPMENT` | 5 ngày |
| `ESCALATED` | Admin `REJECT` | `CLOSED` | — |
| `REFUNDING` | Mọi tác vụ phụ idempotent đã xong | `REFUNDED` | — |

- Vào `REFUNDING`: trong MỘT transaction đặt đơn `RETURN_REQUESTED → RETURNED`, ghi timeline, outbox `order.returned` (`from_status = RETURN_REQUESTED`) và `order.return_updated`. Sau commit (và lặp lại bởi watchdog tới khi xong): `CreateRefund` số còn lại `P − refunded_vnd` (nếu > 0, key `order-return-refund:<order_id>`, dùng cho cả VIETQR lẫn COD đã thu), `ReleasePromotionShop`, `RestockOrderItems` CHỈ khi hàng thật sự về kho người bán (đã qua `RECEIVED` và không phải nhánh tranh chấp hàng sai). Chỉ sau khi mọi bước trả về thành công mới sang `REFUNDED`.
- Vào `CLOSED`: đơn `RETURN_REQUESTED → COMPLETED` qua cùng đường code hoàn tất đơn (event `order.completed`, xu, quyết toán).
- Mỗi lần chuyển trạng thái ghi `return_events` (append-only) và outbox `order.return_updated`. Một đơn tối đa một yêu cầu trả hàng (unique theo `order_id`).
- Vận đơn RETURN quá hạn/`CLOSED` khi chưa lấy hàng: huỷ vận đơn RETURN (best-effort, idempotent).
- Phí trả hàng: chỉ ghi `return_fee_vnd` và `return_fee_bearer` (`SELLER` mặc định khi seller chấp nhận; admin chọn khi phân xử). Chưa hạch toán sổ cái ở đợt này (`ponytail:`).
- Quyền: người mua chủ đơn; người bán theo `shop_id` trong claims; admin/super_admin cho phân xử. Mọi bước chạy song song (hai lần bấm, job và người dùng) phải an toàn: khoá dòng `return_requests` `FOR UPDATE`, kiểm trạng thái trong transaction.

### 1.3 Giao thất bại (UC-SHIP05)
- `shipment.updated = failed`: thông báo người mua và người bán. Người mua `POST /orders/{id}/redelivery {preferred_date, note}` khi vận đơn FORWARD đang `failed`; tối đa `SHIPPING_MAX_REDELIVERY` (3) lần, quá thì 409. Shipping gọi adapter hãng (mock chấp nhận), ghi `shipment_events`.
- Hàng hoàn (`returning`, `returned`): thông báo người bán.

## 2. Tiền (bổ sung cho plan.md §2)
- **Giữ tiền (PAY-B7, ORD-B9)**: `order.completed` → quyết toán vào `seller_pending` (như hiện tại). `order` KHÔNG gọi `ReleaseEscrow` nữa. Job ở payment mỗi 60 giây chuyển `pending_release → released` (`seller_pending → seller_available`) khi `settled_at + PAYMENT_SELLER_HOLD_HOURS` (mặc định 72) đã qua. `ReleaseEscrow` RPC chỉ còn admin/super_admin, bỏ khỏi `SERVICE_METHODS`.
- **Hoàn tiền hai bước (PAY-B11, UC-PAY04)**: `CreateRefund` ghi nghĩa vụ như hiện tại (bút toán `escrow_liability → refund_payable`, phát `payment.refunded`) nhưng bản ghi ở `pending` (chờ chuyển khoản). Người mua nhập tài khoản nhận; admin `SettleRefund` (mã tham chiếu chuyển khoản) → Nợ `refund_payable` / Có `gateway_clearing`, `completed`, phát `payment.refund_settled`. Tổng hoàn vẫn tính cả `pending`. Dòng `completed` cũ chưa có bút toán tất toán → migration đưa về `pending`.
- **Giao dịch không khớp (PAY-B9, UC-A14)**: hành động `ASSIGN` (đủ tiền: xác nhận như webhook thành công; thừa: xác nhận và tạo refund `pending` phần thừa; thiếu: 409), `REFUND` (ghi Nợ `gateway_clearing` / Có `refund_payable`, refund `pending`; cần có `payment_id`), `SUSPEND` (treo, có ghi chú). Mỗi mục chỉ kết luận một lần (trừ `SUSPEND` → được kết luận lại).
- **Đối soát (PAY-B8)**: ngày đối soát theo `Asia/Ho_Chi_Minh`; mock đọc giao dịch từ `webhook_events` thay vì RAM; SePay dùng thời gian thật của giao dịch; PayOS không có API liệt kê thì trả lỗi rõ ("cổng chưa có dữ liệu") và run ở trạng thái `failed`, không gắn `missing_provider` hàng loạt.
- Bất biến sau mọi bài test: tổng nợ = tổng có; không `escrow_liability` nhóm nào âm; không `refund_payable` âm; mỗi nhóm đơn ≤ 1 payment `succeeded`; Σ(R+Ship−D) = amount.

## 3. Bảo mật và tài khoản
- **USR-B1**: khoá tài khoản, hạ/đổi quyền, đặt lại mật khẩu → đặt Redis `user:revoked_before:<user_id>` = unix giây, TTL bằng hạn access token (15 phút + 1 phút); gateway từ chối token có `iat <= giá trị đó` (401). Đổi role cũng thu hồi refresh token. Gateway đọc hai khoá bằng một lệnh `MGET`; lỗi Redis → 503 như hiện tại.
- **USR-B8**: `verify-otp`, `resend-otp`, `forgot-password` trả cùng một phản hồi cho email có và không có; login với email không tồn tại chạy một phép băm giả; tài khoản `LOCKED` (mật khẩu đúng) trả lý do khoá và hướng dẫn liên hệ.
- **UC-B11 (phần mật khẩu)**: `POST /auth/forgot-password {email}` gửi link hạn 15 phút (token ngẫu nhiên 32 byte, chỉ lưu hash, dùng một lần); `POST /auth/reset-password {token,new_password}` thu hồi mọi refresh token và access token. Đăng nhập Google: không làm (thiếu credential).

## 4. Thông báo (PRM-B14)
- Người bán nhận in-app + email cho: đơn mới (`order.confirmed`), đơn huỷ, yêu cầu trả hàng mới/khiếu nại, hàng trả về kho, giao thất bại, hàng hoàn về.
- `shipment.updated`/`order.return_updated` đến trước `order.created` → trả lỗi để NATS gửi lại, không ghi `processed_events` sớm.
- `shipment.updated` `delivering|delivered|failed` có email cho người mua.

## 5. Quy trình
1. `w2a-contract` tuần tự, manager review.
2. Song song: `w2a-order`, `w2a-payment`, `w2a-shipping`, `w2a-user`, `w2a-gateway`, `w2a-notification`, `w2a-web`.
3. `w2a-qa`: sửa bài e2e/e2e-web/seed cũ theo luật mới (ship không còn vào `SHIPPING` ngay, tiền vào `pending`).
4. Review độc lập (agy) cho order/payment/shipping; sửa; nghiệm thu stack sạch; kiểm bất biến; commit.
5. Làm tương tự cho 2b (brief viết sau khi 2a xong).
