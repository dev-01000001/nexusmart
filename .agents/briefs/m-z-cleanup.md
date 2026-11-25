# Swarm Z – Dọn việc nhỏ sau review (2026-11-21)
Hợp đồng: `m-all-contract.md`. Lỗi: `docs/reports/issues.md`.

## Sở hữu
`deploy/k8s/`, `services/user/cmd/`, `web/src/features/assistant/assistant.test.mjs`, `web/src/features/seller/site/siteSummary.test.mjs`. CẤM mọi thư mục khác (A, E, H đang chạy).

## Việc
1. `deploy/k8s/create-secrets.sh`: payment production giờ từ chối khởi động nếu thiếu `ZALOPAY_KEY1`, `ZALOPAY_KEY2`, `PAYMENT_WEBHOOK_SECRET` hoặc khóa PayOS khi `VIETQR_MODE=payos` (xem `services/payment/src/config.rs` hàm `validate`, `deploy/helm/payment/values-production.yaml`). Script phải nạp các khóa này vào `nexusmart-payment-secret` và báo lỗi rõ khi thiếu ở môi trường production. Không đưa giá trị thật vào repo.
2. OBS-09 và OBS-13 phần `user` (`services/user/cmd/user/main.go`): đăng ký `ProcessCollector`/`GoCollector`, gắn RED/trace middleware cho `opsSrv`, lỗi fatal ghi JSON qua `logger.New`. Làm theo cách K đã làm cho gateway (`services/gateway/cmd/gateway/main.go`). Giữ dòng `BootstrapSiteTemplates` của T.
3. Chạy `npx prettier --write` cho hai file test web trong phần sở hữu.

## Gate
`bash -n deploy/k8s/create-secrets.sh` và `shellcheck` nếu có. Với `services/user`: `gofmt -l` rỗng, `golangci-lint run --allow-parallel-runners -c <root>/.golangci.yml ./...`, `go test -race -count=1 ./...`. `cd web && npx prettier --check src`. Ghi `docs/reports/z-cleanup.md` (lệnh + đầu ra thật). Đánh dấu OBS-09/OBS-13 phần user là "đã sửa bởi Z" trong `issues.md`.
