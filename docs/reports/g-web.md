# Báo cáo Swarm G – Web cho các tính năng mới

> Trạng thái: Hoàn tất toàn bộ phạm vi sở hữu của Swarm G (BFF, site-injectable components, trả hàng người mua, Seller real screens + AI + Chat, Admin Khiếu nại + Cấu hình, và vá `OBS-11`). Gate toàn repo `web/` chỉ còn bị chặn tạm thời bởi các file đang sửa dở của Swarm T (`web/src/features/seller/site/` và `web/src/features/site/` — Swarm G bị cấm sửa).

## 1. Component chèn vào layout site (cho Swarm T)

Mọi component dưới đây là Client Component tự quản lý BFF của site hiện tại (host isolation tự động từ `Host` header), dùng bộ token `var(--site-*, var(--...))` để khớp mọi template sáng/tối của Swarm T, và không phụ thuộc vào file trong `web/src/features/site/`.

### 1.1 Thanh tìm kiếm có gợi ý (`SiteSearchBar`)
- **Import**: `import { SiteSearchBar } from "@/features/search";`
  (File: `web/src/features/search/SiteSearchBar.tsx`, barrel `web/src/features/search/index.ts`)
- **Chức năng**: Ô tìm kiếm cho header/drawer của site, debounce 200ms gọi `GET /api/search/suggest?q=`, điều hướng phím mũi tên/Enter/Escape, lịch sử tìm kiếm gần đây (có nút xóa), gửi form tới `/tim-kiem?q=...`.
- **Props (`SiteSearchBarProps`)**:
  ```ts
  export interface SiteSearchBarProps {
    brandName: string;
    shopId?: string;
    initialQuery?: string;
    placeholder?: string;
    /** Gọn hơn cho header desktop hoặc mobile drawer. */
    compact?: boolean;
    /** Gọi sau khi người mua chọn gợi ý hoặc gửi tìm kiếm (vd: đóng mobile drawer). */
    onNavigate?: (query: string) => void;
    className?: string;
  }
  ```

### 1.2 Trang kết quả tìm kiếm trên site (`SiteSearchResults`)
- **Import**: `import { SiteSearchResults } from "@/features/search";`
  (Cũng export bí danh `SiteSearchPage` cùng props. File: `web/src/features/search/SiteSearchResults.tsx`)
- **Chức năng**: Giao diện trang `/tim-kiem` hoàn chỉnh trên site host: gọi `GET /api/search` (tự khóa `shop_id` theo host), lọc theo danh mục (facet), khoảng giá nguyên VND (`min_price`, `max_price`), sắp xếp (`relevance | price_asc | price_desc | newest`), gợi ý từ khóa, phân trang, tự fallback về tìm kiếm tiếng Việt không dấu trên `fallbackProducts` nếu Search service chưa chạy.
- **Props (`SiteSearchResultsProps`)**:
  ```ts
  export interface SiteSearchResultsProps {
    brandName: string;
    shopId: string;
    initialQuery?: string;
    initialCategory?: string;
    initialSort?: "relevance" | "price_asc" | "price_desc" | "newest" | string;
    initialMinPrice?: number;
    initialMaxPrice?: number;
    initialPage?: number;
    /** Dữ liệu dự phòng từ bundle của site khi Search service tạm gián đoạn. */
    fallbackProducts?: Product[];
    categories?: CategoryInfo[];
  }
  ```

### 1.3 Nút và khung chat với gian hàng (`SiteChatWidget`)
- **Import**: `import { SiteChatWidget } from "@/features/chat";`
  (File: `web/src/features/chat/SiteChatWidget.tsx`, barrel `web/src/features/chat/index.ts`)
- **Chức năng**: Nút mở chat cố định góc dưới-phải + khung hội thoại thời gian thực (WebSocket qua ticket ngắn hạn `POST /api/chat/ws-ticket`, tự kết nối lại và đồng bộ cursor, fallback HTTP), hỗ trợ gửi chữ, ảnh, thẻ sản phẩm (`productContext`), mã đơn (`orderContext`), cảnh báo giao dịch ngoài sàn (UC-B14), tự mở khi nhận sự kiện bàn giao `nexusmart:open-chat` từ `SiteAssistantWidget`. Khi chưa đăng nhập sẽ hiển thị lời mời đăng nhập giữ nguyên `next`.
- **Props (`SiteChatWidgetProps`)**:
  ```ts
  export interface SiteChatProductContext {
    id: string;
    slug?: string;
    name: string;
    priceVnd: number;
    image?: string;
  }

  export interface SiteChatOrderContext {
    id: string;
    orderCode: string;
  }

  export interface SiteChatWidgetProps {
    shopId: string;
    brandName: string;
    productContext?: SiteChatProductContext;
    orderContext?: SiteChatOrderContext;
    /** Khi truyền từ ngoài (vd: sau khi handoff từ trợ lý), tự mở tới hội thoại này. */
    externalConversationId?: string | null;
    defaultOpen?: boolean;
    /** Vị trí nổi mặc định tránh chồng với SiteAssistantWidget. */
    offsetBottomPx?: number;
    className?: string;
  }
  ```

