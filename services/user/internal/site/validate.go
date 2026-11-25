package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

const (
	MaxConfigBytes      = 128 * 1024
	MaxJSONDepth        = 8
	MaxSections         = 20
	MaxPageSections     = 8
	MaxTotalSections    = 32
	MaxBlocksPerSection = 12
	MaxURLLength        = 500
	MinContrastRatio    = 4.5
)

var (
	subdomainRe        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)
	hexColorRe         = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	inlineEventAttrRe  = regexp.MustCompile(`(?i)\bon[a-z]+\s*=`)
	cssExpressionRe    = regexp.MustCompile(`(?i)\bexpression\s*\(`)
	cssURLFuncRe       = regexp.MustCompile(`(?i)\burl\s*\(`)
	forbiddenTextTerms = []string{
		"<script",
		"</script",
		"<style",
		"</style",
		"<svg",
		"</svg",
		"<iframe",
		"<object",
		"<embed",
		"<link",
		"<meta",
		"javascript:",
		"vbscript:",
		"data:",
		"@import",
		"-moz-binding",
	}
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
	SchemaVersion *int         `json:"schema_version"`
	Theme         *ThemeConfig `json:"theme"`
	Sections      []rawSection `json:"sections"`
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

// ValidateCustomHostname normalizes and validates a seller custom domain hostname.
// It trims outer whitespace, rejects forbidden characters/wildcards/ports/IPs,
// converts IDN labels to lowercase ASCII punycode via UTS #46 lookup, enforces
// DNS length/label constraints, rejects the platform root domain (and its
// subdomains), and rejects reserved local/internal hostnames (.test is allowed
// only when allowMockTest is true).
func ValidateCustomHostname(raw, rootDomain string, allowMockTest bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("invalid_hostname: hostname is required")
	}

	for _, r := range s {
		if unicode.IsSpace(r) {
			return "", errors.New("invalid_hostname: hostname must not contain whitespace")
		}
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return "", errors.New("invalid_hostname: hostname must not contain control characters")
		}
		switch r {
		case '/', '\\', '@', '?', '#', ':', '*', '[', ']', '%':
			return "", fmt.Errorf("invalid_hostname: hostname contains disallowed character %q", r)
		}
	}

	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return "", errors.New("invalid_hostname: hostname must not start or end with a dot")
	}
	if strings.Contains(s, "..") {
		return "", errors.New("invalid_hostname: hostname must not contain consecutive dots")
	}

	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", fmt.Errorf("invalid_hostname: invalid internationalized domain name: %w", err)
	}
	host := strings.ToLower(ascii)

	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", errors.New("invalid_hostname: hostname must not start or end with a dot")
	}
	if strings.Contains(host, "..") {
		return "", errors.New("invalid_hostname: hostname must not contain consecutive dots")
	}
	if len(host) < 2 || len(host) > 253 {
		return "", errors.New("invalid_hostname: hostname length must be between 2 and 253 characters")
	}
	if net.ParseIP(host) != nil {
		return "", errors.New("invalid_hostname: IP addresses are not allowed")
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", errors.New("invalid_hostname: hostname must contain at least two labels")
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 {
			return "", errors.New("invalid_hostname: each label must be between 1 and 63 characters")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid_hostname: labels must not start or end with a hyphen")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", errors.New("invalid_hostname: labels must contain only lowercase letters, digits, or hyphens")
			}
		}
	}

	tld := labels[len(labels)-1]
	allDigits := true
	for i := 0; i < len(tld); i++ {
		if tld[i] < '0' || tld[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "", errors.New("invalid_hostname: top-level domain must not be all numeric")
	}

	normRoot := strings.Trim(strings.ToLower(strings.TrimSpace(rootDomain)), ".")
	if normRoot != "" && (host == normRoot || strings.HasSuffix(host, "."+normRoot)) {
		return "", errors.New("invalid_hostname: hostname must not be the platform root domain or a subdomain of it")
	}

	if host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") {
		return "", errors.New("invalid_hostname: local and internal hostnames are not allowed")
	}
	if !allowMockTest && strings.HasSuffix(host, ".test") {
		return "", errors.New("invalid_hostname: .test hostnames are not allowed")
	}

	return host, nil
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

// ValidateImageURL validates an optional image URL. It extends ValidateSafeURL by rejecting
// SVG image URLs (.svg/.svgz, image/svg, svg+xml) and blocking SSRF targets on external https://
// hosts (IP literals, numeric/hex/octal IP forms, loopback/private/link-local/metadata/internal
// and single-label hostnames).
func ValidateImageURL(raw string) (string, error) {
	s, err := ValidateSafeURL(raw)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", nil
	}

	if hasDisallowedSVGURL(s) {
		return "", errors.New("UNSAFE_URL: SVG images are not allowed")
	}

	if strings.HasPrefix(s, "/") {
		return s, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("UNSAFE_URL: invalid image url")
	}
	if isDisallowedImageHost(u.Hostname()) {
		return "", errors.New("UNSAFE_URL: image host is not allowed (SSRF protection)")
	}
	return s, nil
}

