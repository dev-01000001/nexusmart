package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var templateIDRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

var (
	allowedRadii = map[string]struct{}{
		"":     {},
		"none": {},
		"sm":   {},
		"md":   {},
		"lg":   {},
	}
	allowedDensities = map[string]struct{}{
		"":         {},
		"compact":  {},
		"balanced": {},
		"airy":     {},
	}
	allowedArchetypes = map[string]struct{}{
		"minimal-fashion":   {},
		"editorial":         {},
		"dense-grid":        {},
		"craft-atelier":     {},
		"sport-active":      {},
		"luxury-maison":     {},
		"one-page-launch":   {},
		"multi-sku-catalog": {},
		"classic":           {},
	}
	allowedHeaderStyles = map[string]struct{}{
		"minimal-split":     {},
		"centered-masthead": {},
		"utility-compact":   {},
		"atelier-framed":    {},
		"bold-dark":         {},
		"maison-centered":   {},
		"anchor-minimal":    {},
		"catalog-rail":      {},
	}
	allowedFooterStyles = map[string]struct{}{
		"minimal-row":    {},
		"editorial-cols": {},
		"compact-bar":    {},
		"atelier-footer": {},
		"bold-dark":      {},
		"maison-minimal": {},
	}
	allowedContainerWidths = map[string]struct{}{
		"narrow":   {},
		"standard": {},
		"wide":     {},
		"full":     {},
	}
	allowedCardStyles = map[string]struct{}{
		"minimal":     {},
		"editorial":   {},
		"dense":       {},
		"atelier":     {},
		"sport":       {},
		"luxury":      {},
		"compact-row": {},
	}
	allowedSettingsSurfaces = map[string]struct{}{
		"":            {},
		"bg":          {},
		"surface":     {},
		"band":        {},
		"accent-soft": {},
	}
	allowedSettingsPaddings = map[string]struct{}{
		"":         {},
		"compact":  {},
		"normal":   {},
		"generous": {},
	}
	allowedSettingsAligns = map[string]struct{}{
		"":       {},
		"left":   {},
		"center": {},
	}
	allowedCatalogLayouts = map[string]struct{}{
		"minimal-gallery":    {},
		"editorial-magazine": {},
		"top-bar":            {},
		"sidebar-left":       {},
		"luxury-curated":     {},
		"single-strip":       {},
		"sku-table":          {},
	}
	allowedPDPLayouts = map[string]struct{}{
		"classic-split":    {},
		"editorial-tall":   {},
		"dense-spec":       {},
		"atelier-story":    {},
		"sport-telemetry":  {},
		"luxury-framed":    {},
		"one-page-drawer":  {},
		"sku-order-matrix": {},
	}
	allowedGalleryStyles = map[string]struct{}{
		"thumbnails": {},
		"stacked":    {},
		"framed":     {},
		"compact":    {},
	}
	allowedCartLayouts = map[string]struct{}{
		"split-summary":    {},
		"compact-table":    {},
		"editorial-ledger": {},
		"atelier-bag":      {},
		"express-bar":      {},
		"luxury-folio":     {},
	}
	allowedInfoLayouts = map[string]struct{}{
		"split-hero":         {},
		"editorial-essay":    {},
		"craft-timeline":     {},
		"athletic-manifesto": {},
		"luxury-maison":      {},
		"compact-faq":        {},
		"b2b-catalog-sheet":  {},
	}
	allowedSectionVariants = map[string]map[string]struct{}{
		"hero": {
			"":               {},
			"split":          {},
			"fullbleed":      {},
			"editorial":      {},
			"centered":       {},
			"compact-bar":    {},
			"atelier":        {},
			"sport-diagonal": {},
			"luxury-framed":  {},
		},
		"featured_products": {
			"":               {},
			"grid-4":         {},
			"grid-3":         {},
			"grid-2":         {},
			"dense-5":        {},
			"editorial-asym": {},
			"list-sku":       {},
		},
		"collection": {
			"":          {},
			"split":     {},
			"editorial": {},
			"compact":   {},
			"cards":     {},
		},
		"text": {
			"":          {},
			"split":     {},
			"centered":  {},
			"pullquote": {},
			"monograph": {},
		},
		"banner": {
			"":            {},
			"band":        {},
			"split-image": {},
			"framed":      {},
			"compact":     {},
		},
		"voucher_strip": {
			"":            {},
			"grid":        {},
			"compact-bar": {},
			"cards":       {},
		},
		"policies": {
			"":             {},
			"grid":         {},
			"timeline":     {},
			"compact-list": {},
			"cards":        {},
		},
		"lookbook": {
			"":          {},
			"asym-3":    {},
			"duo-split": {},
			"strip-4":   {},
		},
		"metrics_strip": {
			"":          {},
			"bar-4":     {},
			"cards-3":   {},
			"telemetry": {},
		},
		"sku_matrix": {
			"":              {},
			"compact-table": {},
			"grouped":       {},
		},
		"testimonials": {
			"":                 {},
			"grid-3":           {},
			"editorial-quotes": {},
		},
	}
	allowedHeadingLevels = map[string]struct{}{
		"":   {},
		"h2": {},
		"h3": {},
	}
	allowedBadgeTones = map[string]struct{}{
		"":        {},
		"neutral": {},
		"accent":  {},
		"band":    {},
	}
	allowedCTAStyles = map[string]struct{}{
		"":          {},
		"primary":   {},
		"secondary": {},
		"link":      {},
	}
	allowedImageAspects = map[string]struct{}{
		"":     {},
		"16:9": {},
		"4:3":  {},
		"3:4":  {},
		"1:1":  {},
	}
)

