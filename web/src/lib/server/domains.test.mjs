// Run: node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/lib/server/domains.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  formatDomainErrorForSeller,
  getDomainStatusMeta,
  isValidCustomHostname,
  normalizeSiteDomain,
  normalizeSiteDomains,
  resolveDomainGatewayError,
  validateCustomHostname,
} from "../site/domains.ts";
import { GatewayError, errorFromResponse } from "./errors.ts";

const FORBIDDEN_LEAK_PATTERNS = [
  /cloudflare/i,
  /cf_api_token/i,
  /cf_zone/i,
  /bearer\s+/i,
  /sk_live/i,
  /stack/i,
  /sqlstate/i,
  /grpc/i,
  /dial tcp/i,
  /custom_hostnames/i,
];

function assertNoInternalLeak(text) {
  for (const re of FORBIDDEN_LEAK_PATTERNS) {
    assert.equal(re.test(text), false, `Expected no internal leak matching ${re} in: ${text}`);
  }
}

test("normalizeSiteDomain & normalizeSiteDomains normalize SUBDOMAIN, CUSTOM states, verification records, and drop malformed items", () => {
  const rawList = [
    {
      id: " dom-sub-1 ",
      host: "  Hanoi-Knit.NexusMart.Localhost ",
      kind: "subdomain",
      status: "active",
      is_primary: false,
      verification: [],
      last_error: "",
      verified_at: "2026-11-20T08:00:00Z",
      created_at: "2026-11-20T07:59:00Z",
    },
    {
      id: "dom-custom-pending",
      host: "Shop.HanoiKnit.VN",
      kind: "CUSTOM",
      status: "PENDING",
      is_primary: false,
      verification: [
        { type: "cname", name: "shop.hanoiknit.vn", value: "shops.nexusmart.vn" },
        {
          type: "txt",
          name: "_cf-custom-hostname.shop.hanoiknit.vn",
          value: "verify-token-abc",
        },
        { type: "TXT", name: "", value: "missing-name-filtered" },
        null,
        "invalid-item",
      ],
      last_error: null,
      verified_at: "",
      created_at: "2026-11-21T01:00:00Z",
    },
    {
      id: "dom-custom-active",
      host: "hanoi-knit.verified.test",
      kind: "CUSTOM",
      status: "ACTIVE",
      is_primary: true,
      verification: [
        { type: "CNAME", name: "hanoi-knit.verified.test", value: "shops.nexusmart.vn" },
      ],
      last_error: "",
      verified_at: "2026-11-21T02:00:00Z",
      created_at: "2026-11-21T01:50:00Z",
    },
    {
      id: "dom-custom-failed",
      host: "broken.fail.test",
      kind: "CUSTOM",
      status: "FAILED",
      is_primary: false,
      verification: [{ type: "TXT", name: "_acme.broken.fail.test", value: "val-123" }],
      last_error: "DNS verification expired after 7 days",
      verified_at: null,
      created_at: "2026-11-10T00:00:00Z",
    },
    {
      id: "dom-custom-removing",
      host: "old.brand.vn",
      kind: "CUSTOM",
      status: "REMOVING",
      is_primary: false,
      verification: "not-an-array",
      last_error: "delete pending",
      verified_at: null,
      created_at: "2026-11-19T00:00:00Z",
    },
    // Malformed entries must be filtered out cleanly
    null,
    undefined,
    {},
    { id: "", host: "no-id.example.com" },
    { id: "missing-host", host: "   " },
  ];

  const domains = normalizeSiteDomains(rawList);
  assert.equal(domains.length, 5);

  assert.deepEqual(domains[0], {
    id: "dom-sub-1",
    host: "hanoi-knit.nexusmart.localhost",
    kind: "SUBDOMAIN",
    status: "ACTIVE",
    is_primary: false,
    verification: [],
    last_error: "",
    verified_at: "2026-11-20T08:00:00Z",
    created_at: "2026-11-20T07:59:00Z",
  });

  assert.deepEqual(domains[1], {
    id: "dom-custom-pending",
    host: "shop.hanoiknit.vn",
    kind: "CUSTOM",
    status: "PENDING",
    is_primary: false,
    verification: [
      { type: "CNAME", name: "shop.hanoiknit.vn", value: "shops.nexusmart.vn" },
      {
        type: "TXT",
        name: "_cf-custom-hostname.shop.hanoiknit.vn",
        value: "verify-token-abc",
      },
    ],
    last_error: "",
    verified_at: null,
    created_at: "2026-11-21T01:00:00Z",
  });

  assert.equal(domains[2].status, "ACTIVE");
  assert.equal(domains[2].is_primary, true);
  assert.equal(domains[2].verified_at, "2026-11-21T02:00:00Z");

  assert.equal(domains[3].status, "FAILED");
  assert.equal(domains[3].last_error, "DNS verification expired after 7 days");
  assert.equal(domains[3].verified_at, null);

  assert.equal(domains[4].status, "REMOVING");
  assert.deepEqual(domains[4].verification, []);

  assert.equal(normalizeSiteDomain(null), null);
  assert.equal(normalizeSiteDomain(undefined), null);
  assert.deepEqual(normalizeSiteDomains(null), []);
  assert.deepEqual(normalizeSiteDomains("not-a-list"), []);
});

