import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  WebClient,
  config,
  createBuyerAddress,
  ensureRateBudget,
  normalizeStatus,
  pollUntil,
  registerAndLoginBuyer,
  snapshotMailpitIds,
  uniqueEmail,
  uniquePhone,
} from "./helpers.mjs";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";
const STAFF_PASSWORD = "NexusStaffE2E123!";

async function loginAccount(email, password) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `login ${email} expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

function extractResetToken(...parts) {
  const combined = parts.filter(Boolean).join("\n");
  const linkMatch = /\/dat-lai-mat-khau\?token=([^&\s"'<>]+)/i.exec(combined);
  if (linkMatch?.[1]) {
    try {
      return decodeURIComponent(linkMatch[1].replace(/[.,;)]+$/, ""));
    } catch {
      return linkMatch[1].replace(/[.,;)]+$/, "");
    }
  }
  const paramMatch = /[?&]token=([A-Za-z0-9._~%-]{16,})/i.exec(combined);
  if (paramMatch?.[1]) {
    try {
      return decodeURIComponent(paramMatch[1]);
    } catch {
      return paramMatch[1];
    }
  }
  return "";
}

/**
 * Polls Mailpit for the password-reset link sent after POST /api/gw/admin/staff (F14).
 * Never logs the reset token value.
 */
async function waitForPasswordResetToken(email, seenIds = new Set()) {
  const target = email.trim().toLowerCase();
  return pollUntil(
    async () => {
      const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      if (!listRes.ok) {
        return { done: false, detail: `mailpit list HTTP ${listRes.status}` };
      }
      const list = await listRes.json();
      const messages = Array.isArray(list?.messages) ? list.messages : [];
      for (const summary of messages) {
        if (summary?.ID && seenIds.has(summary.ID)) continue;
        const recipients = Array.isArray(summary?.To) ? summary.To : [];
        const matched = recipients.some(
          (r) => String(r?.Address ?? "").trim().toLowerCase() === target
        );
        if (!matched) continue;

        const msgRes = await fetch(
          `${config.mailpitUrl}/api/v1/message/${encodeURIComponent(summary.ID)}`,
          { signal: AbortSignal.timeout(config.requestTimeoutMs) }
        );
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const token = extractResetToken(full?.Text, full?.HTML, summary?.Snippet);
        if (token) {
          if (summary?.ID) seenIds.add(summary.ID);
          return { done: true, value: token };
        }
      }
      return { done: false, detail: `no password reset email for ${target} yet` };
    },
    {
      timeoutMs: config.pollTimeoutMs,
      intervalMs: config.pollIntervalMs,
      label: `password reset email for ${target}`,
    }
  );
}

/**
 * Creates a passwordless internal staff account via F14 (POST /api/gw/admin/staff),
 * sets its password through the real reset-password email flow, and signs in.
 */
async function createAndActivateStaff(superAdminClient, role, label) {
  const email = uniqueEmail(`staff-${label}`);
  const phone = uniquePhone();
  const fullName = `Nhân viên E2E ${label}`;
  const seenIds = await snapshotMailpitIds(email);

  const createRes = await superAdminClient.json("POST", "/api/gw/admin/staff", {
    body: {
      full_name: fullName,
      email,
      phone,
      role,
    },
  });
  assert.equal(
    createRes.status,
    201,
    `POST /api/gw/admin/staff (${role}) expected 201, got ${createRes.status}: ${createRes.text}`
  );
  assert.equal(
    createRes.data?.password_reset_sent,
    true,
    `expected password_reset_sent=true for ${role}: ${createRes.text}`
  );
  assert.equal(normalizeStatus(createRes.data?.user?.role), role.toUpperCase());

  const resetToken = await waitForPasswordResetToken(email, seenIds);
  const resetClient = new WebClient();
  const resetRes = await resetClient.json("POST", "/api/auth/reset-password", {
    body: {
      token: resetToken,
      new_password: STAFF_PASSWORD,
    },
  });
  assert.equal(
    resetRes.status,
    200,
    `POST /api/auth/reset-password (${role}) expected 200, got ${resetRes.status}: ${resetRes.text}`
  );

  const { client, user } = await loginAccount(email, STAFF_PASSWORD);
  assert.equal(normalizeStatus(user?.role), role.toUpperCase());
  return { client, user, email, phone };
}

async function cleanupEphemeralStaff(superAdminClient, staffList) {
  for (const staff of staffList) {
    const uid = staff?.user?.id;
    if (!uid) continue;
    await superAdminClient
      .json("PATCH", `/api/gw/admin/users/${encodeURIComponent(uid)}/role`, {
        body: { role: "buyer", reason: "E2E 12 cleanup demote ephemeral staff" },
      })
      .catch(() => {});
    await superAdminClient
      .json("PATCH", `/api/gw/admin/users/${encodeURIComponent(uid)}/status`, {
        body: { status: "LOCKED", reason: "E2E 12 cleanup lock ephemeral staff" },
      })
      .catch(() => {});
  }
}

describe("12. Wave 3b-2 A7 (6 internal staff roles, passwordless staff creation, RBAC & zero-403 routes, buyer activity log, two-person shop suspension)", () => {
  test("1. [A7/F9/F10/F14/F15] Passwordless staff creation (moderator & accountant), admin route guard, and capability enforcement", async () => {
    await ensureRateBudget(55, { label: "w3b2-a7-rbac" });
    const { client: superAdmin, user: superAdminUser } = await loginAccount(
      ADMIN_EMAIL,
      ADMIN_PASSWORD
    );
    assert.equal(
      normalizeStatus(superAdminUser?.role),
      "SUPER_ADMIN",
      `seeded admin expected super_admin role, got ${superAdminUser?.role}`
    );

    const createdStaff = [];
    try {
      const moderator = await createAndActivateStaff(superAdmin, "moderator", "mod");
      createdStaff.push(moderator);
      const accountant = await createAndActivateStaff(superAdmin, "accountant", "acc");
      createdStaff.push(accountant);

      // Moderator SSR access: allowed routes render 200; forbidden route /admin/doi-soat renders 200
      // with AdminShell zero-403 access denied view (no redirect to login, no uncaught error)
      const modOverview = await moderator.client.page("/admin");
      assert.equal(modOverview.status, 200, `moderator /admin expected 200, got ${modOverview.status}`);

      const modShops = await moderator.client.page("/admin/gian-hang");
      assert.equal(
        modShops.status,
        200,
        `moderator /admin/gian-hang expected 200, got ${modShops.status}`
      );

      const modModeration = await moderator.client.page("/admin/kiem-duyet");
      assert.equal(
        modModeration.status,
        200,
        `moderator /admin/kiem-duyet expected 200, got ${modModeration.status}`
      );

      const modForbiddenPage = await moderator.client.page("/admin/doi-soat");
      assert.equal(
        modForbiddenPage.status,
        200,
        `moderator /admin/doi-soat shell expected 200, got ${modForbiddenPage.status}`
      );
      assert.ok(
        modForbiddenPage.text.includes("Không đủ quyền truy cập"),
        "moderator visiting /admin/doi-soat must see AdminShell access denied message"
      );

      // Moderator capability checks (positive & negative)
      const modListShops = await moderator.client.json("GET", "/api/gw/admin/shops?page=1&page_size=5");
      assert.equal(modListShops.status, 200, `moderator GET /admin/shops expected 200: ${modListShops.text}`);

      const modListProducts = await moderator.client.json(
        "GET",
        "/api/gw/admin/products?page=1&page_size=5"
      );
      assert.equal(
        modListProducts.status,
        200,
        `moderator GET /admin/products expected 200: ${modListProducts.text}`
      );

      const modLedger = await moderator.client.json("GET", "/api/gw/admin/ledger/summary");
      assert.equal(modLedger.status, 403, `moderator GET /admin/ledger/summary must be 403`);

      const modReports = await moderator.client.json("GET", "/api/gw/admin/reports");
      assert.equal(modReports.status, 403, `moderator GET /admin/reports must be 403`);

      const modCreateStaff = await moderator.client.json("POST", "/api/gw/admin/staff", {
        body: {
          full_name: "Unauthorized Staff",
          email: uniqueEmail("unauth-staff"),
          phone: uniquePhone(),
          role: "cskh",
        },
      });
      assert.equal(modCreateStaff.status, 403, `moderator POST /admin/staff must be 403`);

      // F10 RoleRank guard: moderator cannot lock super_admin
      const modLockSuper = await moderator.client.json(
        "PATCH",
        `/api/gw/admin/users/${encodeURIComponent(superAdminUser.id)}/status`,
        { body: { status: "LOCKED", reason: "must fail" } }
      );
      assert.equal(modLockSuper.status, 403, `moderator locking super_admin must be 403`);

      // Accountant capability checks (positive & negative)
      const accLedger = await accountant.client.json("GET", "/api/gw/admin/ledger/summary");
      assert.equal(accLedger.status, 200, `accountant GET /admin/ledger/summary expected 200: ${accLedger.text}`);

      const accPaymentMethods = await accountant.client.json("GET", "/api/gw/admin/payment-methods");
      assert.equal(
        accPaymentMethods.status,
        200,
        `accountant GET /admin/payment-methods expected 200: ${accPaymentMethods.text}`
      );

      const accCarriers = await accountant.client.json("GET", "/api/gw/admin/carriers");
      assert.equal(accCarriers.status, 403, `accountant GET /admin/carriers must be 403`);

      const accCreateStaff = await accountant.client.json("POST", "/api/gw/admin/staff", {
        body: {
          full_name: "Unauthorized Staff 2",
          email: uniqueEmail("unauth-staff2"),
          phone: uniquePhone(),
          role: "ops",
        },
      });
      assert.equal(accCreateStaff.status, 403, `accountant POST /admin/staff must be 403`);
    } finally {
      await cleanupEphemeralStaff(superAdmin, createdStaff);
    }
  });

  test("2. [A7/F13] Admin views buyer activity history (login timeline, cod_rejected_count, recent_orders)", async () => {
    await ensureRateBudget(30, { label: "w3b2-a7-activities" });
    const { client: superAdmin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    const buyer = new WebClient();
    const buyerAcct = await registerAndLoginBuyer(buyer, "w3b2-act");

    const actRes = await superAdmin.json(
      "GET",
      `/api/gw/admin/users/${encodeURIComponent(buyerAcct.userId)}/activities?page=1&page_size=20`
    );
    assert.equal(
      actRes.status,
      200,
      `GET /admin/users/{id}/activities expected 200, got ${actRes.status}: ${actRes.text}`
    );
    assert.ok(Array.isArray(actRes.data?.items), `expected items array: ${actRes.text}`);
    assert.ok(
      actRes.data.items.length >= 1,
      `expected >= 1 activity entry after buyer login: ${actRes.text}`
    );
    assert.equal(
      typeof actRes.data?.cod_rejected_count,
      "number",
      `expected numeric cod_rejected_count: ${actRes.text}`
    );
    assert.ok(
      Array.isArray(actRes.data?.recent_orders),
      `expected recent_orders array: ${actRes.text}`
    );
  });

  test("3. [A7/F11/F12] Two-person shop suspension on an isolated test shop (ops1 maker != ops2 checker), audit log date filter, and unsuspension cleanup", async () => {
    await ensureRateBudget(80, { label: "w3b2-a7-two-person" });
    const { client: superAdmin } = await loginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);

    const createdStaff = [];
    let ops1 = null;
    let ops2 = null;
    let targetShop = null;
    let approvalId = null;
    let shopSuspended = false;

    try {
      ops1 = await createAndActivateStaff(superAdmin, "ops", "ops1");
      createdStaff.push(ops1);
      ops2 = await createAndActivateStaff(superAdmin, "ops", "ops2");
      createdStaff.push(ops2);

      // Create & approve an isolated shop specifically for this test so no seeded shop or other test's shop is touched
      const shopOwnerClient = new WebClient();
      const shopTag = crypto.randomBytes(3).toString("hex");
      const shopOwnerName = `Chu Shop E2E12 ${shopTag}`;
      const shopOwner = await registerAndLoginBuyer(shopOwnerClient, "w3b2-gov-shop", {
        fullName: shopOwnerName,
      });
      const { address: pickupAddress } = await createBuyerAddress(shopOwnerClient, shopOwner);

      const regShopRes = await shopOwnerClient.json("POST", "/api/gw/shops/register", {
        body: {
          name: `Xuong E2E12 ${shopTag}`,
          description: "Gian hàng cô lập dùng riêng cho bài kiểm thử duyệt 2 người đợt 3b-2.",
          pickup_address_id: pickupAddress.id,
          contact_phone: shopOwner.phone,
          business_license_doc: `GPKD-E2E12-${shopTag.toUpperCase()}`,
        },
      });
      assert.equal(
        regShopRes.status,
        201,
        `register isolated shop expected 201, got ${regShopRes.status}: ${regShopRes.text}`
      );
      const createdShop = regShopRes.data?.shop;
      assert.ok(createdShop?.id, `missing created shop id: ${regShopRes.text}`);

      const approveShopRes = await ops1.client.json(
        "POST",
        `/api/gw/admin/shops/${encodeURIComponent(createdShop.id)}/review`,
        {
          body: {
            decision: "APPROVE",
            reason: "E2E 12 approve isolated shop before suspension test",
            expected_owner_name: shopOwnerName,
          },
        }
      );
      assert.equal(
        approveShopRes.status,
        200,
        `approve isolated shop failed (${approveShopRes.status}): ${approveShopRes.text}`
      );
      targetShop = approveShopRes.data?.shop ?? createdShop;
      assert.equal(
        normalizeStatus(targetShop.status),
        "ACTIVE",
        `expected isolated shop to be ACTIVE: ${approveShopRes.text}`
      );

      const windowStartIso = new Date(Date.now() - 60_000).toISOString();

      // Maker (ops1) requests shop suspension -> 201 PENDING; shop remains ACTIVE
      const reqRes = await ops1.client.json(
        "POST",
        `/api/gw/admin/shops/${encodeURIComponent(targetShop.id)}/suspend`,
        { body: { reason: "E2E 12 two-person suspension verification" } }
      );
      assert.equal(
        reqRes.status,
        201,
        `ops1 POST /suspend expected 201, got ${reqRes.status}: ${reqRes.text}`
      );
      approvalId = reqRes.data?.approval?.id ?? null;
      assert.ok(approvalId, `suspend response missing approval.id: ${reqRes.text}`);
      assert.equal(normalizeStatus(reqRes.data?.approval?.status), "PENDING");

      // Self-approval by maker (ops1) must be rejected with 403 (F11 maker != checker)
      const selfApproveRes = await ops1.client.json(
        "POST",
        `/api/gw/admin/approvals/shop/${encodeURIComponent(approvalId)}/decide`,
        { body: { decision: "APPROVE", note: "self-approve attempt must fail" } }
      );
      assert.equal(
        selfApproveRes.status,
        403,
        `maker self-approval must return 403, got ${selfApproveRes.status}: ${selfApproveRes.text}`
      );

      // Checker (ops2) sees pending request in GET /api/gw/admin/approvals
      const queueRes = await ops2.client.json(
        "GET",
        "/api/gw/admin/approvals?type=shop&status=PENDING&page=1&page_size=50"
      );
      assert.equal(queueRes.status, 200, `ops2 GET /admin/approvals failed: ${queueRes.text}`);
      const shopApprovals = Array.isArray(queueRes.data?.shop_approvals)
        ? queueRes.data.shop_approvals
        : [];
      const pendingEntry = shopApprovals.find((a) => a.id === approvalId);
      assert.ok(pendingEntry, `pending approval ${approvalId} not found in GET /admin/approvals`);

      // Checker (ops2) approves -> shop transitions to SUSPENDED
      const approveRes = await ops2.client.json(
        "POST",
        `/api/gw/admin/approvals/shop/${encodeURIComponent(approvalId)}/decide`,
        { body: { decision: "APPROVE", note: "E2E 12 checker verified" } }
      );
      assert.equal(
        approveRes.status,
        200,
        `ops2 approve shop suspension expected 200, got ${approveRes.status}: ${approveRes.text}`
      );
      shopSuspended = true;
      approvalId = null; // no longer pending
      const suspendedShop = approveRes.data?.shop;
      assert.equal(
        normalizeStatus(suspendedShop?.status),
        "SUSPENDED",
        `expected shop to become SUSPENDED: ${approveRes.text}`
      );

      // Audit log with from_time/to_time filter captures request & approval
      const windowEndIso = new Date(Date.now() + 60_000).toISOString();
      const auditRes = await ops2.client.json(
        "GET",
        `/api/gw/admin/audit-logs?from_time=${encodeURIComponent(windowStartIso)}&to_time=${encodeURIComponent(windowEndIso)}&page=1&page_size=50`
      );
      assert.equal(auditRes.status, 200, `audit-logs date filter failed: ${auditRes.text}`);
      const auditItems = Array.isArray(auditRes.data?.items) ? auditRes.data.items : [];
      assert.ok(
        auditItems.some((item) => item.target_id === targetShop.id || String(item.reason ?? "").includes("E2E 12")),
        "expected audit log entries for two-person shop suspension in filtered date range"
      );
    } finally {
      if (approvalId && ops2) {
        await ops2.client
          .json("POST", `/api/gw/admin/approvals/shop/${encodeURIComponent(approvalId)}/decide`, {
            body: { decision: "REJECT", note: "E2E 12 cleanup reject pending" },
          })
          .catch(() => {});
      }
      if (shopSuspended && targetShop?.id && ops2) {
        const unsuspendRes = await ops2.client.json(
          "POST",
          `/api/gw/admin/shops/${encodeURIComponent(targetShop.id)}/unsuspend`,
          { body: { reason: "E2E 12 cleanup restore shop" } }
        );
        assert.equal(
          unsuspendRes.status,
          200,
          `cleanup unsuspend shop ${targetShop.id} failed (${unsuspendRes.status}): ${unsuspendRes.text}`
        );
        assert.equal(normalizeStatus(unsuspendRes.data?.shop?.status), "ACTIVE");
      }
      await cleanupEphemeralStaff(superAdmin, createdStaff);
    }
  });
});