// ConfigV2 is the Declarative Theme JSON v2 configuration stored in JSONB.
type ConfigV2 struct {
	SchemaVersion int           `json:"schema_version"`
	TemplateID    string        `json:"template_id"`
	Theme         ThemeConfigV2 `json:"theme"`
	Layout        LayoutConfig  `json:"layout"`
	Sections      []SectionV2   `json:"sections"`
	Pages         PagesConfig   `json:"pages"`
}

// ThemeConfigV2 extends v1 ThemeConfig with optional radius and density tokens.
type ThemeConfigV2 struct {
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
	Radius          string `json:"radius,omitempty"`
	Density         string `json:"density,omitempty"`
}

// ToV1 converts ThemeConfigV2 to the legacy ThemeConfig projection.
func (t ThemeConfigV2) ToV1() ThemeConfig {
	return ThemeConfig{
		BGColor:         t.BGColor,
		SurfaceColor:    t.SurfaceColor,
		TextColor:       t.TextColor,
		AccentColor:     t.AccentColor,
		AccentTextColor: t.AccentTextColor,
		FontFamily:      t.FontFamily,
		LogoURL:         t.LogoURL,
		BannerURL:       t.BannerURL,
		BrandName:       t.BrandName,
		Tagline:         t.Tagline,
	}
}

// LayoutSlots controls integration slots (Swarm G search/chat/assistant) in the storefront chrome.
type LayoutSlots struct {
	Search    bool `json:"search"`
	Chat      bool `json:"chat"`
	Assistant bool `json:"assistant"`
}

// LayoutConfig defines site-wide layout chrome and structural presentation.
type LayoutConfig struct {
	Archetype        string      `json:"archetype"`
	HeaderStyle      string      `json:"header_style"`
	FooterStyle      string      `json:"footer_style"`
	ContainerWidth   string      `json:"container_width"`
	CardStyle        string      `json:"card_style"`
	ShowAnnouncement bool        `json:"show_announcement"`
	AnnouncementText string      `json:"announcement_text"`
	Slots            LayoutSlots `json:"slots"`
}

// SectionSettings defines per-section surface, spacing, columns, alignment, and item caps.
type SectionSettings struct {
	Surface  string `json:"surface,omitempty"`
	Padding  string `json:"padding,omitempty"`
	Columns  int    `json:"columns,omitempty"`
	Align    string `json:"align,omitempty"`
	MaxItems int    `json:"max_items,omitempty"`
}

// SectionBlock is a typed child block inside a v2 section.
type SectionBlock struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Props any    `json:"props"`
}

// SectionV2 is a v2 storefront section supporting variants, settings, typed props, and child blocks.
type SectionV2 struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Visible  bool             `json:"visible"`
	Variant  string           `json:"variant,omitempty"`
	Settings *SectionSettings `json:"settings,omitempty"`
	Props    any              `json:"props"`
	Blocks   []SectionBlock   `json:"blocks,omitempty"`
}

// LookbookProps configures a visual lookbook gallery section.
type LookbookProps struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	CTALabel string `json:"cta_label"`
	CTAHref  string `json:"cta_href"`
}

// MetricsStripProps configures a key figures / telemetry metrics strip section.
type MetricsStripProps struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}

// SkuMatrixProps configures a multi-SKU table / wholesale order matrix section.
type SkuMatrixProps struct {
	Title          string `json:"title"`
	Subtitle       string `json:"subtitle"`
	CategorySlug   string `json:"category_slug"`
	Limit          int    `json:"limit"`
	ShowOutOfStock bool   `json:"show_out_of_stock"`
}

// TestimonialsProps configures a customer/editorial quotes section.
type TestimonialsProps struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}

// Block props structs (9 block types).
type HeadingBlockProps struct {
	Text  string `json:"text"`
	Level string `json:"level,omitempty"`
}

type ParagraphBlockProps struct {
	Text string `json:"text"`
}

type BadgeBlockProps struct {
	Label string `json:"label"`
	Tone  string `json:"tone"`
}

type CTABlockProps struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	Style string `json:"style"`
}

type ImageBlockProps struct {
	ImageURL string `json:"image_url"`
	Alt      string `json:"alt"`
	Caption  string `json:"caption"`
	Aspect   string `json:"aspect"`
}

type StatBlockProps struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Note  string `json:"note"`
}

type PolicyBlockProps struct {
	Heading string `json:"heading"`
	Detail  string `json:"detail"`
}

type QuoteBlockProps struct {
	Body   string `json:"body"`
	Author string `json:"author"`
	Role   string `json:"role"`
}

type FeatureBlockProps struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Tag    string `json:"tag"`
}

// PagesConfig configures layout and optional extra sections for catalog, PDP, cart, and info pages.
type PagesConfig struct {
	Catalog CatalogPageConfig `json:"catalog"`
	PDP     PDPPageConfig     `json:"pdp"`
	Cart    CartPageConfig    `json:"cart"`
	Info    InfoPageConfig    `json:"info"`
}

