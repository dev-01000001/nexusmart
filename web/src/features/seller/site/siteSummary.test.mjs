// Run: node --test web/src/features/seller/site/siteSummary.test.mjs (Node 22 type stripping)
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildPublicSiteUrl, summarizeSellerSite } from "./siteSummary.ts";

test("buildPublicSiteUrl keeps the site host and borrows only the request port", () => {
  assert.equal(
    buildPublicSiteUrl("bach-dang.nexusmart.localhost", "nexusmart.localhost:3300"),
    "http://bach-dang.nexusmart.localhost:3300"
  );
  assert.equal(
    buildPublicSiteUrl("bach-dang.nexusmart.vn", "nexusmart.vn", "https"),
    "https://bach-dang.nexusmart.vn"
  );
  assert.equal(
    buildPublicSiteUrl("Sai-Gon.NexusMart.localhost:4000", "localhost:3300", "https:"),
    "https://sai-gon.nexusmart.localhost:4000"
  );
  assert.equal(buildPublicSiteUrl("a.nexusmart.localhost", null, "ftp"), "http://a.nexusmart.localhost");
});

test("buildPublicSiteUrl rejects empty or unsafe hosts", () => {
  assert.equal(buildPublicSiteUrl("", "localhost:3300"), "");
  assert.equal(buildPublicSiteUrl("evil.com/path", "localhost:3300"), "");
  assert.equal(buildPublicSiteUrl("javascript:alert(1)", null), "");
});

test("summarizeSellerSite maps backend site status to the seller-facing state", () => {
  assert.deepEqual(summarizeSellerSite(null), {
    state: "none",
    host: "",
    url: "",
    suspendedReason: "",
  });

  const published = summarizeSellerSite(
    { status: "PUBLISHED", primary_host: "bach-dang.nexusmart.localhost" },
    "nexusmart.localhost:3404"
  );
  assert.equal(published.state, "published");
  assert.equal(published.url, "http://bach-dang.nexusmart.localhost:3404");

  const draft = summarizeSellerSite({ status: "DRAFT", primary_host: "x.nexusmart.localhost" });
  assert.equal(draft.state, "draft");
  assert.equal(draft.url, "", "unpublished sites never expose a public link");

  const suspended = summarizeSellerSite({
    status: "suspended",
    primary_host: "x.nexusmart.localhost",
    suspended_reason: " Vi phạm chính sách ",
  });
  assert.equal(suspended.state, "suspended");
  assert.equal(suspended.url, "");
  assert.equal(suspended.suspendedReason, "Vi phạm chính sách");

  assert.equal(summarizeSellerSite({ status: "PUBLISHED", primary_host: "" }).state, "draft");
});
