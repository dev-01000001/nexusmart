# S9b — QA đợt 2: viết lại test bám giao diện cũ

Đọc trước: `common.md`, spec §13c (không xoá bất biến), kiểm kê của chính S9 ở `.runs/shadcn-redesign/p1-4/qa/test-inventory.md`.
Mã của mọi khu đã xong (admin, platform, site S4a/S4b đã commit; seller S1a–c, trang chủ S3b, site S4c/S4d nằm trong working
tree, chờ Claude commit). Không swarm nào đang sửa mã sản phẩm.

## Mục tiêu
`npm run test:unit` xanh hết trên working tree hiện tại mà không mất bất biến nào. Hiện 504 test, 491 pass, 13 fail:
- `scripts/`: Wave 1 #13, Wave 2b #7, Wave 3a #23 #24 #25, Wave 3b-1 #11 #12, Wave 3b-2 #6 #7, Wave 3b-3 Round 6 W3–W6;
- `src/`: `AdminConfigValidators.test.mjs` (search reindex / cau-hinh), `checkout/helpers.test.mjs` (Mục 3), `seller/sellerDedupe.test.mjs`.

Các swarm đã báo `TEST VỠ` kèm lý do trong `.runs/shadcn-redesign/p1-4/<swarm>/progress.md` và báo cáo cuối (`run*.json`), ví dụ:
`w2b-web.test.mjs:607-611, 1742-1749, 1913-1914`, `w3b-web.test.mjs:808-811`, `w3b2-web.test.mjs:696-701, 748`,
`w3b3-web-a8.test.mjs:1034-1068`, `AdminConfigValidators.test.mjs:426`, `checkout/helpers.test.mjs:439`, `sellerDedupe.test.mjs:93-99, 130-133`.

## Phạm vi
- Được sửa: `web/scripts/*.test.mjs`, `web/src/**/*.test.mjs`; trong `tests/playwright/specs/` chỉ đổi bộ chọn "Mở giỏ hàng" →
  "Xem giỏ hàng" (`buyer-flow.spec.ts:76`, `zalopay-mock.spec.ts:40`).
- Cấm: mọi file không phải test, `tests/ui-flows/`, file sinh tự động. Không xoá test, không thêm `skip`/`todo`.

## Cách viết lại
- Giữ nguyên assertion logic. Chỉ viết lại assertion bám CSS Module, tên class hay component cũ, để kiểm **cùng bất biến** trên mã mới.
- Ưu tiên kiểm hành vi (gọi helper, render SSR như test hiện có). Không được thì grep mã mới theo cái mang bất biến: prop, component
  shadcn/Radix (`AlertDialog`, `EmptyState` với `href`…), lớp Tailwind chủ chốt (`sticky`, `right-0`, `truncate`, `break-words`…).
  Không bám thứ tự chuỗi class.
- Mock loader (`sellerDedupe.test.mjs`): đổi theo import mới, không mock `.module.css` đã xoá.
- Nếu bất biến thật sự đã mất trong mã mới: để test đỏ, ghi `LỖI THẬT: <file test:dòng> — <bất biến> — <file mã:dòng>`, không làm yếu test.

## Xong khi
- `flock /tmp/nm-web-ts.lock npm run test:unit`: 0 fail, hoặc chỉ còn các test đã ghi `LỖI THẬT`.
- `npx eslint` + `npx prettier --check` sạch trên các file test đã sửa.
- `rg -n "module\.css" web/scripts web/src --glob '*.test.mjs'` chỉ còn file `.module.css` còn tồn tại.
- Không cần trình duyệt, không cần dev server.
- Báo cáo theo `common.md` §5, kèm bảng: file:dòng, assertion cũ → assertion mới, bất biến được giữ.
