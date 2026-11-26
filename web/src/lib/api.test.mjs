// Run: node --test src/lib/api.test.mjs
import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError, SESSION_EXPIRED_EVENT, api, apiBlob, refreshSessionOnce } from "./api.ts";

function installMockWindow() {
  const target = new EventTarget();
  let expiredEvents = 0;
  target.addEventListener(SESSION_EXPIRED_EVENT, () => {
    expiredEvents += 1;
  });
  const prevWindow = globalThis.window;
  globalThis.window = target;
  return {
    getExpiredCount: () => expiredEvents,
    restore() {
      if (prevWindow === undefined) {
        delete globalThis.window;
      } else {
        globalThis.window = prevWindow;
      }
    },
  };
}

function jsonRes(body, status = 200, headers = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

test("api() and apiBlob() MUST NOT emit SESSION_EXPIRED_EVENT on 429, 5xx, or network error during /api/auth/refresh", async () => {
  const win = installMockWindow();
  const origFetch = globalThis.fetch;

  try {
    // 1. 429 from /api/auth/refresh -> throws 429 ApiError, 0 session-expired events
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return jsonRes(
          {
            error: {
              code: "RATE_LIMITED",
              message: "Bạn thao tác hơi nhanh. Vui lòng thử lại sau ít phút.",
            },
          },
          429,
          { "Retry-After": "30" }
        );
      }
      return jsonRes(
        {
          error: {
            code: "UNAUTHENTICATED",
            message: "Phiên đăng nhập đã hết hạn. Bạn vui lòng đăng nhập lại.",
          },
        },
        401
      );
    };

    await assert.rejects(
      () => api("/api/orders"),
      (err) => err instanceof ApiError && err.status === 429 && err.code === "RATE_LIMITED"
    );
    assert.equal(win.getExpiredCount(), 0, "429 during refresh must NOT kick user out");

    // 2. 503 from /api/auth/refresh -> throws 503 ApiError, 0 session-expired events (api & apiBlob)
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return jsonRes(
          {
            error: {
              code: "UNAVAILABLE",
              message: "Hệ thống đang bận. Bạn vui lòng thử lại sau.",
            },
          },
          503
        );
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
    };

    await assert.rejects(
      () => api("/api/orders"),
      (err) => err instanceof ApiError && err.status === 503 && err.code === "UNAVAILABLE"
    );
    await assert.rejects(
      () => apiBlob("/api/shipping/labels/TRK123"),
      (err) => err instanceof ApiError && err.status === 503 && err.code === "UNAVAILABLE"
    );
    assert.equal(win.getExpiredCount(), 0, "503 during refresh must NOT kick user out");

    // 3. 500 from /api/auth/refresh -> throws 500 ApiError, 0 session-expired events
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return new Response("Internal Server Error", { status: 500 });
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
    };

    await assert.rejects(
      () => api("/api/cart"),
      (err) => err instanceof ApiError && err.status === 500 && err.code === "UNAVAILABLE"
    );
    assert.equal(win.getExpiredCount(), 0, "500 during refresh must NOT kick user out");

    // 4. Network error during /api/auth/refresh -> throws 0 NETWORK ApiError, 0 session-expired events
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        throw new TypeError("Failed to fetch");
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
    };

    await assert.rejects(
      () => api("/api/cart"),
      (err) => err instanceof ApiError && err.status === 0 && err.code === "NETWORK"
    );
    assert.equal(win.getExpiredCount(), 0, "network failure during refresh must NOT kick user out");
  } finally {
    globalThis.fetch = origFetch;
    win.restore();
  }
});

test("api() emits SESSION_EXPIRED_EVENT only when /api/auth/refresh returns 401/403 or post-refresh retry still returns 401", async () => {
  const win = installMockWindow();
  const origFetch = globalThis.fetch;

  try {
    // 1. /api/auth/refresh returns 401 -> emits SESSION_EXPIRED_EVENT
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return jsonRes(
          { error: { code: "UNAUTHENTICATED", message: "Phiên đăng nhập đã hết hạn." } },
          401
        );
      }
      return jsonRes(
        { error: { code: "UNAUTHENTICATED", message: "Phiên đăng nhập đã hết hạn." } },
        401
      );
    };

    await assert.rejects(
      () => api("/api/orders"),
      (err) => err instanceof ApiError && err.status === 401
    );
    assert.equal(win.getExpiredCount(), 1);

    // 2. /api/auth/refresh returns 403 -> emits SESSION_EXPIRED_EVENT
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return jsonRes({ error: { code: "FORBIDDEN", message: "Không có quyền." } }, 403);
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
    };

    await assert.rejects(
      () => api("/api/orders"),
      (err) => err instanceof ApiError && err.status === 401
    );
    assert.equal(win.getExpiredCount(), 2);

    // 3. /api/auth/refresh returns 200, retry returns 503 -> does NOT emit SESSION_EXPIRED_EVENT
    let orderCalls = 0;
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        return jsonRes({ ok: true }, 200);
      }
      orderCalls += 1;
      if (orderCalls === 1) {
        return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
      }
      return jsonRes({ error: { code: "UNAVAILABLE", message: "Hệ thống đang bận." } }, 503);
    };

    await assert.rejects(
      () => api("/api/orders"),
      (err) => err instanceof ApiError && err.status === 503
    );
    assert.equal(win.getExpiredCount(), 2, "503 on post-refresh retry must NOT kick user out");
  } finally {
    globalThis.fetch = origFetch;
    win.restore();
  }
});

