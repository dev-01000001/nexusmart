# Phase 5 (dọn dẹp) — luật chung, đọc trước brief riêng

Phase 1–4 đã nghiệm thu (59/59 luồng + 8 mẫu × COD trên bản production). Phase 5 theo spec `../design.md` §14 dòng 5:
bật preflight, xoá token cũ, `globals.css` cũ, CSS Module, component cũ, Archivo, trang `/design`. **Mục tiêu số một:
không đổi hình.** Mọi trang phải giống bản đã nghiệm thu.

## 1. Áp dụng lại từ phase 1–4
`../p1-4/common.md` §1 (không thêm phụ thuộc, không đổi logic, giữ chữ hiển thị, import từng file), §2 (luật working tree, cấm git
ghi, chỉ ghi file trong phạm vi), §4 (lệnh kiểm, `flock`, tài khoản dev, không tự chạy hay khởi động lại server), §5 (progress,
báo cáo cuối). Thư mục chạy của phase này: `.runs/shadcn-redesign/p5/<swarm>/`, tín hiệu ở `.runs/shadcn-redesign/p5/signals/`.

Khác phase 1–4:
- **Test:** swarm được sửa đúng các test liệt kê trong brief riêng, theo luật S9b: viết lại để kiểm cùng bất biến trên mã mới,
  không xoá assertion, không `skip`. Test chỉ kiểm module bị xoá thì xoá cùng module, ghi rõ trong báo cáo.
- **Xanh toàn app:** phase này xoá file dùng chung, nên `tsc` phải 0 lỗi trên **toàn bộ** `web/src` (bỏ qua `.next*/types`),
  `npm run lint` 0, `npm run test:unit` 0 fail (hoặc chỉ còn lỗi của swarm kia, ghi rõ).

## 2. Server và mốc so sánh
- Dev chung (working tree hiện tại): `http://nexusmart.localhost:43110`.
- **Mốc phase 5** (production, HEAD đã nghiệm thu phase 1–4, đóng băng): `http://nexusmart.localhost:43102`, site
  `http://<sub>.nexusmart.localhost:43102`. Mọi trang so với mốc này.
- Trang chủ `/` so thêm với **43101** (production `c265058`, bản gốc trước redesign), như S3b.
- Hai mốc đọc cùng dữ liệu live với 43110. Chụp hai cổng liền nhau để dữ liệu không trôi.

## 3. So pixel (bắt buộc sau mỗi bước có đổi CSS)
```bash
cd /home/tai/Document/nexusmart
D=.runs/shadcn-redesign/p5/<swarm>/diff-<bước>
for P in 43102 43110; do
  flock /tmp/nm-web-browser.lock node .agents/tools/ui-shots.mjs --port $P --login seller \
    --routes-file tests/ui-flows/routes/seller.txt --widths 390,1440 --out $D/seller-$P
done
MASK="0:ih-72:72:72" .agents/tools/ui-diff.sh $D/seller-43102 $D/seller-43110 $D/seller-diff
```
- Làm cho đủ: `platform.txt` (`--login none`), `admin.txt` (`--login admin`), `seller.txt` (`--login seller`), `site.txt` với
  `--site bach-dang` và `--site sai-gon` (`--login buyer`), `site-ha-noi.txt` qua `tests/ui-flows/extra-shots.mjs --mode site-custom`.
- `MASK` che huy hiệu dev của Next (góc dưới trái 72×72) ở cả hai ảnh.
- `ui-shots.mjs` tự chờ 60 giây khi gặp 429; vẫn còn `still rate-limited` thì chạy lại riêng route đó.
- Đọc `report.txt` của `ui-diff.sh`: `SAME` là đạt. Mỗi dòng `DIFF`/`SIZE` phải **mở ảnh diff ra xem** và xếp loại:
  - (a) dữ liệu live đổi giữa hai lần chụp (giờ, số đếm): ghi lại, không sửa;
  - (b) lệch ≤ 6/255 chỉ trên gradient/màu trong suốt: do Lightning CSS chỉ chạy ở bản production (mốc là production, 43110 là dev),
    ghi lại, không sửa;
  - (c) còn lại là **hồi quy**: sửa, chụp lại.
- Trang chủ: `node .agents/tools/scroll-shots.mjs <dir>/p43101 43101` và `<dir>/p43110 43110`, rồi `ui-diff.sh` với `MASK`.
- Trạng thái mở (⌘K, Sheet, Dialog, Popover, toast), vòng focus khi bấm Tab, chuyển động Reveal khi không reduce-motion:
  dùng `tests/ui-flows/states.mjs` khi S9 báo `signals/s9d.done` (cách chạy trong báo cáo của S9).
- Luồng: `node tests/ui-flows/run.mjs --port 43110 --area <platform|admin|seller|site>` phải xanh như trước.

## 4. Báo cáo cuối (thêm vào `../p1-4/common.md` §5)
Bảng diff từng khu: số `SAME` / `DIFF` / `SIZE`, mỗi `DIFF` kèm loại (a)/(b)/(c) và đường dẫn ảnh. Không có (c) mới được báo xong.
