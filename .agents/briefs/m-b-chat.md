# Swarm B – Chat service
Hợp đồng: `m-all-contract.md`. Spec hàng "Chat", use case chat trong `docs/spec.md`.
- `services/chat` (Go, coder/websocket, Redis pub/sub để nhiều instance, Postgres lưu tin): hội thoại người mua–gian hàng (gian hàng của site), tin text, ảnh (URL đã upload, kiểm tra loại và kích thước), product card, mã đơn; đồng bộ lại khi mất kết nối (theo cursor); trả lời tự động theo cấu hình của gian hàng; đánh dấu đã đọc.
- Quyền: chỉ người mua trong hội thoại và nhân viên của gian hàng đó đọc được (kiểm tra ở gateway và service).
- Test: unit, integration; bài thử 1.000 kết nối đồng thời trên một instance (script Go hoặc k6, ghi số thật); 2 instance nhận tin của nhau qua Redis.
