# 3b-1 – agent `cart` (sở hữu `services/cart/**`)
Theo E3: `buildCart` trả dòng `unavailable` thay vì `continue`; tổng và `total_items` chỉ tính món bán được; giữ `selected` đã lưu; xoá được dòng ẩn; product lỗi tạm (`Unavailable`) KHÔNG được biến mọi món thành "không còn bán" mà phải trả lỗi. Chỉ đọc không ghi Redis. Test cho từng ý, có ca product trả thiếu SKU (`SKU_NOT_FOUND`).
