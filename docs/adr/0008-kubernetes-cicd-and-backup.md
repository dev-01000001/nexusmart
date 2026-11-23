# ADR-0008: Nơi triển khai Kubernetes, CI/CD và sao lưu cơ sở dữ liệu

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-23

## Bối cảnh

- **Vấn đề chặn đầu (`docs/adr/README.md:3`)**: Nơi triển khai (**Kubernetes cục bộ / tự host hay cloud**) là quyết định gốc ảnh hưởng trực tiếp tới edge gateway (`ADR-0001`), Object Storage (`ADR-0005`), quản lý Secret (`ADR-0006`) và observability/AIOps (`ADR-0007`).
- **Hiện trạng trong repo** (`docs/audit/plt.md` `UC-OPS05/06/09`, `PLT-B8`, `PLT-B9`; `.github/workflows/ci.yml`):
  - Chưa có thư mục `k8s/`, `charts/`, `helm/`, script rollback, hay backup/restore (`pg_dump`/WAL/PITR).
  - 10/10 container app (`gateway`, 8 backend, `web`) đã có Dockerfile đa giai đoạn build từ root repo và chạy non-root.
  - `.github/workflows/ci.yml` chạy tay qua `workflow_dispatch` (tuân thủ `AGENTS.md`: **không thêm trigger `push`/`pull_request`/`schedule`** để tiết kiệm quota), đã có `proto-lint`, `go-lint-and-test`, `rust-payment`, `web`, `docker-build` (nhưng thiếu `web/Dockerfile`, thiếu quét lỗ hổng dependency/image, thiếu đo coverage Rust, và coverage `order` 51.3% / `promotion` 51.9% < 60% `PLT-B8`).
- **Use case & tiêu chí đo trích từ `docs/spec.md`**:
  - `UC-OPS05` (Cao): Kubernetes có 2 môi trường **`staging` và `production`**; **Helm chart cho từng service**, cấu hình tách khỏi image; rolling update với **`readinessProbe` và `livenessProbe`**; **HPA** cho service chịu tải; pipeline lint, test, build image, **quét lỗ hổng image & dependency**, deploy staging + integration test, duyệt thủ công rồi deploy production + đánh dấu phát hành trên Grafana.
  - `UC-OPS06` (Trung bình): **rollback bằng một lệnh**, phát hiện error rate tăng sau deploy, chặn tự rollback khi bản mới có DB migration không tương thích ngược.
  - `UC-OPS09` (Trung bình): **sao lưu PostgreSQL hằng ngày + lưu WAL liên tục (PITR)**, mã hoá và đẩy lên object storage, kiểm tra bản sao lưu + cảnh báo khi lỗi, **diễn tập khôi phục** sang môi trường riêng và ghi lại thời gian khôi phục (RTO).
  - **Ngoài phạm vi**: Multi-cloud, tự động co giãn cụm (Cluster Autoscaler), GitOps đầy đủ (`docs/spec.md:37`).
- **Lưu ý bảo mật OSS (tra `2026-11-23`)**: `kubernetes/ingress-nginx` đã **ngừng bảo trì (EOL) và lưu trữ (`archived`) từ tháng 03/2026** (bản cuối `v1.15.1`, không còn vá CVE). Mọi triển khai Kubernetes mới **không dùng `ingress-nginx`**, thay bằng Kubernetes Gateway API (**Envoy Gateway `v1.10.0`** hoặc **Traefik `v3.7.19`** theo `ADR-0001`).

## Ứng viên

### Phần A — Nơi triển khai cụm Kubernetes (Demo cục bộ vs Self-host VPS vs Cloud Managed)

1. **Cục bộ bằng `k3d` hoặc `kind` (chạy trên máy dev/demo)**
   - **Phiên bản & bảo trì**:
     - `k3d-io/k3d` `v5.9.0` (**MIT**) bọc `k3s-io/k3s` `v1.37.2+k3s1` (**Apache-2.0**) — tích hợp sẵn Traefik + ServiceLB + local-path storage, nhẹ RAM (~30–40% nhẹ hơn `kind`).
     - `kubernetes-sigs/kind` `v0.33.0` (**Apache-2.0**) — chuẩn upstream Kubernetes, rất tốt cho CI test nhưng nặng RAM hơn và không có sẵn LB/Ingress.
   - **Khả năng đáp ứng**: Tạo đủ 2 namespace `nexusmart-staging` và `nexusmart-prod` (hoặc 2 cụm riêng), chạy đầy đủ Helm `v4.3.2`, HPA (`metrics-server`), rolling update, rollback, tiêm lỗi AIOps, backup sang SeaweedFS; **chi phí `$0`**.
   - **Giới hạn**: Cần máy ≥ 16 GB RAM (khuyến nghị 24–32 GB khi bật full 12 service + monitoring); không có IP public thật nên webhook GHN/payOS sandbox cần tunnel hoặc chạy ở chế độ mock.

