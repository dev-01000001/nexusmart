# S9 — QA (đợt 1: mốc + công cụ kiểm chức năng)

Đọc trước: `common.md`, spec §13c (chính sách test), §15 (tiêu chí nghiệm thu).
Mục tiêu đợt này: có **mốc cố định của bản cũ** và **bộ luồng chức năng chạy được trên cả bản cũ lẫn bản mới**, để
nghiệm thu phase 1–4 chứng minh được "không mất chức năng".

## Phạm vi
- Ghi:
  - `tests/ui-flows/**` (thư mục mới);
  - `.runs/shadcn-redesign/p1-4/qa/**`, `.runs/shadcn-redesign/p1-4/baseline/**`.
- Chỉ đọc ở đợt này: `web/scripts/*.test.mjs` (đợt sau mới viết lại), toàn bộ `web/src/**`.
- Không chạy `make e2e-web`, không rebuild container, không đổi mẫu đã xuất bản của 4 site seed.

## Việc

### 1. Danh sách route
`tests/ui-flows/routes/{seller,admin,platform,site}.txt`, mỗi dòng một path, dùng được với
`node .agents/tools/ui-shots.mjs --routes-file`.
- **seller:** đủ 16 trang `/seller/**` (lấy id đơn, id sản phẩm thật từ dữ liệu), cùng `/seller/site/xem-truoc`,
  `/seller/site/ten-mien`.
- **admin:** đủ 9 trang `/admin/**`.
- **platform:** 7 trang host gốc.
- **site:** đủ 21 trang của `(site)/s/[host]`.
  - Trang cần đăng nhập (giỏ, thanh toán, đơn, tài khoản) chụp với `--login buyer`.
  - Có thêm `tai-khoan?tab=…` cho 4 tab.

### 2. Kiểm kê test bám giao diện cũ
Ghi `.runs/shadcn-redesign/p1-4/qa/test-inventory.md`:
- **Nguồn:** mọi assertion trong `web/scripts/*.test.mjs` và `web/src/**/*.test.mjs` có một trong ba dạng:
  - đọc `.module.css`;
  - grep tên class / `styles.x`;
  - grep tên component cũ (`AdminDrawer`, `StateView`, `shared.mobileList`…).
- **Mỗi dòng:** `file:dòng`, swarm sở hữu mã bị kiểm (theo bảng `common.md` §3), **bất biến** mà assertion bảo vệ, và cách
  kiểm lại bất biến đó trên mã mới (đọc nguồn hay Playwright).
- Nhóm theo swarm.

### 3. Luồng chức năng
Viết `tests/ui-flows/run.mjs --port <n> --area seller|admin|platform|site [--only <tên luồng>]`:
- In `PASS` / `FAIL <lý do>` từng luồng; exit 1 nếu có `FAIL`.
- Playwright ở `/home/tai/.npm/_npx/9833c18b2d85bc59/node_modules/playwright/index.mjs`, kênh `chrome`. Tài khoản dev và
  host như `common.md` §4.

**Locator**
- Chỉ dùng **vai trò + tên hiển thị tiếng Việt** (`getByRole`, `getByLabel`, `getByText`). Không dùng class, `data-*`
  hay cấu trúc DOM.
- Như vậy luồng chạy được cả trên giao diện mới, vì swarm khu vực phải giữ nguyên chữ.

**Mỗi luồng:** mở trang → thao tác → kiểm kết quả nhìn thấy được. Tối thiểu:
- **seller:**
  - lọc đơn theo các nhóm;
  - mở xem nhanh đơn;
  - chi tiết đơn;
  - tìm / lọc sản phẩm "sắp hết";
  - sửa tồn một SKU trong bảng rồi lưu, sau đó trả lại số cũ;
  - tạo sản phẩm nháp `QA-S9-…`;
  - mở khuyến mãi qua từng tab;
  - tài chính: mở tab sổ cái;
  - tương tác: đổi tab;
  - Site Studio: mở và đổi tab / chế độ, không lưu;
  - hồ sơ: mở;
  - ⌘K tìm được một trang.
- **admin:**
  - lọc gian hàng, mở chi tiết, mở hộp xác nhận rồi huỷ;
  - kiểm duyệt: đổi tab;
  - cấu hình: đi qua 7 tab;
  - đối soát: mở tab;
  - audit log: lọc;
  - flash sale: mở form.
- **platform:**
  - đăng nhập đúng / sai;
  - đăng ký tới bước OTP (Mailpit `http://127.0.0.1:48025`);
  - quên mật khẩu;
  - 2 redirect 308;
  - mở hỏi đáp ở `/tro-giup`.
- **site** (chạy cho cả 4 site seed):
  - xem danh mục, lọc / sắp xếp / phân trang;
  - tìm kiếm;
  - trang sản phẩm: chọn biến thể → thêm vào giỏ → thấy drawer xác nhận;
  - giỏ: tăng / giảm / xoá;
  - thanh toán COD với địa chỉ thật (tỉnh / phường) → thấy đơn đã tạo;
  - xem đơn trong `/don-hang`;
  - tra cứu đơn;
  - tài khoản: đổi 4 tab;
  - voucher / flash sale: mở.

**Yêu cầu**
- **Chạy xanh trên bản cũ ở cổng 43101.**
  - Luồng nào không thể xanh trên bản cũ (thiếu dữ liệu, lỗi sẵn có) thì đánh dấu `KNOWN` kèm lý do, không bỏ.
  - Ghi kết quả vào `.runs/shadcn-redesign/p1-4/qa/flows-baseline.txt`.
- Dữ liệu tạo ra đặt tên `QA-S9-…`. Luồng chạy lại nhiều lần không hỏng: kiểm tồn trước khi tạo, dọn sau khi chạy nếu
  giao diện có nút xoá.

### 4. Ảnh mốc của bản cũ
Chạy `ui-shots` trên cổng **43101** cho 4 danh sách route:
- ảnh fullPage vào `.runs/shadcn-redesign/p1-4/baseline/<area>/full/`;
- ảnh `--viewport` vào `.runs/shadcn-redesign/p1-4/baseline/<area>/viewport/`;
- site: chạy cho cả 4 site seed;
- trang chủ host gốc: thêm ảnh viewport sau khi cuộn tới footer.

## Xong khi
- 4 file route.
- `test-inventory.md`.
- `run.mjs` cùng luồng cho 4 khu vực, `flows-baseline.txt` (số PASS / FAIL / KNOWN mỗi khu vực).
- Ảnh mốc.
- `npx eslint tests/ui-flows` (nếu cấu hình eslint không phủ `tests/` thì ghi rõ) và
  `node --check tests/ui-flows/*.mjs` sạch.
- Báo cáo theo `common.md` §5, thêm: thời gian chạy mỗi khu vực, và luồng nào phụ thuộc dữ liệu có thể đổi.
