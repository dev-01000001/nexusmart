# Báo cáo tổng hợp rà soát nghiệp vụ và nghiệm thu Đợt 1 — NexusMart

Ngày lập: 2026-11-23 · Mốc rà soát gốc: `HEAD` `0db2cdd` (trên stack `nexusmart-audit`).

## 1. Tóm tắt

- **Phạm vi rà soát**: toàn bộ 100 Use Case trong `docs/spec.md`, khung đối chiếu 19 nhóm Shopify và nhóm nghiệp vụ sàn, 9 service đang chạy (8 Go gồm `gateway`, 1 Rust `payment`), 45 trang `web/` và bộ dữ liệu seed.
- **Cách rà soát**: chạy kiểm chứng trực tiếp trên stack thật `nexusmart-audit` (DB/Redis/NATS/Mailpit/Web thật) bằng 8 agent song song theo miền (`usr`, `cat`, `ord`, `pay`, `prm`, `shp`, `plt`, `web`), sau đó manager tự kiểm lại toàn bộ lỗi P0 và phần lớn P1 qua DB/curl/code (`verify-log.md`), rồi chạy hai đợt review độc lập sau các vòng sửa Đợt 1.
- **Kết quả trước Đợt 1**: dù `make lint`, `make test`, `make e2e` (11/11) và `make e2e-web` (13/13) đều xanh, bộ test cũ không bắt được các lỗi sai tiền P0; trong 36 UC mức Cao chỉ có 2 Đủ (`UC-B05`, `UC-PAY01`), 12 Một phần, 16 Lỗi và 6 Thiếu (toàn bộ 6 UC Thiếu là phân hệ AI và AIOps chưa dựng service).
- **Kết quả Đợt 1** (chưa commit, diff `137` file, `+12.999 / −3.642` dòng): đã sửa và nghiệm thu xong 10 nguyên nhân gốc `R1–R10`, các lỗi P1/P2 đi kèm, toàn bộ lỗi từ review độc lập lần 1 (vòng sửa 2) và lần 2 (vòng sửa 3), và viết 5 ADR đề xuất (chờ duyệt) cho nhóm B (`0004–0008`).
- **Bằng chứng nghiệm thu cuối Đợt 1** (stack `nexusmart-audit` reset sạch): `make lint` 0 lỗi, `make test` xanh (36 package Go + 53 test Rust), `make e2e` 23/23 (74s), `make e2e-web` 23/23 hai lần liên tiếp, kiểm tĩnh `web/` xanh; sổ cái cân (`dr = cr = 16.759.105₫`), 0 nhóm có hơn 1 payment `succeeded`, 0/28 nhóm escrow âm, 0 refund thanh toán muộn trùng, 0 đơn COD huỷ còn treo escrow.
- **Rủi ro chính cần biết**: hồi quy huỷ lẻ đơn con VietQR chưa trả đã được gỡ ở vòng 3 bằng quyết định huỷ cả nhóm của manager (chủ dự án có thể đổi); rủi ro bảo mật chính còn tồn là đường qua BFF `web` vẫn né được rate limit IP khi `web` mở thẳng ra ngoài không qua reverse proxy.
- **Việc chờ chủ dự án quyết**: duyệt commit Đợt 1; quyết chạy Đợt 2 (khoảng trống P2, trả hàng hoàn tiền đầu-cuối, chuẩn hoá dữ liệu seed, chốt quy tắc huỷ VietQR/thời gian giữ tiền) và chốt các câu hỏi mở trong ADR `0004–0008` trước khi dựng 4 phân hệ mới.

---

## 2. Đối chiếu theo khung 19 nhóm Shopify và nghiệp vụ sàn

Trạng thái chỉ dùng 5 mức: `Đủ`, `Một phần`, `Thiếu`, `Lỗi`, `Ngoài phạm vi`. Cột **Sau đợt 1** chỉ đổi khi `.agents/briefs/fix/verify-log.md` ghi nhận đã sửa và nghiệm thu. Nhóm nghiệp vụ sàn lấy nguyên tên từ 8 báo cáo (7 dòng do `plt.md` tách riêng `Chat người mua với người bán (sàn)`).

### 2.1. 19 nhóm Shopify

