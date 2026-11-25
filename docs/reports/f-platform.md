# Báo cáo Swarm F – Platform (Kubernetes, CI, Kiểm thử tải, Playwright, Sao lưu)

- **Ngày thực hiện:** 2026-11-21
- **Phạm vi sở hữu (theo `m-all-contract.md`):** `deploy/helm/`, `deploy/k8s/`, `.github/workflows/`, `tests/load/`, `tests/playwright/`, `tools/backup/`, thêm target vào `Makefile`, báo cáo `docs/reports/f-platform.md` và ghi nhận lỗi vào `docs/reports/issues.md`.
- **Chiếu theo đặc tả (`docs/spec.md`):** Hàng "Hạ tầng và CI/CD", "Kiểm thử", "Dữ liệu", "Bảo mật".

---

## 1. Công cụ user-local đã cài (không sudo, tại `~/.local/bin`)

| Công cụ | Phiên bản thật | Đường dẫn |
|---|---|---|
| `kubectl` | `Client Version: v1.37.2` (Kustomize `v5.8.1`) | `~/.local/bin/kubectl` |
| `helm` | `v4.3.2+g8346aaa` | `~/.local/bin/helm` |
| `kind` | `v0.33.0 go1.26.7 linux/amd64` | `~/.local/bin/kind` |
| `kubeconform` | `v0.8.0` | `~/.local/bin/kubeconform` |
| `k6` | `v2.4.0 (go1.26.11, linux/amd64)` | `~/.local/bin/k6` |

---

## 2. Phần đã hoàn thành

### 2.1 Kubernetes & Helm (`deploy/helm/`, `deploy/k8s/`, `Makefile`)
- **14 Helm chart độc lập** (182 file, `0` file nén `.tgz`) cho đủ mọi service theo cổng hợp đồng (`m-all-contract.md`):
  - Core: `gateway` (HTTP 8080, **HPA**, Ingress), `user` (8081 / gRPC 50051), `product` (8082 / 50052), `cart` (8083 / 50053), `promotion` (8084 / 50054), `order` (8085 / 50055, **HPA**), `payment` (8086 / 50056, Rust), `shipping` (8087 / 50057), `notification` (8088 / 50058).
  - Service mới A/B/C/E: `search` (8089 / 50059, **HPA**, pgvector + Meilisearch 7700), `chat` (8090 WS / 50060), `assistant` (8091 SSE / 50061, `LLM_MODE=mock`, `EMBED_MODE=mock`), `aiops` (8092, Rust, `AIOPS_DRY_RUN=true`).
  - Frontend: `web` (HTTP 3000, UID/GID 1000, probe `/`, Ingress).
- Mỗi chart có đủ `Chart.yaml`, `values.yaml`, `values-staging.yaml`, `values-production.yaml`, và template `deployment.yaml`, `service.yaml`, `configmap.yaml`, `serviceaccount.yaml`, `hpa.yaml`, `pdb.yaml`, `ingress.yaml`:
  - **Cấu hình tách khỏi image:** biến không nhạy cảm nằm trong `ConfigMap` (kèm annotation `checksum/config` để rolling restart khi đổi config).
  - **Secret không nằm trong repo:** Pod nạp Secret ngoài qua `secrets.existingSecret` (`optional: false`); `0` manifest `kind: Secret` được commit hay render ra từ chart.
  - **Readiness/Liveness probe:** `/readyz` và `/healthz` cho 13 backend service, `/` cho `web`.
  - **Rolling update:** `type: RollingUpdate`, `maxUnavailable: 0`, `maxSurge: 1`.
  - **HPA (`autoscaling/v2`):** bật mặc định cho `gateway`, `order`, `search` (CPU 70% / Memory 80%).
  - **PDB (`policy/v1`):** bật ở `values-production.yaml` cho cả 14 service.
  - **Pod/Container SecurityContext:** `runAsNonRoot: true`, `runAsUser`/`runAsGroup` cố định (`10001` cho backend, `1000` cho `web`), `allowPrivilegeEscalation: false`, `capabilities.drop: ["ALL"]`, `seccompProfile.type: RuntimeDefault`, `automountServiceAccountToken: false`.
