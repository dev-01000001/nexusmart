# Swarm brief – NexusMart UI đợt 2: bổ sung tính năng ngang Coolmate

Bạn là coordinator của swarm. Claude Code là manager và sẽ review bằng skill design-taste-frontend. Đọc `AGENTS.md`, `.taste-skill/swarm-brief.md` (brief đợt 1, mọi ràng buộc vẫn giữ nguyên) và `web/DESIGN_REPORT.md` (hiện trạng) trước.

## Mục tiêu
`web/` đã có 28 route đạt gate. Chủ dự án yêu cầu: mọi tính năng người dùng mà Coolmate có thì NexusMart cũng phải có. Ba file nghiên cứu liệt kê 55 tính năng:
- `.taste-skill/research/browse.md` (điều hướng, tìm kiếm, danh sách, nội dung)
- `.taste-skill/research/product.md` (trang sản phẩm)
- `.taste-skill/research/checkout.md` (giỏ, thanh toán, sau mua, tài khoản, thành viên, hỗ trợ)

## Việc phải làm
1. Lập bảng đối chiếu 55 tính năng với `web/` hiện tại: đã có, có một phần, chưa có. Ghi vào `web/FEATURE_MATRIX.md`.
2. Làm UI (dữ liệu mock) cho mọi tính năng "một phần" và "chưa có", ở phía người mua. Với tính năng cần dữ liệu theo gian hàng hoặc cần kiểm duyệt, thêm màn quản lý tương ứng ở seller center hoặc admin, theo cột "áp dụng cho sàn đa gian hàng" trong file nghiên cứu.
3. Các mục đặc tả xlsx ghi ngoài phạm vi (combo, mua kèm, hạng thành viên, thẻ quà tặng) vẫn làm UI mock theo yêu cầu mới của chủ dự án. Không làm form thẻ ngân hàng hay thanh toán thật.

## Ràng buộc
- Chỉ sao chép chức năng. KHÔNG dùng tên, logo, câu chữ, ảnh, tên chương trình (ví dụ tên câu lạc bộ thành viên, tên đơn vị điểm thưởng) hay bố cục của Coolmate. Đặt tên riêng cho NexusMart. Chính sách (số ngày đổi trả, ngưỡng freeship) là nội dung mẫu của NexusMart, liệt kê trong báo cáo.
- Giữ nguyên art direction, tokens, typeface, house rules và các component dùng chung. Không thêm dependency. Tái dùng component có sẵn trước khi viết mới.
- Không bịa đánh giá của khách thật, số liệu kinh doanh, logo báo chí. Đánh giá mẫu phải nằm trong danh sách nội dung mẫu.
- Không làm hỏng 28 route hiện có. Chỉ sửa trong `web/`. Một swarm khác đang làm `services/`, `proto/`, `docs/`: không chạm vào.

## Gate trước khi báo xong
- `cd web && npm run lint && npm run format:check && node scripts/check-data.mjs && npm run build` sạch.
- `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs web/src` sạch.
- `inspect.mjs` 0 FAIL ở 1440, 768, 390 cho mọi route mới và cho trang chủ, danh mục, một trang sản phẩm `/san-pham/<slug>`, giỏ hàng, thanh toán, tài khoản; `--app` cho màn seller và admin mới. Mở ảnh chụp và sửa lỗi nhìn thấy.
- Cập nhật `web/FEATURE_MATRIX.md`: mỗi tính năng có trạng thái cuối, route, và ghi rõ phần nào chưa làm kèm lý do. Cập nhật `web/DESIGN_REPORT.md`.