func hasDisallowedSVGURL(s string) bool {
	candidates := []string{s}
	cur := s
	for i := 0; i < 2; i++ {
		next, err := url.QueryUnescape(cur)
		if err != nil || next == cur {
			break
		}
		candidates = append(candidates, next)
		cur = next
	}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if strings.Contains(lc, "image/svg") || strings.Contains(lc, "svg+xml") {
			return true
		}
	}

	u, err := url.Parse(s)
	if err != nil {
		return true
	}
	paths := []string{u.Path, u.EscapedPath()}
	curPath := u.EscapedPath()
	for i := 0; i < 2; i++ {
		next, err := url.PathUnescape(curPath)
		if err != nil || next == curPath {
			break
		}
		paths = append(paths, next)
		curPath = next
	}
	for _, p := range paths {
		if idx := strings.IndexAny(p, "?#;"); idx >= 0 {
			p = p[:idx]
		}
		lp := strings.ToLower(strings.TrimSpace(p))
		if strings.HasSuffix(lp, ".svg") || strings.HasSuffix(lp, ".svgz") {
			return true
		}
	}
	return false
}

func isDisallowedImageHost(rawHost string) bool {
	host := strings.ToLower(strings.TrimSpace(rawHost))
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return true
	}
	if strings.ContainsAny(host, "%:[]/\\@ \t\r\n") {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}

	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(ascii, "."))
	if host == "" || len(host) > 253 {
		return true
	}
	if strings.Contains(host, ":") || net.ParseIP(host) != nil {
		return true
	}
	if strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return true
	}
	if !strings.Contains(host, ".") {
		return true
	}

	if host == "localhost" ||
		host == "metadata.google.internal" ||
		host == "metadata.goog" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".localdomain") ||
		strings.HasSuffix(host, ".home.arpa") ||
		strings.HasSuffix(host, ".arpa") {
		return true
	}

	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return true
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 {
			return true
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return true
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return true
			}
		}
	}

	tld := labels[len(labels)-1]
	return isNumericOrHexLabel(tld)
}

func isNumericOrHexLabel(label string) bool {
	if label == "" {
		return true
	}
	if _, err := strconv.ParseUint(label, 0, 64); err == nil {
		return true
	}
	allDigits := true
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			allDigits = false
			break
		}
	}
	return allDigits
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

type jsonFrame struct {
	isObject  bool
	keys      map[string]struct{}
	expectKey bool
}

// validateJSONStructure performs a streaming pre-decode structural validation of a site config:
// size <= MaxConfigBytes, root JSON object, nesting depth <= MaxJSONDepth (counting '{' and '['),
// no duplicate object keys at any nesting level, and no trailing tokens.
func validateJSONStructure(raw []byte) error {
	if len(raw) > MaxConfigBytes {
		return fmt.Errorf("INVALID_SITE_CONFIG: config_json exceeds %d bytes", MaxConfigBytes)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("INVALID_SITE_CONFIG: config_json is required")
	}
	if trimmed[0] != '{' {
		return errors.New("INVALID_SITE_CONFIG: config_json must be a JSON object")
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	firstTok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("INVALID_SITE_CONFIG: invalid JSON: %w", err)
	}
	delim, ok := firstTok.(json.Delim)
	if !ok || delim != '{' {
		return errors.New("INVALID_SITE_CONFIG: config_json must be a JSON object")
	}

	stack := []jsonFrame{{
		isObject:  true,
		keys:      make(map[string]struct{}),
		expectKey: true,
	}}

	for len(stack) > 0 {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("INVALID_SITE_CONFIG: invalid JSON: %w", err)
		}

		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				if len(stack)+1 > MaxJSONDepth {
					return fmt.Errorf("INVALID_SITE_CONFIG: JSON nesting depth exceeds %d", MaxJSONDepth)
				}
				stack = append(stack, jsonFrame{
					isObject:  true,
					keys:      make(map[string]struct{}),
					expectKey: true,
				})
			case '[':
				if len(stack)+1 > MaxJSONDepth {
					return fmt.Errorf("INVALID_SITE_CONFIG: JSON nesting depth exceeds %d", MaxJSONDepth)
				}
				stack = append(stack, jsonFrame{
					isObject: false,
				})
			case '}', ']':
				top := stack[len(stack)-1]
				if (t == '}' && !top.isObject) || (t == ']' && top.isObject) {
					return errors.New("INVALID_SITE_CONFIG: mismatched JSON delimiter")
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObject {
					stack[len(stack)-1].expectKey = true
				}
			}
		case string:
			top := &stack[len(stack)-1]
			if top.isObject {
				if top.expectKey {
					if _, dup := top.keys[t]; dup {
						return fmt.Errorf("INVALID_SITE_CONFIG: duplicate JSON key %q", t)
					}
					top.keys[t] = struct{}{}
					top.expectKey = false
				} else {
					top.expectKey = true
				}
			}
		default:
			top := &stack[len(stack)-1]
			if top.isObject {
				top.expectKey = true
			}
		}
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("INVALID_SITE_CONFIG: unexpected trailing JSON token")
		}
		return fmt.Errorf("INVALID_SITE_CONFIG: unexpected trailing JSON content: %w", err)
	}
	return nil
}

