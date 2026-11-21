package site

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBuiltInTemplatesValidDistinctAndAccessible(t *testing.T) {
	templates := BuiltInTemplates()
	if len(templates) != 8 {
		t.Fatalf("expected 8 built-in templates, got %d", len(templates))
	}

	seenIDs := make(map[string]bool)
	seenArchetypes := make(map[string]bool)
	seenHeaders := make(map[string]bool)
	seenPDPLayouts := make(map[string]bool)

	for _, tmpl := range templates {
		if seenIDs[tmpl.ID] {
			t.Fatalf("duplicate template ID %q", tmpl.ID)
		}
		seenIDs[tmpl.ID] = true

		if tmpl.ID != tmpl.Slug {
			t.Errorf("template %q slug %q mismatch", tmpl.ID, tmpl.Slug)
		}
		if _, err := ValidateImageURL(tmpl.PreviewImageURL); err != nil {
			t.Errorf("template %q invalid preview_image_url %q: %v", tmpl.ID, tmpl.PreviewImageURL, err)
		}

		canonical, err := tmpl.ConfigJSON()
		if err != nil {
			t.Fatalf("template %q ConfigJSON failed: %v", tmpl.ID, err)
		}

		v2, canonical2, err := ValidateAndCanonicalizeConfigV2(canonical)
		if err != nil {
			t.Fatalf("template %q ValidateAndCanonicalizeConfigV2 failed: %v", tmpl.ID, err)
		}
		if !bytes.Equal(canonical, canonical2) {
			t.Errorf("template %q canonicalization is not idempotent", tmpl.ID)
		}

		// Also verify entry point ValidateAndCanonicalizeConfig routes v2 cleanly.
		summary, viaMain, err := ValidateAndCanonicalizeConfig(canonical)
		if err != nil {
			t.Fatalf("template %q ValidateAndCanonicalizeConfig failed: %v", tmpl.ID, err)
		}
		if !bytes.Equal(canonical, viaMain) {
			t.Errorf("template %q ValidateAndCanonicalizeConfig returned different bytes", tmpl.ID)
		}
		if summary.Theme.BrandName != v2.Theme.BrandName || len(summary.Sections) != len(v2.Sections) {
			t.Errorf("template %q summary projection mismatch", tmpl.ID)
		}

		// Verify WCAG AA >= 4.5:1 explicitly on all 3 pairs.
		for _, pair := range [][2]string{
			{v2.Theme.BGColor, v2.Theme.TextColor},
			{v2.Theme.SurfaceColor, v2.Theme.TextColor},
			{v2.Theme.AccentColor, v2.Theme.AccentTextColor},
		} {
			ratio, err := ContrastRatio(pair[0], pair[1])
			if err != nil || ratio < MinContrastRatio {
				t.Errorf("template %q contrast (%s vs %s) = %.2f (err=%v), want >= %.1f", tmpl.ID, pair[0], pair[1], ratio, err, MinContrastRatio)
			}
		}

		// Verify all 5 pages present (home sections + 4 sub-pages).
		if len(v2.Sections) < 4 {
			t.Errorf("template %q should have at least 4 home sections, got %d", tmpl.ID, len(v2.Sections))
		}
		if v2.Pages.Catalog.Layout == "" || v2.Pages.PDP.Layout == "" || v2.Pages.Cart.Layout == "" || v2.Pages.Info.Layout == "" {
			t.Errorf("template %q missing one of the 4 page layouts: %+v", tmpl.ID, v2.Pages)
		}

		seenArchetypes[v2.Layout.Archetype] = true
		seenHeaders[v2.Layout.HeaderStyle] = true
		seenPDPLayouts[v2.Pages.PDP.Layout] = true
	}

	if len(seenArchetypes) != 8 {
		t.Errorf("expected 8 distinct archetypes across templates, got %d", len(seenArchetypes))
	}
	if len(seenHeaders) != 8 {
		t.Errorf("expected 8 distinct header styles across templates, got %d", len(seenHeaders))
	}
	if len(seenPDPLayouts) != 8 {
		t.Errorf("expected 8 distinct PDP layouts across templates, got %d", len(seenPDPLayouts))
	}
}

