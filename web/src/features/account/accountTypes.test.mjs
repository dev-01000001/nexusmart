// Chạy: node --test web/src/features/account/ (Node 22, type stripping).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  mapGatewayAddress,
  safeInternalPath,
  safeNextPath,
  toStorefrontProvinceCode,
} from "./accountTypes.ts";

test("safeNextPath allows safe relative paths and rejects open redirects or auth loops", () => {
  assert.equal(safeNextPath("/don-hang"), "/don-hang");
  assert.equal(safeNextPath("/don-hang/abc?tab=1"), "/don-hang/abc?tab=1");
  assert.equal(safeNextPath("/thanh-toan"), "/thanh-toan");
  assert.equal(safeNextPath(" /san-pham?sort=price_asc "), "/san-pham?sort=price_asc");
  assert.equal(safeNextPath(null), "/tai-khoan");
  assert.equal(safeNextPath(""), "/tai-khoan");
  assert.equal(safeNextPath("https://evil.example/steal"), "/tai-khoan");
  assert.equal(safeNextPath("//evil.com"), "/tai-khoan");
  assert.equal(safeNextPath("//evil.example/steal"), "/tai-khoan");
  assert.equal(safeNextPath("/\\evil.com"), "/tai-khoan");
  assert.equal(safeNextPath("/\\evil.example"), "/tai-khoan");
  assert.equal(safeNextPath("/foo\\bar"), "/tai-khoan");
  assert.equal(safeNextPath("/\t/evil.com"), "/tai-khoan");
  assert.equal(safeNextPath(" /\t/evil"), "/tai-khoan");
  assert.equal(safeNextPath("/\n/evil"), "/tai-khoan");
  assert.equal(safeNextPath("/\x00/evil"), "/tai-khoan");
  assert.equal(safeNextPath("/\x1f/evil"), "/tai-khoan");
  assert.equal(safeNextPath("/\x7f/evil"), "/tai-khoan");
  assert.equal(safeNextPath("javascript:alert(1)"), "/tai-khoan");
  assert.equal(safeNextPath("/dang-nhap"), "/tai-khoan");
  assert.equal(safeNextPath("/dang-nhap?next=/tai-khoan"), "/tai-khoan");
  assert.equal(safeNextPath("/dang-ky"), "/tai-khoan");
  assert.equal(safeNextPath("/quen-mat-khau"), "/tai-khoan");
  assert.equal(safeNextPath("/dat-lai-mat-khau?token=abc"), "/tai-khoan");
  assert.equal(safeNextPath("/tai-khoan\r\nSet-Cookie: x=1"), "/tai-khoan");
  assert.equal(safeNextPath("/%2f/evil.com"), "/tai-khoan");
  assert.equal(safeNextPath("/tai-khoan/../dang-nhap"), "/tai-khoan");
});

test("safeInternalPath allows auth paths for logout while blocking open redirects", () => {
  assert.equal(safeInternalPath("/dang-nhap", "/"), "/dang-nhap");
  assert.equal(safeInternalPath("/tai-khoan?tab=addresses", "/"), "/tai-khoan?tab=addresses");
  assert.equal(safeInternalPath("/\t/evil.com", "/"), "/");
  assert.equal(safeInternalPath(" /\t/evil", "/"), "/");
  assert.equal(safeInternalPath("/\\evil.com", "/"), "/");
  assert.equal(safeInternalPath("//evil.com", "/"), "/");
  assert.equal(safeInternalPath("/\n/evil", "/"), "/");
  assert.equal(safeInternalPath("javascript:alert(1)", "/"), "/");
});

test("mapGatewayAddress converts gateway DTO to AddressView", () => {
  const addr = mapGatewayAddress({
    id: "addr-1",
    recipient_name: "Nguyễn Minh Quân",
    recipient_phone: "0912486305",
    province_code: "79",
    province_name: "Thành phố Hồ Chí Minh",
    ward_code: "79001",
    ward_name: "Phường Bến Nghé",
    street_address: "Số 248 Võ Văn Tần",
    is_default: true,
  });
  assert.deepEqual(addr, {
    id: "addr-1",
    recipientName: "Nguyễn Minh Quân",
    phone: "0912486305",
    provinceCode: "79",
    provinceName: "Thành phố Hồ Chí Minh",
    wardCode: "79001",
    wardName: "Phường Bến Nghé",
    streetDetail: "Số 248 Võ Văn Tần",
    isDefault: true,
  });
});

test("toStorefrontProvinceCode maps numeric backend province codes", () => {
  assert.equal(toStorefrontProvinceCode("79"), "HCM");
  assert.equal(toStorefrontProvinceCode("01"), "HN");
  assert.equal(toStorefrontProvinceCode("48"), "DN");
  assert.equal(toStorefrontProvinceCode("HCM"), "HCM");
});
