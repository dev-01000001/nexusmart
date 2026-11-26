import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

const HANDLED = new Set(["--font-sans", "--ease-out", "--ease-in-out"]);

function readCss(relPath) {
  return fs.readFileSync(new URL(relPath, import.meta.url), "utf8");
}

function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function extractDeclaredVars(css) {
  const clean = stripComments(css);
  const vars = new Set();
  for (const match of clean.matchAll(/(--[a-z0-9-]+)\s*:/gi)) {
    vars.add(match[1].toLowerCase());
  }
  return vars;
}

function extractKeyframes(css) {
  const clean = stripComments(css);
  const names = new Set();
  for (const match of clean.matchAll(/@keyframes\s+([a-zA-Z0-9_-]+)/g)) {
    names.add(match[1]);
  }
  return names;
}

function extractBlock(css, headerRegex) {
  const clean = stripComments(css);
  const match = headerRegex.exec(clean);
  if (!match) return "";
  let idx = match.index + match[0].length;
  let depth = 1;
  const start = idx;
  while (idx < clean.length && depth > 0) {
    const ch = clean[idx];
    if (ch === "{") depth++;
    else if (ch === "}") depth--;
    idx++;
  }
  return depth === 0 ? clean.slice(start, idx - 1) : "";
}

function readLegacyFiles() {
  return [
    readCss("./tokens.css"),
    readCss("./globals.css"),
    readCss("../features/site/site.module.css"),
    readCss("../features/site/siteTemplates.module.css"),
  ].join("\n");
}

test("ui.css never redefines legacy tokens", () => {
  const legacyVars = extractDeclaredVars(readLegacyFiles());
  const uiVars = extractDeclaredVars(readCss("./ui.css"));
  const collisions = [...uiVars].filter((v) => legacyVars.has(v) && !HANDLED.has(v));
  assert.deepEqual(
    collisions,
    [],
    `ui.css must not redefine legacy tokens outside HANDLED: ${collisions.join(", ")}`
  );
});

