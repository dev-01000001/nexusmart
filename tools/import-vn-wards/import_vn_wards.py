#!/usr/bin/env python3
"""Build and cross-check Vietnam's 2025 2-level administrative catalog (34 provinces, 3,321 wards).

Primary source: Official National Statistics Office (Cuc Thong ke) administrative catalog
  https://danhmuchanhchinh.nso.gov.vn/DMDVHC.asmx (Resolution 202/2025/QH15 & Decision 19/2025/QD-TTg)
Cross-check source (MIT):
  thanglequoc/vietnamese-provinces-database (vn_only_simplified_json_generated_data_vn_units.json)

Uses only Python 3 standard library and default urllib User-Agent (no project metadata sent).
"""

import argparse
import gzip
import hashlib
import json
import os
import re
import sys
import unicodedata
import urllib.request
import xml.etree.ElementTree as ET
from datetime import datetime, timezone

NSO_SOAP_URL = "https://danhmuchanhchinh.nso.gov.vn/DMDVHC.asmx"
MIT_DEFAULT_URL = (
    "https://raw.githubusercontent.com/thanglequoc/vietnamese-provinces-database/"
    "8b78ba5118715e1fa81769286724db79346abf52/json/vn_only_simplified_json_generated_data_vn_units.json"
)

# E16: Island special zones where COD is unsupported by default.
COD_UNSUPPORTED_WARD_CODES = {
    "20333",  # Dac khu Hoang Sa (Da Nang, 48)
    "22736",  # Dac khu Truong Sa (Khanh Hoa, 56)
}

# Known GHN sandbox/production district & ward mappings preserved from legacy catalog.
GHN_WARD_OVERRIDES = {
    "00070": (1489, "1A0606"),  # Phuong Hoan Kiem (01)
    "00004": (1484, "1A0101"),  # Phuong Ba Dinh (01)
    "00166": (1485, "1A0201"),  # Phuong Cau Giay (01)
    "00235": (1486, "1A0301"),  # Phuong Dong Da (01)
    "00283": (1488, "1A0501"),  # Phuong Hai Ba Trung (01)
    "00343": (1493, "1A1001"),  # Phuong Thanh Xuan (01)
    "20242": (1526, "40101"),   # Phuong Hai Chau (48)
    "20263": (1530, "40501"),   # Phuong Son Tra (48)
    "20227": (1527, "40201"),   # Phuong Thanh Khe (48)
    "20284": (1529, "40401"),   # Phuong Ngu Hanh Son (48)
    "26740": (1442, "20101"),   # Phuong Sai Gon (79)
    "26743": (1442, "20102"),   # Phuong Ben Thanh (79)
    "27487": (1449, "20706"),   # Phuong Tan My (79)
    "26824": (3695, "90737"),   # Phuong Thu Duc (79)
    "26929": (1462, "21601"),   # Phuong Binh Thanh (79)
    "25747": (1538, "25747"),   # Phuong Thu Dau Mot (79)
    "25942": (1539, "25942"),   # Phuong Di An (79)
    "26506": (1540, "26506"),   # Phuong Vung Tau (79)
    "31135": (1572, "550101"),  # Phuong Ninh Kieu (92)
    "31168": (1573, "550201"),  # Phuong Cai Rang (92)
}

PROVINCE_META = {
    "01": ("north", 201), "04": ("north", 247), "08": ("north", 229),
    "11": ("north", 265), "12": ("north", 264), "14": ("north", 267),
    "15": ("north", 269), "19": ("north", 244), "20": ("north", 248),
    "22": ("north", 230), "24": ("north", 249), "25": ("north", 228),
    "31": ("north", 224), "33": ("north", 268), "37": ("north", 233),
    "38": ("central", 234), "40": ("central", 235), "42": ("central", 236),
    "44": ("central", 238), "46": ("central", 223), "48": ("central", 203),
    "51": ("central", 242), "52": ("central", 259), "56": ("central", 208),
    "66": ("central", 210), "68": ("central", 209),
    "75": ("south", 204), "79": ("south", 202), "80": ("south", 213),
    "82": ("south", 216), "86": ("south", 215), "91": ("south", 217),
    "92": ("south", 220), "96": ("south", 252),
}


def clean_text(raw: str) -> str:
    nfc = unicodedata.normalize("NFC", raw or "")
    return re.sub(r"\s+", " ", nfc).strip()