| Nhóm | Nghiệp vụ trong nhóm | UC tương ứng hoặc "ngoài phạm vi" (dẫn dòng spec) | Trước đợt 1 | Sau đợt 1 | Phần còn lại thuộc đợt/ADR nào |
| --- | --- | --- | --- | --- | --- |
| Thiết lập cửa hàng | Đăng ký, gói dịch vụ và billing, tên miền, nhân viên và phân quyền, chính sách cửa hàng, cài đặt thuế và tiền tệ | `UC-S01` (Cao), `UC-A11` (Cao), `UC-SHIP06` (TB), `UC-S12`/`S14` (Thấp). Ngoài phạm vi: gói dịch vụ/billing, tên miền riêng, thuế và đa tiền tệ (`docs/spec.md:27,39`; `AGENTS.md` luật 6) | Lỗi | Một phần | Đợt 2 (`UC-SHIP06`, hoàn thiện vai trò nội bộ & duyệt 2 người `UC-A11`); Không làm (`UC-S12`/`S14` Thấp, mục ngoài phạm vi) |
| Storefront và nội dung | Theme và trình chỉnh sửa, trang và blog, menu, tìm kiếm và lọc, gợi ý sản phẩm, SEO, đa ngôn ngữ, headless qua Storefront API | `UC-B03`/`B04`/`B20`/`AI02` (Cao), `UC-B12`/`B21` (TB), `UC-A08`/`S12`/`AI07` (Thấp). Ngoài phạm vi: theme editor, CMS trang/blog, mega-menu động, bộ SEO đầy đủ, đa ngôn ngữ, GraphQL Storefront API ngoài (`docs/spec.md:39`) | Một phần | Một phần | Đợt 2 (tìm kiếm tức thời thật `CAT-B12`/`WEB-B8`); Đợt 3 / `ADR-0004` (`services/search` + hybrid vector); Không làm (UC Thấp, mục ngoài phạm vi) |
| Catalog | Sản phẩm, biến thể và tuỳ chọn, collection thủ công và tự động, media, sản phẩm số, bundle, subscription, gift card, giá và giá so sánh, import/export | `UC-S02`/`S07` (Cao), `UC-S13`/`S18`/`B16` (TB), `UC-S12` (Thấp). Ngoài phạm vi: collection tự động, sản phẩm số, bundle/combo, subscription, gift card, export (`docs/spec.md:29,39`) | Một phần | Một phần | Đợt 2 (`CAT-D1` giá gốc `original_price_vnd`, lỗi cây danh mục `CAT-B8/B9`, `UC-S13`, `UC-S18`); Đợt 3 / `ADR-0005` (upload media S3); Không làm (combo, gift card, subscription, sản phẩm số) |
| Tồn kho | Nhiều kho, theo dõi số lượng, chuyển kho, đơn nhập hàng, điều chỉnh, kiểm kê, trạng thái sẵn có / đã giữ / đang về | `UC-S03` (Cao), `UC-B06` (Cao). Ngoài phạm vi: nhiều kho, chuyển kho, đơn nhập hàng, kiểm kê, trạng thái đang về (`docs/spec.md:28` *"quản lý kho"*) | Một phần | Một phần | Đợt 2 (API/UI đọc `stock_history`, đồng bộ badge `OUT_OF_STOCK` sau `PATCH` `CAT-B13`); Không làm (quản lý nhiều kho ngoài phạm vi) |
| Giỏ hàng và checkout | Giỏ hàng, checkout, thanh toán nhanh, áp mã giảm giá, tính thuế và phí vận chuyển, khôi phục giỏ bỏ dở, draft order, upsell sau mua | `UC-B05`/`B06`/`B15`/`SHIP02` (Cao). Ngoài phạm vi: thanh toán nhanh/guest checkout, tính thuế/VAT, khôi phục giỏ bỏ dở, draft order, upsell mua kèm (`docs/spec.md:27,29,39,79`) | Lỗi | Một phần | Đợt 2 (`PRM-B9` phạm vi danh mục voucher, `PRM-B12` giá flash trong giỏ, gỡ dải upsell tĩnh `ORD-D4`); Không làm (guest checkout, thuế, draft order, abandoned cart) |
| Thanh toán | Cổng chính, cổng bên thứ ba, thanh toán thủ công (COD, chuyển khoản), trả góp, đa tiền tệ, hoàn tiền, khiếu nại chargeback, phân tích gian lận, đối soát và payout | `UC-PAY01`/`PAY02`/`B07`/`B25`/`PAY06` (Cao), `UC-PAY03`/`PAY04`/`PAY07`/`A13`/`A14` (TB), `UC-PAY05`/`A16` (Thấp). Ngoài phạm vi: trả góp, đa tiền tệ, chargeback thẻ (`docs/spec.md:25,27,39`) | Lỗi | Một phần | Đợt 2 (ví điện tử sandbox `UC-B07`, `PAY-B7/B8/B9/B11`, `UC-PAY03`, `UC-A13/A14`); Không làm (`UC-PAY05`/`A16` Thấp, trả góp/đa tiền tệ/chargeback) |
| Đơn hàng | Vòng đời đơn, sửa và huỷ đơn, trạng thái thanh toán và giao hàng, định tuyến đơn theo kho, trả hàng và đổi hàng, store credit | `UC-B08`/`B17`/`S04`/`PAY06` (Cao), `UC-B09`/`B26`/`S10`/`S15` (TB), `UC-PAY08` (Thấp). Ngoài phạm vi: sửa hàng sau đặt, định tuyến đa kho, tự tạo đơn đổi, store credit (`docs/spec.md:28,39`) | Lỗi | Một phần | Đợt 2 (trả hàng hoàn tiền đầu-cuối `UC-B09`/`S10`, job tự `COMPLETED` 7 ngày & tự huỷ 48h `ORD-B9`, `UC-B26`, `UC-S15`); Không làm (`UC-PAY08` Thấp, đa kho, store credit) |
| Giao hàng | Vùng và biểu phí, phí tính theo hãng, mua nhãn vận chuyển, giao nội thành, nhận tại cửa hàng, giao một phần, tracking, tích hợp 3PL | `UC-SHIP01`/`SHIP02`/`SHIP03`/`SHIP04` (Cao), `UC-SHIP05`/`SHIP06`/`SHIP07`/`SHIP08`/`A12`/`S15` (TB). Ngoài phạm vi: giao hoả tốc nội thành, nhận tại cửa hàng, giao một phần, quản lý kho 3PL (`docs/spec.md:28,39`) | Lỗi | Một phần | Đợt 2 (`SHP-B3`/`UC-SHIP07` huỷ vận đơn, `SHP-B4`/`UC-SHIP08` vận đơn trả hàng, nhãn in thật `SHP-B8`, `UC-SHIP05/06`, `UC-A12`, bổ sung danh mục phường/mã GHN `SHP-D3`); Không làm (giao hoả tốc, nhận tại quầy, giao một phần) |
| Khách hàng | Hồ sơ, tài khoản, phân nhóm (segment), tag, đồng ý nhận tin, xoá dữ liệu theo yêu cầu | `UC-B01`/`B02`/`A03` (Cao), `UC-B11`/`B19`/`B23` (TB), `UC-B27` (Thấp). Ngoài phạm vi: phân nhóm/hạng thành viên, tag (`docs/spec.md:29,39`) | Lỗi | Một phần | Đợt 2 (`USR-B1` thu hồi tức thời access token qua denylist Redis 15p, `USR-B8` chống dò tài khoản, `UC-B11` Google OAuth & quên mật khẩu, phần bảo mật còn thiếu của `UC-B19`); Không làm (`UC-B27` Thấp, segment/hạng, tag) |
| Khuyến mãi và marketing | Mã giảm giá và giảm tự động, mua X tặng Y, miễn phí vận chuyển, kết hợp khuyến mãi, email marketing, automation, form thu lead, chat Inbox có nhân viên bán hàng AI, hợp tác influencer | `UC-B15` (Cao), `UC-S05`/`A07`/`B22`/`S18`/`SHIP06` (TB), `UC-B18` (Thấp); phần chat/AI tách sang `UC-B14`/`S09`/`AI01..12`. Ngoài phạm vi: mua X tặng Y, email marketing, automation, form thu lead, affiliate/influencer (`docs/spec.md:25,29,39`) | Lỗi | Một phần | Đợt 2 (`PRM-B9` sửa/bật-tắt/phạm vi danh mục voucher, `PRM-B13`/`UC-S18` giảm giá shop, `UC-SHIP06` freeship ngưỡng shop); Đợt 3 / `ADR-0005`/`0006` (chat & AI); Không làm (mua X tặng Y, email marketing, automation, lead, influencer) |
| Kênh bán | Online store, POS, app, mạng xã hội, Google, marketplace bên ngoài, nhập hàng của người bán khác | Online store: `UC-B01..B27`, `UC-S01..S18`. Ngoài phạm vi: app mobile native (`docs/spec.md:24`), POS, mạng xã hội, Google, marketplace ngoài (`docs/spec.md:39`) | Một phần | Một phần | Đợt 2 (dọn các trang mock còn lại trên web storefront); Không làm (mọi kênh ngoài online store web) |
| POS | Bán tại quầy, thanh toán trực tiếp, quản lý ca và tiền mặt, trả hàng tại cửa hàng, mua online nhận tại quầy | Ngoài phạm vi đặc tả (`docs/spec.md:27` loại trừ tiền thật; `docs/spec.md:39` không có trong 100 UC) | Ngoài phạm vi | Ngoài phạm vi | Không làm |
| Bán quốc tế (Markets) | Giá và catalog theo thị trường, tên miền địa phương, thuế nhập khẩu, ngôn ngữ | Ngoài phạm vi đặc tả (`docs/spec.md:23` không multi-region, `docs/spec.md:28` không giao quốc tế, `docs/spec.md:39`; `AGENTS.md` luật 6 chỉ VND) | Ngoài phạm vi | Ngoài phạm vi | Không làm (thêm tiền tệ cần ADR riêng) |
| B2B | Hồ sơ công ty, bảng giá riêng, điều khoản thanh toán, số lượng tối thiểu, báo giá | Ngoài phạm vi đặc tả (`docs/spec.md:27` loại trừ hoá đơn điện tử; `docs/spec.md:39` không có trong tab Use case) | Ngoài phạm vi | Ngoài phạm vi | Không làm (Đợt 2 dọn copy VAT mẫu `WEB-D6`) |
| Phân tích | Dashboard, báo cáo bán hàng, khách hàng, tồn kho, hiệu quả marketing, xem trực tiếp | `UC-S06` (TB), `UC-A06` (TB), `UC-S11`/`AI09` (Thấp). Ngoài phạm vi: BI nâng cao, xem trực tiếp, data warehouse (`docs/spec.md:26,36,39`) | Một phần | Một phần | Đợt 2 (`PLT-B10`: tính báo cáo doanh thu seller và thống kê sàn ở server, lọc ngày, xuất báo cáo); Không làm (`UC-S11`/`AI09` Thấp, BI nâng cao/data warehouse) |
| Tài chính | Tài khoản số dư (Balance), vay vốn, thuế, báo cáo tài chính | `UC-S17`/`S06`/`A06` (TB). Ngoài phạm vi: vay vốn, thuế và hoá đơn điện tử (`docs/spec.md:27,39,117`) | Một phần | Một phần | Đợt 2 (`UC-S17` khai báo TK ngân hàng + OTP + sổ giao dịch chi tiết theo shop, `PLT-B10` xuất báo cáo); Không làm (vay vốn, thuế/hoá đơn điện tử) |
| Tự động hoá và AI | Workflow theo sự kiện, hẹn giờ phát hành hoặc A/B test, trợ lý cho người bán; Trợ lý AI làm kênh bán (checkout qua trợ lý) | `UC-AI01`/`AI02`/`AI13`/`AI14` (Cao), `UC-AI03..05`/`AI10..12` (TB), `UC-AI06..09` (Thấp). Ngoài phạm vi: AI tự checkout/thanh toán (`docs/spec.md:32,63`), workflow thương mại/A-B test (`docs/spec.md:39`) | Thiếu | Thiếu | Đợt 2 (gỡ/đánh dấu mẫu cho panel AI giả và link 404 `WEB-B5`); Đợt 3 / `ADR-0006` (`services/ai-assistant`, RAG, guardrail, eval); Không làm (`UC-AI06..09` Thấp, AI tự thanh toán) |
| Nền tảng cho nhà phát triển | App store, Admin API và Storefront API, webhook ra ngoài, functions, extension, theme store | Ngoài phạm vi đặc tả (`docs/spec.md:39` — không có trong tab Use case; `/api/v1/*` chỉ phục vụ web BFF nội bộ) | Ngoài phạm vi | Ngoài phạm vi | Không làm |
| Thông báo và tuân thủ | Mẫu email và SMS giao dịch, chính sách cửa hàng; cookie và quyền riêng tư, chống bot, nhật ký hoạt động | `UC-A11` (Cao), `UC-B01`/`B07`/`B08`/`S04`/`PAY04`/`SHIP04` (email/thông báo), `UC-B23` (TB), `UC-B27` (Thấp), rate limit (`docs/spec.md:35`). Ngoài phạm vi: SMS giao dịch (`docs/spec.md:39,74`), WAF thương mại (`docs/spec.md:35`), cookie consent/CAPTCHA (`docs/spec.md:39`) | Lỗi | Một phần | Đợt 2 (`PRM-B14` thông báo đơn cho seller & email `shipment.updated`, `PLT-B6` security headers/413, hoàn thiện `UC-A11`); Đợt 3 / `ADR-0008` (reverse proxy tin cậy trước web); Không làm (`UC-B27` Thấp, SMS/WAF/CAPTCHA) |

### 2.2. Nghiệp vụ sàn (7 dòng trích nguyên từ 8 báo cáo)

