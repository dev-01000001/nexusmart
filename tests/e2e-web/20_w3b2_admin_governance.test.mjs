import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, normalizeStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  cleanupEphemeralStaff,
  createAndActivateStaff,
  createEphemeralSeller,
  demoCredentials,
  loginOn,
  registerBuyer,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("20. Wave 3b-2 Admin Governance (RBAC 6 roles, passwordless staff onboarding, RoleRank F10, buyer activities F13 & two-person suspension F11/F12)", () => {
  let adminRoot;
  let adminUser;
  const ephemeralStaff = [];

  before(async () => {
    await resetRateLimitKeys();
    adminRoot = new Browser("gov20-superadmin").at(ROOT_HOST);
    const login = await loginOn(
      adminRoot,
      demoCredentials.admin.email,
      demoCredentials.admin.password,
      "gov20-superadmin"
    );
    adminUser = login.user;
  });

  after(async () => {
    await cleanupEphemeralStaff(adminRoot, ephemeralStaff);
    await Promise.allSettled([
      ...ephemeralStaff.map((s) => s.client.post("/api/auth/logout", {})),
      adminRoot.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("1. [F9/F10/F13/F14] Passwordless staff onboarding, SSR route guards, capability enforcement, RoleRank F10 & buyer activities", async () => {
    const moderator = await createAndActivateStaff(adminRoot, "moderator", "mod20");
    ephemeralStaff.push(moderator);
    const accountant = await createAndActivateStaff(adminRoot, "accountant", "acc20");
    ephemeralStaff.push(accountant);

    // Admin SSR guards: forbidden routes must either return 404 (notFound) OR 200 with AdminShell's
    // "Không đủ quyền truy cập" access-denied view and ZERO leaked operational data.
    expectStatus(await moderator.client.page("/admin"), 200, "moderator GET /admin");
    expectStatus(await moderator.client.page("/admin/gian-hang"), 200, "moderator GET /admin/gian-hang");
    expectStatus(await moderator.client.page("/admin/kiem-duyet"), 200, "moderator GET /admin/kiem-duyet");

    const modRecon = await moderator.client.page("/admin/doi-soat");
    assert.ok(
      modRecon.status === 404 ||
        (modRecon.status === 200 && modRecon.text.includes("Không đủ quyền truy cập")),
      `moderator GET /admin/doi-soat must return 404 or 200 access-denied shell, got HTTP ${modRecon.status}`
    );
    for (const leaked of ["Tổng nợ", "Tổng có", "Hàng đợi đối soát", "Đối soát COD", "Chuyển khoản hoàn tiền"]) {
      assert.equal(
        modRecon.text.includes(leaked),
        false,
        `moderator /admin/doi-soat leaked financial/reconciliation content: "${leaked}"`
      );
    }

    expectStatus(await accountant.client.page("/admin/doi-soat"), 200, "accountant GET /admin/doi-soat");
    const accMod = await accountant.client.page("/admin/kiem-duyet");
    assert.ok(
      accMod.status === 404 ||
        (accMod.status === 200 && accMod.text.includes("Không đủ quyền truy cập")),
      `accountant GET /admin/kiem-duyet must return 404 or 200 access-denied shell, got HTTP ${accMod.status}`
    );

    // API capability enforcement: positive and negative 403.
    expectStatus(await moderator.client.get("/api/admin/ledger"), 403, "moderator GET /api/admin/ledger -> 403");
    expectStatus(await accountant.client.get("/api/admin/ledger"), 200, "accountant GET /api/admin/ledger -> 200");
    expectStatus(await accountant.client.get("/api/admin/banned-keywords"), 403, "accountant GET /api/admin/banned-keywords -> 403");
    expectStatus(await moderator.client.get("/api/admin/banned-keywords"), 200, "moderator GET /api/admin/banned-keywords -> 200");

    // F10 RoleRank guard: moderator cannot lock super_admin.
    const lockAdmin = await moderator.client.patch(`/api/admin/users/${encodeURIComponent(adminUser.id)}/status`, {
      status: "LOCKED",
      reason: "F10 RoleRank escalation attempt",
    });
    expectStatus(lockAdmin, 403, "moderator locking super_admin must return 403");

    // F13 Buyer activities endpoint.
    const buyerSite = new Browser("gov20-buyer").at(SITE_A_HOST);
    try {
      const buyer = await registerBuyer(buyerSite, "gov20-b");
      const act = expectStatus(
        await adminRoot.get(`/api/admin/users/${encodeURIComponent(buyer.user.id)}/activities`),
        200,
        "GET /api/admin/users/[id]/activities"
      );
      assert.ok(act !== undefined, "activities response must be valid JSON");
    } finally {
      await buyerSite.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("2. [F11/F12] Two-person shop suspension (maker ops1 != checker ops2), self-approve 403, audit logs & unsuspend cleanup", async () => {
    const ops1 = await createAndActivateStaff(adminRoot, "ops", "ops20-a");
    ephemeralStaff.push(ops1);
    const ops2 = await createAndActivateStaff(adminRoot, "ops", "ops20-b");
    ephemeralStaff.push(ops2);

    const eph = await createEphemeralSeller("gov20-shop");
    let suspended = false;

    try {
      // Maker (ops1) initiates suspension request -> pending approval.
      const reqSusp = expectStatus(
        await ops1.client.post(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/suspend`, {
          reason: "E2E 20 kiểm thử phê duyệt hai người (maker-checker)",
        }),
        [200, 201, 202],
        "ops1 POST /api/admin/shops/[id]/suspend"
      );
      const approvalId = reqSusp.approval?.id ?? reqSusp.id;
      assert.ok(approvalId, `expected approval id from suspend request: ${JSON.stringify(reqSusp)}`);

      // Maker (ops1) attempting to self-approve -> 403.
      const selfApprove = await ops1.client.post(
        `/api/admin/approvals/shop/${encodeURIComponent(approvalId)}/decide`,
        { decision: "APPROVE", note: "Self-approval must fail" }
      );
      expectStatus(selfApprove, 403, "maker self-approval must return 403");

      // Checker (ops2) approves -> shop transitions to SUSPENDED.
      const decided = expectStatus(
        await ops2.client.post(`/api/admin/approvals/shop/${encodeURIComponent(approvalId)}/decide`, {
          decision: "APPROVE",
          note: "Checker ops2 xác nhận tạm ngưng gian hàng",
        }),
        200,
        "ops2 approve shop suspension"
      );
      assert.equal(normalizeStatus(decided.shop?.status), "SUSPENDED");
      suspended = true;

      // Audit logs filter by date returns recorded actions.
      const today = new Date(Date.now() + 7 * 3600e3).toISOString().slice(0, 10);
      const logs = expectStatus(
        await adminRoot.get(`/api/admin/audit-logs?from=${today}&to=${today}&page=1&page_size=20`),
        200,
        "GET /api/admin/audit-logs with date filter"
      );
      assert.ok(Array.isArray(logs.items) && logs.items.length > 0, "audit logs must record suspension approval");

      // Unsuspend cleanup restores shop to ACTIVE.
      const unsusp = expectStatus(
        await adminRoot.post(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/unsuspend`, {
          reason: "E2E 20 cleanup unsuspend",
        }),
        200,
        "POST /api/admin/shops/[id]/unsuspend"
      );
      assert.equal(normalizeStatus(unsusp.shop?.status), "ACTIVE");
      suspended = false;
    } finally {
      if (suspended) {
        await adminRoot
          .post(`/api/admin/shops/${encodeURIComponent(eph.shopId)}/unsuspend`, {
            reason: "E2E 20 finally cleanup",
          })
          .catch(() => {});
      }
      await eph.client.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