- **`deploy/k8s/`:** `namespaces.yaml` (`nexusmart-staging`, `nexusmart-production`), `kind-config.yaml`, `create-secrets.sh` (tạo 13 Secret theo từng service lúc triển khai; fail-closed với `nexusmart-production` nếu thiếu biến môi trường), `scripts/lint.sh`, `scripts/rollback.sh`, `README.md`.
- **`Makefile`:** bổ sung các target `helm-lint`, `rollback` (`make rollback SERVICE=<svc> REV=<rev>`), `backup`, `restore-drill` (chỉ thêm mới, không sửa target của swarm khác).

### 2.2 CI/CD & Quét bảo mật (`.github/workflows/`)
- **`.github/workflows/ci.yml`** (chỉ `on: workflow_dispatch`, `permissions: contents: read`):
  - Mở rộng matrix cho service mới `search`, `chat`, `assistant`, `aiops` (có kiểm tra tồn tại `go.mod`/`Cargo.toml`/`Dockerfile`).
  - Thêm job `helm-lint` chạy `deploy/k8s/scripts/lint.sh` (`helm lint --strict` + `helm template | kubeconform -strict`) và kiểm tra không có `kind: Secret` trong `deploy/`.
- **`.github/workflows/security.yml`** (chỉ `on: workflow_dispatch`, `permissions: contents: read`):
  - `go-vulncheck`: `govulncheck@v1.1.4 ./...` cho toàn bộ 13 Go module.
  - `rust-audit`: `cargo audit` cho `services/payment` và `services/aiops`.
  - `npm-audit`: `npm audit --omit=dev --audit-level=high` cho `web/` và `npm audit --audit-level=high` cho `tests/playwright/`.
  - `helm-and-k8s-lint`: `helm lint` + `kubeconform` + kiểm tra không có `kind: Secret`.
  - `trivy-iac-scan`: Trivy `config` trên `deploy/helm` và `fs` (`misconfig,secret`) trên Dockerfile/repo (`CRITICAL,HIGH`, `exit-code: 1`).
  - `trivy-image-scan`: build cục bộ (`load: true`) và quét Trivy cho toàn bộ 14 image service (`CRITICAL,HIGH`, `ignore-unfixed: true`, `exit-code: 1`).

### 2.3 Kiểm thử tải k6 (`tests/load/`)
- Zero npm dependency, dùng thuần stdlib k6 (`k6/http`, `k6/net/grpc`, `k6/crypto`, `k6/metrics`):
  - `tests/load/lib/common.js`: helper đăng nhập, đăng ký + lấy OTP từ Mailpit (`/api/v1/search`), tạo địa chỉ 2 cấp, tạo sản phẩm tạm nhiều SKU (`skuCount`) để không làm cạn tồn kho seed, tính phí ship, ký JWT HS256, phân tách metric 429 vs 5xx.
  - `tests/load/checkout.js`: 200 VU (`ramping-vus`), 80% đọc (`/products`, `/products/{id}`, `/categories`, `/sites`, `/vouchers`, `/orders`) + 20% tạo đơn COD (`POST /api/v1/checkout`, `Idempotency-Key`), phân bổ trên `BUYER_POOL_SIZE` buyer và `SKU_POOL_SIZE` SKU; ngưỡng `read_duration p(95)<300`, `checkout_duration p(95)<1000`, `server_errors_5xx count==0`, `checkout_created count>0`.
  - `tests/load/flash-sale.js`: 1.000 yêu cầu đồng thời tranh 100 suất (`shared-iterations`), hỗ trợ cả `MODE=grpc` (`PromotionService/ReservePromotion` với 1.000 JWT riêng, kiểm chứng Lua script Redis không bị rate-limit IP che khuất) và `MODE=http` (`POST /api/v1/checkout`); `teardown()` xác minh `remaining_slots == 0`, `oversell == 0` và kết thúc cửa sổ flash sale tạm.
  - `tests/load/search.js`: truy vấn tiếng Việt có dấu & không dấu (`áo thun`/`ao thun`, `quần jogger`/`quan jogger`, `cotton`, `áo polo`/`ao polo`, `quần short`/`quan short`), lọc giá, sắp xếp, `shop_id`, `/api/v1/search/suggest`; ngưỡng `search_duration p(95)<200`, `suggest_duration p(95)<200`; hỗ trợ `-e FALLBACK_PRODUCTS=true` khi `/api/v1/search` chưa được dựng trên compose.

