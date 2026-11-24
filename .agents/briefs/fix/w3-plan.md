# Kế hoạch đợt 3 (lập 2026-11-23, chốt 2026-11-24)

Nền: đợt 2 (2a + 2b) đã nghiệm thu và push lên `main` (`44e6585..1b1a385`). 3a đang chạy (`w3a-plan.md`). ADR 0004–0008 được chốt theo uỷ quyền (§1.1), trạng thái trong `docs/adr/` đổi sau khi tra lại phiên bản.

Đợt 3 chia hai đường:
- **Đường A**: không cần ADR, chạy được ngay khi chủ dự án đồng ý.
- **Đường B**: các phân hệ mới theo ADR 0004–0008, mỗi phân hệ chỉ bắt đầu sau khi ADR của nó được duyệt (AGENTS.md luật 11).

## 1. Chủ dự án cần trả lời

**Đã chốt 2026-11-24.** Chủ dự án nhắn nguyên văn: "mấy câu hỏi bạn từ đề xuất tự làm luôn đi". Manager hiểu là uỷ quyền chốt theo đề xuất và làm tiếp; chủ dự án chưa tự đọc duyệt từng ADR. Bảng dưới giữ câu hỏi gốc, câu chốt ở §1.1.

| # | Câu hỏi | Giả định | Chặn việc gì nếu chưa trả lời |
| --- | --- | --- | --- |
| 1 | Kubernetes chạy ở đâu: `k3d` trên máy, VPS `k3s`, hay cloud? (ADR-0008) | `k3d`, chi phí 0 | B5 (T2–T4), bài tiêm lỗi của B4. Không chặn B1–B3 vì các phân hệ này chạy được trên Compose |
| 2 | Có thêm container Meilisearch không, hay chỉ dùng Postgres (`pg_trgm` + `pgvector`)? (ADR-0004) | Meilisearch + `pgvector` | B1 |
| 3 | Object storage: SeaweedFS hay Garage? (ADR-0005) | SeaweedFS | B2, tải giấy tờ KYC, ảnh sản phẩm, đích sao lưu của B5 |
| 4 | LLM: nhà cung cấp nào, trần ngân sách bao nhiêu USD? (ADR-0006) | OpenAI, ước tính của ADR 10–12 USD | Chạy thật và eval của B3. Code và CI dùng mode `mock`, không cần khoá |
| 5 | Grafana/Loki/Tempo hay VictoriaMetrics/VictoriaLogs? (ADR-0007) | Grafana/Loki/Tempo | B4 |
| 6 | Ví điện tử sandbox: ZaloPay, MoMo hay VNPay? (UC-B07, B25) | ZaloPay: đặc tả ghi có sandbox với app và key test; hai cổng kia cần đăng ký merchant | Phần chạy sandbox thật của A6. Adapter và mock làm trước được |
| 7 | UC-PAY02 đòi SĐT đã xác minh trước khi đặt COD, nhưng hệ thống không có nhà cung cấp SMS. Dùng SMS mock (mã vào Mailpit) được không? | SMS mock qua `notification` | Một phần của A5 |
| 8 | Đăng nhập Google (UC-B11): có OAuth client không? | Không, để ngoài đợt 3 | Chỉ UC-B11 |

### 1.1 Câu chốt (manager, theo uỷ quyền 2026-11-24)

