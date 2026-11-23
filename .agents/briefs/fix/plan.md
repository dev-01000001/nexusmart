# Kế hoạch sửa sau rà soát nghiệp vụ 2026-11-23

Nguồn: 8 báo cáo `docs/audit/{usr,cat,ord,pay,prm,shp,plt,web}.md` (agy rà trên stack `nexusmart-audit`, HEAD `0db2cdd`). Manager đã tự kiểm lại mọi lỗi P0 và phần lớn P1 bằng DB/curl/code: `verify-log.md` (có hạ mức một số mục). Ma trận 100 UC: `uc-matrix.tsv`. Danh sách lỗi thô: `bugs-raw.txt`.

## 1. Kết quả rà soát (trạng thái xấu nhất giữa các worker nghiệp vụ)

| Ưu tiên | ĐỦ | MỘT PHẦN | LỖI | THIẾU | Tổng |
|---|---|---|---|---|---|
| Cao | 2 | 12 | 16 | 6 | 36 |
| Trung bình | 0 | 13 | 5 | 27 | 45 |
| Thấp | 0 | 0 | 2 | 17 | 19 |

- ĐỦ: UC-B05 (giỏ hàng), UC-PAY01 (VietQR).
- THIẾU ở mức Cao đều là phân hệ chưa có service: UC-AI01, AI02, AI13, AI14 (trợ lý AI, tìm kiếm ngữ nghĩa), UC-OPS02, OPS08 (AIOps).
- Mốc trước khi sửa: `make lint`, `make test`, `make e2e` 11/11, `make e2e-web` 13/13 đều xanh. Bộ test hiện tại không bắt được lỗi tiền nào bên dưới.

## 2. Mô hình tiền (manager chốt, căn cứ `docs/spec.md` UC-B15, B17, B25, PAY02, PAY04, PAY05)

Cho một đơn con (một shop) trong một lần checkout:
- `G` = `subtotal_vnd`; `F` = `flash_sale_discount_vnd`; `V_shop` = `shop_voucher_discount_vnd` → **seller chịu** (seller tự đăng ký giá flash ở UC-S08 và tự tạo voucher shop).
- `V_plat` = `platform_voucher_discount_vnd`, `S_free` = `freeship_discount_vnd`, `X` = `coins_discount_vnd` → **sàn chịu** (voucher sàn và mã freeship do admin tạo, xu do sàn phát).
- `Ship` = `shipping_fee_vnd` (phí đầy đủ trước freeship).
- Người mua trả `P = G − F − V_shop − V_plat − S_free − X + Ship` (= `total_payable_vnd`, ≥ 0).
- Doanh thu hàng của seller `R = G − F − V_shop`. Phần sàn trợ giá `D = V_plat + S_free + X`. Luôn có `P + D = R + Ship`.
- Hoa hồng `C`: giữ cách tính hiện tại (`checkout.go` `applyDiscountsAndTotals`: theo giá trị dòng sau giá flash, bps theo danh mục hoặc `ORDER_DEFAULT_COMMISSION_BPS`), chỉ đổi trần thành `R`.
- Allocation gửi sang payment: `subtotal_vnd = R`, `commission_vnd = C`, `shipping_fee_vnd = Ship`, `platform_discount_vnd = D` (trường mới). Tổng `amount_vnd` của payment = Σ(R + Ship − D) = Σ P.
- Quyết toán một đơn con khi completed (bút toán cân): Nợ `escrow_liability` (P đã thu − đã hoàn), Nợ `platform_promo_expense` D (tài khoản mới, loại EXPENSE), Có `seller_pending:<shop>` (R − C − phần hàng đã hoàn), Có `platform_fee_revenue` C, Có `shipping_fee_payable` Ship. Giữ cơ chế pending → available hiện có; KHÔNG thêm thời gian giữ tiền mới ở đợt này.
- Đơn 0₫ (P = 0): vẫn tạo payment (amount 0, `succeeded` ngay, không QR) với allocation đủ R, C, Ship, D để seller vẫn nhận R − C.
- COD (UC-PAY02 "đơn confirmed với khoản COD chờ thu"): lúc đặt, payment COD ở trạng thái chờ thu (không phải `succeeded`), chưa ghi `cod_receivable`. Khi `order.delivered`: Nợ `cod_receivable` / Có `escrow_liability` P, payment → `succeeded`. Huỷ trước khi giao: payment → `cancelled`, không còn bút toán treo. Chỉ hoàn tiền cho payment đã thu tiền thật (VIETQR `succeeded`, hoặc COD đã giao).
- Hoàn tiền (UC-PAY04): tổng hoàn của một đơn con ≤ số người mua đã trả cho đơn con đó. Quyết toán escrow trừ phần đã hoàn. Hoàn sau khi đã quyết toán: trừ `seller_pending` rồi `seller_available` của đúng shop đó, không đủ thì từ chối (409). Không bao giờ rút `escrow_liability` vượt số đã nạp cho đơn đó.
- Thanh toán lại (UC-B25): chỉ khi đơn còn chờ thanh toán và chưa quá hạn. `orders.payment_id` và `payment_expires_at` của mọi đơn con trong nhóm cập nhật theo payment mới; payment cũ `superseded`. Mỗi nhóm đơn có đúng một giao dịch thành công. Giao dịch thành công muộn (payment đã `superseded`, hoặc nhóm đơn đã huỷ) → tự tạo refund `pending` và không đổi trạng thái đơn, không ghi đè `orders.payment_status`.
- Huỷ một đơn con (UC-B15 ngoại lệ "đơn bị huỷ: hoàn lại voucher và xu"): hoàn ngay xu đã phân bổ cho đơn con đó, lượt voucher shop của shop đó, suất flash sale của các SKU trong đơn con. Voucher sàn chỉ hoàn khi mọi đơn con trong nhóm bị huỷ. Đơn `RETURNED`: hoàn xu đã dùng của đơn con.
- Xu thưởng khi completed: tính trên tiền người mua thực trả cho hàng của đơn con (`R − V_plat − X`, không âm, không gồm phí ship); đơn 0₫ được 0 xu.