| Nhóm | Nghiệp vụ trong nhóm | UC tương ứng hoặc "ngoài phạm vi" (dẫn dòng spec) | Trước đợt 1 | Sau đợt 1 | Phần còn lại thuộc đợt/ADR nào |
| --- | --- | --- | --- | --- | --- |
| Người bán (sàn) | Đăng ký và xét duyệt, xác minh danh tính (KYC), xếp hạng shop, xử lý vi phạm | `UC-S01`/`A01` (Cao), `UC-B21` (TB), `UC-S11`/`A10`/`B24` (Thấp) | Lỗi | Một phần | Đợt 2 (`SuspendShop`); Đợt 3 / `ADR-0005` (upload tệp hồ sơ KYC nếu bật); Không làm (`UC-S11`/`A10`/`B24` Thấp) |
| Đơn nhiều người bán (sàn) | Tách một lần checkout thành nhiều đơn con, phí vận chuyển theo từng shop | `UC-B06` (Cao), `UC-SHIP02` (Cao) | Lỗi | Một phần | Đợt 2 (`UC-SHIP06` cấu hình vận chuyển từng shop; hoặc cho retry phần đơn con còn lại nếu chủ dự án đổi quyết định huỷ cả nhóm VietQR chưa trả) |
| Tiền của sàn | Hoa hồng, phí dịch vụ, giữ tiền chờ giao thành công (escrow), đối soát và chi trả cho người bán | `UC-B17` (Cao), `UC-A09`/`PAY03`/`S17` (TB), `UC-PAY05` (Thấp) | Lỗi | Một phần | Đợt 2 (`PAY-B7` thời gian giữ tiền, `UC-A09` cấu hình phí sàn/phí thanh toán có ngày hiệu lực, `UC-PAY03` đối soát COD, `UC-S17`); Không làm (`UC-PAY05` Thấp) |
| Tranh chấp (sàn) | Khiếu nại giữa người mua và người bán, sàn làm trọng tài, hoàn tiền | `UC-A05` (TB), `UC-S10` (TB), `UC-PAY04` (TB) | Thiếu | Thiếu | Đợt 2 (trả hàng hoàn tiền & khiếu nại đầu-cuối `UC-B09`/`S10`/`A05`/`SHIP08`, gỡ mock `/admin/khieu-nai`) |
| Niềm tin (sàn) | Đánh giá và nhận xét, kiểm duyệt sản phẩm và hàng cấm | `UC-A02` (Cao), `UC-B10`/`A15`/`AI05` (TB), `UC-S16`/`A10` (Thấp) | Một phần | Một phần | Đợt 2 (`CAT-B3` outbox + thông báo khi duyệt SP, `UC-B10` đánh giá thật, `UC-A15` từ khoá cấm); Đợt 3 / `ADR-0004` (`product.>`), `ADR-0006` (`UC-AI05`); Không làm (`UC-S16`/`A10` Thấp) |
| Chương trình toàn sàn (sàn) | Flash sale, voucher sàn và voucher shop, đăng ký sản phẩm vào chiến dịch | `UC-B15` (Cao), `UC-A07`/`B16`/`B22`/`S05` (TB), `UC-S08` (Thấp). Ngoài phạm vi: chiến dịch sàn khác CSR/BST (`docs/spec.md:39`) | Lỗi | Một phần | Đợt 2 (`PRM-B9` sửa/tắt/phạm vi danh mục voucher, `PRM-B10/B11/B12` kiểm giá-tồn & khoá tồn flash, hàng duyệt flash, giá flash trong giỏ, `PRM-D1` suất seed vượt tồn); Không làm (chiến dịch ngoài tab Use case) |
| Chat người mua với người bán (sàn) | Chat 1-1 thời gian thực, đính kèm ảnh/product card/mã đơn, trả lời tự động ngoài giờ, tỷ lệ phản hồi | `UC-B14` (TB), `UC-S09` (TB). Ngoài phạm vi: gọi thoại, gọi video, chatbot riêng gian hàng (`docs/spec.md:30`) | Thiếu | Thiếu | Đợt 2 (gỡ link 404 mock `WEB-B5`, gắn nhãn mẫu); Đợt 3 / `ADR-0005` (`services/chat` + Object Storage S3) |

---

## 3. Thống kê Use Case trước Đợt 1 và các UC mức Cao còn chưa Đủ sau Đợt 1

### 3.1. Bảng UC trước Đợt 1 (lấy trạng thái xấu nhất giữa các worker nghiệp vụ trong `uc-matrix.tsv`)

| Ưu tiên | Đủ | Một phần | Lỗi | Thiếu | Tổng |
| --- | ---: | ---: | ---: | ---: | ---: |
| Cao | 2 | 12 | 16 | 6 | 36 |
| Trung bình | 0 | 13 | 5 | 27 | 45 |
| Thấp | 0 | 0 | 2 | 17 | 19 |
| **Tổng** | **2** | **25** | **23** | **50** | **100** |

Hai UC đạt **Đủ** từ trước Đợt 1 (và giữ vững sau Đợt 1): `UC-B05` (Quản lý giỏ hàng) và `UC-PAY01` (Thanh toán VietQR ở chế độ mock).

### 3.2. Danh sách 34 UC mức Cao còn chưa Đủ sau Đợt 1

**Nhóm A — 6 UC Cao vẫn ở trạng thái `Thiếu` (chưa có service, chờ duyệt ADR Đợt 3):**

| UC | Tên | Lý do còn Thiếu | Đợt / ADR |
| --- | --- | --- | --- |
| `UC-AI01` | Tư vấn mua hàng bằng hội thoại | Chưa có `services/ai-assistant`, `pgvector`, LLM API; web chỉ có panel mock | Đợt 3 / `ADR-0006` |
| `UC-AI02` | Tìm kiếm ngữ nghĩa | Chưa có `services/search` và vector `pgvector` hybrid | Đợt 3 / `ADR-0004`, `ADR-0006` |
| `UC-AI13` | Thu phản hồi và đánh giá chất lượng trợ lý | Chưa có dịch vụ AI, lưu hội thoại ẩn PII và bộ eval 100 câu | Đợt 3 / `ADR-0006` |
| `UC-AI14` | Guardrail và kiểm soát chi phí LLM | Chưa có bộ lọc prompt injection, hạn mức token/ngân sách ngày và dịch vụ AI | Đợt 3 / `ADR-0006` |
| `UC-OPS02` | Phát hiện bất thường và cảnh báo | Chưa có Prometheus/Alertmanager/Telegram và `services/aiops` (Rust) cài z-score/EWMA | Đợt 3 / `ADR-0007` |
| `UC-OPS08` | Tiêm lỗi có chủ đích để kiểm chứng AIOps | Chưa có môi trường staging K8s, `services/aiops` và 5 kịch bản tiêm lỗi + `k6` | Đợt 3 / `ADR-0007`, `ADR-0008` |

**Nhóm B — 28 UC Cao đã chạy phần lõi nhưng còn chưa Đủ sau Đợt 1:**