test("validateCustomHostname accepts valid domains including .verified.test and strips one trailing dot", () => {
  const validCases = [
    ["shop.thuonghieu.vn", "shop.thuonghieu.vn"],
    ["  SHOP.Brand-VN.Com  ", "shop.brand-vn.com"],
    ["hanoi-knit.verified.test", "hanoi-knit.verified.test"],
    ["store.pending.test.", "store.pending.test"],
    ["a.b.c.example.co.uk", "a.b.c.example.co.uk"],
    ["xn--thnghiu-1wa.vn", "xn--thnghiu-1wa.vn"],
    ["01.shop99.vn", "01.shop99.vn"],
  ];

  for (const [input, expected] of validCases) {
    const res = validateCustomHostname(input, "nexusmart.localhost");
    assert.equal(
      res.ok,
      true,
      `Expected valid hostname for "${input}", got ${JSON.stringify(res)}`
    );
    if (res.ok) {
      assert.equal(res.hostname, expected);
    }
    assert.equal(isValidCustomHostname(input, "nexusmart.localhost"), true);
  }
});

test("validateCustomHostname rejects invalid hostnames per Phase 5 contract", () => {
  const label64 = `${"a".repeat(64)}.example.com`;
  const host254 = `${"a".repeat(60)}.${"b".repeat(60)}.${"c".repeat(60)}.${"d".repeat(60)}.example.com`;
  assert.ok(host254.length > 253);

  const invalidCases = [
    ["", "empty"],
    ["   ", "empty"],
    ["shop.brand.vn:3000", "port_or_ipv6"],
    ["[2001:db8::1]", "port_or_ipv6"],
    ["127.0.0.1", "ip_literal"],
    ["192.168.1.10", "ip_literal"],
    ["999.999.999.999", "ip_literal"],
    ["127.1", "ip_literal"],
    ["localhost", "forbidden_host"],
    ["shop.localhost", "forbidden_host"],
    ["shop.local", "forbidden_host"],
    ["api.internal", "forbidden_host"],
    ["nexusmart.localhost", "forbidden_host"],
    ["bach-dang.nexusmart.localhost", "forbidden_host"],
    ["a.b.nexusmart.localhost", "forbidden_host"],
    ["*.brand.vn", "wildcard"],
    ["shop.*.vn", "wildcard"],
    ["https://shop.brand.vn", "invalid_chars"],
    ["shop.brand.vn/path", "invalid_chars"],
    ["shop brand.vn", "invalid_chars"],
    ["cửa-hàng.vn", "non_ascii"],
    ["shop_one.brand.vn", "invalid_label"],
    ["-shop.brand.vn", "invalid_label"],
    ["shop-.brand.vn", "invalid_label"],
    ["shop..brand.vn", "invalid_label"],
    ["shop.brand.vn..", "invalid_label"],
    ["singlelabel", "no_dot"],
    [label64, "invalid_label"],
    [host254, "length"],
  ];

  for (const [input, expectedReason] of invalidCases) {
    const res = validateCustomHostname(input, "nexusmart.localhost");
    assert.equal(res.ok, false, `Expected invalid hostname for "${input}"`);
    if (!res.ok) {
      assert.equal(
        res.reason,
        expectedReason,
        `Wrong reason for "${input}": expected ${expectedReason}, got ${res.reason}`
      );
      assert.ok(res.message.length > 0);
      assertNoInternalLeak(res.message);
    }
    assert.equal(isValidCustomHostname(input, "nexusmart.localhost"), false);
  }

  // Production root domain check as well
  const prodRoot = validateCustomHostname("nexusmart.vn", "nexusmart.vn");
  assert.equal(prodRoot.ok, false);
  if (!prodRoot.ok) assert.equal(prodRoot.reason, "platform_domain");

  const prodSub = validateCustomHostname("shop1.nexusmart.vn", "nexusmart.vn");
  assert.equal(prodSub.ok, false);
  if (!prodSub.ok) assert.equal(prodSub.reason, "platform_domain");
});

