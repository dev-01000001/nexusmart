import { test } from "node:test";
import assert from "node:assert/strict";
import { parseNumericStat } from "./siteMetrics.ts";

test("parseNumericStat extracts valid count-up integers and rejects ranges, ratios, and standalone years", () => {
  assert.equal(parseNumericStat("1 đến 3 ngày"), null);
  assert.equal(parseNumericStat("4.9/5"), null);
  assert.equal(parseNumericStat("2024"), null);

  assert.deepEqual(parseNumericStat("1.500+"), {
    prefix: "",
    to: 1500,
    suffix: "+",
  });
  assert.deepEqual(parseNumericStat("98%"), {
    prefix: "",
    to: 98,
    suffix: "%",
  });
  assert.deepEqual(parseNumericStat("12.000+"), {
    prefix: "",
    to: 12000,
    suffix: "+",
  });

  // Additional edge cases
  assert.deepEqual(parseNumericStat("220gsm"), {
    prefix: "",
    to: 220,
    suffix: "gsm",
  });
  assert.equal(parseNumericStat("1–3 ngày"), null);
  assert.equal(parseNumericStat("Từ 2024"), null);
  assert.equal(parseNumericStat("4,9★"), null);
  assert.equal(parseNumericStat("0%"), null);
  assert.equal(parseNumericStat("-10%"), null);
  assert.equal(parseNumericStat(""), null);
});
