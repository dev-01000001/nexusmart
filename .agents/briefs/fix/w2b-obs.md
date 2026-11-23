# Đợt 2b – agent `obs` (sở hữu `pkg/common/**` (trừ `auth/auth.go` allowlist), `infra/**`, `.github/**`, `scripts/**` (trừ `agy.sh`), và các dòng khởi tạo trong `services/*/cmd/**/main.go`; phối hợp: chỉ sửa dòng khởi tạo interceptor/middleware/stream, không đụng nghiệp vụ)
Chạy TRƯỚC các agent service (không ai khác sửa services/* lúc này). Ưu tiên mục 2 và 3 để agent service dùng lại; làm xong hai mục đó phải build được cả 10 module.
1. PLT-B3: kiểm collector scrape đủ 9 service (cổng 8889 có dữ liệu thật); PLT-B7 user RED.
2. PLT-B4: interceptor gRPC server log JSON một dòng mỗi RPC (method, code, duration, trace_id, không payload/PII) cho mọi service Go qua `pkg/common`; payment (Rust): extract W3C trace context vào span ở gRPC/HTTP/NATS và log có trace_id. Đầu-cuối: một trace_id từ gateway qua checkout tới payment và event (chứng minh bằng log thật).
3. Hàm chung đảm bảo stream NATS (hợp thêm subject) trong `pkg/common`, chuyển các service Go sang dùng.
4. PLT-B8: `make coverage` (Go order/promotion, Rust payment bằng công cụ sẵn có nếu có; không thêm dependency) in %; chưa đạt 60% thì báo số thật và 5 chỗ thiếu lớn nhất, không viết test vô nghĩa để đủ số.
5. PLT-B9: CI `workflow_dispatch` build image web + quét dependency (govulncheck, cargo audit, npm audit) và image; không thêm trigger.
Test: unit interceptor (che PII), script; kiểm tay ghi lệnh và kết quả.
