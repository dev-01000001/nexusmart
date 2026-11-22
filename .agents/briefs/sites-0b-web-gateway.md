# Giai đoạn 0b – Nối `web/` với gateway (dữ liệu thật cho luồng mua)

Bạn là coordinator của swarm. Claude Code là manager và sẽ review. Đọc `AGENTS.md`, `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`, `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`, `docs/research/sites/fit.md` và `review-opus.md` trước.

## Hiện trạng
- `web/` (Next.js 15, 44 route) dùng 100% dữ liệu mẫu: `src/data/*`, `StoreContext.tsx` (~1.850 dòng, localStorage), `sellerStore`, `auditStore`. Không có `fetch()`, không có `middleware.ts`, không Dockerfile, không nằm trong compose/CI.
- Backend chạy qua `make up`; worktree này có stack riêng, gateway REST ở `http://localhost:38080` (xem `sites-0b-swarm-rules.md`). gRPC không còn công bố ra host. `make e2e` chạy trong network compose.

## Mục tiêu
Một người mua thật dùng `web/` để: đăng ký (OTP qua mailpit), đăng nhập, xem danh mục và sản phẩm, thêm giỏ, áp voucher, đặt hàng nhiều gian hàng bằng COD và VietQR mock, xem danh sách và chi tiết đơn. Mọi dữ liệu của luồng này đến từ backend qua gateway.

## Việc phải làm
1. **Lớp BFF cùng origin**: trình duyệt chỉ gọi route handler / server action / server component của Next. Next server gọi gateway (`GATEWAY_URL`). Không thêm CORS vào gateway. Access token và refresh token nằm trong cookie `HttpOnly`, `Secure` (trừ dev), `SameSite=Lax`, host-only (không đặt `Domain`). Xoay refresh token đúng cơ chế của service `user`. Có bảo vệ CSRF cho mọi thao tác ghi (kiểm `Origin`/server action). Không lưu token trong `localStorage`.
2. **Dữ liệu đọc**: danh mục, danh sách sản phẩm có lọc và sắp xếp, trang sản phẩm theo SKU (tồn kho thật), trang gian hàng, flash sale, voucher. Nếu gateway thiếu endpoint mà service đã có RPC, thêm endpoint REST vào gateway. Nếu service thiếu khả năng thật sự cần cho luồng mua, thêm tối thiểu và ghi vào báo cáo.
3. **Dữ liệu ghi**: giỏ hàng (service `cart`), áp voucher/xu (`promotion`), checkout với idempotency key sinh ở server, thanh toán COD và VietQR mock (trang chờ thanh toán hỏi trạng thái), đơn hàng. Xử lý lỗi thật: hết tồn, voucher hết lượt, hết phiên, gateway không sẵn sàng (trạng thái error của UI đã có).
4. **Seed**: một lệnh `make seed` nạp vào backend đúng 4 gian hàng, danh mục, 18 sản phẩm (đủ biến thể, giá, tồn, ảnh trong `web/public/products` và `web/public/models`), voucher và các khung flash sale tương ứng với dữ liệu mẫu hiện tại, cùng một tài khoản người mua, một người bán, một admin cho demo. Seed chạy lại nhiều lần không tạo trùng.
5. **Seller center và admin**: nối các màn có RPC sẵn (đơn của gian hàng, sản phẩm và tồn kho, duyệt gian hàng, đối soát). Màn chưa có backend giữ mock.
6. **Phần giữ mock**: tính năng không có backend (hạng hội viên, thẻ quà tặng, tạp chí, chat, trợ lý AI, tìm kiếm nâng cao, đánh giá…) giữ dữ liệu mẫu nhưng tách vào một chỗ rõ ràng, không trộn với dữ liệu thật trong cùng một store. Xoá phần của `StoreContext` đã được backend thay thế; không để hai nguồn sự thật cho giỏ và đơn.
7. **Hạ tầng**: `web/Dockerfile` (output standalone), service `web` trong `docker-compose.yml` (cổng host có override, mặc định không đụng 3111), job CI cho `web` (lint, format, build). `.env.example` cập nhật.
8. **Bảo mật**: mọi trang tài khoản/đơn hàng/seller/admin kiểm phiên ở server; RBAC dựa trên role từ backend, không từ client. Không tin `shop_id`/`user_id` do client gửi. Không lộ lỗi nội bộ của gateway ra UI.

## Ràng buộc
- Giữ nguyên thiết kế UI, art direction, house rules, chữ tiếng Việt cho người mua (không thuật ngữ kỹ thuật). Không thêm dependency runtime vào `web/` nếu `fetch` và API của Next đủ dùng.
- Không sửa `pkg/common/auth` theo hướng nới lỏng. Không công bố lại cổng gRPC. Không chạy `make down` (xoá volume) nếu không cần; nếu phải đổi schema, dùng migration.
- Không `sudo`. Không secret thật. Server web của manager ở cổng 3111 và monitor ở 4777: không kill; dùng cổng khác để kiểm tra.
- Ponytail: giải pháp nhỏ nhất đúng gốc; không dựng framework dữ liệu riêng, không thêm thư viện state.

## Chia việc gợi ý
1. Nền: client gateway phía server, phiên cookie + middleware bảo vệ route, seed, Dockerfile/compose. Các worker khác chờ.
2. Song song: danh mục + sản phẩm + gian hàng + flash sale; giỏ + voucher + checkout + thanh toán; đơn hàng + tài khoản + đăng ký/đăng nhập; seller + admin; endpoint gateway còn thiếu.
3. Một worker QA viết và chạy test đầu-cuối qua web, gửi lỗi về đúng worker.

## Gate trước khi báo xong
- `cd web && npm run lint && npm run format:check && npm run build` sạch; `node /home/tai/.claude/plugins/cache/taste-skill/taste-skill/2.1.4/skills/taste-skill/scripts/lint.mjs web/src` không FAIL mới.
- `make lint`, `make test`, `make e2e` (9/9) vẫn xanh.
- Test đầu-cuối tự động MỚI chạy qua web server (BFF) trên backend thật, một lệnh `make e2e-web`: đăng ký + OTP mailpit → đăng nhập → thêm giỏ từ 2 gian hàng → áp voucher → đặt COD → thấy đơn trong danh sách; một lượt VietQR mock tới trạng thái đã thanh toán; gửi lại checkout cùng idempotency key chỉ ra một đơn; người mua A không đọc được đơn của người mua B (403/404); gọi route ghi không có phiên → 401; request ghi từ `Origin` lạ bị từ chối.
- `grep -rn "localStorage" web/src` chỉ còn tiện ích theo người xem (ghi lý do từng chỗ); không còn giỏ/đơn/token trong localStorage.
- `node /home/tai/.claude/plugins/cache/taste-skill/taste-skill/2.1.4/skills/taste-skill/scripts/inspect.mjs` 0 FAIL ở 1440/768/390 cho trang chủ, danh mục, một trang sản phẩm, giỏ, thanh toán, đơn hàng với dữ liệu thật sau `make seed`.
- Viết `docs/WEB_INTEGRATION_REPORT.md`: bảng route → nguồn dữ liệu (thật / mock + lý do), endpoint gateway và RPC đã thêm, cách chạy, kết quả từng gate, phần chưa xong.