### 1.4 Khung trợ lý mua sắm AI (`SiteAssistantWidget`)
- **Import**: `import { SiteAssistantWidget } from "@/features/assistant";`
  (File: `web/src/features/assistant/SiteAssistantWidget.tsx`, barrel `web/src/features/assistant/index.ts`)
- **Chức năng**: Nút mở trợ lý AI + khung hội thoại SSE streaming (`POST /api/assistant/chat`), hiển thị từng token theo thời gian thực, thẻ sản phẩm kèm link `/san-pham/[slug]`, xác nhận thêm vào giỏ (UC-AI04, không tự thanh toán), tra cứu đơn hàng bằng phiên đăng nhập của người mua, trích dẫn chính sách (UC-AI11), phản hồi hữu ích/chưa đúng (UC-AI13), và nút "Gặp nhân viên gian hàng" (`POST /api/assistant/handoff` + phát sự kiện `nexusmart:open-chat` cho `SiteChatWidget`).
- **Props (`SiteAssistantWidgetProps`)**:
  ```ts
  export interface SiteAssistantProductContext {
    id: string;
    slug?: string;
    name: string;
    priceVnd?: number;
  }

  export interface SiteAssistantWidgetProps {
    shopId: string;
    brandName: string;
    productContext?: SiteAssistantProductContext;
    /** Gọi sau khi bàn giao thành công sang hội thoại chat với nhân viên gian hàng. */
    onHandoffToChat?: (conversationId: string) => void;
    defaultOpen?: boolean;
    offsetBottomPx?: number;
    className?: string;
  }
  ```

### 1.5 Tóm tắt đánh giá sản phẩm do AI tổng hợp (`ProductReviewSummary`)
- **Import**: `import { ProductReviewSummary } from "@/features/assistant";`
  (File: `web/src/features/assistant/ProductReviewSummary.tsx`)
- **Chức năng**: Khối tóm tắt ưu điểm / nhược điểm cho trang chi tiết sản phẩm (`GET /api/products/[id]/review-summary`). Tự ẩn gọn (`null`) khi sản phẩm chưa đủ 10 đánh giá hoặc dịch vụ chưa sẵn sàng (UC-AI05).
- **Props (`ProductReviewSummaryProps`)**:
  ```ts
  export interface ProductReviewSummaryProps {
    productId: string;
    productName?: string;
    className?: string;
  }
  ```

### Gợi ý chèn nhanh vào `SiteShell` (Swarm T)
- Trong header (`previewMode === false`): thay `<form action="/tim-kiem">` bằng `<SiteSearchBar brandName={brandName} shopId={shopId} compact />`.
- Cuối `SiteShell` (khi `previewMode === false`):
  ```tsx
  <SiteAssistantWidget shopId={shopId} brandName={brandName} />
  <SiteChatWidget shopId={shopId} brandName={brandName} />
  ```
- Trang `/tim-kiem`: truyền props từ `loadPublicSitePageBundle` vào `<SiteSearchResults ... />`.
- Trang `/san-pham/[slug]`: đặt `<ProductReviewSummary productId={product.backendId || product.id} productName={product.name} />` dưới mô tả sản phẩm.

---

## 2. Danh mục hạng mục đã hoàn thành