| # | Chốt | Cổng còn giữ (uỷ quyền không bỏ) |
| --- | --- | --- |
| 1 | `k3d` trên máy, chi phí 0 (ADR-0008) | Không thuê VPS/cloud |
| 2 | Meilisearch + `pgvector` (ADR-0004), đúng công cụ `docs/spec.md` nêu cho Search service | Đổi image Postgres thử trên bản sao volume trước; tắt Meilisearch vẫn tìm được |
| 3 | SeaweedFS (ADR-0005), gọi qua S3 API | Đo RAM sau khi thêm |
| 4 | Adapter OpenAI, mặc định và CI chạy `mock` (ADR-0006) | Máy không có khoá API. Không tự đặt ngân sách, không tự mở tài khoản; gọi thật lần đầu phải hỏi (dữ liệu sản phẩm ra ngoài, tốn tiền) |
| 5 | Grafana/Loki/Tempo (ADR-0007), đúng `docs/spec.md` (UC-OPS01, dòng Observability) | Điều kiện đổi sang Victoria (< 16 GB RAM) đã chạm vì máy có 14,6 GB: B4 T2 phải đo RAM thật, không đạt thì chuyển Victoria |
| 6 | ZaloPay: adapter + mock (A6) | Gọi sandbox thật lần đầu phải hỏi; khoá test lấy từ tài liệu chính thức, không gõ theo trí nhớ, không commit |
| 7 | SMS mock qua `notification`, mã vào Mailpit (A5) | Không gửi SMS thật |
| 8 | Đăng nhập Google để ngoài đợt 3 | UC-B11 ghi "chưa làm" trong báo cáo |

ADR 0004–0008: chấp nhận phương án đề xuất, ghi ở "Chủ dự án quyết" là uỷ quyền, kèm ngày. Trước khi đổi trạng thái phải tra lại phiên bản và giấy phép. Đã tra lại 2026-11-24 (verify-log): SeaweedFS tối thiểu 4.55 và chỉ mở cổng S3 nội bộ; model LLM là cấu hình (`gpt-6-luna`, `gpt-6.1-terra`), SDK `openai-go/v3`; CloudNativePG dùng image tự build có `postgresql-16-pgvector`. A4–A8 và đường B được chạy theo thứ tự §5 sau khi 3a nghiệm thu và commit local. Push vẫn phải hỏi từng lần.

Lưu ý cho câu 5: ADR-0007 tự đặt điều kiện chuyển sang Victoria khi máy demo có dưới 16 GB RAM. Máy hiện tại có 14,6 GB và đang dùng swap vì chạy nhiều dự án khác. Riêng stack NexusMart chỉ tốn khoảng 1,25 GB cho 15 container (đo 2026-11-23). Vì vậy giữ giả định Grafana, nhưng B4 phải đo RAM thật trước khi chốt. Các phiên bản ghi trong ADR do agent tra ngày 2026-11-23, phải tra lại lúc duyệt.

## 2. Đối chiếu 100 UC sau đợt 2

`uc-matrix.tsv` là ảnh chụp **trước đợt 1**, không dùng để tính việc còn lại. Danh sách dưới đây đối chiếu từ `docs/audit/README.md` §3.2, phạm vi `w2-plan.md` và `w2b-plan.md`, và code trên `main`.

- **Đợt 2 đã làm** (không lập lại): B01, B09, B16, B17, S05, S06, S08, S10, S17, S18, A02 (trừ từ cấm và index tìm kiếm), A05 (trừ đổi hàng), A06, A07, A09, A14, B11 (phần mật khẩu), PAY04, SHIP04 (poller đã phân trang theo cursor), SHIP05, SHIP06, SHIP07, SHIP08, thu hồi phiên (A03), PLT-B3/B4/B7/B9.
- **Mức Cao còn thiếu một phần, không cần hạ tầng mới** (đủ 34 UC ở §3.2; các mục có ghi chi tiết đã kiểm bằng code trên `main`, agent nhận việc phải kiểm lại trước khi sửa):
  - A2–A3: B20 (danh sách cắt ở 100 sản phẩm: A3 cho trang chủ, B1 cho danh mục; banner là A08 mức Thấp), PAY06 (mới kiểm bằng hạn rút ngắn, chưa chờ đủ 15 phút thật: A2).
  - A4: B04. A5: PAY02, SHIP01, SHIP02 (PDP còn ước tính phí bằng hàm tĩnh `estimateDelivery`), SHIP03. A6: B06 (chỉ còn ví), B07, B25 (code chặn mọi phương thức ngoài VietQR).
  - A7: A01, A03 (lịch sử hoạt động từng người dùng), A11. A8: B08, B15 (ba voucher `EDU15`, `SINHNHAT50`, `NEXUSVIP12` ghi điều kiện đối tượng mà backend không kiểm), S03 (`stock_history` có ghi nhưng chưa có API và màn hình), S04 (danh sách đơn của người bán cắt ở 100).
