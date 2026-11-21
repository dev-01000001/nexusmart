# Swarm C – AI assistant
Hợp đồng: `m-all-contract.md`. Spec hàng "Trợ lý AI".
- `services/assistant` (Go): RAG trên sản phẩm của đúng gian hàng (truy hồi qua gRPC search), function calling: tra đơn, thêm giỏ, báo cáo cho người bán, hỏi đáp chính sách (tài liệu chính sách trong repo), chuyển tiếp nhân viên (tạo hội thoại chat). Tool gọi gateway bằng JWT của người dùng, không có quyền riêng. SSE, token đầu tiên < 2 s (đo với mock và ghi cách đo live).
- Tóm tắt đánh giá, sinh mô tả sản phẩm, gợi ý trả lời chat cho người bán.
- Hạn mức token theo người dùng mỗi ngày (Redis), giới hạn chi phí theo ngày, cache câu trả lời lặp lại.
- `evals/assistant`: eval set khoảng 100 câu (tiếng Việt), đo tỷ lệ trả lời có căn cứ và tỷ lệ bịa sản phẩm; bộ test prompt injection (≥ 30 ca, gồm injection trong mô tả sản phẩm và trong tin chat). Runner chạy được với mock và với live khi có key.
