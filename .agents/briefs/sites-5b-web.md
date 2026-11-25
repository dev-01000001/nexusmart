# Swarm 5B – web: host custom, chuyển hướng, màn tên miền
Đọc `.agents/briefs/sites-5-contract.md` (hợp đồng, bắt buộc theo đúng) trước.
Sở hữu: `web/` (middleware, `lib/site`, `lib/server`, BFF `/api/seller/site/domains/**`, `/api/admin/sites/**/domains/**`, màn seller "Tên miền", màn admin, SEO).
Cấm: `services/`, `proto/`, `tests/`, `tools/`, `docs/` (trừ mục "5B" trong `docs/PHASE5_REPORT.md`).
Backend có thể chưa xong khi bạn làm: code theo REST trong hợp đồng; unit test với dữ liệu giả.
Việc: classifyHost loại custom + cache âm có giới hạn; host custom chạy như host site; 308 subdomain → custom khi custom là primary (chỉ trang GET); canonical/sitemap/robots theo primary_host; BFF seller/admin tên miền (CSRF, assertSellerSession/assertAdminSession); màn seller Tên miền (thêm, bảng DNS có nút sao chép, trạng thái, kiểm tra lại, xóa có xác nhận) và phần admin. Chữ UI tiếng Việt cho người bán, không thuật ngữ nội bộ.
Gate: `cd web && npm run lint && npm run format:check && npm run test:unit && npx tsc --noEmit && NEXT_DIST_DIR=.next-5b npm run build` sạch (xóa `.next-5b`, khôi phục tsconfig/next-env). Thêm unit test cho classifyHost (IP, localhost, custom hợp lệ, ký tự lạ, cổng) và quyết định 308. Không chạy make e2e/seed/e2e-web, không rebuild container.