type CatalogPageConfig struct {
	Layout        string      `json:"layout"`
	Columns       int         `json:"columns"`
	ShowFilters   bool        `json:"show_filters"`
	ShowSearch    bool        `json:"show_search"`
	ShowSkuMatrix bool        `json:"show_sku_matrix"`
	IntroTitle    string      `json:"intro_title"`
	IntroSubtitle string      `json:"intro_subtitle"`
	Sections      []SectionV2 `json:"sections,omitempty"`
}

type PDPPageConfig struct {
	Layout        string      `json:"layout"`
	GalleryStyle  string      `json:"gallery_style"`
	ShowVouchers  bool        `json:"show_vouchers"`
	ShowSkuTable  bool        `json:"show_sku_table"`
	ShowStoryBand bool        `json:"show_story_band"`
	ShowRelated   bool        `json:"show_related"`
	Sections      []SectionV2 `json:"sections,omitempty"`
}

type CartPageConfig struct {
	Layout       string      `json:"layout"`
	ShowVouchers bool        `json:"show_vouchers"`
	ShowAddons   bool        `json:"show_addons"`
	TrustNote    string      `json:"trust_note"`
	Sections     []SectionV2 `json:"sections,omitempty"`
}

type InfoPageConfig struct {
	Layout             string      `json:"layout"`
	ShowFactoryProfile bool        `json:"show_factory_profile"`
	ShowTOC            bool        `json:"show_toc"`
	ShowShowcase       bool        `json:"show_showcase"`
	Sections           []SectionV2 `json:"sections,omitempty"`
}

// ToSummaryConfig projects a ConfigV2 into the legacy v1 *Config summary (Theme + Home Sections)
// so existing callers that inspect Theme/Sections continue working seamlessly.
func (c *ConfigV2) ToSummaryConfig() *Config {
	if c == nil {
		return nil
	}
	secs := make([]Section, 0, len(c.Sections))
	for _, s := range c.Sections {
		secs = append(secs, Section{
			ID:      s.ID,
			Type:    s.Type,
			Visible: s.Visible,
			Props:   s.Props,
		})
	}
	return &Config{
		Theme:    c.Theme.ToV1(),
		Sections: secs,
	}
}

type rawConfigV2 struct {
	SchemaVersion int            `json:"schema_version"`
	TemplateID    string         `json:"template_id"`
	Theme         *ThemeConfigV2 `json:"theme"`
	Layout        *LayoutConfig  `json:"layout"`
	Sections      []rawSectionV2 `json:"sections"`
	Pages         *rawPagesV2    `json:"pages"`
}

type rawSectionV2 struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Visible  *bool            `json:"visible"`
	Variant  string           `json:"variant"`
	Settings *SectionSettings `json:"settings"`
	Props    json.RawMessage  `json:"props"`
	Blocks   []rawBlockV2     `json:"blocks"`
}

type rawBlockV2 struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Props json.RawMessage `json:"props"`
}

type rawPagesV2 struct {
	Catalog *rawCatalogPageV2 `json:"catalog"`
	PDP     *rawPDPPageV2     `json:"pdp"`
	Cart    *rawCartPageV2    `json:"cart"`
	Info    *rawInfoPageV2    `json:"info"`
}

type rawCatalogPageV2 struct {
	Layout        string         `json:"layout"`
	Columns       int            `json:"columns"`
	ShowFilters   bool           `json:"show_filters"`
	ShowSearch    bool           `json:"show_search"`
	ShowSkuMatrix bool           `json:"show_sku_matrix"`
	IntroTitle    string         `json:"intro_title"`
	IntroSubtitle string         `json:"intro_subtitle"`
	Sections      []rawSectionV2 `json:"sections"`
}

type rawPDPPageV2 struct {
	Layout        string         `json:"layout"`
	GalleryStyle  string         `json:"gallery_style"`
	ShowVouchers  bool           `json:"show_vouchers"`
	ShowSkuTable  bool           `json:"show_sku_table"`
	ShowStoryBand bool           `json:"show_story_band"`
	ShowRelated   bool           `json:"show_related"`
	Sections      []rawSectionV2 `json:"sections"`
}

type rawCartPageV2 struct {
	Layout       string         `json:"layout"`
	ShowVouchers bool           `json:"show_vouchers"`
	ShowAddons   bool           `json:"show_addons"`
	TrustNote    string         `json:"trust_note"`
	Sections     []rawSectionV2 `json:"sections"`
}

type rawInfoPageV2 struct {
	Layout             string         `json:"layout"`
	ShowFactoryProfile bool           `json:"show_factory_profile"`
	ShowTOC            bool           `json:"show_toc"`
	ShowShowcase       bool           `json:"show_showcase"`
	Sections           []rawSectionV2 `json:"sections"`
}

