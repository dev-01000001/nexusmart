# Swarm G – Web cho các tính năng mới
Hợp đồng: `m-all-contract.md`. Brief UI cũ: `.taste-skill/polish-brief.md` (giữ art direction và house rules).
Không sửa phần của swarm T (`web/src/features/site/`, `web/src/app/(site)/`, `web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/`). Thành phần cần hiện trong site (thanh tìm kiếm có gợi ý, nút chat, khung trợ lý) viết thành component trong `web/src/features/{search,chat,assistant}/` để T chèn vào layout site; ghi tên component và props vào `docs/reports/g-web.md` sớm.
- BFF cho search, chat (WebSocket qua ticket), assistant (proxy SSE), trả hàng, seller chat/AI, admin khiếu nại. CSRF, session, host isolation như hiện có.
- Trang tìm kiếm trên site host (lọc, sắp xếp, gợi ý), trả hàng trong chi tiết đơn, seller: hộp chat, gợi ý trả lời, báo cáo AI, sinh mô tả trong form sản phẩm, thay 3 màn mock; admin: khiếu nại, cấu hình.
- Backend có thể chưa xong: code theo hợp đồng REST, unit test với dữ liệu giả.
- Gate: lint, format, `npm run test:unit`, tsc, build (`NEXT_DIST_DIR=.next-g`), design-taste lint.
