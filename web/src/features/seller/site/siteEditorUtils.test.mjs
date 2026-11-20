// Run: node --test web/src/features/seller/site/siteEditorUtils.test.mjs (Node 22 type stripping)
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MAX_SECTIONS,
  SECTION_TYPE_ORDER,
  THEME_PRESETS,
  buildCleanSiteConfigPayload,
  checkThemeContrast,
  contrastRatio,
  createDefaultSiteConfig,
  createSectionTemplate,
  validateSafeUrlClient,
  validateSiteDraftClient,
  validateSubdomainClient,
} from "./siteEditorUtils.ts";

test("all curated THEME_PRESETS pass WCAG 2.1 >= 4.5 on every required pair", () => {
  assert.ok(THEME_PRESETS.length >= 4);
  for (const preset of THEME_PRESETS) {
    const checks = checkThemeContrast({
      ...preset.colors,
      font_family: "work-sans",
      brand_name: "Xưởng Dệt Bạch Đằng",
    });
    for (const check of checks) {
      assert.equal(
        check.passes,
        true,
        `Preset ${preset.id} pair ${check.id} contrast ${check.ratio} must be >= 4.5`
      );
    }
  }
});

test("contrastRatio detects low-contrast combinations", () => {
  const low = contrastRatio("#FFFFFF", "#B0B8C0");
  assert.ok(low !== null && low < 4.5);

  const high = contrastRatio("#FFFFFF", "#1F3038");
  assert.ok(high !== null && high > 12);

  const cfg = createDefaultSiteConfig("Xưởng Dệt Bạch Đằng");
  cfg.theme.text_color = "#C0C5CA";
  const err = validateSiteDraftClient("bach-dang", cfg);
  assert.ok(err && err.includes("4,5:1"));
});

test("validateSubdomainClient accepts valid shop subdomains and blocks reserved/phishing names", () => {
  assert.equal(validateSubdomainClient("bach-dang"), null);
  assert.equal(validateSubdomainClient("sai-gon-2026"), null);
  assert.equal(validateSubdomainClient("abc"), null);

  assert.notEqual(validateSubdomainClient("ab"), null);
  assert.notEqual(validateSubdomainClient("-bach-dang"), null);
  assert.notEqual(validateSubdomainClient("bach-dang-"), null);
  assert.notEqual(validateSubdomainClient("bach_dang"), null);
  assert.notEqual(validateSubdomainClient("www"), null);
  assert.notEqual(validateSubdomainClient("admin"), null);
  assert.notEqual(validateSubdomainClient("seller"), null);
  assert.notEqual(validateSubdomainClient("api"), null);
  assert.notEqual(validateSubdomainClient("nexusmart"), null);
  assert.notEqual(validateSubdomainClient("vietcombank-uudai"), null);
  assert.notEqual(validateSubdomainClient("nexusmart-sale"), null);
});

test("validateSafeUrlClient allows empty, single-slash relative, and https URLs only", () => {
  assert.equal(validateSafeUrlClient(""), null);
  assert.equal(validateSafeUrlClient("/banners/campaign-everyday.jpg"), null);
  assert.equal(validateSafeUrlClient("/san-pham?cat=ao-thun"), null);
  assert.equal(validateSafeUrlClient("https://cdn.nexusmart.vn/logo.png"), null);

  assert.notEqual(validateSafeUrlClient("javascript:alert(1)"), null);
  assert.notEqual(validateSafeUrlClient("JAVASCRIPT:void(0)"), null);
  assert.notEqual(validateSafeUrlClient("data:text/html,<script>alert(1)</script>"), null);
  assert.notEqual(validateSafeUrlClient("http://cdn.nexusmart.vn/logo.png"), null);
  assert.notEqual(validateSafeUrlClient("//cdn.nexusmart.vn/logo.png"), null);
  assert.notEqual(validateSafeUrlClient("/banners\\evil.jpg"), null);
});

test("default config and all 7 section templates pass full draft validation", () => {
  const cfg = createDefaultSiteConfig("Xưởng Dệt Bạch Đằng", "Áo thun cotton 220gsm Hải Phòng");
  assert.equal(validateSiteDraftClient("bach-dang", cfg), null);

  const allSeven = SECTION_TYPE_ORDER.map((type, idx) => createSectionTemplate(type, idx));
  const fullCfg = buildCleanSiteConfigPayload({
    ...cfg,
    sections: allSeven,
  });
  assert.equal(validateSiteDraftClient("bach-dang", fullCfg), null);
});

test("validateSiteDraftClient rejects more than 20 sections", () => {
  const cfg = createDefaultSiteConfig("Xưởng Dệt Bạch Đằng");
  cfg.sections = Array.from({ length: MAX_SECTIONS + 1 }, (_, i) =>
    createSectionTemplate("text", i)
  );
  const err = validateSiteDraftClient("bach-dang", cfg);
  assert.ok(err && err.includes(String(MAX_SECTIONS)));
});
