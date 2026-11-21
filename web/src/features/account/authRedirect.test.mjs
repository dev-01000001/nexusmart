// Chạy: node --test web/src/features/account/ (Node 22, type stripping).
import { test } from "node:test";
import assert from "node:assert/strict";
import { resolvePostLoginPath } from "./authRedirect.ts";

test("site variant keeps a safe next path and falls back to the account page", () => {
  assert.equal(resolvePostLoginPath("site", "buyer", "/thanh-toan"), "/thanh-toan");
  assert.equal(resolvePostLoginPath("site", "buyer", "/don-hang/abc"), "/don-hang/abc");
  assert.equal(resolvePostLoginPath("site", "buyer", null), "/tai-khoan");
  assert.equal(resolvePostLoginPath("site", "buyer", "https://evil.example"), "/tai-khoan");
  assert.equal(resolvePostLoginPath("site", "buyer", "//evil.example"), "/tai-khoan");
  assert.equal(resolvePostLoginPath("site", "seller", "/dang-nhap"), "/tai-khoan");
});

test("platform variant routes admins and sellers to their own area only", () => {
  assert.equal(resolvePostLoginPath("platform", "admin", null), "/admin");
  assert.equal(resolvePostLoginPath("platform", "super_admin", "/admin/sites"), "/admin/sites");
  assert.equal(resolvePostLoginPath("platform", "admin", "/seller"), "/admin");
  assert.equal(resolvePostLoginPath("platform", "admin", "/administrator"), "/admin");
  assert.equal(resolvePostLoginPath("platform", "seller", "/seller/site"), "/seller/site");
  assert.equal(resolvePostLoginPath("platform", "seller", "/admin"), "/seller");
  assert.equal(resolvePostLoginPath("platform", "seller", "/tai-khoan"), "/seller");
  assert.equal(resolvePostLoginPath("platform", "seller", "//evil.example/seller"), "/seller");
});

test("platform variant sends buyers to the shop directory whatever next says", () => {
  assert.equal(resolvePostLoginPath("platform", "buyer", "/tai-khoan"), "/");
  assert.equal(resolvePostLoginPath("platform", "buyer", "/seller"), "/");
  assert.equal(resolvePostLoginPath("platform", "buyer", null, "/#cua-hang"), "/#cua-hang");
});