### 2.4 Kiểm thử trình duyệt end-to-end Playwright (`tests/playwright/`)
- Gói độc lập tại `tests/playwright/` (`@playwright/test`, `typescript`, cài bằng `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`, tái sử dụng Chromium sẵn có tại `/home/dev/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome` hoặc `/usr/bin/google-chrome`, `--host-resolver-rules=MAP *.nexusmart.localhost 127.0.0.1`).
- `tests/playwright/specs/buyer-flow.spec.ts` chạy đầy đủ 5 bước luồng mua chính trên site host `http://bach-dang.nexusmart.localhost:3300`:
  1. Đăng ký tài khoản mới (`/dang-ky`), lấy OTP 6 chữ số từ Mailpit (`http://127.0.0.1:8025`), xác thực OTP và xác nhận phiên đăng nhập trên site host.
  2. Tìm kiếm sản phẩm (`"cotton"`) trên `/tim-kiem`, mở trang chi tiết sản phẩm (`/san-pham/...`).
  3. Chọn biến thể còn hàng, thêm vào giỏ hàng, xác minh sản phẩm và tạm tính tại `/gio-hang`.
  4. Chuyển sang `/thanh-toan`, thêm địa chỉ giao hàng 2 cấp, chọn **COD**, đợi báo giá vận chuyển, bấm đặt hàng.
  5. Xác minh đơn hàng COD mới tạo hiển thị đầy đủ trên `/don-hang/<id>` và trong danh sách `/don-hang`.

### 2.5 Sao lưu & diễn tập khôi phục (`tools/backup/`)
- `tools/backup/backup.sh`: tự động phát hiện mọi database PostgreSQL không phải template/`postgres` (hiện gồm 7 DB, tự bao phủ thêm `search_db`, `chat_db`, `assistant_db` khi xuất hiện); dùng MVCC snapshot (`pg_export_snapshot`) để `pg_dump --format=custom --compress=6` và truy vấn đếm dòng chính xác (`SELECT count(*)`) vào `manifest.tsv` không bị lệch dưới tải sống; tạo `globals.sql`, `SHA256SUMS`, xoay vòng theo `RETENTION_DAYS=7`.
- `tools/backup/restore-drill.sh`: kiểm tra `SHA256SUMS`, khởi tạo container Postgres cô lập `f-test-restore-pg` (`postgres:16-alpine`, luôn xoá qua `trap EXIT`), khôi phục toàn bộ DB, so sánh chính xác số dòng từng bảng (`c.relkind = 'r'`) với `manifest.tsv`, in bảng tổng hợp và trả exit `0` khi khớp 100%.
- `tools/backup/daily-backup.cron` & `tools/backup/k8s-cronjob.yaml` (`batch/v1`, non-root UID 70, `readOnlyRootFilesystem: true`, thông tin đăng nhập qua `secretKeyRef`, 0 secret cứng).

---

## 3. Lệnh kiểm tra và đầu ra thật

