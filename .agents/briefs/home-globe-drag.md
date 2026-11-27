# Quả cầu trang chủ: kéo để xoay như trang mẫu

Chủ dự án (2026-11-27), gửi kèm ảnh khối quả cầu "Vững khi đông khách. Chính xác từng đơn." trên nexusmartai.store:
"Trên trang chủ chưa tương tác như trang mẫu được." Trang mẫu là shopify.com/vn (khối "Hoạt động ổn định. Tốc độ chớp nhoáng.").

Đọc trước: `AGENTS.md`, `web/CLAUDE.md`, brief gốc của quả cầu `.agents/briefs/home-shopify-v2-globe.md`, rồi đọc hết
`web/src/features/platform/PlatformGlobe.tsx` và `web/src/features/platform/globeRenderer.ts`.

## 1. Trang mẫu làm gì (Claude đã đo bằng Playwright)

Ảnh ở `.agents/briefs/home-shopify-v2/ref/drag/`:
- `t0.jpg` → `t3-idle.jpg`: lúc không ai chạm, quả cầu tự trôi rất chậm.
- Rê chuột lên canvas: con trỏ `grab`. Canvas có `pointer-events: auto`, `touch-action: none`.
- `t-drag.jpg`: nhấn giữa quả cầu rồi kéo sang phải 400px: quả cầu xoay theo tay, từ Bắc Mỹ sang Thái Bình Dương/Nhật Bản.
- `t-release150.jpg`, `t-release1650.jpg`: thả ra thì vẫn trôi tiếp.

Bản của mình: canvas và lớp phủ đều `pointer-events-none`. Lục địa, ánh đèn đô thị (Pass C), khí quyển đều vẽ **một lần** vào
`offscreen` trong `renderStaticBackdrop`, mỗi khung chỉ vẽ sao, thành phố, vòng cung. Vì thế không xoay được.

## 2. Hành vi cần làm (Claude đã chốt; khác trang mẫu ở chỗ quay về Việt Nam, có lý do)

1. **Kéo để xoay.** Chuột/bút: kéo ngang đổi kinh độ, kéo dọc đổi vĩ độ. Kinh độ tự do, vĩ độ tâm nhìn kẹp trong khoảng
   ±20° quanh vĩ độ nghỉ hiện tại (13.5°N). Quả cầu bám tay: điểm dưới con trỏ đi theo con trỏ, gần đúng là
   `độ xoay = khoảng kéo / bán kính`. Không lật cực, không đảo trục.
2. **Quán tính ngắn khi thả.** Vận tốc lúc thả lấy trung bình trên ~80ms cuối, giảm dần theo hàm mũ, dừng hẳn trong ~1s.
3. **Tự quay về.** Không chạm ~2.5s (tính từ lúc quán tính dừng) thì êm về đúng góc nhìn nghỉ hiện tại (`CENTER_LAT_DEG`,
   `CENTER_LON_DEG`, `AXIAL_ROLL_DEG`) trong ~1.6s, đi đường kinh độ ngắn nhất, dùng `easeInOutCubic` có sẵn. Đang quay về mà
   người dùng nhấn lại thì dừng ngay và bám tay từ góc hiện tại, không giật.
   Lý do không xoay liên tục như Shopify: đèn 34 tỉnh, vòng cung nội địa, vòng cung TP.HCM và thẻ "34 tỉnh thành" chỉ có nghĩa
   khi Việt Nam ở mặt trước.
4. **Lúc nghỉ trông như hiện nay.** Ở góc nhìn nghỉ, hình phải gần như bản đang chạy (cùng tâm, cùng ánh sáng, cùng vòng cung).
   Sao, đèn nhấp nháy và vòng cung vẫn chạy như cũ.
5. **Ánh sáng cố định theo màn hình.** Ánh sáng, khí quyển, viền sáng, vệt hoàng hôn và lớp làm mờ đáy đứng yên; chỉ lục địa,
   đèn đô thị, thành phố và vòng cung xoay theo quả cầu. Mặt trời vẫn ở trên bên trái, phần lục địa xoay sang phải thì tối dần
   như thật.
