# ADR-0005: Chat thời gian thực và Object Storage cho media

- **Trạng thái**: Chấp nhận
- **Ngày**: 2026-11-23 (chấp nhận 2026-11-24 theo uỷ quyền)

## Bối cảnh

- **Hiện trạng trong repo** (`docs/audit/plt.md` mục 0, `UC-B14`, `UC-S09`, `PLT-D2`; `docs/audit/cat.md` `CAT-F8`):
  - Chưa có `services/chat`, `proto/nexusmart/chat/`, hay WebSocket route trong `services/gateway/internal/router/router.go`.
  - Web hiện mô phỏng hoàn toàn trong RAM (`BuyerChatPanel.tsx:150` dùng `setTimeout(850ms)`, `/seller/chat` đọc `MockStoreContext`/`sellerStore`), không truyền qua mạng, không lưu DB.
  - Chưa có Object Storage trong stack (`docker-compose.yml` chỉ có Postgres, Redis, NATS, Mailpit, OTel). Ảnh sản phẩm hiện là URL tĩnh trong `web/public/`; cả **ảnh chat (`UC-B14`)**, **upload ảnh sản phẩm/đánh giá (`UC-S02`, `UC-B10`)**, **nhập hàng loạt CSV (`UC-S13`)** và **sao lưu WAL Postgres (`UC-OPS09`)** đều cần một tầng lưu trữ đối tượng chuẩn S3.
- **Use case & tiêu chí đo trích từ `docs/spec.md`**:
  - `UC-B14` (Trung bình): chat 1-1 người mua ↔ người bán qua WebSocket; đính kèm **ảnh**, **product card**, **mã đơn**; đồng bộ lịch sử đa thiết bị; tự kết nối lại và tải tin còn thiếu khi rớt mạng; người nhận offline thì lưu tin và gửi thông báo (`Notification`); cảnh báo khi tin chứa SĐT/link ngoài sàn; trạng thái đã xem.
  - `UC-S09` (Trung bình): hộp chat seller, lọc chưa trả lời, gửi product card/voucher, cài **tin chào tự động**, **trả lời ngoài giờ**, **câu trả lời nhanh**, chặn người dùng spam, tính **tỷ lệ và thời gian phản hồi** của gian hàng.
  - Liên kết nghiệp vụ: `UC-A15` (từ khoá cấm), `UC-AI10` (AI gợi ý câu trả lời cho seller), `UC-AI12` (chuyển tiếp hội thoại AI sang nhân viên CSKH).
  - **Tiêu chí định lượng** (`docs/spec.md:30`): WebSocket + Redis pub/sub để chạy nhiều instance; lưu tin nhắn + đồng bộ lại khi mất kết nối; mục tiêu **1.000 kết nối đồng thời trên một instance**.
  - **Ngoài phạm vi**: gọi thoại, gọi video, chatbot riêng của từng gian hàng (`docs/spec.md:30`).
- **Stack đã chốt**: Go 1.22, PostgreSQL 16, Redis 7 (`go-redis/v9`), NATS JetStream 2.10, `services/gateway` (`chi/v5`).

## Ứng viên

### Phần A — Kiến trúc Chat thời gian thực

1. **Tự viết `services/chat` bằng Go (`coder/websocket` + Redis 7 Pub/Sub + `chat_db` Postgres)**
   - **Phiên bản & bảo trì**: `github.com/coder/websocket` `v1.8.15` (kế thừa `nhooyr/websocket`, bảo trì tích cực, hỗ trợ `context.Context` chuẩn Go); dùng sẵn `redis:7-alpine` và `postgres:16`.
   - **Giấy phép**: **ISC** (tương đương MIT, OSI).
   - **Vận hành & K8s**: 1 microservice Go chuẩn theo mẫu repo (`:50060/:8090`, `/healthz`, `/readyz`, `/metrics`); 1.000 kết nối WS trên Go tốn ~20–40 MB RAM; scale ngang nhiều pod nhờ Redis Pub/Sub kênh `chat:user:<user_id>` và `chat:shop:<shop_id>`; khớp nguyên văn `docs/spec.md:171`.
   - **Đồng bộ & nghiệp vụ**: Mỗi hội thoại có `seq` tăng đơn điệu (hoặc UUIDv7 + `created_at`) trong `chat_db`; client rớt mạng gửi `GET /conversations/{id}/messages?after_seq=<n>` để bù tin thiếu không bỏ sót; lọc SĐT/link/từ cấm, auto-reply ngoài giờ và thống kê tỷ lệ phản hồi xử lý đồng bộ ngay trong `services/chat`.

