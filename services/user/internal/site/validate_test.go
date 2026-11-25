package site

import (
	"encoding/json"
	"strings"
	"testing"
)

func validBaseConfig() map[string]any {
	return map[string]any{
		"theme": map[string]any{
			"bg_color":          "#F8F6F1",
			"surface_color":     "#FFFFFF",
			"text_color":        "#182026",
			"accent_color":      "#1D4ED8",
			"accent_text_color": "#FFFFFF",
			"font_family":       "work-sans",
			"logo_url":          "/banners/vietnam-workshop.jpg",
			"banner_url":        "https://cdn.example.com/banners/hero.jpg",
			"brand_name":        "Xưởng Dệt Bạch Đằng",
			"tagline":           "Áo thun Cotton Compact 220gsm may trực tiếp tại xưởng",
		},
		"sections": []any{
			map[string]any{
				"id":      "hero-1",
				"type":    "hero",
				"visible": true,
				"props": map[string]any{
					"eyebrow":   "Bộ sưu tập mới",
					"title":     "Cotton Compact 220gsm",
					"subtitle":  "Thoáng mát và giữ phom sau nhiều lần giặt.",
					"image_url": "/banners/campaign-everyday.jpg",
					"cta_label": "Xem sản phẩm",
					"cta_href":  "/san-pham",
				},
			},
			map[string]any{
				"id":      "featured-1",
				"type":    "featured_products",
				"visible": true,
				"props": map[string]any{
					"title":       "Sản phẩm nổi bật",
					"subtitle":    "Bán chạy nhất tại xưởng",
					"product_ids": []string{"prod-1", "prod-2"},
					"limit":       8,
				},
			},
			map[string]any{
				"id":      "collection-1",
				"type":    "collection",
				"visible": true,
				"props": map[string]any{
					"title":           "Áo thun hàng ngày",
					"subtitle":        "Dễ phối đồ mọi mùa",
					"collection_slug": "everyday-tee",
					"category_slug":   "ao-thun",
					"image_url":       "/banners/campaign-everyday.jpg",
					"limit":           6,
				},
			},
			map[string]any{
				"id":      "text-1",
				"type":    "text",
				"visible": true,
				"props": map[string]any{
					"title": "Câu chuyện thương hiệu",
					"body":  "Mỗi đường kim mũi chỉ được kiểm tra trực tiếp tại xưởng Hải Phòng.\nCam kết không pha sợi kém chất lượng.",
					"align": "left",
				},
			},
			map[string]any{
				"id":      "banner-1",
				"type":    "banner",
				"visible": true,
				"props": map[string]any{
					"title":     "Ưu đãi xưởng tháng này",
					"subtitle":  "Miễn phí đổi size trong 14 ngày",
					"image_url": "https://cdn.example.com/banner-promo.jpg",
					"cta_label": "Khám phá",
					"cta_href":  "/san-pham?sort=price_asc",
				},
			},
			map[string]any{
				"id":      "voucher-1",
				"type":    "voucher_strip",
				"visible": true,
				"props": map[string]any{
					"title":    "Mã giảm giá riêng của gian hàng",
					"subtitle": "Áp dụng cho mọi đơn hàng trên site riêng",
					"limit":    4,
				},
			},
			map[string]any{
				"id":      "policies-1",
				"type":    "policies",
				"visible": true,
				"props": map[string]any{
					"title": "Chính sách cửa hàng",
					"items": []any{
						map[string]any{
							"heading": "Đổi trả 14 ngày",
							"detail":  "Hỗ trợ đổi size miễn phí nếu còn nguyên tem mác.",
						},
					},
				},
			},
		},
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestValidateSubdomain(t *testing.T) {
	valid := []struct {
		in   string
		want string
	}{
		{"bach-dang", "bach-dang"},
		{"  Sai-Gon  ", "sai-gon"},
		{"abc", "abc"},
		{"shop123", "shop123"},
		{strings.Repeat("a", 40), strings.Repeat("a", 40)},
		{"test-shop-1", "test-shop-1"},
	}
	for _, tc := range valid {
		got, err := ValidateSubdomain(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ValidateSubdomain(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
	}

	invalid := []string{
		"",
		"ab",
		strings.Repeat("a", 41),
		"-abc",
		"abc-",
		"abc_def",
		"abc.def",
		"abc def",
		"www",
		"WWW",
		"api",
		"admin",
		"seller",
		"app",
		"mail",
		"static",
		"cdn",
		"checkout",
		"pay",
		"shop",
		"storefront",
		"s",
		"localhost",
		"nexus",
		"nexusmart",
		"nexuspay",
		"vietcombank",
		"Vietcombank",
		"techcombank",
		"bidv",
		"agribank",
		"momo",
		"zalopay",
		"vnpay",
		"shopee",
		"lazada",
		"tiki",
		"gov",
		"chinhphu",
		"vietcombank-uudai",
		"nexusmart-official",
		"momo-khuyenmai",
		"vnpay-verify",
		"gov-thongbao",
		"xn--punycode",
	}
	for _, in := range invalid {
		if got, err := ValidateSubdomain(in); err == nil {
			t.Errorf("ValidateSubdomain(%q) unexpectedly succeeded with %q", in, got)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Bach-Dang.NexusMart.Localhost:3300", "bach-dang.nexusmart.localhost", false},
		{"sai-gon.nexusmart.localhost", "sai-gon.nexusmart.localhost", false},
		{"sai-gon.nexusmart.localhost.", "sai-gon.nexusmart.localhost", false},
		{"", "", true},
		{"bad host", "", true},
		{"evil.com/path", "", true},
		{"host:notaport", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeHost(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("NormalizeHost(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateCustomHostname(t *testing.T) {
	maxLabel := strings.Repeat("a", 63)
	// 63 + 1 + 63 + 1 + 63 + 1 + 58 + 1 + 2 = 253 chars
	maxHost253 := maxLabel + "." + maxLabel + "." + maxLabel + "." + strings.Repeat("b", 58) + ".vn"
	if len(maxHost253) != 253 {
		t.Fatalf("test setup bug: expected 253 chars, got %d", len(maxHost253))
	}
	tooLong254 := maxLabel + "." + maxLabel + "." + maxLabel + "." + strings.Repeat("b", 59) + ".vn"
	if len(tooLong254) != 254 {
		t.Fatalf("test setup bug: expected 254 chars, got %d", len(tooLong254))
	}

	validCases := []struct {
		name          string
		raw           string
		rootDomain    string
		allowMockTest bool
		want          string
	}{
		{
			name:       "standard ascii",
			raw:        "shop.example.com",
			rootDomain: "nexusmart.vn",
			want:       "shop.example.com",
		},
		{
			name:       "uppercase and outer whitespace normalized",
			raw:        "  Shop-01.MyBrand.VN  ",
			rootDomain: "nexusmart.vn",
			want:       "shop-01.mybrand.vn",
		},
		{
			name:       "multi-level subdomain",
			raw:        "a.b.c.shop-1.example.co.uk",
			rootDomain: "nexusmart.vn",
			want:       "a.b.c.shop-1.example.co.uk",
		},
		{
			name:       "vietnamese IDN punycode",
			raw:        "cửa-hàng.vn",
			rootDomain: "nexusmart.vn",
			want:       "xn--ca-hng-lta4562d.vn",
		},
		{
			name:       "uppercase vietnamese IDN punycode",
			raw:        "CỬA-HÀNG.VN",
			rootDomain: "nexusmart.vn",
			want:       "xn--ca-hng-lta4562d.vn",
		},
		{
			name:       "german IDN punycode",
			raw:        "bücher.example.de",
			rootDomain: "nexusmart.vn",
			want:       "xn--bcher-kva.example.de",
		},
		{
			name:       "unrelated domain containing root substring",
			raw:        "notnexusmart.vn",
			rootDomain: "nexusmart.vn",
			want:       "notnexusmart.vn",
		},
		{
			name:       "unrelated domain with root as prefix label",
			raw:        "nexusmart.vn.other.com",
			rootDomain: "nexusmart.vn",
			want:       "nexusmart.vn.other.com",
		},
		{
			name:          "mock verified test domain allowed in mock mode",
			raw:           "hanoi-knit.verified.test",
			rootDomain:    "nexusmart.localhost",
			allowMockTest: true,
			want:          "hanoi-knit.verified.test",
		},
		{
			name:          "mock pending test domain allowed in mock mode",
			raw:           "danang.pending.test",
			rootDomain:    "nexusmart.localhost",
			allowMockTest: true,
			want:          "danang.pending.test",
		},
		{
			name:          "mock fail test domain allowed in mock mode",
			raw:           "shop.fail.test",
			rootDomain:    "nexusmart.localhost",
			allowMockTest: true,
			want:          "shop.fail.test",
		},
		{
			name:       "exact 253 chars and 63 char labels",
			raw:        maxHost253,
			rootDomain: "nexusmart.vn",
			want:       maxHost253,
		},
		{
			name:       "tld ending with test substring is allowed",
			raw:        "shop.mytest",
			rootDomain: "nexusmart.vn",
			want:       "shop.mytest",
		},
		{
			name:       "numeric label allowed when tld is non-numeric",
			raw:        "123.example.com",
			rootDomain: "nexusmart.vn",
			want:       "123.example.com",
		},
	}

	for _, tc := range validCases {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			got, err := ValidateCustomHostname(tc.raw, tc.rootDomain, tc.allowMockTest)
			if err != nil {
				t.Fatalf("ValidateCustomHostname(%q, %q, %v) unexpected error: %v", tc.raw, tc.rootDomain, tc.allowMockTest, err)
			}
			if got != tc.want {
				t.Errorf("ValidateCustomHostname(%q, %q, %v) = %q, want %q", tc.raw, tc.rootDomain, tc.allowMockTest, got, tc.want)
			}
		})
	}

	invalidCases := []struct {
		name          string
		raw           string
		rootDomain    string
		allowMockTest bool
	}{
		{name: "empty", raw: "", rootDomain: "nexusmart.vn"},
		{name: "only whitespace", raw: "   \t  ", rootDomain: "nexusmart.vn"},
		{name: "single label", raw: "example", rootDomain: "nexusmart.vn"},
		{name: "too long 254 chars", raw: tooLong254, rootDomain: "nexusmart.vn"},
		{name: "label too long 64 chars", raw: strings.Repeat("a", 64) + ".com", rootDomain: "nexusmart.vn"},
		{name: "leading hyphen in label", raw: "-shop.example.com", rootDomain: "nexusmart.vn"},
		{name: "trailing hyphen in label", raw: "shop-.example.com", rootDomain: "nexusmart.vn"},
		{name: "leading hyphen in second label", raw: "shop.-example.com", rootDomain: "nexusmart.vn"},
		{name: "leading dot", raw: ".example.com", rootDomain: "nexusmart.vn"},
		{name: "trailing dot", raw: "example.com.", rootDomain: "nexusmart.vn"},
		{name: "consecutive dots", raw: "shop..example.com", rootDomain: "nexusmart.vn"},
		{name: "unicode trailing fullwidth dot", raw: "example.com。", rootDomain: "nexusmart.vn"},
		{name: "unicode consecutive dots", raw: "shop。。example.com", rootDomain: "nexusmart.vn"},
		{name: "ipv4 address", raw: "192.168.1.1", rootDomain: "nexusmart.vn"},
		{name: "ipv4 loopback", raw: "127.0.0.1", rootDomain: "nexusmart.vn"},
		{name: "short numeric ipv4", raw: "127.1", rootDomain: "nexusmart.vn"},
		{name: "malformed ipv4 numeric tld", raw: "999.999.999.999", rootDomain: "nexusmart.vn"},
		{name: "all numeric tld", raw: "shop.123", rootDomain: "nexusmart.vn"},
		{name: "ipv6 address", raw: "::1", rootDomain: "nexusmart.vn"},
		{name: "bracketed ipv6", raw: "[::1]", rootDomain: "nexusmart.vn"},
		{name: "with port", raw: "shop.example.com:443", rootDomain: "nexusmart.vn"},
		{name: "wildcard", raw: "*.example.com", rootDomain: "nexusmart.vn"},
		{name: "scheme prefix", raw: "https://shop.example.com", rootDomain: "nexusmart.vn"},
		{name: "path suffix", raw: "shop.example.com/products", rootDomain: "nexusmart.vn"},
		{name: "backslash", raw: `shop.example.com\path`, rootDomain: "nexusmart.vn"},
		{name: "userinfo at sign", raw: "user@shop.example.com", rootDomain: "nexusmart.vn"},
		{name: "query string", raw: "shop.example.com?a=1", rootDomain: "nexusmart.vn"},
		{name: "fragment", raw: "shop.example.com#top", rootDomain: "nexusmart.vn"},
		{name: "percent encoding", raw: "shop%20.example.com", rootDomain: "nexusmart.vn"},
		{name: "whitespace inside", raw: "shop name.example.com", rootDomain: "nexusmart.vn"},
		{name: "tab inside", raw: "shop\t.example.com", rootDomain: "nexusmart.vn"},
		{name: "control char null", raw: "shop\x00.example.com", rootDomain: "nexusmart.vn"},
		{name: "control char bell", raw: "shop\x07.example.com", rootDomain: "nexusmart.vn"},
		{name: "underscore disallowed", raw: "my_shop.example.com", rootDomain: "nexusmart.vn"},
		{name: "exact root domain", raw: "nexusmart.vn", rootDomain: "nexusmart.vn"},
		{name: "exact root domain uppercase and padded root", raw: "NEXUSMART.VN", rootDomain: " .NexusMart.VN. "},
		{name: "subdomain of root domain", raw: "shop.nexusmart.vn", rootDomain: "nexusmart.vn"},
		{name: "deep subdomain of root domain", raw: "a.b.nexusmart.vn", rootDomain: "nexusmart.vn"},
		{name: "localhost", raw: "localhost", rootDomain: "nexusmart.vn"},
		{name: "localhost with allowMockTest", raw: "localhost", rootDomain: "nexusmart.vn", allowMockTest: true},
		{name: "subdomain of localhost", raw: "a.localhost", rootDomain: "nexusmart.vn"},
		{name: "subdomain of localhost even in mock mode", raw: "a.localhost", rootDomain: "nexusmart.vn", allowMockTest: true},
		{name: "dot local", raw: "a.local", rootDomain: "nexusmart.vn"},
		{name: "dot local even in mock mode", raw: "a.local", rootDomain: "nexusmart.vn", allowMockTest: true},
		{name: "dot internal", raw: "a.internal", rootDomain: "nexusmart.vn"},
		{name: "dot internal even in mock mode", raw: "a.internal", rootDomain: "nexusmart.vn", allowMockTest: true},
		{name: "dot test disallowed in live mode", raw: "a.test", rootDomain: "nexusmart.vn", allowMockTest: false},
		{name: "verified test disallowed in live mode", raw: "hanoi-knit.verified.test", rootDomain: "nexusmart.vn", allowMockTest: false},
	}

	for _, tc := range invalidCases {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			got, err := ValidateCustomHostname(tc.raw, tc.rootDomain, tc.allowMockTest)
			if err == nil {
				t.Fatalf("ValidateCustomHostname(%q, %q, %v) unexpectedly succeeded with %q", tc.raw, tc.rootDomain, tc.allowMockTest, got)
			}
			if !strings.HasPrefix(err.Error(), "invalid_hostname: ") {
				t.Errorf("ValidateCustomHostname(%q) error %q missing 'invalid_hostname: ' prefix", tc.raw, err.Error())
			}
		})
	}
}

func TestValidateSafeURL(t *testing.T) {
	okURLs := []string{
		"",
		"/banners/vietnam-workshop.jpg",
		"/san-pham?category=ao-thun&sort=newest",
		"https://cdn.example.com/a/b.png",
		"https://example.com:8443/path?x=1#top",
	}
	for _, u := range okURLs {
		if _, err := ValidateSafeURL(u); err != nil {
			t.Errorf("ValidateSafeURL(%q) unexpected error: %v", u, err)
		}
	}

	badURLs := []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"  javascript:alert(1)",
		"java\tscript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"http://example.com/logo.png",
		"//evil.com/payload.js",
		"/\\evil.com",
		"/path\nX-Inject: 1",
		"https://",
		"https://user:pass@evil.com/a.jpg",
		"relative/without/leading/slash",
		"https://example.com/" + strings.Repeat("a", 500),
	}
	for _, u := range badURLs {
		if _, err := ValidateSafeURL(u); err == nil {
			t.Errorf("ValidateSafeURL(%q) expected error, got nil", u)
		}
	}
}

func TestValidateConfigHappyPathAndDarkPalette(t *testing.T) {
	cfg, canonical, err := ValidateAndCanonicalizeConfig(mustMarshal(t, validBaseConfig()))
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if len(cfg.Sections) != 7 {
		t.Fatalf("expected 7 sections, got %d", len(cfg.Sections))
	}
	if len(canonical) == 0 {
		t.Fatalf("expected non-empty canonical json")
	}

	// Verify dark palette from seed spec passes WCAG >= 4.5.
	dark := validBaseConfig()
	dark["theme"] = map[string]any{
		"bg_color":          "#14181C",
		"surface_color":     "#1E242B",
		"text_color":        "#F4F6F8",
		"accent_color":      "#D97706",
		"accent_text_color": "#111418",
		"font_family":       "be-vietnam-pro",
		"logo_url":          "/banners/campaign-running.jpg",
		"banner_url":        "/banners/campaign-running.jpg",
		"brand_name":        "Kho Basic Sài Gòn",
		"tagline":           "Đồ thể thao chạy bộ thiết kế tối giản",
	}
	dark["sections"] = []any{}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, dark)); err != nil {
		t.Fatalf("expected dark theme config to pass, got %v", err)
	}

	// Harmless angle brackets are accepted as plain text, while <script> markup is rejected per ADR-0001.
	withPlainAngles := validBaseConfig()
	withPlainAngles["sections"] = []any{
		map[string]any{
			"id":      "txt-1",
			"type":    "text",
			"visible": true,
			"props": map[string]any{
				"title": "Cotton > 95% & độ co < 2%",
				"body":  "Kiểm định 3 bước: độ co < 2% và mật độ > 220gsm.",
				"align": "center",
			},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, withPlainAngles)); err != nil {
		t.Fatalf("harmless angle brackets should be accepted as plain text, got %v", err)
	}

	withScriptText := validBaseConfig()
	withScriptText["sections"] = []any{
		map[string]any{
			"id":      "txt-1",
			"type":    "text",
			"visible": true,
			"props": map[string]any{
				"title": "Test <script>alert(1)</script>",
				"body":  "Plain text body <script>alert('xss')</script>",
				"align": "center",
			},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, withScriptText)); err == nil {
		t.Fatalf("expected <script> markup in text props to be rejected")
	}
}

func TestValidateConfigRejectsWCAGLowContrast(t *testing.T) {
	cases := []struct {
		name  string
		field string
		val   string
	}{
		{"bg vs text low contrast", "bg_color", "#222222"},
		{"surface vs text low contrast", "surface_color", "#252525"},
		{"accent vs accent_text low contrast", "accent_color", "#E5E7EB"},
		{"borderline 4.48 contrast", "text_color", "#777777"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := validBaseConfig()
			theme := raw["theme"].(map[string]any)
			theme[tc.field] = tc.val
			if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, raw)); err == nil || !strings.Contains(err.Error(), "LOW_CONTRAST") {
				t.Fatalf("expected LOW_CONTRAST error, got %v", err)
			}
		})
	}
}

