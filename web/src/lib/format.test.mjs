import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import {
  buildVnPastDays,
  formatCompactVnd,
  formatDateTimeVi,
  formatDateVi,
  formatDayMonthVi,
  formatDiscountPercent,
  formatInt,
  formatPercent,
  formatShortTimeDateVi,
  formatSignedInt,
  formatSignedVnd,
  formatTimeVi,
  formatVnd,
  formatVnIsoDate,
  getVnDateParts,
} from "./format.ts";

const formatModulePath = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "format.ts");

test("VN UTC+7 date/time formatters roll midnight accurately for 2026-11-24T17:30:00Z", () => {
  const iso = "2026-11-24T17:30:00Z";
  assert.equal(formatDateTimeVi(iso), "00:30, 25/11/2026");
  assert.equal(formatDateVi(iso), "25/11/2026");
  assert.equal(formatDayMonthVi(iso), "25/11");
  assert.equal(formatTimeVi(iso), "00:30");
  assert.equal(formatShortTimeDateVi(iso), "00:30 25/11");
  assert.equal(formatVnIsoDate(iso), "2026-11-25");

  const parts = getVnDateParts(iso);
  assert.deepEqual(parts, {
    year: 2026,
    month: 11,
    day: 25,
    hour: 0,
    minute: 30,
    second: 0,
    dateKey: "2026-11-25",
    dayStartUtcMs: Date.UTC(2026, 10, 25),
  });

  const days = buildVnPastDays(3, Date.parse(iso));
  assert.deepEqual(
    days.map((d) => ({ key: d.key, label: d.label })),
    [
      { key: "2026-11-23", label: "23/11" },
      { key: "2026-11-24", label: "24/11" },
      { key: "2026-11-25", label: "25/11" },
    ]
  );
});

test("format helpers produce identical output under TZ=UTC, TZ=Asia/Ho_Chi_Minh, and TZ=America/New_York", () => {
  const zones = ["UTC", "Asia/Ho_Chi_Minh", "America/New_York"];
  const script = `
    import {
      buildVnPastDays,
      formatDateTimeVi,
      formatDateVi,
      formatDayMonthVi,
      formatShortTimeDateVi,
      formatTimeVi,
      formatVnIsoDate,
    } from ${JSON.stringify(`file://${formatModulePath}`)};
    const samples = [
      "2026-11-24T17:30:00Z",
      "2026-01-01T00:05:00+07:00",
      "2026-12-31T18:45:00Z",
    ];
    const out = samples.map((iso) => ({
      iso,
      dateTime: formatDateTimeVi(iso),
      date: formatDateVi(iso),
      dayMonth: formatDayMonthVi(iso),
      time: formatTimeVi(iso),
      shortTimeDate: formatShortTimeDateVi(iso),
      isoDate: formatVnIsoDate(iso),
      days: buildVnPastDays(3, Date.parse(iso)).map((d) => d.key + ":" + d.label),
    }));
    process.stdout.write(JSON.stringify(out));
  `;

  const outputs = zones.map((tz) =>
    execFileSync(process.execPath, ["--input-type=module", "-e", script], {
      env: { ...process.env, TZ: tz },
      encoding: "utf8",
    })
  );

  const parsedUtc = JSON.parse(outputs[0]);
  assert.equal(parsedUtc[0].dateTime, "00:30, 25/11/2026");
  assert.equal(parsedUtc[1].dateTime, "00:05, 01/01/2026");
  assert.equal(parsedUtc[2].dateTime, "01:45, 01/01/2027");

  assert.equal(outputs[1], outputs[0], "Asia/Ho_Chi_Minh must match UTC");
  assert.equal(outputs[2], outputs[0], "America/New_York must match UTC");
});

test("numeric & currency formatters guard non-finite values, handle compact tier carry-over, and avoid -0%", () => {
  for (const bad of [Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
    assert.equal(formatVnd(bad), "0₫");
    assert.equal(formatSignedVnd(bad), "0₫");
    assert.equal(formatCompactVnd(bad), "0₫");
    assert.equal(formatInt(bad), "0");
    assert.equal(formatSignedInt(bad), "0");
    assert.equal(formatPercent(bad, 1), "0,0%");
    assert.equal(formatDiscountPercent(bad, 100), "0%");
    assert.equal(formatDiscountPercent(100, bad), "0%");
  }

  assert.equal(formatCompactVnd(999_499), "999k₫");
  assert.equal(formatCompactVnd(999_500), "1 tr₫");
  assert.equal(formatCompactVnd(-999_500), "\u22121 tr₫");
  assert.equal(formatCompactVnd(999_949_999), "999,9 tr₫");
  assert.equal(formatCompactVnd(999_950_000), "1 tỷ₫");

  assert.equal(formatDiscountPercent(100_000, 99_900), "0%");
  assert.equal(formatDiscountPercent(100_000, 78_000), "-22%");

  const nowMs = Date.parse("2026-11-24T17:30:00Z");
  assert.deepEqual(buildVnPastDays(Number.POSITIVE_INFINITY, nowMs), []);
  assert.deepEqual(buildVnPastDays(Number.NaN, nowMs), []);
  assert.deepEqual(buildVnPastDays(0, nowMs), []);
  const floored = buildVnPastDays(3.5, nowMs);
  assert.equal(floored.length, 3);
  assert.deepEqual(
    floored.map((d) => d.key),
    ["2026-11-23", "2026-11-24", "2026-11-25"]
  );
  assert.equal(floored[2].dayStartUtcMs, Date.UTC(2026, 10, 25));
});
