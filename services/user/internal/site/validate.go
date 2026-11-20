package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxConfigBytes   = 128 * 1024
	MaxSections      = 20
	MaxURLLength     = 500
	MinContrastRatio = 4.5
)

var (
	subdomainRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)
	hexColorRe  = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// reservedSubdomains lists exact reserved and brand-impersonation subdomains (lowercase).
var reservedSubdomains = map[string]struct{}{
	"www": {}, "api": {}, "admin": {}, "seller": {}, "app": {},
	"mail": {}, "smtp": {}, "imap": {}, "pop": {},
	"static": {}, "cdn": {}, "assets": {}, "media": {}, "img": {}, "images": {}, "files": {},
	"auth": {}, "login": {}, "sso": {}, "oauth": {}, "account": {}, "accounts": {},
	"checkout": {}, "cart": {}, "pay": {}, "payment": {}, "payments": {}, "billing": {},
	"shop": {}, "store": {}, "storefront": {}, "s": {},
	"status": {}, "health": {}, "docs": {}, "help": {}, "support": {}, "blog": {}, "news": {},
	"portal": {}, "dashboard": {}, "console": {}, "internal": {},
	"staging": {}, "dev": {}, "test": {}, "demo": {}, "localhost": {},
	"nexus": {}, "nexusmart": {}, "nexuspay": {},
	"vietcombank": {}, "techcombank": {}, "bidv": {}, "agribank": {},
	"momo": {}, "zalopay": {}, "vnpay": {},
	"shopee": {}, "lazada": {}, "tiki": {},
	"gov": {}, "chinhphu": {},
}

// forbiddenBrandPrefixes lists bank, government, and platform brands that cannot be impersonated
// via prefix-hyphenated subdomains (e.g. vietcombank-uudai, nexusmart-official).
var forbiddenBrandPrefixes = []string{
	"nexus",
	"nexusmart",
	"nexuspay",
	"vietcombank",
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
}

var allowedFonts = map[string]struct{}{
	"work-sans":         {},
	"be-vietnam-pro":    {},
	"inter":             {},
	"ibm-plex-sans":     {},
	"plus-jakarta-sans": {},
}

// Config is the validated, canonical site configuration stored as JSONB.
type Config struct {
	Theme    ThemeConfig `json:"theme"`
	Sections []Section   `json:"sections"`
}

// ThemeConfig holds strictly typed design tokens for a shop site.
type ThemeConfig struct {
	BGColor         string `json:"bg_color"`
	SurfaceColor    string `json:"surface_color"`
	TextColor       string `json:"text_color"`
	AccentColor     string `json:"accent_color"`
	AccentTextColor string `json:"accent_text_color"`
	FontFamily      string `json:"font_family"`
	LogoURL         string `json:"logo_url"`
	BannerURL       string `json:"banner_url"`
	BrandName       string `json:"brand_name"`
	Tagline         string `json:"tagline"`
}

// Section is a validated content block with strictly typed Props.
type Section struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Visible bool   `json:"visible"`
	Props   any    `json:"props"`
}

