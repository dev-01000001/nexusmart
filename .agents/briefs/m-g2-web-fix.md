# Swarm G2 – Sửa lỗi web sau review (2026-11-21)
Hợp đồng: `m-all-contract.md`. Báo cáo trước: `docs/reports/g-web.md`. Lỗi: `docs/reports/issues.md`.

## Sở hữu
`web/` TRỪ phần của T (`web/src/features/site/`, `web/src/app/(site)/`, `web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/`, `web/src/app/api/seller/site/`). Không sửa `services/`, `tests/`.

## Quyết định hợp đồng (Claude Code chốt)
Gateway là nguồn sự thật cho REST. BFF đọc đúng định dạng gateway, không thêm fallback hai khóa.
- QA-01: danh sách hội thoại và tin nhắn của gateway nằm ở khóa `items` (xem `services/gateway/internal/router/handlers_chat.go`). BFF gửi `metadata` dạng object, không gửi `metadata_json`. Auto-reply chỉ gọi `/api/v1/seller/chat/auto-reply`, bỏ lần thử `/api/v1/chat/auto-reply`.
- QA-02: resolve khiếu nại gửi đúng body mà `services/gateway/internal/router/handlers_d.go` đọc: `{resolution: REFUND|REPLACE|DISMISS, note, require_return_shipping}`. Bỏ lựa chọn UI mà gateway không hỗ trợ (ví dụ hoàn một phần) thay vì giả lập.

## Việc
1. QA-01 phần BFF (4 route chat + auto-reply), có unit test với payload gateway thật (lấy từ test gateway của B).
2. QA-02 phần BFF + `adminComplaintsTypes.ts` + màn `/admin/khieu-nai`.
3. OBS-11 phần G: kiểm tra `web/src/lib/server/trace.ts` đã có; mọi lời gọi gateway (`gw`, `gwRaw`, `callRefresh`, `lookupPublishedSiteHost`) truyền `traceparent`; `logServer` có `time`, `trace_id`, che token/email/SĐT.
4. PLT-02: nâng `next` lên bản vá cùng major để `npm audit --omit=dev --audit-level=high` exit 0. Làm việc này cuối cùng (T đang build web song song).

## Gate
`npm run lint`, `prettier --check` file đã sửa, `npm run test:unit`, `npx tsc --noEmit`, `NEXT_DIST_DIR=.next-g2 npm run build` (xóa `.next-g2`, khôi phục `web/tsconfig.json` và `web/next-env.d.ts`), `npm audit --omit=dev --audit-level=high`. Nếu tsc/build chỉ còn lỗi trong file của T (WEB-T-01..03), ghi rõ, không sửa file của T.
Ghi kết quả vào `docs/reports/g2-web-fix.md` (lệnh + đầu ra thật). Đánh dấu ID đã sửa trong `docs/reports/issues.md` (thêm cột/ghi chú "đã sửa bởi G2"), không xóa dòng.