- **Mức Cao cần ADR**: AI01, AI02, AI13, AI14, OPS01, OPS02, OPS05, OPS08, B03, S07 (đồng bộ tìm kiếm), S01 và S02 (phần tải tệp), B02 (web tin hop đầu của `x-forwarded-for` nên né được giới hạn theo IP; cần reverse proxy tin cậy, B5 T2).
- **Mức Trung bình chưa có, không cần hạ tầng mới**: A13, A15, B10, B26, S13, S15, PAY03; A12 còn phần mã hoá token hãng và bảng phí dự phòng (đã có RPC bật/tắt, kiểm kết nối).
- **Mức Trung bình cần ADR**: B12 (0004); B14, S09 (0005); AI03, AI04, AI05, AI10, AI11, AI12 (0006); OPS03, OPS06, OPS07 (0007, 0008); OPS09 (0008).
- **Mức Thấp**: ngoài đợt 3. `docs/spec.md` ghi "Use case mức Thấp chỉ đặc tả, không bắt buộc cài đặt".

## 3. Đường A: không cần ADR

Thứ tự: A1 → A2 → A3 trước (nợ của đợt 2), rồi A4–A8 song song theo service.

| Việc | Phạm vi | Tiêu chí nghiệm thu |
| --- | --- | --- |
| **A1. Quyền và dữ liệu nhạy cảm** | `ReservePromotion`/`PreviewDiscounts` đang tin giá và `category_id` gửi qua gRPC bằng JWT người dùng: chuyển `ReservePromotion` sang token service của `order`, `PreviewDiscounts` tự lấy giá ở server. `GetSKUsBatch` công khai lọc SKU ẩn. Gỡ `commission_rate_bps` khỏi `GET /products`, `/categories` công khai. Mã hoá số tài khoản ngân hàng khi lưu. Giới hạn tần suất riêng cho OTP ngân hàng ở gateway. Duyệt tài khoản ngân hàng phải khớp phiên bản admin đã xem. Thêm test chéo shop cho `EndShopDiscountProgram` | Bài tấn công: người mua gọi `ReservePromotion` trực tiếp với giá giả → từ chối; DB không còn số tài khoản dạng rõ; duyệt sau khi dữ liệu đổi → 409; mỗi mục có test |
| **A2. Nợ order/payment/promotion** | `order`: `EXPIRED` ghi thành `CANCELLED` khi đóng nhóm toàn huỷ, trả 200 khi nhóm đã `FAILED`, vòng chờ 50 ms gọi `GetPayment`, `resolveShopPickupAddress` dùng địa chỉ người mua. `payment`: test đua 23505 trên Postgres thật, N11 (báo cáo lệch khi `payment.refunded` tới trễ), SSE với id không phải UUID, index `payment_fee_configs`. `promotion`: `UpdateVoucher` đổi luật khi có reservation, Redis lệch khi `AdjustVoucherKeys` lỗi, mất Redis thì `per_user_limit` về 0, job nhắc voucher không lọc `per_user_limit`. Publisher outbox không giữ transaction khi publish. Kiểm hết hạn 15 phút thật trên stack một lần (PAY06). Thêm e2e cho nhóm checkout `IN_PROGRESS` (hiện chỉ có unit và Postgres thật). Test SQL của payment chạy câu SQL trên Postgres thật thay vì chỉ hàm Rust. Ba điểm web P2-3 ý 3, P3-1, P3-2 trong `w2b-review2-web-result.md`. Tách `make test-db` chạy test Postgres thật (hiện bị bỏ qua mặc định), gồm `order/internal/store/postgres.go` đang 0% | Độ phủ ≥ 60% cho cả ba service theo `make coverage` (đo 2026-11-23: `order` 57,0%, `promotion` 61,2%, `payment` chưa đo được vì thiếu `cargo-llvm-cov`); mất Redis giữa chừng không vượt `per_user_limit` |
| **A3. Trần 100 sản phẩm ở flash sale và trang chủ** | Web ghép suất flash với 100 sản phẩm đầu (`web/src/lib/catalog.ts`), nên khi có hơn 100 sản phẩm thì khung flash hiện "0 SP" dù API trả đủ suất; chạy `tools/loadgen` sẽ gặp chắc chắn. Lấy sản phẩm theo danh sách SKU của suất | Nạp 20.000 sản phẩm bằng loadgen, `/flash-sale` và trang chủ hiện đúng số suất API trả về; e2e-web có bài này |
| **A4. Đánh giá và từ cấm** | UC-B10 ở `product`: chỉ người đã `COMPLETED` mới được đánh giá, mỗi dòng hàng một lần, 1–5 sao, ảnh bằng URL (tải tệp chờ B2), điểm trung bình thật thay số tĩnh, event `review.created`. UC-A15: từ khoá cấm có chuẩn hoá ký tự, mức chặn/chờ duyệt, xem trước số sản phẩm bị ảnh hưởng, quét lại. Phần còn lại của B04 | Không thể đánh giá đơn của người khác hay hai lần; từ cấm viết lách vẫn bị chặn; PDP và thẻ sản phẩm không còn sao tĩnh |
| **A5. COD và vận chuyển** | PDP tính phí bằng API thật (SHIP02). UC-PAY03 đối soát COD bằng file hãng (khớp theo mã vận đơn và tiền, dòng lệch vào hàng đợi, không cộng tiền hai lần). UC-PAY02: kiểm SĐT đã xác minh và khu vực hãng nhận COD. UC-SHIP03: tạo vận đơn lỗi thì lưu và thử lại có backoff. UC-SHIP01: nạp đủ phường của 34 tỉnh (hiện 115 phường mẫu), `AddressForm` gọi `map-legacy`. Phần còn lại của A12 | Nhập cùng file hai lần không đổi sổ cái; tổng nợ = tổng có; hãng lỗi 5xx ba lần rồi sống lại thì vận đơn được tạo đúng một lần |
| **A6. Thanh toán** | Ví sandbox theo skill `integrate-provider` (adapter, mock, IPN HMAC, chống replay). UC-B25 đổi phương thức khi trả lại. UC-A13 bật/tắt phương thức và hạn mức | IPN lặp 10 lần chỉ ghi một bút toán; phương thức bị tắt không tạo được đơn mới; đơn dở vẫn xử lý xong |
| **A7. Quản trị** | UC-A11: vai trò nội bộ (kiểm duyệt, CSKH, kế toán, vận hành), duyệt hai người cho hoàn tiền lớn và khoá gian hàng, lọc audit theo thời gian, lịch sử hoạt động từng người dùng (A03). UC-A01: khoá/mở gian hàng đang hoạt động, yêu cầu bổ sung hồ sơ | Ma trận vai trò × API có test ở gateway và service; người tạo không tự duyệt được |
| **A8. Người mua và người bán** | UC-B26 sửa địa chỉ trước khi lấy hàng, UC-S13 nhập sản phẩm bằng file (job nền, lỗi theo dòng), UC-S15 xử lý đơn và in nhãn gộp, tra cứu đơn khách thật thay trang mock (UC-B08), xem lịch sử tồn kho (S03), phân trang danh sách đơn của người bán (S04), sửa hoặc gỡ ba voucher mẫu hứa sai điều kiện (B15) | Phí mới cao hơn thì phải trả thêm hoặc giữ địa chỉ cũ; file 500 dòng có 10 dòng lỗi vẫn nhập đúng 490 dòng; tra cứu đơn cần đúng mã đơn và SĐT, có giới hạn tần suất |

