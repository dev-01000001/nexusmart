// Run: node --test web/src/features/platform/platform.test.mjs (Node 22 type stripping)
import test from "node:test";
import assert from "node:assert/strict";
import {
  buildPageList,
  normalizeDirectoryResponse,
  normalizeDirectorySite,
  parseDirectoryPage,
} from "./directoryModel.ts";
import { checkSubdomain, suggestSubdomain } from "./subdomain.ts";
import { legacySiteTarget, shopMatchesLegacySlug } from "./legacyModel.ts";
import { legacyNotFound, legacyRedirect, safeDecode } from "./legacyResponse.ts";

const ctx = {
  rootDomain: "nexusmart.localhost",
  rawHost: "nexusmart.localhost:3401",
  proto: "http",
};

const row = (over = {}) => ({
  site_id: "site-1",
  shop_id: "shop-1",
  shop_name: "Xưởng Dệt Bạch Đằng",
  subdomain: "bach-dang",
  primary_host: "bach-dang.nexusmart.localhost",
  logo_url: "/banners/logo.jpg",
  cover_url: "https://cdn.example.vn/cover.jpg",
  tagline: "Áo thun Cotton Compact",
  ...over,
});

test("directory site keeps the request port and only safe image URLs", () => {
  const site = normalizeDirectorySite(row({ logo_url: "javascript:alert(1)" }), ctx);
  assert.equal(site.origin, "http://bach-dang.nexusmart.localhost:3401");
  assert.equal(site.host, "bach-dang.nexusmart.localhost");
  assert.equal(site.logoUrl, "");
  assert.equal(site.coverUrl, "https://cdn.example.vn/cover.jpg");
});

test("directory site accepts custom primary_host, falls back to subdomain, and rejects bad/IP/localhost/forbidden hosts", () => {
  const custom = normalizeDirectorySite(row({ primary_host: "hanoi-knit.verified.test" }), ctx);
  assert.equal(custom.host, "hanoi-knit.verified.test");
  assert.equal(custom.origin, "http://hanoi-knit.verified.test:3401");

  const prodCustom = normalizeDirectorySite(row({ primary_host: "shop.brand.vn" }), {
    rootDomain: "nexusmart.vn",
    rawHost: "nexusmart.vn",
    proto: "https",
  });
  assert.equal(prodCustom.host, "shop.brand.vn");
  assert.equal(prodCustom.origin, "https://shop.brand.vn");

  assert.equal(
    normalizeDirectorySite(row({ primary_host: "" }), ctx).host,
    "bach-dang.nexusmart.localhost"
  );
  // Invalid primary_host falls back to valid subdomain.
  assert.equal(
    normalizeDirectorySite(row({ primary_host: "shop.local" }), ctx).host,
    "bach-dang.nexusmart.localhost"
  );

  // Root, localhost, IP, forbidden, reserved, and malformed hosts are rejected.
  for (const badHost of [
    "nexusmart.localhost",
    "localhost",
    "foo.localhost",
    "shop.local",
    "svc.internal",
    "127.0.0.1",
    "[::1]",
    "999.999.999.999",
    "*.brand.vn",
    "bad_host.com",
    "example.com..",
    "admin.nexusmart.localhost",
    "singlelabel",
  ]) {
    assert.equal(
      normalizeDirectorySite(row({ primary_host: badHost, subdomain: "" }), ctx),
      null,
      `expected directory site to reject primary_host=${badHost}`
    );
  }

  assert.equal(normalizeDirectorySite(row({ primary_host: "", subdomain: "admin" }), ctx), null);
  assert.equal(normalizeDirectorySite(row({ shop_name: "" }), ctx), null);
});

test("directory response dedupes rows, caps page size and derives page count", () => {
  const page = normalizeDirectoryResponse(
    {
      sites: [
        row(),
        row(),
        row({ site_id: "site-2", subdomain: "sai-gon", primary_host: "" }),
        null,
      ],
      total: 50,
      page: 2,
      page_size: 500,
    },
    ctx,
    { page: 2, pageSize: 24 }
  );
  assert.equal(page.sites.length, 2);
  assert.equal(page.pageSize, 48);
  assert.equal(page.page, 2);
  assert.equal(page.totalPages, 2);

  const empty = normalizeDirectoryResponse(null, ctx, { page: 1, pageSize: 24 });
  assert.deepEqual(empty, { sites: [], total: 0, page: 1, pageSize: 24, totalPages: 1 });
});

test("page param parsing and compact pager", () => {
  assert.equal(parseDirectoryPage(undefined), 1);
  assert.equal(parseDirectoryPage("3"), 3);
  assert.equal(parseDirectoryPage(["4", "5"]), 4);
  assert.equal(parseDirectoryPage("-2"), 1);
  assert.equal(parseDirectoryPage("abc"), 1);
  assert.equal(parseDirectoryPage("0"), 1);
  assert.deepEqual(buildPageList(1, 1), [1]);
  assert.deepEqual(buildPageList(1, 3), [1, 2, 3]);
  assert.deepEqual(buildPageList(6, 12), [1, null, 5, 6, 7, null, 12]);
  assert.deepEqual(buildPageList(12, 12), [1, null, 11, 12]);
});

test("subdomain suggestion strips Vietnamese marks and checks reserved labels", () => {
  assert.equal(suggestSubdomain("Xưởng Dệt Bạch Đằng"), "xuong-det-bach-dang");
  assert.equal(suggestSubdomain("  Áo  thun -- Sài Gòn!! "), "ao-thun-sai-gon");
  assert.equal(suggestSubdomain("a".repeat(60)).length, 40);
  assert.equal(checkSubdomain(""), "empty");
  assert.equal(checkSubdomain("a"), "short");
  assert.equal(checkSubdomain("admin"), "reserved");
  assert.equal(checkSubdomain("bach-dang"), "ok");
});

test("legacy shop slug matches id, subdomain or the old profile name", () => {
  const site = normalizeDirectorySite(row(), ctx);
  assert.equal(shopMatchesLegacySlug(site, "shop-1", null), true);
  assert.equal(shopMatchesLegacySlug(site, "bach-dang", null), true);
  assert.equal(shopMatchesLegacySlug(site, "det-bach-dang", "xưởng dệt bạch đằng"), true);
  assert.equal(shopMatchesLegacySlug(site, "det-kim-ha-noi", "Dệt Kim Hà Nội"), false);
});

test("legacy redirect target stays on the site origin", () => {
  const origin = "http://bach-dang.nexusmart.localhost:3401";
  assert.equal(legacySiteTarget(origin), `${origin}/`);
  assert.equal(
    legacySiteTarget(origin, "ao-thun trắng"),
    `${origin}/san-pham/ao-thun%20tr%E1%BA%AFng`
  );
});

test("legacy HTTP responses use 308/404 with no-store and safe decoding", () => {
  const redir = legacyRedirect("http://bach-dang.nexusmart.localhost:3401/");
  assert.equal(redir.status, 308);
  assert.equal(redir.headers.get("location"), "http://bach-dang.nexusmart.localhost:3401/");
  assert.equal(redir.headers.get("cache-control"), "no-store");

  const miss = legacyNotFound();
  assert.equal(miss.status, 404);
  assert.equal(miss.headers.get("cache-control"), "no-store");

  assert.equal(safeDecode("ao-thun%20trang"), "ao-thun trang");
  assert.equal(safeDecode("bad%ZZslug"), "bad%ZZslug");
});