| UC | Tên | Đã sửa ở Đợt 1 | Lý do còn chưa Đủ | Đợt / ADR |
| --- | --- | --- | --- | --- |
| `UC-A01` | Duyệt người bán | Sửa `USR-B2`, `USR-B3`, `PRM-B6/B7`, `x-client-ip` audit | Giấy tờ mới là chuỗi text (chưa upload tệp KYC); chưa có `SuspendShop` cho shop đang `ACTIVE` | Đợt 2; `ADR-0005` |
| `UC-A02` | Duyệt sản phẩm | Sửa `CAT-B7` (audit), `CAT-B10` (bắt buộc lý do) | Không phát event `product.*` và không gửi thông báo cho seller (`CAT-B3`); chưa index Search; chưa có danh mục hàng cấm (`UC-A15`) | Đợt 2; `ADR-0004` |
| `UC-A03` | Quản lý người dùng và phân quyền | Sửa `x-client-ip` cho 3 route `user` | Khoá/hạ quyền chưa thu hồi tức thời access token ≤ 15p (`USR-B1`); đổi role chưa thu hồi refresh token; thiếu xem lịch sử hoạt động user và vai trò `super_admin` trên UI | Đợt 2 |
| `UC-A11` | Quản lý vai trò nội bộ và audit log | Sửa R8 (audit tập trung `/api/v1/admin/*` + IP thật), `USR-B5` | Thiếu vai trò nhân viên sàn (`moderator`, `cs`, `accountant`, `ops`), tạo nhân viên, duyệt 2 người (4-eyes), lọc thời gian trên UI và trigger `BEFORE TRUNCATE` | Đợt 2 |
| `UC-B01` | Đăng ký tài khoản | Sửa `USR-B7`, `PRM-B7`, `USR-B9` | Còn dò tài khoản qua `verify-otp`/`resend-otp` (`USR-B8`); trang `/dang-ky` vẫn hiển thị tặng `15.000 Xu` không có thật (`USR-D4`) | Đợt 2 |
| `UC-B02` | Đăng nhập | Sửa R9 (rate limit gateway), `WEB-B4` (chuyển theo role) | Qua đường BFF `web` vẫn né được rate limit IP do chưa có reverse proxy tin cậy; thiếu lý do khoá khi `LOCKED` và timing side-channel (`USR-B8`) | Đợt 2; `ADR-0008` |
| `UC-B03` | Tìm kiếm và lọc sản phẩm | Sửa R10 (`CAT-B1` tìm không dấu + thoát wildcard) | Chưa có `services/search` và index qua event; thiếu lọc theo đánh giá; web vẫn lọc client-side trên trần 100 SP | Đợt 3 / `ADR-0004` |
| `UC-B04` | Xem chi tiết sản phẩm | Sửa `WEB-B3` (gỡ lời hứa giảm bậc/quà/combo giả) | Thiếu đánh giá thật (`UC-B10`), tóm tắt AI (`UC-AI05`), giá gốc gạch ngang trong DB (`CAT-D1`), dự tính ship thật (`SHP-D5`); SP ngừng bán trả 404 thay vì gợi ý tương tự | Đợt 2; `ADR-0006` |
| `UC-B06` | Đặt hàng (checkout) | Sửa R1, R5, R7, `ORD-B4`, `SHP-B2`, review-A, review2-O + huỷ cả nhóm VietQR chưa trả | Thiếu ví điện tử sandbox ở bước thanh toán; giỏ chưa áp giá flash (`PRM-B12`) | Đợt 2 |
| `UC-B07` | Thanh toán online | Sửa R1, R3, R4, `PRM-B6/B7`, review-B, review2-P | Mới có VietQR mock; thiếu hoàn toàn 1 ví điện tử sandbox (VNPay/MoMo/ZaloPay theo `docs/spec.md:27,80`) | Đợt 2 |
| `UC-B08` | Theo dõi đơn hàng | Sửa `ORD-B8`, `ORD-B10` (link thông báo đúng `order_id`) | Timeline còn nhãn tiếng Anh thô và thiếu tab lọc `RETURNED` (`ORD-B12`); trang `/tra-cuu-don-hang` vẫn là mock 100% (`ORD-D1`) | Đợt 2 |
| `UC-B15` | Áp voucher và xu khi checkout | Sửa R1 (`PRM-B1`), R5 (`PRM-B3`), `PRM-B8`, review-A | Thiếu phạm vi danh mục và sửa/bật-tắt voucher (`PRM-B9`); 3 voucher seed (`EDU15`, `SINHNHAT50`, `NEXUSVIP12`) ghi điều kiện đối tượng nhưng backend không kiểm (`PRM-D2`) | Đợt 2 |
| `UC-B17` | Xác nhận đã nhận hàng | Sửa R1, `ORD-B7`, review-B P0-1, review2-P P1-2 (thu COD ngay trong `order.completed`) | Thiếu job tự `COMPLETED` sau 7 ngày kể từ khi giao (`ORD-B9`); thiếu thời gian giữ tiền `seller_pending` (`PAY-B7`); chưa mở quyền đánh giá thật (`UC-B10`) | Đợt 2 |
| `UC-B20` | Duyệt trang chủ và danh mục | Sửa `CAT-B2` (route công khai luôn public-only) | Lọc danh mục cha mới gom 1 cấp con (`CAT-B9`); banner và gợi ý hôm nay là dữ liệu tĩnh; web cắt danh sách ở trần 100 SP | Đợt 2; `ADR-0004` |
| `UC-B25` | Thanh toán lại đơn chờ thanh toán | Sửa R3 (`PAY-B1/B5/B6/B10`), review-A/B, review2-O P1-3 (advisory lock đa replica) | Chưa hỗ trợ đổi phương thức sang ví điện tử hoặc COD; huỷ một đơn con VietQR chưa trả hiện huỷ cả nhóm theo quyết định manager (chủ dự án có thể đổi sang retry phần còn lại ở Đợt 2) | Đợt 2 |
| `UC-OPS01` | Xem dashboard giám sát | Sửa `PLT-B5` (`RequestLogger` có `user_id`) | Chưa có Prometheus/Grafana/Loki/Tempo; cổng `:48889` rỗng (`PLT-B3`); log thiếu `trace_id` ở 6/8 service và `payment` đứt span (`PLT-B4`); `user` thiếu RED metrics (`PLT-B7`) | Đợt 2; `ADR-0007` |
| `UC-OPS05` | Triển khai tự động qua CI/CD | Chưa đụng ở Đợt 1 | Chưa có Helm chart/K8s/HPA/staging-prod; CI thiếu build `web/Dockerfile` và quét CVE (`PLT-B9`); coverage `order`/`promotion` dưới 60% (`PLT-B8`) | Đợt 2; `ADR-0008` |
| `UC-PAY02` | Đặt hàng COD và kiểm soát rủi ro COD | Sửa R2 (vòng đời COD), R6 (`RecordCODRejection`) | Chưa kiểm tra `phone_verified` lúc đặt COD theo tiền điều kiện spec; chưa kiểm khu vực hãng hỗ trợ COD; chưa có đối soát COD (`UC-PAY03`) | Đợt 2 |
| `UC-PAY06` | Tự huỷ đơn quá hạn thanh toán | Sửa `ORD-B5`/`PAY-B5`, `PAY-B10`/`ORD-B11`, review-A P0-2, review-B P1-4 | Đã vá các lỗi đồng bộ và race, nhưng chưa kiểm kịch bản chờ hết hạn 15 phút thời gian thực trên stack (mới kiểm qua unit test + E2E retry) | Đợt 2 (kiểm xác nhận E2E) |
| `UC-S01` | Đăng ký gian hàng | Sửa `USR-B2`, `USR-B9`, form web 5i, `ResubmitShop` race | Hồ sơ giấy tờ mới là chuỗi text `business_license_doc`, chưa có upload tệp giấy phép/CCCD lên Object Storage | Đợt 3 / `ADR-0005` |
| `UC-S02` | Quản lý sản phẩm | Sửa `CAT-B4`, `CAT-B5`/`WEB-B6`, `CAT-B6` | Chưa có event đồng bộ Search (`CAT-B3`/`ADR-0004`) và chưa có upload ảnh thật (`ADR-0005`) | Đợt 2; `ADR-0004`, `ADR-0005` |
| `UC-S03` | Quản lý tồn kho | Sửa `CAT-B4` (`PUT` đi đường optimistic), `CAT-B11` | Chưa có API/UI xem lịch sử `stock_history`; UI chưa tự cập nhật badge `OUT_OF_STOCK` sau `PATCH` về 0 (`CAT-B13`) | Đợt 2 |
| `UC-S04` | Xử lý đơn hàng | Sửa `ORD-B1`/`ORD-D3` (tiền thực nhận), `SHP-B2` | Seller chưa nhận thông báo đơn mới/huỷ (`PRM-B14`); thiếu job tự huỷ sau 48h không xác nhận; `confirm` chưa báo buyer; danh sách đơn seller cắt cứng 100 đơn (`ORD-D5`) | Đợt 2 |
| `UC-S07` | Đăng sản phẩm có biến thể | Sửa `CAT-B6` (kích hoạt lại `sku_code` đã gỡ) | Thẻ `ProductCard` mới hiện `minPriceVnd`, chưa hiện khoảng giá min–max (`CAT-B13`); chưa đồng bộ Search | Đợt 2; `ADR-0004` |
| `UC-SHIP01` | Sổ địa chỉ 2 cấp 34 tỉnh | Sửa `USR-B6` (validate 34 tỉnh, SĐT VN, giữ `is_default`, chặn xoá kho shop) | `shipping_db.wards` chỉ có 115 phường mẫu (`SHP-D3`); `AddressForm` chưa gọi `map-legacy`; `UpdateAddressRequest` thiếu trường `legacy_*`; `provinces.ts` web lệch mã GSO (`SHP-D4`) | Đợt 2 |
| `UC-SHIP02` | Tính phí vận chuyển khi checkout | Sửa R7 (`SHP-B1`/`WEB-B1`), `SHP-B7` | PDP vẫn tính ship bằng hàm tĩnh mock (`SHP-D5`); lệch nhẹ SKU 0g (web 100g vs order 500g) và `cod_amount_vnd` flash sale nếu bật GHN sandbox (review-C P2-14); hãng đang chạy mock | Đợt 2 |
| `UC-SHIP03` | Tạo vận đơn và in nhãn | Sửa `SHP-B2` (khóa hãng/dịch vụ của đơn) | Tạo vận đơn chuyển ngay đơn sang `SHIPPING` chặn huỷ trước lấy hàng (`SHP-B3`); URL nhãn `.local` chết và UI chỉ in text (`SHP-B8`); thiếu outbox retry tạo vận đơn | Đợt 2 |
| `UC-SHIP04` | Cập nhật trạng thái giao hàng qua webhook | Sửa `SHP-B6` (khớp `carrier_code` URL), Việt hoá thông báo | Poller cứng `limit=100` chưa phân trang; `shipment.updated` chưa gửi email (`PRM-B14`) và còn lặp thông báo khi `delivered` (`SHP-B8`) | Đợt 2 |

---

## 4. Lỗi đã sửa ở Đợt 1

### 4.1. 10 nguyên nhân gốc (`R1–R10`) và các lỗi đi kèm ở Pha 1A–1B

