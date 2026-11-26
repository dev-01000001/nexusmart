#!/usr/bin/env node
// Screenshot + sanity check for web routes at several widths. Shared by every redesign swarm and by Claude's review.
//
//   node .agents/tools/ui-shots.mjs --port 43110 --login seller --routes /seller,/seller/don-hang --out .runs/x/seller
//   node .agents/tools/ui-shots.mjs --port 43110 --site bach-dang --login buyer --routes /,/gio-hang --out .runs/x/site
//
// Options:
//   --port <n>            server port (required). Root host = nexusmart.localhost, site host = <site>.nexusmart.localhost
//   --site <subdomain>    open routes on the site host instead of the root host
//   --login <role>        none (default) | seller | admin | buyer   (buyer logs in on the site host)
//   --routes a,b,c        comma list of paths, or --routes-file <file> (one path per line, # comments allowed)
//   --widths 390,768,1440 default
//   --viewport            viewport-sized screenshot instead of full page (use for pixel comparisons)
//   --out <dir>           output dir (required): <slug>-<width>.png + report.txt
// Checks per route/width: HTTP status, console errors + page errors (hydration #418 shows here), horizontal overflow,
// number of <h1>. Exit code 1 if any check fails. Wrap calls in: flock /tmp/nm-web-browser.lock node .agents/tools/ui-shots.mjs …
import fs from "node:fs";
import path from "node:path";

const PW = process.env.PLAYWRIGHT_MODULE || "/home/tai/.npm/_npx/9833c18b2d85bc59/node_modules/playwright/index.mjs";
const { chromium } = await import(PW);

const argv = process.argv.slice(2);
const opt = (name, def) => {
  const i = argv.indexOf(`--${name}`);
  return i === -1 ? def : argv[i + 1];
};
const flag = (name) => argv.includes(`--${name}`);
const port = opt("port");
const out = opt("out");
if (!port || !out) {
  console.error("usage: --port <n> --out <dir> [--site sub] [--login role] [--routes a,b | --routes-file f]");
  process.exit(2);
}
const site = opt("site");
const login = opt("login", "none");
const widths = opt("widths", "390,768,1440").split(",").map(Number);
const routesFile = opt("routes-file");
const routes = routesFile
  ? fs.readFileSync(routesFile, "utf8").split("\n").map((l) => l.replace(/#.*/, "").trim()).filter(Boolean)
  : opt("routes", "/").split(",").map((r) => r.trim()).filter(Boolean);

const rootOrigin = `http://nexusmart.localhost:${port}`;
const origin = site ? `http://${site}.nexusmart.localhost:${port}` : rootOrigin;
// Dev defaults from .env.example / tools/seed/seed.mjs (local audit stack only). Override with env vars.
const creds = {
  admin: [process.env.ADMIN_EMAIL || "admin@nexusmart.local", process.env.ADMIN_PASSWORD || "AdminDevPassword123!"],
  seller: [process.env.SELLER_EMAIL || "seller@nexusmart.local", process.env.SELLER_PASSWORD || "SellerDemo123!"],
  buyer: [process.env.BUYER_EMAIL || "buyer@nexusmart.local", process.env.BUYER_PASSWORD || "BuyerDemo123!"],
};

fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ channel: "chrome" });
const ctx = await browser.newContext({ reducedMotion: "reduce" });
if (login !== "none") {
  const [email, password] = creds[login] ?? [];
  if (!email) throw new Error(`unknown --login ${login}`);
  const loginOrigin = login === "buyer" ? origin : rootOrigin;
  const res = await ctx.request.post(`${loginOrigin}/api/auth/login`, {
    data: { email, password },
    headers: { Origin: loginOrigin },
  });
  if (!res.ok()) throw new Error(`${login} login -> HTTP ${res.status()}`);
}

const page = await ctx.newPage();
let current = "";
const problems = [];
const lines = [];
page.on("console", (m) => m.type() === "error" && problems.push(`${current}: console ${m.text().slice(0, 200)}`));
page.on("pageerror", (e) => problems.push(`${current}: pageerror ${e.message.slice(0, 200)}`));

for (const route of routes) {
  const slug = (site ? `${site}-` : "") + (route.replace(/^\/|\/$/g, "").replace(/[^\w-]+/g, "_") || "home");
  for (const w of widths) {
    current = `${route} @${w}`;
    await page.setViewportSize({ width: w, height: 900 });
    let status = 0;
    try {
      const res = await page.goto(origin + route, { waitUntil: "networkidle", timeout: 45000 });
      status = res?.status() ?? 0;
    } catch (e) {
      problems.push(`${current}: navigation ${e.message.slice(0, 120)}`);
    }
    await page.waitForTimeout(300);
    const info = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      h1: document.querySelectorAll("h1").length,
      url: location.pathname + location.search,
    }));
    if (status >= 400 || status === 0) problems.push(`${current}: HTTP ${status}`);
    if (info.overflow > 1) problems.push(`${current}: horizontal overflow ${info.overflow}px`);
    if (info.h1 !== 1) problems.push(`${current}: ${info.h1} <h1>`);
    await page.screenshot({ path: path.join(out, `${slug}-${w}.png`), fullPage: !flag("viewport") });
    lines.push(`${String(status).padEnd(4)} ${current.padEnd(40)} final=${info.url} overflow=${info.overflow} h1=${info.h1}`);
  }
}
await browser.close();
const report = [...lines, "", `${problems.length} problem(s)`, ...problems].join("\n") + "\n";
fs.writeFileSync(path.join(out, "report.txt"), report);
console.log(report);
process.exit(problems.length ? 1 : 0);