test("single-flight deduplicates concurrent 401 refreshes for both 200 recovery and 429 transient errors", async () => {
  const win = installMockWindow();
  const origFetch = globalThis.fetch;

  try {
    // 1. Concurrent 401s + 200 refresh -> exactly ONE POST /api/auth/refresh and all succeed
    let refreshCalls = 0;
    const seen = new Map();
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        refreshCalls += 1;
        await new Promise((r) => setTimeout(r, 20));
        return jsonRes({ ok: true }, 200);
      }
      const n = (seen.get(url) ?? 0) + 1;
      seen.set(url, n);
      if (n === 1) {
        return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
      }
      return jsonRes({ ok: true, path: url }, 200);
    };

    const results = await Promise.all([
      api("/api/orders?a=1"),
      api("/api/orders?a=2"),
      api("/api/orders?a=3"),
    ]);
    assert.equal(refreshCalls, 1, "parallel 401s must share one single-flight refresh");
    assert.equal(results.length, 3);
    assert.equal(win.getExpiredCount(), 0);

    // 2. Concurrent 401s + 429 refresh -> ONE POST /api/auth/refresh, all reject 429, 0 expired events
    refreshCalls = 0;
    globalThis.fetch = async (url) => {
      if (url === "/api/auth/refresh") {
        refreshCalls += 1;
        await new Promise((r) => setTimeout(r, 20));
        return jsonRes(
          { error: { code: "RATE_LIMITED", message: "Bạn thao tác hơi nhanh." } },
          429
        );
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
    };

    const settled = await Promise.allSettled([api("/api/cart"), api("/api/orders")]);
    assert.equal(refreshCalls, 1, "parallel 401s hitting 429 must share one single-flight refresh");
    assert.equal(win.getExpiredCount(), 0);
    for (const item of settled) {
      assert.equal(item.status, "rejected");
      assert.ok(item.reason instanceof ApiError && item.reason.status === 429);
    }

    // 3. refreshSessionOnce() preserves boolean contract
    globalThis.fetch = async () => jsonRes({ ok: true }, 200);
    assert.equal(await refreshSessionOnce(), true);
    globalThis.fetch = async () => jsonRes({ error: { code: "RATE_LIMITED" } }, 429);
    assert.equal(await refreshSessionOnce(), false);
  } finally {
    globalThis.fetch = origFetch;
    win.restore();
  }
});

test("/api/auth/addresses*, PATCH /api/auth/me retry 401 via refresh, while /api/auth/login|logout|refresh never retry", async () => {
  const win = installMockWindow();
  const origFetch = globalThis.fetch;

  try {
    // 1. /api/auth/addresses and /api/auth/addresses/[id] and PATCH /api/auth/me retry on 401
    for (const reqSpec of [
      { path: "/api/auth/addresses", method: "GET" },
      { path: "/api/auth/addresses/addr-1", method: "PUT", body: { recipient_name: "An" } },
      { path: "/api/auth/me", method: "PATCH", body: { fullName: "Nguyen An" } },
    ]) {
      let calls = 0;
      let refreshed = 0;
      globalThis.fetch = async (url) => {
        if (url === "/api/auth/refresh") {
          refreshed += 1;
          return jsonRes({ ok: true }, 200);
        }
        calls += 1;
        if (calls === 1) {
          return jsonRes({ error: { code: "UNAUTHENTICATED", message: "Hết hạn" } }, 401);
        }
        return jsonRes({ ok: true }, 200);
      };

      const res = await api(reqSpec.path, { method: reqSpec.method, body: reqSpec.body });
      assert.deepEqual(res, { ok: true });
      assert.equal(refreshed, 1, `${reqSpec.method} ${reqSpec.path} must refresh on 401`);
      assert.equal(calls, 2, `${reqSpec.method} ${reqSpec.path} must retry after refresh`);
    }

    // 2. /api/auth/login, /api/auth/logout, /api/auth/refresh never trigger refresh or expired event
    for (const authPath of ["/api/auth/login", "/api/auth/logout", "/api/auth/refresh"]) {
      let refreshed = 0;
      globalThis.fetch = async (url) => {
        if (url === "/api/auth/refresh" && authPath !== "/api/auth/refresh") {
          refreshed += 1;
        }
        return jsonRes(
          { error: { code: "UNAUTHENTICATED", message: "Email hoặc mật khẩu chưa chính xác." } },
          401
        );
      };

      await assert.rejects(
        () => api(authPath, { method: "POST", body: {} }),
        (err) => err instanceof ApiError && err.status === 401
      );
      assert.equal(refreshed, 0, `${authPath} must NOT trigger /api/auth/refresh`);
    }
    assert.equal(win.getExpiredCount(), 0);
  } finally {
    globalThis.fetch = origFetch;
    win.restore();
  }
});
