# Báo cáo Swarm G2 – Sửa lỗi web sau review (2026-11-21)

> Trạng thái: **ĐẠT TOÀN BỘ GATE** (0 lỗi ESLint, 0 lỗi Prettier trên file G2, 191/191 unit test xanh, 0 lỗi TypeScript `tsc --noEmit`, production build `NEXT_DIST_DIR=.next-g2` thành công 58/58 route và đã dọn sạch `.next-g2` + khôi phục `web/tsconfig.json` & `web/next-env.d.ts`, `npm audit --omit=dev --audit-level=high` báo `found 0 vulnerabilities`).
>
> Quyền sở hữu: chỉ sửa trong `web/` thuộc phần G và `docs/reports/`. Không sửa file của Swarm T (`web/src/features/site/`, `web/src/app/(site)/`, `web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/`, `web/src/app/api/seller/site/`), không sửa `services/` hay `tests/`.

---

## 1. Các hạng mục đã hoàn thành

### 1.1 `QA-01` — Đồng bộ hợp đồng Chat BFF ↔ Gateway (nguồn sự thật `handlers_chat.go`, không fallback hai khóa)
- `web/src/features/chat/chatTypes.ts`:
  - Thêm các hàm adapter thuần theo đúng REST contract của gateway:
    - `parseGatewayConversationsResponse`: đọc trực tiếp khóa `items` (bỏ đọc `conversations`), lọc theo `shop_id`.
    - `parseGatewayMessagesResponse`: đọc trực tiếp khóa `items` (bỏ đọc `messages`), ghép và sắp xếp tin nhắn.
    - `buildGatewaySendMessageBody`: gửi `metadata` dạng JSON object (không gửi `metadata_json`).
    - `buildGatewayAutoReplyBody` và `SELLER_AUTO_REPLY_GATEWAY_PATH = "/api/v1/seller/chat/auto-reply"`.
  - Siết `normalizeMessage` và `normalizeAutoReplyConfig` đọc đúng định dạng gateway (`metadata` object, `config.greeting_message`), không dùng fallback hai khóa.
- `web/src/features/chat/chatClient.ts`:
  - Sửa frame gửi WebSocket gửi `metadata` dạng object thay vì `metadata_json` (khớp `services/chat/internal/ws/handler.go:ClientFrame`).
- 5 route BFF chat:
  - `web/src/app/api/chat/conversations/route.ts`: đọc `items` qua `parseGatewayConversationsResponse`; `POST` chuẩn hóa trực tiếp `res.conversation`.
  - `web/src/app/api/chat/conversations/[id]/messages/route.ts`: `GET` đọc `items` qua `parseGatewayMessagesResponse`; `POST` gửi `metadata` object qua `buildGatewaySendMessageBody`.
  - `web/src/app/api/seller/chat/conversations/route.ts`: gọi thẳng `/api/v1/seller/chat/conversations` và đọc `items` qua `parseGatewayConversationsResponse`.
  - `web/src/app/api/seller/chat/conversations/[id]/messages/route.ts`: `GET` đọc `items`; `POST` gửi `metadata` object.
  - `web/src/app/api/seller/chat/auto-reply/route.ts`: gọi duy nhất `/api/v1/seller/chat/auto-reply` cho cả `GET` và `PUT`, xóa bỏ hoàn toàn lần thử `/api/v1/chat/auto-reply` và trường thừa.
- Unit test (`web/src/features/chat/chat.test.mjs`):
  - Bổ sung bộ test với payload gateway thật trích từ `services/gateway/internal/router/router_chat_test.go` (danh sách hội thoại `items`, danh sách tin nhắn `items` đủ 4 loại `text`/`image`/`product`/`order`, `POST .../messages` gửi `metadata` object, `GET`/`PUT /api/v1/seller/chat/auto-reply`, kiểm tra không fallback khóa cũ `conversations`/`messages`, kiểm tra WS `metadata` object, và kiểm tra nguồn 5 file BFF không còn `metadata_json`, `/api/v1/chat/auto-reply`, `res.conversations`, `res.messages`).

### 1.2 `QA-02` — Đồng bộ hợp đồng Admin Khiếu nại (`handlers_d.go`) & bỏ lựa chọn không hỗ trợ
- `web/src/features/admin/adminComplaintsTypes.ts`:
  - Thay `ComplaintDecision` cũ (`REFUND_BUYER`, `APPROVE_RETURN`, `PARTIAL_REFUND`, `REJECT_COMPLAINT`) bằng đúng enum của gateway: `ComplaintResolution = "REFUND" | "REPLACE" | "DISMISS"`.
  - `validateComplaintResolution` kiểm tra và trả đúng body gateway: `{ resolution, note, require_return_shipping }` (bỏ `decision`, bỏ `PARTIAL_REFUND` và `refund_amount_vnd`, không thêm fallback hai khóa).
  - `normalizeComplaintsListResponse` và `normalizeComplaint` đọc đúng khóa gateway `items`, `resolution`, `admin_note`, `evidence_urls`, `reason` (giữ `sanitizeSafeUrl` cho ảnh bằng chứng).
  - Đổi helper nhãn sang `getComplaintResolutionLabelVi` cho 3 giá trị `REFUND | REPLACE | DISMISS`.