def strip_diacritics(raw: str) -> str:
    s = clean_text(raw).lower().replace("đ", "d")
    s = unicodedata.normalize("NFD", s)
    s = "".join(ch for ch in s if unicodedata.category(ch) != "Mn")
    return re.sub(r"[^a-z0-9]+", " ", s).strip()


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def fetch_nso_soap(operation: str, body_xml: str) -> bytes:
    envelope = (
        '<?xml version="1.0" encoding="utf-8"?>'
        '<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">'
        f"<soap:Body>{body_xml}</soap:Body>"
        "</soap:Envelope>"
    ).encode("utf-8")
    req = urllib.request.Request(
        NSO_SOAP_URL,
        data=envelope,
        headers={
            "Content-Type": "text/xml; charset=utf-8",
            "SOAPAction": f'"http://tempuri.org/{operation}"',
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return resp.read()


def fetch_url(url: str) -> bytes:
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=60) as resp:
        return resp.read()


def parse_nso_xml(raw: bytes) -> list[dict[str, str]]:
    root = ET.fromstring(raw)
    rows: list[dict[str, str]] = []
    for elem in root.iter():
        if elem.tag.endswith("TABLE"):
            row = {ch.tag.split("}")[-1]: clean_text(ch.text or "") for ch in elem}
            if row:
                rows.append(row)
    return rows


def main() -> int:
    parser = argparse.ArgumentParser(description="Import and cross-check VN 2025 provinces/wards catalog")
    parser.add_argument("--nso-provinces", help="Local path to NSO DanhMucTinh XML")
    parser.add_argument("--nso-wards", help="Local path to NSO DanhMucPhuongXa XML")
    parser.add_argument("--mit-json", help="Local path to MIT cross-check JSON")
    parser.add_argument("--as-of", default="24/11/2026", help="DenNgay parameter (DD/MM/YYYY) for --fetch")
    parser.add_argument("--fetch", action="store_true", help="Fetch raw files from public NSO + MIT URLs")
    parser.add_argument(
        "--out",
        default="services/shipping/internal/address/data/vn_admin_2025.json.gz",
        help="Output gzipped JSON path",
    )
    args = parser.parse_args()

    if args.fetch:
        nso_prov_raw = fetch_nso_soap(
            "DanhMucTinh",
            f'<DanhMucTinh xmlns="http://tempuri.org/"><DenNgay>{args.as_of}</DenNgay></DanhMucTinh>',
        )
        nso_ward_raw = fetch_nso_soap(
            "DanhMucPhuongXa",
            f'<DanhMucPhuongXa xmlns="http://tempuri.org/"><DenNgay>{args.as_of}</DenNgay>'
            "<Tinh></Tinh><TenTinh></TenTinh><QuanHuyen></QuanHuyen><TenQuanHuyen></TenQuanHuyen>"
            "</DanhMucPhuongXa>",
        )
        mit_raw = fetch_url(MIT_DEFAULT_URL)
    else:
        if not (args.nso_provinces and args.nso_wards and args.mit_json):
            parser.error("provide either --fetch or all of --nso-provinces, --nso-wards, --mit-json")
        with open(args.nso_provinces, "rb") as f:
            nso_prov_raw = f.read()
        with open(args.nso_wards, "rb") as f:
            nso_ward_raw = f.read()
        with open(args.mit_json, "rb") as f:
            mit_raw = f.read()

    nso_provs_rows = parse_nso_xml(nso_prov_raw)
    nso_wards_rows = parse_nso_xml(nso_ward_raw)
    mit_data = json.loads(mit_raw.decode("utf-8"))

    nso_provs = {r["MaTinh"]: r["TenTinh"] for r in nso_provs_rows}
    nso_wards = {r["MaPhuongXa"]: (r["MaTinh"], r["TenPhuongXa"]) for r in nso_wards_rows}

    mit_provs = {p["Code"]: clean_text(p["FullName"]) for p in mit_data}
    mit_wards = {
        w["Code"]: (w["ProvinceCode"], clean_text(w["FullName"]))
        for p in mit_data
        for w in p["Wards"]
    }

    # Strict cross-check between official NSO and MIT dataset.
    prov_only_nso = sorted(set(nso_provs) - set(mit_provs))
    prov_only_mit = sorted(set(mit_provs) - set(nso_provs))
    ward_only_nso = sorted(set(nso_wards) - set(mit_wards))
    ward_only_mit = sorted(set(mit_wards) - set(nso_wards))
    prov_mismatch = [
        (code, nso_wards[code][0], mit_wards[code][0])
        for code in sorted(set(nso_wards) & set(mit_wards))
        if nso_wards[code][0] != mit_wards[code][0]
    ]
    name_semantic_mismatch = [
        (code, nso_wards[code][1], mit_wards[code][1])
        for code in sorted(set(nso_wards) & set(mit_wards))
        if strip_diacritics(nso_wards[code][1]) != strip_diacritics(mit_wards[code][1])
    ]

    if prov_only_nso or prov_only_mit or ward_only_nso or ward_only_mit or prov_mismatch or name_semantic_mismatch:
        print("CROSS-CHECK FAILED:", file=sys.stderr)
        print(f"  prov_only_nso={prov_only_nso}", file=sys.stderr)
        print(f"  prov_only_mit={prov_only_mit}", file=sys.stderr)
        print(f"  ward_only_nso={ward_only_nso[:20]} (total {len(ward_only_nso)})", file=sys.stderr)
        print(f"  ward_only_mit={ward_only_mit[:20]} (total {len(ward_only_mit)})", file=sys.stderr)
        print(f"  prov_mismatch={prov_mismatch[:20]} (total {len(prov_mismatch)})", file=sys.stderr)
        print(f"  name_semantic_mismatch={name_semantic_mismatch[:20]} (total {len(name_semantic_mismatch)})", file=sys.stderr)
        return 1

    # Verify every province has region/GHN metadata.
    missing_meta = sorted(set(nso_provs) - set(PROVINCE_META))
    if missing_meta:
        print(f"Missing PROVINCE_META for: {missing_meta}", file=sys.stderr)
        return 1

    # Build deterministic sorted catalog (NSO official names normalized to Unicode NFC).
    provinces_out = []
    for code in sorted(nso_provs):
        region, ghn_prov_id = PROVINCE_META[code]
        provinces_out.append({
            "code": code,
            "name": nso_provs[code],
            "region": region,
            "ghn_province_id": ghn_prov_id,
        })

    wards_out = []
    for code in sorted(nso_wards):
        prov_code, name = nso_wards[code]
        ghn_dist, ghn_ward = GHN_WARD_OVERRIDES.get(code, (0, ""))
        cod_supported = code not in COD_UNSUPPORTED_WARD_CODES
        wards_out.append({
            "code": code,
            "province_code": prov_code,
            "name": name,
            "ghn_district_id": ghn_dist,
            "ghn_ward_code": ghn_ward,
            "cod_supported": cod_supported,
        })

    # Sanity check COD-excluded islands exist.
    for island_code in sorted(COD_UNSUPPORTED_WARD_CODES):
        if island_code not in nso_wards:
            print(f"Expected island ward code {island_code} not found in NSO data", file=sys.stderr)
            return 1

    payload = {
        "schema_version": 1,
        "resolution": "202/2025/QH15",
        "decision": "19/2025/QD-TTg",
        "province_count": len(provinces_out),
        "ward_count": len(wards_out),
        "provinces": provinces_out,
        "wards": wards_out,
    }
    raw_json = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")

    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "wb") as raw_f:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw_f, mtime=0) as gz:
            gz.write(raw_json)

    with open(args.out, "rb") as f:
        gz_bytes = f.read()

    print(f"Generated: {args.out} ({len(gz_bytes)} bytes gzipped, {len(raw_json)} bytes JSON)")
    print(f"Provinces: {len(provinces_out)}, Wards: {len(wards_out)}")
    print(f"NSO DanhMucTinh XML SHA256:    {sha256_bytes(nso_prov_raw)}")
    print(f"NSO DanhMucPhuongXa XML SHA256:{sha256_bytes(nso_ward_raw)}")
    print(f"MIT JSON SHA256:               {sha256_bytes(mit_raw)}")
    print(f"Output JSON SHA256:            {sha256_bytes(raw_json)}")
    print(f"Output .json.gz SHA256:        {sha256_bytes(gz_bytes)}")
    print(f"Timestamp UTC:                 {datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