func TestMigration0004SiteTemplatesJSONBValidV2(t *testing.T) {
	migPath := filepath.Join("..", "..", "migrations", "0004_site_templates.sql")
	content, err := os.ReadFile(migPath)
	if err != nil {
		t.Fatalf("read migration 0004_site_templates.sql: %v", err)
	}

	re := regexp.MustCompile(`(?s)\$cfg\$(.*?)\$cfg\$::jsonb`)
	matches := re.FindAllSubmatch(content, -1)
	if len(matches) != 8 {
		t.Fatalf("expected 8 template JSONB payloads in 0004_site_templates.sql, found %d", len(matches))
	}

	seen := make(map[string]bool)
	for i, m := range matches {
		raw := m[1]
		v2, _, err := ValidateAndCanonicalizeConfigV2(raw)
		if err != nil {
			t.Fatalf("0004_site_templates.sql payload #%d invalid v2: %v", i+1, err)
		}
		seen[v2.TemplateID] = true
	}
	if len(seen) != 8 {
		t.Fatalf("expected 8 unique template_ids in 0004_site_templates.sql, got %d", len(seen))
	}
}

func TestSecurityRejectsXSSStyleInjectionAndSVG(t *testing.T) {
	baseV2Bytes, err := BuiltInTemplates()[0].ConfigJSON()
	if err != nil {
		t.Fatalf("base template: %v", err)
	}

	badPayloads := []string{
		`<script>alert(1)</script>`,
		`hello </SCRIPT> world`,
		`<style>body{display:none}</style>`,
		`</style>x`,
		`<svg onload=alert(1)>`,
		`</svg>`,
		`<iframe src="https://evil.com">`,
		`<object data="x">`,
		`<embed src="x">`,
		`<link rel="stylesheet" href="x">`,
		`<meta http-equiv="refresh">`,
		`img onerror=alert(1)`,
		`btn onclick = "hack()"`,
		`javascript:alert(1)`,
		`VBSCRIPT:msgbox(1)`,
		`data:text/html,boom`,
		`width: expression(alert(1))`,
		`background: url(https://evil.com/x.png)`,
		`@import "evil.css"`,
		`-moz-binding: foo`,
	}

	for _, bad := range badPayloads {
		// 1. Test in v2 theme.tagline
		var doc map[string]any
		if err := json.Unmarshal(baseV2Bytes, &doc); err != nil {
			t.Fatalf("unmarshal base: %v", err)
		}
		doc["theme"].(map[string]any)["tagline"] = bad
		b, _ := json.Marshal(doc)
		if _, _, err := ValidateAndCanonicalizeConfigV2(b); err == nil {
			t.Errorf("v2 theme.tagline accepted dangerous payload %q", bad)
		}

		// 2. Test in v1 text props.body
		v1 := validBaseConfig()
		v1["sections"] = []any{
			map[string]any{
				"id":      "txt-sec",
				"type":    "text",
				"visible": true,
				"props": map[string]any{
					"title": "Clean title",
					"body":  bad,
					"align": "left",
				},
			},
		}
		if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, v1)); err == nil {
			t.Errorf("v1 text.body accepted dangerous payload %q", bad)
		}
	}

	// Test SVG image URLs rejected by ValidateImageURL (and in v2 image block / theme.logo_url).
	badSVGURLs := []string{
		"/icons/logo.svg",
		"/icons/logo.SVG",
		"/icons/logo.svgz",
		"https://cdn.example.com/vector.svg?v=1",
		"https://cdn.example.com/vector.svg#frag",
		"https://cdn.example.com/vector%2esvg",
		"https://cdn.example.com/vector%252esvg",
		"https://cdn.example.com/proxy?mime=image/svg+xml",
		"https://cdn.example.com/proxy?mime=image%2Fsvg%2Bxml",
		"data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'/>",
		"javascript:alert(1)",
	}
	for _, u := range badSVGURLs {
		if _, err := ValidateImageURL(u); err == nil {
			t.Errorf("ValidateImageURL(%q) expected error for SVG/unsafe URL, got nil", u)
		}
	}
}