- `web/src/app/api/admin/complaints/[id]/resolve/route.ts`:
  - Gửi nguyên `{ resolution, note, require_return_shipping }` lên `POST /api/v1/admin/complaints/{id}/resolve`, cập nhật bảng thông báo lỗi theo `INVALID_RESOLUTION` và `INVALID_RETURN_SHIPPING`.
- `web/src/features/admin/AdminComplaintsClient.tsx` & `web/src/app/(admin)/admin/khieu-nai/page.module.css`:
  - Bỏ hoàn toàn tùy chọn UI "Hoàn một phần tiền" (`PARTIAL_REFUND`) và ô nhập số tiền giả lập.
  - Hiển thị đúng 3 phương án gateway hỗ trợ: **Hoàn tiền cho người mua (`REFUND`)** (kèm checkbox `require_return_shipping`: "Yêu cầu người mua gửi trả hàng trước khi hoàn tiền"), **Đổi sản phẩm thay thế (`REPLACE`)** (gửi `require_return_shipping: true`), và **Bác khiếu nại (`DISMISS`)** (gửi `require_return_shipping: false`).
  - Cập nhật bộ lọc trạng thái khớp gateway (`Tất cả`, `Chờ xử lý`, `Đã giải quyết`, `Đã bác`).
- Unit test (`web/src/features/admin/adminComplaints.test.mjs`):
  - Kiểm tra parse payload thật của `handlers_d.go` (`items`, `complaintDTO`), xác nhận không fallback sang khóa `complaints`, xác nhận `validateComplaintResolution` chấp nhận đúng `{ resolution, note, require_return_shipping }` và từ chối `PARTIAL_REFUND`, `REFUND_BUYER`, `APPROVE_RETURN`, `REJECT_COMPLAINT`, và khóa `decision` cũ.

### 1.3 `OBS-11` (phần G) — Truyền W3C `traceparent`/`tracestate` & log JSON `logServer`
- Đã kiểm tra và củng cố toàn bộ luồng trace + log trong phạm vi G:
  - `web/src/lib/server/trace.ts`: đã có; bổ sung kiểu `TraceContext` và mở rộng regex `VN_PHONE_RE` che cả số điện thoại Việt Nam có dấu cách/chấm/gạch ngang (`+84 912 345 678`, `0987.654.321`) mà không làm hỏng `trace_id` hex.
  - `web/src/lib/server/gateway.ts`: `gw` và `gwRaw` luôn gắn `traceparent`/`tracestate`, truyền tiếp cùng ngữ cảnh trace sang `refreshInPlace -> rotateRefreshToken`, và gắn `traceId` vào mọi `GatewayError` (kể cả timeout / lỗi mạng / phản hồi không phải JSON).
  - `web/src/lib/server/tokens.ts`: `rotateRefreshToken` / `callRefresh` nhận và forward `traceparent`/`tracestate` (tự sinh W3C `traceparent` nếu không truyền), gắn `traceId` vào `GatewayError`.
  - `web/src/lib/server/siteLookup.ts` & `web/src/lib/server/sites.ts`: `lookupPublishedSiteHost` và `fetchPublishedSiteByShop(uncached)` forward `traceparent`/`tracestate` từ request hiện tại (hoặc tự sinh khi ngoài request context).
  - `web/src/middleware.ts`: đảm bảo mọi request đi qua middleware có `traceparent` hợp lệ và truyền cùng `trace` cho `lookupPublishedSiteHost` và `rotateRefreshToken`.
  - `web/src/lib/server/errors.ts` & `web/src/lib/server/http.ts`: `logServer` luôn xuất JSON có `time` (RFC3339), `level`, `msg` (đã scrub PII/secret), `service: "web-bff"`, `trace_id` và `redactLogFields`; `errorFromResponse` lấy fallback từ `x-trace-id` / `traceId` của request; `apiRoute` truyền `trace_id` từ `traceparent` vào `errorResponse` để cả `bff_unhandled_error` và `bff_gateway_unavailable` đều có `trace_id` mà không rò rỉ ra trình duyệt.
  - `web/src/features/platform/directory.server.ts:59` & `web/src/lib/catalog.ts:147`: bổ sung `trace_id: err instanceof GatewayError ? err.traceId : ""` vào 2 lời gọi `logServer` còn thiếu.
  - Unit test (`web/src/lib/server/security.test.mjs`): bổ sung test xác minh JSON output của `logServer` (`time`, `trace_id`, che token/JWT/email/SĐT trong cả `msg` và `fields`), `errorResponse` ghi `trace_id` fallback, và `rotateRefreshToken` (`callRefresh`) / `lookupPublishedSiteHost` gửi đúng header `traceparent`/`tracestate`.