2. **Centrifugo OSS + `services/chat` Go (tách tầng WebSocket)**
   - **Phiên bản & bảo trì**: `centrifugal/centrifugo` `v6.10.3` (2026-11-09); bảo trì rất tích cực (viết bằng Go; bản PRO ở repo riêng `centrifugo-pro` là thương mại).
   - **Giấy phép**: **Apache-2.0** (bản OSS).
   - **Client Go / Web**: SDK JS `centrifuge-js` (MIT), Go HTTP/gRPC server API (`gocent`).
   - **Vận hành & K8s**: Thêm 1 cụm dịch vụ riêng + Helm chart riêng, có sẵn presence/recovery/scale hàng trăm nghìn kết nối, nhưng vẫn phải viết `services/chat` để xử lý lưu DB, lọc SĐT/spam, auto-reply, product card → tăng độ phức tạp không cần thiết ở ngưỡng 1.000 kết nối/instance.

3. **NATS JetStream WebSocket (client nối thẳng NATS)**
   - **Phiên bản & bảo trì**: `nats:2.10-alpine` (đã có trong stack), Apache-2.0.
   - **Hạn chế**: Mở NATS ra trình duyệt cần hệ thống phân quyền NATS JWT/NKey riêng tách khỏi JWT `services/user`, khó chèn bộ lọc nội dung (chặn SĐT/từ cấm, auto-reply, kiểm `shop_id` ownership) trước khi phát tán tin, dễ phá vỡ biên giới bảo mật nội bộ của message broker.

#### Bảng so sánh — Tầng Chat

| Tiêu chí | Go `coder/websocket` + Redis Pub/Sub | Centrifugo OSS + `services/chat` | NATS WebSocket trực tiếp |
| --- | --- | --- | --- |
| Độ khớp tiêu chí (1k conn/instance, sync, auto-reply, lọc SĐT) | **Rất tốt** (khớp nguyên văn spec, chủ động toàn bộ logic) | Tốt (mạnh về WS transport, nhưng vẫn phải tự viết logic nghiệp vụ) | Kém (khó chèn lọc nội dung & phân quyền theo `shop_id`/JWT) |
| Độ phức tạp vận hành | **Thấp** (tái dụng Redis 7 + Postgres 16 sẵn có) | Trung bình (thêm 1 cụm Centrifugo + SDK protocol riêng) | Trung bình–cao (phải vận hành hệ NATS auth/NKey riêng ra biên) |
| Chi phí hạ tầng | **Rất thấp** (~50–100 MB RAM/pod) | Thấp–trung bình (thêm pod Centrifugo) | Thấp |
| Giấy phép | **ISC** (`coder/websocket`) | **Apache-2.0** (OSS) / Thương mại (PRO) | **Apache-2.0** |
| Mức độ trưởng thành | Cao (thư viện chuẩn Go WS) | Rất cao (dùng ở quy mô hàng trăm nghìn kết nối) | Cao (nhưng thiết kế cho messaging hạ tầng hơn là chat sàn) |

---

### Phần B — Object Storage (S3-compatible) cho ảnh chat và media toàn sàn

> **Cảnh báo tra web (2026-11-23)**: **MinIO Community (`minio/minio`, AGPL-3.0) bị loại** vì đã ngừng phát hành binary/image công khai từ 10/2025, chuyển maintenance mode từ 12/2025 và **lưu trữ (`archived`) read-only ngày 2026-04-25**, không còn bản vá bảo mật CVE.

