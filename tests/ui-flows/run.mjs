#!/usr/bin/env node
// Functional UI flow verifier for NexusMart redesign (runs on both frozen baseline 43101 & new 43110).
// Usage:
//   flock /tmp/nm-web-browser.lock node tests/ui-flows/run.mjs --port 43101 --area seller [--only <substr>]
//   flock /tmp/nm-web-browser.lock node tests/ui-flows/run.mjs --port 43101 --area site [--site bach-dang,sai-gon]
import process from "node:process";
import { launchBrowser, runFlowList } from "./lib.mjs";
import { buildSellerFlows } from "./seller.mjs";
import { buildAdminFlows } from "./admin.mjs";
import { buildPlatformFlows } from "./platform.mjs";
import { buildSiteFlows } from "./site.mjs";
import { buildTemplatesFlows } from "./templates.mjs";
import { buildJourneyFlows } from "./journey.mjs";

const argv = process.argv.slice(2);
const opt = (name, def = "") => {
  const i = argv.indexOf(`--${name}`);
  return i === -1 ? def : (argv[i + 1] ?? def);
};

const port = opt("port");
const area = opt("area");
const only = opt("only");
const sitesArg = opt("site");
const videoDir = opt("video");

const VALID_AREAS = new Set([
  "seller",
  "admin",
  "platform",
  "site",
  "templates",
  "journey",
]);
if (!port || !VALID_AREAS.has(area)) {
  console.error(
    "Usage: node tests/ui-flows/run.mjs --port <n> --area seller|admin|platform|site|templates|journey [--only <pattern>] [--site s1,s2] [--video <dir>]",
  );
  process.exit(2);
}

const tStart = Date.now();
const browser = await launchBrowser();
let results = [];
let cleanup = async () => {};

try {
  if (area === "seller") {
    ({ flows: results, cleanup } = await buildSellerFlows({ browser, port }));
  } else if (area === "admin") {
    ({ flows: results, cleanup } = await buildAdminFlows({ browser, port }));
  } else if (area === "platform") {
    ({ flows: results, cleanup } = await buildPlatformFlows({ browser, port }));
  } else if (area === "site") {
    const sites = sitesArg
      ? sitesArg
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)
      : undefined;
    ({ flows: results, cleanup } = await buildSiteFlows({
      browser,
      port,
      sites,
    }));
  } else if (area === "templates") {
    ({ flows: results, cleanup } = await buildTemplatesFlows({
      browser,
      port,
    }));
  } else if (area === "journey") {
    ({ flows: results, cleanup } = await buildJourneyFlows({
      browser,
      port,
      videoDir,
    }));
  }

  const ran = await runFlowList(results, { only });
  const pass = ran.filter((r) => r.status === "PASS").length;
  const fail = ran.filter((r) => r.status === "FAIL").length;
  const known = ran.filter((r) => r.status === "KNOWN").length;
  const totalSec = ((Date.now() - tStart) / 1000).toFixed(1);

  console.log(
    `\nSUMMARY area=${area} port=${port} PASS=${pass} FAIL=${fail} KNOWN=${known} total=${ran.length} time=${totalSec}s`,
  );
  if (ran.length === 0) {
    console.error(`FAIL no flows matched --only "${only}"`);
    process.exitCode = 1;
  } else if (fail > 0) {
    process.exitCode = 1;
  }
} finally {
  await cleanup().catch(() => {});
  await browser.close().catch(() => {});
}