### 3.1 Helm lint, kubeconform & cụm `kind` staging thật
- **Lệnh:** `make helm-lint` (gọi `./deploy/k8s/scripts/lint.sh`)
- **Kết quả:** Exit `0`
  - `bash -n` trên `create-secrets.sh`, `lint.sh`, `rollback.sh`: `OK`
  - Hygiene scan `deploy/helm/` & `deploy/k8s/`: `0 committed kind: Secret manifests, 0 .tgz archives`
  - `kubeconform -strict` trên `deploy/k8s/namespaces.yaml`: `Valid: 2, Invalid: 0, Errors: 0`
  - 14 chart × 2 môi trường (`staging`, `production`): `PASS: 28 chart/environment combinations linted & kubeconform-validated cleanly.`
- **Diễn tập trên cụm `kind` thật (`nexusmart-f-test`, đã xoá sạch sau khi xong):**
  - `kind create cluster --name nexusmart-f-test --config deploy/k8s/kind-config.yaml` → Ready sau `47s`.
  - `kubectl apply -f deploy/k8s/namespaces.yaml` → `nexusmart-staging`, `nexusmart-production` Active.
  - Negative test `./deploy/k8s/create-secrets.sh nexusmart-production` → fail-closed (`ERROR: POSTGRES_PASSWORD must be explicitly provided via environment for nexusmart-production`).
  - `./deploy/k8s/create-secrets.sh nexusmart-staging` → tạo đủ 13 per-service `Opaque` secrets.
  - Cài thử `gateway` & `web` (`--wait` với image local `nexus_ecom-gateway:latest`, `nexus_ecom-web:latest` + redis tạm) và `order`, `search`:
    - `deployment/gateway 1/1 Running` (UID `10001`, `/healthz` + `/readyz` xanh), `deployment/web 1/1 Running` (UID `1000`, `GET /` xanh).
    - HPA hoạt động cho `gateway`, `order`, `search` (`autoscaling/v2`).
  - Rolling upgrade `gateway` lên Rev 2 (`config.LOG_LEVEL=debug`) → `STATUS: deployed, REVISION: 2`.
  - Rollback một lệnh `SERVICE=gateway REV=1 NAMESPACE=nexusmart-staging ./deploy/k8s/scripts/rollback.sh` → `Rollback was a success!`, `deployment "gateway" successfully rolled out`, `helm history` Rev 3 (`Rollback to 1`), `LOG_LEVEL_AFTER_ROLLBACK=info`.
  - Negative test `make rollback SERVICE=invalid` → `ERROR: unknown service 'invalid'` (exit 1).
  - Dọn dẹp: `kind delete cluster --name nexusmart-f-test` → `kind get clusters` trả về `No kind clusters found.`

### 3.2 Xác thực CI workflow & bảo mật
- **Lệnh:** Kiểm tra YAML và policy `workflow_dispatch`-only bằng Python `PyYAML`:
  ```text
  OK .github/workflows/ci.yml: triggers=['workflow_dispatch'] jobs=['proto-lint', 'go-lint-and-test', 'rust-lint-and-test', 'web', 'helm-lint', 'docker-build', 'docker-build-web']
  OK .github/workflows/security.yml: triggers=['workflow_dispatch'] jobs=['go-vulncheck', 'rust-audit', 'npm-audit', 'helm-and-k8s-lint', 'trivy-iac-scan', 'trivy-image-scan']
  ```
- **Kiểm tra `npm audit` cục bộ (read-only):**
  - `cd tests/playwright && npm install` (`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`): `added 4 packages, found 0 vulnerabilities`.
  - `cd web && npm audit --omit=dev --audit-level=high`: phát hiện `2 vulnerabilities (1 moderate, 1 high)` (`postcss <=8.5.22` qua `next@^15.5.0`) → đã ghi `PLT-02` vào `docs/reports/issues.md`.

### 3.3 Kiểm thử tải k6 (chạy thật dưới khóa `.runs/stack.lock`)
- **Kiểm tra cú pháp (`k6 v2.4.0`):**
  - `k6 inspect tests/load/checkout.js` → exit `0`
  - `k6 inspect tests/load/flash-sale.js` (`MODE=grpc` và `MODE=http`) → exit `0`
  - `k6 inspect tests/load/search.js` → exit `0`