1. **SeaweedFS (Open Source Edition)**
   - **Phiên bản & bảo trì**: `seaweedfs/seaweedfs` tối thiểu **`4.55`** (`2026-11-23`), tiếp tục cập nhật đều đặn trên GitHub; viết bằng Go. *(Không dùng `v4.42` hay các bản `<= 4.47` do loạt GHSA công bố `2026-11-09` — xem mục `Tra lại 2026-11-24`.)*
   - **Giấy phép**: **Apache-2.0** (bản Open Source trên GitHub; bản Enterprise tính phí riêng cho tính năng doanh nghiệp nâng cao).
   - **Client Go / Rust**: Giao tiếp qua **S3 API chuẩn** bằng `github.com/aws/aws-sdk-go-v2/service/s3` `v1.116.0` (Apache-2.0) và Rust crate `aws-sdk-s3` `v1.161.0` (Apache-2.0) — không khoá nhà cung cấp.
   - **Vận hành & K8s**: 1 container đơn (`weed server -s3` / `weed mini`) trong `docker-compose.yml` (~150–250 MB RAM); có Helm chart và `seaweedfs-operator` chính thức cho K8s; hỗ trợ presigned URL, bucket lifecycle, Prometheus `/metrics`. Bắt buộc khoá mạng nội bộ (chỉ mở cổng S3 trong mạng nội bộ compose/k8s, tắt S3 Tables/Iceberg/Lance/STS, bắt buộc filer signing key). Đáp ứng chung cho ảnh chat, ảnh SP/review và mục tiêu S3 cho backup WAL (`UC-OPS09`).

2. **Garage (Deuxfleurs)**
   - **Phiên bản & bảo trì**: `Deuxfleurs/garage` `v2.5.0` (2026-10-24); viết bằng Rust, bảo trì đều bởi tập thể Deuxfleurs.
   - **Giấy phép**: **AGPL-3.0** (cần lưu ý copyleft mạng nếu sửa đổi code).
   - **Client Go / Rust**: Qua S3 API chuẩn (`aws-sdk-go-v2`, `aws-sdk-s3`).
   - **Vận hành & K8s**: Rất nhẹ (~50–100 MB RAM), thiết kế cho self-host quy mô nhỏ–vừa; có Helm chart chính thức; hỗ trợ S3 cơ bản (presigned URL, multipart), nhưng thiếu một số tính năng S3 nâng cao (versioning/object lock đầy đủ) và cần bước bootstrap layout cluster bằng CLI.

3. **RustFS**
   - **Phiên bản & bảo trì**: `rustfs/rustfs` `v1.0.6` (2026-11); viết bằng Rust, phát triển rất nhanh nhằm thay thế MinIO.
   - **Giấy phép**: **Apache-2.0**.
   - **Client Go / Rust**: Qua S3 API chuẩn (`aws-sdk-go-v2`, `aws-sdk-s3`).
   - **Vận hành & K8s**: Nhẹ, hiệu năng cao, API tương thích S3/MinIO, nhưng mới đạt nhánh `1.0.x` nên độ chín production, kiểm chứng backup WAL dài hạn và hệ sinh thái K8s Operator còn non hơn SeaweedFS.

4. **Managed S3-compatible trên Cloud (Cloudflare R2 / DigitalOcean Spaces / GCS S3 / AWS S3)**
   - **Vận hành**: 0 công quản trị đĩa/HA/backup; chi phí rất nhỏ cho đồ án (< $1–5/tháng với 10–50 GB). Phụ thuộc quyết định nơi triển khai (`ADR-0008`).

#### Bảng so sánh — Object Storage