func TestValidateImageURLSSRFDefense(t *testing.T) {
	allowed := []string{
		"",
		"/banners/vietnam-workshop.jpg",
		"/atmosphere/cotton-twill-slate.jpg?w=800",
		"https://cdn.example.com/banners/hero.jpg",
		"https://images.nexusmart.vn/uploads/a/b.webp?v=2",
	}
	for _, u := range allowed {
		if _, err := ValidateImageURL(u); err != nil {
			t.Errorf("ValidateImageURL(%q) unexpectedly failed: %v", u, err)
		}
	}

	blocked := []string{
		"http://cdn.example.com/a.jpg",
		"//cdn.example.com/a.jpg",
		"https://127.0.0.1/a.png",
		"https://127.0.0.1:8443/a.png",
		"https://localhost/a.png",
		"https://sub.localhost/a.png",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/a.png",
		"https://192.168.1.1/a.png",
		"https://172.16.0.1/a.png",
		"https://172.31.255.254/a.png",
		"https://[::1]/a.png",
		"https://[::ffff:127.0.0.1]/a.png",
		"https://[fe80::1]/a.png",
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://service.internal/a.png",
		"https://printer.local/a.png",
		"https://db.localdomain/a.png",
		"https://router.home.arpa/a.png",
		"https://2130706433/a.png",
		"https://0x7f000001/a.png",
		"https://0177.0.0.1/a.png",
		"https://127.1/a.png",
		"https://intranet/a.png",
		"https://user:pass@cdn.example.com/a.png",
	}
	for _, u := range blocked {
		if _, err := ValidateImageURL(u); err == nil {
			t.Errorf("ValidateImageURL(%q) expected SSRF rejection, got nil", u)
		}
	}
}

