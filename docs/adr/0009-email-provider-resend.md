# ADR-0009: Nhà cung cấp gửi email giao dịch cho `services/notification` (SMTP / Resend)

- **Trạng thái**: Đề xuất, chờ chủ dự án duyệt
- **Ngày**: 2026-11-26

## Bối cảnh

- **Hiện trạng trong repo**:
  - `services/notification` (Go 1.22, chỉ dùng stdlib `net/smtp` + `crypto/tls`, không thêm thư viện ngoài):
    - `services/notification/internal/mailer/mailer.go` (457 dòng) & `mailer_test.go` (899 dòng, 15 unit test): cài đặt `SMTPMailer` hai chế độ. Khi `SMTP_USERNAME` rỗng, gửi SMTP không mã hoá tới **Mailpit** (`axllent/mailpit:v1.20`, MIT trong `docker-compose.yml:83-89`) phục vụ dev/staging và E2E (`tests/e2e/02_cod_checkout_test.go`, `24_notification_w2_test.go`, `33_notification_w2b_test.go`). Khi `SMTP_USERNAME` được cấu hình, buộc phải có đủ `SMTP_PASSWORD` và bắt buộc **TLS >= 1.2** (`InsecureSkipVerify: false`, `ServerName: host`): implicit TLS cho cổng `465`/`2465`, bắt buộc `STARTTLS` cho cổng khác (từ chối nếu server không quảng bá `STARTTLS` hoặc handshake chưa hoàn tất), rồi mới xác thực `AUTH PLAIN`; lỗi SMTP được che mật khẩu/base64/PII (`sanitizeAuthError`, `sanitizeSendError`). Địa chỉ `SMTP_FROM` được kiểm hợp lệ (cấm `\r\n`, hỗ trợ `Display Name <addr>`, gửi địa chỉ trần cho `MAIL FROM` và lấy domain cho `Message-ID`).
    - `services/notification/internal/config/config.go` (86 dòng), `config_test.go` (63 dòng, 2 unit test) & `cmd/server/main.go` (255 dòng): đọc `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM`, `SMTP_USERNAME`, `SMTP_PASSWORD`; gọi `cfg.Validate()` và `mailer.NewSMTPMailer(...)` fail-fast lúc khởi động nếu lệch cặp tài khoản hoặc sai `SMTP_FROM`.
    - `services/notification/internal/service/notification.go` (2.599 dòng) & `internal/consumer/consumer.go` (453 dòng): gửi SMTP ngoài transaction Postgres, ghi dedup theo `(event_id, user_id)` sau từng thư thành công; lỗi gửi được NATS JetStream retry tối đa `NATS_MAX_DELIVER` (mặc định `5`) lần (`messageProcessTimeout = 25s`, `AckWait = 30s`) rồi đẩy sang `dead_letter_events` + `NEXUSMART_DLQ` (đã che OTP/token/PII).
  - **Cấu hình triển khai hiện tại**:
    - `deploy/helm/notification/values.yaml` (L108-110, L120-122) & `values-staging.yaml` (L19-22): giữ `SMTP_HOST: "mailpit"`, `SMTP_PORT: "1025"`, không đặt `SMTP_USERNAME`.
    - `deploy/helm/notification/values-production.yaml` (24 dòng, L20-24): đề xuất sẵn **Resend qua SMTP** (`SMTP_HOST: "smtp.resend.com"`, `SMTP_PORT: "587"`, `SMTP_USERNAME: "resend"`, `SMTP_FROM: "NexusMart <no-reply@nexusmartai.store>"`); `SMTP_PASSWORD` nạp từ Kubernetes Secret `nexusmart-notification-secret` (`deploy/k8s/create-secrets.sh:137, 180-185, 290-294`), không commit vào Git.
- **Giới hạn hiện có trong code `services/notification`**:
  - **Bỏ qua im lặng miền dành riêng khi bật xác thực**: khi `SMTP_USERNAME != ""`, `isReservedDomain` (`mailer.go:245-247, 398-424`) trả `nil` ngay không mở kết nối SMTP với các miền/subdomain `local`, `test`, `invalid`, `example`, `localhost`, `example.com`, `example.net`, `example.org` (tránh tốn hạn mức/tăng bounce từ dữ liệu seed; chế độ Mailpit vẫn gửi bình thường).
  - **Chỉ gửi văn bản thuần (`text/plain; charset=UTF-8`)**: chưa có mẫu HTML/multipart hay tệp đính kèm (`mailer.go:118`).
  - **Timeout `10 giây` cho mỗi lần gửi & không tái sử dụng kết nối**: `defaultSMTPTimeout = 10s` (`mailer.go:21`), mở kết nối TCP/TLS mới cho từng email, chưa có connection pool, rate limiter hay circuit breaker riêng phía client; khi một event gửi nhiều người nhận tuần tự, tổng thời gian bị giới hạn bởi `messageProcessTimeout = 25s` ở consumer.
  - **Chưa có webhook bounce/complaint và suppression list**: chỉ xác nhận ở mức relay chấp nhận thư qua SMTP, chưa xử lý trả lại (hard bounce) hay khiếu nại spam bất đồng bộ từ nhà cung cấp.