6. **Thành phố và vòng cung xoay theo.** Điểm nào ra sau quả cầu thì ẩn như cách đang cull theo `z`. Vòng cung nội địa và vòng
   cung lớn từ TP.HCM đi theo TP.HCM. Đoạn vòng cung ra sau quả cầu thì ẩn đúng như logic `visible` hiện có.
7. **Con trỏ và chọn chữ.** Con trỏ `grab` khi rê trên canvas, `grabbing` khi đang kéo. Đang kéo thì không bôi chọn chữ trên trang
   (`user-select: none` chỉ trong lúc kéo, hoặc `preventDefault` ở `pointerdown` của chuột). Không bắt sự kiện `wheel`.
8. **Cảm ứng.** Dùng `touch-action: pan-y` (KHÔNG dùng `none` như Shopify: ở 390px quả cầu rộng hết màn, sẽ kẹt cuộn trang).
   Vuốt dọc trên quả cầu phải cuộn trang bình thường; vuốt ngang xoay quả cầu (chỉ kinh độ). Xử lý `pointercancel` khi trình
   duyệt giành cử chỉ để cuộn: coi như thả tay, không quán tính.
9. **Pointer capture.** `setPointerCapture` ở `pointerdown`, xử lý `pointerup`, `pointercancel`, `lostpointercapture`. Chỉ một
   con trỏ chính; con trỏ thứ hai bỏ qua. Chuột chỉ nhận nút trái.
10. **Giảm chuyển động** (`prefers-reduced-motion: reduce`): không quán tính, không tự quay về, không có vòng lặp chạy khi rảnh
    (giữ như hiện nay: vẽ một khung tĩnh). Kéo vẫn xoay được, chỉ vẽ lại khi có `pointermove`. Thả ra thì đứng yên ở góc đó.
    Hiện `shouldAnimate()` trả false khi giảm chuyển động, nên đừng để kéo bị đứng hình.
11. **Thẻ chữ** (`34 TỈNH THÀNH`, `COD VÀ VIETQR`) giữ nguyên chỗ và nội dung như hiện nay. Lớp `ul` vẫn `pointer-events-none`,
    thẻ vẫn `pointer-events-auto` như cũ. Quả cầu dự phòng bằng CSS giữ nguyên.
12. Khi khung ra khỏi màn hình hoặc tab ẩn: dừng vòng lặp như hiện nay; đang kéo dở mà mất capture thì coi như thả.

## 3. Cách vẽ để vẫn mượt (bắt buộc làm theo hướng này)

Vẽ lại lục địa mỗi khung bằng cách hiện tại (≈8.264 chấm Pass A + ≈24.800 chấm Pass B, mỗi chấm một `fillStyle` chuỗi, một
`arc`, một `fill`; Pass C mỗi tia một `createRadialGradient`) sẽ quá nặng. Làm thế này:

1. **Tách offscreen theo cái gì phụ thuộc góc xoay.** Cache một lần cho mỗi kích thước: lớp dưới (quầng ngoài, đại dương,
   `nightTint`), lớp phủ A (`terminatorVeil`, `sunHaze`), lớp phủ B (fresnel, viền sáng, vệt hoàng hôn). Mỗi khung khi góc thay
   đổi: lớp dưới → lục địa → lớp phủ A → đèn đô thị Pass C → lớp phủ B → làm mờ đáy `destination-out` → sao, thành phố, vòng cung.
   Giữ đúng thứ tự hiện nay để góc nghỉ ra hình như cũ.
2. **Tính trước một lần, mỗi khung chỉ xoay.** Lúc nạp dữ liệu (không phụ thuộc kích thước), tính và cất vào `Float32Array`:
   - vector đơn vị 3D của từng chấm lục địa **sau khi đã jitter** (Pass A và 3 chấm con Pass B), cùng các hệ số chỉ phụ thuộc
     lat/lon (`sizeSeed`, `biomeNoise`, `terrainNoise`);
   - vị trí các tia sáng Pass C (`mulberry32` có seed nên tính trước được) và cường độ gốc của chúng;
   - thành phố, điểm mẫu vòng cung nội địa và đường Bézier vòng cung lớn (đang tính trong không gian nhìn ở góc nghỉ: đổi về
     không gian quả cầu một lần, rồi mỗi khung xoay lại).
   Mỗi khung: tính một ma trận xoay 3×3 từ góc hiện tại, nhân cho từng vector, cull theo `z`, tính sáng với `LIGHT_DIR` cố định,
   chiếu ra màn hình. Không gọi `Math.sin`/`Math.cos` cho từng chấm trong khung.
