# 0b pha A – worker `gateway`: rà khoảng hở API cho web rồi bổ sung tối thiểu

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md` (luật chung, bảng sở hữu, VÙNG CẤM ở `services/order`), `.agents/briefs/sites-0b-web-gateway.md` mục 2, 3, 5, `docs/contracts.md`, skill `add-rpc`, `add-migration`, `checkout-saga`. Chạy song song với `bff` (sửa `web/src`) và `platform` (Docker/compose/seed qua gateway REST): không sửa file của họ.

## Bước 1 – Rà khoảng hở (làm trước, ghi `docs/web-gap-audit.md`)
Với từng route trong `web/src/app` (44 route, 3 khu vực), liệt kê dữ liệu trang đang hiển thị (đọc trang và `web/src/data/*` nó dùng) và endpoint REST gateway sẽ cấp dữ liệu đó. Bảng: route → dữ liệu → endpoint có sẵn / thiếu (có RPC mà gateway chưa mở / service thiếu hẳn) / giữ mock (tính năng ngoài phạm vi backend, theo brief 0b mục 6). Kiểm ít nhất:
- Trang sản phẩm là `/san-pham/[slug]` nhưng REST chỉ có `GET /products/{id}`; proto sản phẩm có `slug`. Cần tra theo slug thế nào.
- Gian hàng `/gian-hang/[slug]`: shop không có slug, không có danh sách gian hàng công khai.
- Danh mục theo slug; lọc/sắp xếp sản phẩm mà `CatalogExplorer` cần so với `ListProducts`.
- Seller center: danh sách sản phẩm của chính shop (kể cả chưa duyệt), đơn của shop, số dư; admin: duyệt gian hàng, duyệt sản phẩm, đối soát, sổ cái, người dùng, audit log.
- Luồng mua: giỏ, preview khuyến mãi, phí ship, checkout, thanh toán VietQR mock (trang chờ), đơn, timeline, huỷ, xác nhận đã nhận, địa chỉ 2 cấp.
- Người mua đăng ký shop xong được duyệt: role có tự đổi sang `seller` không.
Ghi rõ cái nào web CẦN cho brief 0b (luồng mua + màn seller/admin đã có RPC) và cái nào để sau.

## Bước 2 – Bổ sung tối thiểu
- Ưu tiên chỉ sửa `services/gateway` (mở REST cho RPC đã có, ghép từ RPC sẵn có). Chỉ thêm RPC/field/migration ở service khi web thật sự cần và không ghép được; làm theo `add-rpc`/`add-migration` (proto trước → `make proto` → code → `docs/contracts.md` cùng thay đổi; RPC nội bộ dùng token service thì thêm vào `internalRPCs`).
- Lựa chọn slug: chọn cách nhỏ nhất đúng (ví dụ tra sản phẩm theo slug qua RPC/field sẵn có; với gian hàng, cân nhắc giữa thêm slug cho shop bằng migration `user` mới và dùng `id` trong URL) và ghi lý do trong `docs/web-gap-audit.md`. Endpoint công khai chỉ trả dữ liệu công khai (shop đã duyệt, sản phẩm đã duyệt), không lộ email/điện thoại/giấy phép.
- Endpoint seller lấy `shop_id` từ claims đã xác minh, không nhận từ client. Endpoint admin kiểm role ở gateway và service.
- KHÔNG sửa vùng cấm ở `services/order` (xem luật chung). Nếu luồng web cần đổi ở đó, ghi vào báo cáo.
- Rebuild service đã sửa trong stack worktree: `docker compose up -d --build <service>` ở gốc worktree. Không rebuild `web`.

## Tiêu chí nghiệm thu
- `docs/web-gap-audit.md` đủ 44 route.
- Mỗi endpoint mới có test Go (handler gateway và/hoặc service), kể cả ca phân quyền: không token → 401, sai role → 403, seller A không đọc được dữ liệu shop B, endpoint công khai không trả trường riêng tư.
- `docs/contracts.md` cập nhật mọi endpoint/RPC/field mới.
- `make proto` (nếu đổi proto), `make lint`, `make test` sạch; `make e2e` 9/9 trong stack worktree (chạy ở gốc worktree). Dán dòng kết quả.
- `curl` thật trên `http://localhost:38080` cho từng endpoint mới (dán status + một phần body). Stack có thể chưa có dữ liệu cho tới khi `platform` chạy `make seed`; khi đó kiểm bằng dữ liệu bạn tự tạo qua REST hoặc ghi rõ.