| Tiêu chí | SeaweedFS (OSS) | Garage | RustFS | Managed S3 Cloud (R2/Spaces/S3) |
| --- | --- | --- | --- | --- |
| Độ khớp tiêu chí (S3 presigned URL, ảnh chat/SP + backup WAL) | **Rất tốt** (S3 đầy đủ, phục vụ tốt cả media lẫn backup) | Tốt (đủ media + backup cơ bản; thiếu vài API S3 nâng cao) | Tốt (S3 chuẩn, còn mới) | **Xuất sắc** (độ bền 99.999999999%) |
| Độ phức tạp vận hành | Thấp–trung bình (1 container local; Helm/Operator trên K8s) | Thấp (nhẹ, cần init layout CLI) | Thấp (mới, ít tài liệu vận hành lỗi thực tế) | **Rất thấp** (managed hoàn toàn) |
| Chi phí | $0 phần mềm (~200 MB RAM + PVC) | $0 phần mềm (~80 MB RAM + PVC) | $0 phần mềm (~100 MB RAM + PVC) | ~$0–5/tháng (cần thẻ/tài khoản cloud) |
| Giấy phép | **Apache-2.0** | **AGPL-3.0** | **Apache-2.0** | Dịch vụ thương mại (chuẩn giao thức S3 mở) |
| Mức độ trưởng thành | **Cao** (phát triển lâu năm, nhiều hệ production dùng) | Trung bình–cao (tốt cho cụm nhỏ) | Thấp–trung bình (mới ra `1.0.x`) | Rất cao |

## Quyết định

- **Đề xuất**:
  1. **Chat**: Xây `services/chat` bằng **Go + `coder/websocket` (`v1.8.15`) + Redis 7 Pub/Sub + `chat_db` (PostgreSQL 16)**.
     - Xác thực WS an toàn: trình duyệt gọi `POST /api/v1/chat/ws-ticket` (qua BFF/JWT như bình thường) để lấy **ticket dùng 1 lần TTL 30 giây** lưu trên Redis (`chat:ws_ticket:<id>`), rồi mở WS với `?ticket=...` — tránh đưa JWT dài hạn lên URL/access log.
     - `services/gateway` mở reverse proxy WS (`/api/v1/chat/ws`) có kiểm origin/ticket và heartbeat ping/pong 30s; khi người nhận offline, `services/chat` phát event `chat.message.offline` lên NATS để `services/notification` tạo thông báo in-app/push.
  2. **Object Storage**: Chọn **SeaweedFS (tối thiểu `4.55`, Apache-2.0)** cho môi trường local/tự host/`k3d`, và chuẩn hoá mọi giao tiếp qua **S3 API (`aws-sdk-go-v2/service/s3 v1.116.0`)** để có thể đổi sang **Managed S3 (R2/Spaces/GCS/S3)** chỉ bằng biến môi trường (`S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`) nếu sau này chủ dự án duyệt lên cloud.
     - **Ràng buộc bảo mật bắt buộc cho SeaweedFS (tra 2026-11-24)**: tối thiểu bản **`4.55`** (`2026-11-23`), **KHÔNG khẳng định `4.55` đã vá hết** vì metadata GHSA công bố `2026-11-09` vẫn để trống trường `patched` (gồm các Critical `GHSA-25hp-4mw7-9c75`, `GHSA-p983-2j86-34vj`, `GHSA-jq8h-9mjh-xv8f`, `GHSA-47fc-cj9r-ghpm`, `GHSA-f6rh-hw92-h9rx`). Trước khi triển khai B2 phải tra lại từng GHSA. Khi chạy bắt buộc: **chỉ mở cổng S3 trong mạng nội bộ compose/k8s**, **không công bố cổng gRPC/admin/STS/S3 Tables/Iceberg ra host**, **không bật S3 Tables/Iceberg/Lance/STS**, **presigned URL đi qua gateway hoặc domain nội bộ** (proxy streaming có kiểm giới hạn, không buffer toàn bộ binary qua Next.js BFF để tránh cạn RAM như `PLT-B6`), và **bắt buộc cấu hình filer signing key**. **Nếu tại thời điểm B2 vẫn còn Critical chưa vá chạm cấu hình này thì dừng, hỏi lại.**
     - Luồng gửi ảnh dùng **Presigned PUT URL** (TTL 5 phút, giới hạn ≤ 5 MB, chỉ `image/jpeg|png|webp`, `services/chat` kiểm tra `HEAD`/magic bytes khi đính kèm) đi qua gateway/domain nội bộ (không đẩy binary qua Next.js BFF).
     - Bucket tách rõ: `nexusmart-media-public` (ảnh SP/đánh giá), `nexusmart-chat-private` (ảnh chat truy cập qua presigned GET URL có kiểm quyền thành viên hội thoại), `nexusmart-backups` (cho `UC-OPS09`).