- **Ràng buộc kiến trúc**: nơi triển khai đã chốt là **`k3d` cục bộ (`$0`, không thuê VPS/cloud)** theo [ADR-0008](0008-kubernetes-cicd-and-backup.md); không có IP công cộng sạch hay PTR/rDNS; mọi lần bật khoá/dịch vụ trả phí hoặc gửi thật ra ngoài cần chủ dự án duyệt.

## Ứng viên

1. **SMTP tự vận hành (Postfix hoặc Postal)**
   - **Postfix**: dual-license **IPL 1.0 / EPL 2.0**; bảo trì tích cực, bản ổn định mới nhất **`3.11.8`** (`2026-11-22`). Nhẹ tài nguyên nhưng là MTA thuần: phải tự dựng và vận hành OpenDKIM, SPF, DMARC, TLS, IP tĩnh, PTR/rDNS, quản trị hàng đợi và uy tín IP; không có Helm chart chính thức.
   - **Postal** (`postalserver/postal`): **MIT**; bảo trì tích cực, bản mới nhất **`v3.5.0`** (`11/2026`). Cần tối thiểu ~4 GB RAM, 2 CPU và thêm **MariaDB + RabbitMQ** (phát sinh thêm 2 thành phần hạ tầng ngoài Postgres/NATS); hỗ trợ chính thức qua Docker Compose (chưa có Helm chart chính thức), cần cổng `25` ra/vào không bị chặn và IP công cộng kèm PTR/rDNS.
   - **Client Go/Rust**: giao tiếp SMTP chuẩn (`net/smtp` có sẵn; Rust chưa cần gửi mail).
2. **Amazon SES (qua giao diện SMTP)**
   - **Loại / giao thức**: dịch vụ thương mại AWS (không OSS); hỗ trợ SMTP chuẩn (`email-smtp.<region>.amazonaws.com`), bắt buộc TLS (STARTTLS `25`/`587`/`2587`, TLS Wrapper `465`/`2465`), xác thực bằng SMTP credentials tạo từ IAM theo từng region.
   - **Vận hành & giới hạn**: tài khoản mới mặc định ở **Amazon SES Sandbox** (chỉ gửi tới địa chỉ/domain đã xác minh, tối đa 200 thư/24h, 1 thư/giây); muốn gửi thật phải xin duyệt **Production Access** qua AWS Support kèm quy trình bounce/complaint. Cần tài khoản AWS, quản trị IAM và phương thức thanh toán.
   - **Giá và hạn mức sau khi rời sandbox**: **cần kiểm** trên bảng giá AWS theo region trước khi dùng.
   - **Client Go/Rust**: dùng trực tiếp `net/smtp` có sẵn, không cần SDK AWS.
3. **Resend (qua giao diện SMTP)**
   - **Loại / giao thức**: dịch vụ email giao dịch thương mại (không OSS); hỗ trợ SMTP chuẩn (`smtp.resend.com`, username `resend`, password là API key `re_...`), hỗ trợ Implicit TLS (`465`/`2465`) và STARTTLS (`25`/`587`/`2587`, dùng `2587`/`2465` nếu ISP chặn `587`/`465`). Chỉ gửi đi (send-only), yêu cầu xác minh tên miền gửi qua DNS (SPF/DKIM).
   - **Vận hành**: không cần IP công cộng, không cần mở cổng `25`, chạy được ngay từ `k3d` cục bộ lẫn môi trường production sau này; giữ nguyên **Mailpit** cho dev/staging/E2E.
   - **Giá và hạn mức (gói miễn phí, số thư/ngày, số thư/tháng, rate limit req/s)**: **cần kiểm** trực tiếp trên bảng giá/dashboard Resend (tài liệu xác nhận SMTP dùng chung rate limit với HTTP API theo gói).
   - **Client Go/Rust**: dùng trực tiếp `net/smtp` có sẵn, không gắn SDK Resend vào code.

## Tiêu chí