// ValidateAndCanonicalizeConfig strictly decodes, validates, and re-marshals a SiteConfig JSON payload.
// Supports both legacy v1 (schema_version omitted or 1) and Declarative Theme JSON v2 (schema_version == 2).
// Unknown fields, duplicate keys, excessive nesting depth, and trailing tokens are rejected at every level.
func ValidateAndCanonicalizeConfig(raw []byte) (*Config, []byte, error) {
	if err := validateJSONStructure(raw); err != nil {
		return nil, nil, err
	}
	trimmed := bytes.TrimSpace(raw)

	version, err := peekSchemaVersion(trimmed)
	if err != nil {
		return nil, nil, err
	}
	switch version {
	case 0, 1:
		return validateAndCanonicalizeConfigV1(trimmed)
	case 2:
		v2, canonical, err := ValidateAndCanonicalizeConfigV2(trimmed)
		if err != nil {
			return nil, nil, err
		}
		return v2.ToSummaryConfig(), canonical, nil
	default:
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: unsupported schema_version %d", version)
	}
}

func peekSchemaVersion(trimmed []byte) (int, error) {
	var probe struct {
		SchemaVersion *json.RawMessage `json:"schema_version"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return 0, fmt.Errorf("INVALID_SITE_CONFIG: %w", err)
	}
	if probe.SchemaVersion == nil {
		return 0, nil
	}
	rawVer := bytes.TrimSpace(*probe.SchemaVersion)
	if len(rawVer) == 0 || bytes.Equal(rawVer, []byte("null")) {
		return 0, errors.New("INVALID_SITE_CONFIG: schema_version must be an integer (1 or 2)")
	}
	var ver int
	if err := json.Unmarshal(rawVer, &ver); err != nil {
		return 0, errors.New("INVALID_SITE_CONFIG: schema_version must be an integer (1 or 2)")
	}
	if ver != 1 && ver != 2 {
		return 0, fmt.Errorf("INVALID_SITE_CONFIG: unsupported schema_version %d", ver)
	}
	return ver, nil
}

func validateAndCanonicalizeConfigV1(trimmed []byte) (*Config, []byte, error) {
	var rc rawConfig
	if err := decodeStrictJSON(trimmed, &rc); err != nil {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: %w", err)
	}
	if rc.SchemaVersion != nil && *rc.SchemaVersion != 1 {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: unsupported schema_version %d", *rc.SchemaVersion)
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
	if t.LogoURL, err = ValidateImageURL(t.LogoURL); err != nil {
		return ThemeConfig{}, fmt.Errorf("theme.logo_url: %w", err)
	}
	if t.BannerURL, err = ValidateImageURL(t.BannerURL); err != nil {
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
		if p.ImageURL, err = ValidateImageURL(p.ImageURL); err != nil {
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
		if p.ImageURL, err = ValidateImageURL(p.ImageURL); err != nil {
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
		if p.ImageURL, err = ValidateImageURL(p.ImageURL); err != nil {
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
	if err := checkTextSecurity(field, s); err != nil {
		return "", err
	}
	return s, nil
}

func checkTextSecurity(field, s string) error {
	if s == "" {
		return nil
	}
	lower := strings.ToLower(s)
	for _, term := range forbiddenTextTerms {
		if strings.Contains(lower, term) {
			return fmt.Errorf("INVALID_SITE_CONFIG: UNSAFE_TEXT: %s contains disallowed pattern %q", field, term)
		}
	}
	if inlineEventAttrRe.MatchString(s) {
		return fmt.Errorf("INVALID_SITE_CONFIG: UNSAFE_TEXT: %s contains disallowed inline event handler", field)
	}
	if cssExpressionRe.MatchString(s) {
		return fmt.Errorf("INVALID_SITE_CONFIG: UNSAFE_TEXT: %s contains disallowed css expression()", field)
	}
	if cssURLFuncRe.MatchString(s) {
		return fmt.Errorf("INVALID_SITE_CONFIG: UNSAFE_TEXT: %s contains disallowed css url()", field)
	}
	return nil
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
