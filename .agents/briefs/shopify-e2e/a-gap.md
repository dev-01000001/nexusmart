# Brief A — So khoảng trống tính năng NexusMart với Shopify (CHỈ ĐỌC)

## Mục tiêu
Chủ dự án muốn "phát triển tiếp toàn bộ dự án theo hướng Shopify": mỗi người bán có site gian hàng độc lập (đã có). Cần một bảng so
khoảng trống có dẫn chứng code để manager chọn phần làm tiếp. KHÔNG sửa file nào trong repo; chỉ ghi kết quả vào
`.runs/shopify-e2e/gap.md`.

## Việc
1. Liệt kê năng lực cốt lõi của trang quản trị Shopify cho người bán (dùng hiểu biết của bạn, không cần mạng): Products (biến thể,
   tồn kho nhiều kho, collections thủ công/tự động, nhập CSV), Orders (fulfillment, draft order, abandoned checkout, hoàn tiền một phần,
   ghi chú/tag, in hoá đơn/vận đơn), Customers (danh sách, phân khúc, lịch sử mua, ghi chú), Online Store (theme, trình chỉnh theme,
   pages, blog, navigation/menu, preferences SEO, domains), Discounts, Analytics/Reports, Marketing (email, tự động hoá giỏ bỏ dở),
   Finances/payouts, Settings (shipping zones/rates, payments, checkout, notifications templates, staff/quyền, policies).
2. Với mỗi mục: NexusMart Có / Một phần / Thiếu, kèm dẫn chứng `file:dòng` (web `web/src/app/(seller)`, `(site)`, `(admin)`, BFF
   `web/src/app/api`, proto `proto/nexusmart/*/v1`, service `services/*`). Dùng codebase-memory (`search_graph`, `trace_path`) trước grep.
3. Đối chiếu `docs/spec.md` (mục Phạm vi, cột "Ngoài phạm vi") để đánh dấu mục nào đặc tả loại trừ.
4. Đề xuất TOP 8 khoảng trống nên làm tiếp, xếp theo giá trị cho người bán thật / công sức. Mỗi mục: cần đổi proto/migration/service nào,
   trang web nào, ước lượng S/M/L, rủi ro. Ưu tiên mục làm được bằng service sẵn có (không thêm hạ tầng mới).

## Ràng buộc
- Không sửa, không tạo file trong repo (chỉ `.runs/shopify-e2e/gap.md`). Không chạy docker, make, build.
- Viết tiếng Việt, ngắn, bảng là chính. Dẫn chứng phải mở được (đường dẫn thật, số dòng thật).
