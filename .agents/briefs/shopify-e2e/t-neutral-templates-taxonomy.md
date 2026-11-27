# Swarm T — Phần F2 còn sót ở backend: chữ mẫu site + cây ngành hàng chung

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `.agents/briefs/shopify-e2e/w-web-fixes.md` (mục F2 — W đã sửa phần web).

## Vì sao (manager thấy trên Chrome thật, lượt r3a)
Xưởng gốm "Gốm Bát Tràng" chọn mẫu "Xưởng thủ công" → site hiện hero "Dệt tại xưởng, kiểm từng mẻ vải", "Cotton 220gsm…", ảnh xưởng vải.
Nguồn: `services/user/internal/site/templates_catalog.go` (8 mẫu `BuiltInTemplates()`). Và form đăng sản phẩm chỉ có ngành may mặc
(Áo thun, Áo polo, Quần short, Quần jogger, Đồ lót, Tất & phụ kiện…) vì seed (`tools/seed/seed.mjs` `buildCategorySeedPlan`, dữ liệu
`web/src/data/*`) chỉ tạo cây may mặc ⇒ shop ngành khác không có ngành phù hợp.

## Việc 1 — Chữ mặc định 8 mẫu site trung tính ngành hàng
- Trong `templates_catalog.go`: giữ tên mẫu, id/slug, bố cục, màu, font; thay mọi chữ chỉ đúng với may mặc (dệt, vải, cotton, gsm, size, form
  áo, mặc, giặt, co rút, đường may, mẻ vải, bộ sưu tập thời trang…) bằng câu trung tính dùng được cho mọi ngành (gốm, mỹ phẩm, đồ gỗ, thực
  phẩm khô, phụ kiện…). Ví dụ "Làm tại xưởng, kiểm từng mẻ hàng", "Chất liệu chọn lọc, hoàn thiện thủ công". Tiếng Việt tự nhiên, không thuật
  ngữ kỹ thuật.
- Ảnh mặc định: nếu `web/public/{banners,atmosphere,…}` có ảnh trung tính (xưởng chung, không gian, bàn làm việc, sản phẩm chung) thì dùng; nếu
  không có ảnh trung tính thì giữ ảnh hiện có (người bán nay tự tải ảnh hero/banner lên được) và ghi rõ trong báo cáo ảnh nào còn mang ngành
  may. KHÔNG thêm ảnh mới từ internet.
- Kiểm cách mẫu vào DB: nếu `BuiltInTemplates()` được đồng bộ vào bảng `site_templates` khi khởi động (upsert) thì sửa code là đủ; nếu mẫu được
  ghi bằng migration thì thêm migration mới forward-only theo kiểu đánh số của `user` (`0001_` …), KHÔNG sửa file cũ. Site đã áp mẫu trước đó giữ
  nguyên bản sao của mình (không động vào `sites`/`site_versions`).
- Test hiện có của template (grep `templates_catalog`, `BuiltInTemplates` trong `services/user`) phải xanh; thêm 1 test quét mọi chuỗi chữ của 8
  mẫu không chứa danh sách từ may mặc trên (không phân biệt hoa thường).

## Việc 2 — Cây ngành hàng chung trong seed
- `tools/seed/seed.mjs` (+ `seed.test.mjs`): ngoài cây may mặc hiện có, tạo thêm các ngành gốc phổ biến kiểu sàn VN, mỗi ngành vài ngành con, ví
  dụ: "Nhà cửa & Đời sống" (Gốm sứ & Đồ bếp, Trang trí nhà cửa, Đồ gỗ & Nội thất nhỏ), "Thủ công mỹ nghệ" (Gốm sứ mỹ nghệ, Mây tre đan, Quà tặng
  thủ công), "Sức khỏe & Làm đẹp", "Thực phẩm & Đồ uống" (Đặc sản khô, Trà & Cà phê), "Điện tử & Phụ kiện", "Mẹ & Bé", "Văn phòng phẩm & Sách".
  Đặt cây may mặc hiện có dưới gốc "Thời trang" chỉ khi seed hiện tại cho phép đổi cha an toàn (idempotent, không đổi id); nếu rủi ro thì để
  nguyên và báo. Tỷ lệ hoa hồng (`commission_rate_bps`) theo cách seed đang tính cho ngành con.
- Idempotent như phần còn lại của seed (chạy lại không tạo trùng; khớp theo slug). Nếu `check:data` hoặc test web đọc danh sách ngành từ
  `web/src/data/*` thì giữ cho chúng xanh.
- Không chạy `make seed` (manager chạy).

## Được sửa
`services/user/internal/site/**`, `services/user/migrations/**` (chỉ khi thật sự cần, file mới), `tools/seed/**`, `web/src/data/**` (chỉ khi seed
đọc dữ liệu ngành từ đó), test tương ứng. KHÔNG sửa `services/gateway`, `services/payment`, `services/product`, `web/src/app|features|lib|components`,
`tests/**` (Q đang chạy gate).

## Kiểm (phải xanh, dán kết quả)
`cd services/user && gofmt -l . && golangci-lint run ./... && go test -race ./...`; `node --test tools/seed/seed.test.mjs`;
`cd web && npm run check:data && npm run test:unit` (nếu sửa `web/src/data`).