## 4. Đường B: cần ADR được duyệt

Phụ thuộc: B1 trước B3 (AI cần `Search.HybridSearch`); B2 trước phần tải tệp của S01/S02/A01/B10 và trước sao lưu ở B5; B4 trước bài tiêm lỗi, bài tiêm lỗi cần B5 T2–T3. Câu hỏi 1 (nơi chạy Kubernetes) là quyết định gốc theo `docs/adr/README.md`, nhưng B1–B3 và B4 T2–T4 làm được trên Compose trước.

| Việc | Đã có từ đợt 2 | Còn phải làm | Tiêu chí nghiệm thu |
| --- | --- | --- | --- |
| **B1. Search (0004)**: UC-B03, B12, AI02, S02/S07 (đồng bộ) | Outbox ở `product` (mới phát `product.reviewed`), tìm không dấu, loadgen 20.000–50.000 SP | T1 `product.upserted/deleted`, `category.updated`; T2 `search.proto`, `services/search` (:50059/:8089, `search_db`); T3 indexer; T4 gợi ý, lịch sử, hybrid RRF; T5 gateway, web, bỏ trần 100 SP ở danh mục | Sửa sản phẩm hiện trong kết quả < 5 giây; k6 trên ≥ 20.000 SP p95 < 200 ms; tắt Meilisearch vẫn tìm được qua `Product.ListProducts`; gợi ý < 80 ms |
| **B2. Object storage và chat (0005)**: UC-B14, S09, phần tải tệp của S01/A01/S02/B10 | Chưa có; bằng chứng trả hàng đang dùng URL | T1 S3 + presigned PUT (≤ 5 MB, kiểm magic bytes); T2 `chat.proto`, `chat_db`; T3 WebSocket + Redis Pub/Sub (:50060/:8090), ticket 30 giây; T4 nghiệp vụ; T5 web | Hai instance nhận tin chéo < 200 ms; rớt mạng nối lại đủ tin; k6 ≥ 1.000 kết nối/instance; người ngoài hội thoại nhận 403; file sai loại bị từ chối |
| **B3. Trợ lý AI (0006)**: UC-AI01, 03, 04, 05, 10, 11, 12, 13, 14 | Chưa có | T1 khung `services/ai-assistant` (:50061/:8091, `ai_db`), `LLMClient`/`Embedder` có `mock`; T2 RAG + SSE; T3 tool bằng JWT người dùng; T4 guardrail + hạn mức; T5 eval 100 câu. AI05 cần A4, AI10/AI12 cần B2 | TTFT p95 < 2 giây; ≥ 25 kịch bản injection bị chặn 100%; buyer A không đọc được đơn buyer B qua tool; bịa sản phẩm < 2%, có căn cứ ≥ 90%; vượt hạn mức trả 429 |
| **B4. Giám sát và AIOps (0007)**: UC-OPS01, 02, 03, 07, 08 | Phần lớn T1: RED metrics mọi service, collector đọc `/metrics`, log gRPC có `trace_id`, W3C `set_parent` ở payment | T1 còn lại: xuất trace ra backend (collector đang chỉ `debug`), bỏ span cha giả, xác nhận span `payment` trong cây trace; T2 stack + dashboard + SLO + Telegram; T3 detector Rust (`services/aiops`, `aiops_db`); T4 RCA; T5 tiêm lỗi (cần B5) | Một checkout là một cây trace liền gồm `payment`; tắt service báo Telegram < 2 phút; 5/5 kịch bản phát hiện < 2 phút, ≥ 4/5 RCA đúng; log gửi LLM che 100% PII và secret |
| **B5. Kubernetes, CI/CD, sao lưu (0008)**: UC-OPS05, 06, 09 | T1 phần lớn: CI chạy tay có build web, `govulncheck`, `cargo audit`, `npm audit`, Trivy | T1 còn lại: cổng độ phủ 60%; T2 `k3d` và edge proxy tin cậy (B02); T3 Helm, HPA, rollback (trước khi chạy nhiều bản: `RunMigrations` của user, order, notification bọc advisory lock); T4 CloudNativePG + PITR (cần B2 T1 hoặc S3 ngoài) | Dựng cụm < 3 phút; rolling update không rớt request; rollback < 60 giây; khôi phục PITR đúng mốc và ghi RTO thật |