- **Chạy thật 1 — Flash Sale (`MODE=grpc`, 1.000 yêu cầu đồng thời / 100 suất, 200 VU):** **ĐẠT (exit 0)**
  ```text
  [flash-sale setup] mode=grpc run=3a94d5b8 requests=1000 slots=100 vus=200 per_user_limit=1
  [flash-sale verify] item=2baa155f-728c-4ff5-864d-99e117abeb70 found=true total_slots=100 remaining_slots=0 server_sold=100 oversell=0
  === NexusMart Flash Sale Race Test (mode=grpc, 1000 reqs / 100 slots) ===
  Results : sold=100/100 | rejected=900 | oversell=0
  Errors  : 429_rate_limited=0 | unexpected_errors=0
  Latency : p50=108.00 ms | p95=4090.60 ms
  ```
- **Chạy thật 2 — Checkout hỗn hợp (`VUS=200`, `DURATION=30s`, `BUYER_POOL_SIZE=30`, `SKU_POOL_SIZE=20`, `SPOOF_XFF=true` qua `gateway-bff:8080`):** **CHƯA ĐẠT ngưỡng p95 trên single-replica compose (exit 99)**
  ```text
  [checkout setup] run=c0b93c24 vus=200 duration=30s buyers=30 skus=20 stock_per_sku=2500 carrier=ghn/STANDARD
  === NexusMart Checkout Load Test (200 VUs, 30s) ===
  Read API     : count=4264 | p50=294.84 ms | p95=1914.33 ms (target < 300 ms)
  Checkout API : orders=1001 | p50=3471.47 ms | p95=7945.86 ms (target < 1000 ms)
  Reliability  : 429_rate_limited=0 | 5xx_server_errors=12 | 4xx_client_errors=0
  ```
  - *Tham chiếu đọc thuần 200 VU* (lượt chạy trước khi chưa tạo đơn nặng đồng thời): `Read API count=13884 | p50=2.71 ms | p95=169.69 ms (< 300 ms)`. Khi chạy đồng thời ~33 đơn/giây qua saga trên 1 container Postgres + 1 replica Go (và máy chủ có 8 swarm chạy song song), p95 tăng vọt và phát sinh `12/5.277` (`0,23%`) lỗi 5xx (`STOCK_VERSION_CONFLICT` / `context canceled`). Đã ghi nhận `PLT-07` vào `docs/reports/issues.md`.
- **Chạy thật 3 — Tìm kiếm (`VUS=50`, `DURATION=20s`, `SPOOF_XFF=true`):**
  - Probe sống `GET /api/v1/search?q=cotton` → `HTTP 404` (Swarm A chưa được Swarm H rebuild vào stack compose).
  - Chạy chế độ dự phòng `-e FALLBACK_PRODUCTS=true` (`/api/v1/products?keyword=...`): **ĐẠT (exit 0)**
  ```text
  === NexusMart Search Load Test (50 VUs, 20s) ===
  Search API  : count=9986 | p50=2.84 ms | p95=43.41 ms (target < 200 ms)
  Suggest API : count=0 | p50=0.00 ms | p95=0.00 ms (target < 200 ms)
  Reliability : 429_rate_limited=0 | 5xx_server_errors=0 | 4xx_client_errors=0
  ```

### 3.4 Kiểm thử Playwright thật (chạy dưới khóa `.runs/stack.lock`)
- **Kiểm tra offline:** `cd tests/playwright && npx tsc --noEmit && npx playwright test --list` → exit `0` (`Total: 1 test in 1 file`).
- **Chạy thật trên trình duyệt Chromium đối với `http://bach-dang.nexusmart.localhost:3300`:** **ĐẠT (exit 0)**
  ```text
  Running 1 test using 1 worker
    ✓  1 [chromium] › specs/buyer-flow.spec.ts:31:3 › Storefront Buyer E2E Journey › register with Mailpit OTP, search product, add to cart, checkout COD, and view order (23.6s)
  1 passed (35.0s)
  ```
