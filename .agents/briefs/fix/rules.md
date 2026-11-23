# Đợt sửa 1 – luật chung cho mọi coder agy

Kế hoạch và mô hình tiền: `.agents/briefs/fix/plan.md` (đọc mục 2 và 3 trước khi code). Brief riêng: `.agents/briefs/fix/w1-<tên>.md`. Báo cáo rà soát gốc: `docs/audit/*.md` (mỗi mã lỗi có tái hiện chi tiết). Sổ kiểm chứng của manager: `.agents/briefs/fix/verify-log.md`.

## Chỗ làm việc
- Repo `/home/tai/Document/nexusmart`, nhánh `fix/business-audit`. Không git write (commit, stash, checkout, reset, clean, add, rebase): manager commit sau khi review.
- Nhiều agent làm song song trên cùng cây file. CHỈ sửa file trong phần "Sở hữu" của brief mình. Cần đổi file của người khác: ghi vào mục "Nhờ agent khác" của báo cáo cuối (file, lý do, đề xuất cụ thể), không tự sửa.
- `docs/contracts.md`, `proto/`, `gen/go`, `pkg/common/auth/auth.go`: chỉ agent `contract` (pha 1A) sửa. Ở pha 1B cần đổi hợp đồng thì ghi vào báo cáo mục "Cập nhật contracts".
- Không sửa tay `gen/go` và file sqlc sinh; đổi `queries/*.sql` hoặc migration thì chạy `cd services/<svc> && sqlc generate`.
- Migration mới (forward-only, không sửa file cũ), số kế tiếp: user `0003_`, product `000004_`, order `0003_`, payment `0002_` (sqlx kiểm checksum), shipping `000002_`, notification `000002_`, promotion `002_` (SQL chạy lại mỗi lần khởi động, phải idempotent).

## Test trước, sửa sau
- Mỗi lỗi trong brief: viết test tái hiện (unit test trong module, hoặc e2e), chạy thấy ĐỎ, rồi sửa, chạy thấy XANH. Báo cáo dán tên test và dòng kết quả đỏ → xanh.
- Test e2e mới: chỉ thêm file mới trong `tests/e2e/` theo số của mình: order `12_`, payment `13_`, promotion `14_`, user `15_`, gateway `16_`, shipping `17_`, product `18_`. Không sửa `client.go`, `config.go`, `fixtures_test.go`, `types.go`, `mailpit.go` và test cũ; cần helper thì viết trong file của mình với tiền tố tên riêng (ví dụ `ordFix...`). Test cũ đổi kỳ vọng vì mô hình tiền mới (ví dụ `08_ledger_test.go`): KHÔNG sửa, ghi vào báo cáo để manager quyết.
- `tests/e2e-web/`: chỉ agent `web`.

## Stack để kiểm (project compose `nexusmart-audit`, cổng trong `.env` gốc repo)
- Gateway `http://localhost:48080`, web `http://localhost:43000`, Mailpit `http://localhost:48025`, Postgres `docker compose exec -T postgres psql -U nexusmart -d <db>`.
- Chỉ rebuild service mình sở hữu, sau khi unit test xanh: `docker compose up -d --build <service>` (chạy ở gốc repo). Không `make up|down|seed`, không `docker compose down|restart|stop|rm` toàn bộ, không đụng stack `nexusmart`, `nexusmart-web`, cổng 3111, 4777.
- Chạy một test e2e: `GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e go test -v -count=1 -run '<TestTên>' ./...`. Agent khác đang sửa service khác cùng lúc, nên test e2e xuyên service có thể đỏ vì phần của người khác chưa xong: ghi rõ trong báo cáo, không sửa hộ.
- Rate limit gateway 300 request/phút/IP dùng chung; e2e chạy trong mạng compose nên không bị chung giới hạn với host.

## Chất lượng
- Ponytail: diff nhỏ nhất đúng gốc, dùng lại code có sẵn (`pkg/common`), không thêm dependency, không abstraction một cài đặt.
- Tiền: `int64` VND. Bút toán luôn cân (tổng nợ = tổng có mỗi giao dịch).
- Trước khi báo xong, trong module mình: Go `gofmt -l` rỗng, `golangci-lint run -c "$PWD/../../.golangci.yml" ./...` (chạy trong thư mục module) sạch, `go test -race ./...` xanh; Rust `cargo fmt --all -- --check`, `cargo clippy --all-targets -- -D warnings`, `cargo test`; web `npm run lint`, `npm run format:check`, `npm run build` (dùng `NEXT_DIST_DIR=.next-fix` nếu cấu hình có hỗ trợ, nếu không thì build mặc định).
- Không in secret, token, chữ ký webhook vào log, báo cáo hay test output.

## Báo cáo cuối (câu trả lời cuối của tiến trình agy)
1. Từng mục trong brief: XONG / CHƯA XONG / BỎ (lý do), kèm mã lỗi.
2. File đã tạo/sửa.
3. Test mới và kết quả đỏ → xanh; lệnh kiểm đã chạy và dòng kết quả thật.
4. Nhờ agent khác; Cập nhật contracts.
5. Quyết định đáng chú ý, giới hạn còn lại (`ponytail:` nếu cắt góc có trần).
