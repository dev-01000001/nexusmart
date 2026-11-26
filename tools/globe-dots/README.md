# `tools/globe-dots`

Sinh dữ liệu chấm lục địa (`web/public/globe/land-dots.json`) cho component quả địa cầu trên trang chủ NexusMart.

## Nguồn dữ liệu và giấy phép

- **Nguồn:** Natural Earth `110m_physical` — tệp `ne_110m_land.geojson` từ kho chính thức [`nvkelso/natural-earth-vector`](https://github.com/nvkelso/natural-earth-vector/blob/master/geojson/ne_110m_land.geojson) (hoặc [naturalearthdata.com](https://www.naturalearthdata.com/downloads/110m-physical-vectors/)).
- **Giấy phép:** Phạm vi công cộng (**Public Domain** — theo [Natural Earth Terms of Use](https://www.naturalearthdata.com/about/terms-of-use/), được tự do sử dụng, sửa đổi và phân phối lại mà không cần xin phép).
- **Phạm vi hình học:** Chỉ chứa đa giác lục địa vật lý ở tỷ lệ 1:110m (không có biên giới quốc gia, không có địa danh, không tự thêm hay bớt đảo so với dữ liệu gốc).

## Cách sinh lại dữ liệu

Không commit tệp GeoJSON gốc vào repo; tải về thư mục tạm rồi chạy script bằng Node 24 (không cần cài thêm gói):

```bash
curl -fsSL \
  "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_land.geojson" \
  -o /tmp/ne_110m_land.geojson

node tools/globe-dots/build.mjs /tmp/ne_110m_land.geojson
```

Tuỳ chọn:

- Tham số thứ hai đổi đường dẫn đầu ra (mặc định `web/public/globe/land-dots.json`).
- `--step=1.2` chỉnh bước lưới đều theo diện tích tính bằng độ (khuyến nghị `1.2`–`1.6`).

Định dạng đầu ra là mảng JSON các cặp `[vĩ_độ, kinh_độ]` làm tròn 1 chữ số thập phân.
