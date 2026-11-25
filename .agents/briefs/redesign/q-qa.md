# Q – QA (sau khi mọi mảng xong)
Chỉ mảng này được chạy stack `nexusmart-redesign` và rebuild container `web` của nó. Đọc `design.md` mục 7, 8.
1. `make e2e-web` hai lần liên tiếp và Playwright `tests/playwright` trên stack `nexusmart-redesign` (cổng host +50000, `SEED_CATALOG_COUNT=0`). Đỏ: xác định lỗi thật hay môi trường, ghi đúng đầu ra, sửa ở file UI gây lỗi, KHÔNG sửa test.
2. Quét mọi route (root, 4 site, seller, admin; đăng nhập bằng tài khoản seed) ở 360/390/768/1440: tràn ngang, lỗi console, 4xx/5xx, font lạ, ảnh vỡ, tương phản < 4.5, chữ < 13px, tiêu đề > 2 dòng, mục tiêu chạm < 44px ở 390, phần tử vẫn ẩn sau khi cuộn hết trang, reduced-motion.
3. Bàn phím: tab qua header, hero, cửa hàng, PDP, giỏ, thanh toán, dialog (bẫy focus, Esc, trả focus).
Báo cáo `docs/reports/redesign-qa.md`: lệnh + đầu ra thật, lỗi theo file:dòng, đã sửa/chưa.
