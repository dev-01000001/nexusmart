# Kế hoạch đợt 2b (chạy sau khi 2a nghiệm thu và commit; chủ dự án đồng ý 2026-11-23)

Luật chung: `w2-rules.md` (số migration và số file e2e ghi lại ở mục 6 dưới đây). Mô hình tiền: `plan.md` §2 + `w2-plan.md` §2; mục 1 dưới đây bổ sung. Không thêm hạ tầng (ADR 0004–0008 chưa duyệt).

## 1. Tiền: giảm giá của shop, freeship của shop, phí sàn
- Ký hiệu thêm cho một đơn con: `P_shop` = giảm giá chương trình của shop (UC-S18), `S_shop` = phần phí ship shop chịu (freeship của shop, UC-SHIP06). Cả hai do **seller chịu**: `R = G − F − P_shop − V_shop − S_shop`. Người mua trả `P = G − F − P_shop − V_shop − V_plat − S_free − S_shop − X + Ship`. Vẫn `P + D = R + Ship`, `D = V_plat + S_free + X`.
- Một SKU chỉ một loại giá: flash đang chạy thắng chương trình của shop (UC-S18 ngoại lệ). `F` và `P_shop` không cộng dồn trên cùng dòng hàng.
- Freeship của shop áp khi tổng hàng của shop sau `F + P_shop + V_shop` ≥ `free_ship_min_order_vnd`; `S_shop = min(Ship, free_ship_max_vnd)`. Freeship sàn áp sau, trên phần ship còn lại. R không âm: nếu âm thì cắt `S_shop`.
- Hoa hồng: giữ công thức (bps danh mục trên giá trị dòng sau giảm giá hàng), trần `R`. Phí thanh toán (UC-A09) là `payment_fee_bps` theo phương thức, tính trên `P`, seller chịu, cộng vào `commission_vnd` gửi payment nhưng lưu riêng ở đơn (`payment_fee_vnd`). `C_total = commission + payment_fee ≤ R`.
- Cấu hình phí có `effective_from`; đơn lưu bps hoa hồng từng dòng và bps phí thanh toán tại lúc đặt; đổi cấu hình không ảnh hưởng đơn cũ.

## 2. Khuyến mãi
- Voucher (UC-S05, A07; PRM-B9): `PAUSED`/`ACTIVE`/`DELETED` (xoá mềm), sửa; đã có lượt dùng thì chỉ cho sửa tên, tăng quota/ngân sách, kéo dài `ends_at`; mã không đổi. Voucher tạm dừng không dùng được ngay (kể cả đã lưu ví). Cache/Redis đồng bộ trong cùng thao tác. Phạm vi danh mục cho voucher sàn: chỉ tính trên dòng thuộc danh mục (gồm con cháu).
- Flash (UC-S08, A07; PRM-B10, B11): đăng ký và duyệt đều kiểm giá flash < giá thấp nhất 30 ngày của SKU (lịch sử giá ở `product`), mức giảm tối thiểu của chương trình, `total_slots ≤ available_stock`; hai khung giờ trùng nhau cho cùng SKU → từ chối. Không khoá tồn riêng (checkout đã giữ tồn; `ponytail:`). Admin/seller liệt kê item theo trạng thái.
- Mua flash (UC-B16; PRM-B12): server chỉ nhận suất đang chạy (không nhận sắp mở); đơn có flash dùng hạn thanh toán ngắn `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` (mặc định 5) và không nhận COD; giỏ hàng hiển thị và gửi giá flash đang chạy, hết suất thì báo rõ và cho mua giá thường.
- Chương trình giảm giá shop (UC-S18; PRM-B13): shop tạo chương trình (SKU, giá hoặc %, thời gian, giới hạn mỗi người); kiểm SKU của shop, không trùng flash đã duyệt cùng SKU cùng khung (cảnh báo), giá giảm phải < giá gốc; tăng giá gốc trong 30 ngày trước → cảnh báo và cờ vi phạm. Giữ/nhả giới hạn mỗi người trong saga như voucher shop. Quá giới hạn mỗi người: cả dòng hàng đó tính giá thường và có cảnh báo (preview và đặt hàng giống nhau), không chặn mua; promotion trả `applied_shop_program_item_ids` theo shop, order chỉ áp giá chương trình cho các item trong danh sách (manager quyết 2026-11-23). Hiển thị giá gốc gạch và giá giảm.
- Thông báo `PROMOTION` (PRM-B14 phần còn lại, B15): voucher đã lưu sắp hết hạn 24h (job, mỗi voucher/người một lần), biểu tượng thông báo ở header với số chưa đọc.