UC-OPS04 (tự khắc phục) là mức Thấp: chỉ làm khung allowlist + duyệt tay nếu B4 T4 còn dư công.

## 5. Thứ tự đề nghị

1. **3a**: A1, A2, A3. Nợ bảo mật và tiền của đợt 2, không chờ quyết định nào.
2. **3b**: A4–A8 và B1 (nếu ADR-0004 được duyệt). B1 đụng `product`, nên A4 và B1 T1 không chạy cùng lúc trên `product`.
3. **3c**: B2, B4 T1–T3.
4. **3d**: B3 (sau B1 T4), B4 T4.
5. **3e**: B5, rồi B4 T5 (tiêm lỗi) và các con số định lượng cho báo cáo (k6, AIOps, eval AI).

Lộ trình đặc tả đặt AI và tìm kiếm ngữ nghĩa ở tháng 4, AIOps và tiêm lỗi ở tháng 5. Nếu phải cắt: giữ A1–A3, B1, B3, B4, B5 (nhóm "làm sâu" gồm Order, Payment, Shipping, AI assistant, AIOps), cắt A8 và phần A7 ngoài khoá gian hàng trước.

Mỗi phần nghiệm thu và commit riêng trên `main`. Quyền push một lần của đợt 2 đã dùng; mỗi lần push sau phải hỏi lại.

