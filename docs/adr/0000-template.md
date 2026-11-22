# ADR-XXXX: <Tiêu đề ngắn gọn>

- **Trạng thái**: Đề xuất | Đã duyệt | Bị thay bởi [ADR-YYYY](YYYY-slug.md)
- **Ngày**: YYYY-MM-DD

## Bối cảnh

- Hiện trạng trong repo: file chính kèm `wc -l`, cơ chế đang chạy, test/e2e phủ.
- Vấn đề hoặc quyết định cần chốt, ràng buộc kiến trúc (gRPC, Go/Rust, bảo mật, nơi triển khai).

## Ứng viên

- **Phương án 1 (hiện có / tự viết)**: phạm vi và giới hạn.
- **Ứng viên OSS A / B**: license, trạng thái bảo trì, bản phát hành gần nhất + ngày, SDK Go/Rust.

## Tiêu chí

Độ phức tạp vận hành, rủi ro chuyển đổi, khớp microservice gRPC + Go/Rust, chi phí vận hành, bảo mật, điều kiện kích hoạt chuyển.

## Quyết định

- **Đề xuất**: <chọn phương án + lý do ngắn>.
- **Điều kiện kích hoạt chuyển đổi**: <ngưỡng đo được để xem xét lại>.
- **Phụ thuộc**: <ví dụ: ADR nơi triển khai>.
- **Chủ dự án quyết**: (trống)

## Hệ quả

- Tích cực / tiêu cực của từng lựa chọn (đánh đổi vận hành, chi phí, bảo trì, bảo mật).

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: YYYY-MM-DD
- **Nguồn**:
  - <Tên ứng viên>: <URL chính thức>
