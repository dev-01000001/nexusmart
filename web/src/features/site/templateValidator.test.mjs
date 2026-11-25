import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { checkThemeContrast } from "../../lib/site/theme.ts";
import { PLATFORM_TEMPLATES } from "./templateCatalog.ts";
import { MAX_BLOCKS_PER_SECTION, MAX_CONFIG_BYTES, MAX_HOME_SECTIONS } from "./templateTypes.ts";
import {
  convertV1ToV2,
  normalizeSiteTemplateConfig,
  sanitizeSafeImageUrl,
  validateImageUrlStrict,
  validateSafeHrefStrict,
  validateSiteTemplateJson,
} from "./templateValidator.ts";

function cloneBaseTemplate() {
  return JSON.parse(JSON.stringify(PLATFORM_TEMPLATES[0].config));
}

describe("PLATFORM_TEMPLATES library", () => {
  it("contains >= 8 templates with distinct archetypes covering all 5 views and WCAG >= 4.5:1", () => {
    assert.ok(PLATFORM_TEMPLATES.length >= 8, "expected at least 8 platform templates");

    const archetypes = new Set();
    const ids = new Set();

    for (const tpl of PLATFORM_TEMPLATES) {
      assert.ok(!ids.has(tpl.id), `duplicate template id: ${tpl.id}`);
      ids.add(tpl.id);
      archetypes.add(tpl.archetype);

      const json = JSON.stringify(tpl.config, null, 2);
      const res = validateSiteTemplateJson(json);
      assert.equal(
        res.ok,
        true,
        `template ${tpl.id} failed validation: ${JSON.stringify(res.issues)}`
      );
      assert.ok(res.config);

      // Must cover all 5 views: home (sections), catalog, pdp, cart, info
      assert.ok(res.config.sections.length >= 3, `${tpl.id} should define rich home sections`);
      assert.ok(res.config.pages.catalog.layout);
      assert.ok(res.config.pages.pdp.layout);
      assert.ok(res.config.pages.cart.layout);
      assert.ok(res.config.pages.info.layout);

      // WCAG AA >= 4.5:1 across all pairs
      const contrast = checkThemeContrast(res.config.theme, 4.5);
      assert.equal(
        contrast.ok,
        true,
        `template ${tpl.id} failed WCAG contrast: ${JSON.stringify(contrast)}`
      );
    }

    assert.equal(
      archetypes.size,
      PLATFORM_TEMPLATES.length,
      "each platform template must have a distinct layout archetype"
    );
  });
});

describe("XSS, style injection & SVG defense", () => {
  const xssPayloads = [
    "<script>alert(1)</script>",
    "javascript:alert(1)",
    "vbscript:x",
    "data:text/html,<script>alert(1)</script>",
    "onerror=alert(1)",
    "<style>body{color:red}</style>",
    "expression(alert(1))",
    "url(javascript:1)",
    "@import url(x)",
    "<svg onload=alert(1)>",
  ];

  for (const payload of xssPayloads) {
    it(`rejects XSS/style injection payload in text fields: ${payload}`, () => {
      const cfg = cloneBaseTemplate();
      cfg.theme.tagline = payload;
      const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
      assert.equal(res.ok, false, `expected rejection for payload: ${payload}`);
      assert.ok(
        res.issues.some((i) => i.code === "XSS_REJECTED"),
        `expected XSS_REJECTED for ${payload}, got ${JSON.stringify(res.issues)}`
      );
    });
  }

  const dangerousHrefs = ["javascript:alert(1)", "vbscript:x", "data:text/html,<h1>x</h1>"];
  for (const href of dangerousHrefs) {
    it(`rejects dangerous href protocol: ${href}`, () => {
      assert.ok(validateSafeHrefStrict(href) !== null);
      const cfg = cloneBaseTemplate();
      cfg.sections[0].props.cta_href = href;
      const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
      assert.equal(res.ok, false);
      assert.ok(res.issues.some((i) => i.code === "UNSAFE_URL"));
    });
  }

  const svgUrls = [
    "/icons/logo.svg",
    "https://cdn.example.com/a.SVG?x=1",
    "/a.svgz",
    "data:image/svg+xml,<svg/>",
  ];

  for (const svgUrl of svgUrls) {
    it(`rejects SVG image URL: ${svgUrl}`, () => {
      assert.ok(validateImageUrlStrict(svgUrl) !== null);
      assert.equal(sanitizeSafeImageUrl(svgUrl), "");

      const cfg = cloneBaseTemplate();
      cfg.theme.logo_url = svgUrl;
      const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
      assert.equal(res.ok, false);
      assert.ok(res.issues.some((i) => i.code === "UNSAFE_IMAGE_URL"));
    });
  }
});

