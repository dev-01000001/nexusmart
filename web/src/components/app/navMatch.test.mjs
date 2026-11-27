// Run: cd web && node --test src/components/app/navMatch.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { isNavItemActive, resolveActiveNav } from "./navMatch.ts";

function splitUrl(href) {
  const qIdx = href.indexOf("?");
  if (qIdx === -1) return { pathname: href, search: "" };
  return { pathname: href.slice(0, qIdx), search: href.slice(qIdx) };
}

test("isNavItemActive matches exact home, prefix paths, and query parameters accurately", () => {
  assert.equal(isNavItemActive("/seller", "", "/seller", "/seller"), true);
  assert.equal(isNavItemActive("/seller/don-hang", "", "/seller", "/seller"), false);
  assert.equal(isNavItemActive("/seller/don-hang/123", "", "/seller/don-hang", "/seller"), true);
  assert.equal(isNavItemActive("/design/app", "?kieu=a1", "/design/app?kieu=a1", "/x"), true);
  assert.equal(isNavItemActive("/design/app", "?kieu=a2", "/design/app?kieu=a1", "/x"), false);
  assert.equal(
    isNavItemActive(
      "/seller/san-pham",
      "?view=ton-kho&page=2",
      "/seller/san-pham?view=ton-kho",
      "/seller"
    ),
    true
  );
  assert.equal(
    isNavItemActive("/seller/san-pham", "", "/seller/san-pham?view=ton-kho", "/seller"),
    false
  );
});

test("resolveActiveNav highlights exactly one top-level item across all SELLER_NAV_GROUPS hrefs", () => {
  // Representative seller nav group items with query-param variants
  const sellerDemoItems = [
    { href: "/design/app?kieu=a9", match: "exact" },
    { href: "/design/app?kieu=a2" },
    { href: "/design/app?kieu=a1&muc=tra-hang" },
    {
      href: "/design/app?kieu=a1",
      match: "exact",
      children: [{ href: "/design/app?kieu=a3" }, { href: "/design/app?kieu=a1&view=ton-kho" }],
    },
    { href: "/design/app?kieu=a4&muc=khuyen-mai" },
    { href: "/design/app?kieu=a6" },
    { href: "/design/app?kieu=a5&muc=tuong-tac" },
    {
      href: "/design/app?kieu=a7",
      children: [{ href: "/design/app?kieu=a8" }],
    },
    { href: "/design/site" },
    { href: "/design/app?kieu=a4" },
    { href: "/design/app?kieu=a5" },
  ];

  const cases = [
    {
      url: "/design/app?kieu=a9",
      expected: { activeHref: "/design/app?kieu=a9", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a2",
      expected: { activeHref: "/design/app?kieu=a2", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a1&muc=tra-hang",
      expected: { activeHref: "/design/app?kieu=a1&muc=tra-hang", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a1",
      expected: { activeHref: "/design/app?kieu=a1", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a3",
      expected: { activeHref: "/design/app?kieu=a1", activeChildHref: "/design/app?kieu=a3" },
    },
    {
      url: "/design/app?kieu=a1&view=ton-kho",
      expected: {
        activeHref: "/design/app?kieu=a1",
        activeChildHref: "/design/app?kieu=a1&view=ton-kho",
      },
    },
    {
      url: "/design/app?kieu=a4&muc=khuyen-mai",
      expected: { activeHref: "/design/app?kieu=a4&muc=khuyen-mai", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a6",
      expected: { activeHref: "/design/app?kieu=a6", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a5&muc=tuong-tac",
      expected: { activeHref: "/design/app?kieu=a5&muc=tuong-tac", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a7",
      expected: { activeHref: "/design/app?kieu=a7", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a8",
      expected: { activeHref: "/design/app?kieu=a7", activeChildHref: "/design/app?kieu=a8" },
    },
    {
      url: "/design/site",
      expected: { activeHref: "/design/site", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a4",
      expected: { activeHref: "/design/app?kieu=a4", activeChildHref: null },
    },
    {
      url: "/design/app?kieu=a5",
      expected: { activeHref: "/design/app?kieu=a5", activeChildHref: null },
    },
  ];

  for (const { url, expected } of cases) {
    const { pathname, search } = splitUrl(url);
    assert.deepEqual(
      resolveActiveNav(sellerDemoItems, pathname, search, "/design/app"),
      expected,
      `URL ${url}`
    );
  }

  assert.deepEqual(resolveActiveNav(sellerDemoItems, "/design/app", "", "/design/app"), {
    activeHref: null,
    activeChildHref: null,
  });
});

test("resolveActiveNav prefers more specific child query params (Phase 1 seller products)", () => {
  const items = [
    { href: "/seller", match: "exact" },
    {
      href: "/seller/san-pham",
      children: [{ href: "/seller/san-pham" }, { href: "/seller/san-pham?view=ton-kho" }],
    },
  ];

  assert.deepEqual(resolveActiveNav(items, "/seller/san-pham", "?view=ton-kho", "/seller"), {
    activeHref: "/seller/san-pham",
    activeChildHref: "/seller/san-pham?view=ton-kho",
  });

  assert.deepEqual(resolveActiveNav(items, "/seller/san-pham", "", "/seller"), {
    activeHref: "/seller/san-pham",
    activeChildHref: "/seller/san-pham",
  });

  assert.deepEqual(resolveActiveNav(items, "/seller/san-pham/42", "", "/seller"), {
    activeHref: "/seller/san-pham",
    activeChildHref: null,
  });
});

test("resolveActiveNav ranks longer paths higher and breaks ties in favor of earlier items", () => {
  const pathItems = [
    { href: "/seller" },
    { href: "/seller/don-hang" },
    { href: "/seller/don-hang/tra-hang" },
  ];
  assert.deepEqual(resolveActiveNav(pathItems, "/seller/don-hang/tra-hang", "", "/seller"), {
    activeHref: "/seller/don-hang/tra-hang",
    activeChildHref: null,
  });

  const tieItems = [
    { href: "/seller/don-hang?tab=cho-duyet" },
    { href: "/seller/don-hang?kieu=nhanh" },
  ];
  assert.deepEqual(
    resolveActiveNav(tieItems, "/seller/don-hang", "?tab=cho-duyet&kieu=nhanh", "/seller"),
    {
      activeHref: "/seller/don-hang?tab=cho-duyet",
      activeChildHref: null,
    }
  );
});