// ValidateAndCanonicalizeConfigV2 strictly decodes, validates, and re-marshals a Declarative Theme JSON v2 payload.
func ValidateAndCanonicalizeConfigV2(raw []byte) (*ConfigV2, []byte, error) {
	if err := validateJSONStructure(raw); err != nil {
		return nil, nil, err
	}
	trimmed := bytes.TrimSpace(raw)

	var rc rawConfigV2
	if err := decodeStrictJSON(trimmed, &rc); err != nil {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: %w", err)
	}
	if rc.SchemaVersion != 2 {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: schema_version must be 2, got %d", rc.SchemaVersion)
	}

	templateID := strings.TrimSpace(rc.TemplateID)
	if !templateIDRe.MatchString(templateID) {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: template_id must be 1-64 chars of [a-z0-9-]")
	}
	if rc.Theme == nil {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: theme is required")
	}
	if rc.Layout == nil {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: layout is required")
	}
	if rc.Pages == nil {
		return nil, nil, errors.New("INVALID_SITE_CONFIG: pages is required")
	}

	theme, err := validateThemeV2(*rc.Theme)
	if err != nil {
		return nil, nil, err
	}
	layout, err := validateLayoutV2(*rc.Layout)
	if err != nil {
		return nil, nil, err
	}

	if len(rc.Sections) > MaxSections {
		return nil, nil, fmt.Errorf("TOO_MANY_SECTIONS: at most %d home sections are allowed", MaxSections)
	}
	sections := make([]SectionV2, 0, len(rc.Sections))
	for i, rs := range rc.Sections {
		sec, err := validateSectionV2(fmt.Sprintf("sections[%d]", i), rs)
		if err != nil {
			return nil, nil, err
		}
		sections = append(sections, sec)
	}

	pages, pageSectionCount, err := validatePagesV2(*rc.Pages)
	if err != nil {
		return nil, nil, err
	}
	if len(sections)+pageSectionCount > MaxTotalSections {
		return nil, nil, fmt.Errorf("TOO_MANY_SECTIONS: total sections across home and pages cannot exceed %d", MaxTotalSections)
	}

	cfg := &ConfigV2{
		SchemaVersion: 2,
		TemplateID:    templateID,
		Theme:         theme,
		Layout:        layout,
		Sections:      sections,
		Pages:         pages,
	}
	canonical, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal canonical site config v2: %w", err)
	}
	if len(canonical) > MaxConfigBytes {
		return nil, nil, fmt.Errorf("INVALID_SITE_CONFIG: canonical config exceeds %d bytes", MaxConfigBytes)
	}
	return cfg, canonical, nil
}

func validateThemeV2(t ThemeConfigV2) (ThemeConfigV2, error) {
	base, err := validateTheme(t.ToV1())
	if err != nil {
		return ThemeConfigV2{}, err
	}
	radius := strings.TrimSpace(t.Radius)
	if _, ok := allowedRadii[radius]; !ok {
		return ThemeConfigV2{}, fmt.Errorf("INVALID_SITE_CONFIG: theme.radius %q is not allowed", t.Radius)
	}
	density := strings.TrimSpace(t.Density)
	if _, ok := allowedDensities[density]; !ok {
		return ThemeConfigV2{}, fmt.Errorf("INVALID_SITE_CONFIG: theme.density %q is not allowed", t.Density)
	}
	return ThemeConfigV2{
		BGColor:         base.BGColor,
		SurfaceColor:    base.SurfaceColor,
		TextColor:       base.TextColor,
		AccentColor:     base.AccentColor,
		AccentTextColor: base.AccentTextColor,
		FontFamily:      base.FontFamily,
		LogoURL:         base.LogoURL,
		BannerURL:       base.BannerURL,
		BrandName:       base.BrandName,
		Tagline:         base.Tagline,
		Radius:          radius,
		Density:         density,
	}, nil
}

func validateLayoutV2(l LayoutConfig) (LayoutConfig, error) {
	l.Archetype = strings.TrimSpace(l.Archetype)
	if _, ok := allowedArchetypes[l.Archetype]; !ok {
		return LayoutConfig{}, fmt.Errorf("INVALID_SITE_CONFIG: layout.archetype %q is not allowed", l.Archetype)
	}
	l.HeaderStyle = strings.TrimSpace(l.HeaderStyle)
	if _, ok := allowedHeaderStyles[l.HeaderStyle]; !ok {
		return LayoutConfig{}, fmt.Errorf("INVALID_SITE_CONFIG: layout.header_style %q is not allowed", l.HeaderStyle)
	}
	l.FooterStyle = strings.TrimSpace(l.FooterStyle)
	if _, ok := allowedFooterStyles[l.FooterStyle]; !ok {
		return LayoutConfig{}, fmt.Errorf("INVALID_SITE_CONFIG: layout.footer_style %q is not allowed", l.FooterStyle)
	}
	l.ContainerWidth = strings.TrimSpace(l.ContainerWidth)
	if _, ok := allowedContainerWidths[l.ContainerWidth]; !ok {
		return LayoutConfig{}, fmt.Errorf("INVALID_SITE_CONFIG: layout.container_width %q is not allowed", l.ContainerWidth)
	}
	l.CardStyle = strings.TrimSpace(l.CardStyle)
	if _, ok := allowedCardStyles[l.CardStyle]; !ok {
		return LayoutConfig{}, fmt.Errorf("INVALID_SITE_CONFIG: layout.card_style %q is not allowed", l.CardStyle)
	}
	var err error
	if l.AnnouncementText, err = validateText("layout.announcement_text", l.AnnouncementText, 0, 140, false); err != nil {
		return LayoutConfig{}, err
	}
	return l, nil
}