| Gốc / Mã lỗi | Mức | Mô tả ngắn gọn phần đã sửa | Bằng chứng (test / kiểm DB) |
| --- | --- | --- | --- |
| **R1** (`PAY-B3`, `PRM-B1`, `ORD-B1`) | P0 | Tách `platform_discount_vnd` (`D = V_plat + S_free + X`) khỏi doanh thu seller `R = G − F − V_shop`, hạch toán vào tài khoản `platform_promo_expense`, và luôn tạo payment cho đơn 0₫ để seller nhận đủ `R − C`. | `TestE2E_OrderMoneyAndLifecycleFixes`, `TestE2E_PaymentMoney_R1_And_LatePayment`, `TestAllocationFormula_AllDiscountComponentsAndZeroPayable`; DB sau e2e: 0 payment lệch `Σ(R+Ship−D) ≠ amount`, sổ cái cân `16.759.105₫` |
| **R2** (`PAY-B4`, `ORD-B2`, `ORD-B8`, `PRM-B5`) | P0 | Giữ payment COD ở trạng thái `pending` lúc đặt (không ghi `cod_receivable`, không gửi email "thanh toán thành công"), chỉ ghi thu hộ khi `order.delivered`, huỷ/giao thất bại (`order.returned` từ `SHIPPING`) thì huỷ allocation và chặn hoàn COD chưa thu. | `TestE2E_PaymentMoney_PAY_B4_CODLifecycle`, `TestCODPaymentStatus_PendingAtCheckout_SucceededOnDeliveredEvent`, `services/notification` unit tests; DB: 0 đơn COD huỷ còn `escrow_status='held'` |
| **R3** (`PAY-B1`, `PAY-B5`, `ORD-B5`, `PAY-B6`, `PAY-B10`, `ORD-B11`) | P0/P1 | Chuyển retry sang `OrderService.RetryPayment` (chỉ nhóm còn `PENDING` chưa quá hạn), đồng bộ `orders.payment_id` + `payment_expires_at`, giới hạn 1 payment `succeeded`/nhóm (migration `0002` DO-block), webhook muộn tự tạo `late_payment` refund không đổi đơn, và ẩn nút retry khi hết hạn. | `TestE2E_OrderMoneyAndLifecycleFixes`, `TestE2E_PaymentMoney_R1_And_LatePayment`, `TestRetryPayment_ValidAndRejectedCases`, `05_audit_fixes.test.mjs` (5h); DB: 0 nhóm có > 1 payment `succeeded`, 0 refund thanh toán muộn trùng |
| **R4** (`PAY-B2`) | P0 | Quyết toán trừ phần đã hoàn của đơn con; hoàn sau quyết toán trừ `seller_pending` rồi `seller_available` của đúng shop (thiếu trả 409), không bao giờ rút vượt `escrow_liability` đã nạp. | `TestE2E_PaymentMoney_PAY_B2_RefundAndEscrowInvariant`, 53 test Rust `services/payment`; DB: 0/28 nhóm escrow âm (`min = 0`) |
| **R5** (`PRM-B3`, `ORD-B3`) | P1 | Thêm RPC `PromotionService.ReleasePromotionShop` idempotent (migration `002_partial_release.sql` + Lua `release_partial_atomic.lua`) để huỷ lẻ một đơn con hoặc `RETURNED` hoàn ngay xu, voucher shop và suất flash của shop đó; voucher sàn hoàn khi huỷ đơn cuối. | `TestE2E_PromotionPartialRelease_SubOrderCancel`, `TestE2E_PromotionService_DirectGRPC_PartialRelease_Auth_And_ListFilter`, `TestReleasePartial_IdempotentAndLeavesRemainderForFullRelease` |
| **R6** (`ORD-B6`, `SHP-B5` phần COD) | P1 | Thêm RPC nội bộ `UserService.RecordCODRejection` (chỉ token service, migration `0003_cod_rejections.sql`, idempotent theo `order_id`) do `order` gọi khi đơn COD chuyển `SHIPPING → RETURNED` để luật chặn bom COD hoạt động. | `TestRecordCODRejection_AuthAndIdempotency`, `TestCODRejection_ShippingToReturnedCallsRecordCODRejection` |
| **R7** (`SHP-B1`, `WEB-B1`) | P1 | `Shipping.CalculateFee` tự tra địa chỉ kho theo `shop_id` qua `UserService` khi thiếu điểm gửi (đưa điểm gửi thật vào khoá cache); `/gio-hang` và `/thanh-toan` bỏ truyền `from_province_code: ""`. | `TestE2E_ShippingFeeShopPickupValidationAndWebhookCarrier` (đỏ `40.000` vs `24.000` → xanh), `05_audit_fixes.test.mjs` (5e), `TestQuoteCacheKeyRequiresRealOriginAndDestination` |
| **R8** (`USR-B4`, `CAT-B7`, `PLT-B11`) | P1 | Thêm middleware `AdminAudit` tại gateway ghi `UserService.WriteAuditLog` cho mọi thao tác ghi 2xx dưới `/api/v1/admin/*` (bỏ qua 3 route `user` tự ghi) kèm `ClientIP`. | `TestE2E_GatewayRules`, `TestAdminAuditMiddlewareCallsWriteAuditLogAndSkipsUserAndGET` |
| **R9** (`PLT-B1`) | P1 | `ClientIP` tại gateway chỉ đọc `X-Forwarded-For` khi peer nằm trong `TRUSTED_PROXIES` (compose đặt `web`) và duyệt phải→trái để chống giả mạo IP né rate limit. | `TestRateLimitByIPBlocksSpoofedXFFFromUntrustedPeer`, `TestTrustedProxiesClientIPAndMultiHopXFF`, `TestE2E_GatewayRules` |
| **R10** (`CAT-B1`) | P1 | Thêm cột `search_text` không dấu (xử lý cả `đ`/`Đ`, migration `000004_product_search_text.sql`) và thoát `%`/`_` khi truy vấn `ILIKE`. | `TestE2E_ProductRules_AccentSearchStockSyncReactivateAndGuards`, `TestSearchAccentInsensitiveAndWildcardEscape`, kiểm live (`"ao thun"` 9 kết quả, `"dam"` ra Đầm, `"%"` chỉ khớp SP có `%`) |
| `ORD-B4`, `ORD-B7`, `SHP-B2`, `PRM-B2` | P1/P2 | Lưu `request_hash` chống replay khác payload (409); chặn `BuyerConfirmReceived` khi `RETURN_REQUESTED` (409); khoá hãng/dịch vụ giao hàng khớp checkout ở cả order và UI; tính xu thưởng trên tiền hàng thực trả `R − V_plat − X` (0₫ = 0 xu). | `TestE2E_OrderMoneyAndLifecycleFixes`, `TestIdempotencyPayloadHash_SameKeyDiffPayloadConflict`, `TestBuyerConfirmReceived_OnlyFromDelivered_RejectsReturnRequested`, `TestSellerShipOrder_EnforcesBuyerSelectedCarrierAndService`, `TestRewardCoins_*`, `05_audit_fixes.test.mjs` (5g) |
| `USR-B2`, `USR-B3`, `USR-B5`, `USR-B6`, `USR-B7` | P1/P2 | Cho nộp lại hồ sơ shop sau `REQUEST_INFO`/`REJECT` cùng tên cũ và bắt buộc trường/lý do; chặn review shop `ACTIVE`; khoá auth `WriteAuditLog`/`ValidateUser`; kiểm địa chỉ 34 tỉnh/SĐT VN/giữ `is_default`/chặn xoá kho shop `ACTIVE`; OTP email không bật `phone_verified`. | `TestUserRulesWave1`, `services/user/internal/service/rules_test.go`, `pkg/common/auth/grpc_trust_test.go` |
| `CAT-B2`, `CAT-B4`, `CAT-B5`/`WEB-B6`, `CAT-B6`, `CAT-B10`, `CAT-B11` | P1/P2 | Route SP công khai luôn public-only; `UpdateProduct` đi qua optimistic locking + `stock_history` + sync `OUT_OF_STOCK`; form seller ẩn `HIDDEN` khi `PENDING`; kích hoạt lại `sku_code` `INACTIVE`; chặn `PATCH` tồn SKU `INACTIVE`; từ chối SP bắt buộc lý do. | `TestE2E_ProductRules_*`, `TestE2E_GatewayRules`, `services/product/internal/server/rules_fix_test.go`, `05_audit_fixes.test.mjs` (5f) |
| `PRM-B4`, `PRM-B6`, `PRM-B7`, `PRM-B8`, `USR-B9`, `ORD-B10` | P1/P2 | Khoá authz gRPC cho 4 RPC promotion; lọc voucher hết hạn/hết lượt trong `ListAvailableVouchers`; ẩn OTP khỏi Subject, Việt hoá email/nhãn/tiền tệ và sửa link thông báo trỏ đúng route web dùng `order_id`. | `TestService_PRM_B4_AuthEnforcement`, `TestService_PRM_B8_*`, `services/notification` unit tests (`mailer_test.go`, `notification_test.go`, migration `000002`) |
| `PLT-B2`, `PLT-B5`, `SHP-B6`, `SHP-B7` | P1/P2 | Gateway che lỗi `Internal` và sửa thứ tự middleware để `RequestLogger` ghi `user_id`; shipping chặn cân nặng ≤ 0/mã địa giới sai và kiểm khớp `carrier_code` ở webhook. | `TestMapGRPCErrorMasksInternalErrors`, `TestRequestLoggerRecordsUserIDWhenAuthRunsInside`, `TestE2E_GatewayRules`, `TestE2E_ShippingFeeShopPickupValidationAndWebhookCarrier` |
| `WEB-B2`, `WEB-B3`, `WEB-B4`, `WEB-B9` | P1/P2 | Gỡ khối Deal sốc/Combo và lời hứa giảm theo bậc/quà 499k giả trên `/flash-sale` và PDP; đăng nhập chuyển đúng trang theo role; Mua ngay flash sale dùng giá flash thật và phân trang > 100 SP. | `05_audit_fixes.test.mjs` (5a–5d), `web/scripts/audit-fixes.test.mjs`, Playwright 1440px và 390px không tràn ngang và không còn chuỗi combo/deal |

### 4.2. Lỗi do 3 agent review độc lập tìm thêm và đã sửa ở vòng 2

