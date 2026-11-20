// Run: node --import 'data:text/javascript,import{register}from"node:module";import{pathToFileURL}from"node:url";register(pathToFileURL("tools/seed/ts-resolve.mjs"));' --test web/src/lib/server/security.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { GatewayError, SAFE_MESSAGES, errorFromResponse, errorResponse } from "./errors.ts";
import {
  assertSameOrigin,
  buildLoginRedirectUrl,
  resolveForwardedClientIp,
  resolveRequestHost,
  resolveRequestProto,
} from "./http.ts";
import {
  ACCESS_COOKIE,
  COOKIE_SECURE,
  REFRESH_COOKIE,
  TRUST_PROXY_HEADERS,
  baseCookie,
} from "./config.ts";
import {
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  setAuthCookies,
} from "./tokens.ts";
import { toAddressItem, toGatewayAddressBody } from "./address.ts";

function makeReq(url, headers = {}, method = "POST") {
  return new Request(url, { method, headers });
}

test("assertSameOrigin accepts matching same-origin Origin header", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
    origin: "http://127.0.0.1:3201",
    "sec-fetch-site": "same-origin",
  });
  assert.doesNotThrow(() => assertSameOrigin(req));
});

test("assertSameOrigin rejects foreign Origin with 403 BAD_ORIGIN", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
    origin: "https://evil.example",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin rejects Origin: null", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/logout", {
    host: "127.0.0.1:3201",
    origin: "null",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin rejects cross-site and same-site Sec-Fetch-Site", () => {
  for (const sfs of ["cross-site", "same-site"]) {
    const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
      host: "127.0.0.1:3201",
      origin: "http://127.0.0.1:3201",
      "sec-fetch-site": sfs,
    });
    assert.throws(
      () => assertSameOrigin(req),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
    );
  }
});

test("assertSameOrigin rejects missing Origin and Referer", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin rejects protocol mismatch between Origin and request", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
    origin: "https://127.0.0.1:3201",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin accepts same-origin Referer only when Origin header is absent", () => {
  const okReq = makeReq("http://127.0.0.1:3201/api/auth/logout", {
    host: "127.0.0.1:3201",
    referer: "http://127.0.0.1:3201/tai-khoan",
  });
  assert.doesNotThrow(() => assertSameOrigin(okReq));

  const badReq = makeReq("http://127.0.0.1:3201/api/auth/logout", {
    host: "127.0.0.1:3201",
    referer: "https://evil.example/phish",
  });
  assert.throws(
    () => assertSameOrigin(badReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("errorFromResponse and errorResponse never leak gateway internals or trace_id", async () => {
  const fakeRes = new Response(
    JSON.stringify({
      error: {
        code: "INTERNAL_DB_FAILURE",
        message: "pq: password authentication failed at 10.0.0.5:5432",
        trace_id: "abc-trace-secret",
      },
    }),
    { status: 500, headers: { "Content-Type": "application/json" } }
  );
  const gwErr = await errorFromResponse(fakeRes);
  assert.equal(gwErr.status, 503);
  assert.equal(gwErr.code, "UNAVAILABLE");

  const clientRes = errorResponse(gwErr);
  assert.equal(clientRes.status, 503);
  assert.equal(clientRes.headers.get("cache-control"), "no-store");
  const body = await clientRes.json();
  assert.deepEqual(body, {
    error: { code: "UNAVAILABLE", message: SAFE_MESSAGES.unavailable },
  });
  assert.equal(JSON.stringify(body).includes("pq:"), false);
  assert.equal(JSON.stringify(body).includes("trace"), false);
});

test("cookie configuration is HttpOnly, SameSite=Lax, host-only (no Domain)", () => {
  assert.equal(baseCookie.httpOnly, true);
  assert.equal(baseCookie.sameSite, "lax");
  assert.equal(baseCookie.path, "/");
  assert.equal("domain" in baseCookie, false);
  assert.equal(baseCookie.secure, COOKIE_SECURE);

  const written = [];
  const jar = { set: (name, value, options) => written.push({ name, value, options }) };
  setAuthCookies(jar, { accessToken: "at-1", refreshToken: "rt-1", expiresIn: 900 });
  assert.equal(written.length, 2);
  assert.equal(written[0].name, ACCESS_COOKIE);
  assert.equal(written[0].options.httpOnly, true);
  assert.equal(written[0].options.maxAge, 900);
  assert.equal("domain" in written[0].options, false);
  assert.equal(written[1].name, REFRESH_COOKIE);
  assert.equal(written[1].options.httpOnly, true);

  written.length = 0;
  clearAuthCookies(jar);
  assert.equal(written[0].options.maxAge, 0);
  assert.equal(written[1].options.maxAge, 0);
});

test("accessExpiresSoon detects missing, malformed, expired, and valid JWTs", () => {
  const mkJwt = (exp) => {
    const h = Buffer.from(JSON.stringify({ alg: "HS256" })).toString("base64url");
    const p = Buffer.from(JSON.stringify({ exp })).toString("base64url");
    return `${h}.${p}.sig`;
  };
  const now = Math.floor(Date.now() / 1000);
  assert.equal(accessExpiresSoon(undefined), true);
  assert.equal(accessExpiresSoon("not-a-jwt"), true);
  assert.equal(accessExpiresSoon(mkJwt(now + 10)), true);
  assert.equal(accessExpiresSoon(mkJwt(now + 600)), false);
});

test("applyAuthCookiesToHeader replaces or removes auth cookies while preserving others", () => {
  const orig = `theme=light; ${ACCESS_COOKIE}=old_at; other=1; ${REFRESH_COOKIE}=old_rt`;
  const replaced = applyAuthCookiesToHeader(orig, {
    accessToken: "new_at",
    refreshToken: "new_rt",
    expiresIn: 900,
  });
  assert.equal(
    replaced,
    `theme=light; other=1; ${ACCESS_COOKIE}=new_at; ${REFRESH_COOKIE}=new_rt`
  );

  const cleared = applyAuthCookiesToHeader(orig, null);
  assert.equal(cleared, "theme=light; other=1");
});

test("toGatewayAddressBody normalizes camelCase/snake_case and never includes user_id", () => {
  const payload = toGatewayAddressBody({
    recipientName: "  Nguyễn An ",
    recipientPhone: "0909123456",
    provinceCode: "01",
    provinceName: "Hà Nội",
    wardCode: "00001",
    wardName: "Phường Phúc Xá",
    streetAddress: "12 Phố Huế",
    isDefault: true,
  });
  assert.equal(payload.recipient_name, "Nguyễn An");
  assert.equal(payload.ward_code, "00001");
  assert.equal(payload.is_default, true);
  assert.equal("user_id" in payload, false);

  const item = toAddressItem({
    id: "addr-1",
    user_id: "u-1",
    recipient_name: "Nguyễn An",
    recipient_phone: "0909123456",
    province_code: "01",
    province_name: "Hà Nội",
    ward_code: "00001",
    ward_name: "Phường Phúc Xá",
    street_address: "12 Phố Huế",
    is_default: true,
  });
  assert.equal(item.recipientName, "Nguyễn An");
  assert.equal(item.isDefault, true);
});

test("TRUST_PROXY_HEADERS defaults to false and ignores spoofed x-forwarded-host/proto in CSRF checks", () => {
  assert.equal(TRUST_PROXY_HEADERS, false);

  const spoofedHostReq = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-host": "evil.example",
    "x-forwarded-proto": "https",
    origin: "https://evil.example",
  });
  assert.throws(
    () => assertSameOrigin(spoofedHostReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );

  const spoofedProtoReq = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-proto": "https",
    origin: "https://localhost:3300",
  });
  assert.throws(
    () => assertSameOrigin(spoofedProtoReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );

  // Legitimate request matching Host + http still succeeds even if attacker injects x-forwarded-*.
  const legitReqWithSpoofedHeaders = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-host": "evil.example",
    "x-forwarded-proto": "https",
    origin: "http://localhost:3300",
  });
  assert.doesNotThrow(() => assertSameOrigin(legitReqWithSpoofedHeaders));
});

