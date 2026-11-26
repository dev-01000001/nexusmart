// Run: cd web && node --test src/lib/site/siteUi.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { PLATFORM_TEMPLATES } from "../../features/site/templateCatalog.ts";
import {
  SITE_UI_FONT_STACKS,
  buildSiteUiVars,
  contrastRatio,
  resolveSiteFontStack,
  resolveSiteUiFontStack,
  siteRadiusToCss,
} from "./theme.ts";
import { ALLOWED_SITE_FONTS, DEFAULT_SITE_THEME } from "./types.ts";

test("site ui font stacks map every allowed font", () => {
  for (const f of ALLOWED_SITE_FONTS) {
    assert.equal(
      resolveSiteUiFontStack(f),
      SITE_UI_FONT_STACKS[f],
      `expected resolveSiteUiFontStack(${f}) to match SITE_UI_FONT_STACKS[${f}]`
    );
  }

  assert.equal(
    resolveSiteUiFontStack("inter").startsWith("var(--font-inter)"),
    true,
    'expected resolveSiteUiFontStack("inter") to start with "var(--font-inter)"'
  );

  const fallback = SITE_UI_FONT_STACKS[DEFAULT_SITE_THEME.font_family];
  assert.equal(resolveSiteUiFontStack("comic-sans"), fallback);
  assert.equal(resolveSiteUiFontStack(undefined), fallback);
  assert.equal(resolveSiteFontStack("inter"), SITE_UI_FONT_STACKS.inter);
});

test("every platform template yields accessible ui vars", () => {
  assert.ok(PLATFORM_TEMPLATES.length >= 8, "expected at least 8 platform templates");
  for (const t of PLATFORM_TEMPLATES) {
    const v = buildSiteUiVars(t.config.theme);
    assert.ok(
      contrastRatio(v["--ui-muted-foreground"], v["--ui-background"]) >= 4.5,
      `${t.id}: muted-foreground vs background contrast < 4.5`
    );
    assert.ok(
      contrastRatio(v["--ui-muted-foreground"], v["--ui-card"]) >= 4.5,
      `${t.id}: muted-foreground vs card contrast < 4.5`
    );
    assert.ok(
      contrastRatio(v["--ui-input"], v["--ui-background"]) >= 3,
      `${t.id}: input vs background contrast < 3`
    );
    assert.ok(
      contrastRatio(v["--ui-ring"], v["--ui-background"]) >= 3,
      `${t.id}: ring vs background contrast < 3`
    );
    assert.equal(v["--ui-primary"], t.config.theme.accent_color.toUpperCase());
    assert.equal(v["--ui-primary-foreground"], t.config.theme.accent_text_color.toUpperCase());
  }
});

test("radius mapping", () => {
  assert.equal(siteRadiusToCss("none"), "0px");
  assert.equal(siteRadiusToCss("sm"), "4px");
  assert.equal(siteRadiusToCss("md"), "8px");
  assert.equal(siteRadiusToCss("lg"), "16px");
  assert.equal(siteRadiusToCss(""), "0px");
  assert.equal(siteRadiusToCss(undefined), "0px");
  assert.equal(siteRadiusToCss("xl"), "0px");
});

test("broken theme falls back to defaults", () => {
  const fromNull = buildSiteUiVars(null);
  assert.equal(fromNull["--ui-background"], DEFAULT_SITE_THEME.bg_color.toUpperCase());

  const broken = buildSiteUiVars({
    ...DEFAULT_SITE_THEME,
    bg_color: "red",
    radius: "weird",
    font_family: "x",
  });
  assert.equal(broken["--ui-background"], DEFAULT_SITE_THEME.bg_color.toUpperCase());
  assert.equal(broken["--ui-radius"], "0px");
  assert.equal(broken["--ui-font"], SITE_UI_FONT_STACKS[DEFAULT_SITE_THEME.font_family]);
});

test("ui vars has exactly the documented keys", () => {
  const expectedKeys = [
    "--ui-accent",
    "--ui-accent-foreground",
    "--ui-background",
    "--ui-border",
    "--ui-brand",
    "--ui-brand-foreground",
    "--ui-card",
    "--ui-card-foreground",
    "--ui-font",
    "--ui-foreground",
    "--ui-input",
    "--ui-muted",
    "--ui-muted-foreground",
    "--ui-popover",
    "--ui-popover-foreground",
    "--ui-primary",
    "--ui-primary-foreground",
    "--ui-radius",
    "--ui-ring",
    "--ui-secondary",
    "--ui-secondary-foreground",
  ].sort();

  assert.deepEqual(Object.keys(buildSiteUiVars(null)).sort(), expectedKeys);
});