| Nguồn / Mã | Mức | Mô tả ngắn gọn phần đã sửa | Bằng chứng (test / kiểm tra) |
| --- | --- | --- | --- |
| Review A `P0-1` | P0 | Hoàn tiền một phần rồi trả hàng/huỷ đơn chỉ gọi hoàn số còn lại (`TotalPayableVND - RefundedVND`) thay vì tổng cũ gây 409 bị nuốt. | `TestReviewA_P0_1_PartialRefundThenReturnOrCancel_RefundsOnlyRemaining` |
| Review A `P0-2` | P0 | Chuyển trạng thái đơn trong TX `FOR UPDATE` trước khi nhả tồn/khuyến mãi để `payment.failed`/`expired` không đua với `RetryPayment` làm hỏng `ConfirmStock`. | `TestReviewA_P0_2_PaymentFailedRaceWithRetry_DoesNotReleaseStockOrPromo` |
| Review A `P0-3` | P0 | Bảo vệ `RetryPayment` đa pod/ngắt kết nối bằng `context.WithoutCancel`, migration `0004_retry_idempotency` và namespace idempotency key theo nhóm đơn để không huỷ nhầm payment mới. | `TestReviewA_P0_3_And_ReviewB_P0_2_RetryPaymentResilienceAndScopedIdemKey` |
| Review A `P0-4` | P0 | Dùng context bù trừ cho `ListOrdersByCheckoutGroup` (lỗi thì chỉ gọi `ReleasePromotionShop`) và chốt `prevStatus`/`wasPaid` bên trong TX `FOR UPDATE` của `CancelOrder`. | `TestReviewA_P0_4_CancelOrder_CanceledCtxAndTOCTOURaceWithPaymentSucceeded` |
| Review B `P0-1` | P0 | Xử lý race `order.delivered` với `BuyerConfirmReceived`/`ReleaseEscrow` trên đơn COD bằng tự ghi thu COD idempotent trong `release_escrow` (và ở vòng 3 thu ngay trong `order.completed`). | `TestE2E_PaymentMoney_Round2_ReviewB`, 53 test Rust |
| Review B `P0-2` | P0 | Chặn IDOR `idempotency_key` toàn cục trong `create_payment` bằng cách kiểm khớp `user_id`, `order_group_id`, `amount_vnd`, `method` (lệch trả 409) cùng namespace key phía `order`. | `TestE2E_PaymentMoney_Round2_ReviewB`, `TestReviewA_P0_3_And_ReviewB_P0_2_*` |
| Review B `P0-3` | P0 | Chặn race huỷ đơn con với webhook `SUCCEEDED` VietQR đa shop bằng cách huỷ payment VietQR `pending` khi nhận `order.cancelled` và định tuyến webhook sang `LatePaymentRefund` *(hồi quy phát sinh đã gỡ ở vòng 3 bằng quyết định huỷ cả nhóm — mục 4.3 và 6)*. | `TestE2E_PaymentMoney_Round2_ReviewB`, `test_p0_3_pending_payment_with_cancelled_allocation_triggers_late_refund` |
| Review A `P1-5`, `P1-6`, `P1-7` | P1 | Miễn lọc người mua khi seller xem voucher shop mình; chặn IDOR đọc voucher theo `user_id` trong `ListAvailableVouchers`; không nuốt lỗi bù trừ sau commit ở `HandleShipmentUpdated`. | `TestService_ReviewA_P1_5_*`, `TestService_ReviewA_P1_6_*`, `TestReviewA_P1_7_*` |
| Review B `P1-4`, `P1-5` | P1 | Khoá nhóm theo `ORDER BY id ASC FOR UPDATE` + `AND status='pending'` ở đối soát/quét hết hạn, chống deadlock AB-BA và đưa giao dịch muộn về `late_payment_refund` thay vì `matched`. | `TestE2E_PaymentMoney_Round2_ReviewB` (tái hiện deadlock thật trước sửa → xanh) |
| Review C `P1-2`, `P1-3`, `P1-4` | P1 | Sửa form đăng ký/nộp lại shop trên web (hiện form khi `REQUEST_INFO`, bắt buộc kho/giấy tờ, regex SĐT VN); đổi `seed.mjs` `listAllAdminProducts` sang `/api/v1/admin/products`; gateway gửi metadata `x-client-ip` cho mọi lệnh gRPC. | `05_audit_fixes.test.mjs` (5i), `tools/seed/seed.test.mjs` + `make seed` 2 lần toàn `existing`, `TestGatewayForwardsClientIPMetadataToDownstreamGRPC` |
| Review A/B/C `P2` & `P3` | P2/P3 | Cộng dồn `refunded_vnd` mức nhóm, xoá snapshot replay sau retry, tương thích `COD_PENDING` cũ, không loại oan `FREESHIP` theo ngân sách, xử lý đơn 0₫ `RETURNED`/`CANCELLED`, chặn `supersede` nhóm chết, kiểm khớp replay `create_refund`, đồng bộ `payments.status='refunded'`, `ResubmitShop` WHERE chống race, `SplitHostPort` + fail-closed peer, che 503/504 và fail-closed SP `UNSPECIFIED`, sửa `??` ở `types.ts` và chặn `canRetryPayment` quá hạn, khai báo `USER_GRPC_ADDR` trong compose, sửa `contracts.md`, chống flaky `18_product_rules`, tự đợi HTTP 429 trong harness E2E, và dọn dead code (`AllocationSplit`...). | `TestReviewA_P2_8..10`, `TestReviewA_P3_12`, `TestService_ReviewA_P2_11_*`, `TestE2E_PaymentMoney_Round2_ReviewB`, `TestResubmitShop_ConcurrentApprovalReturnsConflict`, `TestAuditIP_*`, `TestWriteGRPCErrorMasksUnavailableAndDeadlineExceeded`, `TestPublicProductRoutesFailClosedOnUnspecifiedAndSyncTotal`, `web/scripts/audit-fixes.test.mjs` (9–10), `make e2e` 23/23 & `make e2e-web` 23/23 ×2 xanh |

### 4.3. Lỗi do review độc lập lần 2 tìm thêm và đã sửa ở vòng 3

Không phát hiện lỗi P0 mới ở review lần 2 (`review2-P-payment.md`, `review2-O-order.md`).

| Nguồn / Mã | Mức | Mô tả ngắn gọn phần đã sửa | Bằng chứng (test / kiểm DB) |
| --- | --- | --- | --- |
| Review2-P `P1-1` | P1 | Hợp nhất `record_late_payment_refund_tx` về một khoá idempotency duy nhất `late-webhook:{provider_tx_id}` ở mọi đường (webhook, quét hết hạn, đối soát), chặn tạo refund thanh toán muộn trùng gấp đôi. | `TestE2E_PaymentMoney_Round3_Review2P`, 53 test Rust; DB: 0 refund thanh toán muộn trùng |
| Review2-P `P1-2` | P1 | Thu COD ngay trong `process_order_completed_event` (`collect_cod_order_allocations_tx`) thay vì trả `Conflict` khi còn `awaiting_collection`, tránh `order.completed` rơi DLQ làm seller mất quyết toán nếu `order.delivered` trễ/mất. | `TestE2E_PaymentMoney_Round3_Review2P`, 53 test Rust |
| Review2-P `P1-3` | P1 | Xử lý race giữa `process_order_cancelled_event` / `cancel_payment` với `create_payment(supersede_previous=true)` để không bỏ sót huỷ payment `P2` mới tạo khi `P1` vừa thành `superseded`. | `TestE2E_PaymentMoney_Round3_Review2P`, 53 test Rust |
| Review2-P `P2-4..7`, `P3-8..9` | P2/P3 | Khoá `seller_balances` trước `settle_order_to_pending_tx` chống deadlock `ledger_accounts`; tách `run_reconciliation` mỗi nhóm một transaction; định tuyến giao dịch chuyển trùng (`provider_tx_id` khác) sang `LatePaymentRefund`; gộp khoá `cod-collect` theo `order_id`; sửa replay `create_refund` khi `order_id=group` trên đơn 1 shop và đồng bộ `payments.status='refunded'` ở đa shop có đơn 0₫ giao thất bại. | `TestE2E_PaymentMoney_Round3_Review2P`, 53 test Rust |
| Review2-O `P1-1` | P1 | Bỏ lọc `EventID != ""` và mở rộng `NeedsDownstream` trong `TransitionOrdersPaymentResult` (nhận mọi `curStatus != CANCELLED` cho `SUCCEEDED` và khớp cả `FAILED`/`EXPIRED`) để không rò rỉ tồn/khuyến mãi khi downstream lỗi tạm hoặc đơn đã `SHIPPING` trước lúc redeliver. | `TestReview2O_P1_1_PaymentResultRedelivery_EventIDEmptyAndShippedOrder` |
| Review2-O `P1-2` | P1 | Chặn ghi đè `orders.payment_status` và `checkout_groups.payment_status` từ `REFUNDED` ngược về `SUCCEEDED`/`EXPIRED` khi `payment.refunded` đến trước `payment.succeeded` hay trước watchdog hết hạn. | `TestReview2O_P1_2_RefundedBeforeSucceededOrExpiry_NeverDowngradesPaymentStatus` |
| Review2-O `P1-3` | P1 | Bao trọn `RetryPayment` bằng khoá phân tán `pg_advisory_xact_lock` theo `checkout_group_id` để các request song song khác `Idempotency-Key` trên nhiều replica không lưu `payment_id` đã `superseded` và không huỷ nhầm payment sống. | `TestReview2O_P1_3_MultiReplicaRetryPayment_DifferentKeysSerialized` |
| Review2-O `P1-4a` | P1 | Bọc `ReleaseStock` bằng retry idempotent và chỉ fallback `RestockOrderItems` khi chắc chắn không lỗi tạm thời, tránh cộng tồn ảo `+qty` khi huỷ đơn. | `TestReview2O_P1_4a_CancelOrder_ReleaseStockTransientErr_DoesNotRestockPhantomStock` |
| Quyết định manager + Review2-O `P1-4b` | P1 | Huỷ một đơn con VietQR chưa thanh toán (`PENDING`) chuyển sang huỷ cả nhóm checkout (1 lần `ReleaseStock`/`ReleasePromotion`/`CancelPayment`, phát `order.cancelled` từng đơn con; web hiện hộp xác nhận rõ N gian hàng và tải lại danh sách), gỡ dứt điểm hồi quy P0-3 và ca cộng tồn ảo huỷ lần lượt trước `ConfirmStock`. | `TestReview2O_ManagerDecision_CancelUnpaidVietQRSubOrder_CancelsWholeGroup`, `05_audit_fixes.test.mjs` (5j) |
| Review2-O `P2-5..7`, `P3-8..9` | P2/P3 | Sửa cập nhật `payment_status` ở 3 biên đa shop và hoàn mức nhóm; đổi `GetOrderByIDForUpdate` sang đọc không khoá trước khi khoá cả nhóm chống deadlock; kiểm `newPay.Status == PENDING` khi replay retry; đặt `payment_status=CANCELLED` cho đơn 0₫ giao thất bại và không nuốt `tlErr`. | `TestReview2O_P2_5_*`, `TestReview2O_P2_7_*`, `TestReview2O_P3_8_*` |
| Web SSR 429 → 500 (tìm khi chạy `e2e-web` 2 lần liên tiếp) | P1 | Bắt `GatewayError` ở `/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]`, `(seller)/layout`, `(admin)/layout` và chuyển tiếp `Retry-After` thay vì biến HTTP 429 thành 500; cập nhật `waitFor` trình duyệt trong `tests/e2e-web/helpers.mjs` hiểu rate limit. | `make e2e-web` 23/23 hai lần liên tiếp (lần 2 gặp 429 thật, hiển thị thông báo thân thiện và tự hồi phục), web `lint`/`format:check`/`check:data` xanh |

