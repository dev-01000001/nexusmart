# Đợt 3 – agent `adr`: đề xuất kiến trúc cho các phân hệ chưa có

Mục tiêu: chủ dự án có đủ thông tin để duyệt trước khi code (AGENTS.md luật 11). CHỈ viết tài liệu, không sửa code, không cài gì. Không git write.

Đọc: `docs/spec.md` (tab Phạm vi, Use case, Công nghệ), `docs/adr/README.md`, `docs/adr/0000-template.md`, các ADR 0001–0003 (văn phong và mức chi tiết), `.agents/briefs/fix/plan.md`, `docs/audit/plt.md` (mục 0 và bảng UC), `docs/audit/cat.md` (CAT-B1, CAT-F5, CAT-F6). Dùng skill `evaluate-oss` và `platform-stack` (stack đã chốt: Postgres 16, Redis 7, NATS JetStream 2.10, OTel Collector, Mailpit).

## Sở hữu
`docs/adr/0004-*.md` … `docs/adr/0008-*.md`, `docs/adr/README.md` (chỉ thêm dòng chỉ mục).

## Viết 5 ADR, trạng thái "Đề xuất"
1. `0004` Search service + tìm kiếm hybrid (UC-B03, B12, AI02; spec: không dấu, gợi ý từ khoá, index qua event Product < 5 giây, 20.000–50.000 sản phẩm, p95 < 200 ms, pgvector). So sánh ít nhất: Postgres FTS + pg_trgm + pgvector, Meilisearch, Typesense, OpenSearch.
2. `0005` Chat thời gian thực (UC-B14, S09; spec: WebSocket, Redis pub/sub nhiều instance, lưu tin, đồng bộ lại khi mất kết nối, 1.000 kết nối/instance; gửi ảnh, product card, mã đơn; trả lời tự động). Lưu trữ ảnh cần năng lực mới (object storage): nêu lựa chọn.
3. `0006` Trợ lý AI (UC-AI01..AI14; spec: RAG trên dữ liệu sản phẩm, function calling bằng JWT người dùng, SSE token đầu < 2 giây, eval ~100 câu, chống prompt injection, hạn mức token theo người dùng; ngoài phạm vi: fine-tune, tự host LLM). So sánh nhà cung cấp LLM qua API (tối thiểu Anthropic Claude, Google Gemini, OpenAI) và mô hình embedding cho tiếng Việt; chi phí ước tính cho demo; cách giữ khoá API ngoài repo.
4. `0007` Giám sát và AIOps (UC-OPS01..OPS09; spec: RED theo service, dashboard theo luồng, cảnh báo Telegram, SLO dạng code, z-score/EWMA, RCA bằng LLM, runbook có allowlist và giới hạn số lần/giờ, tiêm lỗi). Hiện collector `:8889` rỗng, service tự có `/metrics` (PLT-B3): đề xuất Prometheus/Grafana/Alertmanager (hoặc tương đương) và vị trí engine Rust.
5. `0008` Triển khai Kubernetes, CI/CD, sao lưu (UC-OPS05, 06, 09; spec: staging + production, Helm theo service, readiness/liveness, HPA, rollback một lệnh, sao lưu hằng ngày + diễn tập khôi phục; CI chỉ `workflow_dispatch` theo AGENTS.md). Môi trường demo cục bộ (kind/k3d) so với cloud.

Mỗi ADR: bối cảnh (UC, tiêu chí đo trích từ spec), lựa chọn (bảng so sánh: độ khớp tiêu chí, độ phức tạp vận hành, chi phí, giấy phép, mức độ trưởng thành), đề xuất, hệ quả, kế hoạch triển khai chia task nhỏ có tiêu chí nghiệm thu đo được, câu hỏi chủ dự án cần trả lời. Ngắn gọn, tiếng Việt, không văn mẫu.

## Câu trả lời cuối
Danh sách file đã tạo, mỗi ADR một dòng: lựa chọn đề xuất và câu hỏi mở quan trọng nhất.