func TestValidateConfigRejectsUnknownFieldsAndTrailingTokens(t *testing.T) {
	// 1. Unknown root field
	rootExtra := validBaseConfig()
	rootExtra["custom_css"] = "body{color:red}"
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, rootExtra)); err == nil {
		t.Fatalf("expected error for unknown root field")
	}

	// 2. Unknown theme field
	themeExtra := validBaseConfig()
	themeExtra["theme"].(map[string]any)["raw_html"] = "<style></style>"
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, themeExtra)); err == nil {
		t.Fatalf("expected error for unknown theme field")
	}

	// 3. Unknown section field
	secExtra := validBaseConfig()
	secExtra["sections"] = []any{
		map[string]any{
			"id":      "s1",
			"type":    "text",
			"visible": true,
			"html":    "<script>alert(1)</script>",
			"props":   map[string]any{"title": "T", "body": "B"},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, secExtra)); err == nil {
		t.Fatalf("expected error for unknown section field")
	}

	// 4. Unknown props field
	propsExtra := validBaseConfig()
	propsExtra["sections"] = []any{
		map[string]any{
			"id":      "s1",
			"type":    "hero",
			"visible": true,
			"props": map[string]any{
				"title":   "Valid",
				"onclick": "alert(1)",
			},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, propsExtra)); err == nil {
		t.Fatalf("expected error for unknown props field")
	}

	// 5. Unknown policy item field
	polExtra := validBaseConfig()
	polExtra["sections"] = []any{
		map[string]any{
			"id":      "p1",
			"type":    "policies",
			"visible": true,
			"props": map[string]any{
				"title": "Policies",
				"items": []any{
					map[string]any{"heading": "H", "detail": "D", "extra": 1},
				},
			},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, polExtra)); err == nil {
		t.Fatalf("expected error for unknown policy item field")
	}

	// 6. Trailing JSON token
	trailing := append(mustMarshal(t, validBaseConfig()), []byte(` {"extra":1}`)...)
	if _, _, err := ValidateAndCanonicalizeConfig(trailing); err == nil {
		t.Fatalf("expected error for trailing JSON token")
	}
}