- **Điều kiện kích hoạt chuyển đổi**:
  - Chuyển tầng WS sang **Centrifugo OSS** nếu vượt > 20.000–50.000 kết nối đồng thời hoặc cần nhiều tính năng real-time ngoài chat (live presence toàn sàn, notification push fan-out cực lớn).
  - Đổi SeaweedFS sang **Garage** hoặc **Managed S3** nếu tại thời điểm B2 còn lỗ hổng Critical chưa vá chạm cấu hình S3 nội bộ (dừng, hỏi lại chủ dự án), hoặc khi chủ dự án duyệt triển khai production trên cloud.
- **Phụ thuộc**:
  - `ADR-0001` (edge gateway Envoy/Traefik phải bật `Upgrade: websocket` và idle timeout phù hợp).
  - `ADR-0008` (đã chốt `k3d` cục bộ, không thuê VPS/cloud).
- **Chủ dự án quyết**: 2026-11-24: chủ dự án uỷ quyền cho manager chốt theo phương án đề xuất (nguyên văn: «mấy câu hỏi bạn từ đề xuất tự làm luôn đi»). Chủ dự án không duyệt riêng từng ADR.
  - **Chốt (`w3-plan.md` §1.1 câu 3)**: Tự viết `services/chat` (`coder/websocket v1.8.15` + Redis Pub/Sub) và chọn SeaweedFS tối thiểu `4.55` (Apache-2.0) gọi qua S3 API (`aws-sdk-go-v2/service/s3 v1.116.0`) kèm đầy đủ ràng buộc cô lập mạng và bảo mật ở trên.
  - **Cổng còn giữ**: đo RAM sau khi thêm; trước khi triển khai B2 phải tra lại từng GHSA Critical công bố 2026-11-09 — nếu còn Critical chưa vá chạm cấu hình này thì dừng, hỏi lại; không tự mở tài khoản cloud trả phí; push vẫn phải hỏi chủ dự án từng lần.

## Hệ quả

- **Tự viết `services/chat` + Redis Pub/Sub (đề xuất)**:
  - *Tích cực*: khớp 100% stack Go/Redis/Postgres hiện có; kiểm soát chặt ownership (`buyer_id`/`shop_id` từ JWT claims), chặn spam, cảnh báo SĐT/link, tính tỷ lệ phản hồi chính xác ngay tầng dịch vụ; dễ test tải 1.000 kết nối/instance.
  - *Tiêu cực*: phải tự quản lý vòng đời kết nối WS (graceful shutdown khi rolling update K8s, backpressure khi client chậm, giới hạn số kết nối/user).
- **SeaweedFS qua S3 API chuẩn (đề xuất)**:
  - *Tích cực*: loại bỏ rủi ro MinIO bị archive; Apache-2.0 sạch pháp lý; giải quyết 1 lần cho cả ảnh chat, ảnh sản phẩm (`UC-S02`), đánh giá (`UC-B10`), CSV import (`UC-S13`) và backup DB (`UC-OPS09`); dễ chuyển sang cloud S3 không đổi code.
  - *Tiêu cực*: thêm 1 dịch vụ stateful trong Compose/K8s cần PVC bền vững, phải cấu hình chặt mạng nội bộ/filer signing key và đường dẫn proxy streaming cho presigned URL qua gateway hoặc domain nội bộ (không mở thẳng cổng S3/gRPC/admin ra host).

