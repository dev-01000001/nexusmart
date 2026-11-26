// Run: cd web && node --test src/components/app/setupProgress.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupProgress } from "./setupProgress.ts";

test("setupProgress counts only non-optional steps and ignores optional done", () => {
  const result = setupProgress([
    { id: "product", status: "done" },
    { id: "bank", status: "done" },
    { id: "site", status: "todo" },
    { id: "domain", status: "done", optional: true },
  ]);
  assert.deepEqual(result, { done: 2, total: 3, complete: false });
});

test("setupProgress treats unknown as not done", () => {
  const result = setupProgress([
    { id: "product", status: "done" },
    { id: "bank", status: "unknown" },
    { id: "site", status: "done" },
  ]);
  assert.deepEqual(result, { done: 2, total: 3, complete: false });
});

test("setupProgress marks complete when all required steps are done even if optional is todo", () => {
  const result = setupProgress([
    { id: "product", status: "done" },
    { id: "bank", status: "done" },
    { id: "site", status: "done" },
    { id: "domain", status: "todo", optional: true },
  ]);
  assert.deepEqual(result, { done: 3, total: 3, complete: true });
});

test("setupProgress returns incomplete for empty steps list", () => {
  assert.deepEqual(setupProgress([]), { done: 0, total: 0, complete: false });
});
