// Viewport screenshots of one page at every scroll position (0, 900, 1800, … to the end), for pixel parity checks.
//   flock /tmp/nm-web-browser.lock node .agents/tools/scroll-shots.mjs <out-dir> <port> [path=/] [widths=390,768,1440]
// Files: <out-dir>/w<width>_y<offset>.png (same names for every port, so two runs can go through ui-diff.sh).
// Reduce-motion on, caret "initial", 1.5 s settle after each scroll. Root host nexusmart.localhost.
import { launchBrowser } from "../../tests/ui-flows/lib.mjs";
import fs from "node:fs";
const [out, port, route = "/", widthsArg = "390,768,1440"] = process.argv.slice(2);
if (!out || !port) throw new Error("usage: scroll-shots.mjs <out-dir> <port> [path] [widths]");
fs.mkdirSync(out, { recursive: true });
const browser = await launchBrowser();
for (const w of widthsArg.split(",").map(Number)) {
  const ctx = await browser.newContext({ viewport: { width: w, height: 900 }, reducedMotion: "reduce" });
  const page = await ctx.newPage();
  await page.goto(`http://nexusmart.localhost:${port}${route}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(1500);
  const H = await page.evaluate(() => document.documentElement.scrollHeight);
  for (let y = 0; y < H; y += 900) {
    await page.evaluate((y) => window.scrollTo(0, y), y);
    await page.waitForTimeout(1500);
    await page.screenshot({ path: `${out}/w${w}_y${String(y).padStart(5, "0")}.png`, caret: "initial" });
  }
  console.log(`port ${port} width ${w} height ${H}`);
  await ctx.close();
}
await browser.close();