- *Lỗi thực tế phát hiện trong quá trình chạy:* SKU seed `BD-AT01-TRANG-S` thuộc `NexusMart Flash Sale 12:00` thiếu bản ghi trong `product_db.flash_sale_stock_reservations`, khiến checkout trên sản phẩm đó trả `409 FLASH_SALE_EXHAUSTED` → đã ghi `PLT-01` vào `docs/reports/issues.md`.

### 3.5 Sao lưu & diễn tập khôi phục thật (`tools/backup/`)
- **Kiểm tra cú pháp & manifest:**
  - `bash -n tools/backup/backup.sh tools/backup/restore-drill.sh` → `OK` (exit 0).
  - `~/.local/bin/kubeconform -strict -summary tools/backup/k8s-cronjob.yaml` → `Valid: 1, Invalid: 0, Errors: 0`.
- **Chạy thật (`BACKUP_DIR=/tmp/nexusmart-backup-drill-fresh ./tools/backup/restore-drill.sh --fresh` trên `nexus_ecom-postgres-1`, chỉ đọc):** **ĐẠT (exit 0)**
  - Snapshot `20261121T080036Z` (`4.3 MB`, 1s), `sha256sum -c --strict SHA256SUMS`: 9/9 file `OK`.
  - Khôi phục vào container cô lập `f-test-restore-pg` (`postgres:16-alpine`, thời gian khôi phục thuần `1.78s`, tổng diễn tập `14.18s`):
  ```text
  DATABASE             TABLES  EXPECTED_ROWS  RESTORED_ROWS  RESTORE_S   STATUS
  --------------------------------------------------------------------------------
  notification_db           6          14484          14484      0.12s   PASS
  order_db                  9          23954          23954      0.27s   PASS
  payment_db               14          13337          13337      0.19s   PASS
  product_db                7           6415           6415      0.14s   PASS
  promotion_db              8           4499           4499      0.88s   PASS
  shipping_db               9            537            537      0.08s   PASS
  user_db                   9           2874           2874      0.10s   PASS
  --------------------------------------------------------------------------------
  TOTAL (7 DBs)            62          66100          66100      1.78s   PASS
  ```
- **Kiểm thử âm tính:** Phát hiện sửa đổi checksum (`FAILED` trước khi dựng container), phát hiện sai lệch đếm dòng (`FAIL(rowcount)`), kiểm tra retention prune (`pruned=1`), container tạm & thư mục tạm đã xoá sạch.

### 3.6 Audit toàn bộ 15 Dockerfile trong repo
- **Đạt chuẩn multi-stage:** `14/15` (chỉ `deploy/observability/telegram-relay/Dockerfile` là single-stage — đã ghi `PLT-05`).
- **Đạt chuẩn non-root `USER`:** `15/15` (`0` Dockerfile chạy root).
- **Khuyến nghị/lỗi đã ghi vào `docs/reports/issues.md`:** `PLT-03` (`services/order/Dockerfile` hardcode `GOARCH=amd64` + `chown` binary), `PLT-04` (13/15 dùng tên user thay vì UID số), `PLT-05` (`telegram-relay` single-stage), `PLT-06` (thiếu `.dockerignore` ở root & Go 1.22 EOL).

---

## 4. Bảng đối chiếu tiêu chí đặc tả (`docs/spec.md`)

