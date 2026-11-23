# Đợt 2b – agent `shipping` (sở hữu `services/shipping/**`, `tests/e2e/31_*.go`)
Theo `w2b-plan.md` §1 (freeship shop), §8. Migration `000004_` idempotent. `ShopShippingSettings` thêm `free_ship_enabled/min_order/max`, kiểm giá trị, CalculateFee KHÔNG tự trừ freeship (order/promotion tính), trả cấu hình cho order. Stream NATS §8. Test unit + e2e 31.