### 2.1 Nền tảng BFF, Host Isolation & Observability (`OBS-11`)
- `web/src/lib/site/host.ts` & `web/src/lib/site/site.test.mjs`: mở rộng `SITE_ALLOWED_API_PREFIXES` và `BUYER_API_PREFIXES` cho `/api/search`, `/api/chat`, `/api/assistant`, `/api/products` (chỉ chạy trên site host đã xuất bản, 404 trên root host; mọi `/api/seller/*` và `/api/admin/*` giữ nguyên 404 trên site host).
- `web/src/lib/server/gateway.ts`: thêm `gwRaw` hỗ trợ proxy SSE streaming (tự gắn cookie JWT người dùng, xoay vòng refresh token khi 401, truyền `req.signal`, không buffer, không lộ lỗi nội bộ).
- `web/src/lib/server/config.ts`: thêm `buildChatWsUrl(ticket)` sinh URL `ws(s)://.../api/v1/chat/ws?ticket=...`.
- `web/src/lib/server/trace.ts`, `errors.ts`, `gateway.ts`, `tokens.ts`, `siteLookup.ts`, `sites.ts` (khắc phục `OBS-11` từ `docs/reports/issues.md`): truyền/khởi tạo W3C `traceparent`/`tracestate` qua các lệnh gọi gateway; bổ sung `time`, `trace_id` và redaction PII/secret (Bearer, JWT, DSN password, email, SĐT, sensitive keys) trong `logServer`.
- `web/src/components/Icons.tsx`: thêm `IconSend`, `IconFlag`.

### 2.2 Tìm kiếm trên site & Trả hàng trong chi tiết đơn
- BFF:
  - `web/src/app/api/search/route.ts` (`GET /api/search`, khóa cứng `shop_id` từ `requireRequestSite`, lọc giá/danh mục/sắp xếp/phân trang)
  - `web/src/app/api/search/suggest/route.ts` (`GET /api/search/suggest`, khóa `shop_id` theo host)
  - `web/src/app/api/orders/[id]/returns/route.ts` (`GET`, `POST` theo hợp đồng `{items, reason, photos}`, kiểm tra `assertInStorefront`, `assertBuyerOrderOwnership`, đối chiếu item/số lượng thuộc đơn và URL ảnh an toàn)
- UI & Unit tests:
  - `web/src/features/search/` (`SiteSearchBar`, `SiteSearchResults`/`SiteSearchPage`, `searchTypes.ts`, `search.test.mjs`)
  - `web/src/features/account/` (`returnsTypes.ts`, `returns.test.mjs`, nâng cấp `OrderDetailClientView.tsx` cho phép chọn từng dòng hàng/số lượng, đính kèm tối đa 5 ảnh hợp lệ và theo dõi trạng thái đổi trả/hoàn tiền)

### 2.3 Chat thời gian thực (Người mua & Người bán)
- BFF người mua (site host):
  - `web/src/app/api/chat/conversations/route.ts` (`GET` lọc theo `site.shopId`, `POST` khóa `shop_id: site.shopId`)
  - `web/src/app/api/chat/conversations/[id]/messages/route.ts` (`GET` theo cursor `before`/`after`/`limit`, `POST` fallback HTTP có validate loại tin)
  - `web/src/app/api/chat/ws-ticket/route.ts` (`POST` trả `{ticket, ws_url, expires_at}`)
- BFF người bán (root host):
  - `web/src/app/api/seller/chat/conversations/route.ts`
  - `web/src/app/api/seller/chat/conversations/[id]/messages/route.ts`
  - `web/src/app/api/seller/chat/ws-ticket/route.ts`
  - `web/src/app/api/seller/chat/auto-reply/route.ts` (`GET`, `PUT`)
- Shared WS client & UI:
  - `web/src/features/chat/` (`chatTypes.ts`, `chatClient.ts` có backoff reconnect & cursor sync, `SiteChatWidget.tsx`, `chat.test.mjs`)
  - Thay hoàn toàn màn mock `web/src/app/(seller)/seller/chat/` bằng hộp thư thật kết nối WebSocket + nút **Gợi ý trả lời AI** (`POST /api/seller/assistant/suggest-reply`, không tự gửi).

### 2.4 Trợ lý AI (Người mua & Người bán)
- BFF:
  - `web/src/app/api/assistant/chat/route.ts` (`POST` SSE proxy qua `gwRaw`, khóa `shop_id: site.shopId`, chạy bằng JWT cookie của người dùng)
  - `web/src/app/api/assistant/handoff/route.ts` (`POST` chuyển tiếp nhân viên gian hàng)
  - `web/src/app/api/products/[id]/review-summary/route.ts` (`GET`)
  - `web/src/app/api/seller/assistant/{report,describe-product,suggest-reply}/route.ts`
  - `web/src/app/api/seller/products/[id]/review-summary/route.ts`
