# Swarm brief – NexusMart UI đợt 3: hết "sơ sài"

Bạn là coordinator của swarm. Claude Code là manager và sẽ review bằng skill design-taste-frontend. Đọc `AGENTS.md`, `.taste-skill/swarm-brief.md`, `.taste-skill/features-brief.md` (mọi ràng buộc cũ vẫn giữ) và `web/DESIGN_REPORT.md` trước.

## Vấn đề
Chủ dự án xem `web/` và nhận xét: nhiều chỗ rất sơ sài. Manager đã chạy inspector trên 44 route và xem ảnh chụp trong `.taste-skill/review4/<route>/` (mở ảnh `desktop-page-01.jpg`, `mobile-page-01.jpg` của từng route để tự thấy). Trang đạt máy đo nhưng trông như wireframe. Nguyên nhân gốc, theo thứ tự ưu tiên:

### A. Trang chủ không phải trang chủ của một cửa hàng (`/`)
1. Sau hero không có sản phẩm nào kèm ảnh lớn, tên, giá trong 2 màn hình đầu. "Bảng giá xưởng hôm nay" là danh sách chữ trong một cột hẹp giữa trang, hai bên trống khoảng 60% bề ngang.
2. Ảnh con đường tuyết (khối "Giao 34 tỉnh") không liên quan đến đồ basic Việt Nam. Thay bằng ảnh đúng chủ đề.
3. Thiếu các khối một cửa hàng thời trang phải có: ô danh mục có ảnh (áo thun, polo, short, jogger, đồ lót, tất, áo khoác), dải flash sale có đếm ngược và thẻ sản phẩm, hàng bán chạy, hàng mới về, banner chiến dịch/bộ sưu tập, dải gian hàng nổi bật, khối hội viên, bài tạp chí, dải cam kết (đổi trả, freeship, COD).
4. "Quy chuẩn dệt may" và "Hệ sinh thái xưởng" là các cột chữ xám. Gộp, rút gọn, hoặc gắn với ảnh.
Giữ ý tưởng contact-sheet của direction sheet cho hero và nhịp lưới, nhưng mỗi màn hình desktop phải có một thứ chủ đạo và nội dung thật. Không màn nào gần như trống.

### B. Thiếu ảnh người mặc và ảnh không khí (toàn site)
Mọi ảnh sản phẩm là flat-lay trên cùng một nền xám nhạt, nên trang nào cũng giống nhau và lạnh. Dùng công cụ tạo ảnh để sinh:
- Ảnh người mẫu Việt Nam mặc từng nhóm sản phẩm (ít nhất 12 ảnh: nam, nữ, bối cảnh đời thường, văn phòng, chạy bộ, ở nhà), ánh sáng nhất quán, không logo thật.
- 4–6 ảnh banner ngang cho chiến dịch và bộ sưu tập; ảnh cận chất liệu vải; ảnh xưởng dệt.
- Ảnh thứ hai (người mặc) cho mỗi sản phẩm: dùng làm ảnh hover trên ProductCard và trong gallery trang sản phẩm.
Lưu vào `web/public/`, dùng `next/image`, có `alt`. Bỏ ảnh con đường tuyết và ảnh ngoài không liên quan.

### C. Header và footer quá mỏng (toàn site, `(shop)` layout)
- Footer hiện là hai dòng chữ nhỏ. Làm footer đầy đủ: các cột Hỗ trợ khách hàng, Chính sách, Về NexusMart, Bán hàng cùng NexusMart, liên hệ (hotline, email, giờ làm việc), phương thức thanh toán (COD, VietQR, ví – chữ, không logo thương hiệu thật), đơn vị vận chuyển (chữ), đăng ký nhận tin, dòng pháp lý mẫu.
- Header: menu "Danh mục" phải là mega menu có nhóm, mục con và một ảnh; ô tìm kiếm rộng hơn và có gợi ý; logo và hành động (tài khoản, yêu thích, giỏ) rõ hơn. Điện thoại: thanh điều hướng đáy hoặc drawer đầy đủ.

### D. Mọi thứ là hộp xám trên nền xám nhạt (toàn site)
Giỏ hàng, thanh toán, tài khoản, trợ giúp, hội viên: hộp lồng trong hộp, cùng một độ xám, không có thứ bậc. Sửa bằng thứ bậc rõ: một bề mặt chính, phần phụ không đóng hộp (dùng khoảng trắng và một đường kẻ), giá và tổng tiền lớn hơn, dùng màu accent và các field màu của palette cho các khối quan trọng (ưu đãi, hạng hội viên, flash sale). Vẫn giữ house rules (không gradient, không shadow lúc nghỉ, không icon card).

