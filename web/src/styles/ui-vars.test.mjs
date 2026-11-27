import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

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

test("ui.css pins @layer order and imports preflight inside layer(base)", () => {
  const uiCss = stripComments(readCss("./ui.css")).trimStart();
  assert.match(
    uiCss,
    /^@layer\s+theme,\s*base,\s*components,\s*utilities;/,
    "ui.css must declare @layer theme, base, components, utilities; as its first statement"
  );
  assert.match(
    uiCss,
    /@import\s+["']tailwindcss\/theme\.css["']\s+layer\(theme\);/,
    "ui.css must import tailwindcss/theme.css into layer(theme)"
  );
  assert.match(
    uiCss,
    /@import\s+["']tailwindcss\/preflight\.css["']\s+layer\(base\);/,
    "ui.css must import tailwindcss/preflight.css into layer(base)"
  );
  assert.match(
    uiCss,
    /@import\s+["']tailwindcss\/utilities\.css["']\s+layer\(utilities\)/,
    "ui.css must import tailwindcss/utilities.css into layer(utilities)"
  );

  const rootLayout = readCss("../app/layout.tsx");
  assert.equal(
    rootLayout.includes("@/styles/ui.css"),
    true,
    "src/app/layout.tsx must import @/styles/ui.css globally"
  );
  assert.equal(
    rootLayout.includes("globals.css"),
    false,
    "src/app/layout.tsx must not import removed globals.css"
  );
  assert.equal(
    rootLayout.includes("tokens.css"),
    false,
    "src/app/layout.tsx must not import removed tokens.css"
  );
  assert.equal(
    /\bArchivo\b|--font-archivo/.test(rootLayout),
    false,
    "src/app/layout.tsx must not load removed Archivo font"
  );
  assert.equal(
    fs.existsSync(new URL("./globals.css", import.meta.url)),
    false,
    "web/src/styles/globals.css must be deleted"
  );
  assert.equal(
    fs.existsSync(new URL("./tokens.css", import.meta.url)),
    false,
    "web/src/styles/tokens.css must be deleted"
  );
});

test("ui.css @theme inline keeps --font-sans dynamic and drops redundant --ease-* overrides", () => {
  const uiCss = readCss("./ui.css");
  const themeInline = extractBlock(uiCss, /@theme\s+inline\s*\{/);
  assert.ok(themeInline.length > 0, "ui.css must declare @theme inline");
  assert.match(
    themeInline,
    /--font-sans\s*:\s*var\(--ui-font\)/,
    "@theme inline must keep --font-sans wired to var(--ui-font) for per-site font overrides"
  );
  assert.equal(
    /--ease-(?:out|in-out)\s*:/.test(themeInline),
    false,
    "@theme inline must not redefine Tailwind built-in --ease-out / --ease-in-out"
  );
});

test("home-tokens.css scopes --home-* to [data-platform-public] without legacy token references", () => {
  const homeTokens = stripComments(readCss("../features/platform/home/home-tokens.css"));
  assert.equal(
    /:root\s*\{/.test(homeTokens),
    false,
    "home-tokens.css must scope --home-* to [data-platform-public], not :root"
  );
  const block = extractBlock(homeTokens, /\[data-platform-public="true"\]\s*\{/);
  assert.ok(block.length > 0, 'home-tokens.css must declare [data-platform-public="true"] block');
  const declared = [...extractDeclaredVars(block)];
  assert.ok(declared.length > 40, "home-tokens.css must declare all --home-* tokens");
  const nonHome = declared.filter((v) => !v.startsWith("--home-"));
  assert.deepEqual(
    nonHome,
    [],
    `home-tokens.css must only declare --home-* variables, found: ${nonHome.join(", ")}`
  );
  assert.equal(
    /var\(--(?:band-dark-accent|accent)\)/.test(block),
    false,
    "home-tokens.css must not reference deleted legacy tokens"
  );
});

test("ui.css has no data-ui reset or revert-layer overrides", () => {
  const clean = stripComments(readCss("./ui.css"));
  assert.equal(
    /revert-layer/i.test(clean),
    false,
    "ui.css must not contain legacy revert-layer overrides"
  );
  assert.equal(
    /\[data-ui\s*=\s*["']true["']\]/i.test(clean),
    false,
    'ui.css must not scope resets to [data-ui="true"]'
  );
});

test("ui.css :root and dark blocks only declare --ui-* custom properties (no Tailwind/shadcn name collisions)", () => {
  const uiCss = readCss("./ui.css");
  const rootBlock = extractBlock(uiCss, /:root\s*\{/);
  const darkBlock = extractBlock(uiCss, /\[data-site-theme-mode="dark"\]\s*\{/);
  assert.ok(rootBlock.length > 0, "ui.css must define a :root block");
  assert.ok(darkBlock.length > 0, 'ui.css must define a [data-site-theme-mode="dark"] block');

  const tailwindThemeVars = extractDeclaredVars(
    readCss("../../node_modules/tailwindcss/theme.css")
  );
  const rawVars = new Set([...extractDeclaredVars(rootBlock), ...extractDeclaredVars(darkBlock)]);

  const nonUiVars = [...rawVars].filter((v) => !v.startsWith("--ui-"));
  assert.deepEqual(
    nonUiVars,
    [],
    `All raw custom properties in ui.css :root/dark must use --ui-* prefix, found: ${nonUiVars.join(", ")}`
  );

  const tailwindCollisions = [...rawVars].filter((v) => tailwindThemeVars.has(v));
  assert.deepEqual(
    tailwindCollisions,
    [],
    `Raw ui.css custom properties must not collide with Tailwind theme vars: ${tailwindCollisions.join(", ")}`
  );
});

test("app palette and dark mode tokens match spec §6.1 completely", () => {
  const uiCss = readCss("./ui.css");
  const rootBlock = extractBlock(uiCss, /:root\s*\{/);
  const darkBlock = extractBlock(uiCss, /\[data-site-theme-mode="dark"\]\s*\{/);

  const parseVars = (block) => {
    const map = new Map();
    for (const match of block.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
      map.set(match[1].toLowerCase(), match[2].trim().toLowerCase());
    }
    return map;
  };

  const rootVars = parseVars(rootBlock);
  const expectedRoot = [
    ["--ui-background", "#eff3f5"],
    ["--ui-foreground", "#1f3038"],
    ["--ui-card", "#ffffff"],
    ["--ui-card-foreground", "#1f3038"],
    ["--ui-popover", "#ffffff"],
    ["--ui-popover-foreground", "#1f3038"],
    ["--ui-primary", "#1f3038"],
    ["--ui-primary-foreground", "#ffffff"],
    ["--ui-secondary", "#e4eaee"],
    ["--ui-secondary-foreground", "#1f3038"],
    ["--ui-muted", "#eff3f5"],
    ["--ui-muted-foreground", "#52626a"],
    ["--ui-accent", "#e4eaee"],
    ["--ui-accent-foreground", "#152229"],
    ["--ui-brand", "#0d7480"],
    ["--ui-brand-foreground", "#ffffff"],
    ["--ui-destructive", "#b33a3a"],
    ["--ui-destructive-foreground", "#ffffff"],
    ["--ui-border", "#dde4e8"],
    ["--ui-input", "#7d8e97"],
    ["--ui-ring", "#0d7480"],
    ["--ui-topbar", "#152229"],
    ["--ui-topbar-foreground", "#d3dde2"],
    ["--ui-topbar-input", "#293e48"],
    ["--ui-topbar-placeholder", "#9fb0b8"],
    ["--ui-sidebar", "#e6ecef"],
    ["--ui-sidebar-foreground", "#1f3038"],
    ["--ui-sidebar-muted", "#52626a"],
    ["--ui-sidebar-accent", "#ffffff"],
    ["--ui-sidebar-accent-foreground", "#152229"],
    ["--ui-sidebar-border", "#dde4e8"],
    ["--ui-sidebar-ring", "#0d7480"],
    ["--ui-sidebar-primary", "#0d7480"],
    ["--ui-sidebar-primary-foreground", "#ffffff"],
    ["--ui-badge-success-bg", "#ddf3e6"],
    ["--ui-badge-success-ink", "#145338"],
    ["--ui-badge-attention-bg", "#fbefb5"],
    ["--ui-badge-attention-ink", "#5c4a00"],
    ["--ui-badge-warning-bg", "#fcddb8"],
    ["--ui-badge-warning-ink", "#6b3d00"],
    ["--ui-badge-info-bg", "#dcebfa"],
    ["--ui-badge-info-ink", "#0b4a7a"],
    ["--ui-badge-critical-bg", "#f8e2e2"],
    ["--ui-badge-critical-ink", "#8c2626"],
    ["--ui-badge-neutral-bg", "#e6ecef"],
    ["--ui-badge-neutral-ink", "#2c3e47"],
    ["--ui-radius", "0.75rem"],
    ["--ui-font", "var(--font-inter)"],
  ];

  for (const [prop, value] of expectedRoot) {
    assert.equal(
      rootVars.get(prop),
      value,
      `Expected ${prop} in :root to equal ${value}, got ${rootVars.get(prop)}`
    );
  }
  assert.ok(rootVars.has("--ui-shadow-card"), "ui.css :root must define --ui-shadow-card");
  assert.ok(rootVars.has("--ui-shadow-button"), "ui.css :root must define --ui-shadow-button");
  assert.ok(rootVars.has("--ui-shadow-overlay"), "ui.css :root must define --ui-shadow-overlay");

  const darkVars = parseVars(darkBlock);
  const expectedDark = [
    ["--ui-destructive", "#f07a7a"],
    ["--ui-badge-success-bg", "#16352a"],
    ["--ui-badge-success-ink", "#8ee0b5"],
    ["--ui-badge-attention-bg", "#3a3210"],
    ["--ui-badge-attention-ink", "#f2de7a"],
    ["--ui-badge-warning-bg", "#3d2a12"],
    ["--ui-badge-warning-ink", "#f5b97a"],
    ["--ui-badge-info-bg", "#132c44"],
    ["--ui-badge-info-ink", "#8cc4f5"],
    ["--ui-badge-critical-bg", "#3d1a1a"],
    ["--ui-badge-critical-ink", "#f5a3a3"],
    ["--ui-badge-neutral-bg", "#262c33"],
    ["--ui-badge-neutral-ink", "#d3dde2"],
  ];

  for (const [prop, value] of expectedDark) {
    assert.equal(
      darkVars.get(prop),
      value,
      `Expected ${prop} in dark theme to equal ${value}, got ${darkVars.get(prop)}`
    );
  }
});

test("keyframes names do not collide across ui.css, home-keyframes.css, Tailwind, and tw-animate-css", () => {
  const customKeyframes = new Set([
    ...extractKeyframes(readCss("./ui.css")),
    ...extractKeyframes(readCss("../features/platform/home/home-keyframes.css")),
  ]);
  const vendorKeyframes = new Set([
    ...extractKeyframes(readCss("../../node_modules/tailwindcss/theme.css")),
    ...extractKeyframes(readCss("../../node_modules/tw-animate-css/dist/tw-animate.css")),
  ]);
  const collisions = [...customKeyframes].filter((k) => vendorKeyframes.has(k));
  assert.deepEqual(
    collisions,
    [],
    `Unexpected @keyframes name collisions: ${collisions.join(", ")}`
  );
});
