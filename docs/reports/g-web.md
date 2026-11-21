# Báo cáo Swarm G – Web cho các tính năng mới

> Trạng thái: Đang triển khai (hợp đồng component chèn vào site đã chốt sớm để Swarm T tích hợp song song).

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
- **Chức năng**: Nút mở chat cố định góc dưới-phải + khung hội thoại thời gian thực (WebSocket qua ticket ngắn hạn `POST /api/chat/ws-ticket`, tự kết nối lại và đồng bộ cursor, fallback HTTP), hỗ trợ gửi chữ, ảnh, thẻ sản phẩm (`productContext`), mã đơn (`orderContext`), tự mở khi nhận sự kiện bàn giao `nexusmart:open-chat` từ `SiteAssistantWidget`. Khi chưa đăng nhập sẽ hiển thị lời mời đăng nhập giữ nguyên `next`.
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
- **Chức năng**: Nút mở trợ lý AI + khung hội thoại SSE streaming (`POST /api/assistant/chat`), hiển thị từng token theo thời gian thực, thẻ sản phẩm kèm link `/san-pham/[slug]`, xác nhận thêm vào giỏ (không tự thanh toán), tra cứu đơn hàng bằng phiên đăng nhập của người mua, trích dẫn chính sách, phản hồi hữu ích/chưa đúng, và nút "Gặp nhân viên gian hàng" (`POST /api/assistant/handoff` + phát sự kiện `nexusmart:open-chat` cho `SiteChatWidget`).
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
- **Chức năng**: Khối tóm tắt ưu điểm / nhược điểm cho trang chi tiết sản phẩm (`GET /api/products/[id]/review-summary`). Tự ẩn gọn (`null`) khi sản phẩm chưa đủ số đánh giá hoặc dịch vụ chưa sẵn sàng.
- **Props (`ProductReviewSummaryProps`)**:
  ```ts
  export interface ProductReviewSummaryProps {
    productId: string;
    productName?: string;
    className?: string;
  }
  ```

### Gợi ý chèn nhanh vào `SiteShell` (Swarm T)
- Trong header (`previewMode === false`): thay `<form action="/tim-kiem">` bằng `<SiteSearchBar brandName={brandName} shopId={config_shop_id} compact />`.
- Cuối `SiteShell` (khi `previewMode === false`):
  ```tsx
  <SiteAssistantWidget shopId={shopId} brandName={brandName} />
  <SiteChatWidget shopId={shopId} brandName={brandName} />
  ```
  (Hai nút nổi được xếp chồng gọn ở góc dưới-phải không đè lên nhau; khi trợ lý bàn giao sang nhân viên, `SiteChatWidget` tự lắng nghe `nexusmart:open-chat` và mở đúng hội thoại vừa tạo.)
- Trang `/tim-kiem`: truyền props từ `loadPublicSitePageBundle` vào `<SiteSearchResults ... />`.
- Trang `/san-pham/[slug]`: đặt `<ProductReviewSummary productId={product.backendId || product.id} productName={product.name} />` dưới mô tả sản phẩm.

---

## 2. Tiến độ triển khai và kiểm thử

*(Sẽ cập nhật kết quả chi tiết, lệnh kiểm tra và đầu ra thật sau khi hoàn thành toàn bộ các hạng mục và gate.)*
