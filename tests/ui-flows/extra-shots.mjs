#!/usr/bin/env node
// Companion screenshot helper for cases .agents/tools/ui-shots.mjs does not support directly:
//   1. --mode site-custom: storefront site with custom verified primary_host (ha-noi -> hanoi-knit.verified.test),
//      which needs Chromium --host-resolver-rules and browser-side login.
//   2. --mode root-footer: root homepage viewport screenshot after scrolling down to footer (390/768/1440).
// Output naming and report.txt format match .agents/tools/ui-shots.mjs.
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import {
  CREDS,
  launchBrowser,
  loginViaBrowser,
  rootOrigin,
  siteOrigin,
} from "./lib.mjs";

const argv = process.argv.slice(2);
const opt = (name, def = "") => {
  const i = argv.indexOf(`--${name}`);
  return i === -1 ? def : (argv[i + 1] ?? def);
};
const flag = (name) => argv.includes(`--${name}`);

const port = opt("port");
const out = opt("out");
const mode = opt("mode");
const widths = opt("widths", "390,768,1440").split(",").map(Number);

if (!port || !out || !mode) {
  console.error(
    "Usage: node tests/ui-flows/extra-shots.mjs --port <n> --out <dir> --mode root-footer|site-custom [--site ha-noi] [--routes-file f] [--viewport]",
  );
  process.exit(2);
}

fs.mkdirSync(out, { recursive: true });
const browser = await launchBrowser();
const ctx = await browser.newContext({ reducedMotion: "reduce" });
const page = await ctx.newPage();

let current = "";
const problems = [];
const lines = [];
page.on("console", (m) => {
  if (m.type() === "error")
    problems.push(`${current}: console ${m.text().slice(0, 200)}`);
});
page.on("pageerror", (e) => {
  problems.push(`${current}: pageerror ${e.message.slice(0, 200)}`);
});

if (mode === "root-footer") {
  const origin = rootOrigin(port);
  for (const w of widths) {
    current = `/#footer @${w}`;
    await page.setViewportSize({ width: w, height: 900 });
    let status = 0;
    try {
      const res = await page.goto(`${origin}/`, {
        waitUntil: "networkidle",
        timeout: 45_000,
      });
      status = res?.status() ?? 0;
    } catch (e) {
      problems.push(`${current}: navigation ${e.message.slice(0, 120)}`);
    }
    await page.evaluate(() => {
      const footer = document.querySelector("footer");
      if (footer) {
        footer.scrollIntoView({ behavior: "instant", block: "end" });
      } else {
        window.scrollTo(0, document.documentElement.scrollHeight);
      }
    });
    await page.waitForTimeout(350);
    const info = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      h1: document.querySelectorAll("h1").length,
      url: location.pathname + location.search,
      scrollY: Math.round(window.scrollY),
    }));
    if (status >= 400 || status === 0)
      problems.push(`${current}: HTTP ${status}`);
    if (info.overflow > 1)
      problems.push(`${current}: horizontal overflow ${info.overflow}px`);
    if (info.h1 !== 1) problems.push(`${current}: ${info.h1} <h1>`);
    await page.screenshot({
      path: path.join(out, `home-footer-${w}.png`),
      fullPage: false,
    });
    lines.push(
      `${String(status).padEnd(4)} ${current.padEnd(40)} final=${info.url} scrollY=${info.scrollY} overflow=${info.overflow} h1=${info.h1}`,
    );
  }
} else if (mode === "site-custom") {
  const site = opt("site", "ha-noi");
  const routesFile = opt("routes-file");
  const routes = routesFile
    ? fs
        .readFileSync(routesFile, "utf8")
        .split("\n")
        .map((l) => l.replace(/#.*/, "").trim())
        .filter(Boolean)
    : ["/"];

  const entry = siteOrigin(site, port);
  await loginViaBrowser(page, `${entry}/`, CREDS.buyer);
  const canonicalOrigin = new URL(page.url()).origin;

  for (const route of routes) {
    const slug = `${site}-${route.replace(/^\/|\/$/g, "").replace(/[^\w-]+/g, "_") || "home"}`;
    for (const w of widths) {
      current = `${route} @${w}`;
      await page.setViewportSize({ width: w, height: 900 });
      let status = 0;
      try {
        for (let attempt = 0; attempt < 2; attempt++) {
          const res = await page.goto(`${canonicalOrigin}${route}`, {
            waitUntil: "networkidle",
            timeout: 45_000,
          });
          status = res?.status() ?? 0;
          if (
            (status === 429 || status === 500 || status === 503) &&
            attempt === 0
          ) {
            await page.waitForTimeout(8_000);
            continue;
          }
          break;
        }
      } catch (e) {
        problems.push(`${current}: navigation ${e.message.slice(0, 120)}`);
      }
      await page.waitForTimeout(300);
      const info = await page.evaluate(() => ({
        overflow: document.documentElement.scrollWidth - window.innerWidth,
        h1: document.querySelectorAll("h1").length,
        url: location.pathname + location.search,
      }));
      if (status >= 400 || status === 0)
        problems.push(`${current}: HTTP ${status}`);
      if (info.overflow > 1)
        problems.push(`${current}: horizontal overflow ${info.overflow}px`);
      if (info.h1 !== 1) problems.push(`${current}: ${info.h1} <h1>`);
      await page.screenshot({
        path: path.join(out, `${slug}-${w}.png`),
        fullPage: !flag("viewport"),
      });
      lines.push(
        `${String(status).padEnd(4)} ${current.padEnd(40)} final=${info.url} overflow=${info.overflow} h1=${info.h1}`,
      );
    }
  }
} else {
  console.error(`Unknown --mode ${mode}`);
  await browser.close();
  process.exit(2);
}

await browser.close();
const reportName =
  mode === "root-footer"
    ? "report-footer.txt"
    : `report-${opt("site", "ha-noi")}.txt`;
const report =
  [...lines, "", `${problems.length} problem(s)`, ...problems].join("\n") +
  "\n";
fs.writeFileSync(path.join(out, reportName), report);
console.log(report);
process.exit(problems.length ? 1 : 0);