## Kế hoạch triển khai

| Task | Phạm vi | Tiêu chí nghiệm thu đo được |
| --- | --- | --- |
| `T1` Hạ tầng Object Storage & thư viện S3 dùng chung | Thêm SeaweedFS (tối thiểu `4.55`, chỉ mở cổng S3 nội bộ, tắt S3 Tables/Iceberg/Lance/STS, bật filer signing key, không công bố gRPC/admin ra host) vào Compose/Helm với 3 bucket init; thêm helper S3 presign/verify gọn vào `pkg/common/storage` (`aws-sdk-go-v2/service/s3 v1.116.0`) và đường dẫn presigned URL qua gateway/domain nội bộ | Upload + đọc lại ảnh ≤ 5 MB qua presigned URL thành công; từ chối file > 5 MB hoặc sai MIME; không buffer binary qua BFF; cổng gRPC/admin không lộ ra host |
| `T2` Hợp đồng `chat.proto` & DB `chat_db` | Tạo `proto/nexusmart/chat/v1/chat.proto`, `make proto`, cập nhật `docs/contracts.md`; migration `chat_db` (`conversations`, `messages` có `seq`, `auto_reply_rules`, `blocked_users`, `shop_response_stats`) | `buf lint` xanh; migration idempotent; kiểm tra chéo quyền: user A không đọc được hội thoại của user B (`403`) |
| `T3` Core WebSocket + Redis Pub/Sub + đồng bộ rớt mạng | Dựng `services/chat` (`coder/websocket`, `:50060/:8090`, WS ticket 30s trên Redis, Redis Pub/Sub đa instance, reconnect `after_seq`, ping/pong, graceful drain) + proxy WS ở `gateway` | Chạy **2 instance `services/chat`**: user nối instance 1 gửi tin tới seller ở instance 2 độ trễ < 200 ms; ngắt mạng rồi nối lại nhận đủ 100% tin thiếu; kiểm thử tải `k6` đạt **≥ 1.000 kết nối đồng thời/instance** |
| `T4` Nghiệp vụ sàn & thông báo offline | Gửi ảnh (presigned GET private), product card (snapshot tên/giá từ `ProductService`), mã đơn (kiểm quyền `OrderService`), cảnh báo SĐT/link ngoài sàn, auto-reply chào/ngoài giờ, chặn spam, tính tỷ lệ/thời gian phản hồi, phát event NATS khi offline | Unit + integration test phủ đủ: cảnh báo SĐT/URL, auto-reply ngoài giờ, chặn user spam không gửi được tin, người nhận offline có thông báo trong `notification` |
| `T5` Nối Web Buyer & Seller (thay mock `PLT-D2`) | Thay state giả trong `BuyerChatPanel.tsx` và `web/src/app/(seller)/seller/chat/page.tsx` bằng WebSocket + REST thật | Playwright E2E với 2 ngữ cảnh trình duyệt (buyer + seller) chat qua lại realtime, gửi ảnh/product card và tải lại trang vẫn giữ đủ lịch sử |

## Câu hỏi chủ dự án cần trả lời

1. **Chốt Object Storage cho môi trường dev/tự host**: chọn **SeaweedFS** (Apache-2.0, đủ S3 cho cả media + backup WAL) hay **Garage** (nhẹ hơn nhưng AGPL-3.0)? Nếu đã có sẵn tài khoản cloud (R2/Spaces/S3) cho staging/prod thì có dùng thẳng cho môi trường ngoài local không?
   - **Trả lời (`w3-plan.md` §1.1 câu 3)**: Chốt SeaweedFS (tối thiểu `4.55`, Apache-2.0), gọi qua S3 API, đo RAM sau khi thêm và không thuê cloud trả phí (`§1.1 câu 1`); trước B2 tra lại từng GHSA Critical, nếu còn chạm cấu hình thì dừng hỏi lại.