## 3. Danh mục sản phẩm
- Event `product.reviewed` qua outbox mới ở `product` (stream thêm `product.>`), notification cho seller (UC-A02, CAT-B3).
- CAT-B8 (xoá danh mục), CAT-B9 (cấm vòng lặp, lọc cha gồm mọi cấp con, chuẩn hoá slug, sửa không reset bps), lịch sử giá SKU (`sku_price_history`, dùng cho flash/giảm giá), `original_price_vnd` lấy từ chương trình giảm giá đang chạy (không nhập tay).
- Web: tìm kiếm tức thời ở header dùng API thật (CAT-B12, WEB-B8), khoảng giá min–max trên thẻ sản phẩm (CAT-B13).

## 4. Báo cáo, tài chính, vận hành
- UC-S06 (seller) và UC-A06 (admin): tính ở server theo khoảng ngày giờ VN (≤ 366 ngày): doanh thu/GMV, số đơn theo trạng thái, tỷ lệ huỷ/trả, top sản phẩm, chuỗi theo ngày; admin thêm người dùng, shop mới; xuất CSV (chống CSV injection).
- UC-A09: cấu hình phí theo danh mục + phí thanh toán, ngày hiệu lực, lịch sử.
- UC-S17: tài khoản ngân hàng shop ở `user` (OTP email, tên phải khớp tên chủ shop hoặc chờ admin duyệt, đổi tài khoản khoá rút 24h), sổ giao dịch seller ở `payment` (từ `ledger_entries`).
- UC-SHIP06 phần còn lại: freeship của shop, `default_weight_grams` dùng ở checkout.
- Admin: danh sách đơn `orphan_shipment_needs_manual_review` và đánh dấu đã xử lý (từ 2a).
- Quan sát: PLT-B3 (collector scrape `/metrics` mọi service + healthcheck collector), PLT-B4 (gRPC request log có `trace_id` ở mọi service Go qua `pkg/common`, span cha–con và trace_id ở payment), PLT-B7 (RED metrics user), PLT-B8 (script đo coverage order/promotion/payment, mục tiêu 60%), PLT-B9 (CI chạy tay: build image web, quét dependency/image; không thêm trigger).

## 5. Web và dữ liệu
- Trang mock (WEB-B5, B7, B10): hội viên, thẻ quà, chat, trợ lý AI, tương tác: gỡ khỏi điều hướng chính; trang còn lại ghi rõ "Bản xem trước, dữ liệu mẫu", không hiện số dư/hạng giả cạnh tài khoản thật, không link ID mock; 404 thật cho route không tồn tại; bảng sổ cái `/admin/doi-soat` có bản cho điện thoại (hiện ẩn ở 390px), favicon.
- Seed: PRM-D1, CAT-D3, đơn mẫu ở nhiều trạng thái (gồm trả hàng), script sinh 20.000–50.000 sản phẩm (tách khỏi `make seed`, chạy riêng, có tham số số lượng và dọn được).

## 6. Số migration và file e2e của 2b
user `0005_`, product `000005_`, order `0010_`, payment `0007_`, promotion `003_`, shipping `000004_`, notification `000004_`. E2E: promotion `26_`, product `27_`, order `28_`, payment `29_`, user `30_`, shipping `31_`, gateway `32_`, notification `33_`; e2e-web `07_`.

## 7. Quy trình
`w2b-contract` tuần tự → manager review → agent song song (promotion, product, order, payment, user, shipping, gateway, notification, observability, web, seed) → manager review từng agent → review độc lập hai lượt cho order/promotion/payment → QA → nghiệm thu stack sạch → commit trên `main`. Push hỏi riêng.

## 8. Luật bổ sung cho mọi agent 2b
- **Stream NATS**: subject chuẩn `["user.>","order.>","payment.>","shipment.>","product.>","promotion.>"]`. Service nào khởi tạo stream: nếu đã có thì đọc danh sách hiện tại, chỉ HỢP THÊM subject thiếu rồi UpdateStream; không bao giờ ghi đè bớt subject; chạy đồng thời nhiều service phải an toàn (đọc-hợp-ghi, lỗi xung đột thì đọc lại). Đưa vào một hàm dùng chung ở `pkg/common` nếu các service Go đang lặp code này (agent `obs` làm, các agent khác gọi); payment (Rust) làm riêng.
- **Giá và tiền**: mọi phép tính giá ở server; không tin giá, % giảm, category_id, free_ship_* do client gửi. `order` tự lấy từ `product`/`promotion`/`shipping`.
- **Quyền**: như 2a (kiểm ở service theo claims, shop_id chỉ từ token seller). Thao tác admin có audit (middleware gateway).
- **Job**: idempotent, nhiều bản, có cờ hoặc khoá; không gửi trùng thông báo.
- Không nới test cũ; test cũ đổi kỳ vọng thì ghi báo cáo cho QA.
