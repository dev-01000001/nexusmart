# Swarm M — Người bán tải ảnh của mình lên (F3), theo ADR-0005 (đã chấp nhận)

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `docs/adr/0005-realtime-chat-and-object-storage.md` (Phần B + ràng buộc bảo mật + T1),
`web/CLAUDE.md`, skill `platform-stack`.

## Vì sao
Người bán chỉ chọn được ~12 ảnh áo mẫu (`web/src/features/seller/ProductFormView.tsx`), không tải được ảnh thật ⇒ shop gốm/mỹ phẩm/đồ ăn không bán
được. ADR-0005 đã chốt SeaweedFS (≥ 4.55, Apache-2.0) qua S3 API (`aws-sdk-go-v2/service/s3`) nhưng T1 chưa làm (compose chưa có object storage,
chưa có `pkg/common/storage`).

## Bước 0 — cổng an toàn (BẮT BUỘC, trước khi code)
ADR-0005 yêu cầu: trước khi triển khai phải tra lại từng GHSA Critical của SeaweedFS công bố 2026-11-09 (`GHSA-25hp-4mw7-9c75`,
`GHSA-p983-2j86-34vj`, `GHSA-jq8h-9mjh-xv8f`, `GHSA-47fc-cj9r-ghpm`, `GHSA-f6rh-hw92-h9rx`) và bản phát hành mới nhất. Tra trên web
(GitHub advisories, release notes). Ghi kết quả vào phần "Tra lại 2026-11-27" cuối ADR-0005. Nếu còn Critical chưa vá chạm cấu hình dùng ở đây
(S3 nội bộ + filer) mà không né được bằng cấu hình ⇒ DỪNG, không code, báo cáo để manager hỏi chủ dự án.

## Thiết kế (manager chốt; lệch so với ADR ghi bổ sung vào ADR-0005)
Thay presigned PUT bằng upload qua gateway có kiểm nội dung (đơn giản hơn, kiểm magic bytes trước khi lưu, trình duyệt không cần thấy S3):
- Compose: service `seaweedfs` (image chính thức, bản đã tra ở bước 0, ghim tag), chạy S3 + filer với signing key, khoá S3 từ env
  (`S3_ACCESS_KEY`, `S3_SECRET_KEY`, giá trị dev trong `.env.example`, không commit secret thật), CHỈ trong mạng compose (không `ports:` ra host),
  không bật S3 Tables/Iceberg/STS/admin UI ra ngoài, volume bền. Healthcheck. Bucket `nexusmart-media-public` tạo tự động (gateway đảm bảo khi khởi
  động, idempotent). Đo RAM container sau khi chạy (ghi số vào báo cáo).
- `pkg/common/storage`: client S3 gọn (Put, Get stream, Head), cấu hình `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`,
  path-style. Test với fake/httptest.
- Gateway:
  - `POST /api/v1/seller/media` (seller, `shop_id` từ JWT; body là file ảnh thô, `Content-Type: image/jpeg|image/png|image/webp`,
    `Content-Length` ≤ 5 MB): đọc tối đa 5 MB + 1 byte (quá ⇒ `413`), kiểm magic bytes khớp loại (sai ⇒ `415`), giải mã header để lấy kích thước
    (≤ 6000×6000, ảnh hỏng ⇒ `400`), key `shops/<shop_id>/<sha256-hex>.<ext>` (trùng nội dung ⇒ dùng lại), `PutObject`. Trả
    `201 {"media":{"url":"/media/shops/<shop_id>/<hash>.<ext>","width","height","bytes","content_type"}}`. Rate limit theo user như các route seller.
    Chặn SVG/HTML/GIF. Không lưu tên file gốc. (EXIF: chưa bỏ — ghi `ponytail:` nêu trần.)
  - `GET /media/{key}` công khai: chỉ key khớp `^shops/[0-9a-f-]{36}/[0-9a-f]{64}\.(jpg|png|webp)$`, stream từ S3, `Content-Type` từ object,
    `Cache-Control: public, max-age=31536000, immutable`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'`;
    không có ⇒ `404`. Hỗ trợ `If-None-Match`/ETag nếu rẻ.
  - Ghi vào `docs/contracts.md`.
- Product service: trường `images` của sản phẩm/SKU chấp nhận URL `/media/shops/<shop_id của chính seller>/…` (ảnh của shop khác ⇒ `400`) cùng
  ảnh mẫu `/products/…` đang có. Kiểm nơi đang validate `images` trong `services/product` và thêm luật này (không đổi proto).
- Web:
  - BFF `web/src/app/api/seller/media/route.ts` (POST): kiểm session/CSRF như mẫu BFF, chuyển body dạng stream tới gateway (`duplex: "half"`),
    không đọc hết vào bộ nhớ; giới hạn 5 MB theo `Content-Length`.
  - `web/src/app/media/[...key]/route.ts` (GET, mọi host gốc + site): stream từ gateway, giữ header cache. Cập nhật CSP `img-src` nếu cần (đang
    `'self'` thì không phải đổi).
  - Form sản phẩm (`ProductFormView.tsx`, phần Hình ảnh): ô kéo-thả/chọn tệp (nhiều ảnh, tối đa 9), xem trước, tiến độ, xoá, kéo đổi thứ tự hoặc
    "đặt làm ảnh chính", lỗi tiếng Việt (quá 5 MB, sai định dạng). Thư viện ảnh mẫu giữ lại như tùy chọn phụ. Ảnh biến thể (SKU `image_url`) chọn
    từ ảnh đã tải.
  - Site Studio: ô logo và ảnh banner/hero nếu hiện chỉ nhận URL hoặc ảnh mẫu thì thêm nút "Tải ảnh lên" dùng cùng component.

## Được sửa
`docker-compose.yml`, `.env.example`, `pkg/common/storage/**` (mới), `pkg/common/go.mod|go.sum`, `services/gateway/**`, `services/product/**`
(chỉ validate images + test), `docs/contracts.md`, `docs/adr/0005-realtime-chat-and-object-storage.md` (mục bổ sung/tra lại),
`web/src/app/api/seller/media/**`, `web/src/app/media/**`, `web/src/components/app/**` (component tải ảnh), phần Hình ảnh của
`web/src/features/seller/ProductFormView*`, `web/src/features/seller/site/**` (ô logo/banner), `web/next.config.*`/middleware CSP chỉ khi bắt buộc.
Phối hợp: swarm W có thể vừa sửa `ProductFormView.tsx` (biến thể) — chỉ sửa phần Hình ảnh, đọc lại file ngay trước khi sửa.

## Kiểm (phải xanh, dán kết quả)
`cd pkg/common && go test -race ./...`, `cd services/gateway && go test -race ./...`, `cd services/product && go test -race ./...`, `make lint`
phần Go liên quan, web lint/format/test:unit + build (`NEXT_DIST_DIR=.next-m`). Test bắt buộc: 413/415/400 ảnh hỏng/SVG giả JPEG, key ngoài mẫu ⇒
404, seller A không gắn ảnh của shop B vào sản phẩm. `docker compose config` hợp lệ (KHÔNG `up`; manager tự dựng stack).