describe("SSRF defense on image URLs", () => {
  const ssrfUrls = [
    "https://127.0.0.1/a.png",
    "https://localhost/a.png",
    "https://169.254.169.254/latest/meta-data",
    "https://10.0.0.1/a.png",
    "https://192.168.1.1/a.png",
    "https://172.16.0.1/a.png",
    "https://[::1]/a.png",
    "https://[::ffff:127.0.0.1]/a.png",
    "https://metadata.google.internal/a.png",
    "https://redis.internal/a.png",
    "https://2130706433/a.png",
    "https://intranet/a.png",
  ];

  for (const badUrl of ssrfUrls) {
    it(`blocks SSRF image target: ${badUrl}`, () => {
      assert.ok(validateImageUrlStrict(badUrl) !== null, `should block ${badUrl}`);
      assert.equal(sanitizeSafeImageUrl(badUrl), "");

      const cfg = cloneBaseTemplate();
      cfg.theme.banner_url = badUrl;
      const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
      assert.equal(res.ok, false);
      assert.ok(res.issues.some((i) => i.code === "UNSAFE_IMAGE_URL"));
    });
  }

  it("allows safe public https and single-slash relative raster images", () => {
    assert.equal(validateImageUrlStrict("/banners/vietnam-workshop.jpg"), null);
    assert.equal(validateImageUrlStrict("https://cdn.nexusmart.vn/assets/hero.webp"), null);
    assert.equal(
      sanitizeSafeImageUrl("https://cdn.nexusmart.vn/assets/hero.webp"),
      "https://cdn.nexusmart.vn/assets/hero.webp"
    );
  });
});

describe("Structural limits & parser hardening", () => {
  it("rejects payload larger than 128 KiB", () => {
    const huge = `{"padding":"${"a".repeat(MAX_CONFIG_BYTES + 100)}"}`;
    const res = validateSiteTemplateJson(huge);
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "CONFIG_TOO_LARGE"));
  });

  it("rejects JSON nesting depth > 8", () => {
    const deep = '{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":1}}}}}}}}}';
    const res = validateSiteTemplateJson(deep);
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "MAX_DEPTH_EXCEEDED"));
  });

  it("rejects duplicate keys in the same JSON object", () => {
    const cfg = cloneBaseTemplate();
    const formatted = JSON.stringify(cfg, null, 2).replace(
      '"template_id": "minimal-fashion",',
      '"template_id": "minimal-fashion",\n  "template_id": "editorial",'
    );
    const res = validateSiteTemplateJson(formatted);
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "DUPLICATE_KEY"));
  });

  it("rejects unknown fields strictly", () => {
    const cfg = cloneBaseTemplate();
    cfg.unexpected_root_field = true;
    const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "UNKNOWN_FIELD"));
  });

  it("rejects trailing tokens after root JSON object", () => {
    const cfg = cloneBaseTemplate();
    const res = validateSiteTemplateJson(`${JSON.stringify(cfg)} {"extra":1}`);
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "TRAILING_TOKEN"));
  });

  it("rejects > 20 home sections", () => {
    const cfg = cloneBaseTemplate();
    while (cfg.sections.length <= MAX_HOME_SECTIONS) {
      cfg.sections.push({
        id: `extra-sec-${cfg.sections.length}`,
        type: "text",
        visible: true,
        props: { title: "Mục thêm", body: "Nội dung kiểm thử giới hạn section." },
      });
    }
    const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "TOO_MANY_SECTIONS"));
  });

  it("rejects > 12 blocks in a section", () => {
    const cfg = cloneBaseTemplate();
    cfg.sections[0].blocks = Array.from({ length: MAX_BLOCKS_PER_SECTION + 1 }, (_, i) => ({
      id: `blk-${i}`,
      type: "badge",
      props: { label: `Nhãn ${i + 1}`, tone: "neutral" },
    }));
    const res = validateSiteTemplateJson(JSON.stringify(cfg, null, 2));
    assert.equal(res.ok, false);
    assert.ok(res.issues.some((i) => i.code === "TOO_MANY_BLOCKS"));
  });
});

