# Vietnam 2025 Administrative Catalog Importer

Reproducible Python 3 (stdlib-only) generator for `services/shipping/internal/address/data/vn_admin_2025.json.gz`.

## Sources
1. **Official (Primary)**: National Statistics Office (Cục Thống kê) Web Service `https://danhmuchanhchinh.nso.gov.vn/DMDVHC.asmx` (`DanhMucTinh`, `DanhMucPhuongXa`) implementing Resolution `202/2025/QH15` and Decision `19/2025/QĐ-TTg` (effective 01/07/2025).
2. **MIT Cross-Check**: `thanglequoc/vietnamese-provinces-database` (`json/vn_only_simplified_json_generated_data_vn_units.json`, MIT license).

The script enforces 100% agreement between both sources on province codes, ward codes, ward-to-province ownership, and diacritic-stripped names, normalizes names to Unicode NFC with collapsed whitespace, preserves known GHN sandbox codes, marks Hoàng Sa (`20333`) and Trường Sa (`22736`) with `cod_supported: false`, and writes deterministic gzipped JSON (`mtime=0`).

## Usage

From repository root:

```bash
# Fetch live public data (uses default stdlib User-Agent) and regenerate:
python3 tools/import-vn-wards/import_vn_wards.py --fetch

# Or build offline from downloaded raw files:
python3 tools/import-vn-wards/import_vn_wards.py \
  --nso-provinces /path/to/nso_tinh.xml \
  --nso-wards /path/to/nso_xa.xml \
  --mit-json /path/to/mit_vn_units.json
```
