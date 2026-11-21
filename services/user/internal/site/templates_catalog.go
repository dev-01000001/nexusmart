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
		Description:     "Bố cục gallery thoáng rộng, chữ tinh gọn, lưới sản phẩm 3 cột không viền nặng, tập trung tuyệt đối vào phom dáng và chất liệu.",
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
				Tagline:         "Trang phục cơ bản tỉ lệ chuẩn, cắt may tối giản mặc bền nhiều mùa.",
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
						Eyebrow:  "Bộ sưu tập mới",
						Title:    "Phom dáng tinh gọn cho tủ đồ hằng ngày",
						Subtitle: "Chọn lọc cotton chải kỹ và linen tự nhiên, giữ bề mặt vải phẳng mịn và đường may tối giản.",
						ImageURL: "/banners/campaign-everyday.jpg",
						CTALabel: "Xem bộ sưu tập",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "mf-hero-b1", Type: "badge", Props: BadgeBlockProps{Label: "Cotton Compact 220gsm", Tone: "neutral"}},
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
						Subtitle:   "Ba cột sản phẩm thoáng mắt, tôn màu vải và tỷ lệ phom mặc thật.",
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
						Title: "Ít chi tiết hơn, bền dáng hơn",
						Body:  "Mỗi thiết kế được lược bỏ họa tiết thừa để tập trung vào độ đứng vai, độ rủ của sợi vải và khả năng phối linh hoạt từ công sở đến cuối tuần.",
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
							{Heading: "Đổi size 45 ngày", Detail: "Hỗ trợ đổi cỡ tận nơi không phát sinh phí hai chiều."},
							{Heading: "Đồng kiểm khi nhận", Detail: "Kiểm tra đúng phom dáng và chất vải trước khi thanh toán."},
							{Heading: "Minh bạch chất liệu", Detail: "Công bố định lượng GSM và tỷ lệ sợi rõ ràng trên từng mẫu."},
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
					IntroSubtitle: "Lọc nhanh theo danh mục và xem phom mặc ở tỷ lệ ảnh đứng 3:4.",
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
					TrustNote:    "Đóng gói hộp giấy gọn gàng, hỗ trợ đổi size trong 45 ngày.",
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
		Description:     "Đầu trang masthead căn giữa kiểu tạp chí, lookbook bất đối xứng 1 lớn 2 nhỏ, trích dẫn nhấn mạnh và trang sản phẩm ảnh cuộn dọc.",
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
				Tagline:         "Ấn phẩm thời trang công sở và dạo phố kể chuyện qua từng khung hình.",
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
				AnnouncementText: "Số Thu Đông 2026 · Giao tiêu chuẩn toàn quốc 1–3 ngày",
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
						Eyebrow:  "Số phát hành 04 · Thu Đông",
						Title:    "Đường cắt công sở nhẹ vai giữa nhịp sống đô thị",
						Subtitle: "Khám phá cách phối polo dệt kim, quần linen thoáng khí và áo khoác nhẹ trong 4 bối cảnh di chuyển.",
						ImageURL: "/banners/campaign-office.jpg",
						CTALabel: "Đọc & mua theo bộ",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{
							ID:   "ed-hero-b1",
							Type: "quote",
							Props: QuoteBlockProps{
								Body:   "Một chiếc áo mặc suốt 10 tiếng cần nhẹ ở vai và giữ phom cổ đến cuối ngày.",
								Author: "Ban biên tập sản phẩm",
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
						Title:    "Khung hình Lookbook mùa này",
						Subtitle: "Bố cục bất đối xứng 1 khung ảnh chủ đạo kết hợp 2 cận cảnh chất liệu.",
						CTALabel: "Xem toàn bộ danh mục",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ed-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-office.jpg", Alt: "Trang phục công sở gọn gàng", Caption: "Khung 01 · Phom đứng gọn gàng cho ngày làm việc", Aspect: "3:4"}},
						{ID: "ed-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/fabric-closeup.jpg", Alt: "Cận cảnh bề mặt sợi vải", Caption: "Khung 02 · Sợi dệt thoáng mịn giảm nhăn tự nhiên", Aspect: "4:3"}},
						{ID: "ed-lb-3", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/vietnam-workshop.jpg", Alt: "Xưởng may hoàn thiện sản phẩm", Caption: "Khung 03 · Cắt rập và kiểm định tại xưởng nội địa", Aspect: "4:3"}},
					},
				},
				{
					ID:       "ed-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "editorial-asym",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Gợi ý từ ban biên tập",
						Subtitle:   "Các thiết kế tiêu biểu xuất hiện trong ấn phẩm mới nhất.",
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
						Title:    "Ưu đãi dành cho độc giả cửa hàng",
						Subtitle: "Lưu mã giảm giá trực tiếp từ gian hàng trước khi đặt mua.",
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
						Title: "Từ bản phác thảo đến tủ đồ mặc hằng ngày",
						Body:  "Thay vì chạy theo xu hướng ngắn hạn, chúng tôi ghi lại câu chuyện của từng mẻ vải, độ bền màu sau giặt và trải nghiệm thực tế của người mặc trong khí hậu nhiệt đới ẩm.",
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
					IntroSubtitle: "Hai cột lớn giàu khoảng thở kèm ghi chú chất liệu cho từng thiết kế.",
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
					TrustNote:    "Mỗi đơn hàng đi kèm hướng dẫn chăm sóc chất liệu in riêng.",
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
		Description:     "Mật độ hiển thị cao, thanh tiện ích gọn, đưa mã ưu đãi lên ngay đầu trang cùng lưới 4–5 cột tối ưu tốc độ quét và chọn mua.",
		PreviewImageURL: "/banners/campaign-green-fiber.jpg",
		Tags:            []string{"Lưới 4–5 cột", "Mật độ cao", "Chốt đơn nhanh"},
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
				Tagline:         "Hàng cơ bản sẵn kho đủ size S–2XL, cập nhật tồn thực tế từng phút.",
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
				AnnouncementText: "Đơn đặt trước 17h bàn giao vận chuyển trong ngày · Đồng kiểm toàn quốc",
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
						Eyebrow:  "Sẵn kho hôm nay",
						Title:    "Đầy đủ size từ S đến 2XL — giao nhanh từ kho",
						Subtitle: "Xem giá niêm yết, tồn kho thực tế và mã giảm giá của cửa hàng trên cùng một màn hình.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Mở toàn bộ kho hàng",
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
						Title:    "Mã giảm giá áp dụng ngay",
						Subtitle: "Lưu nhanh mã riêng của cửa hàng cho đơn hàng hôm nay.",
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
						Title:      "Toàn bộ sản phẩm bán chạy",
						Subtitle:   "Lưới mật độ cao giúp so sánh giá, màu và tồn kho trong một lượt cuộn.",
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
						Title:          "Dòng áo thun mặc mỗi ngày",
						Subtitle:       "Sẵn số lượng lớn cho mua lẻ và mua theo bộ.",
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
							{Heading: "Xuất kho trong ngày", Detail: "Đơn trước 17h được đóng gói và bàn giao ngay trong ngày."},
							{Heading: "Tồn kho đồng bộ", Detail: "Số lượng hiển thị khớp trực tiếp tồn kho thực tế từng SKU."},
							{Heading: "Đổi size 45 ngày", Detail: "Đổi cỡ tận nơi cho sản phẩm chưa sử dụng còn nguyên tem."},
							{Heading: "Thanh toán linh hoạt", Detail: "Hỗ trợ COD đồng kiểm và chuyển khoản VietQR tự động."},
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
					IntroSubtitle: "Lọc nhanh theo danh mục, từ khóa và sắp xếp theo giá.",
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
					TrustNote:    "Kiểm hàng trước khi thanh toán COD · Tự động trừ mã giảm giá gian hàng.",
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
		Description:     "Tôn vinh quy trình dệt may Việt Nam: dải thông số kỹ thuật vải GSM/co rút, sidebar lọc theo danh mục bên trái và dòng thời gian xưởng.",
		PreviewImageURL: "/banners/vietnam-workshop.jpg",
		Tags:            []string{"Xưởng may", "Sidebar trái", "Thông số chất liệu"},
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
				Tagline:         "Chủ động từ khâu dệt vải, xử lý co rút đến đường kim hoàn thiện tại Việt Nam.",
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
						Eyebrow:  "Trực tiếp từ xưởng dệt Việt Nam",
						Title:    "Từng mẻ vải kiểm định 3 bước trước khi lên rập",
						Subtitle: "Minh bạch định lượng 220gsm, tỷ lệ sợi tự nhiên và tiêu chuẩn đường may 4 kim 6 chỉ ngay trên trang cửa hàng.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Khám phá sản phẩm xưởng",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ca-h-b1", Type: "feature", Props: FeatureBlockProps{Title: "Dệt kim tròn chủ động", Detail: "Kiểm soát độ khít mặt vải và độ thoáng khí theo khí hậu Việt Nam.", Tag: "Xưởng nguồn"}},
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
						Subtitle: "Thông số đo thực tế trên mỗi lô vải trước khi xuất hàng.",
					},
					Blocks: []SectionBlock{
						{ID: "ca-m1", Type: "stat", Props: StatBlockProps{Value: "220gsm", Label: "Định lượng vải", Note: "Đứng phom, không lộ"}},
						{ID: "ca-m2", Type: "stat", Props: StatBlockProps{Value: "< 2%", Label: "Độ co rút sau giặt", Note: "Xử lý pre-shrunk 3 chu kỳ"}},
						{ID: "ca-m3", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Đổi size tận nơi", Note: "Bưu tá nhận đổi hai chiều"}},
						{ID: "ca-m4", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Kiểm QC từng chiếc", Note: "Soi mật độ mũi kim trước đóng gói"}},
					},
				},
				{
					ID:       "ca-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-3",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3},
					Props: FeaturedProductsProps{
						Title:      "Sản phẩm xuất xưởng trực tiếp",
						Subtitle:   "Giá gốc từ xưởng, minh bạch thông số sợi và bảng cỡ.",
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
						Title:    "Tìm hiểu quy trình dệt & hoàn thiện của xưởng",
						Subtitle: "Từ sợi bông chải kỹ đến kiểm bền màu và đóng gói giao tận tay người mặc.",
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
						Title: "Quy trình 4 bước trước khi đến tay bạn",
						Items: []PolicyItem{
							{Heading: "01. Chọn sợi & dệt mộc", Detail: "Chọn sợi bông Compact và Modal gỗ sồi đạt chuẩn OEKO-TEX."},
							{Heading: "02. Giặt thử chống co rút", Detail: "Đo co rút 3 chu kỳ giúp giữ ổn định chiều dài và cổ áo."},
							{Heading: "03. Cắt rập & may chập phẳng", Detail: "Đường may êm mịn, giảm cọ xát ở vai và nách áo."},
							{Heading: "04. Đồng kiểm & hậu mãi 45 ngày", Detail: "Hỗ trợ đổi cỡ tận nhà khi sản phẩm còn nguyên tem."},
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
					IntroTitle:    "Danh mục sản phẩm xưởng",
					IntroSubtitle: "Thanh lọc cố định bên trái giúp chuyển nhanh giữa các dòng chất liệu.",
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
					TrustNote:    "Xuất trực tiếp từ xưởng may, bảo hành đường kim mũi chỉ.",
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
		Description:     "Nền tối tương phản cao, hero chéo đậm nhịp vận động, dải chỉ số hiệu năng (Dry-Fit/thoát ẩm) và bố cục PDP tập trung thông số thi đấu.",
		PreviewImageURL: "/banners/campaign-running.jpg",
		Tags:            []string{"Nền tối", "Hiệu năng", "Thể thao"},
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
				Tagline:         "Trang phục Dry-Fit siêu nhẹ, thoát ẩm nhanh cho chạy bộ và tập luyện cường độ cao.",
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
				AnnouncementText: "Giao nhanh 2–4h nội thành cho đơn thể thao đặt trước 17h",
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
						Eyebrow:  "Dry-Fit 145gsm · Running & Gym",
						Title:    "Nhẹ bước chạy, khô thoáng suốt cung đường nắng nóng",
						Subtitle: "Sợi Micro-mesh thoát ẩm nhanh, đường may phẳng chống ma sát cho cự ly 5K tới bán marathon.",
						ImageURL: "/banners/campaign-running.jpg",
						CTALabel: "Chọn đồ tập ngay",
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
						Title:    "Thông số hiệu năng chất liệu",
						Subtitle: "Kiểm thử thực tế trên đường chạy nhiệt đới.",
					},
					Blocks: []SectionBlock{
						{ID: "sp-t1", Type: "stat", Props: StatBlockProps{Value: "145g", Label: "Trọng lượng áo", Note: "Nhẹ không bám mồ hôi"}},
						{ID: "sp-t2", Type: "stat", Props: StatBlockProps{Value: "45 phút", Label: "Khô phơi gió", Note: "Cấu trúc lỗ thoáng khí"}},
						{ID: "sp-t3", Type: "stat", Props: StatBlockProps{Value: "4-Way", Label: "Độ đàn hồi", Note: "Phục hồi phom tức thì"}},
						{ID: "sp-t4", Type: "stat", Props: StatBlockProps{Value: "Flatlock", Label: "Đường may phẳng", Note: "Giảm ma sát khi chạy dài"}},
					},
				},
				{
					ID:       "sp-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid-4",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 4},
					Props: FeaturedProductsProps{
						Title:      "Trang bị tập luyện bán chạy",
						Subtitle:   "Sẵn size cho cả nam và nữ vận động viên phong trào.",
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
						Title:    "Mã ưu đãi đồng hành đường chạy",
						Subtitle: "Lưu mã giảm giá của cửa hàng cho đơn mua theo bộ chạy bộ.",
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
						Title:    "Phối trọn bộ chạy bộ 5K — 21K",
						Subtitle: "Áo Dry-Fit, quần short 5 inch túi khóa kéo và áo khoác gió gấp gọn.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Xem danh mục chạy bộ",
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
					IntroSubtitle: "Lọc nhanh theo hạng mục tập luyện và chọn cỡ sẵn kho.",
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
					TrustNote:    "Hỗ trợ giao nhanh nội thành và đổi size tận nơi trong 45 ngày.",
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
		Description:     "Thẩm mỹ boutique sang trọng với nền than trầm ánh vàng đồng, khung ảnh viền đôi, lưới trưng bày 2 cột rộng và trang PDP dạng phòng trưng bày.",
		PreviewImageURL: "/banners/fabric-closeup.jpg",
		Tags:            []string{"Boutique", "Lưới 2 cột", "Khung ảnh trưng bày"},
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
				Tagline:         "Phiên bản giới hạn từ sợi tuyển chọn, hoàn thiện tỉ mỉ theo chuẩn may đo.",
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
						Eyebrow:  "Atelier Edition · 2026",
						Title:    "Sự tĩnh lặng của chất liệu thượng hạng",
						Subtitle: "Mỗi phiên bản được sản xuất theo lô nhỏ, kiểm soát từng mũi dệt và đóng gói hộp cứng cao cấp.",
						ImageURL: "/banners/fabric-closeup.jpg",
						CTALabel: "Khám phá bộ sưu tập",
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
						Subtitle:   "Trưng bày hai cột khổ lớn làm nổi bật kết cấu bề mặt sợi.",
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
						Title:    "Góc nhìn từ phòng mẫu",
						Subtitle: "Hai khung hình song song giới thiệu độ rủ và hoàn thiện cổ áo.",
						CTALabel: "Xem câu chuyện Maison",
						CTAHref:  "/gioi-thieu",
					},
					Blocks: []SectionBlock{
						{ID: "lm-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-office.jpg", Alt: "Phom áo thanh lịch", Caption: "Cấu trúc vai tự nhiên, đường nẹp giấu chỉ tinh tế", Aspect: "3:4"}},
						{ID: "lm-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/vietnam-workshop.jpg", Alt: "Không gian hoàn thiện xưởng may", Caption: "Kiểm định thủ công từng thành phẩm trước khi niêm phong", Aspect: "3:4"}},
					},
				},
				{
					ID:       "lm-philosophy",
					Type:     "text",
					Visible:  true,
					Variant:  "monograph",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Align: "center"},
					Props: TextProps{
						Title: "Tuyên ngôn thủ công của Maison",
						Body:  "Chúng tôi tin vào những thiết kế không cần phô trương nhãn hiệu. Giá trị nằm ở cảm giác chạm tay ngay khoảnh khắc mở hộp và sự bền bỉ của phom dáng sau nhiều năm mặc.",
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
						Title: "Dịch vụ chăm sóc riêng",
						Items: []PolicyItem{
							{Heading: "Tư vấn cỡ cá nhân", Detail: "Hỗ trợ chọn phom theo số đo vai, ngực và thói quen mặc."},
							{Heading: "Đổi cỡ tận nơi 45 ngày", Detail: "Xử lý ưu tiên đổi size hai chiều tại địa chỉ khách yêu cầu."},
							{Heading: "Đóng gói bảo vệ", Detail: "Hộp cứng chống ẩm kèm giấy lụa bảo vệ sợi vải tự nhiên."},
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
					IntroTitle:    "Phòng trưng bày sản phẩm",
					IntroSubtitle: "Hai cột khổ lớn với khoảng thở rộng dành cho từng thiết kế.",
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
					TrustNote:    "Miễn phí hộp quà bảo vệ sợi và hỗ trợ đổi cỡ ưu tiên.",
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
		Description:     "Bố cục một trang liên hoàn tối ưu chuyển đổi: điều hướng neo cuộn mượt, sản phẩm tâm điểm, đánh giá khách hàng, mã giảm giá và cam kết trên một mạch dọc.",
		PreviewImageURL: "/banners/campaign-everyday.jpg",
		Tags:            []string{"Một trang", "Landing chuyển đổi", "Đánh giá thực tế"},
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
				Tagline:         "Trải nghiệm trọn vẹn dòng sản phẩm chủ lực trên một trang gọn rõ.",
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
				AnnouncementText: "Đợt mở bán mới · Lưu mã giảm giá ngay bên dưới",
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
						Eyebrow:  "Phiên bản chủ lực",
						Title:    "Một dòng áo làm tốt mọi nhu cầu mặc hằng ngày",
						Subtitle: "Xem nhanh điểm nổi bật, đánh giá khách hàng, ưu đãi và đặt mua ngay mà không phải chuyển qua nhiều trang.",
						ImageURL: "/banners/campaign-everyday.jpg",
						CTALabel: "Chọn sản phẩm ngay",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "op-h-b1", Type: "cta", Props: CTABlockProps{Label: "Xem chính sách đổi trả", Href: "/chinh-sach", Style: "secondary"}},
					},
				},
				{
					ID:       "op-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "cards-3",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: MetricsStripProps{
						Title:    "Vì sao khách hàng quay lại đặt thêm",
						Subtitle: "Số liệu thực tế từ đơn hàng trên cửa hàng.",
					},
					Blocks: []SectionBlock{
						{ID: "op-m1", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Đổi size tận nhà", Note: "Không cần giải thích phức tạp"}},
						{ID: "op-m2", Type: "stat", Props: StatBlockProps{Value: "1–3 ngày", Label: "Giao toàn quốc", Note: "Đóng gói trực tiếp tại kho"}},
						{ID: "op-m3", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Đồng kiểm COD", Note: "Xem vải và cỡ trước khi trả"}},
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
						Subtitle:   "Đủ màu cơ bản và kích cỡ sẵn giao trực tiếp.",
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
						Title:    "Khách hàng nhận xét gì sau khi mặc",
						Subtitle: "Phản hồi thực tế sau nhiều lần giặt máy.",
					},
					Blocks: []SectionBlock{
						{ID: "op-q1", Type: "quote", Props: QuoteBlockProps{Body: "Vải dày vừa tay, giặt máy hơn mười lần cổ áo vẫn đứng phẳng như mới mua.", Author: "Nguyễn Minh Quân", Role: "Đặt 4 chiếc · Hà Nội"}},
						{ID: "op-q2", Type: "quote", Props: QuoteBlockProps{Body: "Đổi từ size M sang L chỉ mất một ngày bưu tá ghé tận nhà rất tiện.", Author: "Trần Hải Anh", Role: "Khách mua lần 2 · TP.HCM"}},
						{ID: "op-q3", Type: "quote", Props: QuoteBlockProps{Body: "Màu đúng ảnh, nhận hàng được mở kiểm tra chất vải ngay trước khi thanh toán.", Author: "Lê Hoàng Nam", Role: "Đơn giao nhanh · Đà Nẵng"}},
					},
				},
				{
					ID:       "op-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "normal"},
					Props: VoucherStripProps{
						Title:    "Nhận mã giảm giá cho đơn đầu tiên",
						Subtitle: "Lưu mã của gian hàng và áp dụng ngay trong giỏ hàng.",
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
						Title: "Câu hỏi thường gặp & Cam kết",
						Items: []PolicyItem{
							{Heading: "Làm sao chọn đúng size?", Detail: "Mỗi trang sản phẩm đều ghi rõ phom mặc và chất liệu; bạn được đổi size miễn phí trong 45 ngày."},
							{Heading: "Có được kiểm hàng trước không?", Detail: "Mọi đơn COD đều hỗ trợ đồng kiểm đúng mẫu, đúng màu và đúng kích cỡ."},
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
					IntroSubtitle: "Mỗi sản phẩm trình bày ngang kèm mô tả ngắn và nút chọn cỡ trực tiếp.",
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
					TrustNote:    "Một bước xác nhận giỏ hàng, giữ tồn kho thực tế và áp mã gian hàng tự động.",
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
		Description:     "Thiết kế chuyên cho gian hàng nhiều màu/size: bảng ma trận SKU hiển thị tồn kho từng biến thể ngay trên trang chủ, danh mục và PDP.",
		PreviewImageURL: "/banners/vietnam-workshop.jpg",
		Tags:            []string{"Ma trận SKU", "Đa biến thể", "Bảng tồn kho"},
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
				Tagline:         "Tra cứu nhanh tồn kho theo màu, kích cỡ và đặt hàng nhiều biến thể chuẩn xác.",
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
				AnnouncementText: "Cập nhật tồn kho trực tiếp theo từng SKU · Xuất hóa đơn & giao toàn quốc",
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
						Eyebrow:  "Catalog trực tuyến",
						Title:    "Tra cứu nhanh hàng trăm biến thể màu & size",
						Subtitle: "Xem bảng ma trận SKU kèm tồn kho trực tiếp ngay bên dưới hoặc chuyển sang trang danh mục có thanh lọc danh mục.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Mở bảng Catalog đầy đủ",
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
						Title:          "Bảng tồn kho nhanh theo SKU",
						Subtitle:       "Hiển thị mã màu, dải size sẵn hàng, đơn giá VND và liên kết đặt mua trực tiếp.",
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
						Title:      "Sản phẩm nhập kho mới nhất",
						Subtitle:   "Dạng thẻ ngang kèm tổng quan biến thể và tồn kho.",
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
						Title: "Chính sách kho vận & đổi trả",
						Items: []PolicyItem{
							{Heading: "Tồn kho thời gian thực", Detail: "Mỗi biến thể màu và size được khóa tồn kho tự động khi đặt đơn."},
							{Heading: "Đóng gói chuẩn mã SKU", Detail: "Dán nhãn mã phân loại rõ ràng giúp kiểm nhận nhanh cho đơn nhiều món."},
							{Heading: "Hỗ trợ đổi cỡ 45 ngày", Detail: "Đổi size linh hoạt theo chính sách minh bạch của gian hàng."},
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
					IntroSubtitle: "Chuyển đổi giữa chế độ bảng SKU chi tiết từng size và chế độ lưới thẻ.",
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
					TrustNote:    "Tự động kiểm tra giới hạn tồn từng SKU và giữ hàng trong giỏ.",
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