| Tiêu chí spec | Trạng thái | Ghi chú / Bằng chứng |
|---|---|---|
| Helm chart cho từng service, tách cấu hình khỏi image, `staging` & `production` | **Đạt** | 14 chart, 28/28 `helm lint --strict` + `kubeconform -strict` xanh |
| Secret trong K8s Secret, không nằm trong repo | **Đạt** | `0` manifest `kind: Secret` trong repo; `create-secrets.sh` fail-closed cho prod |
| Rolling update + readiness/liveness probe | **Đạt** | `maxUnavailable: 0`, `maxSurge: 1`; chạy thật trên `kind` (`gateway` & `web` Ready 1/1) |
| HPA cho `gateway`, `order`, `search` | **Đạt** | `autoscaling/v2` bật mặc định và đã xác nhận tạo trên cụm `kind` |
| Rollback bằng một lệnh (`make rollback SERVICE=… REV=…`) | **Đạt** | Đã diễn tập thật Rev 2 → Rev 1 trên `kind` (`gateway`) |
| CI quét lỗ hổng dependency & image (`govulncheck`, `cargo audit`, `npm audit`, Trivy), lint Helm, chỉ `workflow_dispatch` | **Đạt** | `.github/workflows/{ci,security}.yml` xác thực YAML + `workflow_dispatch`-only |
| Kiểm thử tải Flash Sale: 1.000 yêu cầu / 100 suất, bán đúng 100 | **Đạt** | `sold=100/100`, `rejected=900`, `oversell=0`, `remaining_slots=0` |
| Kiểm thử tải Checkout: 200 VU, p95 đọc `< 300 ms`, tạo đơn `< 1 s` | **Chưa đạt (hạ tầng compose đơn lẻ)** | Kịch bản k6 đầy đủ & chạy thật `1.001` đơn COD, nhưng trên 1 replica compose (chịu tải song song từ 8 swarm) đo được đọc `p95=1.914 ms`, tạo đơn `p95=7.946 ms`, `5xx=12` (`PLT-07`). Đọc thuần 200 VU đạt `p95=169,69 ms < 300 ms`. |
| Kiểm thử tải Tìm kiếm: p95 `< 200 ms` trên `/api/v1/search` | **Đạt một phần (chờ Swarm H rebuild A)** | Kịch bản `tests/load/search.js` sẵn sàng; `/api/v1/search` hiện trả `404` trên stack sống nên đo fallback `/api/v1/products` đạt `p95=43,41 ms < 200 ms`. Cần chạy lại không fallback sau khi Swarm H rebuild service `search`. |
| End-to-end Playwright luồng mua chính trên site host | **Đạt** | `1 passed (35.0s)` trên `bach-dang.nexusmart.localhost:3300` |
| Sao lưu hằng ngày mọi DB & diễn tập khôi phục đối chiếu đếm dòng | **Đạt** | `7 DB`, `62 bảng`, `66.100/66.100` dòng khớp tuyệt đối, `1.78s` restore |
| Audit mọi Dockerfile: multi-stage, user non-root | **Đạt (đã ghi lỗi vào `issues.md`)** | 15/15 non-root, 14/15 multi-stage; lỗi chi tiết tại `PLT-03`..`PLT-06` |

---

## 5. Phần chưa làm / phụ thuộc ngoài phạm vi Swarm F

1. **Đo k6 thật trên `GET /api/v1/search` & `/api/v1/search/suggest`:** Swarm F không được rebuild compose stack (quyền của Swarm H), nên hiện `gateway` sống trả `404` cho `/api/v1/search`. Sau khi Swarm H rebuild `search` + `gateway` và nạp 20.000 sản phẩm, chạy lại `tests/load/search.js` (không bật `FALLBACK_PRODUCTS`) để chốt số p95 chính thức cho `docs/EVAL_REPORT.md`.
2. **Đạt p95 `< 300 ms` (đọc) và `< 1.000 ms` (tạo đơn) ở 200 VU checkout:** Cần chạy trên cụm nhiều replica (HPA `gateway`/`order`) hoặc tách tải CPU khỏi các swarm đang biên dịch song song, đồng thời xử lý tranh chấp optimistic lock / pool Postgres (`PLT-07`).
3. **Chạy cục bộ `govulncheck`, `cargo audit`, `trivy`:** Ba công cụ này không có sẵn trên máy host và được cấu hình chạy tự động khi kích hoạt tay `.github/workflows/security.yml` (`gh workflow run security.yml`).