describe("Line & column diagnostic accuracy", () => {
  it("reports exact 1-based line and column for schema & security errors", () => {
    const cfg = cloneBaseTemplate();
    cfg.theme.bg_color = "not-a-hex";
    cfg.sections[0].props.title = "<script>alert(1)</script>";
    const formatted = JSON.stringify(cfg, null, 2);
    const lines = formatted.split("\n");

    const bgLineIdx = lines.findIndex((l) => l.includes('"bg_color"'));
    const titleLineIdx = lines.findIndex((l) => l.includes('"<script>alert(1)</script>"'));
    assert.ok(bgLineIdx >= 0 && titleLineIdx >= 0);

    const res = validateSiteTemplateJson(formatted);
    assert.equal(res.ok, false);

    const bgIssue = res.issues.find((i) => i.path === "theme.bg_color");
    assert.ok(bgIssue, "expected theme.bg_color issue");
    assert.equal(bgIssue.line, bgLineIdx + 1);
    assert.equal(bgIssue.column, lines[bgLineIdx].indexOf('"bg_color"') + 1);

    const xssIssue = res.issues.find((i) => i.path === "sections[0].props.title");
    assert.ok(xssIssue, "expected sections[0].props.title XSS issue");
    assert.equal(xssIssue.line, titleLineIdx + 1);
    assert.equal(xssIssue.column, lines[titleLineIdx].indexOf('"title"') + 1);
  });
});