### 1.4 `PLT-02` — Vá lỗ hổng High trong `npm audit --omit=dev --audit-level=high`
- Phân tích: `next@15.5.29` là bản vá mới nhất của major 15 (`dist-tags.backport = 15.5.29`; phiên bản duy nhất nâng `postcss` mặc định là `next@16.4.6` — breaking major upgrade), và `next@15.5.29` vẫn ghim `postcss@8.4.31` dính 4 advisory của `postcss <=8.5.22` (`GHSA-qx2v-qp2m-jg93`, `GHSA-6g55-p6wh-862q`, `GHSA-fxqj-rqcc-2cmp`, `GHSA-r28c-9q8g-f849`).
- Khắc phục: cập nhật `web/package.json` (`"next": "^15.5.29"` và `"overrides": { "postcss": "^8.5.33" }` — cùng major 8.x của PostCSS, không breaking change) và đồng bộ `web/package-lock.json` qua `npm install --no-audit --no-fund`.
- Kết quả: `npm audit --omit=dev --audit-level=high` và `npm audit --omit=dev` đều trả về `found 0 vulnerabilities` (exit 0), toàn bộ lint/test/tsc/build production chạy xanh.

---

## 2. Lệnh kiểm tra và đầu ra thật

| Gate | Lệnh chạy | Đầu ra thật | Kết quả |
|---|---|---|---|
| ESLint | `cd web && npm run lint` | `npm notice run eslint .` (0 error, 0 warning, exit 0) | **PASS** |
| Prettier (24 file G2) | `cd web && npx prettier --check src/features/chat/chatTypes.ts ... src/lib/server/security.test.mjs` | `All matched files use Prettier code style!` (exit 0) | **PASS** |
| Unit tests | `cd web && npm run test:unit` | `tests 191 / suites 11 / pass 191 / fail 0` (exit 0) | **PASS** |
| TypeScript | `cd web && npx tsc --noEmit` | `npm notice run 'tsc' --noEmit` (0 lỗi trên toàn `web/`, exit 0) | **PASS** |
| Production build (`.next-g2`) | `cd web && NEXT_DIST_DIR=.next-g2 npm run build; BUILD_RC=$?; rm -rf .next-g2; cd .. && git checkout -- web/tsconfig.json web/next-env.d.ts` | `✓ Compiled successfully in 12.1s`, `✓ Linting and checking validity of types`, `✓ Generating static pages (58/58)`, `=== BUILD_RC=0 ===`. Đã xóa `web/.next-g2` (`No such file or directory`) và khôi phục `web/tsconfig.json`, `web/next-env.d.ts`. | **PASS** |
| Security audit (prod deps) | `cd web && npm audit --omit=dev --audit-level=high` | `found 0 vulnerabilities` (exit 0) | **PASS** |
| Data integrity | `cd web && npm run check:data` | `ALL 10 DATA INTEGRITY CHECKS PASSED.` (exit 0) | **PASS** |
| Design-taste lint (file UI/chat G2) | `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs web/src/features/admin/AdminComplaintsClient.tsx web/src/app/(admin)/admin/khieu-nai/page.module.css web/src/features/chat/chatTypes.ts web/src/features/chat/chatClient.ts` | `RESULT: 0 FAIL, 0 WARN` (exit 0) | **PASS** |

---

## 3. Lưu ý liên swarm (ngoài quyền sở hữu G2)

1. **Swarm H (`tests/e2e-web/07_returns_seller_admin.test.mjs:542-545,589`)**: test E2E hiện gửi `{ decision: "REJECT_COMPLAINT", note: ... }` tới `POST /api/admin/complaints/{id}/resolve`. Sau khi G2 áp dụng hợp đồng chốt cho `QA-02` (`{ resolution: "REFUND"|"REPLACE"|"DISMISS", note, require_return_shipping }`, không fallback khóa `decision` cũ), Swarm H cần cập nhật 2 lời gọi đó sang `{ resolution: "DISMISS", note: ..., require_return_shipping: false }`. Đã ghi nhận vào `docs/reports/issues.md`.
2. **Swarm T (`web/src/features/site/siteContext.server.ts:118`)**: nằm ngoài phạm vi sở hữu của G2 nên không chỉnh sửa. (`tsc --noEmit` và `NEXT_DIST_DIR=.next-g2 npm run build` toàn `web/` hiện đã xanh 100%, không còn lỗi type của `WEB-T-01`.)