## 3. Nguyên nhân gốc (đã gộp trùng giữa các báo cáo)

| Gốc | Mã lỗi | Mức | Chủ sửa |
|---|---|---|---|
| R1. Allocation trừ giảm giá do sàn chịu vào doanh thu seller; đơn 0₫ không tạo payment | PAY-B3, PRM-B1, ORD-B1 | P0 | order + payment (+ contract) |
| R2. COD bị đánh dấu `succeeded` lúc đặt; huỷ/hoàn COD không đảo sổ; hoàn tiền được trên COD chưa thu; email "thanh toán COD thành công" | PAY-B4, ORD-B2, ORD-B8, PRM-B5 | P0 | payment + order + notification |
| R3. Thanh toán lại không kiểm trạng thái đơn, không đồng bộ `payment_id`/hạn; webhook muộn ghi đè `payment_status` | PAY-B1, PAY-B5, ORD-B5, PAY-B6 | P0/P1 | order + payment + gateway (+ contract) |
| R4. Quyết toán escrow không trừ phần đã hoàn; hoàn sau quyết toán rút escrow đơn khác | PAY-B2 | P0 | payment |
| R5. Reservation khuyến mãi giữ theo cả nhóm checkout; huỷ một đơn con/`RETURNED` không hoàn xu, voucher shop, suất flash | PRM-B3, ORD-B3 | P1 | promotion + order (+ contract) |
| R6. Luật chặn bom hàng COD vô hiệu (không ai tăng `cod_rejected_count`) | ORD-B6, SHP-B5 (phần COD) | P1 | user + order (+ contract) |
| R7. Phí ship hiển thị lệch phí thật (web gửi `from_province_code: ""`, shipping bỏ `shop_id`) | SHP-B1, WEB-B1 | P1 | shipping + web |
| R8. Audit log thiếu cho thao tác quản trị ngoài `user`, IP sai | USR-B4, CAT-B7, PLT-B11 | P1 | gateway (ghi tập trung cho `/api/v1/admin/*`) |
| R9. Rate limit IP tin `X-Forwarded-For` từ bất kỳ ai | PLT-B1 | P1 | gateway |
| R10. Tìm kiếm không dấu không chạy | CAT-B1 | P1 | product |

Các lỗi P1/P2 khác của đợt 1 nằm trong brief từng service (`w1-*.md`).

## 4. Các đợt

### Đợt 1 (XONG 2026-11-23, chưa commit; nghiệm thu ở verify-log.md): tiền, checkout, bảo mật, dữ liệu sai
- Pha 1A, tuần tự: `w1-contract.md`. Đổi proto tối thiểu, `make proto`, allowlist, `docs/contracts.md`.
- Pha 1B, song song theo service, mỗi agent sở hữu riêng thư mục của mình: `w1-order`, `w1-payment`, `w1-promotion`, `w1-user`, `w1-gateway`, `w1-shipping`, `w1-product`, `w1-notification`, `w1-web`.
- Pha 1C, manager tích hợp: rebuild toàn bộ, `make lint test e2e e2e-web`, tái hiện lại từng lỗi P0/P1 trên stack sạch, review diff. Lỗi thì gửi lại đúng agent (`--conversation`).

