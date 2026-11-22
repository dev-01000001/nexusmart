---
name: evaluate-oss
description: Đánh giá và chọn giải pháp OSS/hạ tầng mới cho NexusMart, viết ADR trạng thái "Đề xuất" trước khi cài đặt hay viết code. Dùng khi có nhu cầu thành phần hạ tầng, công cụ, hoặc dịch vụ OSS mới chưa có trong stack hiện tại.
disable-model-invocation: true
---

# Đánh giá OSS và lập ADR

Đầu vào (`$ARGUMENTS`): mô tả nhu cầu (ví dụ: `object storage cho ảnh sản phẩm`, `full-text search tiếng Việt`, `ingress controller`).

Không cài package, không sửa `docker-compose.yml`, không viết code tích hợp cho đến khi chủ dự án duyệt ADR.

## 1. Kiểm tra quyết định và stack hiện có

- [ ] Đọc `docs/adr/README.md` và các file trong `docs/adr/` xem nhu cầu `$ARGUMENTS` đã có ADR nào chưa.
- [ ] Đối chiếu stack hiện có trong `.claude/skills/platform-stack/SKILL.md` và `docs/contracts.md` §1 (`postgres:16-alpine`, `redis:7-alpine`, `nats:2.10-alpine`, `otel/opentelemetry-collector-contrib:0.108.0`): kiểm tra xem thành phần đang có đã đáp ứng đủ chưa (YAGNI) trước khi thêm hạ tầng mới.
- [ ] Ghi nhớ ràng buộc môi trường triển khai: **nơi triển khai chưa chốt** (chưa quyết Kubernetes tự host hay cloud). Không chọn dịch vụ gắn chặt một nhà cung cấp cloud nếu chưa có ADR về hạ tầng triển khai.

## 2. Chọn 2–3 ứng viên và tra web bắt buộc

**Cấm chọn theo trí nhớ.** Trạng thái dự án OSS đổi nhanh (ví dụ: `ingress-nginx` ngừng bảo trì từ 3/2026, MinIO bản community bị lưu trữ từ 2/2026).

Với từng ứng viên (2–3 phương án), **bắt buộc tra web** và ghi lại **URL nguồn** + **ngày tra (`YYYY-MM-DD`)**:

- [ ] **License hiện tại**: tên license chính xác (Apache-2.0, MIT, BSL, SSPL, AGPL...) và lịch sử đổi license gần đây (nếu có). Đánh dấu rủi ro nếu non-OSI hoặc hạn chế thương mại/SaaS.
- [ ] **Tình trạng bảo trì**: ngày commit gần nhất trên repo chính, trạng thái lưu trữ (`archived`) hoặc thông báo `deprecated`/EOL, mức độ hoạt động của maintainer.
- [ ] **Bản phát hành gần nhất**: số phiên bản và ngày phát hành, chu kỳ release và chính sách bản vá bảo mật.

## 3. Đánh giá kỹ thuật và vận hành

So sánh các ứng viên trên 4 trục:

- [ ] **Chạy trên Kubernetes**: Helm chart hoặc Operator chính thức (nguồn, mức bảo trì); có chạy được bằng container đơn trong `docker-compose.yml` cho dev local không. Nhắc lại nơi triển khai chưa chốt.
- [ ] **Client cho Go và Rust**:
  - Go (`go 1.22`, các service Go ở `services/*/`): tên module, tình trạng bảo trì, hỗ trợ `context.Context` và OTel tracing.
  - Rust (`services/payment/Cargo.toml` và AIOps tương lai): tên crate, tương thích `tokio` async, tình trạng bảo trì.
- [ ] **Chi phí vận hành**: RAM/CPU tối thiểu (dev local và production), số node tối thiểu cho HA, yêu cầu persistent storage, cơ chế backup/restore và nâng cấp không downtime.
- [ ] **Khớp stack hiện có**: tích hợp được với Prometheus `/metrics`, OpenTelemetry (`otel-collector`), auth/RBAC hiện tại và quy tắc dữ liệu cách ly từng service (`docs/contracts.md` §1).

## 4. Viết ADR trạng thái "Đề xuất"

- [ ] Đọc mẫu `docs/adr/0000-template.md`.
- [ ] Tìm số thứ tự tiếp theo trong `docs/adr/` (dạng `NNNN-<slug>.md`) và tạo ADR mới với:
  - Trạng thái: **`Đề xuất`** (không đặt "Đã duyệt").
  - Bảng so sánh 2–3 ứng viên gồm: phiên bản mới nhất + ngày, license, tình trạng bảo trì, K8s (Helm/Operator), client Go/Rust, RAM/CPU/HA/backup, **URL nguồn + ngày tra**.
  - Khuyến nghị 1 phương án kèm lý do, nhược điểm chấp nhận được, và hướng thoát nếu cần đổi sau.
- [ ] Cập nhật bảng mục lục trong `docs/adr/README.md` với link tới ADR mới và trạng thái `Đề xuất`.
- [ ] Dừng lại và trình chủ dự án duyệt.

## Gate

- [ ] File ADR mới nằm trong `docs/adr/NNNN-*.md`, đúng cấu trúc `docs/adr/0000-template.md`, trạng thái là `Đề xuất`.
- [ ] Mọi ứng viên đều có URL nguồn và ngày tra web thật (không suy đoán từ trí nhớ).
- [ ] `docs/adr/README.md` đã có dòng trỏ tới ADR mới.
- [ ] Chạy `git status --short`: chỉ có thay đổi trong `docs/adr/`, không có sửa đổi ở `docker-compose.yml`, `Makefile`, `go.mod`, `Cargo.toml` hay thư mục `services/`.
