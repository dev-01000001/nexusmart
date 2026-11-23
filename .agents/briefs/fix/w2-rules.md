# Đợt 2 – luật chung cho mọi coder agy

Đọc trước khi code: `AGENTS.md`, `.agents/briefs/fix/w2-plan.md` (bắt buộc theo đúng), `plan.md` §2 (mô hình tiền), `docs/contracts.md`, brief riêng `w2a-<tên>.md`. Chi tiết từng mã lỗi (tái hiện, file:dòng): `docs/audit/*.md`.

## Chỗ làm việc
- Repo `/home/tai/Document/nexusmart`, nhánh `main`. Không git write (commit, stash, checkout, reset, clean, add, rebase): manager commit.
- Nhiều agent làm song song trên cùng cây file. CHỈ sửa file trong phần "Sở hữu" của brief. Cần đổi file của người khác: ghi "Nhờ agent khác" trong báo cáo cuối (file, lý do, đề xuất cụ thể).
- `docs/contracts.md`, `proto/`, `gen/go`, `pkg/common/`, `docker-compose.yml`, `.env.example`: chỉ agent `contract` sửa. Pha song song cần đổi thì ghi "Cập nhật contracts" trong báo cáo.
- Không sửa tay `gen/go` và file sqlc sinh; đổi `queries/*.sql` hoặc migration thì `cd services/<svc> && sqlc generate`.
- Migration mới, forward-only, không sửa file cũ: user `0004_`, product `000005_`, order `0005_`, payment `0003_` (sqlx kiểm checksum), shipping `000002_`, notification `000003_`, promotion `003_`. `shipping`/`notification`/`promotion` chạy lại toàn bộ SQL mỗi lần khởi động nên phải idempotent. Migration phải chạy được trên DB audit đang có dữ liệu.
- Không thêm dependency, không thêm hạ tầng (ADR chưa duyệt).

## Test trước, sửa sau
- Mỗi mục: viết test tái hiện/đặc tả hành vi (unit hoặc e2e), thấy ĐỎ, sửa, thấy XANH. Báo cáo dán tên test và dòng kết quả.
- E2E mới chỉ thêm file mới trong `tests/e2e/`: order `19_`, payment `20_`, shipping `21_`, user `22_`, gateway `23_`, notification `24_`. Helper trong file của mình, tiền tố tên riêng (`ordW2...`). Không sửa `client.go`, `config.go`, `fixtures_test.go`, `types.go`, `mailpit.go` và test cũ (agent `qa` sửa); test cũ đổi kỳ vọng thì liệt kê trong báo cáo.
- `tests/e2e-web/`: chỉ agent `web` thêm file mới `06_returns.test.mjs`; file cũ do `qa`.
- Logic theo thời gian (job, hạn): unit test với đồng hồ giả; e2e không chờ thật.

## Stack để kiểm (project compose `nexusmart-audit`, cổng trong `.env` gốc)
- Gateway `http://localhost:48080`, web `http://localhost:43000`, Mailpit `http://localhost:48025`, Postgres `docker compose exec -T postgres psql -U nexusmart -d <db>`.
- Chỉ rebuild service mình sở hữu sau khi unit test xanh: `docker compose up -d --build <service>` ở gốc repo. Không `make up|down|seed`, không `docker compose down|restart|stop|rm` toàn bộ. Không đụng stack `nexusmart`, `nexusmart-web`, cổng 3111, 4777.
- Chạy một test e2e: `GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e go test -v -count=1 -run '<TestTên>' ./...`. Phần của agent khác chưa xong làm đỏ e2e xuyên service thì ghi rõ, không sửa hộ.
- Khi nhiều agent chạy cùng lúc máy rất tải: test có timeout có thể đỏ giả, chạy lại riêng trước khi kết luận.

## Chất lượng
- Ponytail: diff nhỏ nhất đúng gốc, dùng lại code có sẵn (`pkg/common`, helper sẵn trong service), không abstraction một cài đặt. Cắt góc có trần thì ghi `ponytail:`.
- Tiền `int64`/`i64` VND, bút toán luôn cân. Tác vụ phụ sau commit phải idempotent và có đường thử lại (outbox hoặc watchdog), không nuốt lỗi.
- Race: khoá dòng và kiểm trạng thái trong cùng transaction; job quét dùng `FOR UPDATE SKIP LOCKED` hoặc khoá tương đương để chạy nhiều bản.
- Kiểm quyền theo đối tượng ở service (không chỉ gateway); `shop_id`, `user_id` lấy từ claims.
- Không in secret, token, chữ ký, số tài khoản đầy đủ vào log, báo cáo hay output test.
- Trước khi báo xong, trong module mình: Go `gofmt -l` rỗng, `golangci-lint run -c "$PWD/../../.golangci.yml" ./...` sạch, `go test -race ./...` xanh; Rust `cargo fmt --all -- --check`, `cargo clippy --all-targets -- -D warnings`, `cargo test`; web `npm run lint`, `npm run format:check`, build.

## Báo cáo cuối
1. Từng mục brief: XONG / CHƯA XONG / BỎ (lý do).
2. File đã tạo/sửa.
3. Test mới và kết quả đỏ → xanh; lệnh kiểm đã chạy và dòng kết quả thật.
4. Nhờ agent khác; Cập nhật contracts; test cũ cần đổi kỳ vọng.
5. Quyết định đáng chú ý, giới hạn còn lại.
