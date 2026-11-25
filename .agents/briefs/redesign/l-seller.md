# L – Kênh người bán
Đọc `design.md`, `f-handoff.md`, `.taste-skill/redesign/seller/direction.json`. Đây là màn làm việc: tối giản KHÔNG có nghĩa là thưa; giữ mật độ dữ liệu, cắt chữ thừa.

## Sở hữu
`web/src/features/seller/**`, `web/src/app/(seller)/**` (trừ `template.tsx`). Không sửa `*.server.ts`, `sellerServer.ts`, `siteServer.ts`, `sellerApiTypes.ts`, `siteEditorUtils.ts` logic, test.

## Việc
1. Khung: thanh trái 236px ↔ 64px (nhớ lựa chọn), nhóm mục gọn, mục chọn có nền trượt; ô tìm/nhảy nhanh `⌘K` (dialog lọc danh sách trang có sẵn); mobile thanh dưới 5 mục.
2. Tổng quan: một câu hỏi "Hôm nay cần làm gì?": hàng việc (đơn chờ xác nhận, chờ giao, trả hàng, sắp hết tồn) là các ô bấm được ở đầu; dưới là KPI làm tab cho một biểu đồ lớn; số chạy một lần; rồi bảng đơn mới.
3. Đơn/sản phẩm/khuyến mãi/trả hàng: bộ lọc count-card hoặc tab gạch chạy; bảng hàng 48–56px có ảnh, hành động hiện khi hover/focus (luôn hiện ở cảm ứng), chọn nhiều có thanh hành động trượt lên; chi tiết mở drawer nếu đã có dữ liệu, không thêm API. Skeleton đúng hình bảng.
4. Form sản phẩm: 2 cột, xem trước thẻ sống bên phải, khối thu gọn, thanh lưu dính, ma trận SKU rõ.
5. Trình dựng site: bảng mẫu là các ô lớn có ảnh thật, xem trước chiếm ≥ 60% bề rộng, bảng chỉnh bên trái theo nhóm đóng/mở, đổi thiết bị 390/768/1440 có chuyển động, trạng thái nháp/đã xuất bản rõ. Ô chọn font đã bỏ (F làm).
6. Tài chính: số dư lớn, sổ giao dịch gọn. Chat 3 cột. Báo cáo AI, hồ sơ, tương tác: theo cùng ngôn ngữ, bỏ đoạn giới thiệu.
7. Xoá mọi phụ đề "Quản lý…/Theo dõi…" dưới tiêu đề trang.

## Nghiệm thu
Mọi hành động cũ vẫn làm được (liệt kê theo màn: trước → sau). Không mất cột dữ liệu nghiệp vụ. Chụp 390/768/1440 từng màn.