test("assertSameOrigin honors x-forwarded-host/proto ONLY when TRUST_PROXY_HEADERS=true", () => {
  const prev = process.env.TRUST_PROXY_HEADERS;
  try {
    process.env.TRUST_PROXY_HEADERS = "true";

    const proxiedReq = makeReq("http://web:3000/api/checkout", {
      host: "web:3000",
      "x-forwarded-host": "nexusmart.vn, internal-lb",
      "x-forwarded-proto": "https, http",
      origin: "https://nexusmart.vn",
    });
    assert.doesNotThrow(() => assertSameOrigin(proxiedReq));

    // When proxy headers are trusted, the internal container origin no longer matches.
    const internalOriginReq = makeReq("http://web:3000/api/checkout", {
      host: "web:3000",
      "x-forwarded-host": "nexusmart.vn",
      "x-forwarded-proto": "https",
      origin: "http://web:3000",
    });
    assert.throws(
      () => assertSameOrigin(internalOriginReq),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
    );
  } finally {
    if (prev === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prev;
  }
});

test("buildLoginRedirectUrl and proxy helpers ignore x-forwarded-* by default and honor when TRUST_PROXY_HEADERS=true", () => {
  const prev = process.env.TRUST_PROXY_HEADERS;
  try {
    delete process.env.TRUST_PROXY_HEADERS;
    const req = makeReq(
      "http://localhost:3300/seller/don-hang?status=CONFIRMED",
      {
        host: "localhost:3300",
        "x-forwarded-host": "evil.example",
        "x-forwarded-proto": "https",
        "x-forwarded-for": "203.0.113.9, 10.0.0.1",
        "x-real-ip": "203.0.113.10",
      },
      "GET"
    );

    assert.equal(resolveRequestHost(req.headers, "fallback"), "localhost:3300");
    assert.equal(resolveRequestProto(req.headers, req.url), "http");
    assert.equal(resolveForwardedClientIp(req.headers), null);
    assert.equal(
      buildLoginRedirectUrl(req).toString(),
      "http://localhost:3300/dang-nhap?next=%2Fseller%2Fdon-hang%3Fstatus%3DCONFIRMED"
    );

    process.env.TRUST_PROXY_HEADERS = "true";
    assert.equal(resolveRequestHost(req.headers, "fallback"), "evil.example");
    assert.equal(resolveRequestProto(req.headers, req.url), "https");
    assert.equal(resolveForwardedClientIp(req.headers), "203.0.113.9");
    assert.equal(
      buildLoginRedirectUrl(req).toString(),
      "https://evil.example/dang-nhap?next=%2Fseller%2Fdon-hang%3Fstatus%3DCONFIRMED"
    );

    // Invalid x-forwarded-proto is rejected even when TRUST_PROXY_HEADERS=true.
    const badProtoHeaders = new Headers({
      host: "localhost:3300",
      "x-forwarded-proto": "javascript",
    });
    assert.equal(resolveRequestProto(badProtoHeaders, "http://localhost:3300/tai-khoan"), "http");
  } finally {
    if (prev === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prev;
  }
});