func validateSectionV2(prefix string, rs rawSectionV2) (SectionV2, error) {
	id, err := validateText(prefix+".id", rs.ID, 1, 64, false)
	if err != nil {
		return SectionV2{}, err
	}
	secType := strings.TrimSpace(rs.Type)
	variants, ok := allowedSectionVariants[secType]
	if !ok {
		return SectionV2{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.type %q is not supported", prefix, secType)
	}

	variant := strings.TrimSpace(rs.Variant)
	if _, ok := variants[variant]; !ok {
		return SectionV2{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.variant %q is not allowed for type %q", prefix, rs.Variant, secType)
	}

	visible := true
	if rs.Visible != nil {
		visible = *rs.Visible
	}

	var settings *SectionSettings
	if rs.Settings != nil {
		st, err := validateSectionSettings(prefix+".settings", *rs.Settings)
		if err != nil {
			return SectionV2{}, err
		}
		settings = &st
	}

	if len(rs.Blocks) > MaxBlocksPerSection {
		return SectionV2{}, fmt.Errorf("TOO_MANY_BLOCKS: %s.blocks cannot exceed %d items", prefix, MaxBlocksPerSection)
	}
	var blocks []SectionBlock
	if len(rs.Blocks) > 0 {
		blocks = make([]SectionBlock, 0, len(rs.Blocks))
		for j, rb := range rs.Blocks {
			blk, err := validateBlockV2(fmt.Sprintf("%s.blocks[%d]", prefix, j), rb)
			if err != nil {
				return SectionV2{}, err
			}
			blocks = append(blocks, blk)
		}
	}

	rawProps := bytes.TrimSpace(rs.Props)
	if len(rawProps) == 0 || rawProps[0] != '{' {
		return SectionV2{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props must be a JSON object", prefix)
	}

	props, err := validateSectionPropsV2(prefix, secType, rawProps, blocks)
	if err != nil {
		return SectionV2{}, err
	}

	return SectionV2{
		ID:       id,
		Type:     secType,
		Visible:  visible,
		Variant:  variant,
		Settings: settings,
		Props:    props,
		Blocks:   blocks,
	}, nil
}

func validateSectionSettings(prefix string, st SectionSettings) (SectionSettings, error) {
	st.Surface = strings.TrimSpace(st.Surface)
	if _, ok := allowedSettingsSurfaces[st.Surface]; !ok {
		return SectionSettings{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.surface %q is not allowed", prefix, st.Surface)
	}
	st.Padding = strings.TrimSpace(st.Padding)
	if _, ok := allowedSettingsPaddings[st.Padding]; !ok {
		return SectionSettings{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.padding %q is not allowed", prefix, st.Padding)
	}
	if st.Columns < 0 || st.Columns > 5 {
		return SectionSettings{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.columns must be between 0 and 5", prefix)
	}
	st.Align = strings.TrimSpace(st.Align)
	if _, ok := allowedSettingsAligns[st.Align]; !ok {
		return SectionSettings{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.align %q is not allowed", prefix, st.Align)
	}
	if st.MaxItems < 0 || st.MaxItems > 24 {
		return SectionSettings{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.max_items must be between 0 and 24", prefix)
	}
	return st, nil
}

func validateSectionPropsV2(prefix, secType string, rawProps []byte, blocks []SectionBlock) (any, error) {
	var err error
	switch secType {
	case "hero", "featured_products", "collection", "text", "banner", "voucher_strip":
		// Delegate to v1 section prop validation for complete parity on the 6 shared section types.
		vis := true
		sec, err := validateSection(0, rawSection{
			ID:      "v2-proxy",
			Type:    secType,
			Visible: &vis,
			Props:   rawProps,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", prefix, err)
		}
		return sec.Props, nil

	case "policies":
		var p PoliciesProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return nil, err
		}
		hasPolicyBlocks := false
		for _, b := range blocks {
			if b.Type == "policy" {
				hasPolicyBlocks = true
				break
			}
		}
		if len(p.Items) > 8 || (len(p.Items) == 0 && !hasPolicyBlocks) {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.items must have between 1 and 8 items (or provide policy blocks)", prefix)
		}
		for j := range p.Items {
			itemPrefix := fmt.Sprintf("%s.props.items[%d]", prefix, j)
			if p.Items[j].Heading, err = validateText(itemPrefix+".heading", p.Items[j].Heading, 1, 80, false); err != nil {
				return nil, err
			}
			if p.Items[j].Detail, err = validateText(itemPrefix+".detail", p.Items[j].Detail, 1, 400, true); err != nil {
				return nil, err
			}
		}
		return p, nil

	case "lookbook":
		var p LookbookProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return nil, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 400, true); err != nil {
			return nil, err
		}
		if p.CTALabel, err = validateText(prefix+".props.cta_label", p.CTALabel, 0, 40, false); err != nil {
			return nil, err
		}
		if p.CTAHref, err = ValidateSafeURL(p.CTAHref); err != nil {
			return nil, fmt.Errorf("%s.props.cta_href: %w", prefix, err)
		}
		return p, nil

	case "metrics_strip":
		var p MetricsStripProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return nil, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return nil, err
		}
		return p, nil

	case "sku_matrix":
		var p SkuMatrixProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return nil, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return nil, err
		}
		if p.CategorySlug, err = validateText(prefix+".props.category_slug", p.CategorySlug, 0, 80, false); err != nil {
			return nil, err
		}
		if p.Limit < 0 || p.Limit > 24 {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.limit must be between 0 and 24", prefix)
		}
		return p, nil

	case "testimonials":
		var p TestimonialsProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 120, false); err != nil {
			return nil, err
		}
		if p.Subtitle, err = validateText(prefix+".props.subtitle", p.Subtitle, 0, 300, true); err != nil {
			return nil, err
		}
		return p, nil

	default:
		return nil, fmt.Errorf("INVALID_SITE_CONFIG: %s.type %q is not supported", prefix, secType)
	}
}

func validateBlockV2(prefix string, rb rawBlockV2) (SectionBlock, error) {
	id, err := validateText(prefix+".id", rb.ID, 1, 64, false)
	if err != nil {
		return SectionBlock{}, err
	}
	blkType := strings.TrimSpace(rb.Type)
	rawProps := bytes.TrimSpace(rb.Props)
	if len(rawProps) == 0 || rawProps[0] != '{' {
		return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props must be a JSON object", prefix)
	}

	var props any
	switch blkType {
	case "heading":
		var p HeadingBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Text, err = validateText(prefix+".props.text", p.Text, 1, 120, false); err != nil {
			return SectionBlock{}, err
		}
		p.Level = strings.TrimSpace(p.Level)
		if _, ok := allowedHeadingLevels[p.Level]; !ok {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.level %q is not allowed", prefix, p.Level)
		}
		props = p

	case "paragraph":
		var p ParagraphBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Text, err = validateText(prefix+".props.text", p.Text, 1, 800, true); err != nil {
			return SectionBlock{}, err
		}
		props = p

	case "badge":
		var p BadgeBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Label, err = validateText(prefix+".props.label", p.Label, 1, 48, false); err != nil {
			return SectionBlock{}, err
		}
		p.Tone = strings.TrimSpace(p.Tone)
		if _, ok := allowedBadgeTones[p.Tone]; !ok {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.tone %q is not allowed", prefix, p.Tone)
		}
		props = p

	case "cta":
		var p CTABlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Label, err = validateText(prefix+".props.label", p.Label, 1, 48, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Href, err = ValidateSafeURL(p.Href); err != nil {
			return SectionBlock{}, fmt.Errorf("%s.props.href: %w", prefix, err)
		}
		if p.Href == "" {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.href is required", prefix)
		}
		p.Style = strings.TrimSpace(p.Style)
		if _, ok := allowedCTAStyles[p.Style]; !ok {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.style %q is not allowed", prefix, p.Style)
		}
		props = p

	case "image":
		var p ImageBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.ImageURL, err = ValidateImageURL(p.ImageURL); err != nil {
			return SectionBlock{}, fmt.Errorf("%s.props.image_url: %w", prefix, err)
		}
		if p.ImageURL == "" {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.image_url is required", prefix)
		}
		if p.Alt, err = validateText(prefix+".props.alt", p.Alt, 0, 140, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Caption, err = validateText(prefix+".props.caption", p.Caption, 0, 200, false); err != nil {
			return SectionBlock{}, err
		}
		p.Aspect = strings.TrimSpace(p.Aspect)
		if _, ok := allowedImageAspects[p.Aspect]; !ok {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props.aspect %q is not allowed", prefix, p.Aspect)
		}
		props = p

	case "stat":
		var p StatBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Value, err = validateText(prefix+".props.value", p.Value, 1, 32, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Label, err = validateText(prefix+".props.label", p.Label, 1, 60, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Note, err = validateText(prefix+".props.note", p.Note, 0, 140, false); err != nil {
			return SectionBlock{}, err
		}
		props = p

	case "policy":
		var p PolicyBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Heading, err = validateText(prefix+".props.heading", p.Heading, 1, 80, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Detail, err = validateText(prefix+".props.detail", p.Detail, 1, 400, true); err != nil {
			return SectionBlock{}, err
		}
		props = p

	case "quote":
		var p QuoteBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Body, err = validateText(prefix+".props.body", p.Body, 1, 400, true); err != nil {
			return SectionBlock{}, err
		}
		if p.Author, err = validateText(prefix+".props.author", p.Author, 1, 80, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Role, err = validateText(prefix+".props.role", p.Role, 0, 80, false); err != nil {
			return SectionBlock{}, err
		}
		props = p

	case "feature":
		var p FeatureBlockProps
		if err := decodeStrictJSON(rawProps, &p); err != nil {
			return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.props: %w", prefix, err)
		}
		if p.Title, err = validateText(prefix+".props.title", p.Title, 1, 80, false); err != nil {
			return SectionBlock{}, err
		}
		if p.Detail, err = validateText(prefix+".props.detail", p.Detail, 1, 300, true); err != nil {
			return SectionBlock{}, err
		}
		if p.Tag, err = validateText(prefix+".props.tag", p.Tag, 0, 40, false); err != nil {
			return SectionBlock{}, err
		}
		props = p

	default:
		return SectionBlock{}, fmt.Errorf("INVALID_SITE_CONFIG: %s.type %q is not supported", prefix, blkType)
	}

	return SectionBlock{
		ID:    id,
		Type:  blkType,
		Props: props,
	}, nil
}

func validatePageSectionsV2(pageName string, rawSecs []rawSectionV2) ([]SectionV2, error) {
	if len(rawSecs) > MaxPageSections {
		return nil, fmt.Errorf("TOO_MANY_SECTIONS: pages.%s.sections cannot exceed %d items", pageName, MaxPageSections)
	}
	if len(rawSecs) == 0 {
		return nil, nil
	}
	out := make([]SectionV2, 0, len(rawSecs))
	for i, rs := range rawSecs {
		sec, err := validateSectionV2(fmt.Sprintf("pages.%s.sections[%d]", pageName, i), rs)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, nil
}

func validatePagesV2(rp rawPagesV2) (PagesConfig, int, error) {
	if rp.Catalog == nil {
		return PagesConfig{}, 0, errors.New("INVALID_SITE_CONFIG: pages.catalog is required")
	}
	if rp.PDP == nil {
		return PagesConfig{}, 0, errors.New("INVALID_SITE_CONFIG: pages.pdp is required")
	}
	if rp.Cart == nil {
		return PagesConfig{}, 0, errors.New("INVALID_SITE_CONFIG: pages.cart is required")
	}
	if rp.Info == nil {
		return PagesConfig{}, 0, errors.New("INVALID_SITE_CONFIG: pages.info is required")
	}

	catLayout := strings.TrimSpace(rp.Catalog.Layout)
	if _, ok := allowedCatalogLayouts[catLayout]; !ok {
		return PagesConfig{}, 0, fmt.Errorf("INVALID_SITE_CONFIG: pages.catalog.layout %q is not allowed", rp.Catalog.Layout)
	}
	if rp.Catalog.Columns < 1 || rp.Catalog.Columns > 5 {
		return PagesConfig{}, 0, errors.New("INVALID_SITE_CONFIG: pages.catalog.columns must be between 1 and 5")
	}
	introTitle, err := validateText("pages.catalog.intro_title", rp.Catalog.IntroTitle, 0, 120, false)
	if err != nil {
		return PagesConfig{}, 0, err
	}
	introSubtitle, err := validateText("pages.catalog.intro_subtitle", rp.Catalog.IntroSubtitle, 0, 300, true)
	if err != nil {
		return PagesConfig{}, 0, err
	}
	catSecs, err := validatePageSectionsV2("catalog", rp.Catalog.Sections)
	if err != nil {
		return PagesConfig{}, 0, err
	}

	pdpLayout := strings.TrimSpace(rp.PDP.Layout)
	if _, ok := allowedPDPLayouts[pdpLayout]; !ok {
		return PagesConfig{}, 0, fmt.Errorf("INVALID_SITE_CONFIG: pages.pdp.layout %q is not allowed", rp.PDP.Layout)
	}
	galleryStyle := strings.TrimSpace(rp.PDP.GalleryStyle)
	if _, ok := allowedGalleryStyles[galleryStyle]; !ok {
		return PagesConfig{}, 0, fmt.Errorf("INVALID_SITE_CONFIG: pages.pdp.gallery_style %q is not allowed", rp.PDP.GalleryStyle)
	}
	pdpSecs, err := validatePageSectionsV2("pdp", rp.PDP.Sections)
	if err != nil {
		return PagesConfig{}, 0, err
	}

	cartLayout := strings.TrimSpace(rp.Cart.Layout)
	if _, ok := allowedCartLayouts[cartLayout]; !ok {
		return PagesConfig{}, 0, fmt.Errorf("INVALID_SITE_CONFIG: pages.cart.layout %q is not allowed", rp.Cart.Layout)
	}
	trustNote, err := validateText("pages.cart.trust_note", rp.Cart.TrustNote, 0, 240, true)
	if err != nil {
		return PagesConfig{}, 0, err
	}
	cartSecs, err := validatePageSectionsV2("cart", rp.Cart.Sections)
	if err != nil {
		return PagesConfig{}, 0, err
	}

	infoLayout := strings.TrimSpace(rp.Info.Layout)
	if _, ok := allowedInfoLayouts[infoLayout]; !ok {
		return PagesConfig{}, 0, fmt.Errorf("INVALID_SITE_CONFIG: pages.info.layout %q is not allowed", rp.Info.Layout)
	}
	infoSecs, err := validatePageSectionsV2("info", rp.Info.Sections)
	if err != nil {
		return PagesConfig{}, 0, err
	}

	totalPageSecs := len(catSecs) + len(pdpSecs) + len(cartSecs) + len(infoSecs)
	return PagesConfig{
		Catalog: CatalogPageConfig{
			Layout:        catLayout,
			Columns:       rp.Catalog.Columns,
			ShowFilters:   rp.Catalog.ShowFilters,
			ShowSearch:    rp.Catalog.ShowSearch,
			ShowSkuMatrix: rp.Catalog.ShowSkuMatrix,
			IntroTitle:    introTitle,
			IntroSubtitle: introSubtitle,
			Sections:      catSecs,
		},
		PDP: PDPPageConfig{
			Layout:        pdpLayout,
			GalleryStyle:  galleryStyle,
			ShowVouchers:  rp.PDP.ShowVouchers,
			ShowSkuTable:  rp.PDP.ShowSkuTable,
			ShowStoryBand: rp.PDP.ShowStoryBand,
			ShowRelated:   rp.PDP.ShowRelated,
			Sections:      pdpSecs,
		},
		Cart: CartPageConfig{
			Layout:       cartLayout,
			ShowVouchers: rp.Cart.ShowVouchers,
			ShowAddons:   rp.Cart.ShowAddons,
			TrustNote:    trustNote,
			Sections:     cartSecs,
		},
		Info: InfoPageConfig{
			Layout:             infoLayout,
			ShowFactoryProfile: rp.Info.ShowFactoryProfile,
			ShowTOC:            rp.Info.ShowTOC,
			ShowShowcase:       rp.Info.ShowShowcase,
			Sections:           infoSecs,
		},
	}, totalPageSecs, nil
}

// ConvertV1ToV2 deterministically converts a legacy v1 {theme, sections} site config into a
// canonical Declarative Theme JSON v2 config (template_id: "legacy-v1"), preserving all colors,
// typography, branding, and sections without data loss. If raw is already v2, it validates and returns it.
func ConvertV1ToV2(raw []byte) (*ConfigV2, []byte, error) {
	if err := validateJSONStructure(raw); err != nil {
		return nil, nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	ver, err := peekSchemaVersion(trimmed)
	if err != nil {
		return nil, nil, err
	}
	if ver == 2 {
		return ValidateAndCanonicalizeConfigV2(trimmed)
	}

	v1, _, err := validateAndCanonicalizeConfigV1(trimmed)
	if err != nil {
		return nil, nil, err
	}

	sections := make([]SectionV2, 0, len(v1.Sections))
	for _, s := range v1.Sections {
		sections = append(sections, SectionV2{
			ID:      s.ID,
			Type:    s.Type,
			Visible: s.Visible,
			Props:   s.Props,
		})
	}

	v2 := ConfigV2{
		SchemaVersion: 2,
		TemplateID:    "legacy-v1",
		Theme: ThemeConfigV2{
			BGColor:         v1.Theme.BGColor,
			SurfaceColor:    v1.Theme.SurfaceColor,
			TextColor:       v1.Theme.TextColor,
			AccentColor:     v1.Theme.AccentColor,
			AccentTextColor: v1.Theme.AccentTextColor,
			FontFamily:      v1.Theme.FontFamily,
			LogoURL:         v1.Theme.LogoURL,
			BannerURL:       v1.Theme.BannerURL,
			BrandName:       v1.Theme.BrandName,
			Tagline:         v1.Theme.Tagline,
			Radius:          "md",
			Density:         "balanced",
		},
		Layout: LayoutConfig{
			Archetype:        "classic",
			HeaderStyle:      "minimal-split",
			FooterStyle:      "minimal-row",
			ContainerWidth:   "standard",
			CardStyle:        "minimal",
			ShowAnnouncement: false,
			AnnouncementText: "",
			Slots: LayoutSlots{
				Search:    true,
				Chat:      true,
				Assistant: true,
			},
		},
		Sections: sections,
		Pages: PagesConfig{
			Catalog: CatalogPageConfig{
				Layout:        "top-bar",
				Columns:       4,
				ShowFilters:   true,
				ShowSearch:    true,
				ShowSkuMatrix: false,
				IntroTitle:    "",
				IntroSubtitle: "",
			},
			PDP: PDPPageConfig{
				Layout:        "classic-split",
				GalleryStyle:  "thumbnails",
				ShowVouchers:  true,
				ShowSkuTable:  false,
				ShowStoryBand: true,
				ShowRelated:   true,
			},
			Cart: CartPageConfig{
				Layout:       "split-summary",
				ShowVouchers: true,
				ShowAddons:   false,
				TrustNote:    "Đổi trả dễ dàng và thanh toán bảo vệ bởi NexusMart.",
			},
			Info: InfoPageConfig{
				Layout:             "split-hero",
				ShowFactoryProfile: true,
				ShowTOC:            false,
				ShowShowcase:       true,
			},
		},
	}

	rawV2, err := json.Marshal(&v2)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal converted v2 config: %w", err)
	}
	return ValidateAndCanonicalizeConfigV2(rawV2)
}

// ApplyTemplateToConfig applies a platform template's v2 config structure while preserving the
// seller's brand identity (brand_name, customized tagline, and logo_url) from existingRaw or
// shop metadata, returning strictly validated canonical v2 JSON.
func ApplyTemplateToConfig(templateConfigJSON []byte, existingRaw []byte, shopName, shopDescription string) ([]byte, error) {
	tmplCfg, _, err := ValidateAndCanonicalizeConfigV2(templateConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("invalid template config: %w", err)
	}

	var existingBrand, existingTagline, existingLogo string
	if len(bytes.TrimSpace(existingRaw)) > 0 {
		var prev struct {
			Theme struct {
				BrandName string `json:"brand_name"`
				Tagline   string `json:"tagline"`
				LogoURL   string `json:"logo_url"`
			} `json:"theme"`
		}
		if json.Unmarshal(existingRaw, &prev) == nil {
			existingBrand = strings.TrimSpace(prev.Theme.BrandName)
			existingTagline = strings.TrimSpace(prev.Theme.Tagline)
			existingLogo = strings.TrimSpace(prev.Theme.LogoURL)
		}
	}

	if existingBrand != "" {
		if clean, err := validateText("theme.brand_name", existingBrand, 1, 80, false); err == nil {
			tmplCfg.Theme.BrandName = clean
		}
	} else if name := truncateRunes(strings.TrimSpace(shopName), 80); name != "" {
		if clean, err := validateText("theme.brand_name", name, 1, 80, false); err == nil {
			tmplCfg.Theme.BrandName = clean
		}
	}

	if existingTagline != "" {
		if clean, err := validateText("theme.tagline", existingTagline, 0, 160, true); err == nil {
			tmplCfg.Theme.Tagline = clean
		}
	} else if desc := truncateRunes(strings.TrimSpace(shopDescription), 160); desc != "" {
		if clean, err := validateText("theme.tagline", desc, 0, 160, true); err == nil {
			tmplCfg.Theme.Tagline = clean
		}
	}

	if existingLogo != "" {
		if clean, err := ValidateImageURL(existingLogo); err == nil && clean != "" {
			tmplCfg.Theme.LogoURL = clean
		}
	}

	merged, err := json.Marshal(tmplCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal applied template config: %w", err)
	}
	_, canonical, err := ValidateAndCanonicalizeConfigV2(merged)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= maxRunes {
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}