## 6. Luật chung

- Số migration kế tiếp: user `0007_`, product `000007_`, order `0014_`, payment `0008_`, promotion `004_`, shipping `000005_`, notification `000005_`. E2E từ `34_`, e2e-web từ `08_`. DB mới thêm vào `infra/postgres/init-dbs.sql`; volume cũ không tự có DB mới, phải ghi cách nâng cấp.
- Proto và contract làm tuần tự trước, agent theo service chạy sau, như đợt 2. Đọc `w2-rules.md` và `w2b-plan.md` §8.
- Service mới dùng skill `new-service`; provider ngoài dùng `integrate-provider`; mỗi tích hợp có mock.
- Không thêm trigger CI; không commit secret.
- Review độc lập hai lượt cho việc đụng tiền, quyền, AI tool và webhook.

## 7. Bài học đợt 2b đưa vào quy trình nghiệm thu

1. **QA chạy đúng lệnh cổng.** QA r2b báo e2e xanh bằng `go test -timeout 25m` trong khi `make e2e` giới hạn 5 phút và không thể xanh. Từ đợt 3, số báo cáo phải là số của `make …`, kèm thời gian.
2. **Chạy lặp trước khi đóng.** Bộ Rust đỏ khoảng 42% lượt chạy nhưng vẫn từng báo "88/88". Test mới phải qua `go test -race -count=5` hoặc 100 lượt binary Rust.
3. **Dựng volume trắng ở mỗi mốc.** Lỗi healthcheck Postgres chỉ lộ trên volume trắng.
4. **Không chạy agent trong lúc nghiệm thu.**
5. **Thời gian e2e**: 565 giây trên trần 1.200 giây, trong đó có 74 lần chờ 429. Đợt 3 giảm phần chờ bằng giới hạn tần suất riêng cho stack kiểm thử (vẫn giữ bài kiểm 429 thật), không nâng trần thêm.
6. **Xem giao diện với dữ liệu lớn.** Lỗi trần 100 sản phẩm chỉ lộ khi stack có 181 sản phẩm.

## 8. Rủi ro

| Rủi ro | Cách giảm |
| --- | --- |
| Máy 14,6 GB RAM đang dùng swap; thêm Meilisearch, SeaweedFS, quan sát và `k3d` có thể không đủ | Đo RAM sau mỗi phân hệ; tắt các stack audit/web cũ khi demo; Victoria và Postgres-only là đường lùi đã ghi trong ADR |
| Chi phí LLM vượt ngân sách | Mặc định `mock`; ngân sách ngày trên Redis; eval chỉ chạy tay |
| Đổi image Postgres sang `pgvector/pgvector:pg16` với volume cũ | Thử nâng cấp trên bản sao volume trước, có bước quay lại |
| Phiên bản và giấy phép trong ADR cũ | Tra lại lúc duyệt ADR, ghi ngày kiểm tra |
| A1 đổi quyền gọi RPC giữa `order` và `promotion` | Triển khai hai bước (chấp nhận cả hai, rồi siết), có e2e chéo |

## 9. Việc cần chủ dự án làm

