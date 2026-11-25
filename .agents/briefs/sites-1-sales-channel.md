# Brief – Giai đoạn 1: kênh bán (sales channel) + vá phần tin cậy còn sót

Bạn là coordinator. Claude Code là manager và sẽ tự chạy lại mọi gate. Đọc trước: `AGENTS.md`,
`docs/research/sites/review-opus.md` (kiến trúc chốt, mục kênh bán), `docs/SECURITY_0A_REPORT.md`
(phần "còn sót"), `docs/WEB_INTEGRATION_REPORT.md`, `.agents/briefs/sites-0b-swarm-rules.md`.

Nhánh hiện tại: `phase-0b`. KHÔNG commit, không push, không đổi nhánh. CI chỉ `workflow_dispatch`:
không thêm trigger. Không kill tiến trình ở cổng 3111 và 4777; không ghi vào `web/.next`
(build kiểm tra dùng `NEXT_DIST_DIR` riêng). Không dùng `sudo`. Không thêm dependency runtime.

## A. Kênh bán (nền cho site riêng của từng gian hàng ở giai đoạn 2)
1. Proto + DB: thêm `sales_channel` (`MARKETPLACE` mặc định | `STOREFRONT`) và `storefront_shop_id`
   vào đơn hàng (đơn cha và đơn con) và vào phân bổ thanh toán (payment allocation / ledger entry
   metadata). Hai trường BẤT BIẾN sau khi tạo: không RPC nào sửa được; có test chứng minh.
   Migration tương thích ngược: đơn cũ = `MARKETPLACE`.
2. Quy tắc đơn `STOREFRONT` (kiểm ở service `order`, không chỉ ở gateway hay web):
   - mọi dòng hàng phải thuộc đúng `storefront_shop_id`; sai → `INVALID_ARGUMENT`, không giữ tồn;
   - `storefront_shop_id` phải là gian hàng đang hoạt động;
   - chỉ nhận voucher scope `SHOP` của chính gian hàng đó; voucher sàn, freeship sàn, xu sàn bị từ chối
     với mã lỗi rõ ràng (service `promotion` kiểm, `order` truyền kênh xuống).
3. Giỏ hàng: vẫn một khóa giỏ cho mỗi người dùng. Thêm tham số lọc `shop_id` cho đọc giỏ và cho
   checkout (checkout STOREFRONT chỉ lấy dòng của gian hàng đó, các dòng khác ở lại trong giỏ).
4. Gateway + BFF: checkout nhận `sales_channel` và `storefront_shop_id`. Mặc định `MARKETPLACE`.
   Client không được tự chọn kênh qua body tùy ý ở giai đoạn này: BFF `web/src/app/api/checkout/**`
   luôn gửi `MARKETPLACE` (giai đoạn 2 sẽ suy ra từ Host). Gateway vẫn validate giá trị.
5. Hiển thị: API đơn (buyer, seller, admin) trả hai trường; màn seller đơn hàng và admin đơn hàng
   hiện nhãn kênh. Đối soát/sổ cái admin lọc được theo kênh.
6. Sổ cái vẫn cân sau mọi kịch bản. Không đổi cách tính phí ở giai đoạn này.

## B. Vá tin cậy còn sót (xác nhận bằng đọc code trước khi sửa)
1. Các RPC nội bộ còn chấp nhận người gọi ẩn danh trong mạng compose (ít nhất `UpdateStock`,
   `ClearItems`, `CreateShipment`, `ValidateUser`; rà TẤT CẢ service để tìm cái khác): bắt buộc JWT
   người dùng hợp lệ có quyền, hoặc service token và RPC nằm trong `internalRPCs`. Thêm test từ chối.
2. Gateway `ClientIP` đang tin `X-Forwarded-For`/`X-Real-IP` từ bất kỳ ai (cổng 28080 công khai) →
   giả mạo để né rate limit. Chỉ tin header khi `RemoteAddr` thuộc `TRUSTED_PROXY_CIDRS` (env; compose
   đặt là dải mạng compose để BFF web chuyển IP thật). Mặc định rỗng = không tin. Có unit test.
3. `handleSellerGetOrder`: từ chối khi `shop_id` trong JWT rỗng (hiện `"" == ""` lọt qua lớp gateway).
4. `docker-compose.yml`: Postgres, Redis, NATS, mailpit, otel và các cổng HTTP nội bộ 808x của từng
   service chỉ bind `127.0.0.1` (hoặc bỏ publish nếu không cần). Giữ gateway 28080 và web 3300 như cũ.
   gRPC 5005x vẫn không publish.
5. `web/src/middleware.ts` và `lib/server/http.ts` dùng `x-forwarded-host`/`x-forwarded-proto` vô điều
   kiện: chỉ dùng khi có cấu hình tin proxy (env), mặc định dùng `Host`. Giữ test CSRF xanh.

## C. Độ ổn định của test
`make e2e` (~180 request) và `make e2e-web` (~145 request) dùng chung một IP container nên chạy sát
nhau là dính `RATE_LIMIT_IP_PER_MIN=300` (manager đã gặp: 3 lần chạy liên tiếp → 429). Sửa để chuỗi
`make e2e && make seed && make e2e-web && make e2e-web && make e2e-web` xanh mà KHÔNG nới giới hạn
của stack mặc định và KHÔNG dựa vào giả mạo XFF. Gợi ý: profile test dùng biến env giới hạn riêng,
hoặc runner xóa khóa `rl:*` của chính nó trước khi chạy. Phải còn ít nhất một test chứng minh rate
limit thật vẫn chặn (429).

## Gate (dán đầu ra thật vào báo cáo)
- `make lint`, `make test`; `cd web && npm run lint && npm run format:check && npx tsc --noEmit`,
  build với `NEXT_DIST_DIR` riêng.
- `make e2e` thêm kịch bản: STOREFRONT 1 gian hàng thành công (COD và VietQR mock); STOREFRONT có hàng
  của gian hàng khác bị từ chối và tồn kho không đổi; voucher sàn trên đơn STOREFRONT bị từ chối;
  hai trường bất biến; đơn cũ đọc ra `MARKETPLACE`; sổ cái cân; lọc đối soát theo kênh.
- Test từ chối cho từng RPC ở mục B1 và test XFF ở B2.
- Chuỗi lệnh ở mục C chạy xanh liền một mạch.
- `ss -ltnp` chứng minh các cổng ở B4 không còn nghe trên `0.0.0.0`.
- Viết `docs/PHASE1_REPORT.md`: đã làm, bằng chứng từng gate, phần chưa làm kèm lý do, mọi chỗ lệch.
