# Đợt 1 (shopify-e2e) — luật chung cho mọi swarm

Bối cảnh: chủ dự án muốn NexusMart thành nền tảng kiểu Shopify (mỗi người bán một site gian hàng độc lập). Manager (Claude Code) vừa chạy trọn
hành trình người bán → admin → khách trên Chrome thật (stack audit) và tìm ra lỗi; bạn sửa đúng phần được giao.

## Bắt buộc
- Đọc `AGENTS.md`; làm trong `web/` thì đọc `web/CLAUDE.md` + skill `design-system`; làm trong `services/payment/` thì đọc `services/payment/CLAUDE.md`.
- CHỈ sửa file trong danh sách "Được sửa" của brief bạn. File ngoài danh sách cần đổi thì DỪNG, ghi rõ vào báo cáo cuối (file, lý do, diff đề xuất).
  Có swarm khác đang sửa song song cùng working tree.
- Không commit, không `git stash`, không `git checkout -- <file>` file người khác, không xoá file ngoài phạm vi.
- KHÔNG chạy: `make up`, `make down`, `make seed`, `make e2e`, `make e2e-web`, `docker compose up/build/down/restart` (stack chung, chỉ manager/QA chạy).
  Không đụng `~/nexusmart-prod` (production đang chạy trên máy này).
- Được chạy: `make proto` (chỉ swarm có quyền proto), `make lint`/`make test` theo module mình sửa (vd `cd services/payment && cargo test`,
  `cd services/gateway && go test -race ./...`), `cd web && npm run lint && npm run format:check && npm run test:unit`; `npm run build` thì dùng
  `NEXT_DIST_DIR=.next-<tên-swarm>` và hoàn nguyên `web/tsconfig.json` + `web/next-env.d.ts` nếu build sửa chúng.
- Máy ít RAM, production chạy chung: không chạy song song nhiều build nặng; Go `-p=4`, cargo 4 job (wrapper đã đặt).
- Tiền `int64` VND, cấm số thực. Quyền seller lấy `shop_id` từ claims đã xác minh, không tin client. Log không chứa PII/secret.
- Chữ trên UI: tiếng Việt tự nhiên, không ngành hàng cụ thể (nền tảng phục vụ mọi ngành: gốm, mỹ phẩm, đồ ăn, quần áo...).
- Logic không tầm thường phải có test. Báo cáo cuối: file đã sửa, lệnh kiểm đã chạy + kết quả thật (dán dòng tổng), việc chưa xong, rủi ro.
