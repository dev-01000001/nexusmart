# 3b-1 – agent `promotion` (sở hữu `services/promotion/internal/events/**`, migration `005_`, phần main nối dây)
Chỉ E1, E2. Đổi `Subscribe` → `QueueSubscribe` cùng durable: chứng minh bằng NATS thật/embedded rằng nâng cấp từ consumer cũ không phát lại stream và không mất event; nếu JetStream không cho đổi deliver group tại chỗ thì chọn cách an toàn và ghi rõ. Không đụng phần khuyến mãi khác.