test("legacy/tailwind collisions are all handled in @theme inline", () => {
  const legacyVars = extractDeclaredVars(readLegacyFiles());
  const tailwindThemeVars = extractDeclaredVars(
    readCss("../../node_modules/tailwindcss/theme.css")
  );
  const uiCss = readCss("./ui.css");
  const themeInlineBlock = extractBlock(uiCss, /@theme\s+inline\s*\{/);
  const themeInlineVars = extractDeclaredVars(themeInlineBlock);

  const collisions = [...legacyVars].filter((v) => tailwindThemeVars.has(v));
  const unhandled = collisions.filter((v) => !HANDLED.has(v));
  assert.deepEqual(
    unhandled,
    [],
    `Unexpected legacy/tailwind custom property collisions: ${unhandled.join(", ")}`
  );

  for (const name of HANDLED) {
    assert.equal(
      themeInlineVars.has(name),
      true,
      `Expected ${name} to be declared inside @theme inline { ... } in ui.css`
    );
  }
});

test("ui.css has no preflight and no global base rules", () => {
  const uiCss = readCss("./ui.css");
  const clean = stripComments(uiCss);
  assert.equal(
    /@import\s+["']tailwindcss["']/.test(clean),
    false,
    'ui.css must not contain full @import "tailwindcss"'
  );
  assert.equal(
    /preflight/i.test(clean),
    false,
    "ui.css must not import or reference tailwindcss preflight"
  );
  assert.equal(
    /^\s*(\*|html|body)\s*[,{]/m.test(clean),
    false,
    "ui.css must not contain global *, html, or body base rules outside [data-ui]"
  );
});

test("keyframes names do not collide", () => {
  const legacyKeyframes = extractKeyframes(readLegacyFiles());
  const otherKeyframes = new Set([
    ...extractKeyframes(readCss("../../node_modules/tailwindcss/theme.css")),
    ...extractKeyframes(readCss("../../node_modules/tw-animate-css/dist/tw-animate.css")),
    ...extractKeyframes(readCss("./ui.css")),
  ]);
  const collisions = [...legacyKeyframes].filter((k) => otherKeyframes.has(k));
  assert.deepEqual(
    collisions,
    [],
    `Unexpected @keyframes name collisions: ${collisions.join(", ")}`
  );
});

test("app palette matches spec", () => {
  const uiCss = readCss("./ui.css");
  const rootBlock = extractBlock(uiCss, /:root\s*\{/);
  assert.ok(rootBlock.length > 0, "ui.css must define a :root block");

  const expected = [
    ["--ui-background", "#eff3f5"],
    ["--ui-primary", "#1f3038"],
    ["--ui-brand", "#0d7480"],
    ["--ui-input", "#7d8e97"],
    ["--ui-topbar", "#152229"],
    ["--ui-sidebar", "#e6ecef"],
    ["--ui-radius", "0.75rem"],
    ["--ui-font", "var(--font-inter)"],
  ];

  const declared = new Map();
  for (const match of rootBlock.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
    declared.set(match[1].toLowerCase(), match[2].trim().toLowerCase());
  }

  for (const [prop, value] of expected) {
    assert.equal(
      declared.get(prop),
      value,
      `Expected ${prop} in :root to equal ${value}, got ${declared.get(prop)}`
    );
  }
});

function extractAllBlocks(css, headerRegex) {
  const clean = stripComments(css);
  const blocks = [];
  const globalRe = new RegExp(headerRegex.source, "g");
  let match;
  while ((match = globalRe.exec(clean)) !== null) {
    let idx = match.index + match[0].length;
    let depth = 1;
    const start = idx;
    while (idx < clean.length && depth > 0) {
      const ch = clean[idx];
      if (ch === "{") depth++;
      else if (ch === "}") depth--;
      idx++;
    }
    if (depth === 0) {
      blocks.push({ start: match.index, end: idx, body: clean.slice(start, idx - 1) });
      globalRe.lastIndex = idx;
    }
  }
  return blocks;
}

function stripAtLayerBlocks(css) {
  const clean = stripComments(css);
  const blocks = extractAllBlocks(clean, /@layer\s+[a-z0-9_-]+\s*\{/i);
  let out = "";
  let cursor = 0;
  for (const b of blocks) {
    out += clean.slice(cursor, b.start);
    cursor = b.end;
  }
  out += clean.slice(cursor);
  return out;
}

test("globals @layer base only holds element-level selectors", () => {
  const globalsCss = readCss("./globals.css");
  const baseBlocks = extractAllBlocks(globalsCss, /@layer\s+base\s*\{/);
  assert.ok(baseBlocks.length > 0, "globals.css must contain @layer base blocks");

  const forbidden = [];
  for (const block of baseBlocks) {
    for (const match of block.body.matchAll(/([^{}]+)\{/g)) {
      const rawSelectorList = match[1].trim();
      if (!rawSelectorList) continue;
      for (const sel of rawSelectorList.split(",")) {
        const s = sel.trim();
        if (!s) continue;
        // Disallow class (.), attribute ([), or single-colon pseudo-class (: not part of ::)
        const withoutDoubleColon = s.replace(/::/g, "");
        if (s.includes(".") || s.includes("[") || withoutDoubleColon.includes(":")) {
          forbidden.push(s);
        }
      }
    }
  }

  assert.deepEqual(
    forbidden,
    [],
    `globals.css @layer base must only contain element/universal/::pseudo-element selectors (<= (0,0,1)), found: ${forbidden.join(", ")}`
  );
});

test("ui.css has unlayered revert-layer overrides for legacy unlayered rules", () => {
  const unlayered = stripAtLayerBlocks(readCss("./ui.css"));
  const normalized = unlayered.replace(/\s+/g, " ");

  assert.match(
    normalized,
    /\[data-ui="true"\]\s*:is\(ul,\s*ol\)\[class\]\s*\{[^}]*padding:\s*revert-layer;[^}]*list-style:\s*revert-layer;/i,
    "ui.css must have unlayered revert-layer override for [data-ui=\"true\"] :is(ul, ol)[class]"
  );
  assert.match(
    normalized,
    /\[data-ui="true"\]\s*:focus-visible\s*\{[^}]*outline:\s*revert-layer;[^}]*outline-offset:\s*revert-layer;/i,
    "ui.css must have unlayered revert-layer override for [data-ui=\"true\"] :focus-visible"
  );
  assert.match(
    normalized,
    /\[data-ui="true"\]\s*tbody\s+tr:last-child\s+td\s*\{[^}]*border-bottom:\s*revert-layer;/i,
    "ui.css must have unlayered revert-layer override for [data-ui=\"true\"] tbody tr:last-child td"
  );
});

test("ui.css is imported in design layout, not root layout, and globals pins layer order", () => {
  const rootLayout = readCss("../app/layout.tsx");
  const designLayout = readCss("../app/(design)/design/layout.tsx");
  const globalsCss = readCss("./globals.css");

  assert.equal(
    rootLayout.includes("ui.css"),
    false,
    "src/app/layout.tsx must not import ui.css globally"
  );
  assert.equal(
    designLayout.includes("@/styles/ui.css"),
    true,
    "src/app/(design)/design/layout.tsx must import @/styles/ui.css"
  );
  assert.match(
    stripComments(globalsCss).trimStart(),
    /^@layer\s+theme,\s*base,\s*components,\s*utilities;/,
    "src/styles/globals.css must declare @layer theme, base, components, utilities; as its first statement"
  );
});
