# S9d — QA phase 5: chụp trạng thái mở, vòng focus, chuyển động (để so pixel khi bật preflight)

Đọc trước: `common.md` (phase 5), `tests/ui-flows/lib.mjs`, `.agents/tools/ui-shots.mjs`, `.agents/tools/ui-diff.sh`.
Vì sao: `ui-shots.mjs` chỉ chụp trang ở trạng thái đóng, reduce-motion. Bật preflight dễ làm hỏng đúng ba thứ nó không thấy:
overlay của Radix (Dialog, Sheet, Popover, ⌘K, DropdownMenu, Tooltip, toast Sonner), vòng focus khi bấm Tab, và chuyển động Reveal
khi người dùng không bật reduce-motion.

## Việc
Viết `tests/ui-flows/states.mjs`: `node tests/ui-flows/states.mjs --port <p> --out <dir> [--only <pattern>]`. Mỗi ảnh có tên cố định
giống nhau ở mọi cổng (để `ui-diff.sh <dir-43102> <dir-43110>` so được), ảnh **viewport** (không `fullPage`), `caret: "initial"`,
đợi animation mở xong (`waitForTimeout` ~400 ms sau khi phần tử visible). Bọc chạy bằng `flock /tmp/nm-web-browser.lock` +
`systemd-run --user --scope --slice=nm3b.slice -p MemoryMax=2G`. Dùng lại login của `lib.mjs`.

Danh sách tối thiểu (thiếu phần tử thì ghi `SKIP <tên>: lý do` vào report, không dừng):
- **Người bán** (1440): ⌘K mở; menu tài khoản (DropdownMenu) mở; Sheet xem nhanh đơn ở `/seller/don-hang`; Tooltip khi sidebar thu gọn.
  390: Sheet menu điều hướng mở.
- **Quản trị** (1440): ⌘K mở; Sheet kiểm tra gian hàng ở `/admin/gian-hang`; một Dialog/AlertDialog xác nhận mở rồi đóng bằng Esc (không
  xác nhận). 390: Sheet menu.
- **Site** `bach-dang` (sáng) và `sai-gon` (tối): hộp tìm kiếm (CommandDialog) mở; 390 Sheet menu mobile; thêm 1 sản phẩm vào giỏ ⇒ chụp
  Sheet xác nhận giỏ + toast; Popover chọn tỉnh/phường mở ở `/thanh-toan`; rồi **xoá sản phẩm khỏi giỏ** để trả lại trạng thái. Không đặt hàng.
- **Nền tảng:** 390 Sheet menu của header; `/dang-nhap` focus vào ô email.
- **Vòng focus:** từ đầu trang bấm Tab 3 lần rồi chụp, ở `/seller`, `/admin`, trang chủ site `bach-dang`, `/dang-nhap` (host gốc).
- **Chuyển động (không reduce-motion):** trang chủ `/` 1440 và trang chủ site `bach-dang`, `da-nang`: cuộn tới 3 vị trí có Reveal, mỗi vị trí
  chụp 3 khung ở 0 / 0,5 / 1,2 giây sau khi cuộn. Ghi chú trong report là khung chuyển động nên lệch nhỏ giữa hai lần chạy.

## Kiểm độ ổn định
Chạy `states.mjs` **hai lần trên 43102** (mốc phase 5) vào hai thư mục, `ui-diff.sh` giữa chúng. Ảnh nào tự lệch (không phải ảnh chuyển động)
thì làm cho ổn định (đợi lâu hơn, che vùng giờ/số đếm bằng cách chờ dữ liệu, chọn phần tử khác). Mục tiêu: ảnh tĩnh `SAME` 100%.

## Phạm vi ghi
`tests/ui-flows/states.mjs`, nếu cần thêm hàm dùng chung thì thêm vào `tests/ui-flows/lib.mjs` (không đổi hàm cũ). Ảnh và báo cáo trong
`.runs/shadcn-redesign/p5/s9d/`. Cấm sửa `web/**`.

## Xong khi
- Hai lần chạy trên 43102: ảnh tĩnh `SAME` hết; liệt kê ảnh chuyển động và mức lệch tự nhiên của chúng.
- Một lần trên 43110 và `ui-diff.sh 43102 → 43110` (kèm `MASK="0:ih-72:72:72"`): bảng kết quả (P5a/P5b có thể đang sửa dở, chỉ để chạy thử).
- Ghi `.runs/shadcn-redesign/p5/signals/s9d.done` gồm đúng câu lệnh để swarm khác chạy lại, rồi báo cáo theo `../p1-4/common.md` §5.