- UI & Unit tests:
  - `web/src/features/assistant/` (`sseParser.ts`, `assistantTypes.ts`, `SiteAssistantWidget.tsx`, `ProductReviewSummary.tsx`, `assistant.test.mjs`)
  - `web/src/app/(seller)/seller/bao-cao-ai/` (Hỏi đáp số liệu kinh doanh AI kèm bảng số liệu gốc — UC-AI09)
  - `web/src/features/seller/ProductFormView.tsx`: thêm thanh **Tạo mô tả bằng AI** chọn giọng văn, xem trước, áp dụng/chèn thêm (UC-AI06).

### 2.5 Thay 3 màn mock Seller (`chat`, `ho-so`, `tuong-tac`) & Đổi trả phía Seller
- BFF mới:
  - `web/src/app/api/seller/orders/[id]/returns/route.ts`
  - `web/src/app/api/seller/returns/[id]/{approve,reject}/route.ts`
  - `web/src/app/api/seller/notifications/{route.ts,read/route.ts}`
- Giao diện thật thay mock:
  - `/seller/chat`: hộp chat thời gian thực + gợi ý trả lời AI (ở mục 2.3)
  - `/seller/ho-so`: hồ sơ gian hàng thật (`loadSellerShop`), cập nhật liên hệ chủ tài khoản (`PATCH /api/auth/me`), địa chỉ kho lấy hàng (`/api/v1/users/me/addresses`), cấu hình trả lời tự động chat (`/api/seller/chat/auto-reply`)
  - `/seller/tuong-tac`: 3 tab thật — **Đổi trả & Hoàn tiền** (duyệt/từ chối trực tiếp), **Tóm tắt đánh giá AI** theo sản phẩm thật, và **Thông báo vận hành** (`/api/seller/notifications`)
  - `web/src/features/seller/SellerOrderDetailClient.tsx`: hiển thị chi tiết yêu cầu đổi trả & nút Duyệt / Từ chối đổi trả
  - `web/src/features/seller/SellerShell.tsx` & `web/src/app/(seller)/layout.tsx`: bỏ hoàn toàn badge mock, dùng số đơn trả hàng thật và thêm mục điều hướng **Báo cáo AI** (`/seller/bao-cao-ai`)
  - `web/src/features/seller/sellerStore.ts`: dọn sạch toàn bộ dữ liệu/state mock cũ (~430 dòng), chỉ giữ 3 helper thuần (`EMPTY_DASH`, `parseUiState`, `formatDeltaPct`).
  - Unit tests: `web/src/features/seller/sellerReturns.test.mjs`.

### 2.6 Admin Khiếu nại & Cấu hình
- BFF mới (đều qua `apiRoute` + `assertAdminSession`):
  - `web/src/app/api/admin/complaints/route.ts`, `web/src/app/api/admin/complaints/[id]/resolve/route.ts`
  - `web/src/app/api/admin/categories/route.ts`, `web/src/app/api/admin/categories/[id]/route.ts`
  - `web/src/app/api/admin/users/route.ts`, `web/src/app/api/admin/users/[id]/{status,role}/route.ts`
  - `web/src/app/api/admin/vouchers/route.ts`
- UI & Unit tests:
  - `/admin/khieu-nai` (`AdminComplaintsClient.tsx`): bảng lọc trạng thái + thanh tra chi tiết bằng chứng + biểu mẫu phân xử (`REFUND_BUYER`, `APPROVE_RETURN`, `PARTIAL_REFUND` nguyên VND, `REJECT_COMPLAINT`)
  - `/admin/cau-hinh` (`AdminConfigClient.tsx`): 3 tab thật — **Danh mục & Phí hoa hồng** (`commission_rate_bps`), **Voucher sàn & Freeship**, **Người dùng & Phân quyền** (chặn tự khóa / tự đổi vai trò chính mình)
  - `AdminShell.tsx` & `app/(admin)/layout.tsx`: thêm mục **Khiếu nại** (kèm badge đếm an toàn khi backend chưa chạy) và **Cấu hình sàn**, thêm tìm nhanh tiền tố `KN-`.
  - Unit tests: `web/src/features/admin/adminComplaints.test.mjs`.

---

## 3. Lệnh kiểm tra và đầu ra thật

