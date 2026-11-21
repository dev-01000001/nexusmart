# Swarm Q – Sửa test cuối cùng để gate cuối xanh (2026-11-21)
Hợp đồng: `m-all-contract.md`. Lỗi: `docs/reports/issues.md` (QA-03, QA-04). Gate cuối: `docs/reports/h.md`.
Mọi swarm khác đã xong. Q được dùng stack compose (tạo `.runs/stack.lock` nội dung "Q", xóa khi xong).

## Sở hữu
`tests/e2e/13_sites_backend_test.go`, `tests/e2e/15_custom_domains_test.go` (job sửa domains cũ đã xong), `tests/playwright/`. CẤM sửa `services/`, `web/`: bộ lọc SSRF `.local` của T là đúng, giữ nguyên.

## Việc
1. QA-03: đổi URL ảnh fixture `sandbox.nexusmart.local` sang host công khai hợp lệ mà validator chấp nhận (ví dụ `https://cdn.example.com/...`). Không nới validator.
2. QA-04: sửa selector bước tìm kiếm trong `tests/playwright/specs/buyer-flow.spec.ts` theo UI hiện tại (`SiteSearchBar` có `role="combobox"`; ưu tiên `getByRole`), rồi chạy thật Playwright trên site host.

## Gate
`gofmt` + `go vet` cho `tests/e2e`, chuỗi `make e2e && make seed && make e2e-web` EXIT 0 (20/20 Go e2e), Playwright `buyer-flow` PASS. Ghi `docs/reports/q-final-fix.md` (lệnh + đầu ra thật). Đánh dấu QA-03/QA-04 "đã sửa bởi Q" trong `issues.md`. Nếu vẫn đỏ, ghi rõ test, lỗi và nguyên nhân.