func TestStructuralLimitsDuplicateKeysUnknownFieldsAndCounts(t *testing.T) {
	baseBytes, err := BuiltInTemplates()[0].ConfigJSON()
	if err != nil {
		t.Fatalf("base template: %v", err)
	}

	// 1. Depth > 8 rejected.
	deepJSON := []byte(`{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":1}}}}}}}}}`)
	if _, _, err := ValidateAndCanonicalizeConfig(deepJSON); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("expected depth > 8 error, got %v", err)
	}
	deepArrayJSON := []byte(`{"a":[[[[[[[[]]]]]]]]}`)
	if _, _, err := ValidateAndCanonicalizeConfig(deepArrayJSON); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("expected array depth > 8 error, got %v", err)
	}

	// 2. Payload > 128 KiB rejected.
	oversize := make([]byte, MaxConfigBytes+10)
	oversize[0] = '{'
	for i := 1; i < len(oversize)-1; i++ {
		oversize[i] = ' '
	}
	oversize[len(oversize)-1] = '}'
	if _, _, err := ValidateAndCanonicalizeConfig(oversize); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversize error, got %v", err)
	}

	// 3. Duplicate keys at root or nested object rejected.
	dupRoot := []byte(`{"schema_version":2,"schema_version":2,"template_id":"minimal-fashion"}`)
	if _, _, err := ValidateAndCanonicalizeConfig(dupRoot); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate root key error, got %v", err)
	}
	dupNested := bytes.Replace(baseBytes, []byte(`"radius":"none"`), []byte(`"radius":"none","radius":"sm"`), 1)
	if _, _, err := ValidateAndCanonicalizeConfig(dupNested); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate nested key error, got %v", err)
	}
	dupUnicode := []byte(`{"\u0061":1,"a":2}`)
	if _, _, err := ValidateAndCanonicalizeConfig(dupUnicode); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected unicode-escaped duplicate key error, got %v", err)
	}

	// 4. Unknown fields rejected in v2 (root, layout, page, block).
	freshDoc := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal(baseBytes, &m); err != nil {
			t.Fatalf("unmarshal base: %v", err)
		}
		return m
	}

	doc := freshDoc()
	doc["injected_css"] = "body{}"
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil {
		t.Fatalf("expected unknown root field error in v2")
	}

	doc = freshDoc()
	doc["layout"].(map[string]any)["raw_header"] = "<div>"
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil {
		t.Fatalf("expected unknown layout field error in v2")
	}

	// 5. Trailing tokens rejected.
	trailing := append(append([]byte(nil), baseBytes...), []byte(` {"extra":true}`)...)
	if _, _, err := ValidateAndCanonicalizeConfigV2(trailing); err == nil {
		t.Fatalf("expected trailing token error")
	}

	// 6. Unsupported schema_version rejected.
	doc = freshDoc()
	doc["schema_version"] = 3
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, doc)); err == nil {
		t.Fatalf("expected error for schema_version=3")
	}

	// 7. >20 home sections rejected.
	doc = freshDoc()
	manySecs := make([]any, 21)
	for i := range manySecs {
		manySecs[i] = map[string]any{
			"id":      "s",
			"type":    "text",
			"visible": true,
			"props":   map[string]any{"title": "T", "body": "B"},
		}
	}
	doc["sections"] = manySecs
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil || !strings.Contains(err.Error(), "TOO_MANY_SECTIONS") {
		t.Fatalf("expected TOO_MANY_SECTIONS for >20 home sections, got %v", err)
	}

	// 8. >12 blocks in a section rejected.
	doc = freshDoc()
	manyBlocks := make([]any, 13)
	for i := range manyBlocks {
		manyBlocks[i] = map[string]any{
			"id":    "b",
			"type":  "badge",
			"props": map[string]any{"label": "OK", "tone": "neutral"},
		}
	}
	doc["sections"] = []any{
		map[string]any{
			"id":      "sec-1",
			"type":    "text",
			"visible": true,
			"props":   map[string]any{"title": "T", "body": "B"},
			"blocks":  manyBlocks,
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil || !strings.Contains(err.Error(), "TOO_MANY_BLOCKS") {
		t.Fatalf("expected TOO_MANY_BLOCKS for >12 blocks, got %v", err)
	}

	// 9. >8 sections on a sub-page rejected & >32 total sections across home + pages rejected.
	doc = freshDoc()
	ninePageSecs := make([]any, 9)
	for i := range ninePageSecs {
		ninePageSecs[i] = map[string]any{
			"id":      "p",
			"type":    "text",
			"visible": true,
			"props":   map[string]any{"title": "T", "body": "B"},
		}
	}
	doc["pages"].(map[string]any)["catalog"].(map[string]any)["sections"] = ninePageSecs
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil || !strings.Contains(err.Error(), "TOO_MANY_SECTIONS") {
		t.Fatalf("expected TOO_MANY_SECTIONS for >8 catalog page sections, got %v", err)
	}

	doc = freshDoc()
	home20 := make([]any, 20)
	for i := range home20 {
		home20[i] = map[string]any{"id": "h", "type": "text", "visible": true, "props": map[string]any{"title": "T", "body": "B"}}
	}
	page7 := make([]any, 7)
	for i := range page7 {
		page7[i] = map[string]any{"id": "p", "type": "text", "visible": true, "props": map[string]any{"title": "T", "body": "B"}}
	}
	doc["sections"] = home20
	pagesMap := doc["pages"].(map[string]any)
	pagesMap["catalog"].(map[string]any)["sections"] = page7
	pagesMap["pdp"].(map[string]any)["sections"] = page7
	// 20 + 7 + 7 = 34 > 32
	if _, _, err := ValidateAndCanonicalizeConfigV2(mustMarshal(t, doc)); err == nil || !strings.Contains(err.Error(), "TOO_MANY_SECTIONS") {
		t.Fatalf("expected TOO_MANY_SECTIONS for 34 total sections, got %v", err)
	}
}

func TestConvertV1ToV2AllFourSeededSites(t *testing.T) {
	// Exact v1 configurations matching tools/seed/seed.mjs (bach-dang, sai-gon, da-nang, ha-noi).
	seededSites := map[string]map[string]any{
		"bach-dang": {
			"theme": map[string]any{
				"bg_color":          "#F8F6F1",
				"surface_color":     "#FFFFFF",
				"text_color":        "#182026",
				"accent_color":      "#1D4ED8",
				"accent_text_color": "#FFFFFF",
				"font_family":       "work-sans",
				"logo_url":          "/banners/vietnam-workshop.jpg",
				"banner_url":        "/banners/campaign-everyday.jpg",
				"brand_name":        "Xưởng Dệt Bạch Đằng",
				"tagline":           "Áo thun Cotton Compact 220gsm may trực tiếp tại xưởng Hải Phòng",
			},
			"sections": []any{
				map[string]any{
					"id": "bd-hero", "type": "hero", "visible": true,
					"props": map[string]any{
						"title":     "Cotton Compact 220gsm từ xưởng Hải Phòng",
						"subtitle":  "Dệt kim tròn chủ động, xử lý chống co rút trước khi cắt rập. Mặt vải mịn đứng vai và giữ form cổ áo bền bỉ qua hơn 40 lần giặt máy.",
						"image_url": "/banners/campaign-everyday.jpg",
						"cta_label": "Xem sản phẩm xưởng",
						"cta_href":  "/san-pham",
					},
				},
				map[string]any{
					"id": "bd-featured", "type": "featured_products", "visible": true,
					"props": map[string]any{
						"title":       "Sản phẩm chủ lực tại xưởng",
						"subtitle":    "Đồ mặc hằng ngày từ bông chải kỹ 220gsm và sợi gỗ sồi Modal, giá trực tiếp từ Xưởng Dệt Bạch Đằng.",
						"product_ids": []string{"prod-at-trang", "prod-at-xamda", "prod-at-xanhthan", "prod-dolot-boxer"},
						"limit":       4,
					},
				},
				map[string]any{
					"id": "bd-vouchers", "type": "voucher_strip", "visible": true,
					"props": map[string]any{
						"title":    "Ưu đãi riêng của Xưởng Dệt Bạch Đằng",
						"subtitle": "Lưu mã giảm giá gian hàng và áp dụng trực tiếp khi đặt mua từ cửa hàng.",
						"limit":    4,
					},
				},
				map[string]any{
					"id": "bd-collection", "type": "collection", "visible": true,
					"props": map[string]any{
						"title":           "Dòng áo thun Cotton Compact 220gsm",
						"subtitle":        "Ba tông màu cơ bản trắng tinh, xám đá và xanh than dễ phối đồ đi làm lẫn dạo phố.",
						"collection_slug": "cotton-220gsm-bach-dang",
						"category_slug":   "ao-thun",
						"image_url":       "/banners/fabric-closeup.jpg",
						"limit":           4,
					},
				},
				map[string]any{
					"id": "bd-banner", "type": "banner", "visible": true,
					"props": map[string]any{
						"title":     "Kiểm định 3 bước tại nhà máy Hải Phòng",
						"subtitle":  "Mỗi lô vải mộc đều trải qua 3 chu kỳ giặt thử đo co rút, soi mật độ mũi kim và kiểm bền màu trước khi đóng gói.",
						"image_url": "/banners/vietnam-workshop.jpg",
						"cta_label": "Tìm hiểu xưởng dệt",
						"cta_href":  "/gioi-thieu",
					},
				},
				map[string]any{
					"id": "bd-story", "type": "text", "visible": true,
					"props": map[string]any{
						"title": "Triết lý dệt may bền bỉ từ năm 2019",
						"body":  "Xưởng Dệt Bạch Đằng đặt tại Hải Phòng, chủ động từ khâu dệt kim tròn đến cắt rập và hoàn thiện.",
						"align": "left",
					},
				},
				map[string]any{
					"id": "bd-policies", "type": "policies", "visible": true,
					"props": map[string]any{
						"title": "Cam kết bán hàng và hậu mãi",
						"items": []any{
							map[string]any{"heading": "Đổi trả miễn phí 45 ngày", "detail": "Hỗ trợ đổi size hai chiều tận nơi."},
							map[string]any{"heading": "Giao hàng từ kho Hải Phòng", "detail": "Giao tiêu chuẩn liên tỉnh 1 đến 3 ngày."},
						},
					},
				},
			},
		},
		"sai-gon": {
			"theme": map[string]any{
				"bg_color":          "#14181C",
				"surface_color":     "#1E242B",
				"text_color":        "#F4F6F8",
				"accent_color":      "#D97706",
				"accent_text_color": "#111418",
				"font_family":       "be-vietnam-pro",
				"logo_url":          "/banners/campaign-running.jpg",
				"banner_url":        "/banners/campaign-running.jpg",
				"brand_name":        "Kho Basic Sài Gòn",
				"tagline":           "Đồ chạy bộ Dry-Fit siêu nhẹ và trang phục vận động cho khí hậu miền Nam",
			},
			"sections": []any{
				map[string]any{
					"id": "sg-hero", "type": "hero", "visible": true,
					"props": map[string]any{
						"title":     "Trang phục vận động nhẹ mát Sài Gòn",
						"subtitle":  "Sợi Dry-Fit 145gsm khô nhanh sau 45 phút phơi gió cùng quần short chạy bộ 5 inch siêu nhẹ.",
						"image_url": "/banners/campaign-running.jpg",
						"cta_label": "Khám phá đồ thể thao",
						"cta_href":  "/san-pham",
					},
				},
				map[string]any{
					"id": "sg-featured", "type": "featured_products", "visible": true,
					"props": map[string]any{
						"title":       "Thiết kế bán chạy cho tập luyện",
						"subtitle":    "Từ đường chạy 5K buổi sáng tới bộ đồ basic thoáng nhẹ mặc suốt ngày làm việc tại TP.HCM.",
						"product_ids": []string{"prod-at-dryfit", "prod-short-run"},
						"limit":       4,
					},
				},
				map[string]any{
					"id": "sg-collection", "type": "collection", "visible": true,
					"props": map[string]any{
						"title":           "Quần short chạy bộ và mặc hằng ngày",
						"subtitle":        "Cạp giấu dây rút chắc chắn, túi khóa kéo sau lưng.",
						"collection_slug": "chay-bo-sai-gon",
						"category_slug":   "quan-short",
						"image_url":       "/banners/campaign-green-fiber.jpg",
						"limit":           4,
					},
				},
				map[string]any{
					"id": "sg-vouchers", "type": "voucher_strip", "visible": true,
					"props": map[string]any{
						"title":    "Mã ưu đãi Kho Basic Sài Gòn",
						"subtitle": "Nhận ngay mã giảm trực tiếp trên giỏ hàng.",
						"limit":    4,
					},
				},
				map[string]any{
					"id": "sg-story", "type": "text", "visible": true,
					"props": map[string]any{
						"title": "Chuẩn đường may 4 kim 6 chỉ tại Tân Thuận",
						"body":  "Ra đời năm 2020 tại Thành phố Hồ Chí Minh, Kho Basic Sài Gòn tối ưu từng chi tiết nhỏ.",
						"align": "left",
					},
				},
				map[string]any{
					"id": "sg-policies", "type": "policies", "visible": true,
					"props": map[string]any{
						"title": "Chính sách giao vận và đổi cỡ",
						"items": []any{
							map[string]any{"heading": "Giao nhanh 2 đến 4 giờ TP.HCM", "detail": "Áp dụng cho đơn đặt trước 17 giờ."},
						},
					},
				},
			},
		},
		"da-nang": {
			"theme": map[string]any{
				"bg_color":          "#EEF2EA",
				"surface_color":     "#F9FBF6",
				"text_color":        "#1F2A22",
				"accent_color":      "#3F6B4F",
				"accent_text_color": "#FFFFFF",
				"font_family":       "ibm-plex-sans",
				"logo_url":          "/banners/campaign-green-fiber.jpg",
				"banner_url":        "/banners/campaign-office.jpg",
				"brand_name":        "Sợi Tự Nhiên Đà Nẵng",
				"tagline":           "Áo polo pique, quần short linen và đồ lót sợi tre thoáng mát cho miền Trung nắng gió",
			},
			"sections": []any{
				map[string]any{
					"id": "dn-hero", "type": "hero", "visible": true,
					"props": map[string]any{
						"eyebrow":   "Sợi thực vật từ Đà Nẵng",
						"title":     "Lanh, tre và cotton pique cho ngày nắng",
						"subtitle":  "Chất liệu sợi thực vật thoáng khí như lanh pha bông và sợi tre thiên nhiên.",
						"image_url": "/banners/campaign-office.jpg",
						"cta_label": "Xem đồ sợi tự nhiên",
						"cta_href":  "/san-pham",
					},
				},
				map[string]any{
					"id": "dn-collection", "type": "collection", "visible": true,
					"props": map[string]any{
						"title":           "Polo Pique và Short Linen",
						"subtitle":        "Thoáng nhẹ cho công sở mùa nóng và du lịch cuối tuần.",
						"collection_slug": "cong-so-soi-tu-nhien",
						"category_slug":   "ao-polo",
						"image_url":       "/banners/campaign-green-fiber.jpg",
						"limit":           3,
					},
				},
				map[string]any{
					"id": "dn-featured", "type": "featured_products", "visible": true,
					"props": map[string]any{
						"title":       "Được chọn nhiều tại xưởng Hòa Khánh",
						"subtitle":    "Polo cotton pique chải kỹ, short linen pha cotton và combo đồ lót sợi tre.",
						"product_ids": []string{"prod-polo-kem", "prod-short-linen"},
						"limit":       4,
					},
				},
				map[string]any{
					"id": "dn-text", "type": "text", "visible": true,
					"props": map[string]any{
						"title": "Giặt mềm enzyme, chạm dịu từ lần mặc đầu",
						"body":  "Sợi Tự Nhiên Đà Nẵng làm việc với Xưởng Sợi Tự Nhiên Miền Trung tại Khu công nghiệp Hòa Khánh.",
						"align": "center",
					},
				},
				map[string]any{
					"id": "dn-vouchers", "type": "voucher_strip", "visible": true,
					"props": map[string]any{
						"title":    "Ưu đãi của Sợi Tự Nhiên",
						"subtitle": "Lưu mã của gian hàng và áp dụng ngay ở bước thanh toán.",
						"limit":    3,
					},
				},
				map[string]any{
					"id": "dn-policies", "type": "policies", "visible": true,
					"props": map[string]any{
						"title": "Mua sắm an tâm",
						"items": []any{
							map[string]any{"heading": "Đổi trả miễn phí 45 ngày", "detail": "Đổi size hai chiều tận nơi."},
						},
					},
				},
			},
		},
		"ha-noi": {
			"theme": map[string]any{
				"bg_color":          "#2B1A1E",
				"surface_color":     "#3A2328",
				"text_color":        "#F7EDE8",
				"accent_color":      "#E9B949",
				"accent_text_color": "#2B1A1E",
				"font_family":       "plus-jakarta-sans",
				"logo_url":          "/atmosphere/cotton-twill-slate.jpg",
				"banner_url":        "/atmosphere/jacquard-loom-poster.jpg",
				"brand_name":        "Dệt Kim Hà Nội",
				"tagline":           "Áo polo dệt kim, jogger nỉ chân cua và tất cotton gân theo tay nghề dệt kim miền Bắc",
			},
			"sections": []any{
				map[string]any{
					"id": "hn-banner", "type": "banner", "visible": true,
					"props": map[string]any{
						"title":     "Dệt kim và nỉ chân cua 320gsm",
						"subtitle":  "Đứng phom cho tiết trời mát và môi trường điều hòa.",
						"image_url": "/atmosphere/jacquard-loom-poster.jpg",
						"cta_label": "Xem bộ sưu tập",
						"cta_href":  "/san-pham",
					},
				},
				map[string]any{
					"id": "hn-featured", "type": "featured_products", "visible": true,
					"props": map[string]any{
						"title":       "Hàng dệt kim tiêu biểu",
						"subtitle":    "Cổ polo đứng dáng, bo gấu jogger dày dặn.",
						"product_ids": []string{"prod-polo-detkim", "prod-jogger-terry"},
						"limit":       4,
					},
				},
				map[string]any{
					"id": "hn-vouchers", "type": "voucher_strip", "visible": true,
					"props": map[string]any{
						"title":    "Mã giảm giá Dệt Kim Hà Nội",
						"subtitle": "Áp dụng cho đơn đặt trực tiếp tại gian hàng.",
						"limit":    2,
					},
				},
				map[string]any{
					"id": "hn-collection", "type": "collection", "visible": true,
					"props": map[string]any{
						"title":           "Tất cotton gân và thể thao",
						"subtitle":        "Nối mũi liền mạch, áp dụng chính sách bù lẻ chiếc thất lạc.",
						"collection_slug": "combo-tat-cotton-ha-noi",
						"category_slug":   "tat-phu-kien",
						"image_url":       "/atmosphere/cotton-twill-slate.jpg",
						"limit":           2,
					},
				},
				map[string]any{
					"id": "hn-text", "type": "text", "visible": true,
					"props": map[string]any{
						"title": "Tay nghề dệt kim từ năm 2018",
						"body":  "Dệt Kim Hà Nội kế thừa tay nghề dệt kim truyền thống miền Bắc tại Xưởng Dệt kim truyền thống Minh Khai.",
						"align": "left",
					},
				},
				map[string]any{
					"id": "hn-policies", "type": "policies", "visible": true,
					"props": map[string]any{
						"title": "Chính sách của gian hàng",
						"items": []any{
							map[string]any{"heading": "Đổi trả miễn phí 45 ngày", "detail": "Đổi size hai chiều và bù lẻ chiếc tất thất lạc."},
						},
					},
				},
			},
		},
	}

	for sub, cfgMap := range seededSites {
		t.Run(sub, func(t *testing.T) {
			rawV1 := mustMarshal(t, cfgMap)
			v1, _, err := ValidateAndCanonicalizeConfig(rawV1)
			if err != nil {
				t.Fatalf("seeded site %s failed v1 validation: %v", sub, err)
			}

			v2, canonicalV2, err := ConvertV1ToV2(rawV1)
			if err != nil {
				t.Fatalf("ConvertV1ToV2(%s) failed: %v", sub, err)
			}
			if v2.SchemaVersion != 2 || v2.TemplateID != "legacy-v1" {
				t.Fatalf("unexpected v2 header: version=%d template_id=%q", v2.SchemaVersion, v2.TemplateID)
			}
			if v2.Theme.BrandName != v1.Theme.BrandName ||
				v2.Theme.BGColor != v1.Theme.BGColor ||
				v2.Theme.AccentColor != v1.Theme.AccentColor ||
				v2.Theme.FontFamily != v1.Theme.FontFamily ||
				v2.Theme.LogoURL != v1.Theme.LogoURL {
				t.Fatalf("theme fields lost during conversion for %s", sub)
			}
			if len(v2.Sections) != len(v1.Sections) {
				t.Fatalf("section count mismatch for %s: got %d want %d", sub, len(v2.Sections), len(v1.Sections))
			}
			for i := range v1.Sections {
				if v2.Sections[i].ID != v1.Sections[i].ID || v2.Sections[i].Type != v1.Sections[i].Type {
					t.Fatalf("section[%d] mismatch for %s", i, sub)
				}
			}

			// Re-validating canonicalV2 through ValidateAndCanonicalizeConfigV2 and ConvertV1ToV2 is idempotent.
			if _, canonicalAgain, err := ConvertV1ToV2(canonicalV2); err != nil || !bytes.Equal(canonicalV2, canonicalAgain) {
				t.Fatalf("ConvertV1ToV2 not idempotent for %s: err=%v", sub, err)
			}
		})
	}
}

func TestApplyTemplateToConfigPreservesBrandIdentity(t *testing.T) {
	tmplJSON, err := BuiltInTemplates()[1].ConfigJSON() // editorial
	if err != nil {
		t.Fatalf("editorial template json: %v", err)
	}

	// Case 1: existing v1 config preserves brand_name, tagline, logo_url.
	existingV1 := mustMarshal(t, validBaseConfig())
	merged, err := ApplyTemplateToConfig(tmplJSON, existingV1, "Fallback Shop", "Fallback desc")
	if err != nil {
		t.Fatalf("ApplyTemplateToConfig with v1 existing: %v", err)
	}
	v2, _, err := ValidateAndCanonicalizeConfigV2(merged)
	if err != nil {
		t.Fatalf("merged config invalid: %v", err)
	}
	if v2.TemplateID != "editorial" || v2.Layout.Archetype != "editorial" {
		t.Fatalf("expected editorial layout applied, got template=%q archetype=%q", v2.TemplateID, v2.Layout.Archetype)
	}
	if v2.Theme.BrandName != "Xưởng Dệt Bạch Đằng" {
		t.Fatalf("expected existing brand_name preserved, got %q", v2.Theme.BrandName)
	}
	if v2.Theme.Tagline != "Áo thun Cotton Compact 220gsm may trực tiếp tại xưởng" {
		t.Fatalf("expected existing tagline preserved, got %q", v2.Theme.Tagline)
	}
	if v2.Theme.LogoURL != "/banners/vietnam-workshop.jpg" {
		t.Fatalf("expected existing logo_url preserved, got %q", v2.Theme.LogoURL)
	}

	// Case 2: first-time site uses shopName and shopDescription.
	mergedNew, err := ApplyTemplateToConfig(tmplJSON, nil, "Xưởng Mới Cần Thơ", "Chuyên vải cotton thoáng mát")
	if err != nil {
		t.Fatalf("ApplyTemplateToConfig without existing: %v", err)
	}
	v2New, _, err := ValidateAndCanonicalizeConfigV2(mergedNew)
	if err != nil {
		t.Fatalf("merged new config invalid: %v", err)
	}
	if v2New.Theme.BrandName != "Xưởng Mới Cần Thơ" || v2New.Theme.Tagline != "Chuyên vải cotton thoáng mát" {
		t.Fatalf("expected shop metadata applied, got brand=%q tagline=%q", v2New.Theme.BrandName, v2New.Theme.Tagline)
	}
}