func TestValidateConfigRejectsUnsafeURLsAndLimits(t *testing.T) {
	// javascript: URL in logo_url
	badLogo := validBaseConfig()
	badLogo["theme"].(map[string]any)["logo_url"] = "javascript:alert(1)"
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, badLogo)); err == nil {
		t.Fatalf("expected error for javascript: logo_url")
	}

	// data: URL in hero cta_href
	badCTA := validBaseConfig()
	badCTA["sections"] = []any{
		map[string]any{
			"id":      "h1",
			"type":    "hero",
			"visible": true,
			"props": map[string]any{
				"title":    "Hero",
				"cta_href": "data:text/html,<script>alert(1)</script>",
			},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, badCTA)); err == nil {
		t.Fatalf("expected error for data: cta_href")
	}

	// > 20 sections rejected
	tooMany := validBaseConfig()
	secs := make([]any, 21)
	for i := range secs {
		secs[i] = map[string]any{
			"id":      "t",
			"type":    "text",
			"visible": true,
			"props":   map[string]any{"title": "T", "body": "B"},
		}
	}
	tooMany["sections"] = secs
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, tooMany)); err == nil || !strings.Contains(err.Error(), "TOO_MANY_SECTIONS") {
		t.Fatalf("expected TOO_MANY_SECTIONS error, got %v", err)
	}

	// Disallowed font
	badFont := validBaseConfig()
	badFont["theme"].(map[string]any)["font_family"] = "comic-sans"
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, badFont)); err == nil {
		t.Fatalf("expected error for disallowed font_family")
	}

	// Disallowed section type
	badSecType := validBaseConfig()
	badSecType["sections"] = []any{
		map[string]any{
			"id":      "x1",
			"type":    "custom_html",
			"visible": true,
			"props":   map[string]any{},
		},
	}
	if _, _, err := ValidateAndCanonicalizeConfig(mustMarshal(t, badSecType)); err == nil {
		t.Fatalf("expected error for disallowed section type")
	}
}