3. **Gộp lệnh vẽ.** Lượng tử màu và alpha thành khoảng 32–64 nhóm. Mỗi nhóm một `Path2D` (hoặc một `beginPath`), thêm mọi chấm của
   nhóm rồi `fill` một lần. Chấm nhỏ Pass B vẽ bằng `rect` thay `arc`. Tia sáng Pass C dùng một sprite quầng sáng vẽ sẵn
   (canvas nhỏ) + `drawImage` với `globalAlpha`, thay cho `createRadialGradient` từng tia.
4. **Không đổi góc thì không vẽ lại lục địa.** Lúc nghỉ, chi phí phải như hiện nay: ghép sẵn quả cầu vào một offscreen, mỗi khung
   chỉ `drawImage` rồi vẽ sao, thành phố, vòng cung. Chỉ vẽ lại lục địa khi góc đổi (đang kéo, quán tính, quay về).
5. Gộp nhóm sẽ làm các chấm Pass A chồng nhau hoà lẫn thay vì cộng alpha, nên góc nghỉ sẽ lệch nhẹ. Đặt ảnh góc nghỉ cạnh bản
   đang chạy và chỉnh alpha tới khi gần giống. Không cần trùng từng pixel trong vùng quả cầu.
6. **Ngân sách gói JS.** Logic pointer, quán tính, quay về đặt trong phần được `import()` động (`globeRenderer.ts` hoặc file mới
   nó import). `PlatformGlobe.tsx` chỉ thêm phần tối thiểu (bật pointer cho canvas, lớp con trỏ). First Load `/` hiện khoảng
   124 kB, trần 131 kB.

Phần toán thuần (kẹp vĩ độ, quán tính, quay về theo đường ngắn nhất, ma trận xoay) đặt trong một file thuần mới, ví dụ
`web/src/features/platform/globeMotion.ts`, **không import gì** (Node chạy test bằng type stripping, import không đuôi `.ts`
sẽ hỏng). Thêm vài test nhỏ vào file test **mới** `web/src/features/platform/globeMotion.test.mjs` (`npm run test:unit` tự quét
`src/**/*.test.mjs`; KHÔNG sửa `platform.test.mjs` vì phiên khác đang sửa): vĩ độ bị kẹp; quán tính về 0; quay về đi đường
ngắn nhất (ví dụ từ kinh độ lệch +200° về theo hướng −160°) và kết thúc đúng góc nghỉ; góc nghỉ cho ra đúng ma trận đang dùng
(`latLonToView(13.5, 106)` ra xấp xỉ `(0, 0, 1)`).

## 4. Phạm vi

Được sửa:
- `web/src/features/platform/PlatformGlobe.tsx`
- `web/src/features/platform/globeRenderer.ts`
- `web/src/features/platform/globeData.ts` (chỉ khi thật cần)
- file thuần mới trong `web/src/features/platform/` (ví dụ `globeMotion.ts`)
- file test mới `web/src/features/platform/globeMotion.test.mjs`
- trang thử và ảnh trong `.agents/briefs/home-shopify-v2/ref/agy-globe/` (đã gitignore; có sẵn `preview.html` và `dist/`)

**Có một agy khác đang chạy song song trên cùng working tree** (làm trang `/cua-hang`). Nó sở hữu `PlatformHeader.tsx`,
`PlatformFooter.tsx`, `SiteDirectory*.tsx`, `platformStyles.ts`, `platform.test.mjs`, `features/platform/auth/*`,
`app/(shop)/**`, `tests/ui-flows/**` và file mới nó tạo. Không sửa, không hoàn nguyên các file đó dù thấy chúng đang đổi. Nếu
lint/test/build đỏ ở file của nó thì ghi lại trong câu trả lời, không tự sửa. Đặt tên mọi file mới của bạn bắt đầu bằng `globe`.