type HeroProps struct {
	Eyebrow  string `json:"eyebrow"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	ImageURL string `json:"image_url"`
	CTALabel string `json:"cta_label"`
	CTAHref  string `json:"cta_href"`
}

type FeaturedProductsProps struct {
	Title      string   `json:"title"`
	Subtitle   string   `json:"subtitle"`
	ProductIDs []string `json:"product_ids"`
	Limit      int      `json:"limit"`
}

type CollectionProps struct {
	Title          string `json:"title"`
	Subtitle       string `json:"subtitle"`
	CollectionSlug string `json:"collection_slug"`
	CategorySlug   string `json:"category_slug"`
	ImageURL       string `json:"image_url"`
	Limit          int    `json:"limit"`
}

type TextProps struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Align string `json:"align"`
}

type BannerProps struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	ImageURL string `json:"image_url"`
	CTALabel string `json:"cta_label"`
	CTAHref  string `json:"cta_href"`
}

type VoucherStripProps struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Limit    int    `json:"limit"`
}

type PolicyItem struct {
	Heading string `json:"heading"`
	Detail  string `json:"detail"`
}

type PoliciesProps struct {
	Title string       `json:"title"`
	Items []PolicyItem `json:"items"`
}

type rawConfig struct {
	Theme    *ThemeConfig `json:"theme"`
	Sections []rawSection `json:"sections"`
}

type rawSection struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Visible *bool           `json:"visible"`
	Props   json.RawMessage `json:"props"`
}

// ValidateSubdomain normalizes and validates a site subdomain against format, reserved, and
// brand-impersonation rules.
func ValidateSubdomain(raw string) (string, error) {
	sub := strings.ToLower(strings.TrimSpace(raw))
	if !subdomainRe.MatchString(sub) {
		return "", errors.New("INVALID_SUBDOMAIN: subdomain must be 3-40 chars of lowercase letters, digits, or hyphens, and cannot start or end with a hyphen")
	}
	if strings.HasPrefix(sub, "xn--") {
		return "", errors.New("RESERVED_SUBDOMAIN: punycode subdomains are not allowed")
	}
	if _, denied := reservedSubdomains[sub]; denied {
		return "", fmt.Errorf("RESERVED_SUBDOMAIN: subdomain %q is reserved", sub)
	}
	for _, brand := range forbiddenBrandPrefixes {
		if strings.HasPrefix(sub, brand+"-") {
			return "", fmt.Errorf("RESERVED_SUBDOMAIN: subdomain %q uses a restricted brand prefix", sub)
		}
	}
	return sub, nil
}

// NormalizeHost lowercases a request host and strips any trailing :port.
func NormalizeHost(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" {
		return "", errors.New("host is required")
	}
	if strings.ContainsAny(h, " \t\r\n/\\@?#") {
		return "", errors.New("invalid host characters")
	}
	if strings.Contains(h, ":") {
		hostPart, portPart, err := net.SplitHostPort(h)
		if err != nil {
			return "", errors.New("invalid host:port")
		}
		if portPart == "" {
			return "", errors.New("empty port")
		}
		for i := 0; i < len(portPart); i++ {
			if portPart[i] < '0' || portPart[i] > '9' {
				return "", errors.New("invalid port")
			}
		}
		h = hostPart
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", errors.New("empty host")
	}
	return h, nil
}

// ValidateSafeURL checks an optional URL/href. Empty is allowed; otherwise only https://...
// (with valid host, no userinfo) or a single-slash relative path (/...) is permitted.
func ValidateSafeURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > MaxURLLength {
		return "", fmt.Errorf("UNSAFE_URL: url exceeds %d characters", MaxURLLength)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("UNSAFE_URL: control characters are not allowed in url")
		}
		if r == '\\' {
			return "", errors.New("UNSAFE_URL: backslashes are not allowed in url")
		}
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return "", errors.New("UNSAFE_URL: whitespace is not allowed in url")
	}
	lower := strings.ToLower(s)
	for _, bad := range []string{"javascript:", "data:", "vbscript:", "http:", "file:", "blob:", "ftp:"} {
		if strings.HasPrefix(lower, bad) {
			return "", fmt.Errorf("UNSAFE_URL: disallowed scheme %q", strings.TrimSuffix(bad, ":"))
		}
	}
	if strings.HasPrefix(s, "/") {
		if strings.HasPrefix(s, "//") {
			return "", errors.New("UNSAFE_URL: protocol-relative urls are not allowed")
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
			return "", errors.New("UNSAFE_URL: invalid relative path")
		}
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", errors.New("UNSAFE_URL: only https:// urls or relative / paths are allowed")
	}
	return s, nil
}

// ContrastRatio calculates the WCAG 2.1 contrast ratio between two #RRGGBB hex colors.
func ContrastRatio(hex1, hex2 string) (float64, error) {
	l1, err := relativeLuminance(hex1)
	if err != nil {
		return 0, err
	}
	l2, err := relativeLuminance(hex2)
	if err != nil {
		return 0, err
	}
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05), nil
}

func relativeLuminance(hexColor string) (float64, error) {
	if !hexColorRe.MatchString(hexColor) {
		return 0, fmt.Errorf("INVALID_COLOR: %q must be #RRGGBB", hexColor)
	}
	r, err := strconv.ParseUint(hexColor[1:3], 16, 8)
	if err != nil {
		return 0, err
	}
	g, err := strconv.ParseUint(hexColor[3:5], 16, 8)
	if err != nil {
		return 0, err
	}
	b, err := strconv.ParseUint(hexColor[5:7], 16, 8)
	if err != nil {
		return 0, err
	}
	return 0.2126*linearizeChannel(float64(r)/255.0) +
		0.7152*linearizeChannel(float64(g)/255.0) +
		0.0722*linearizeChannel(float64(b)/255.0), nil
}

func linearizeChannel(srgb float64) float64 {
	if srgb <= 0.04045 {
		return srgb / 12.92
	}
	return math.Pow((srgb+0.055)/1.055, 2.4)
}

// ValidateAndCanonicalizeConfig strictly decodes, validates, and re-marshals a SiteConfig JSON payload.
// Unknown fields and trailing tokens are rejected at every level.
func ValidateAndCanonicalizeConfig(raw []byte) (*Config, []byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: config_json is required")
	}
	if len(trimmed) > MaxConfigBytes {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: config_json exceeds %d bytes", MaxConfigBytes)
	}
	if trimmed[0] != '{' {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: config_json must be a JSON object")
	}

	var rc rawConfig
	if err := decodeStrictJSON(trimmed, &rc); err != nil {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: %w", err)
	}
	if rc.Theme == nil {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: theme is required")
	}

	theme, err := validateTheme(*rc.Theme)
	if err != nil {
		return nil, nil, err
	}

	if len(rc.Sections) > MaxSections {
		return nil, nil, fmt.Errorf("TOO_MANY_SECTIONS: at most %d sections are allowed", MaxSections)
	}

	sections := make([]Section, 0, len(rc.Sections))
	for i, rs := range rc.Sections {
		sec, err := validateSection(i, rs)
		if err != nil {
			return nil, nil, err
		}
		sections = append(sections, sec)
	}

	cfg := &Config{
		Theme:    theme,
		Sections: sections,
	}
	canonical, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal canonical site config: %w", err)
	}
	return cfg, canonical, nil
}

func validateTheme(t ThemeConfig) (ThemeConfig, error) {
	colors := []struct {
		name string
		val  *string
	}{
		{"bg_color", &t.BGColor},
		{"surface_color", &t.SurfaceColor},
		{"text_color", &t.TextColor},
		{"accent_color", &t.AccentColor},
		{"accent_text_color", &t.AccentTextColor},
	}
	for _, c := range colors {
		*c.val = strings.TrimSpace(*c.val)
		if !hexColorRe.MatchString(*c.val) {
			return ThemeConfig{}, fmt.Errorf("INVALID_COLOR: theme.%s must be a #RRGGBB hex color", c.name)
		}
	}

	pairs := []struct {
		name string
		a, b string
	}{
		{"bg_color/text_color", t.BGColor, t.TextColor},
		{"surface_color/text_color", t.SurfaceColor, t.TextColor},
		{"accent_color/accent_text_color", t.AccentColor, t.AccentTextColor},
	}
	for _, p := range pairs {
		ratio, err := ContrastRatio(p.a, p.b)
		if err != nil {
			return ThemeConfig{}, err
		}
		if ratio < MinContrastRatio {
			return ThemeConfig{}, fmt.Errorf("LOW_CONTRAST: theme %s contrast ratio %.2f is below WCAG AA minimum %.1f", p.name, ratio, MinContrastRatio)
		}
	}

	t.FontFamily = strings.TrimSpace(t.FontFamily)
	if _, ok := allowedFonts[t.FontFamily]; !ok {
		return ThemeConfig{}, fmt.Errorf("INVALID_FONT: theme.font_family %q is not in allow-list", t.FontFamily)
	}

	var err error
	if t.LogoURL, err = ValidateSafeURL(t.LogoURL); err != nil {
		return ThemeConfig{}, fmt.Errorf("theme.logo_url: %w", err)
	}
	if t.BannerURL, err = ValidateSafeURL(t.BannerURL); err != nil {
		return ThemeConfig{}, fmt.Errorf("theme.banner_url: %w", err)
	}
	if t.BrandName, err = validateText("theme.brand_name", t.BrandName, 1, 80, false); err != nil {
		return ThemeConfig{}, err
	}
	if t.Tagline, err = validateText("theme.tagline", t.Tagline, 0, 160, true); err != nil {
		return ThemeConfig{}, err
	}
	return t, nil
}

func validateSection(idx int, rs rawSection) (Section, error) {
	prefix := fmt.Sprintf("sections[%d]", idx)
	id, err := validateText(prefix+".id", rs.ID, 1, 64, false)
	if err != nil {
		return Section{}, err
	}

	secType := strings.TrimSpace(rs.Type)
	visible := true
	if rs.Visible != nil {
		visible = *rs.Visible
	}

	rawProps := bytes.TrimSpace(rs.Props)
	if len(rawProps) == 0 || rawProps[0] != '{' {
		return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props must be a JSON object", prefix)
	}

	var props any
	switch secType {
	case "hero":
		var p HeroProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Eyebrow, err = validateText(prefix+".props.eyebrow", p.Eyebrow, 0, 60, false); err != nil {
			return Section{}, err
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 400, true); err != nil {
			return Section{}, err
		}
		if p.ImageURL, err = ValidateSafeURL(p.ImageURL); err != nil {
			return Section{}, fmt.Errorf("%s.props.image_url: %w", prefix, err)
		}
		if p.CTALabel, err = validateText(prefix+".props.cta_label", p.CTALabel, 0, 40, false); err != nil {
			return Section{}, err
		}
		if p.CTAHref, err = ValidateSafeURL(p.CTAHref); err != nil {
			return Section{}, fmt.Errorf("%s.props.cta_href: %w", prefix, err)
		}
		props = p

	case "featured_products":
		var p FeaturedProductsProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return Section{}, err
		}
		if len(p.ProductIDs) > 12 {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.product_ids cannot exceed 12 items", prefix)
		}
		cleanIDs := make([]string, 0, len(p.ProductIDs))
		for j, pid := range p.ProductIDs {
			clean, err := validateText(fmt.Sprintf("%s.props.product_ids[%d]", prefix, j), pid, 1, 64, false)
			if err != nil {
				return Section{}, err
			}
			cleanIDs = append(cleanIDs, clean)
		}
		p.ProductIDs = cleanIDs
		if p.Limit < 0 || p.Limit > 12 {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.limit must be between 0 and 12", prefix)
		}
		props = p

	case "collection":
		var p CollectionProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return Section{}, err
		}
		if p.CollectionSlug, err = validateText(prefix+".props.collection_slug", p.CollectionSlug, 0, 80, false); err != nil {
			return Section{}, err
		}
		if p.CategorySlug, err = validateText(prefix+".props.category_slug", p.CategorySlug, 0, 80, false); err != nil {
			return Section{}, err
		}
		if p.ImageURL, err = ValidateSafeURL(p.ImageURL); err != nil {
			return Section{}, fmt.Errorf("%s.props.image_url: %w", prefix, err)
		}
		if p.Limit < 0 || p.Limit > 12 {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.limit must be between 0 and 12", prefix)
		}
		props = p

	case "text":
		var p TextProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Body, err = validateText(prefix+".props.body", p.Body, 1, 2000, true); err != nil {
			return Section{}, err
		}
		p.Align = strings.TrimSpace(p.Align)
		if p.Align != "" && p.Align != "left" && p.Align != "center" {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.align must be '', 'left', or 'center'", prefix)
		}
		props = p

	case "banner":
		var p BannerProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return Section{}, err
		}
		if p.ImageURL, err = ValidateSafeURL(p.ImageURL); err != nil {
			return Section{}, fmt.Errorf("%s.props.image_url: %w", prefix, err)
		}
		if p.CTALabel, err = validateText(prefix+".props.cta_label", p.CTALabel, 0, 40, false); err != nil {
			return Section{}, err
		}
		if p.CTAHref, err = ValidateSafeURL(p.CTAHref); err != nil {
			return Section{}, fmt.Errorf("%s.props.cta_href: %w", prefix, err)
		}
		props = p

	case "voucher_strip":
		var p VoucherStripProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return Section{}, err
		}
		if p.Limit < 0 || p.Limit > 6 {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.limit must be between 0 and 6", prefix)
		}
		props = p

	case "policies":
		var p PoliciesProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return Section{}, err
		}
		if len(p.Items) < 1 || len(p.Items) > 8 {
			return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.items must have between 1 and 8 items", prefix)
		}
		for j := range p.Items {
			itemPrefix := fmt.Sprintf("%s.props.items[%d]", prefix, j)
			if p.Items[j].Heading, err = validateText(itemPrefix+".heading", p.Items[j].Heading, 1, 80, false); err != nil {
				return Section{}, err
			}
			if p.Items[j].Detail, err = validateText(itemPrefix+".detail", p.Items[j].Detail, 1, 400, true); err != nil {
				return Section{}, err
			}
		}
		props = p

	default:
		return Section{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.type %q is not supported", prefix, secType)
	}

	return Section{
		ID:      id,
		Type:    secType,
		Visible: visible,
		Props:   props,
	}, nil
}

func validateText(field, raw string, minRunes, maxRunes int, allowNewlines bool) (string, error) {
	for _, r := range raw {
		if r == 0 || r == 0x7f {
			return "", fmt.Errorf("INVALID_SITE_CONFIG: %s contains invalid control character", field)
		}
		if r < 0x20 {
			if allowNewlines && (r == '\n' || r == '\r' || r == '\t') {
				continue
			}
			return "", fmt.Errorf("INVALID_SITE_CONFIG: %s contains invalid control character", field)
		}
	}
	s := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(s)
	if n < minRunes {
		return "", fmt.Errorf("INVALID_SITE_CONFIG: %s must be at least %d characters", field, minRunes)
	}
	if n > maxRunes {
		return "", fmt.Errorf("INVALID_SITE_CONFIG: %s must be at most %d characters", field, maxRunes)
	}
	return s, nil
}

func decodeStrictJSON(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON token")
		}
		return fmt.Errorf("unexpected trailing JSON content: %w", err)
	}
	return nil
}
