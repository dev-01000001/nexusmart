# Swarm K – Sửa lỗi core, chat, assistant sau review (2026-11-21)
Hợp đồng: `m-all-contract.md`. Báo cáo trước: `docs/reports/b-chat.md`, `docs/reports/c-assistant.md`. Lỗi: `docs/reports/issues.md`.

## Sở hữu
`pkg/common/`, `infra/`, `services/notification/`, `services/cart/`, `services/chat/`, `services/assistant/`, `evals/assistant/`, `services/gateway/cmd/`, `services/gateway/internal/{proxy,middleware}/`, `services/gateway/internal/router/handlers_{chat,assistant}.go` và test của hai file đó, file `.dockerignore` ở gốc (mới), Dockerfile của gateway, cart, notification, chat, assistant.
CẤM: `services/user/` (T đang sửa `cmd/user/main.go`), `services/{order,payment,shipping,promotion}/` (D), `services/{search,product}/` (A), `services/aiops/`, `deploy/` (E, F), `web/`, `tests/`, các handler gateway khác.

## Việc
1. K-01 (Medium, assistant): `handlers_assistant.go` dùng `pr.SetXForwarded()` nên `X-Forwarded-For` gửi sang assistant là IP của peer trực tiếp (container BFF). Mọi khách chưa đăng nhập trên web chung một bucket quota `ip:<bff>` trong `services/assistant/internal/service/assistant.go:216` → một người xài hết quota trợ lý của mọi khách. Sửa: gateway đặt `X-Forwarded-For` bằng IP client đã giải theo `deps.TrustedProxies` (dùng helper sẵn có trong `internal/middleware`). Test: hai IP client khác nhau qua BFF tin cậy → hai subject khác nhau; header giả từ peer không tin cậy bị bỏ.
2. K-02 (Medium, chat): web mở WebSocket thẳng tới gateway (`web/src/lib/server/config.ts`) với `Origin` là host của site. `services/chat/internal/ws/handler.go:411` chỉ cho localhost và `*.SITE_ROOT_DOMAIN`, nên site dùng tên miền riêng (`tests/e2e/15_custom_domains_test.go`) không chat được. Ticket là thông tin đăng nhập một lần, không gửi tự động như cookie, nên đường ticket không cần kiểm tra Origin; đường cookie giữ kiểm tra Origin chặt. Chọn cách đơn giản nhất, có test cho cả hai đường.
3. OBS-01, OBS-03, OBS-07, OBS-08: sửa theo hướng ghi trong `issues.md`.
4. OBS-09 và OBS-13: chỉ phần gateway và cart (user để sau, ghi vào báo cáo).
5. PLT-04: Dockerfile trong phần sở hữu dùng UID số cố định `10001:10001`. PLT-06: thêm `.dockerignore` gốc (`.git`, `web/node_modules`, `web/.next*`, `services/payment/target`, `services/aiops/target`, `.runs`); không nâng Go toolchain lần này.

## Gate
Với mỗi module Go đã sửa: `gofmt -l` rỗng, `golangci-lint run --allow-parallel-runners -c <root>/.golangci.yml ./...`, `go test -race -count=1 ./...`. `docker compose config -q`. Build image của gateway, chat, assistant (tag `k-test-*`, xóa sau khi build). Không rebuild stack, không chạy `make e2e`.
Ghi kết quả vào `docs/reports/k-core-fix.md` (lệnh + đầu ra thật). Đánh dấu ID đã sửa trong `docs/reports/issues.md` ("đã sửa bởi K"), không xóa dòng.
