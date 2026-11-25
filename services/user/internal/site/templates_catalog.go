package site

import "encoding/json"

// PlatformTemplate describes one built-in NexusMart storefront template.
type PlatformTemplate struct {
	ID              string
	Slug            string
	Name            string
	Category        string
	Archetype       string
	Description     string
	PreviewImageURL string
	Tags            []string
	SortOrder       int32
	Config          ConfigV2
}

// ConfigJSON marshals the template's ConfigV2 into canonical JSON bytes.
func (t PlatformTemplate) ConfigJSON() ([]byte, error) {
	raw, err := json.Marshal(t.Config)
	if err != nil {
		return nil, err
	}
	_, canonical, err := ValidateAndCanonicalizeConfigV2(raw)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

// BuiltInTemplates returns the 8 platform storefront templates aligned 1-to-1 with
// web/src/features/site/templateCatalog.ts, covering 8 distinct layout archetypes
// and all 5 storefront pages (home, catalog, PDP, cart, info).
func BuiltInTemplates() []PlatformTemplate {
	return []PlatformTemplate{
		minimalFashionTemplate(),
		editorialTemplate(),
		denseGridTemplate(),
		craftAtelierTemplate(),
		sportActiveTemplate(),
		luxuryMaisonTemplate(),
		onePageLaunchTemplate(),
		multiSkuCatalogTemplate(),
	}
}

func defaultSlots() LayoutSlots {
	return LayoutSlots{
		Search:    true,
		Chat:      true,
		Assistant: true,
	}
}

func minimalFashionTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "minimal-fashion",
		Slug:            "minimal-fashion",
		Name:            "Thời trang tối giản",
		Category:        "Thời trang",
		Archetype:       "minimal-fashion",
		Description:     "Ảnh đứng giữa, chữ tinh gọn, lưới 3 cột so le độ cao.",
		PreviewImageURL: "/banners/campaign-everyday.jpg",
		Tags:            []string{"Tối giản", "Lưới 3 cột", "Gallery thoáng"},
		SortOrder:       1,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "minimal-fashion",
			Theme: ThemeConfigV2{
				BGColor:         "#FAFAFA",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#14181C",
				AccentColor:     "#14181C",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "inter",
				LogoURL:         "",
				BannerURL:       "/banners/campaign-everyday.jpg",
				BrandName:       "Studio Tối Giản",
				Tagline:         "Trang phục cơ bản tỉ lệ chuẩn, mặc bền nhiều mùa.",
				Radius:          "none",
				Density:         "airy",
			},
			Layout: LayoutConfig{
				Archetype:        "minimal-fashion",
				HeaderStyle:      "minimal-split",
				FooterStyle:      "minimal-row",
				ContainerWidth:   "standard",
				CardStyle:        "minimal",
				ShowAnnouncement: false,
				AnnouncementText: "",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "mf-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "centered",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Phom dáng tinh gọn mặc mỗi ngày",
						Subtitle: "Cotton chải kỹ và linen giữ phom phẳng mịn.",
						ImageURL: "/banners/campaign-everyday.jpg",
						CTALabel: "Xem bộ sưu tập",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "mf-hero-b1", Type: "badge", Props: BadgeBlockProps{Label: "Cotton 220gsm", Tone: "neutral"}},
					},
				},
				{
					ID:       "mf-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-3",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Thiết kế tuyển chọn",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "mf-story",
					Type:     "text",
					Visible:  true,
					Variant:  "centered",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: TextProps{
						Title: "Ít chi tiết, bền dáng hơn",
						Body:  "Lược bỏ họa tiết thừa để tập trung vào độ đứng vai, độ rủ sợi vải và khả năng phối hằng ngày.",
						Align: "center",
					},
				},
				{
					ID:       "mf-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: PoliciesProps{
						Title: "Cam kết cửa hàng",
						Items: []PolicyItem{
							{Heading: "Đổi size 45 ngày", Detail: "Đổi cỡ tận nơi miễn phí hai chiều."},
							{Heading: "Đồng kiểm khi nhận", Detail: "Kiểm tra vải và phom trước khi trả tiền."},
							{Heading: "Rõ định lượng vải", Detail: "Công bố định lượng GSM và tỷ lệ sợi từng mẫu."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "minimal-gallery",
					Columns:       3,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Tủ đồ tối giản",
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
					ShowAddons:   true,
					TrustNote:    "Hộp giấy gọn, đổi size 45 ngày.",
				},
				Info: InfoPageConfig{
					Layout:             "split-hero",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       true,
				},
			},
		},
	}
}

func editorialTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "editorial",
		Slug:            "editorial",
		Name:            "Tạp chí Editorial",
		Category:        "Biên tập & Lookbook",
		Archetype:       "editorial",
		Description:     "Đầu trang masthead, tiêu đề lớn, lưới tạp chí bất đối xứng.",
		PreviewImageURL: "/banners/campaign-office.jpg",
		Tags:            []string{"Tạp chí", "Lookbook bất đối xứng", "Đầu trang căn giữa"},
		SortOrder:       2,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "editorial",
			Theme: ThemeConfigV2{
				BGColor:         "#F6F5F1",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#192026",
				AccentColor:     "#1D4ED8",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "plus-jakarta-sans",
				LogoURL:         "",
				BannerURL:       "/banners/campaign-office.jpg",
				BrandName:       "Tạp Chí Phom Dáng",
				Tagline:         "Ấn phẩm thời trang công sở kể chuyện qua khung hình.",
				Radius:          "none",
				Density:         "airy",
			},
			Layout: LayoutConfig{
				Archetype:        "editorial",
				HeaderStyle:      "centered-masthead",
				FooterStyle:      "editorial-cols",
				ContainerWidth:   "wide",
				CardStyle:        "editorial",
				ShowAnnouncement: true,
				AnnouncementText: "Số Thu Đông 2026, giao toàn quốc 1 đến 3 ngày",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "ed-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "editorial",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Đường cắt nhẹ vai giữa phố",
						Subtitle: "Polo dệt kim, linen thoáng và áo khoác nhẹ.",
						ImageURL: "/banners/campaign-office.jpg",
						CTALabel: "Xem ấn phẩm",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{
							ID:   "ed-hero-b1",
							Type: "quote",
							Props: QuoteBlockProps{
								Body:   "Chiếc áo mặc 10 tiếng cần nhẹ vai và đứng cổ tới cuối ngày.",
								Author: "Ban biên tập",
								Role:   "Ghi chú chất liệu",
							},
						},
					},
				},
				{
					ID:       "ed-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "asym-3",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous"},
					Props: LookbookProps{
						Title:    "Lookbook mùa này",
						Subtitle: "",
						CTALabel: "Xem danh mục",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ed-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-office.jpg", Alt: "Trang phục công sở gọn gàng", Caption: "Phom đứng cho ngày làm việc", Aspect: "3:4"}},
						{ID: "ed-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/fabric-closeup.jpg", Alt: "Cận cảnh bề mặt sợi vải", Caption: "Sợi dệt thoáng, ít nhăn", Aspect: "4:3"}},
						{ID: "ed-lb-3", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/vietnam-workshop.jpg", Alt: "Xưởng may hoàn thiện sản phẩm", Caption: "Cắt rập tại xưởng nội địa", Aspect: "4:3"}},
					},
				},
				{
					ID:       "ed-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "editorial-asym",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Tuyển chọn biên tập",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "ed-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal"},
					Props: VoucherStripProps{
						Title:    "Ưu đãi cửa hàng",
						Subtitle: "",
						Limit:    4,
					},
				},
				{
					ID:       "ed-essay",
					Type:     "text",
					Visible:  true,
					Variant:  "pullquote",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous"},
					Props: TextProps{
						Title: "Từ bản phác thảo đến tủ đồ hằng ngày",
						Body:  "Chúng tôi ghi lại từng mẻ vải, độ bền màu sau giặt và trải nghiệm mặc thực tế trong khí hậu nhiệt đới ẩm.",
						Align: "left",
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "editorial-magazine",
					Columns:       2,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Danh mục ấn phẩm",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "editorial-tall",
					GalleryStyle:  "stacked",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "editorial-ledger",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Kèm hướng dẫn chăm sóc chất liệu in riêng.",
				},
				Info: InfoPageConfig{
					Layout:             "editorial-essay",
					ShowFactoryProfile: true,
					ShowTOC:            true,
					ShowShowcase:       true,
				},
			},
		},
	}
}

func denseGridTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "dense-grid",
		Slug:            "dense-grid",
		Name:            "Lưới dày hiệu suất",
		Category:        "Bán lẻ nhanh",
		Archetype:       "dense-grid",
		Description:     "Vào thẳng ô tìm lớn, lọc dính và tường sản phẩm mật độ cao.",
		PreviewImageURL: "/banners/campaign-green-fiber.jpg",
		Tags:            []string{"Lưới 5 cột", "Mật độ cao", "Chốt đơn nhanh"},
		SortOrder:       3,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "dense-grid",
			Theme: ThemeConfigV2{
				BGColor:         "#F4F6F8",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#132028",
				AccentColor:     "#0D7480",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "work-sans",
				LogoURL:         "",
				BannerURL:       "/banners/campaign-green-fiber.jpg",
				BrandName:       "Kho Sẵn Giao Nhanh",
				Tagline:         "Hàng cơ bản đủ size S tới 2XL, cập nhật tồn thực tế.",
				Radius:          "sm",
				Density:         "compact",
			},
			Layout: LayoutConfig{
				Archetype:        "dense-grid",
				HeaderStyle:      "utility-compact",
				FooterStyle:      "compact-bar",
				ContainerWidth:   "full",
				CardStyle:        "dense",
				ShowAnnouncement: true,
				AnnouncementText: "Đặt trước 17h giao trong ngày, đồng kiểm toàn quốc",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "dg-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "compact-bar",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Kho sẵn đủ size S đến 2XL",
						Subtitle: "Tìm nhanh theo tên, chất liệu hoặc mã mẫu.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Mở kho hàng",
						CTAHref:  "/san-pham",
					},
				},
				{
					ID:       "dg-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "compact-bar",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Mã giảm giá hôm nay",
						Subtitle: "",
						Limit:    6,
					},
				},
				{
					ID:       "dg-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "dense-5",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 5},
					Props: FeaturedProductsProps{
						Title:      "Sản phẩm bán chạy",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      10,
					},
				},
				{
					ID:       "dg-collection",
					Type:     "collection",
					Visible:  true,
					Variant:  "compact",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 4},
					Props: CollectionProps{
						Title:          "Áo thun mặc mỗi ngày",
						Subtitle:       "",
						CollectionSlug: "co-ban-hang-ngay",
						CategorySlug:   "ao-thun",
						ImageURL:       "/banners/campaign-everyday.jpg",
						Limit:          8,
					},
				},
				{
					ID:       "dg-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "compact-list",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 4},
					Props: PoliciesProps{
						Title: "Quy chuẩn giao nhận",
						Items: []PolicyItem{
							{Heading: "Xuất kho trong ngày", Detail: "Đơn trước 17h bàn giao vận chuyển ngay."},
							{Heading: "Tồn kho đồng bộ", Detail: "Hiển thị đúng số lượng sẵn từng SKU."},
							{Heading: "Đổi size 45 ngày", Detail: "Đổi tận nơi với sản phẩm nguyên tem."},
							{Heading: "COD & VietQR", Detail: "Đồng kiểm khi nhận hoặc chuyển khoản nhanh."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "top-bar",
					Columns:       5,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Kho hàng sẵn giao",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "dense-spec",
					GalleryStyle:  "compact",
					ShowVouchers:  true,
					ShowSkuTable:  true,
					ShowStoryBand: false,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "compact-table",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Đồng kiểm COD, áp mã gian hàng tự động.",
				},
				Info: InfoPageConfig{
					Layout:             "compact-faq",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       false,
				},
			},
		},
	}
}

func craftAtelierTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "craft-atelier",
		Slug:            "craft-atelier",
		Name:            "Xưởng thủ công",
		Category:        "Xưởng sản xuất",
		Archetype:       "craft-atelier",
		Description:     "Ảnh chất vải tràn màn, chữ góc dưới trái và dải công đoạn xưởng.",
		PreviewImageURL: "/banners/vietnam-workshop.jpg",
		Tags:            []string{"Xưởng may", "Dải công đoạn", "Thông số chất liệu"},
		SortOrder:       4,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "craft-atelier",
			Theme: ThemeConfigV2{
				BGColor:         "#F3F5F2",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#1A2620",
				AccentColor:     "#1E5C45",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "be-vietnam-pro",
				LogoURL:         "/banners/vietnam-workshop.jpg",
				BannerURL:       "/banners/vietnam-workshop.jpg",
				BrandName:       "Xưởng Dệt Thủ Công",
				Tagline:         "Dệt vải, xử lý co rút và hoàn thiện tại Việt Nam.",
				Radius:          "sm",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "craft-atelier",
				HeaderStyle:      "atelier-framed",
				FooterStyle:      "atelier-footer",
				ContainerWidth:   "standard",
				CardStyle:        "atelier",
				ShowAnnouncement: false,
				AnnouncementText: "",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "ca-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "atelier",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Dệt tại xưởng, kiểm từng mẻ vải",
						Subtitle: "Cotton 220gsm xử lý co rút trước khi lên rập.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Xem sản phẩm xưởng",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ca-h-b1", Type: "feature", Props: FeatureBlockProps{Title: "Dệt kim tròn chủ động", Detail: "Kiểm soát độ khít mặt vải và độ thoáng khí.", Tag: "Xưởng nguồn"}},
					},
				},
				{
					ID:       "ca-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "bar-4",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 4},
					Props: MetricsStripProps{
						Title:    "Chuẩn kiểm định tại xưởng",
						Subtitle: "",
					},
					Blocks: []SectionBlock{
						{ID: "ca-m1", Type: "stat", Props: StatBlockProps{Value: "220gsm", Label: "Định lượng vải", Note: "Đứng phom, không lộ"}},
						{ID: "ca-m2", Type: "stat", Props: StatBlockProps{Value: "< 2%", Label: "Độ co rút", Note: "Giặt thử 3 chu kỳ"}},
						{ID: "ca-m3", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Đổi size tận nhà", Note: "Bưu tá đổi hai chiều"}},
						{ID: "ca-m4", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Kiểm từng chiếc", Note: "Soi mũi kim trước đóng"}},
					},
				},
				{
					ID:       "ca-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-3",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Xuất xưởng trực tiếp",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "ca-banner",
					Type:     "banner",
					Visible:  true,
					Variant:  "split-image",
					Settings: &SectionSettings{Surface: "band", Padding: "normal"},
					Props: BannerProps{
						Title:    "Quy trình dệt và hoàn thiện",
						Subtitle: "Từ sợi bông chải kỹ đến kiểm bền màu trước đóng gói.",
						ImageURL: "/banners/fabric-closeup.jpg",
						CTALabel: "Xem hồ sơ xưởng",
						CTAHref:  "/gioi-thieu",
					},
				},
				{
					ID:       "ca-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "timeline",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal"},
					Props: PoliciesProps{
						Title: "Công đoạn tại xưởng",
						Items: []PolicyItem{
							{Heading: "Chọn sợi & dệt mộc", Detail: "Sợi bông Compact và Modal gỗ sồi chuẩn OEKO-TEX."},
							{Heading: "Giặt thử chống co rút", Detail: "Đo co rút 3 chu kỳ giữ ổn định cổ và chiều dài áo."},
							{Heading: "Cắt rập & may chập phẳng", Detail: "Đường may êm, giảm cọ xát ở vai và nách."},
							{Heading: "Đồng kiểm & đổi 45 ngày", Detail: "Đổi cỡ tận nhà khi sản phẩm còn nguyên tem."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "sidebar-left",
					Columns:       3,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Sản phẩm từ xưởng",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "atelier-story",
					GalleryStyle:  "thumbnails",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "atelier-bag",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Xuất trực tiếp từ xưởng may, bảo hành đường chỉ.",
				},
				Info: InfoPageConfig{
					Layout:             "craft-timeline",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       true,
				},
			},
		},
	}
}

func sportActiveTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "sport-active",
		Slug:            "sport-active",
		Name:            "Thể thao năng động",
		Category:        "Thể thao & Vận động",
		Archetype:       "sport-active",
		Description:     "Nền tối, tiêu đề nén cao giọng poster, ảnh cắt chéo mở theo cuộn.",
		PreviewImageURL: "/banners/campaign-running.jpg",
		Tags:            []string{"Nền tối", "Poster nén", "Thể thao"},
		SortOrder:       5,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "sport-active",
			Theme: ThemeConfigV2{
				BGColor:         "#12161B",
				SurfaceColor:    "#1B222A",
				TextColor:       "#F3F6F9",
				AccentColor:     "#D97706",
				AccentTextColor: "#111418",
				FontFamily:      "be-vietnam-pro",
				LogoURL:         "/banners/campaign-running.jpg",
				BannerURL:       "/banners/campaign-running.jpg",
				BrandName:       "Phòng Chạy Active",
				Tagline:         "Dry-Fit siêu nhẹ, thoát ẩm nhanh cho chạy bộ và tập luyện.",
				Radius:          "none",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "sport-active",
				HeaderStyle:      "bold-dark",
				FooterStyle:      "bold-dark",
				ContainerWidth:   "wide",
				CardStyle:        "sport",
				ShowAnnouncement: true,
				AnnouncementText: "Giao nhanh 2 đến 4 giờ nội thành cho đơn đặt trước 17h",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "sp-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "sport-diagonal",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Nhẹ bước chạy khô thoáng",
						Subtitle: "Dry-Fit 145gsm thoát ẩm nhanh cho cự ly 5K tới 21K.",
						ImageURL: "/banners/campaign-running.jpg",
						CTALabel: "Chọn đồ tập",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "sp-h-b1", Type: "badge", Props: BadgeBlockProps{Label: "145gsm siêu nhẹ", Tone: "accent"}},
						{ID: "sp-h-b2", Type: "badge", Props: BadgeBlockProps{Label: "Co giãn 4 chiều", Tone: "neutral"}},
					},
				},
				{
					ID:       "sp-telemetry",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "telemetry",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 4},
					Props: MetricsStripProps{
						Title:    "Thông số hiệu năng",
						Subtitle: "",
					},
					Blocks: []SectionBlock{
						{ID: "sp-t1", Type: "stat", Props: StatBlockProps{Value: "145g", Label: "Trọng lượng áo", Note: "Không bám mồ hôi"}},
						{ID: "sp-t2", Type: "stat", Props: StatBlockProps{Value: "45 phút", Label: "Khô phơi gió", Note: "Sợi lưới thoáng khí"}},
						{ID: "sp-t3", Type: "stat", Props: StatBlockProps{Value: "4 chiều", Label: "Độ đàn hồi", Note: "Hồi phom tức thì"}},
						{ID: "sp-t4", Type: "stat", Props: StatBlockProps{Value: "Flatlock", Label: "Đường may phẳng", Note: "Giảm ma sát chạy dài"}},
					},
				},
				{
					ID:       "sp-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-4",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 4},
					Props: FeaturedProductsProps{
						Title:      "Trang bị tập luyện",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      8,
					},
				},
				{
					ID:       "sp-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal"},
					Props: VoucherStripProps{
						Title:    "Mã ưu đãi đường chạy",
						Subtitle: "",
						Limit:    4,
					},
				},
				{
					ID:       "sp-banner",
					Type:     "banner",
					Visible:  true,
					Variant:  "band",
					Settings: &SectionSettings{Surface: "band", Padding: "normal"},
					Props: BannerProps{
						Title:    "Trọn bộ chạy 5K đến 21K",
						Subtitle: "Áo Dry-Fit, quần short túi khóa và áo khoác gió.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Xem danh mục",
						CTAHref:  "/san-pham",
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "top-bar",
					Columns:       4,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Trang bị vận động",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "sport-telemetry",
					GalleryStyle:  "thumbnails",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "express-bar",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Giao nhanh nội thành, đổi size 45 ngày.",
				},
				Info: InfoPageConfig{
					Layout:             "athletic-manifesto",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       true,
				},
			},
		},
	}
}

func luxuryMaisonTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "luxury-maison",
		Slug:            "luxury-maison",
		Name:            "Cao cấp Maison",
		Category:        "Cao cấp & Boutique",
		Archetype:       "luxury-maison",
		Description:     "Ảnh đóng khung giữa khoảng lặng rộng, mở chậm, lưới 2 cột lớn.",
		PreviewImageURL: "/banners/fabric-closeup.jpg",
		Tags:            []string{"Boutique", "Lưới 2 cột", "Khung tĩnh"},
		SortOrder:       6,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "luxury-maison",
			Theme: ThemeConfigV2{
				BGColor:         "#151314",
				SurfaceColor:    "#1F1C1E",
				TextColor:       "#F5F2EC",
				AccentColor:     "#D4AF37",
				AccentTextColor: "#141213",
				FontFamily:      "plus-jakarta-sans",
				LogoURL:         "",
				BannerURL:       "/banners/fabric-closeup.jpg",
				BrandName:       "Maison Dệt Kim",
				Tagline:         "Phiên bản giới hạn hoàn thiện theo chuẩn may đo.",
				Radius:          "none",
				Density:         "airy",
			},
			Layout: LayoutConfig{
				Archetype:        "luxury-maison",
				HeaderStyle:      "maison-centered",
				FooterStyle:      "maison-minimal",
				ContainerWidth:   "narrow",
				CardStyle:        "luxury",
				ShowAnnouncement: false,
				AnnouncementText: "",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "lm-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "luxury-framed",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Sự tĩnh lặng của chất liệu",
						Subtitle: "Sản xuất lô nhỏ, kiểm soát từng mũi dệt.",
						ImageURL: "/banners/fabric-closeup.jpg",
						CTALabel: "Khám phá",
						CTAHref:  "/san-pham",
					},
				},
				{
					ID:       "lm-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-2",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Columns: 2},
					Props: FeaturedProductsProps{
						Title:      "Tuyển tập giới hạn",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      4,
					},
				},
				{
					ID:       "lm-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "duo-split",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous"},
					Props: LookbookProps{
						Title:    "Từ phòng mẫu",
						Subtitle: "",
						CTALabel: "Câu chuyện Maison",
						CTAHref:  "/gioi-thieu",
					},
					Blocks: []SectionBlock{
						{ID: "lm-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-office.jpg", Alt: "Phom áo thanh lịch", Caption: "Cấu trúc vai tự nhiên, nẹp giấu chỉ", Aspect: "3:4"}},
						{ID: "lm-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/vietnam-workshop.jpg", Alt: "Không gian hoàn thiện xưởng may", Caption: "Kiểm định thủ công từng thành phẩm", Aspect: "3:4"}},
					},
				},
				{
					ID:       "lm-philosophy",
					Type:     "text",
					Visible:  true,
					Variant:  "monograph",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Align: "center"},
					Props: TextProps{
						Title: "Tuyên ngôn thủ công",
						Body:  "Giá trị nằm ở cảm giác chạm tay ngay lúc mở hộp và sự bền bỉ của phom dáng sau nhiều năm mặc.",
						Align: "center",
					},
				},
				{
					ID:       "lm-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3},
					Props: PoliciesProps{
						Title: "Chăm sóc riêng",
						Items: []PolicyItem{
							{Heading: "Tư vấn cỡ cá nhân", Detail: "Chọn phom theo số đo vai và ngực."},
							{Heading: "Đổi cỡ tận nơi", Detail: "Hỗ trợ ưu tiên hai chiều trong 45 ngày."},
							{Heading: "Hộp bảo vệ sợi", Detail: "Hộp cứng chống ẩm kèm giấy lụa."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "luxury-curated",
					Columns:       2,
					ShowFilters:   true,
					ShowSearch:    false,
					ShowSkuMatrix: false,
					IntroTitle:    "Phòng trưng bày",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "luxury-framed",
					GalleryStyle:  "framed",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "luxury-folio",
					ShowVouchers: true,
					ShowAddons:   false,
					TrustNote:    "Miễn phí hộp cứng và đổi cỡ ưu tiên.",
				},
				Info: InfoPageConfig{
					Layout:             "luxury-maison",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       true,
				},
			},
		},
	}
}

func onePageLaunchTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "one-page-launch",
		Slug:            "one-page-launch",
		Name:            "Một trang (Landing)",
		Category:        "Ra mắt sản phẩm",
		Archetype:       "one-page-launch",
		Description:     "Một sản phẩm giữ chân giữa màn, ba góc độ, nút mua luôn thấy.",
		PreviewImageURL: "/banners/campaign-everyday.jpg",
		Tags:            []string{"Một trang", "Sản phẩm chủ lực", "Nút mua dính"},
		SortOrder:       7,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "one-page-launch",
			Theme: ThemeConfigV2{
				BGColor:         "#F7F9FA",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#162530",
				AccentColor:     "#0E6BA8",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "work-sans",
				LogoURL:         "",
				BannerURL:       "/banners/campaign-everyday.jpg",
				BrandName:       "Bộ Sưu Tập Ra Mắt",
				Tagline:         "Dòng sản phẩm chủ lực trên một mạch cuộn gọn rõ.",
				Radius:          "md",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "one-page-launch",
				HeaderStyle:      "anchor-minimal",
				FooterStyle:      "minimal-row",
				ContainerWidth:   "standard",
				CardStyle:        "minimal",
				ShowAnnouncement: true,
				AnnouncementText: "Đợt mở bán mới, lưu mã giảm giá bên dưới",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "op-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "fullbleed",
					Settings: &SectionSettings{Surface: "band", Padding: "generous"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Một dòng áo cho mọi ngày",
						Subtitle: "Cotton chải kỹ, giữ phom cổ và đổi cỡ 45 ngày.",
						ImageURL: "/banners/campaign-everyday.jpg",
						CTALabel: "Mua ngay",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "op-h-b1", Type: "cta", Props: CTABlockProps{Label: "Chính sách đổi trả", Href: "/chinh-sach", Style: "secondary"}},
					},
				},
				{
					ID:       "op-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "cards-3",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: MetricsStripProps{
						Title:    "Điểm cốt lõi",
						Subtitle: "",
					},
					Blocks: []SectionBlock{
						{ID: "op-m1", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Đổi size tận nhà", Note: "Nhanh gọn hai chiều"}},
						{ID: "op-m2", Type: "stat", Props: StatBlockProps{Value: "1 đến 3 ngày", Label: "Giao toàn quốc", Note: "Xuất trực tiếp tại kho"}},
						{ID: "op-m3", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Đồng kiểm COD", Note: "Kiểm vải trước khi trả"}},
					},
				},
				{
					ID:       "op-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-3",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Chọn phiên bản của bạn",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "op-testimonials",
					Type:     "testimonials",
					Visible:  true,
					Variant:  "grid-3",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: TestimonialsProps{
						Title:    "Nhận xét sau khi mặc",
						Subtitle: "",
					},
					Blocks: []SectionBlock{
						{ID: "op-q1", Type: "quote", Props: QuoteBlockProps{Body: "Vải dày vừa tay, giặt máy hơn mười lần cổ áo vẫn phẳng.", Author: "Nguyễn Minh Quân", Role: "Hà Nội"}},
						{ID: "op-q2", Type: "quote", Props: QuoteBlockProps{Body: "Đổi size M sang L chỉ mất một ngày bưu tá ghé tận nhà.", Author: "Trần Hải Anh", Role: "TP.HCM"}},
						{ID: "op-q3", Type: "quote", Props: QuoteBlockProps{Body: "Màu đúng ảnh, nhận hàng được mở kiểm tra chất vải ngay.", Author: "Lê Hoàng Nam", Role: "Đà Nẵng"}},
					},
				},
				{
					ID:       "op-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "normal"},
					Props: VoucherStripProps{
						Title:    "Mã giảm giá đợt mở bán",
						Subtitle: "",
						Limit:    4,
					},
				},
				{
					ID:       "op-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 2},
					Props: PoliciesProps{
						Title: "Hỏi đáp & Cam kết",
						Items: []PolicyItem{
							{Heading: "Chọn đúng size thế nào?", Detail: "Mỗi mẫu ghi rõ phom mặc và hỗ trợ đổi cỡ 45 ngày."},
							{Heading: "Có kiểm hàng trước không?", Detail: "Mọi đơn COD đều đồng kiểm đúng mẫu, màu và cỡ."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "single-strip",
					Columns:       2,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Danh sách tinh gọn",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "one-page-drawer",
					GalleryStyle:  "thumbnails",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "express-bar",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Giữ tồn thực tế, áp mã gian hàng tự động.",
				},
				Info: InfoPageConfig{
					Layout:             "compact-faq",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       false,
				},
			},
		},
	}
}

func multiSkuCatalogTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "multi-sku-catalog",
		Slug:            "multi-sku-catalog",
		Name:            "Catalog nhiều SKU",
		Category:        "Kho sỉ & Đa biến thể",
		Archetype:       "multi-sku-catalog",
		Description:     "Mục lục chữ từng hàng neo ảnh kèm bảng SKU đặt nhanh.",
		PreviewImageURL: "/banners/vietnam-workshop.jpg",
		Tags:            []string{"Mục lục neo ảnh", "Ma trận SKU", "Đặt nhanh"},
		SortOrder:       8,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "multi-sku-catalog",
			Theme: ThemeConfigV2{
				BGColor:         "#F5F7F9",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#17212B",
				AccentColor:     "#1E40AF",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "ibm-plex-sans",
				LogoURL:         "",
				BannerURL:       "/banners/vietnam-workshop.jpg",
				BrandName:       "Tổng Kho Đa SKU",
				Tagline:         "Tra tồn kho theo màu, size và đặt nhanh từng SKU.",
				Radius:          "sm",
				Density:         "compact",
			},
			Layout: LayoutConfig{
				Archetype:        "multi-sku-catalog",
				HeaderStyle:      "catalog-rail",
				FooterStyle:      "compact-bar",
				ContainerWidth:   "full",
				CardStyle:        "compact-row",
				ShowAnnouncement: true,
				AnnouncementText: "Cập nhật tồn kho thực tế từng SKU, giao toàn quốc",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "sku-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "compact-bar",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact"},
					Props: HeroProps{
						Eyebrow:  "",
						Title:    "Mục lục SKU sẵn kho",
						Subtitle: "Tồn kho theo màu, cỡ và đơn giá cập nhật trực tiếp.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Mở toàn bộ bảng SKU",
						CTAHref:  "/san-pham",
					},
				},
				{
					ID:       "sku-matrix-main",
					Type:     "sku_matrix",
					Visible:  true,
					Variant:  "compact-table",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", MaxItems: 16},
					Props: SkuMatrixProps{
						Title:          "Bảng SKU đặt nhanh",
						Subtitle:       "",
						CategorySlug:   "",
						Limit:          16,
						ShowOutOfStock: true,
					},
				},
				{
					ID:       "sku-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "list-sku",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 4},
					Props: FeaturedProductsProps{
						Title:      "Nhập kho mới nhất",
						Subtitle:   "",
						ProductIDs: []string{},
						Limit:      8,
					},
				},
				{
					ID:       "sku-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "compact-list",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 3},
					Props: PoliciesProps{
						Title: "Chính sách kho vận",
						Items: []PolicyItem{
							{Heading: "Tồn kho thời gian thực", Detail: "Khóa tồn theo từng SKU ngay khi xác nhận đơn."},
							{Heading: "Đóng gói đúng mã SKU", Detail: "Dán nhãn mã phân loại giúp kiểm nhận nhanh."},
							{Heading: "Đổi cỡ 45 ngày", Detail: "Hỗ trợ đổi size linh hoạt cho đơn còn tem."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "sku-table",
					Columns:       4,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: true,
					IntroTitle:    "Catalog đầy đủ theo SKU",
					IntroSubtitle: "",
				},
				PDP: PDPPageConfig{
					Layout:        "sku-order-matrix",
					GalleryStyle:  "compact",
					ShowVouchers:  true,
					ShowSkuTable:  true,
					ShowStoryBand: false,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "compact-table",
					ShowVouchers: true,
					ShowAddons:   false,
					TrustNote:    "Kiểm tra giới hạn tồn từng SKU tự động.",
				},
				Info: InfoPageConfig{
					Layout:             "b2b-catalog-sheet",
					ShowFactoryProfile: true,
					ShowTOC:            true,
					ShowShowcase:       false,
				},
			},
		},
	}
}