| Gate | Lệnh chạy | Kết quả thật |
|---|---|---|
| ESLint (toàn `web/`) | `cd web && npm run lint` | **PASS** (exit 0, 0 error, 0 warning) |
| Prettier (phạm vi Swarm G) | `find src ... !(Swarm T paths) \| xargs -0 npx prettier --check` | **PASS** (`All matched files use Prettier code style!`) |
| Unit tests (toàn `web/`) | `cd web && npm run test:unit` | **PASS** — `tests 138, pass 138, fail 0` (+51 test mới của Swarm G) |
| Data integrity | `cd web && npm run check:data` | **PASS** — `ALL 10 DATA INTEGRITY CHECKS PASSED.` |
| Design-taste lint (phạm vi Swarm G, 134 file) | `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs ...` | **PASS** — `RESULT: 0 FAIL, 4 WARN` (4 WARN đều là `padStart(2,"0")` ngày/hex có sẵn từ trước; 0 FAIL / 0 WARN ở code mới) |
| Kiểm tra thuật ngữ kỹ thuật UI người mua | `grep -riE "sổ cái\|JWT\|IDEM-\|sandbox\|2 cấp\|ghi kép\|Meilisearch\|pgvector\|WebSocket\|RAG" ...` | **PASS** — 0 chuỗi kỹ thuật hiển thị ra UI người mua |
| TypeScript (`tsc --noEmit`) | `cd web && npx tsc --noEmit` | **0 lỗi ở toàn bộ file Swarm G**. Hiện báo lỗi ở file của Swarm T (`src/features/seller/site/SellerSiteEditorClient.tsx`) |
| Production build (`.next-g`) | `cd web && NEXT_DIST_DIR=.next-g npm run build` (kèm xóa `.next-g`, khôi phục `tsconfig.json` & `next-env.d.ts`) | SWC/Webpack **✓ Compiled successfully in 97s**; bước typecheck của Next dừng ở file của Swarm T (`src/features/seller/site/SellerSiteEditorClient.tsx:96`). Đã xóa `.next-g` và khôi phục `tsconfig.json`, `next-env.d.ts` sạch. |

---

## 4. Tiêu chí đặc tả & Phần chưa đạt (kèm lý do)

- **Đạt trong phạm vi Swarm G**:
  - Đủ BFF (CSRF `assertSameOrigin`, session cookie HttpOnly, host isolation khóa `shop_id` từ `Host`, SSE proxy không buffer, WS ticket ngắn hạn, không lộ lỗi nội bộ, truyền W3C `traceparent` + che PII/secret trong log).
  - Đủ 5 component chèn vào site (`SiteSearchBar`, `SiteSearchResults`/`SiteSearchPage`, `SiteChatWidget`, `SiteAssistantWidget`, `ProductReviewSummary`) đúng hợp đồng đã công bố cho Swarm T.
  - Đủ luồng người mua (tìm kiếm + lọc + sắp xếp + gợi ý, chat, trợ lý AI SSE, trả hàng chi tiết đơn), người bán (thay 3 màn mock `chat`, `ho-so`, `tuong-tac`, gợi ý trả lời AI, báo cáo AI, sinh mô tả AI trong form sản phẩm, duyệt/từ chối đổi trả), và quản trị (khiếu nại, cấu hình sàn).
  - 138/138 unit test với dữ liệu giả chạy xanh.
- **Chưa đạt ở mức toàn repo `web/` (do Swarm T chạy song song)**:
  1. `npx tsc --noEmit` và bước typecheck của `NEXT_DIST_DIR=.next-g npm run build` hiện lỗi tại `web/src/features/seller/site/SellerSiteEditorClient.tsx` (`SiteTemplateConfig` chưa tương thích `SiteConfig`) — thuộc quyền sở hữu của Swarm T, Swarm G không được phép sửa.
  2. `npm run format:check` toàn `web/` hiện báo 3 file chưa format của Swarm T (`src/features/site/templateCatalog.ts`, `src/features/site/templateValidator.ts`, `src/app/api/seller/site/apply-template/route.ts`).
  3. `design-taste-frontend/scripts/lint.mjs web/src` toàn `web/` hiện báo 1 FAIL tại `src/features/site/templateValidator.ts:868` (`.eyebrow,`) của Swarm T.
  - Các phát hiện ở phần của Swarm T đã được ghi nhận vào `docs/reports/issues.md`. Sau khi Swarm T hoàn tất các file trên, cần chạy lại `npm run format:check`, `npx tsc --noEmit`, `NEXT_DIST_DIR=.next-g npm run build` và `design-taste lint` trên toàn `web/src`.