test("resolveDomainGatewayError maps contract codes and never leaks Cloudflare or internal details", async () => {
  const invalidHost = resolveDomainGatewayError(
    new GatewayError(
      "invalid",
      "invalid_hostname",
      "cloudflare rejected custom_hostnames payload: invalid hostname"
    )
  );
  assert.equal(invalidHost.status, 400);
  assert.equal(invalidHost.code, "invalid_hostname");
  assertNoInternalLeak(invalidHost.message);

  const taken = resolveDomainGatewayError(
    new GatewayError(
      "conflict",
      "domain_taken",
      "pq: duplicate key value violates unique constraint domains_host_key"
    )
  );
  assert.equal(taken.status, 409);
  assert.equal(taken.code, "domain_taken");
  assertNoInternalLeak(taken.message);

  const limit = resolveDomainGatewayError(
    new GatewayError("conflict", "domain_limit", "site already has a custom domain (at most 1)")
  );
  assert.equal(limit.status, 409);
  assert.equal(limit.code, "domain_limit");
  assertNoInternalLeak(limit.message);

  const notFound = resolveDomainGatewayError(
    new GatewayError("not_found", "NOT_FOUND", "custom_hostnames id cf-12345 not found")
  );
  assert.equal(notFound.status, 404);
  assert.equal(notFound.code, "not_found");
  assertNoInternalLeak(notFound.message);

  const unauth = resolveDomainGatewayError(
    new GatewayError("unauthenticated", "UNAUTHENTICATED", "jwt expired Bearer secret")
  );
  assert.equal(unauth.status, 401);
  assert.equal(unauth.code, "UNAUTHENTICATED");
  assertNoInternalLeak(unauth.message);

  const forbidden = resolveDomainGatewayError(
    new GatewayError("forbidden", "FORBIDDEN", "CF_API_TOKEN denied for zone")
  );
  assert.equal(forbidden.status, 403);
  assert.equal(forbidden.code, "FORBIDDEN");
  assertNoInternalLeak(forbidden.message);

  // Simulated 500 gateway response carrying sensitive provider details -> errorFromResponse + resolveDomainGatewayError
  const raw500 = new Response(
    JSON.stringify({
      error: {
        code: "CLOUDFLARE_UPSTREAM_500",
        message: "Cloudflare API Bearer cf_api_token_secret_999 failed: dial tcp 104.16.0.1:443",
        trace_id: "trace-abc-123",
      },
    }),
    { status: 500, headers: { "Content-Type": "application/json" } }
  );
  const gwErr = await errorFromResponse(raw500);
  assert.equal(gwErr.kind, "unavailable");
  assert.equal(gwErr.code, "UNAVAILABLE");

  const mapped500 = resolveDomainGatewayError(gwErr);
  assert.equal(mapped500.status, 503);
  assert.equal(mapped500.code, "UNAVAILABLE");
  assertNoInternalLeak(mapped500.message);
  assert.equal(mapped500.message.includes("cf_api_token_secret_999"), false);
  assert.equal(mapped500.message.includes("104.16.0.1"), false);
});

test("formatDomainErrorForSeller produces safe Vietnamese guidance without technical/Cloudflare leaks", () => {
  assert.equal(formatDomainErrorForSeller(""), "");
  assert.equal(formatDomainErrorForSeller(null), "");
  assert.equal(formatDomainErrorForSeller("   "), "");

  const samples = [
    "Cloudflare custom_hostnames verification expired after 7 days (CF_ZONE_ID=z123)",
    "Cloudflare SSL/TLS certificate validation failed: dial tcp timeout",
    "DNS TXT ownership_verification record mismatch for _cf-custom-hostname",
    "Cloudflare DELETE custom_hostname failed during remove: Bearer cf_live_token",
    "rpc error: code = Internal desc = grpc stack trace SQLSTATE 23505",
  ];

  for (const raw of samples) {
    const out = formatDomainErrorForSeller(raw);
    assert.ok(out.length > 10, `Expected meaningful Vietnamese guidance for: ${raw}`);
    assertNoInternalLeak(out);
    assert.equal(out.includes("cf_live_token"), false);
    assert.equal(out.includes("z123"), false);
  }

  assert.match(formatDomainErrorForSeller("expired after 7 days"), /7 ngày/);
  assert.match(formatDomainErrorForSeller("TLS cert validation pending"), /chứng chỉ bảo mật/i);
  assert.match(formatDomainErrorForSeller("DNS CNAME mismatch"), /bản ghi DNS/);
  assert.match(formatDomainErrorForSeller("delete failed"), /gỡ bỏ tên miền/);
});

test("getDomainStatusMeta returns plain Vietnamese labels for every contract status", () => {
  assert.deepEqual(getDomainStatusMeta("ACTIVE"), {
    label: "Đang hoạt động",
    tone: "pos",
    adminTone: "positive",
  });
  assert.deepEqual(getDomainStatusMeta("PENDING"), {
    label: "Đang chờ xác minh",
    tone: "warn",
    adminTone: "warning",
  });
  assert.deepEqual(getDomainStatusMeta("FAILED"), {
    label: "Xác minh thất bại",
    tone: "neg",
    adminTone: "negative",
  });
  assert.deepEqual(getDomainStatusMeta("REMOVING"), {
    label: "Đang gỡ bỏ",
    tone: "neutral",
    adminTone: "neutral",
  });
  assert.equal(getDomainStatusMeta("UNKNOWN").label, "Đang xử lý");
});
