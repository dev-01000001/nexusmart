# Swarm F – Kubernetes, CI, kiểm thử tải, Playwright, sao lưu
Hợp đồng: `m-all-contract.md`. Spec hàng "Hạ tầng và CI/CD", "Kiểm thử", "Dữ liệu", "Bảo mật".
- `deploy/helm`: chart cho từng service (kể cả service mới A/B/C/E theo cổng hợp đồng), values `staging` và `production`, cấu hình tách khỏi image, Secret không nằm trong repo, readiness/liveness, rolling update, HPA cho gateway, order, search, rollback một lệnh (`make rollback SERVICE=… REV=…`). Cài `helm`, `kubectl`, `kind`, `k6`, `kubeconform` bản user-local (không sudo). Nếu dựng được cụm `kind` thì cài thử staging; nếu không, `helm lint` + `helm template | kubeconform` phải sạch.
- `.github/workflows/`: thêm quét lỗ hổng dependency và image (govulncheck, cargo audit, npm audit, Trivy), lint Helm. CHỈ `workflow_dispatch`, không trigger push/PR.
- `tests/load` (k6): checkout (200 VU, p95 đọc < 300 ms, tạo đơn < 1 s), flash sale (1.000 yêu cầu/100 suất), tìm kiếm. `tests/playwright`: luồng mua chính trên site host (đăng ký, tìm, giỏ, COD, xem đơn). Viết và kiểm tra cú pháp; chạy thật chỉ khi giữ `.runs/stack.lock` (xem brief E), ghi số thật.
- `tools/backup`: sao lưu hằng ngày mọi database (pg_dump trong container), diễn tập khôi phục có script kiểm tra đếm dòng.
- Kiểm tra mọi Dockerfile: multi-stage, user không phải root; lỗi ghi vào `docs/reports/issues.md`.
