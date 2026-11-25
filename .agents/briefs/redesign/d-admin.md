# D – Quản trị
Đọc `design.md`, `f-handoff.md`, `.taste-skill/redesign/admin/direction.json` nếu có. Màn làm việc, mật độ cao; phải khác Kênh người bán ngay từ khung.

## Sở hữu
`web/src/features/admin/**`, `web/src/app/(admin)/**` (trừ `template.tsx`). Không sửa `adminServer.ts`, `adminSitesServer.ts`, `*Types.ts`, test.

## Việc
1. Khung: thanh trên ngang 2 tầng (nhóm → màn) với gạch chạy, nền `--ink` đậm cho tầng trên, nội dung trên tờ trắng; `⌘K`; mobile sheet.
2. Tổng quan: hàng đợi cần duyệt là nhân vật chính (gian hàng, sản phẩm, khiếu nại, đối soát lệch) dạng danh sách làm ngay; bên phải biểu đồ sức khoẻ một câu hỏi.
3. Duyệt gian hàng/kiểm duyệt: hàng đợi trái + khung xem phải, phím tắt J/K để chuyển, duyệt/từ chối trượt sang mục kế; từ chối buộc có lý do (giữ luật cũ).
4. Đối soát/nhật ký/flash sale/site/khiếu nại/cấu hình: bảng sticky header + cột đầu, bộ lọc gọn một hàng, chi tiết drawer; cấu hình 2 cột nhãn–ô, lưu từng khối, báo đã lưu tại chỗ.
5. Hộp xác nhận hành động nguy hiểm giữ nguyên mức chặt. Xoá mọi đoạn mô tả dưới tiêu đề.

## Nghiệm thu
Như L.
