# 3b-1 – agent `product` (sở hữu `services/product/**`)
Theo E5–E14, `w3b-design-a4.md` §2–3. Migration `000008_`. sqlc sinh lại, không sửa tay file sinh.
1. Đánh giá: `CreateReview`, `ListProductReviews`, `ListOrderReviews`; client `order` và `user` dial lười, timeout, mạch hở; lỗi `order` → `Unavailable`, không ghi. Đua 10 goroutine cùng dòng hàng trên Postgres thật: 1 thành công, 9 `AlreadyExists`, `review_count` +1, 1 outbox.
2. Từ cấm: gói `internal/moderation` (E9, hàm thuần có bảng test lách và bảng test không chặn), CRUD, xem trước, quét lại theo lô. Danh mục cấm kể cả danh mục con.
3. E13: căn cứ "từng công khai" trong DB.
4. Mọi nơi trả `Product` công khai có `rating_avg_x100`/`review_count` (một nguồn, không N+1).
Kiểm: gofmt, lint, build, Postgres thật `-race` cả gói ×2 (0 SKIP), bài mới ×5; mã thoát thật.