---

## 5. Dữ liệu chuẩn chưa (`*-D*`)

**Phần đã chuẩn**: dữ liệu lõi sau seed gồm 6 danh mục, 4 shop, 18 sản phẩm và 159 SKU khớp giữa `product_db`/`user_db` và `web/src/data/*`; 46/46 ảnh tĩnh và 2 video trả HTTP `200`; không có cột tiền kiểu số thực; `NEXUSMART_DLQ` và các bảng `dead_letter_events` bằng 0; không lộ PII hay secret trong log; sau Đợt 1, sổ cái cân và doanh thu seller khớp DB.

**Các vấn đề dữ liệu phát hiện trong 8 báo cáo và lộ trình xử lý:**

| Nhóm vấn đề dữ liệu | Mã báo cáo | Tình trạng sau Đợt 1 | Đợt xử lý |
| --- | --- | --- | --- |
| Quy mô catalog mẫu mới có 18 SP tĩnh (0 SP có biến thể lệch giá, chưa có đơn mẫu seed) so với yêu cầu **20.000–50.000 sản phẩm** của spec (`docs/spec.md:31,36`) | `CAT-D2` | Chưa xử lý | Đợt 2 (script sinh 20k–50k SP + đơn mẫu; phục vụ `ADR-0004` Đợt 3) |
| 16/16 suất flash sale seed vượt tồn kho SKU từ 3,8× đến 18× (ví dụ `BD-AT03-XANHTHAN-2XL` 90 suất vs tồn 5) | `PRM-D1` | Chưa xử lý | Đợt 2 (cắt suất theo tồn trong `seed.mjs` + `PRM-B10`) |
| Giá gốc gạch ngang và `% giảm` lấy từ dữ liệu tĩnh của web (`product_db`/`product.proto` chưa có `original_price_vnd`) | `CAT-D1`, `WEB-D2` | Chưa xử lý (đã xác nhận lại qua Playwright PDP) | Đợt 2 |
| Hoa hồng và cây danh mục con lệch giữa DB và dữ liệu tĩnh web (DB đặt cứng 6 danh mục phẳng `500 bps` = 5%; web ghi `Sợi Tự Nhiên Đà Nẵng` 5,5%, `Quần jogger` 5,5%, `Đồ lót` 6,0% và 15 danh mục con chỉ nằm ở client) | `CAT-D3`, `PAY-D3` | Chưa xử lý | Đợt 2 |
| Địa chỉ kho 4 shop và buyer demo lệch phường, lặp địa giới trong `street_address`, thiếu địa chỉ Hà Nội của buyer demo; `shipping_db.wards` chỉ có 115 phường mẫu (101/115 mã GHN giữ chỗ), `web/src/lib/provinces.ts` lệch mã GSO | `USR-D1`, `USR-D2`, `USR-D5`, `SHP-D1`, `SHP-D2`, `SHP-D3`, `SHP-D4` | Chưa xử lý | Đợt 2 |
| Tính năng mock vẫn có link trực tiếp ở header/shell và hiển thị số giả không nhãn mẫu: Chat 1-1 tự ghi "đồng bộ trực tiếp", Trợ lý AI đọc `INITIAL_ORDERS` gây link 404 cho mọi tài khoản, `/hoi-vien` & `/the-qua-tang` hiển thị `42.000 Xu`/`Hạng Bạc` giả cạnh tên thật, `Yêu thích 2` và phiếu hỗ trợ mẫu có sẵn cho user mới, badge `Tương tác 5`/`Khiếu nại mở 5` trộn cùng số thật, `/tra-cuu-don-hang`, `/admin/khieu-nai`, cấu hình ship/payment/ngân hàng/lịch sử đối soát mock | `PLT-D1`, `PLT-D2`, `PRM-D5`, `ORD-D1`, `ORD-D2`, `PAY-D4`, `SHP-D6`, `WEB-D5` | Chưa xử lý | Đợt 2 (gỡ hoặc gắn nhãn "dữ liệu mẫu" + xoá link 404); Đợt 3 thay thật bằng `ADR-0005` (Chat), `ADR-0006` (AI) |
| Chính sách và cam kết quảng cáo sai lệch trên web: > 25 vị trí ghi **"đổi trả 45 ngày, đổi size 2 chiều, bù lẻ tất"** trái với khoá cứng **7 ngày** ở backend; header hứa freeship tự động 199k/249k và `/dang-ky` hứa tặng `15.000 Xu` không có thật; copy hứa xuất "hoá đơn VAT điện tử" ngoài phạm vi | `WEB-D1`, `WEB-D4`, `WEB-D6`, `USR-D4` (phần copy), `ORD-D1` | Chưa xử lý | Đợt 2 |
| Số liệu tĩnh phủ lên trang thật: sao đánh giá `4,7–4,9` giả trên `ProductCard`, chỉ số vận hành shop tĩnh, dự tính giao hàng PDP hiển thị hãng `SPX` và "giao nhanh 2–4 giờ" (ngoài phạm vi), dải upsell tĩnh ở trang đơn hàng | `CAT-D4`, `SHP-D5`, `WEB-D3`, `ORD-D4` | Chưa xử lý | Đợt 2 |
| Voucher seed hứa điều kiện đối tượng (`EDU15`, `SINHNHAT50`, `NEXUSVIP12`, `NEXUS10`) nhưng backend áp đại trà; lệch hạn dùng và khung giờ giữa file tĩnh với DB | `PRM-D2`, `PRM-D3` | Chưa xử lý | Đợt 2 |
| KPI báo cáo và danh sách cắt ngầm ở trang 1: `/admin` cộng GMV từ 50 đơn, `/seller` cắt ở 100 đơn, tra ảnh đơn hàng cắt ở 100 SP | `PLT-D3`, `ORD-D5`, `ORD-D4` | Chưa xử lý | Đợt 2 (`PLT-B10`) |
| Lệch cấu hình & tồn dư kỹ thuật nhỏ: `POST /shops/register` mở cả cho admin trái `contracts.md`, `product` khai báo `depends_on: nats` thừa, `:48889/metrics` mở nhưng 0 byte, cụm `"tồn kho SKU"` ở 4 trang nội bộ, danh mục test kẹt FK `CAT-D5` | `PLT-D4`, `WEB-D7`, `CAT-D5` | `CAT-D5` sạch sau reset; các mục còn lại chưa xử lý | Đợt 2 (`CAT-B3`, `CAT-B8`, dọn copy); Đợt 3 / `ADR-0007` (`:48889`) |
| Lệch tiền/trạng thái `order_db`–`payment_db`, lệch số dư seller, `audit_logs` thiếu và sai IP, `phone_verified=true` cho SĐT rỗng, Deal sốc & Combo giả trên `/flash-sale` | `ORD-D3`, `PAY-D1`, `PAY-D2`, `PAY-D5`, `USR-D3`, `USR-D4` (phần DB), `PRM-D4` | **Đã xử lý ở Đợt 1** (còn tất toán `refund_payable` ở Đợt 2) | Xong Đợt 1 |

---

## 6. Rủi ro còn tồn và quyết định thiết kế vòng 3

**Hồi quy P0-3 đã được gỡ ở vòng 3 (quyết định của manager, chủ dự án có thể đổi)**: hồi quy "huỷ một đơn con VietQR chưa trả làm đơn con còn lại không thanh toán được" đã được loại bỏ bằng quyết định thiết kế của manager — huỷ một đơn con thuộc nhóm VietQR chưa thanh toán (`PENDING`) sẽ **huỷ cả nhóm checkout** (vì cả nhóm dùng chung một mã QR; hộp xác nhận trên web thông báo rõ số gian hàng bị huỷ, đồng thời chặn luôn ca cộng tồn ảo `review2-O P1-4b`). Đơn đã thanh toán và đơn COD vẫn huỷ từng đơn con như cũ. Nếu chủ dự án muốn cho phép thanh toán lại phần đơn con còn lại, có thể đổi ở Đợt 2.