2. **Ảnh chat cần lưu bao lâu và có cần nén/strip EXIF phía server không** (để tránh lộ toạ độ GPS người dùng và tiết kiệm dung lượng), hay chỉ cần kiểm kích thước ≤ 5 MB + MIME ở giai đoạn đồ án?
   - **Trả lời**: Chưa chốt, mặc định kiểm kích thước ≤ 5 MB + MIME/magic bytes cho giai đoạn đồ án (nén/strip EXIF bổ sung sau nếu cần), đổi được sau.
3. **Tin nhắn chứa SĐT/link ngoài sàn (`UC-B14`)**: chỉ **gắn banner cảnh báo** bên dưới tin nhắn hay **chặn cứng không cho gửi**?
   - **Trả lời**: Chưa chốt, mặc định gắn banner cảnh báo bên dưới tin nhắn (đúng `UC-B14`), đổi được sau.

## Tra lại 2026-11-24

- **SeaweedFS (đổi phiên bản & bổ sung ràng buộc bảo mật)**: GitHub API của `seaweedfs/seaweedfs` liệt kê các GHSA công bố ngày `2026-11-09` ảnh hưởng tới `<= 4.47`, trong đó các Critical gồm `GHSA-25hp-4mw7-9c75`, `GHSA-p983-2j86-34vj`, `GHSA-jq8h-9mjh-xv8f`, `GHSA-47fc-cj9r-ghpm`, `GHSA-f6rh-hw92-h9rx` (cùng High `GHSA-g96f-g546-47xf`, `GHSA-qpcw-w62h-6qfr`, Moderate `GHSA-54j8-2gxc-wr9h`); trường `patched` trên metadata GHSA để trống. Bản mới nhất là **`4.55`** (`2026-11-23`, Apache-2.0). Nâng ngưỡng tối thiểu từ `v4.42` lên `4.55` (KHÔNG khẳng định `4.55` đã vá hết), yêu cầu tra lại từng GHSA trước B2, khoá cổng S3 nội bộ, không mở gRPC/admin/STS/S3 Tables/Iceberg ra host, tắt S3 Tables/Iceberg/Lance/STS, presigned URL đi qua gateway hoặc domain nội bộ, bắt buộc filer signing key; nếu lúc B2 vẫn còn Critical chưa vá chạm cấu hình này thì dừng, hỏi lại.
- **`aws-sdk-go-v2/service/s3`**: nâng nhẹ `v1.115.0` → **`v1.116.0`** (Apache-2.0, release repo `release-2026-11-23`).
- **`coder/websocket`**: giữ nguyên **`v1.8.15`** (ISC, không CVE/GHSA).
- **Nguồn**:
  - https://github.com/seaweedfs/seaweedfs/releases/tag/4.55
  - https://github.com/seaweedfs/seaweedfs/security/advisories
  - https://github.com/seaweedfs/seaweedfs/blob/master/LICENSE
  - https://github.com/aws/aws-sdk-go-v2/releases/tag/release-2026-11-23
  - https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3
  - https://github.com/coder/websocket/releases/tag/v1.8.15

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-23
- **Nguồn**:
  - `coder/websocket`: https://github.com/coder/websocket, https://pkg.go.dev/github.com/coder/websocket
  - Centrifugo: https://github.com/centrifugal/centrifugo, https://github.com/centrifugal/centrifugo/releases
  - SeaweedFS: https://github.com/seaweedfs/seaweedfs, https://github.com/seaweedfs/seaweedfs/releases
  - Garage: https://garagehq.deuxfleurs.fr/, https://git.deuxfleurs.fr/Deuxfleurs/garage
  - RustFS: https://github.com/rustfs/rustfs, https://github.com/rustfs/rustfs/releases
  - MinIO (trạng thái archived): https://github.com/minio/minio
  - AWS SDK S3 Go/Rust: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3, https://crates.io/crates/aws-sdk-s3