### E. Chữ trên UI người mua là thuật ngữ kỹ thuật
Người mua đang đọc đặc tả. Viết lại cho người mua, ví dụ:
- "Người nhận & Địa chỉ 2 cấp", "sổ địa chỉ 2 cấp 34 tỉnh" → "Địa chỉ nhận hàng".
- "ghi sổ cái ghi kép", "khóa phiên IDEM-HCM-3-570200", "Hoạt động 24/7 theo phiên JWT", "tồn kho theo mã SKU", "Sandbox", "đối soát tự động theo đúng số tiền nguyên VND" → bỏ hoặc nói bằng lợi ích cho người mua.
- "Giả lập gián đoạn cổng thanh toán", "dữ liệu mẫu minh họa giao diện trước khi kết nối đơn hàng thật" → không hiện trên UI; nếu cần công tắc demo thì đặt trong một khối "Chế độ demo" gọn, tách khỏi luồng mua.
- Tiêu đề trang ngắn, không nhồi tính năng ("Gom đơn theo gian hàng, tự động trừ giảm tầng mua nhiều, theo dõi mốc freeship…").
Rà toàn bộ `(shop)`. Seller và admin được dùng thuật ngữ nghiệp vụ, nhưng không dùng tên kỹ thuật nội bộ.

### F. Lỗi bố cục cụ thể
- `/dang-nhap`, `/dang-ky`: khoảng trống khoảng 300px giữa tiêu đề và form; chữ 14px; đoạn văn 92–97 ký tự một dòng ở 768px (inspector FAIL `body-size`, WARN `measure`, `grey-body`). Form phải nằm ngay dưới tiêu đề, trong màn hình đầu.
- `/flash-sale`, `/danh-muc`, `/tim-kiem`: hàng cuối của lưới bị mồ côi (1 thẻ trên lưới 3 cột, 2 thẻ trên lưới 3 cột). Dùng lưới 4 cột ở desktop và số thẻ ban đầu chia hết, hoặc lấp hàng cuối.
- `/danh-muc`: cột bộ lọc dài hơn nhiều so với lưới 8 sản phẩm. Thu gọn nhóm lọc phụ mặc định, hiện 12–16 sản phẩm đầu, cột lọc sticky.
- ProductCard: thêm ảnh hover người mặc, chấm màu, nhãn giảm giá rõ, điểm đánh giá mẫu (nằm trong danh sách nội dung mẫu), nút thêm nhanh theo size khi hover.
- `chien-dich/*`, `don-hang/*`, `hoi-vien`, `the-qua-tang`, `tap-chi`, `tro-giup`, `tra-cuu-don*`, `ve-nexusmart`, `tai-khoan`, `gio-hang`, `thanh-toan`: sửa các WARN `card-rows`, `grey-body` (chữ chạy phải dùng ink đầy đủ, tương phản ≥ 7:1), `body-size` (chữ chạy ≥ 16px) trong `.taste-skill/review4/<route>.txt`.
- `/tra-cuu-don` và `/tra-cuu-don-hang` là hai route trùng chức năng: giữ một, route kia redirect.

### G. Seller center và admin
Hai khu này đạt (0 FAIL, gần như 0 WARN) và đủ dày. Chỉ sửa 4 WARN còn lại (`seller`, `seller/khuyen-mai`, `seller/san-pham/moi`, `seller/san-pham/[id]`) và rà chữ theo mục E. Không thiết kế lại.

## Ràng buộc
- Giữ art direction, palette, typeface, house rules. Không thêm dependency. Tái dùng component có sẵn; sửa component dùng chung một lần thay vì vá từng trang.
- Chỉ sao chép chức năng của Coolmate, không tên, logo, câu chữ, ảnh, bố cục.
- Không bịa logo đối tác, báo chí, số liệu kinh doanh. Nội dung mẫu liệt kê trong báo cáo.
- Chỉ sửa trong `web/`. Không chạm `services/`, `proto/`, `docs/`, `tools/`.
- Một server của manager đang chạy ở cổng 3111: không kill nó; dùng cổng khác (ví dụ 3120) cho kiểm tra của bạn và tắt server của bạn khi xong.

## Chia việc gợi ý
1. Ảnh (mục B) và component dùng chung (header, footer, ProductCard, tokens bề mặt) làm trước.
2. Song song: trang chủ; danh mục + tìm kiếm + flash sale + bộ sưu tập + chiến dịch; giỏ + thanh toán + đơn hàng + tra cứu; tài khoản + hội viên + thẻ quà tặng + đăng nhập/đăng ký; trợ giúp + tạp chí + về NexusMart; rà chữ mục E.
3. Một worker QA chạy gate và tự mở ảnh chụp, gửi lỗi về đúng worker.

## Gate trước khi báo xong
- `cd web && npm run lint && npm run format:check && node scripts/check-data.mjs && npm run build` sạch.
- `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs web/src` sạch.
- `inspect.mjs` 0 FAIL ở 1440, 768, 390 cho mọi route `(shop)`; số WARN giảm so với `.taste-skill/review4/summary.txt`, mỗi WARN còn lại có lý do trong báo cáo. `--app` cho seller và admin: 0 FAIL.
- Mở ảnh chụp desktop và mobile của trang chủ, danh mục, sản phẩm, giỏ, thanh toán, tài khoản, đăng nhập: không màn hình nào gần trống, không hàng lưới mồ côi, không thuật ngữ kỹ thuật.
- `grep -riE "sổ cái|JWT|IDEM-|sandbox|2 cấp|ghi kép" web/src/app/\(shop\) web/src/components` chỉ còn kết quả có lý do.
- Cập nhật `web/DESIGN_REPORT.md`: trước và sau cho từng mục A–G, danh sách ảnh đã tạo, nội dung mẫu mới, phần chưa làm kèm lý do.