**Rủi ro còn tồn sau Đợt 1:**

1. **Né rate limit IP và giả mạo IP audit qua BFF `web` (P2)**: `gateway` đặt `TRUSTED_PROXIES=web`, nhưng container `web` vẫn mở cổng trực tiếp ra `0.0.0.0` và `web/src/lib/server/session.ts:75-80` lấy hop đầu `x-forwarded-for` do client tự đặt. Bắt buộc đặt reverse proxy / edge gateway tin cậy (Envoy Gateway hoặc Traefik, `ADR-0001`/`ADR-0008`) phía trước `web` để ghi đè `X-Forwarded-For` và không mở thẳng `web` ra ngoài.
2. **Trả hàng sau khi đã nhận chưa có luồng hoàn tiền đầy đủ**: các trường hợp trả hàng sau giao (kể cả COD đã thu hộ và VietQR) chưa có quy trình duyệt trả hàng, vận đơn chiều ngược và bút toán hoàn tiền/thu hồi seller đầu-cuối (đã đánh dấu để làm ở Đợt 2: `UC-B09`, `UC-S10`, `UC-A05`, `UC-SHIP08`).
3. **Chưa có thời gian giữ tiền escrow (`PAY-B7`)**: `seller_pending` vẫn chuyển thẳng sang `seller_available` ngay khi `confirm-received`, chưa có job giữ tiền theo số ngày quy định (Đợt 2).
4. **Ảnh Docker `web` phụ thuộc mạng tải Google Fonts lúc build**: từng gặp lỗi mạng thoáng qua khi build ở pha 1C; nên chuyển sang font cục bộ trong Đợt 2.
5. **Ghi chú vận hành kiểm thử và biên kỹ thuật nhỏ**: bộ test `e2e-web` chạy dồn sát ngưỡng rate limit `300 request/phút` theo IP của gateway nên đã thêm cơ chế tự đợi và tải lại khi gặp HTTP 429 trong harness (`tests/e2e-web/helpers.mjs`, cùng xử lý thân thiện lỗi 429 ở các trang/layout SSR của `web`); web kẹp cân nặng tối thiểu 100g trong khi `order` mặc định 500g cho SKU 0g (0/181 SKU seed bị 0g); `cod_amount_vnd` của đơn flash sale COD khác công thức giữa web và order nếu bật `GHN_MODE=sandbox` (review-C `P2-14`, chưa ảnh hưởng chế độ mock); cần chạy lại `TestService_1000Concurrent_FlashSale_100Slots_AndRelease` khi máy nhàn để xác nhận ổn định (từng lỗi khi load ~102).

---

## 7. Việc chờ chủ dự án quyết

1. **Commit Đợt 1**: xem xét diff hiện tại (`137` file, `+12.999 / −3.642`) và quyết định commit toàn bộ hay tách riêng 5 file ADR `docs/adr/0004..0008`.
2. **Khởi chạy Đợt 2** và chốt 2 quy tắc nghiệp vụ:
   - Quy tắc huỷ đơn con VietQR chưa trả: giữ nguyên quyết định **huỷ cả nhóm** của manager (UI cảnh báo rõ, đã cài ở vòng 3) hay đổi sang **cho retry phần đơn con còn lại**?
   - Số ngày giữ tiền escrow (`PAY-B7`, `seller_pending → seller_available`): ví dụ chốt `7 ngày` khớp cửa sổ đổi trả `UC-B09`.
3. **Câu hỏi mở của 5 ADR nhóm B (`0004–0008`)** *(lưu ý: các mốc phiên bản/giá/trạng thái OSS như MinIO archived, `ingress-nginx` EOL, giá LLM do agent tra web ngày 2026-11-23 — cần đối chiếu lại khi duyệt)*:
   - **`ADR-0008` (quyết định gốc về nơi triển khai K8s, CI/CD, backup)**:
     1. Khi bảo vệ/demo, chạy Kubernetes ở đâu: cục bộ **`k3d`** (`$0`), thuê **VPS `k3s`** (~`$25–50/tháng`), hay **GKE Autopilot / DOKS** (nếu có credit cloud sinh viên)?
     2. Postgres trên K8s: gom nhiều DB tách quyền trên **1 cụm CloudNativePG/mỗi môi trường** hay tách mỗi service 1 cụm?
     3. Edge Gateway (`ADR-0001` + `ADR-0008`): chọn **Envoy Gateway** hay **Traefik** tích hợp sẵn trong `k3s`/`k3d` (không dùng `ingress-nginx`)?
   - **`ADR-0004` (Search service + hybrid vector)**:
     1. Duyệt thêm container **Meilisearch Community (MIT)** cạnh `pgvector`, hay tối giản **0 container tìm kiếm mới** (chỉ Postgres `unaccent` + `pg_trgm` + `pgvector`)?
     2. Đặt `product_embeddings` ở **`search_db`** (khuyến nghị, `ai-assistant` gọi `Search.HybridSearch`) hay ở `ai_db`?
     3. Từ đồng nghĩa tiếng Việt quản lý qua file JSON/YAML trong repo hay cần UI Admin?
   - **`ADR-0005` (Chat thời gian thực + Object Storage)**:
     1. Chốt Object Storage cho dev/tự host: **SeaweedFS** (Apache-2.0) hay **Garage** (AGPL-3.0); nếu lên cloud có dùng thẳng Managed S3 (R2/Spaces/S3)?
     2. Ảnh chat chỉ kiểm ≤ 5 MB + MIME hay cần nén/xoá EXIF phía server và đặt TTL lưu trữ?
     3. Tin nhắn chứa SĐT/link ngoài sàn (`UC-B14`): chỉ gắn banner cảnh báo hay chặn cứng không cho gửi?
   - **`ADR-0006` (Trợ lý AI — LLM, Embedding, RAG, Guardrail)**:
     1. Chủ dự án có sẵn tài khoản/credit API của nhà cung cấp nào (OpenAI, Google Gemini, Anthropic Claude) và trần ngân sách tối đa (USD) cho phát triển + demo là bao nhiêu?
     2. Chốt 1 nhà cung cấp cho cả LLM + Embedding (ví dụ OpenAI `gpt-5-mini`/`nano` + `text-embedding-3-small`) hay chấp nhận ghép 2 nhà cung cấp (Claude + Voyage)?
     3. Ngưỡng đạt tối thiểu cho bộ eval 100 câu (`UC-AI13`, đề xuất: Recall@5 ≥ 85%, Groundedness ≥ 90%, Hallucination SP ≤ 2%)?
   - **`ADR-0007` (Observability & AIOps Rust)**:
     1. Dùng **Grafana/Loki/Tempo (AGPL-3.0)** chạy nội bộ đúng tên đặc tả, hay đổi sang **VictoriaMetrics + VictoriaLogs** (Apache-2.0, nhẹ RAM hơn)?
     2. `UC-OPS04` (ưu tiên Thấp): bật chế độ tự động thực thi `restart pod`/`scale` trên staging để demo, hay chỉ để chế độ đề xuất + DevOps bấm xác nhận?
     3. Kênh Telegram cảnh báo: dùng 1 bot + 1 group phân biệt staging/prod bằng tag/topic hay tách riêng?

---

## 8. Cách chạy và kiểm chứng

### 8.1. Stack đang chạy và cảnh báo nâng cấp

- **Stack kiểm chứng Đợt 1**: Compose project **`nexusmart-audit`** (`.env` gốc đặt sẵn `COMPOSE_PROJECT_NAME=nexusmart-audit`, nên mọi lệnh `make up`, `make down`, `make seed`, `make e2e`, `make e2e-web` mặc định chạy vào stack này):
  - Web storefront / seller / admin: `http://localhost:43000`
  - API Gateway REST: `http://localhost:48080` (`/healthz`)
  - Mailpit UI (OTP và email giao dịch): `http://localhost:48025`
  - Cổng nội bộ (`127.0.0.1`): Postgres `45432`, Redis `46379`, NATS `44222` (monitor `48222`), HTTP ops `48081–48088`, OTel `44317`/`44318`/`48889`.
  - Tài khoản seed dev: Admin `admin@nexusmart.local` / `AdminDevPassword123!`; Seller `seller.{bachdang,saigon,danang,hanoi}@nexusmart.local` / `SellerDevPassword123!`; Buyer demo `minhquan.nguyen@nexusmart.vn` / `BuyerDevPassword123!`.
- **Hai stack cũ đang chạy song song**: `nexusmart` (web `:3000`, gateway `:28080`) và `nexusmart-web` (web `:33000`, gateway `:38080`) vẫn chạy code **trước khi sửa Đợt 1**. Khi đưa code Đợt 1 sang hai stack này, **nên reset sạch volume (`down -v`, dựng lại và `make seed`) thay vì nâng cấp tại chỗ**, vì các đơn COD lịch sử theo mô hình cũ (`payments.status = 'succeeded'` và đã ghi `cod_receivable` ngay lúc đặt) không có migration chuyển đổi dữ liệu.

### 8.2. Lệnh dựng lại sạch và chạy gate

Chạy tại gốc repo `/home/tai/Document/nexusmart`:

```bash
# 1. Reset sạch stack nexusmart-audit, rebuild ảnh và nạp dữ liệu mẫu
make down && make up && make seed

# 2. Chạy đầy đủ cổng kiểm tra chất lượng
make lint
make test
make e2e
make e2e-web

# 3. Kiểm tra tĩnh phía web
cd web && npm run lint && npm run format:check && npm run check:data
```
