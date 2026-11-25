// Run: node --test web/src/features/seller/site/siteEditorUtils.test.mjs (Node 22 type stripping)
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BLOCK_TYPE_ORDER,
  MAX_SECTIONS,
  SECTION_TYPE_ORDER,
  SECTION_V2_TYPE_ORDER,
  THEME_PRESETS,
  buildCleanSiteConfigPayload,
  buildCleanTemplateConfigPayload,
  checkThemeContrast,
  contrastRatio,
  createBlockTemplate,
  createDefaultSiteConfig,
  createDefaultTemplateConfig,
  createSectionTemplate,
  createSectionV2Template,
  stripPreviewJsClass,
  validateSafeUrlClient,
  validateSiteDraftClient,
  validateSiteTemplateJson,
  validateSubdomainClient,
  validateTemplateDraftClient,
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

// --- Schema v2 Template Studio tests ---

test("creating all 11 v2 section types and all 9 block types passes validateTemplateDraftClient", () => {
  assert.equal(SECTION_V2_TYPE_ORDER.length, 11);
  assert.equal(BLOCK_TYPE_ORDER.length, 9);

  const base = createDefaultTemplateConfig("Xưởng Dệt Bạch Đằng", "Áo thun cotton 220gsm");
  const allBlocks = BLOCK_TYPE_ORDER.map((bType, idx) => createBlockTemplate(bType, idx));
  const allSections = SECTION_V2_TYPE_ORDER.map((sType, idx) => {
    const sec = createSectionV2Template(sType, idx);
    if (idx === 0) {
      return { ...sec, blocks: allBlocks };
    }
    return sec;
  });

  const fullV2 = buildCleanTemplateConfigPayload({
    ...base,
    sections: allSections,
  });

  assert.equal(validateTemplateDraftClient("bach-dang", fullV2), null);
  assert.equal(fullV2.sections.length, 11);
  assert.equal(fullV2.sections[0].blocks?.length, 9);
});

test("validateTemplateDraftClient rejects XSS, style injection, SVG, and SSRF payloads", () => {
  const mk = () => createDefaultTemplateConfig("Xưởng Dệt Bạch Đằng");

  // XSS in text fields
  const xssScript = mk();
  xssScript.theme.tagline = "<script>alert(1)</script>";
  assert.notEqual(validateTemplateDraftClient("bach-dang", xssScript), null);

  const xssOnError = mk();
  xssOnError.theme.brand_name = "Shop onerror=alert(1)";
  assert.notEqual(validateTemplateDraftClient("bach-dang", xssOnError), null);

  const cssInject = mk();
  cssInject.layout.announcement_text = "expression(alert(1))";
  assert.notEqual(validateTemplateDraftClient("bach-dang", cssInject), null);

  const styleTag = mk();
  styleTag.theme.tagline = "<style>body{display:none}</style>";
  assert.notEqual(validateTemplateDraftClient("bach-dang", styleTag), null);

  // javascript: href in CTA block
  const jsHref = mk();
  jsHref.sections[0] = {
    ...jsHref.sections[0],
    blocks: [
      {
        id: "cta-bad",
        type: "cta",
        props: { label: "Click", href: "javascript:alert(1)", style: "primary" },
      },
    ],
  };
  assert.notEqual(validateTemplateDraftClient("bach-dang", jsHref), null);

  // SVG image rejection (.svg, .svgz, image/svg)
  for (const svgUrl of [
    "/icons/logo.svg",
    "https://cdn.example.com/banner.SVG?v=1",
    "/assets/pic.svgz",
    "https://cdn.example.com/proxy?type=image/svg+xml",
  ]) {
    const svgCfg = mk();
    svgCfg.theme.logo_url = svgUrl;
    assert.notEqual(
      validateTemplateDraftClient("bach-dang", svgCfg),
      null,
      `Expected SVG URL to be rejected: ${svgUrl}`
    );
  }

  // SSRF image URL rejection (private/loopback/metadata/IP literals)
  for (const ssrfUrl of [
    "https://127.0.0.1/a.png",
    "https://localhost/a.png",
    "https://169.254.169.254/latest/meta-data",
    "https://10.0.0.1/secret.png",
    "https://192.168.1.10/img.jpg",
    "https://[::1]/img.jpg",
    "https://metadata.google.internal/a.png",
    "https://2130706433/a.png",
  ]) {
    const ssrfCfg = mk();
    ssrfCfg.theme.banner_url = ssrfUrl;
    assert.notEqual(
      validateTemplateDraftClient("bach-dang", ssrfCfg),
      null,
      `Expected SSRF URL to be rejected: ${ssrfUrl}`
    );
  }
});