### Đợt 2 (sau khi đợt 1 xanh): khoảng trống P2 trên UC Cao/Trung bình
- Trả hàng đầu-cuối: UC-B09, S10, A05, SHIP08 (vận đơn trả, SHP-B4), huỷ vận đơn SHIP07 (SHP-B3), giao thất bại SHIP05, cấu hình vận chuyển shop SHIP06, admin cấu hình hãng A12.
- Khuyến mãi: bật/tắt/sửa/xoá voucher, phạm vi danh mục (PRM-B9); kiểm giá và tồn khi đăng ký flash sale, khoá tồn (PRM-B10); liệt kê item flash chờ duyệt (PRM-B11); giá flash trong giỏ, chặn suất sắp mở (PRM-B12, WEB-B9); chương trình giảm giá của shop UC-S18 (PRM-B13); thông báo đơn cho seller (PRM-B14).
- Catalog: event và thông báo khi duyệt sản phẩm (CAT-B3, cần outbox ở `product`); lỗi cây danh mục (CAT-B8, B9); giá so sánh `original_price_vnd` (CAT-D1); tìm kiếm tức thời ở header dùng dữ liệu thật (CAT-B12, WEB-B8).
- Payment: tự completed sau 7 ngày kể từ khi giao (UC-B17 ngoại lệ, ORD-B9); thời gian giữ tiền (PAY-B7, cần chốt số ngày); đối soát lệch múi giờ (PAY-B8); hàng đợi giao dịch không khớp + UI (PAY-B9, UC-A14); tất toán `refund_payable` (PAY-B11); cấu hình phí sàn UC-A09; tài khoản ngân hàng seller + OTP UC-S17.
- User: thu hồi phiên ngay khi khoá/hạ quyền (USR-B1); dò tài khoản (USR-B8); quên mật khẩu UC-B11.
- Quan sát: log có `trace_id` ở mọi service, span của payment (PLT-B4); RED metric cho `user` (PLT-B7); header bảo mật, 413 (PLT-B6); báo cáo doanh thu seller và thống kê sàn tính ở server (PLT-B10, UC-S06, A06); coverage 60% cho order/promotion/payment (PLT-B8).
- Web: gỡ hoặc ghi rõ "dữ liệu mẫu" cho các trang mock còn lại (hội viên, thẻ quà, chat, trợ lý AI...), link 404 (WEB-B5, B7, B10).
- Dữ liệu: suất flash seed vượt tồn kho (PRM-D1); hoa hồng theo shop lệch (CAT-D3); seed thêm đơn mẫu; script sinh 20.000–50.000 sản phẩm cho kiểm thử tải (CAT-D2).

### Đợt 3: ADR cho phân hệ mới (chỉ tài liệu, chủ dự án duyệt rồi mới code; AGENTS.md luật 11)
Search service + hybrid/pgvector (UC-B03, B12, AI02); Chat realtime (UC-B14, S09); trợ lý AI: nhà cung cấp LLM, RAG, guardrail, eval (UC-AI01..14); giám sát + AIOps: Prometheus/Grafana/Alertmanager/Telegram, engine Rust (UC-OPS01..09); Kubernetes/Helm/HPA, sao lưu (OPS05, 06, 09).

### Không làm
- UC mức Thấp (spec: chỉ đặc tả).
- Dòng Shopify ngoài phạm vi đặc tả: POS, Markets, B2B, vay vốn, app store/extension, theme editor, đa tiền tệ, trả góp, subscription, gift card, combo/mua kèm, hạng thành viên (spec tab Phạm vi, dòng "Ngoài phạm vi" và "Mọi tính năng không có trong tab Use case").
- WEB-B2/B3: sửa bằng cách GỠ giao diện combo, deal sốc giả và giảm theo bậc (spec dòng 29: combo, mua kèm ngoài phạm vi), không cài thêm.

## Quyết định manager 2026-11-23 (vòng 3)
- Huỷ một đơn con VietQR chưa thanh toán = huỷ cả nhóm checkout (một QR cho cả nhóm). Gỡ hồi quy do P0-3 và ca cộng tồn ảo review2-O P1-4b. Chủ dự án có thể đổi.