2. **Tự host trên VPS Linux với `k3s`**
   - **Phiên bản & bảo trì**: `k3s-io/k3s` `v1.37.2+k3s1` (**Apache-2.0**, CNCF Sandbox, bảo trì rất tích cực).
   - **Vận hành & chi phí**: 1 VPS 8 vCPU / 16 GB RAM (chạy cả `staging` + `production` tách namespace) hoặc 2 VPS tách môi trường tốn khoảng **`$25–$60/tháng`** (Hetzner/Vultr/VPS VN). Có IP public + TLS thật (Let's Encrypt) để nhận webhook sandbox GHN/payOS và bắn Telegram thật; chạy mượt SeaweedFS + Prometheus/Grafana. Tự lo bảo mật OS, tường lửa và backup node.

3. **Managed Kubernetes trên Cloud (DigitalOcean DOKS hoặc GKE Autopilot)**
   - **Chi phí thực tế (tra `2026-11-23`)**:
     - **DOKS**: Control plane `$0` (HA control plane `+$40/tháng`), worker Droplet tối thiểu `$12/tháng`, nhưng đủ tải cho 12 service + DB + monitoring (~12–16 GB RAM) + Load Balancer (`$12/tháng`) + Block Storage + Spaces tốn khoảng **`$90–$160/tháng`**.
     - **GKE Autopilot**: Phí quản lý `$0.10/giờ` (~`$73/tháng`), được **credit miễn phí `$74.40/tháng`** cho cụm đầu tiên; chi phí compute tính theo pod request (vCPU/RAM) + LB/disk cho full stack khoảng **`$80–$150/tháng`** (có thể giảm mạnh bằng tắt namespace `staging` ngoài giờ demo, hoặc tận dụng gói credit sinh viên/dùng thử `$300`).
   - **Ưu & nhược**: Không phải quản trị control plane, có sẵn Managed Load Balancer và Managed S3/GCS, tính thuyết phục cao khi bảo vệ đồ án; nhưng tốn tiền thực tế hàng tháng và có nguy cơ phát sinh phí ẩn nếu quên tắt tài nguyên.

#### Bảng so sánh — Nơi triển khai Kubernetes

| Tiêu chí | Cục bộ (`k3d` / `kind`) | Self-host VPS (`k3s`) | Cloud Managed (DOKS / GKE Autopilot) |
| --- | --- | --- | --- |
| Độ khớp tiêu chí (staging + prod, Helm, HPA, rollback, chaos) | Tốt (đủ 100% tính năng K8s; webhook ngoài phải mock/tunnel) | **Rất tốt** (đủ K8s + IP/HTTPS public cho webhook thật) | **Xuất sắc** (chuẩn production, có sẵn LB/S3/Secret Manager) |
| Độ phức tạp vận hành | **Thấp nhất** (dựng/xoá 1 lệnh) | Trung bình (quản trị OS/SSH/firewall/TLS) | Thấp–trung bình (cấu hình IAM/VPC/Quota cloud) |
| Chi phí (2–3 tháng demo) | **$0** | **$50–$180 tổng** (~$25–60/tháng) | **$200–$450 tổng** (hoặc $0 nếu đủ credit sinh viên) |
| Giấy phép | `k3d` **MIT** / `kind` & `k3s` **Apache-2.0** | `k3s` **Apache-2.0** | Dịch vụ thương mại (manifest Helm/K8s chuẩn mở) |
| Mức độ trưởng thành | Rất cao (chuẩn dev/CI K8s) | Rất cao (chuẩn lightweight K8s) | Rất cao |

---

### Phần B — Công cụ sao lưu PostgreSQL & PITR (`UC-OPS09`)

1. **CloudNativePG (CNPG) + Barman Cloud Plugin**
   - **Phiên bản & bảo trì**: `cloudnative-pg/cloudnative-pg` `v1.30.1` (2026) — **Apache-2.0** (CNCF Incubating, bảo trì rất tích cực).
   - **Tính năng**: Operator Kubernetes quản lý vòng đời Postgres (hỗ trợ image `pgvector/pgvector:pg16`), cấu hình **base backup hằng ngày (`ScheduledBackup`) + lưu WAL liên tục (PITR)** đẩy lên S3/SeaweedFS có mã hoá AES-256, xuất sẵn Prometheus metrics báo lỗi backup về Alertmanager (`ADR-0007`), khôi phục PITR bằng khai báo YAML mới (rất tiện diễn tập sang namespace `restore-drill`).
2. **WAL-G + Kubernetes CronJob (`pg_dump`)**
   - **Phiên bản & bảo trì**: `wal-g/wal-g` `v3.0.10` (**Apache-2.0**), bảo trì tích cực.
   - **Tính năng**: Chạy trực tiếp trên Postgres StatefulSet thường mà không cần cài Operator; hỗ trợ base backup + archive WAL + mã hoá đẩy lên S3; nhẹ RAM hơn CNPG nhưng phải tự viết script kiểm tra trạng thái backup và xuất metric cảnh báo.
3. **pgBackRest**
   - **Phiên bản & bảo trì**: `pgbackrest/pgbackrest` `v2.60.1` (**MIT**), cực kỳ ổn định cho production.
   - **Tính năng**: Backup vật lý/differential/WAL + mã hoá AES-256 xuất sắc, nhưng tự đóng gói vào StatefulSet trên K8s tốn công hơn CNPG (thường đi kèm Crunchy Postgres Operator vốn nặng hơn CNPG).

#### Bảng so sánh — Sao lưu & PITR (`UC-OPS09`)

| Tiêu chí | CloudNativePG (`v1.30.1`) | WAL-G (`v3.0.10`) + StatefulSet | pgBackRest (`v2.60.1`) |
| --- | --- | --- | --- |
| Độ khớp tiêu chí (hằng ngày, WAL/PITR, mã hoá S3, cảnh báo, drill) | **Xuất sắc** (khai báo YAML, metric backup sẵn, PITR mượt) | Tốt (đủ tính năng, tự viết script metric & restore) | Rất tốt (mạnh nhất về backup vật lý, tự tích hợp K8s phức tạp hơn) |
| Độ phức tạp vận hành | Thấp trên K8s (cài 1 Helm chart Operator) | Trung bình (tự ghép image Postgres + `archive_command` + CronJob) | Cao nếu tự cấu hình không dùng Operator |
| Chi phí tài nguyên | ~100–150 MB RAM cho Operator | **Thấp nhất** (0 Operator) | Thấp (hoặc ~150 MB nếu kèm Operator) |
| Giấy phép | **Apache-2.0** | **Apache-2.0** | **MIT** |
| Mức độ trưởng thành | Rất cao (CNCF) | Cao | Rất cao |

## Quyết định

- **Đề xuất**:
  1. **Chiến lược Kubernetes 2 lớp không khoá nhà cung cấp (portable Helm)**:
     - **Lớp chuẩn cho dev, CI và demo cục bộ**: **`k3d v5.9.0`** (chạy `k3s v1.37.2+k3s1`, chi phí `$0`), bật `metrics-server` cho HPA, tách rõ 2 namespace **`nexusmart-staging`** và **`nexusmart-prod`** (hoặc 2 cụm `k3d` nếu máy đủ RAM), dùng **Envoy Gateway (`v1.10.0`)** hoặc **Traefik (`v3.7.19`)** — tuyệt đối không dùng `ingress-nginx`.
     - **Lớp môi trường online (khi chủ dự án chốt ngân sách)**: ưu tiên **VPS chạy `k3s`** (~$25–50/tháng) nếu tự túc chi phí, hoặc **GKE Autopilot / DOKS** nếu có credit cloud sinh viên — dùng **cùng 1 bộ Helm chart**, chỉ thay file `values-staging.yaml` / `values-production.yaml` (StorageClass, domain TLS, S3 endpoint).
  2. **Đóng gói Helm (`v4.3.2`, Apache-2.0)**:
     - Tạo 1 **library chart dùng chung** (`charts/nexusmart-common/`) định nghĩa mẫu `Deployment`, `Service`, `HPA`, `PodDisruptionBudget`, `ServiceMonitor`, `readinessProbe` (`/readyz`), `livenessProbe` (`/healthz`), `securityContext` (non-root, read-only rootfs).
     - Mỗi microservice có chart mỏng riêng tại `charts/services/<svc>/` (`gateway`, `user`, `product`, `cart`, `promotion`, `order`, `payment`, `shipping`, `notification`, `search`, `chat`, `ai-assistant`, `web`, `aiops`) kèm `values-staging.yaml` & `values-production.yaml` tách sạch cấu hình khỏi image.
     - **Rollback 1 lệnh**: `helm rollback <svc> -n nexusmart-prod` (kèm cờ `--atomic --timeout 5m` lúc deploy để tự quay về bản cũ khi probe hỏng, và nhãn annotation `migration-breaking=true` để pipeline chặn tự rollback khi có migration không tương thích ngược theo `UC-OPS06`).
  3. **CI/CD tuân thủ `AGENTS.md` (giữ `workflow_dispatch`)**:
     - Mở rộng `.github/workflows/ci.yml` (vẫn chỉ `on: workflow_dispatch`): thêm build `web/Dockerfile`, đo coverage Rust (`cargo-llvm-cov`) + cổng coverage ≥ 60% cho `order`/`payment`/`promotion` (`PLT-B8`), quét lỗ hổng dependency (`govulncheck`, `cargo audit`, `npm audit`) và image (**Trivy `v0.76.2`**, Apache-2.0, chặn CVE `CRITICAL`/`HIGH`).
     - Thêm `.github/workflows/deploy.yml` (chạy tay `workflow_dispatch`, đẩy image tag theo SHA lên GHCR, deploy `staging` → chạy smoke/integration test → **GitHub Environment manual approval** → deploy `production` → gửi Grafana deploy annotation cho `UC-OPS05/06`). Không dựng Argo CD vì GitOps đầy đủ nằm ngoài phạm vi.
  4. **Sao lưu & PITR (`UC-OPS09`)**: chọn **CloudNativePG (`v1.30.1`, Apache-2.0)** chạy image `pgvector/pgvector:pg16`, backup hằng ngày + archive WAL liên tục mã hoá AES-256 đẩy lên S3 (`SeaweedFS` ở `ADR-0005` hoặc Managed S3), cảnh báo lỗi backup qua Alertmanager/Telegram (`ADR-0007`), và script diễn tập khôi phục PITR định kỳ sang namespace `restore-drill` ghi nhận RTO.
  5. **Quản lý Secret**: không commit secret dạng rõ; dùng **SOPS `v3.13.3` + `age`** khi chạy `k3d`/VPS `k3s` (hoặc **External Secrets Operator `v2.15.0`** nếu lên cloud).
- **Điều kiện kích hoạt chuyển đổi**:
  - Bổ sung **Argo CD** (GitOps) chỉ khi đội vận hành tăng quy mô cần tự động đồng bộ nhiều cụm production song song.
  - Chuyển từ `k3s` 1 node sang **Managed K8s multi-AZ (DOKS/GKE/EKS)** khi triển khai thương mại thật cần SLA HA đa vùng sẵn sàng.
- **Phụ thuộc**: `ADR-0001` (edge Envoy Gateway/Traefik), `ADR-0005` (Object Storage S3 nhận bản sao lưu WAL), `ADR-0006` (Secret cho LLM API key), `ADR-0007` (Grafana deploy annotation & cảnh báo lỗi backup).
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Chiến lược `k3d` cục bộ + Helm portable + CNPG + `workflow_dispatch` (đề xuất)**:
  - *Tích cực*: kiểm chứng 100% tính năng K8s/Helm/HPA/rollback/CNPG PITR với chi phí `$0` trên local mà không bị khoá cloud; tiết kiệm tối đa quota GitHub Actions theo `AGENTS.md`; sẵn sàng đẩy lên VPS `k3s` hoặc GKE/DOKS bất cứ lúc nào chỉ bằng đổi `values-*.yaml`.
  - *Tiêu cực*: trên `k3d` cục bộ cần cấu hình máy khá (≥ 16 GB RAM); CNPG quản lý 1 cụm Postgres chứa nhiều DB (`user_db`…`aiops_db`) — vẫn giữ nguyên cách ly quyền theo user/DB riêng như `infra/postgres/init-dbs.sql` để không nhân lên 11 cụm Postgres riêng gây quá tải RAM đồ án; triển khai `staging`/`production` cần thao tác bấm tay (`workflow_dispatch`) hoặc script local thay vì auto-deploy khi merge.

## Kế hoạch triển khai

| Task | Phạm vi | Tiêu chí nghiệm thu đo được |
| --- | --- | --- |
| `T1` Hoàn thiện CI & quét bảo mật (`UC-OPS05`, `PLT-B8/B9`) | Cập nhật `.github/workflows/ci.yml` (giữ `workflow_dispatch`): thêm build `web/Dockerfile`, `govulncheck`, `cargo audit`, `npm audit`, `Trivy v0.76.2`, `cargo-llvm-cov` và cổng coverage ≥ 60% cho `order`/`payment`/`promotion` | Chạy `gh workflow run ci.yml` xanh toàn bộ job; cố tình đưa dependency có CVE `HIGH` làm job security fail |
| `T2` Cụm `k3d` local & Gateway API | Viết script `tools/k8s/cluster-up.sh` dựng cụm `k3d` + `metrics-server` + **Envoy Gateway** (hoặc Traefik) + namespace `nexusmart-staging`, `nexusmart-prod`, `observability` + SOPS/Secret mẫu | 1 lệnh dựng cụm sạch sẵn sàng trong **< 3 phút**; không có bất kỳ tham chiếu nào tới `ingress-nginx` |
| `T3` Helm library + chart từng service + HPA + Rollback (`UC-OPS05/06`) | Viết `charts/nexusmart-common/` và `charts/services/<svc>/` với `readinessProbe`, `livenessProbe`, `HPA` (CPU 70%), `values-{staging,prod}.yaml`; thêm `.github/workflows/deploy.yml` (`workflow_dispatch` + duyệt tay prod + Grafana annotation) | Deploy đủ stack lên `staging` & `prod` không downtime khi rolling update; `k6` kích hoạt HPA scale tăng replica; **`helm rollback` khôi phục bản cũ trong 1 lệnh (< 60 giây)** |
| `T4` CloudNativePG + Backup WAL/PITR + Diễn tập (`UC-OPS09`) | Cài CNPG `v1.30.1` với image `pgvector/pgvector:pg16`, cấu hình `ScheduledBackup` hằng ngày + continuous WAL mã hoá sang S3/SeaweedFS, alert khi backup lỗi; viết script `tools/k8s/restore-drill.sh` khôi phục PITR sang namespace `restore-drill` | Có bản base backup + WAL hợp lệ trên S3; chạy diễn tập khôi phục PITR về đúng mốc thời điểm trước khi xoá thử 1 bản ghi, xác minh dữ liệu toàn vẹn và ghi nhận **RTO thực tế** |

## Câu hỏi chủ dự án cần trả lời

1. **Khi bảo vệ/demo đồ án, hệ thống Kubernetes sẽ chạy ở đâu**: (a) chỉ chạy **cục bộ trên `k3d`** (`$0`, webhook dùng mock/tunnel), (b) thuê **1–2 VPS cài `k3s`** (~$25–50/tháng, có HTTPS/webhook thật), hay (c) dùng **GKE Autopilot / DOKS** (nếu có sẵn credit cloud sinh viên)?
2. **Cách bố trí PostgreSQL trên Kubernetes**: gom các DB tách biệt (`user_db`, `product_db`… `aiops_db`, mỗi service 1 role chỉ đọc/ghi DB của mình) trên **1 cụm CloudNativePG/mỗi môi trường** để tiết kiệm RAM, hay tách mỗi service 1 cụm CNPG riêng (tốn gấp ~8–10× RAM)?
3. **Edge Gateway trên Kubernetes (`ADR-0001` + `ADR-0008`)**: dùng **Envoy Gateway** (chuẩn Gateway API CNCF) hay dùng **Traefik** tích hợp sẵn trong `k3s`/`k3d` để giảm thêm 1 thành phần?

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-23
- **Nguồn**:
  - `kind`, `k3d`, `k3s`, Helm: https://github.com/kubernetes-sigs/kind, https://github.com/k3d-io/k3d, https://github.com/k3s-io/k3s, https://github.com/helm/helm
  - CloudNativePG, pgBackRest, WAL-G: https://github.com/cloudnative-pg/cloudnative-pg, https://github.com/pgbackrest/pgbackrest, https://github.com/wal-g/wal-g
  - Trivy & `ingress-nginx` EOL: https://github.com/aquasecurity/trivy, https://github.com/kubernetes/ingress-nginx
  - DOKS & GKE Autopilot pricing: https://www.digitalocean.com/pricing/kubernetes, https://cloud.google.com/kubernetes-engine/pricing
  - SOPS & External Secrets: https://github.com/getsops/sops, https://github.com/external-secrets/external-secrets