test("buildCleanTemplateConfigPayload preserves layout, slots, blocks, and 4 sub-pages losslessly", () => {
  const base = createDefaultTemplateConfig("  Xưởng Dệt Bạch Đằng  ");
  base.template_id = "  CRAFT-ATELIER ";
  base.theme.radius = "sm";
  base.theme.density = "compact";
  base.layout = {
    archetype: "craft-atelier",
    header_style: "atelier-framed",
    footer_style: "atelier-footer",
    container_width: "wide",
    card_style: "atelier",
    show_announcement: true,
    announcement_text: "  Giao nhanh toàn quốc  ",
    slots: { search: true, chat: false, assistant: true },
  };
  base.sections[0] = {
    ...base.sections[0],
    variant: "atelier",
    settings: {
      surface: "surface",
      padding: "generous",
      columns: 3,
      align: "center",
      max_items: 6,
    },
    blocks: [createBlockTemplate("stat", 0), createBlockTemplate("feature", 1)],
  };
  base.pages.catalog = {
    layout: "sku-table",
    columns: 4,
    show_filters: true,
    show_search: true,
    show_sku_matrix: true,
    intro_title: "  Kho đa SKU  ",
    intro_subtitle: "  Lọc theo màu & cỡ  ",
    sections: [createSectionV2Template("sku_matrix", 0)],
  };
  base.pages.pdp.layout = "sku-order-matrix";
  base.pages.pdp.show_sku_table = true;
  base.pages.cart.layout = "atelier-bag";
  base.pages.cart.trust_note = "  Kiểm hàng trước thanh toán  ";
  base.pages.info.layout = "craft-timeline";
  base.pages.info.show_toc = true;
  base.pages.info.sections = [createSectionV2Template("testimonials", 0)];

  const cleaned = buildCleanTemplateConfigPayload(base);
  const res = validateSiteTemplateJson(cleaned);
  assert.equal(res.ok, true, JSON.stringify(res.issues));
  assert.equal(cleaned.schema_version, 2);
  assert.equal(cleaned.template_id, "craft-atelier");
  assert.equal(cleaned.theme.brand_name, "Xưởng Dệt Bạch Đằng");
  assert.equal(cleaned.layout.header_style, "atelier-framed");
  assert.equal(cleaned.layout.slots.chat, false);
  assert.equal(cleaned.layout.announcement_text, "Giao nhanh toàn quốc");
  assert.equal(cleaned.sections[0].variant, "atelier");
  assert.equal(cleaned.sections[0].settings?.columns, 3);
  assert.equal(cleaned.sections[0].blocks?.length, 2);
  assert.equal(cleaned.pages.catalog.layout, "sku-table");
  assert.equal(cleaned.pages.catalog.show_sku_matrix, true);
  assert.equal(cleaned.pages.catalog.sections?.length, 1);
  assert.equal(cleaned.pages.pdp.layout, "sku-order-matrix");
  assert.equal(cleaned.pages.cart.trust_note, "Kiểm hàng trước thanh toán");
  assert.equal(cleaned.pages.info.layout, "craft-timeline");
  assert.equal(cleaned.pages.info.sections?.length, 1);
});

test("stripPreviewJsClass removes only standalone js token while keeping font and other classes", () => {
  assert.equal(stripPreviewJsClass("js __font_archivo_123"), "__font_archivo_123");
  assert.equal(
    stripPreviewJsClass("  __font_archivo   js   theme-dark  "),
    "__font_archivo theme-dark"
  );
  assert.equal(stripPreviewJsClass("js"), "");
  assert.equal(stripPreviewJsClass("jsx my-js js-ready js"), "jsx my-js js-ready");
  assert.equal(stripPreviewJsClass(""), "");
});
