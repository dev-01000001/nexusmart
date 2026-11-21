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

// BuiltInTemplates returns the 8 platform storefront templates covering distinct layout archetypes
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
		Name:            "Thời Trang Tối Giản",
		Category:        "fashion",
		Archetype:       "minimal-fashion",
		Description:     "Bố cục nhiều khoảng thở, lưới 3 cột sắc nét và lookbook nổi bật dành cho thương hiệu thời trang đương đại.",
		PreviewImageURL: "/banners/campaign-everyday.jpg",
		Tags:            []string{"minimal", "fashion", "lookbook", "clean"},
		SortOrder:       1,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "minimal-fashion",
			Theme: ThemeConfigV2{
				BGColor:         "#FAFAFA",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#111827",
				AccentColor:     "#111827",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "inter",
				LogoURL:         "/banners/vietnam-workshop.jpg",
				BannerURL:       "/banners/campaign-everyday.jpg",
				BrandName:       "Studio Tối Giản",
				Tagline:         "Phom dáng tinh gọn và chất liệu tự nhiên cho tủ đồ mặc mỗi ngày",
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
					Variant:  "minimal",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "left"},
					Props: HeroProps{
						Eyebrow:  "BỘ SƯU TẬP MỚI",
						Title:    "Đường cắt tối giản, mặc bền qua nhiều mùa",
						Subtitle: "Thiết kế trung tính tập trung vào chất vải tự nhiên và độ rủ chuẩn xác cho nhịp sống đô thị.",
						ImageURL: "/banners/campaign-everyday.jpg",
						CTALabel: "Khám phá ngay",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "mf-h-b1", Type: "badge", Props: BadgeBlockProps{Label: "Cotton chải kỹ", Tone: "outline"}},
						{ID: "mf-h-b2", Type: "cta", Props: CTABlockProps{Label: "Xem câu chuyện", Href: "/gioi-thieu", Style: "link"}},
					},
				},
				{
					ID:       "mf-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "editorial-grid",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Columns: 3},
					Props: LookbookProps{
						Title:    "Lookbook mặc hằng ngày",
						Subtitle: "Ba bản phối dễ ứng dụng từ phòng làm việc đến cuối tuần.",
						CTALabel: "Xem toàn bộ danh mục",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "mf-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-everyday.jpg", Alt: "Phối đồ tối giản hằng ngày", Caption: "Look 01 — Trắng tinh & Xám đá", Aspect: "portrait"}},
						{ID: "mf-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-office.jpg", Alt: "Phối đồ công sở thanh lịch", Caption: "Look 02 — Thanh lịch công sở", Aspect: "portrait"}},
						{ID: "mf-lb-3", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/fabric-closeup.jpg", Alt: "Cận cảnh bề mặt vải dệt mịn", Caption: "Look 03 — Bề mặt sợi tự nhiên", Aspect: "portrait"}},
					},
				},
				{
					ID:       "mf-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3, MaxItems: 6},
					Props: FeaturedProductsProps{
						Title:      "Thiết kế cốt lõi",
						Subtitle:   "Những món đồ nền tảng dễ kết hợp trong mọi hoàn cảnh.",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "mf-collection",
					Type:     "collection",
					Visible:  true,
					Variant:  "split",
					Settings: &SectionSettings{Surface: "band", Padding: "normal", Columns: 3},
					Props: CollectionProps{
						Title:          "Áo thun nền tảng",
						Subtitle:       "Mặt vải đứng phom, bo cổ giữ dáng qua nhiều chu kỳ giặt.",
						CollectionSlug: "essential-tees",
						CategorySlug:   "ao-thun",
						ImageURL:       "/banners/fabric-closeup.jpg",
						Limit:          4,
					},
				},
				{
					ID:       "mf-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "compact-bar",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 3},
					Props: PoliciesProps{
						Title: "Tiêu chuẩn phục vụ",
						Items: []PolicyItem{
							{Heading: "Đổi cỡ linh hoạt 30 ngày", Detail: "Hỗ trợ đổi size hai chiều tận nhà với sản phẩm còn tem mác."},
							{Heading: "Đóng gói tối giản", Detail: "Hộp giấy tái chế không nhựa dùng một lần."},
							{Heading: "Giao hàng toàn quốc", Detail: "Đóng gói trong ngày và theo dõi hành trình trực tiếp."},
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
					IntroTitle:    "Bộ sưu tập",
					IntroSubtitle: "Lọc nhanh theo danh mục và tầm giá để tìm thiết kế vừa vặn.",
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
					TrustNote:    "Miễn phí đổi cỡ trong 30 ngày và thanh toán an toàn qua NexusMart.",
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
		Name:            "Tạp Chí Editorial",
		Category:        "editorial",
		Archetype:       "editorial",
		Description:     "Nhịp điệu kể chuyện như tạp chí thời trang với tiêu đề giữa, khối trích dẫn, mosaic ảnh và trang giới thiệu dạng bài luận.",
		PreviewImageURL: "/banners/campaign-office.jpg",
		Tags:            []string{"editorial", "magazine", "storytelling", "lookbook"},
		SortOrder:       2,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "editorial",
			Theme: ThemeConfigV2{
				BGColor:         "#F6F1E9",
				SurfaceColor:    "#FFFDF9",
				TextColor:       "#1F1A17",
				AccentColor:     "#9A3412",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "plus-jakarta-sans",
				LogoURL:         "/banners/campaign-office.jpg",
				BannerURL:       "/banners/campaign-office.jpg",
				BrandName:       "Tạp Chí Sợi Việt",
				Tagline:         "Chuyện nghề dệt may, chất liệu bản địa và nhịp mặc chậm có chủ đích",
				Radius:          "sm",
				Density:         "airy",
			},
			Layout: LayoutConfig{
				Archetype:        "editorial",
				HeaderStyle:      "centered-masthead",
				FooterStyle:      "editorial-cols",
				ContainerWidth:   "narrow",
				CardStyle:        "editorial",
				ShowAnnouncement: true,
				AnnouncementText: "Ấn phẩm Thu — Đông: Khám phá bộ sưu tập sợi tự nhiên dệt tại Việt Nam",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "ed-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "editorial",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: HeroProps{
						Eyebrow:  "SỐ ĐẶC BIỆT",
						Title:    "Từ sợi bông nguyên bản đến phom áo vượt thời gian",
						Subtitle: "Một hành trình nhìn gần hơn vào chất liệu, màu nhuộm trầm ấm và những người thợ đứng sau từng đường kim.",
						ImageURL: "/banners/campaign-office.jpg",
						CTALabel: "Đọc & Mua sắm",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ed-h-b1", Type: "badge", Props: BadgeBlockProps{Label: "Ấn phẩm 04", Tone: "accent"}},
						{ID: "ed-h-b2", Type: "cta", Props: CTABlockProps{Label: "Câu chuyện xưởng", Href: "/gioi-thieu", Style: "outline"}},
					},
				},
				{
					ID:       "ed-story",
					Type:     "text",
					Visible:  true,
					Variant:  "editorial",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Align: "left"},
					Props: TextProps{
						Title: "Mặc ít hơn, chọn kỹ hơn",
						Body:  "Chúng tôi thiết kế mỗi bộ sưu tập như một chương truyện nối tiếp thay vì chạy theo chu kỳ giảm giá ngắn hạn. Từng chất liệu được thử nghiệm trực tiếp trong khí hậu nhiệt đới để giữ độ thoáng và bền màu theo năm tháng.",
						Align: "left",
					},
					Blocks: []SectionBlock{
						{ID: "ed-st-1", Type: "heading", Props: HeadingBlockProps{Text: "Ghi chép từ phòng rập", Level: 3}},
						{ID: "ed-st-2", Type: "paragraph", Props: ParagraphBlockProps{Text: "Mỗi mẫu rập đi qua ít nhất 3 vòng mặc thử trước khi đưa lên dây chuyền cắt chính thức."}},
						{ID: "ed-st-3", Type: "quote", Props: QuoteBlockProps{Body: "Trang phục tốt không phô trương, nó đồng hành lặng lẽ suốt cả ngày dài.", Author: "Minh Khuê", Role: "Giám đốc thiết kế"}},
					},
				},
				{
					ID:       "ed-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "mosaic",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Columns: 3},
					Props: LookbookProps{
						Title:    "Khung hình chất liệu",
						Subtitle: "Ánh sáng tự nhiên tôn lên kết cấu dệt mộc mạc của từng sản phẩm.",
						CTALabel: "Khám phá danh mục",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ed-lb-1", Type: "image", Props: ImageBlockProps{ImageURL: "/atmosphere/cotton-boll-harvest.jpg", Alt: "Bông tự nhiên thu hoạch", Caption: "Chương I — Nguyên liệu thuần khiết", Aspect: "landscape"}},
						{ID: "ed-lb-2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/vietnam-workshop.jpg", Alt: "Xưởng may tại Việt Nam", Caption: "Chương II — Bàn tay người thợ", Aspect: "portrait"}},
						{ID: "ed-lb-3", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-green-fiber.jpg", Alt: "Trang phục sợi thực vật", Caption: "Chương III — Nhịp mặc hàng ngày", Aspect: "square"}},
					},
				},
				{
					ID:       "ed-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "editorial",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 2, MaxItems: 4},
					Props: FeaturedProductsProps{
						Title:      "Tuyển chọn của biên tập viên",
						Subtitle:   "Bốn thiết kế đại diện cho tinh thần bộ sưu tập mùa này.",
						ProductIDs: []string{},
						Limit:      4,
					},
				},
				{
					ID:       "ed-testimonials",
					Type:     "testimonials",
					Visible:  true,
					Variant:  "editorial-quotes",
					Settings: &SectionSettings{Surface: "band", Padding: "normal", Columns: 2},
					Props: TestimonialsProps{
						Title:    "Cảm nhận từ người mặc",
						Subtitle: "Những chia sẻ thực tế sau nhiều tháng đồng hành cùng sản phẩm.",
					},
					Blocks: []SectionBlock{
						{ID: "ed-q1", Type: "quote", Props: QuoteBlockProps{Body: "Vải mặc rất mát, giặt máy nhiều lần cổ áo vẫn phẳng và không bị chảy phom.", Author: "Thu Hà", Role: "Kiến trúc sư, TP.HCM"}},
						{ID: "ed-q2", Type: "quote", Props: QuoteBlockProps{Body: "Tông màu trầm ấm dễ phối đồ, giao hàng chỉn chu từ hộp đến giấy gói.", Author: "Quang Huy", Role: "Nhiếp ảnh gia, Hà Nội"}},
					},
				},
				{
					ID:       "ed-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "minimal",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Đặc quyền dành cho độc giả",
						Subtitle: "Lưu mã ưu đãi của gian hàng trước khi hoàn tất đơn.",
						Limit:    3,
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
					IntroTitle:    "Danh mục tuyển chọn",
					IntroSubtitle: "Trình bày khổ lớn 2 cột giúp quan sát rõ phom dáng và chất liệu từng thiết kế.",
					Sections: []SectionV2{
						{
							ID:      "ed-cat-banner",
							Type:    "banner",
							Visible: true,
							Variant: "framed",
							Props: BannerProps{
								Title:    "Cần tư vấn phối đồ theo vóc dáng?",
								Subtitle: "Nhắn trực tiếp cho đội ngũ tư vấn ngay trên cửa hàng để chọn đúng kích cỡ.",
								ImageURL: "/banners/fabric-closeup.jpg",
								CTALabel: "Xem hướng dẫn",
								CTAHref:  "/gioi-thieu",
							},
						},
					},
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
					TrustNote:    "Mỗi đơn hàng được kiểm tra thủ công trước khi rời xưởng.",
				},
				Info: InfoPageConfig{
					Layout:             "editorial-essay",
					ShowFactoryProfile: true,
					ShowTOC:            true,
					ShowShowcase:       true,
					Sections: []SectionV2{
						{
							ID:      "ed-info-quotes",
							Type:    "testimonials",
							Visible: true,
							Variant: "editorial-quotes",
							Props: TestimonialsProps{
								Title:    "Dấu ấn cộng đồng",
								Subtitle: "Phản hồi từ khách hàng đã ghé thăm và trải nghiệm.",
							},
							Blocks: []SectionBlock{
								{ID: "ed-iq-1", Type: "quote", Props: QuoteBlockProps{Body: "Đọc câu chuyện xưởng xong mới hiểu vì sao đường may phẳng và mịn đến vậy.", Author: "Ngọc Anh", Role: "Khách hàng thân thiết"}},
							},
						},
					},
				},
			},
		},
	}
}

func denseGridTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "dense-grid",
		Slug:            "dense-grid",
		Name:            "Lưới Sản Phẩm Dày",
		Category:        "retail",
		Archetype:       "dense-grid",
		Description:     "Tối ưu mật độ hiển thị cho gian hàng nhiều mặt hàng: voucher đầu trang, lưới 4 cột gọn gàng, bộ lọc cạnh trái và bảng thông số nhanh.",
		PreviewImageURL: "/banners/fabric-closeup.jpg",
		Tags:            []string{"dense", "retail", "conversion", "sidebar"},
		SortOrder:       3,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "dense-grid",
			Theme: ThemeConfigV2{
				BGColor:         "#F1F5F9",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#0F172A",
				AccentColor:     "#1D4ED8",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "work-sans",
				LogoURL:         "/banners/vietnam-workshop.jpg",
				BannerURL:       "/banners/campaign-everyday.jpg",
				BrandName:       "Kho Hàng Tiêu Chuẩn",
				Tagline:         "Mua sắm nhanh, giá xưởng rõ ràng và sẵn kho đủ màu đủ size",
				Radius:          "sm",
				Density:         "compact",
			},
			Layout: LayoutConfig{
				Archetype:        "dense-grid",
				HeaderStyle:      "utility-compact",
				FooterStyle:      "compact-bar",
				ContainerWidth:   "wide",
				CardStyle:        "dense",
				ShowAnnouncement: true,
				AnnouncementText: "Freeship đơn từ 199.000đ • Giao trong ngày nội thành • Đổi trả 30 ngày",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "dg-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "inline-bar",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Săn mã giảm giá ngay",
						Subtitle: "Lưu nhanh các mã đang hoạt động để tự động áp dụng ở giỏ hàng.",
						Limit:    6,
					},
				},
				{
					ID:       "dg-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "dense",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 4, MaxItems: 12},
					Props: FeaturedProductsProps{
						Title:      "Bán chạy nhất tuần này",
						Subtitle:   "Hiển thị gọn 4 cột giúp so sánh nhanh giá, màu sắc và tình trạng tồn kho.",
						ProductIDs: []string{},
						Limit:      8,
					},
				},
				{
					ID:       "dg-collection",
					Type:     "collection",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 4},
					Props: CollectionProps{
						Title:          "Nhóm áo mặc hằng ngày",
						Subtitle:       "Chọn nhanh theo màu cơ bản và kích cỡ có sẵn.",
						CollectionSlug: "daily-essentials",
						CategorySlug:   "ao-thun",
						ImageURL:       "/banners/campaign-everyday.jpg",
						Limit:          8,
					},
				},
				{
					ID:       "dg-banner",
					Type:     "banner",
					Visible:  true,
					Variant:  "promo-strip",
					Settings: &SectionSettings{Surface: "band", Padding: "compact"},
					Props: BannerProps{
						Title:    "Mua theo combo tiết kiệm đến 20%",
						Subtitle: "Áp dụng đồng thời với voucher gian hàng khi mua từ 3 sản phẩm trở lên.",
						ImageURL: "/banners/fabric-closeup.jpg",
						CTALabel: "Xem tất cả",
						CTAHref:  "/san-pham",
					},
				},
				{
					ID:       "dg-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "inline-bar",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 4},
					Props: MetricsStripProps{
						Title:    "Chỉ số vận hành",
						Subtitle: "Cập nhật trực tiếp từ hệ thống kho gian hàng.",
					},
					Blocks: []SectionBlock{
						{ID: "dg-m1", Type: "stat", Props: StatBlockProps{Value: "99.4%", Label: "Giao đúng hẹn", Note: "Theo đơn 90 ngày gần nhất"}},
						{ID: "dg-m2", Type: "stat", Props: StatBlockProps{Value: "< 2 giờ", Label: "Đóng gói xuất kho", Note: "Đơn đặt trước 17:00"}},
						{ID: "dg-m3", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Đổi size tận nơi", Note: "Hai chiều tiện lợi"}},
						{ID: "dg-m4", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Ảnh thật tại kho", Note: "Đồng kiểm khi nhận"}},
					},
				},
				{
					ID:       "dg-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "compact-bar",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 4},
					Props: PoliciesProps{
						Title: "Cam kết mua nhanh an tâm",
						Items: []PolicyItem{
							{Heading: "Sẵn kho số lượng lớn", Detail: "Cập nhật tồn kho thực theo từng SKU màu và size."},
							{Heading: "Kiểm hàng trước thanh toán", Detail: "Hỗ trợ đồng kiểm toàn quốc với mọi đơn COD."},
							{Heading: "Đổi trả tự động", Detail: "Tạo yêu cầu trực tiếp trong chi tiết đơn hàng."},
							{Heading: "Hỗ trợ 7 ngày/tuần", Detail: "Tư vấn size và đơn hàng qua chat trực tiếp."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "sidebar-left",
					Columns:       4,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Tất cả sản phẩm",
					IntroSubtitle: "Bộ lọc nhanh bên trái giúp thu hẹp theo danh mục, kích cỡ và mức giá.",
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
					TrustNote:    "Giá hiển thị đã bao gồm kiểm tra chất lượng và đóng gói chống ẩm.",
				},
				Info: InfoPageConfig{
					Layout:             "compact-faq",
					ShowFactoryProfile: false,
					ShowTOC:            true,
					ShowShowcase:       false,
					Sections: []SectionV2{
						{
							ID:      "dg-info-faq",
							Type:    "policies",
							Visible: true,
							Variant: "list",
							Props: PoliciesProps{
								Title: "Câu hỏi thường gặp",
								Items: []PolicyItem{
									{Heading: "Bao lâu nhận được hàng?", Detail: "Nội thành 2–4 giờ, liên tỉnh 1–3 ngày làm việc."},
									{Heading: "Đổi size có mất phí không?", Detail: "Miễn phí đổi size lần đầu cho mọi đơn hàng đủ điều kiện."},
								},
							},
						},
					},
				},
			},
		},
	}
}

func craftAtelierTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "craft-atelier",
		Slug:            "craft-atelier",
		Name:            "Xưởng Thủ Công",
		Category:        "craft",
		Archetype:       "craft-atelier",
		Description:     "Tôn vinh quy trình sản xuất bản địa, chỉ số kiểm định chất liệu, dòng thời gian xưởng nghề và tông màu sợi tự nhiên ấm áp.",
		PreviewImageURL: "/banners/vietnam-workshop.jpg",
		Tags:            []string{"craft", "atelier", "workshop", "sustainable"},
		SortOrder:       4,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "craft-atelier",
			Theme: ThemeConfigV2{
				BGColor:         "#EFF2EA",
				SurfaceColor:    "#FBFDF8",
				TextColor:       "#1B261E",
				AccentColor:     "#2E5A3C",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "ibm-plex-sans",
				LogoURL:         "/banners/vietnam-workshop.jpg",
				BannerURL:       "/banners/campaign-green-fiber.jpg",
				BrandName:       "Xưởng Dệt Thủ Công",
				Tagline:         "Dệt may minh bạch nguồn sợi, kiểm định 3 bước trực tiếp tại xưởng Việt",
				Radius:          "md",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "craft-atelier",
				HeaderStyle:      "atelier-framed",
				FooterStyle:      "atelier-footer",
				ContainerWidth:   "standard",
				CardStyle:        "atelier",
				ShowAnnouncement: true,
				AnnouncementText: "Giá trực tiếp từ xưởng • Minh bạch định lượng sợi và lô sản xuất",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "ca-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "framed",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "left"},
					Props: HeroProps{
						Eyebrow:  "XƯỞNG MAY BẢN ĐỊA",
						Title:    "Từng đường kim kiểm định trước khi đóng gói",
						Subtitle: "Chủ động từ chọn sợi thực vật, dệt kim tròn đến giặt enzyme xử lý co rút ngay tại xưởng nội địa.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Xem sản phẩm xưởng",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ca-h-1", Type: "badge", Props: BadgeBlockProps{Label: "Trực tiếp từ xưởng", Tone: "accent"}},
						{ID: "ca-h-2", Type: "feature", Props: FeatureBlockProps{Title: "Giặt enzyme sinh học", Detail: "Chạm dịu ngay từ lần mặc đầu tiên, hạn chế co rút dưới 2%.", Tag: "Bước 02"}},
					},
				},
				{
					ID:       "ca-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: MetricsStripProps{
						Title:    "Hồ sơ kỹ thuật xưởng",
						Subtitle: "Các chỉ tiêu đo kiểm áp dụng trên 100% lô vải trước khi xuất xưởng.",
					},
					Blocks: []SectionBlock{
						{ID: "ca-m1", Type: "stat", Props: StatBlockProps{Value: "220 gsm", Label: "Định lượng vải chuẩn", Note: "Đứng phom nhưng vẫn thoáng khí"}},
						{ID: "ca-m2", Type: "stat", Props: StatBlockProps{Value: "3 bước", Label: "Kiểm định QC", Note: "Đo co rút, mật độ mũi kim, bền màu"}},
						{ID: "ca-m3", Type: "stat", Props: StatBlockProps{Value: "45 ngày", Label: "Bảo hành đổi cỡ", Note: "Hỗ trợ tận nơi hai chiều"}},
					},
				},
				{
					ID:       "ca-collection",
					Type:     "collection",
					Visible:  true,
					Variant:  "split",
					Settings: &SectionSettings{Surface: "band", Padding: "normal", Columns: 3},
					Props: CollectionProps{
						Title:          "Dòng sợi thực vật thoáng nhẹ",
						Subtitle:       "Lanh pha bông, sợi tre thiên nhiên và cotton pique chải kỹ.",
						CollectionSlug: "natural-fibers",
						CategorySlug:   "ao-polo",
						ImageURL:       "/banners/campaign-green-fiber.jpg",
						Limit:          6,
					},
				},
				{
					ID:       "ca-craft-notes",
					Type:     "text",
					Visible:  true,
					Variant:  "framed",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Align: "left"},
					Props: TextProps{
						Title: "Quy trình làm ra một chiếc áo tại xưởng",
						Body:  "Mỗi công đoạn từ dệt mộc, nhuộm hoàn tất đến cắt rập và may chập phẳng đều được ghi nhận theo mã lô. Khách hàng luôn biết rõ thành phần sợi, định lượng và hướng dẫn bảo quản đi kèm.",
						Align: "left",
					},
					Blocks: []SectionBlock{
						{ID: "ca-f1", Type: "feature", Props: FeatureBlockProps{Title: "Chọn sợi chải kỹ", Detail: "Loại bỏ xơ ngắn giúp bề mặt vải mịn và hạn chế xù lông.", Tag: "01"}},
						{ID: "ca-f2", Type: "feature", Props: FeatureBlockProps{Title: "Cắt rập chuẩn dáng Việt", Detail: "Cân chỉnh độ xuôi vai và vòng nách cho cảm giác cử động thoải mái.", Tag: "02"}},
						{ID: "ca-f3", Type: "feature", Props: FeatureBlockProps{Title: "Soi bàn sáng từng chiếc", Detail: "Kiểm tra đường may cổ áo, gấu áo và nẹp trước khi gấp hộp.", Tag: "03"}},
					},
				},
				{
					ID:       "ca-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3, MaxItems: 6},
					Props: FeaturedProductsProps{
						Title:      "Sản phẩm chủ lực của xưởng",
						Subtitle:   "Các thiết kế được hoàn thiện trực tiếp và luôn giữ ổn định chất lượng qua từng đợt.",
						ProductIDs: []string{},
						Limit:      6,
					},
				},
				{
					ID:       "ca-testimonials",
					Type:     "testimonials",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 2},
					Props: TestimonialsProps{
						Title:    "Đánh giá từ khách mặc lâu năm",
						Subtitle: "Trải nghiệm thực tế về độ bền phom và cảm giác chất liệu.",
					},
					Blocks: []SectionBlock{
						{ID: "ca-q1", Type: "quote", Props: QuoteBlockProps{Body: "Áo polo pique cổ đứng rất đẹp, mặc đi làm cả ngày mùa hè vẫn dễ chịu.", Author: "Hoàng Minh", Role: "Đà Nẵng"}},
						{ID: "ca-q2", Type: "quote", Props: QuoteBlockProps{Body: "Thích cách xưởng ghi rõ định lượng gsm và chăm chút từng đường may phẳng.", Author: "Bảo Trâm", Role: "Hải Phòng"}},
					},
				},
				{
					ID:       "ca-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "timeline",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 3},
					Props: PoliciesProps{
						Title: "Cam kết hậu mãi tại xưởng",
						Items: []PolicyItem{
							{Heading: "Nguồn sợi truy xuất", Detail: "Công bố rõ tỷ lệ sợi, định lượng và hướng dẫn giặt ủi trên nhãn."},
							{Heading: "Đổi size tận nhà 45 ngày", Detail: "Bưu tá nhận lại và giao size mới trong một lượt."},
							{Heading: "Sửa chữa nhỏ miễn phí", Detail: "Hỗ trợ gia cố đường chỉ cho sản phẩm mua tại gian hàng."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "top-bar",
					Columns:       3,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: false,
					IntroTitle:    "Sản phẩm từ xưởng",
					IntroSubtitle: "Chọn lọc theo dòng chất liệu và nhu cầu sử dụng hằng ngày.",
					Sections: []SectionV2{
						{
							ID:      "ca-cat-metrics",
							Type:    "metrics_strip",
							Visible: true,
							Variant: "inline-bar",
							Props: MetricsStripProps{
								Title:    "Minh bạch xuất xưởng",
								Subtitle: "Mọi sản phẩm trên gian hàng đều sản xuất trực tiếp không qua trung gian.",
							},
							Blocks: []SectionBlock{
								{ID: "ca-cm1", Type: "stat", Props: StatBlockProps{Value: "100%", Label: "Sản xuất nội địa", Note: "Kiểm định từng lô"}},
								{ID: "ca-cm2", Type: "stat", Props: StatBlockProps{Value: "< 2%", Label: "Độ co rút tối đa", Note: "Đã qua giặt thử"}},
							},
						},
					},
				},
				PDP: PDPPageConfig{
					Layout:        "atelier-story",
					GalleryStyle:  "framed",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "atelier-bag",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Đóng gói trực tiếp tại xưởng cùng thẻ hướng dẫn chăm sóc chất liệu.",
				},
				Info: InfoPageConfig{
					Layout:             "craft-timeline",
					ShowFactoryProfile: true,
					ShowTOC:            true,
					ShowShowcase:       true,
					Sections: []SectionV2{
						{
							ID:      "ca-info-story",
							Type:    "text",
							Visible: true,
							Variant: "framed",
							Props: TextProps{
								Title: "Dấu mốc hình thành xưởng",
								Body:  "Khởi đầu từ một chuyền dệt gia đình, chúng tôi nâng cấp dần hệ thống máy dệt tròn, bàn cắt chuẩn và phòng kiểm định chất liệu để giữ chất lượng ổn định quanh năm.",
								Align: "left",
							},
						},
					},
				},
			},
		},
	}
}

func sportActiveTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "sport-active",
		Slug:            "sport-active",
		Name:            "Thể Thao Năng Động",
		Category:        "sport",
		Archetype:       "sport-active",
		Description:     "Nền tối tương phản cao, dải thông số hiệu năng (telemetry), ảnh hành động toàn chiều rộng và luồng thanh toán nhanh.",
		PreviewImageURL: "/banners/campaign-running.jpg",
		Tags:            []string{"sport", "dark", "activewear", "telemetry"},
		SortOrder:       5,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "sport-active",
			Theme: ThemeConfigV2{
				BGColor:         "#11151A",
				SurfaceColor:    "#1B222A",
				TextColor:       "#F8FAFC",
				AccentColor:     "#F59E0B",
				AccentTextColor: "#11151A",
				FontFamily:      "be-vietnam-pro",
				LogoURL:         "/banners/campaign-running.jpg",
				BannerURL:       "/banners/campaign-running.jpg",
				BrandName:       "Đường Chạy Việt",
				Tagline:         "Trang phục vận động siêu nhẹ, thoát ẩm nhanh cho thời tiết nhiệt đới",
				Radius:          "sm",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "sport-active",
				HeaderStyle:      "bold-dark",
				FooterStyle:      "bold-dark",
				ContainerWidth:   "wide",
				CardStyle:        "sport",
				ShowAnnouncement: true,
				AnnouncementText: "DRY-FIT 145GSM • Giao hỏa tốc 2–4 giờ nội thành • Đổi size miễn phí",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "sp-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "bold-sport",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "left"},
					Props: HeroProps{
						Eyebrow:  "HIỆU NĂNG VẬN ĐỘNG",
						Title:    "Nhẹ từng bước chạy, khô thoáng suốt cung đường",
						Subtitle: "Sợi Micro-mesh tái chế kết hợp đường may chập phẳng 4 kim 6 chỉ giúp giảm tối đa ma sát khi tập cường độ cao.",
						ImageURL: "/banners/campaign-running.jpg",
						CTALabel: "Mua đồ tập ngay",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "sp-h-b1", Type: "badge", Props: BadgeBlockProps{Label: "Dry-Fit 145gsm", Tone: "accent"}},
						{ID: "sp-h-s1", Type: "stat", Props: StatBlockProps{Value: "45 phút", Label: "Khô nhanh phơi gió", Note: "Thử nghiệm độ ẩm thực tế"}},
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
						Subtitle: "Đo kiểm trên từng lô vải thể thao trước khi cắt may.",
					},
					Blocks: []SectionBlock{
						{ID: "sp-t1", Type: "stat", Props: StatBlockProps{Value: "145 gsm", Label: "Trọng lượng siêu nhẹ", Note: "Không bám dính mồ hôi"}},
						{ID: "sp-t2", Type: "stat", Props: StatBlockProps{Value: "4 chiều", Label: "Độ đàn hồi", Note: "Cử động tự do mọi góc"}},
						{ID: "sp-t3", Type: "stat", Props: StatBlockProps{Value: "4K6C", Label: "May chập phẳng", Note: "Hạn chế cọ xát vùng nách"}},
						{ID: "sp-t4", Type: "stat", Props: StatBlockProps{Value: "UPF 40+", Label: "Chống nắng nhẹ", Note: "Phù hợp chạy ngoài trời"}},
					},
				},
				{
					ID:       "sp-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "spotlight",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal", Columns: 4, MaxItems: 8},
					Props: FeaturedProductsProps{
						Title:      "Trang bị bán chạy cho Runner & Gym",
						Subtitle:   "Tối ưu từ buổi chạy 5K sáng sớm tới các buổi tập dài cuối tuần.",
						ProductIDs: []string{},
						Limit:      8,
					},
				},
				{
					ID:       "sp-banner",
					Type:     "banner",
					Visible:  true,
					Variant:  "full-bleed",
					Settings: &SectionSettings{Surface: "band", Padding: "normal"},
					Props: BannerProps{
						Title:    "Short chạy bộ 5 inch có túi khóa kéo sau lưng",
						Subtitle: "Giữ chắc điện thoại, gel năng lượng và chìa khóa mà không rung lắc.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Xem quần short",
						CTAHref:  "/san-pham?category=quan-short",
					},
				},
				{
					ID:       "sp-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "horizontal-film",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: LookbookProps{
						Title:    "Trên đường chạy thực tế",
						Subtitle: "Thử nghiệm cùng các câu lạc bộ chạy bộ nội địa.",
						CTALabel: "Xem bộ sưu tập",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "sp-lb1", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-running.jpg", Alt: "Chạy bộ sáng sớm", Caption: "Tempo Run — Áo Dry-Fit thoáng khí", Aspect: "wide"}},
						{ID: "sp-lb2", Type: "image", Props: ImageBlockProps{ImageURL: "/banners/campaign-green-fiber.jpg", Alt: "Chất liệu tái chế", Caption: "Sợi Micro-mesh nhẹ mát", Aspect: "wide"}},
						{ID: "sp-lb3", Type: "image", Props: ImageBlockProps{ImageURL: "/atmosphere/cotton-twill-slate.jpg", Alt: "Phục hồi sau buổi tập", Caption: "Đồ mặc thư giãn sau tập", Aspect: "wide"}},
					},
				},
				{
					ID:       "sp-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "tickets",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Mã ưu đãi cho đội nhóm & cá nhân",
						Subtitle: "Lưu mã trước khi thanh toán để nhận chiết khấu trực tiếp.",
						Limit:    4,
					},
				},
				{
					ID:       "sp-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 3},
					Props: PoliciesProps{
						Title: "Hỗ trợ vận động viên",
						Items: []PolicyItem{
							{Heading: "Giao nhanh 2–4 giờ", Detail: "Đáp ứng kịp lịch tập và giải chạy cuối tuần tại nội thành."},
							{Heading: "Đổi size không rườm rà", Detail: "Hỗ trợ đổi form ôm hay rộng trong vòng 45 ngày."},
							{Heading: "Tư vấn chọn cỡ nhanh", Detail: "Đội ngũ phản hồi theo chiều cao, cân nặng và thói quen vận động."},
						},
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
					IntroTitle:    "Trang bị thể thao",
					IntroSubtitle: "Lọc nhanh theo dòng vận động, chất liệu và tầm giá.",
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
					TrustNote:    "Đóng gói chống ẩm và xuất kho ngay trong ngày cho đơn trước 17:00.",
				},
				Info: InfoPageConfig{
					Layout:             "athletic-manifesto",
					ShowFactoryProfile: true,
					ShowTOC:            false,
					ShowShowcase:       true,
					Sections: []SectionV2{
						{
							ID:      "sp-info-metrics",
							Type:    "metrics_strip",
							Visible: true,
							Variant: "telemetry",
							Props: MetricsStripProps{
								Title:    "Kiểm thử thực địa",
								Subtitle: "Mỗi mẫu áo chạy đều vượt qua 200 km chạy thử trước khi ra mắt.",
							},
							Blocks: []SectionBlock{
								{ID: "sp-im1", Type: "stat", Props: StatBlockProps{Value: "200+ km", Label: "Chạy thử nghiệm", Note: "Đánh giá độ bền đường may"}},
								{ID: "sp-im2", Type: "stat", Props: StatBlockProps{Value: "35°C", Label: "Kiểm thử nhiệt ẩm", Note: "Tối ưu cho khí hậu Việt Nam"}},
							},
						},
					},
				},
			},
		},
	}
}