Không còn việc nào chặn bắt đầu (ba việc cũ đã xong: 3a được chạy từ 2026-11-24, §1.1 chốt tám câu và ADR 0004–0008). Còn phải hỏi khi tới: push; khoá và ngân sách LLM thật; gọi sandbox ZaloPay; khoá Telegram (B4).

## 10. Hoãn từ đợt 3a (làm ở 3b trở đi)

| Mục | Chỗ | Lý do hoãn |
| --- | --- | --- |
| Hai khối gợi ý (`CheckoutSuggestionStrip`, size finder trong `ShopDetail`) hiện giá chưa trừ flash sale/giảm giá gian hàng | web, review web P2-16 | Giá thật vẫn do server tính lúc checkout; làm cùng A8 (giao diện) |
| Job hết hạn voucher phát NATS khi đang giữ khoá dòng | `promotion`, review promotion P2-10 | Không sai dữ liệu, chỉ kéo dài khoá khi NATS chậm; chuyển sang outbox khi `promotion` có event nghiệp vụ mới |
| Nâng cấp migration `promotion` 004 phải dừng dịch vụ | `promotion` | Chấp nhận: chưa có triển khai cuốn chiếu; xem lại ở B5 T3 |
| `RunMigrations` chưa có advisory lock ở `user`, `order`, `notification` | 3 service | B5 (nhiều replica) |
| B02: web tin hop đầu `x-forwarded-for` (3b-1 làm nặng thêm: né được trần 30 lượt/phút của `calculate-fee` công khai) | web, gateway | B5 T2 (edge proxy tin cậy) |
| `gateway` và `web` 3a phải triển khai cùng nhau | hợp đồng đổi hai phía | Ghi vào runbook B5 |
| `CHECKOUT_FAILED` không kích được tất định qua API công khai | e2e | Có unit/Postgres test; e2e cần điểm tiêm lỗi (B4 T5) |
| **Việc đầu tiên của 3b**: consumer của `shipping`, `notification`, `promotion`, `payment`: (a) chỉ Ack sau khi DLQ lưu được ít nhất một nơi, ghi DLQ bằng context riêng không theo context tắt máy; lỗi do đang tắt thì không vào DLQ; (b) không `Drain()` durable consumer do tiến trình tạo (nats.go xoá consumer, replica khác mất consumer, khởi động lại phát lại cả stream) | 4 service, theo mẫu `order` r6 | Có từ trước 3a, idempotent nên không sai dữ liệu khi chạy một replica; bắt buộc xong trước B5 |
| Giỏ lặng lẽ bỏ SKU bị ẩn: `cart.buildCart` `continue` nên người mua thấy giỏ trống hoặc thiếu món, không có lời báo (trình duyệt thật B7). Cần `CartItem.unavailable`, cart trả dòng không tính tiền, `order` (`from_cart=true`) xử lý rõ, web hiện "không còn bán" kèm nút xoá | proto, cart, order, web | Có từ trước 3a, giá 0 không lọt; web r9 đã luôn gửi `items` nên đơn không lệch màn |
| Nhãn "Xem tất cả N sản phẩm" ở trang chủ đếm số sản phẩm đã tải (126) chứ không phải tổng (20.365); `tools/loadgen` trỏ ảnh không tồn tại `do-lot-boxer-modal.png` (đúng là `-3.png`) | web, tools/loadgen | Làm cùng B1 (bỏ trần 100 ở danh mục) |
| Tên gian tra từ danh sách 50–100 gian đầu (`web/src/lib/catalog.ts:91`, `thanh-toan/page.tsx:118`, `cho-thanh-toan/[paymentId]/page.tsx:97`): quá 100 gian thì đơn/giỏ của gian cũ hiện "Gian hàng NexusMart" (thấy trên trình duyệt thật, volume cũ 100+ gian) | web (tra `/shops/{id}` theo đúng các `shop_id` đang hiện) | Làm cùng B1 |
| `waitForCommittedReservation` (promotion) chờ tối đa 25×15 ms: gọi trùng `ReservePromotion` cùng đơn trong lúc commit PG chậm hơn ~375 ms thì nhận `Aborted` dù lượt đầu sẽ thành công | promotion | Trần chấp nhận: `order` thử lại được, không giữ hai lần; xem lại ở B4 T5 (tiêm lỗi) |
| Consumer đẩy (Go, NATS push): mỗi lần tắt máy đột ngột, tin đã nằm trong bộ đệm client mất một lượt giao; ≥ 6 lần khởi động hỏng liên tiếp khi còn tồn đọng có thể vượt `MaxDeliver` mà chưa vào DLQ | `order`, `shipping`, `notification`, `promotion` | Chấp nhận P3: cần vòng lặp khởi động hỏng kéo dài; B4/B5 cân nhắc pull consumer `MaxAckPending` nhỏ như `payment` |
| `shipment.create_failed` đến đúng lúc người bán đang bấm gửi lại thì bị bỏ qua, đơn có thể hiện `QUEUED` dù job đã hỏng | `order` `ApplyShipmentCreateFailed` | P3: cửa sổ rất hẹp, người bán bấm gửi lại vẫn chạy (không còn job sống); web không được khoá nút khi `QUEUED` |
| 3.304/3.321 phường chưa có mã quận/phường GHN; hãng GHN ở chế độ sandbox/thật không phục vụ được các phường đó | `shipping` danh mục, `carrier/ghn.go` | Mã GHN chỉ lấy được qua API master-data của GHN (cần token sandbox, phải hỏi chủ dự án). 3b-1 chỉ bảo đảm không gọi GHN với mã 0 |
| `from_cart=true` khi không món nào được chọn thì đặt mọi món còn bán, bỏ qua món không bán | `order` `resolveCheckoutInputs` | Hành vi cũ, web luôn gửi món đã chọn; E4 chốt bỏ qua dòng không chọn |
| Chuyển consumer cũ sang kiểu mới: nếu xoá xong mà tạo lại lỗi thì lần khởi động sau phát lại cả stream | `promotion` consumer | Consumer idempotent, không sai dữ liệu; chỉ xảy ra một lần lúc nâng cấp |
| `resilience/breaker.go` trùng giữa `cart` và `product` | `services/{cart,product}/internal/resilience` | Gom vào `pkg/common` khi có service thứ ba cần |
| OTP SĐT gửi vào email tài khoản (SMS mock, quyết định 7): chưa chứng minh chủ tài khoản giữ số đó, nên `phone_verified` chỉ có giá trị mô phỏng; người lạ có thể xác minh trước số của người khác | `user` `phone_otp.go`, `notification` | Phải có nhà cung cấp SMS thật (chủ dự án chọn) trước khi chạy thật; payload khi đó thêm số đích và gửi tới số đó |
| Từ cấm so không dấu nên chặn nhầm vài cách viết: "hàng cam tươi" dính từ khoá "hàng cấm"; "bổ s.u n g" dính "súng"; "đa g a" dính "đá gà" (r8 và r10 cho kết quả giống nhau, không phải lỗi mới) | `product` `internal/moderation/normalize.go`, E9-bis và E9(c) | Đánh đổi đã chốt: thà chặn nhầm còn hơn lọt cách viết né. Admin có bản xem trước số sản phẩm dính (E11) trước khi bật luật; người bán thấy từ bị dính. Nếu than phiền nhiều: thêm danh sách ngoại lệ theo luật |
| Huỷ đơn đã trả tiền đúng lúc `promotion` chết: `order` đã commit CANCELLED rồi bù trừ đồng bộ quá hạn nên client nhận 503 dù đơn đã huỷ (watchdog làm nốt sau) | `order` CancelOrder, gateway | Dữ liệu đúng; chỉ lời trả về gây hiểu lầm. Sửa: trả 200 sau khi commit, để watchdog bù |
| Tên dịch vụ ở PDP vẫn là "Mock Express Tiêu Chuẩn" trong khi nhãn hãng đã là "Chuyển phát Nexus" | `shipping` `carrier/mock_express.go` ServiceName | Nhãn hiển thị; sửa ở nguồn khi nối hãng thật |
