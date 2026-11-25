// Run: node --test web/src/components/revealUtils.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  REVEAL_SELECTOR,
  easeOutCubic,
  formatCountMetric,
  isRectImmediatelyVisible,
} from "./revealUtils.ts";

test("REVEAL_SELECTOR targets [data-reveal] only", () => {
  assert.equal(REVEAL_SELECTOR, "[data-reveal]");
});

test("isRectImmediatelyVisible detects above-fold, tall, scrolled-past, below-fold, and display:none nodes", () => {
  // Above the fold
  assert.equal(
    isRectImmediatelyVisible({ top: 120, bottom: 320, width: 400, height: 200 }, 900),
    true
  );
  // Partially entering at the very bottom edge
  assert.equal(
    isRectImmediatelyVisible({ top: 899, bottom: 1100, width: 400, height: 201 }, 900),
    true
  );
  // Taller than the viewport
  assert.equal(
    isRectImmediatelyVisible({ top: 0, bottom: 2400, width: 1200, height: 2400 }, 900),
    true
  );
  // Already scrolled past above the fold
  assert.equal(
    isRectImmediatelyVisible({ top: -500, bottom: -120, width: 400, height: 380 }, 900),
    true
  );
  // Strictly below the fold
  assert.equal(
    isRectImmediatelyVisible({ top: 900, bottom: 1200, width: 400, height: 300 }, 900),
    false
  );
  assert.equal(
    isRectImmediatelyVisible({ top: 1450, bottom: 1850, width: 400, height: 400 }, 900),
    false
  );
  // Hidden / display:none (0x0) must wait for IntersectionObserver
  assert.equal(isRectImmediatelyVisible({ top: 0, bottom: 0, width: 0, height: 0 }, 900), false);
});

test("easeOutCubic clamps bounds and progresses monotonically without overshoot", () => {
  assert.equal(easeOutCubic(-0.5), 0);
  assert.equal(easeOutCubic(0), 0);
  assert.equal(easeOutCubic(1), 1);
  assert.equal(easeOutCubic(1.5), 1);
  const mid = easeOutCubic(0.5);
  assert.ok(mid > 0.5 && mid < 1, `expected fast-out progress at 0.5, got ${mid}`);
});

test("formatCountMetric formats VND, integers, percentages, decimals, and affixes", () => {
  assert.equal(formatCountMetric(1250000, { format: "vnd" }), "1.250.000₫");
  assert.equal(formatCountMetric(4280), "4.280");
  assert.equal(formatCountMetric(98.4, { format: "percent", decimals: 1 }), "98,4%");
  assert.equal(formatCountMetric(4.5, { decimals: 1, suffix: " ngày" }), "4,5 ngày");
  assert.equal(formatCountMetric(15, { prefix: "+", suffix: " shop" }), "+15 shop");
});