| Tiêu chí | SMTP tự vận hành (Postfix / Postal) | Amazon SES (SMTP) | Resend (SMTP) |
| --- | --- | --- | --- |
| Độ phức tạp vận hành | Rất cao (IP tĩnh, PTR/rDNS, cổng 25, uy tín IP; Postal thêm MariaDB + RabbitMQ, ~4 GB RAM) | Trung bình (tài khoản AWS, IAM SMTP credentials, quy trình xin thoát Sandbox) | Thấp (xác minh DNS domain + 1 API key trong Secret) |
| Khớp `k3d` cục bộ ([ADR-0008](0008-kubernetes-cicd-and-backup.md)) & Go `net/smtp` | Kém (mạng local không có IP sạch/PTR, ISP thường chặn cổng 25; máy 14,6 GB RAM không nên gánh Postal) | Trung bình (chạy được qua 587/465, nhưng phát sinh tài khoản cloud AWS) | Tốt (chạy qua 587/465/2587/2465, 0 thay đổi code Go) |
| Rủi ro gửi thư & bảo trì | Cao (dễ rơi vào spam/blacklist nếu IP chưa warm-up) | Thấp (uy tín IP tốt, nhưng bị khoá sandbox nếu chưa xử lý bounce/complaint) | Thấp–trung bình (cần xác minh DNS; thiếu webhook bounce trong code hiện tại) |
| Chi phí & hạn mức | Phí VPS/IP tĩnh (không phù hợp `$0` cục bộ); chi phí thực tế **cần kiểm** | Theo sản lượng AWS (**cần kiểm**); Sandbox 200 thư/24h, 1 thư/s | **Cần kiểm** bảng giá và hạn mức gói miễn phí/trả phí trước khi bật live |
| Bảo mật | Tự quản lý vá lỗi MTA, TLS, chống open relay | Bắt buộc TLS + IAM credentials theo region | Bắt buộc TLS (code ép >= TLS 1.2) + API key cấp quyền gửi trong K8s Secret |
| Khoá nhà cung cấp & quay lui | Không khoá dịch vụ ngoài, nhưng tốn công di chuyển hạ tầng | Rất thấp ở tầng code (SMTP chuẩn); gắn nhẹ với IAM/AWS | Rất thấp ở tầng code (SMTP chuẩn, không dùng SDK Resend) |

## Quyết định

- **Đề xuất**: Chọn **Resend qua SMTP** làm nhà cung cấp gửi email giao dịch cho cấu hình production (`deploy/helm/notification/values-production.yaml`), đồng thời **giữ nguyên Mailpit** cho dev, staging và E2E. Không thêm SDK riêng vào `services/notification`; toàn bộ giao tiếp giữ ở giao thức chuẩn SMTP (`net/smtp` + TLS bắt buộc).
- **Cách quay lui / đổi nhà cung cấp**: Không cần sửa code hay build lại image. Chỉ đổi biến cấu hình trong `deploy/helm/notification/values-production.yaml` (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_FROM`) và cập nhật `SMTP_PASSWORD` trong Secret `nexusmart-notification-secret` rồi rollout `notification` (ví dụ chuyển sang `email-smtp.<region>.amazonaws.com` cho Amazon SES, hoặc quay về `SMTP_HOST: "mailpit"`, `SMTP_PORT: "1025"`, để trống `SMTP_USERNAME`/`SMTP_PASSWORD` cho sandbox nội bộ).
- **Điều kiện kích hoạt xem xét lại / chuyển đổi**:
  - Sản lượng hoặc chi phí Resend vượt Amazon SES (sau khi kiểm bảng giá thực tế), hoặc hệ thống chuyển lên hạ tầng AWS production.
  - Phát sinh yêu cầu bắt buộc về **webhook bounce/complaint + suppression list**, mẫu email HTML/multipart, hoặc tải gửi cao khiến việc mở kết nối SMTP mới mỗi thư (`10s`/thư, consumer `25s`) trở thành nút thắt.
- **Phụ thuộc**: [ADR-0008](0008-kubernetes-cicd-and-backup.md) (quản lý Secret trên `k3d`/K8s); xác minh bản ghi DNS (SPF/DKIM/DMARC) cho miền `nexusmartai.store`; kiểm tra lại bảng giá và hạn mức thực tế của Resend trước khi cấp API key thật.
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Tích cực**:
  - Giữ `services/notification` gọn nhẹ (0 dependency ngoài cho SMTP), bảo đảm mọi kiểm thử E2E/staging chạy hoàn toàn ngoại tuyến qua Mailpit.
  - Tránh chi phí và rủi ro vận hành MTA tự host (cổng 25, PTR/rDNS, uy tín IP, MariaDB/RabbitMQ) cũng như thủ tục xin thoát AWS SES Sandbox ở giai đoạn đồ án.
  - Chuyển đổi nhà cung cấp hoặc quay lui về Mailpit chỉ bằng thay đổi cấu hình (`SMTP_HOST`/Secret).
- **Tiêu cực & rủi ro cần theo dõi**:
  - Phụ thuộc dịch vụ bên thứ ba khi gửi email thật; giá và hạn mức gói Resend **cần kiểm** trước khi bật production.
  - Vì code hiện chưa nhận webhook bounce/complaint, chỉ mới chặn miền dành riêng (RFC 2606/6761) để tránh dữ liệu seed làm hỏng uy tín domain; cần bổ sung xử lý bounce/suppression trước khi gửi đại trà cho người dùng thật.
  - Thiếu connection pooling/circuit breaker và chỉ gửi plain text (`text/plain`); nếu ISP chặn cổng `587`, phải đổi `SMTP_PORT` sang `2587` (STARTTLS) hoặc `465`/`2465` (implicit TLS).

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-26
- **Nguồn**:
  - Postfix: https://www.postfix.org/
  - Postal: https://github.com/postalserver/postal
  - Amazon SES (SMTP interface): https://docs.aws.amazon.com/ses/
  - Resend (SMTP docs): https://resend.com/docs
  - Mailpit (dev/e2e): https://github.com/axllent/mailpit/releases