describe("Legacy v1 -> v2 conversion for all 4 seeded sites", () => {
  const seededV1Sites = [
    {
      subdomain: "bach-dang",
      config: {
        theme: {
          bg_color: "#F8F6F1",
          surface_color: "#FFFFFF",
          text_color: "#182026",
          accent_color: "#1D4ED8",
          accent_text_color: "#FFFFFF",
          font_family: "work-sans",
          logo_url: "/banners/vietnam-workshop.jpg",
          banner_url: "/banners/campaign-everyday.jpg",
          brand_name: "Xưởng Dệt Bạch Đằng",
          tagline: "Áo thun cotton Compact 220gsm và trang phục cơ bản từ xưởng dệt Hải Phòng.",
        },
        sections: [
          {
            id: "hero-bd",
            type: "hero",
            visible: true,
            props: {
              eyebrow: "Xưởng Dệt Hải Phòng",
              title: "Cotton Compact 220gsm giữ phom bền bỉ",
              subtitle: "Sản xuất trực tiếp tại xưởng Bạch Đằng, giao nhanh toàn quốc.",
              image_url: "/banners/campaign-everyday.jpg",
              cta_label: "Xem sản phẩm",
              cta_href: "/san-pham",
            },
          },
          {
            id: "featured-bd",
            type: "featured_products",
            visible: true,
            props: { title: "Sản phẩm bán chạy", limit: 8 },
          },
          {
            id: "policies-bd",
            type: "policies",
            visible: true,
            props: {
              title: "Cam kết xưởng",
              items: [{ heading: "Đổi size 45 ngày", detail: "Hỗ trợ đổi cỡ tận nhà hai chiều." }],
            },
          },
        ],
      },
    },
    {
      subdomain: "sai-gon",
      config: {
        theme: {
          bg_color: "#14181C",
          surface_color: "#1E242B",
          text_color: "#F4F6F8",
          accent_color: "#D97706",
          accent_text_color: "#111418",
          font_family: "be-vietnam-pro",
          logo_url: "/banners/campaign-running.jpg",
          banner_url: "/banners/campaign-running.jpg",
          brand_name: "Sài Gòn Active Wear",
          tagline: "Trang phục thể thao Dry-Fit thoát ẩm nhanh cho chạy bộ và phòng tập.",
        },
        sections: [
          {
            id: "hero-sg",
            type: "hero",
            visible: true,
            props: {
              title: "Đồ tập Dry-Fit siêu nhẹ cho khí hậu nhiệt đới",
              image_url: "/banners/campaign-running.jpg",
              cta_label: "Chọn đồ tập",
              cta_href: "/san-pham",
            },
          },
          {
            id: "vouchers-sg",
            type: "voucher_strip",
            visible: true,
            props: { title: "Mã ưu đãi Sài Gòn Active", limit: 4 },
          },
        ],
      },
    },
    {
      subdomain: "da-nang",
      config: {
        theme: {
          bg_color: "#EEF2EA",
          surface_color: "#FFFFFF",
          text_color: "#1A2B22",
          accent_color: "#3F6B4F",
          accent_text_color: "#FFFFFF",
          font_family: "ibm-plex-sans",
          logo_url: "/banners/campaign-green-fiber.jpg",
          banner_url: "/banners/campaign-green-fiber.jpg",
          brand_name: "Sợi Xanh Đà Nẵng",
          tagline: "Chất liệu linen, bamboo và modal tự nhiên thoáng mát quanh năm.",
        },
        sections: [
          {
            id: "hero-dn",
            type: "hero",
            visible: true,
            props: {
              title: "Sợi tự nhiên thoáng nhẹ cho ngày nắng miền Trung",
              image_url: "/banners/campaign-green-fiber.jpg",
              cta_label: "Khám phá chất liệu",
              cta_href: "/san-pham",
            },
          },
          {
            id: "text-dn",
            type: "text",
            visible: true,
            props: {
              title: "Triết lý sợi lành tính",
              body: "Ưu tiên sợi tái tạo và quy trình nhuộm ít nước để bảo vệ làn da.",
              align: "left",
            },
          },
        ],
      },
    },
    {
      subdomain: "ha-noi",
      config: {
        theme: {
          bg_color: "#2B1A1E",
          surface_color: "#3A2429",
          text_color: "#F7F3EE",
          accent_color: "#E9B949",
          accent_text_color: "#1C1114",
          font_family: "plus-jakarta-sans",
          logo_url: "/banners/fabric-closeup.jpg",
          banner_url: "/banners/campaign-office.jpg",
          brand_name: "Dệt Kim Hà Nội",
          tagline: "Áo polo dệt kim, jogger nỉ chân cua và tất cotton gân theo tay nghề miền Bắc.",
        },
        sections: [
          {
            id: "hero-hn",
            type: "hero",
            visible: true,
            props: {
              title: "Dệt kim chuẩn phom từ tay nghề Hà Nội",
              image_url: "/banners/campaign-office.jpg",
              cta_label: "Xem bộ sưu tập",
              cta_href: "/san-pham",
            },
          },
          {
            id: "banner-hn",
            type: "banner",
            visible: true,
            props: {
              title: "Bộ sưu tập Thu Đông dệt kim",
              cta_label: "Khám phá ngay",
              cta_href: "/san-pham",
            },
          },
        ],
      },
    },
  ];

  for (const seed of seededV1Sites) {
    it(`converts legacy v1 config for ${seed.subdomain} cleanly to canonical v2`, () => {
      const converted = convertV1ToV2(JSON.stringify(seed.config, null, 2));
      assert.equal(
        converted.ok,
        true,
        `${seed.subdomain} v1->v2 failed: ${JSON.stringify(converted.issues)}`
      );
      assert.ok(converted.config);
      assert.equal(converted.config.schema_version, 2);
      assert.equal(converted.config.template_id, "legacy-v1");
      assert.equal(converted.config.theme.brand_name, seed.config.theme.brand_name);
      assert.equal(converted.config.theme.font_family, seed.config.theme.font_family);
      assert.equal(converted.config.theme.bg_color, seed.config.theme.bg_color);
      assert.equal(converted.config.theme.accent_color, seed.config.theme.accent_color);
      assert.equal(converted.config.sections.length, seed.config.sections.length);
      assert.ok(converted.config.layout.archetype);
      assert.ok(converted.config.pages.catalog.layout);
      assert.ok(converted.config.pages.pdp.layout);
      assert.ok(converted.config.pages.cart.layout);
      assert.ok(converted.config.pages.info.layout);

      // Re-validating the converted v2 JSON must pass cleanly as v2
      const roundTrip = validateSiteTemplateJson(JSON.stringify(converted.config, null, 2));
      assert.equal(roundTrip.ok, true, `${seed.subdomain} v2 round-trip failed`);

      // normalizeSiteTemplateConfig preserves everything without throwing
      const normalized = normalizeSiteTemplateConfig(seed.config, seed.config.theme.brand_name);
      assert.ok(normalized);
      assert.equal(normalized.theme.brand_name, seed.config.theme.brand_name);
      assert.equal(normalized.sections.length, seed.config.sections.length);
    });
  }
});