func luxuryMaisonTemplate() PlatformTemplate {
	return PlatformTemplate{
		ID:              "luxury-maison",
		Slug:            "luxury-maison",
		Name:            "Cao Cấp Maison",
		Category:        "luxury",
		Archetype:       "luxury-maison",
		Description:     "Không gian tối sang trọng với điểm nhấn vàng ánh kim, khung ảnh lookbook rộng rãi và danh mục tuyển chọn 2 cột.",
		PreviewImageURL: "/atmosphere/jacquard-loom-poster.jpg",
		Tags:            []string{"luxury", "maison", "dark", "curated"},
		SortOrder:       6,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "luxury-maison",
			Theme: ThemeConfigV2{
				BGColor:         "#1D1417",
				SurfaceColor:    "#291D21",
				TextColor:       "#F9F2EC",
				AccentColor:     "#E6B85C",
				AccentTextColor: "#1D1417",
				FontFamily:      "plus-jakarta-sans",
				LogoURL:         "/atmosphere/cotton-twill-slate.jpg",
				BannerURL:       "/atmosphere/jacquard-loom-poster.jpg",
				BrandName:       "Maison Dệt Kim",
				Tagline:         "Tay nghề dệt kim tinh tuyển, số lượng giới hạn cho từng mùa",
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
					Variant:  "luxury-maison",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: HeroProps{
						Eyebrow:  "MAISON ARCHIVE",
						Title:    "Nghệ thuật dệt kim trầm mặc và chuẩn xác",
						Subtitle: "Mỗi thiết kế được dệt tỉ mỉ từ sợi cotton dài và nỉ chân cua 320gsm, giữ phom sang trọng qua nhiều năm.",
						ImageURL: "/atmosphere/jacquard-loom-poster.jpg",
						CTALabel: "Khám phá lưu trữ",
						CTAHref:  "/san-pham",
					},
				},
				{
					ID:       "lm-lookbook",
					Type:     "lookbook",
					Visible:  true,
					Variant:  "framed-gallery",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Columns: 2},
					Props: LookbookProps{
						Title:    "Bộ ảnh Maison",
						Subtitle: "Chi tiết mắt dệt, cổ áo đứng dáng và ánh sắc trầm ấm.",
						CTALabel: "Xem tuyển chọn",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "lm-lb1", Type: "image", Props: ImageBlockProps{ImageURL: "/atmosphere/jacquard-loom-poster.jpg", Alt: "Khung cửi dệt kim tinh xảo", Caption: "Mắt dệt Jacquard & Rib 1:1", Aspect: "landscape"}},
						{ID: "lm-lb2", Type: "image", Props: ImageBlockProps{ImageURL: "/atmosphere/cotton-twill-slate.jpg", Alt: "Bề mặt vải chéo màu đá", Caption: "Độ rủ và kết cấu bề mặt", Aspect: "landscape"}},
					},
				},
				{
					ID:       "lm-manifesto",
					Type:     "text",
					Visible:  true,
					Variant:  "split-quote",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "left"},
					Props: TextProps{
						Title: "Giá trị nằm ở điều không vội vã",
						Body:  "Chúng tôi giữ quy mô xưởng nhỏ để mọi chiếc áo đều đi qua bàn soi ánh sáng trước khi đóng hộp. Từ bo cổ, đường nối vai đến độ đàn hồi của gấu áo đều tuân thủ tiêu chuẩn khắt khe.",
						Align: "left",
					},
					Blocks: []SectionBlock{
						{ID: "lm-q1", Type: "quote", Props: QuoteBlockProps{Body: "Sự sang trọng thực sự thể hiện qua cảm giác chạm tay và độ bền qua năm tháng.", Author: "Đức Anh", Role: "Nghệ nhân dệt kim"}},
					},
				},
				{
					ID:       "lm-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "editorial",
					Settings: &SectionSettings{Surface: "surface", Padding: "generous", Columns: 2, MaxItems: 4},
					Props: FeaturedProductsProps{
						Title:      "Tuyển phẩm giới hạn",
						Subtitle:   "Bốn thiết kế tiêu biểu của mùa hiện tại.",
						ProductIDs: []string{},
						Limit:      4,
					},
				},
				{
					ID:       "lm-collection",
					Type:     "collection",
					Visible:  true,
					Variant:  "gallery",
					Settings: &SectionSettings{Surface: "band", Padding: "normal", Columns: 2},
					Props: CollectionProps{
						Title:          "Dòng dệt kim & nỉ 320gsm",
						Subtitle:       "Giữ phom đứng cho tiết trời se lạnh và không gian điều hòa.",
						CollectionSlug: "maison-knitwear",
						CategorySlug:   "ao-polo",
						ImageURL:       "/atmosphere/cotton-twill-slate.jpg",
						Limit:          4,
					},
				},
				{
					ID:       "lm-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "list",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal"},
					Props: PoliciesProps{
						Title: "Đặc quyền khách hàng Maison",
						Items: []PolicyItem{
							{Heading: "Hộp quà lưu trữ cao cấp", Detail: "Đóng gói trong hộp cứng kèm giấy lụa và thẻ chăm sóc riêng."},
							{Heading: "Đổi cỡ tận nơi trong 45 ngày", Detail: "Dịch vụ nhận và giao lại riêng biệt không phát sinh chi phí."},
							{Heading: "Tư vấn 1–1 cùng chuyên viên", Detail: "Hỗ trợ đo chọn kích cỡ và bảo quản sợi dệt kim lâu bền."},
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
					IntroTitle:    "Bộ sưu tập tuyển chọn",
					IntroSubtitle: "Khám phá từng thiết kế trong không gian trưng bày tinh giản.",
				},
				PDP: PDPPageConfig{
					Layout:        "luxury-framed",
					GalleryStyle:  "framed",
					ShowVouchers:  false,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   true,
				},
				Cart: CartPageConfig{
					Layout:       "luxury-folio",
					ShowVouchers: true,
					ShowAddons:   false,
					TrustNote:    "Mỗi đơn hàng được đóng hộp thủ công và bảo vệ trọn vẹn khi vận chuyển.",
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
		Name:            "Một Trang Ra Mắt",
		Category:        "launch",
		Archetype:       "one-page-launch",
		Description:     "Bố cục tập trung chuyển đổi cho thương hiệu ít SKU hoặc đợt ra mắt sản phẩm chủ lực với điều hướng neo gọn và điểm nhấn giá trị rõ ràng.",
		PreviewImageURL: "/banners/campaign-green-fiber.jpg",
		Tags:            []string{"one-page", "launch", "hero-product", "conversion"},
		SortOrder:       7,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "one-page-launch",
			Theme: ThemeConfigV2{
				BGColor:         "#F8FAFC",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#0F172A",
				AccentColor:     "#4338CA",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "be-vietnam-pro",
				LogoURL:         "/banners/campaign-everyday.jpg",
				BannerURL:       "/banners/campaign-green-fiber.jpg",
				BrandName:       "Sản Phẩm Ra Mắt",
				Tagline:         "Một thiết kế chủ lực hoàn thiện đến từng chi tiết, sẵn sàng giao ngay",
				Radius:          "lg",
				Density:         "balanced",
			},
			Layout: LayoutConfig{
				Archetype:        "one-page-launch",
				HeaderStyle:      "anchor-minimal",
				FooterStyle:      "minimal-row",
				ContainerWidth:   "standard",
				CardStyle:        "minimal",
				ShowAnnouncement: true,
				AnnouncementText: "Đợt mở bán đầu tiên • Ưu đãi đặt sớm kèm đổi size miễn phí 45 ngày",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "op-hero",
					Type:     "hero",
					Visible:  true,
					Variant:  "compact-launch",
					Settings: &SectionSettings{Surface: "bg", Padding: "generous", Align: "center"},
					Props: HeroProps{
						Eyebrow:  "PHIÊN BẢN RA MẮT 2026",
						Title:    "Chiếc áo duy nhất bạn muốn mặc mỗi ngày",
						Subtitle: "Tối ưu độ thoáng, độ đứng phom và cảm giác mềm mại chỉ trong một dòng sản phẩm chủ lực.",
						ImageURL: "/banners/campaign-green-fiber.jpg",
						CTALabel: "Đặt mua ngay",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "op-h-b1", Type: "badge", Props: BadgeBlockProps{Label: "Mở bán giới hạn", Tone: "accent"}},
						{ID: "op-h-c1", Type: "cta", Props: CTABlockProps{Label: "Xem thông số", Href: "/gioi-thieu", Style: "secondary"}},
					},
				},
				{
					ID:       "op-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "grid",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3},
					Props: MetricsStripProps{
						Title:    "Ba con số tạo nên khác biệt",
						Subtitle: "Tập trung toàn bộ nguồn lực vào chất lượng cốt lõi.",
					},
					Blocks: []SectionBlock{
						{ID: "op-m1", Type: "stat", Props: StatBlockProps{Value: "40+", Label: "Chu kỳ giặt giữ phom", Note: "Kiểm định co rút < 2%"}},
						{ID: "op-m2", Type: "stat", Props: StatBlockProps{Value: "4.9/5", Label: "Mức độ hài lòng", Note: "Từ khách hàng đặt sớm"}},
						{ID: "op-m3", Type: "stat", Props: StatBlockProps{Value: "24 giờ", Label: "Xử lý xuất kho", Note: "Giao nhanh toàn quốc"}},
					},
				},
				{
					ID:       "op-features",
					Type:     "text",
					Visible:  true,
					Variant:  "manifesto",
					Settings: &SectionSettings{Surface: "band", Padding: "normal", Align: "left"},
					Props: TextProps{
						Title: "Vì sao chúng tôi chỉ làm 3 thiết kế cốt lõi?",
						Body:  "Thay vì ra mắt hàng trăm mẫu mã mỗi mùa, toàn bộ thời gian thiết kế và kiểm định được dồn cho những món đồ mặc nhiều nhất trong tuần.",
						Align: "left",
					},
					Blocks: []SectionBlock{
						{ID: "op-f1", Type: "feature", Props: FeatureBlockProps{Title: "Cổ áo chống bai giãn", Detail: "Dệt bo rib mật độ cao giữ đường cổ phẳng phiu cả ngày.", Tag: "Chi tiết 01"}},
						{ID: "op-f2", Type: "feature", Props: FeatureBlockProps{Title: "Thân áo thoáng nhẹ", Detail: "Bề mặt mịn không bám da khi di chuyển ngoài trời.", Tag: "Chi tiết 02"}},
						{ID: "op-f3", Type: "feature", Props: FeatureBlockProps{Title: "Phom dáng dễ chọn cỡ", Detail: "Bảng size chuẩn dáng người Việt từ S đến XXL.", Tag: "Chi tiết 03"}},
					},
				},
				{
					ID:       "op-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "spotlight",
					Settings: &SectionSettings{Surface: "surface", Padding: "normal", Columns: 3, MaxItems: 3},
					Props: FeaturedProductsProps{
						Title:      "Chọn phiên bản của bạn",
						Subtitle:   "Đặt trực tiếp ngay hôm nay để nhận ưu đãi đợt mở bán.",
						ProductIDs: []string{},
						Limit:      3,
					},
				},
				{
					ID:       "op-testimonials",
					Type:     "testimonials",
					Visible:  true,
					Variant:  "single-feature",
					Settings: &SectionSettings{Surface: "bg", Padding: "normal"},
					Props: TestimonialsProps{
						Title:    "Phản hồi đầu tiên",
						Subtitle: "Trải nghiệm từ nhóm khách thử nghiệm trước ngày mở bán.",
					},
					Blocks: []SectionBlock{
						{ID: "op-q1", Type: "quote", Props: QuoteBlockProps{Body: "Nhận áo xong mình đặt thêm luôn hai màu còn lại vì mặc cực kỳ nhẹ và đứng dáng.", Author: "Thanh Tùng", Role: "Kỹ sư phần mềm, TP.HCM"}},
					},
				},
				{
					ID:       "op-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "tickets",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Ưu đãi đặt sớm",
						Subtitle: "Số lượng có hạn cho đợt ra mắt đầu tiên.",
						Limit:    3,
					},
				},
				{
					ID:       "op-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "cards",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", Columns: 3},
					Props: PoliciesProps{
						Title: "Bảo chứng trải nghiệm",
						Items: []PolicyItem{
							{Heading: "Dùng thử an tâm 45 ngày", Detail: "Không vừa ý được đổi size hoặc hoàn tiền minh bạch."},
							{Heading: "Thanh toán linh hoạt", Detail: "Hỗ trợ VietQR xác nhận tức thì và COD đồng kiểm."},
							{Heading: "Hỗ trợ trực tiếp", Detail: "Trò chuyện ngay với đội ngũ phát triển sản phẩm."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "single-strip",
					Columns:       3,
					ShowFilters:   false,
					ShowSearch:    false,
					ShowSkuMatrix: false,
					IntroTitle:    "Dòng sản phẩm ra mắt",
					IntroSubtitle: "Tất cả phiên bản màu sắc và kích cỡ trong đợt mở bán hiện tại.",
				},
				PDP: PDPPageConfig{
					Layout:        "one-page-drawer",
					GalleryStyle:  "stacked",
					ShowVouchers:  true,
					ShowSkuTable:  false,
					ShowStoryBand: true,
					ShowRelated:   false,
				},
				Cart: CartPageConfig{
					Layout:       "express-bar",
					ShowVouchers: true,
					ShowAddons:   true,
					TrustNote:    "Thanh toán một bước gọn gàng, miễn phí đổi cỡ tận nơi.",
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
		Name:            "Catalog Nhiều SKU & B2B",
		Category:        "catalog",
		Archetype:       "multi-sku-catalog",
		Description:     "Thiết kế cho xưởng sỉ và gian hàng có nhiều biến thể màu/size: bảng ma trận SKU đặt nhanh, thanh lọc công nghiệp và biểu mẫu đơn gọn.",
		PreviewImageURL: "/atmosphere/cotton-twill-slate.jpg",
		Tags:            []string{"b2b", "sku-matrix", "catalog", "wholesale"},
		SortOrder:       8,
		Config: ConfigV2{
			SchemaVersion: 2,
			TemplateID:    "multi-sku-catalog",
			Theme: ThemeConfigV2{
				BGColor:         "#F3F5F7",
				SurfaceColor:    "#FFFFFF",
				TextColor:       "#111827",
				AccentColor:     "#0F766E",
				AccentTextColor: "#FFFFFF",
				FontFamily:      "ibm-plex-sans",
				LogoURL:         "/atmosphere/cotton-twill-slate.jpg",
				BannerURL:       "/banners/vietnam-workshop.jpg",
				BrandName:       "Kho Sỉ & Catalog SKU",
				Tagline:         "Bảng ma trận biến thể trực quan, tồn kho thực và đặt nhanh nhiều cỡ",
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
				AnnouncementText: "Bảng tồn kho cập nhật theo thời gian thực • Hỗ trợ đơn sỉ & xuất hóa đơn VAT",
				Slots:            defaultSlots(),
			},
			Sections: []SectionV2{
				{
					ID:       "ms-banner",
					Type:     "banner",
					Visible:  true,
					Variant:  "split",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact"},
					Props: BannerProps{
						Title:    "Catalog biến thể màu & size trực tiếp từ kho",
						Subtitle: "Xem mã SKU, đơn giá nguyên VND và số lượng còn sẵn ngay trên một bảng duy nhất.",
						ImageURL: "/banners/vietnam-workshop.jpg",
						CTALabel: "Mở bảng SKU đầy đủ",
						CTAHref:  "/san-pham",
					},
					Blocks: []SectionBlock{
						{ID: "ms-b1", Type: "badge", Props: BadgeBlockProps{Label: "Sẵn kho > 150 SKU", Tone: "accent"}},
						{ID: "ms-b2", Type: "cta", Props: CTABlockProps{Label: "Chính sách sỉ", Href: "/gioi-thieu", Style: "outline"}},
					},
				},
				{
					ID:       "ms-sku-matrix",
					Type:     "sku_matrix",
					Visible:  true,
					Variant:  "table",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact", MaxItems: 20},
					Props: SkuMatrixProps{
						Title:          "Bảng đặt nhanh theo SKU",
						Subtitle:       "Chọn số lượng trực tiếp cho từng màu và kích cỡ đang còn hàng.",
						CategorySlug:   "",
						Limit:          20,
						ShowOutOfStock: true,
					},
				},
				{
					ID:       "ms-metrics",
					Type:     "metrics_strip",
					Visible:  true,
					Variant:  "inline-bar",
					Settings: &SectionSettings{Surface: "band", Padding: "compact", Columns: 4},
					Props: MetricsStripProps{
						Title:    "Năng lực cung ứng kho",
						Subtitle: "Phục vụ cả khách lẻ và đối tác đồng phục, bán buôn.",
					},
					Blocks: []SectionBlock{
						{ID: "ms-m1", Type: "stat", Props: StatBlockProps{Value: "150+", Label: "Mã SKU sẵn kho", Note: "Đủ size S đến XXL"}},
						{ID: "ms-m2", Type: "stat", Props: StatBlockProps{Value: "1 cái", Label: "Đặt tối thiểu linh hoạt", Note: "Chiết khấu tự động theo bậc"}},
						{ID: "ms-m3", Type: "stat", Props: StatBlockProps{Value: "VAT", Label: "Hóa đơn đầy đủ", Note: "Xuất theo thông tin doanh nghiệp"}},
						{ID: "ms-m4", Type: "stat", Props: StatBlockProps{Value: "24 giờ", Label: "Xuất hàng liên tỉnh", Note: "Đóng kiện tiêu chuẩn"}},
					},
				},
				{
					ID:       "ms-featured",
					Type:     "featured_products",
					Visible:  true,
					Variant:  "compact-rows",
					Settings: &SectionSettings{Surface: "bg", Padding: "compact", Columns: 4, MaxItems: 8},
					Props: FeaturedProductsProps{
						Title:      "Danh mục mã hàng chủ lực",
						Subtitle:   "Bấm vào từng sản phẩm để mở bảng chọn nhiều biến thể cùng lúc.",
						ProductIDs: []string{},
						Limit:      8,
					},
				},
				{
					ID:       "ms-vouchers",
					Type:     "voucher_strip",
					Visible:  true,
					Variant:  "inline-bar",
					Settings: &SectionSettings{Surface: "accent-soft", Padding: "compact"},
					Props: VoucherStripProps{
						Title:    "Mã chiết khấu theo giá trị đơn",
						Subtitle: "Lưu mã để áp dụng nhanh cho đơn mua nhiều sản phẩm.",
						Limit:    4,
					},
				},
				{
					ID:       "ms-policies",
					Type:     "policies",
					Visible:  true,
					Variant:  "list",
					Settings: &SectionSettings{Surface: "surface", Padding: "compact"},
					Props: PoliciesProps{
						Title: "Điều khoản cung ứng & đổi trả",
						Items: []PolicyItem{
							{Heading: "Kiểm đếm đúng mã SKU", Detail: "Mỗi kiện hàng kèm phiếu đóng gói chi tiết từng mã màu và size."},
							{Heading: "Đổi size linh hoạt", Detail: "Hỗ trợ đổi phần trăm tồn size chưa sử dụng trong 30 ngày."},
							{Heading: "Hỗ trợ báo giá nhanh", Detail: "Nhắn trực tiếp qua chat gian hàng cho đơn số lượng lớn."},
						},
					},
				},
			},
			Pages: PagesConfig{
				Catalog: CatalogPageConfig{
					Layout:        "sku-table",
					Columns:       5,
					ShowFilters:   true,
					ShowSearch:    true,
					ShowSkuMatrix: true,
					IntroTitle:    "Danh mục SKU toàn gian hàng",
					IntroSubtitle: "Chuyển đổi giữa chế độ bảng SKU và lưới sản phẩm để đặt hàng nhanh.",
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
					TrustNote:    "Hiển thị rõ từng mã SKU, số lượng và tổng tiền nguyên VND trước khi xác nhận.",
				},
				Info: InfoPageConfig{
					Layout:             "b2b-catalog-sheet",
					ShowFactoryProfile: true,
					ShowTOC:            true,
					ShowShowcase:       false,
					Sections: []SectionV2{
						{
							ID:      "ms-info-matrix",
							Type:    "sku_matrix",
							Visible: true,
							Variant: "wholesale-sheet",
							Props: SkuMatrixProps{
								Title:          "Bảng quy cách mã hàng tiêu chuẩn",
								Subtitle:       "Tra cứu nhanh nhóm mã hàng sẵn kho của xưởng.",
								CategorySlug:   "",
								Limit:          12,
								ShowOutOfStock: true,
							},
						},
					},
				},
			},
		},
	}
}