Cấm: mọi file khác, nhất là `PlatformHome.tsx`, `platform.test.mjs`, token `home-tokens.css`, `web/tsconfig.json`, `web/next-env.d.ts`, thư mục ngoài
`web/`. Không thêm phụ thuộc npm, không three.js/WebGL, giữ Canvas 2D. Không git ghi (add, commit, stash, checkout, reset).
Không chạy `make up|seed|e2e*`, không đụng cổng 3111, 4777, 43000, 43101, 43110 và stack production `~/nexusmart-prod`.
Server thử của bạn dùng cổng 43120–43129.

## 5. Tiêu chí nghiệm thu (tự kiểm, ghi số đo vào câu trả lời)

1. **Kéo bằng chuột ở 1440:** con trỏ `grab` trên canvas, `grabbing` khi kéo; kéo ngang 300px thì quả cầu xoay rõ (lục địa đổi
   chỗ), kéo dọc thì nghiêng nhưng không vượt kẹp; thả ra có trôi ngắn; sau 6s từ lúc thả (quán tính ~1s + chờ 2.5s + quay về 1.6s), ảnh vùng quả cầu khớp ảnh góc nghỉ
   (so với ảnh chụp trước khi kéo, cùng thời điểm hoạt ảnh, hoặc chụp với reduced motion bằng cách gọi trực tiếp).
2. **Cảm ứng ở 390** (`hasTouch: true`, `isMobile: true`, dùng CDP `Input.dispatchTouchEvent`): vuốt dọc trên quả cầu thì
   `scrollY` đổi; vuốt ngang thì pixel quả cầu đổi và `scrollY` không đổi.
3. **Khung hình khi kéo:** kéo liên tục 2s (`page.mouse` nhiều bước), đo khoảng cách `requestAnimationFrame` ngay trong trang và
   thời gian của hàm vẽ một khung. Đo ở 1440 dpr 2 và 390 dpr 2, mỗi khổ một lần không bóp CPU và một lần bóp CPU 4×
   (CDP `Emulation.setCPUThrottlingRate`). Cần p95 khoảng cách khung ≤ 16.7ms khi không bóp, ≤ 33ms khi bóp 4×. Ghi bảng
   p50/p95 khoảng cách khung và thời gian vẽ, so với thời gian `renderStaticBackdrop` cũ cho một khung.
4. **Lúc nghỉ không tốn hơn:** không kéo thì thời gian vẽ một khung tương đương bản cũ (ghi số).
5. **Giảm chuyển động:** `emulateMedia({ reducedMotion: "reduce" })`: rảnh thì không có rAF chạy; kéo vẫn xoay; thả ra đứng yên.
6. **Trang thật:** chạy bản build production của `web/` (build qua khoá chung:
   `flock /tmp/nm-next-build.lock env NEXT_DIST_DIR=.next-globe npm run build`, rồi
   `NEXT_DIST_DIR=.next-globe npx next start -p 43121`; hoàn nguyên `web/tsconfig.json` nếu build tự sửa nó; dọn `.next-globe`
   khi xong): `/` không lỗi console, canvas lên `opacity-100`, không lỗi hydrate, không tràn ngang ở 390/768/1440, kéo được trên
   trang thật. Ghi First Load JS của `/` từ log build.
7. **Gate xanh**, chạy trong `web/`: `npx eslint src/features/platform`, `npm run format:check`, `npm run test:unit`,
   `npm run check:data`, và build ở mục 6 (không chạy thêm `npm run build` vào `.next` mặc định).

Câu trả lời cuối: danh sách file đã sửa, bảng số đo mục 3–4, đường dẫn ảnh trước/sau (góc nghỉ cũ, góc nghỉ mới, đang kéo,
390), kết quả từng tiêu chí, phần chưa đạt nói thẳng.
