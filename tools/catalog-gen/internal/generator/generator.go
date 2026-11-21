package generator

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

const (
	DefaultSeed  uint64 = 20261121
	DefaultCount int    = 20000

	MinPriceVND int64 = 59000
	MaxPriceVND int64 = 2490000
	PriceStep   int64 = 1000
	MaxStock    int32 = 200
)

// ShopTarget represents a target shop that generated products are assigned to.
type ShopTarget struct {
	ID   string
	Slug string
	Name string
}

// CategoryTarget represents a target category discovered from Product API.
type CategoryTarget struct {
	ID   string
	Slug string
	Name string
}

// SKUInput is a single generated SKU matching Product API's SKUInput schema.
type SKUInput struct {
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url"`
}

// ProductSpec is a single generated product ready to submit via REST or gRPC Product API.
type ProductSpec struct {
	Index       int        `json:"-"`
	ShopID      string     `json:"shop_id"`
	ShopSlug    string     `json:"-"`
	CategoryID  string     `json:"category_id"`
	CategoryKey string     `json:"-"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Images      []string   `json:"images"`
	SKUs        []SKUInput `json:"skus"`
}

// DefaultShops returns the 4 seeded NexusMart shops (used in --dry-run mode).
func DefaultShops() []ShopTarget {
	return []ShopTarget{
		{ID: "shop-bach-dang", Slug: "shop-bach-dang", Name: "Xưởng Dệt Bạch Đằng"},
		{ID: "shop-sai-gon", Slug: "shop-sai-gon", Name: "Kho Basic Sài Gòn"},
		{ID: "shop-da-nang", Slug: "shop-da-nang", Name: "Sợi Tự Nhiên Đà Nẵng"},
		{ID: "shop-ha-noi", Slug: "shop-ha-noi", Name: "Xưởng May Hà Nội"},
	}
}

// DefaultCategories returns the 6 seeded NexusMart categories (used in --dry-run mode).
func DefaultCategories() []CategoryTarget {
	return []CategoryTarget{
		{ID: "cat-ao-thun", Slug: "ao-thun", Name: "Áo thun"},
		{ID: "cat-ao-polo", Slug: "ao-polo", Name: "Áo polo"},
		{ID: "cat-quan-short", Slug: "quan-short", Name: "Quần short"},
		{ID: "cat-quan-jogger", Slug: "quan-jogger", Name: "Quần jogger"},
		{ID: "cat-do-lot", Slug: "do-lot", Name: "Đồ lót"},
		{ID: "cat-tat-phu-kien", Slug: "tat-phu-kien", Name: "Tất & Phụ kiện"},
	}
}

type itemArchetype struct {
	BaseName    string
	Materials   []string
	Specs       []string
	Fits        []string
	Occasions   []string
	CareGuide   string
	Colors      []string
	Sizes       []string
	MinPriceVND int64
	MaxPriceVND int64
	WeightMin   int32
	WeightMax   int32
	Images      []string
	IsTech      bool
}

var commonColors = []string{
	"Đen tuyền",
	"Trắng tinh",
	"Xanh navy",
	"Xám đá",
	"Be cát",
	"Đỏ đô",
	"Xanh rêu",
	"Nâu đất",
	"Xanh cổ vịt",
	"Than chì",
	"Xanh dương đậm",
	"Kem sữa",
}

var apparelSizes = []string{"S", "M", "L", "XL", "2XL"}
var underwearSizes = []string{"M", "L", "XL", "2XL"}
var accessorySizes = []string{"Tiêu chuẩn", "Cỡ nhỏ", "Cỡ lớn"}

var styleEditions = []string{
	"dòng Cơ Bản",
	"phiên bản Đặc Biệt",
	"bộ sưu tập Đô Thị",
	"dòng Cao Cấp",
	"mẫu Đường Phố",
	"bản Giới Hạn",
	"dòng Thể Thao Pro",
	"phong cách Tối Giản",
	"thiết kế Mới 2026",
	"chuẩn Xưởng Việt",
}

var categoryArchetypes = map[string][]itemArchetype{
	"ao-thun": {
		{
			BaseName: "Áo thun thể thao đẹp",
			Materials: []string{
				"Dry-Fit tái chế Micro-mesh",
				"Polyester thoáng khí lỗ kim",
				"Polyamide co giãn 4 chiều mát lạnh",
				"Cotton pha Spandex thấm hút nhanh",
			},
			Specs:     []string{"145gsm siêu nhẹ", "160gsm thoát ẩm nhanh", "175gsm kháng mùi", "150gsm khô nhanh 45 phút"},
			Fits:      []string{"dáng Athletic ôm gọn vai", "form Regular năng động", "cắt raglan linh hoạt cử động"},
			Occasions: []string{"chạy bộ đường dài", "tập gym cường độ cao", "đánh cầu lông cuối tuần", "đạp xe buổi sáng"},
			CareGuide: "Giặt máy chế độ nhẹ với nước lạnh dưới 30°C, không dùng nước xả làm bít sợi vải, phơi nơi thoáng gió.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 129000,
			MaxPriceVND: 489000,
			WeightMin:   180,
			WeightMax:   260,
			Images:      []string{"/products/ao-thun-dryfit-co-vit.png", "/models/model-ao-thun-dryfit.jpg"},
		},
		{
			BaseName: "Áo thun Cotton Compact",
			Materials: []string{
				"100% Cotton chải kỹ Compact",
				"95% Cotton Compact pha 5% Spandex",
				"Cotton hữu cơ sợi dài mềm mịn",
				"Cotton se đôi chống xù lông",
			},
			Specs:     []string{"220gsm đứng phom", "235gsm dày dặn không lộ", "210gsm mặt vải mịn", "240gsm bền màu 40 lần giặt"},
			Fits:      []string{"form Regular vừa vặn", "dáng Relaxed thoải mái", "cổ bo dệt rib 1:1 chống bai giãn"},
			Occasions: []string{"mặc hằng ngày", "đi làm công sở trẻ trung", "dạo phố cuối tuần", "du lịch bốn mùa"},
			CareGuide: "Lộn trái khi giặt máy ở nhiệt độ thường, không dùng thuốc tẩy mạnh, ủi hơi nước ở nhiệt độ trung bình.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 149000,
			MaxPriceVND: 449000,
			WeightMin:   220,
			WeightMax:   310,
			Images: []string{
				"/products/ao-thun-cotton-trang.png",
				"/products/ao-thun-cotton-xam-da.png",
				"/products/ao-thun-cotton-xanh-than.png",
				"/models/model-ao-thun-trang.jpg",
				"/models/model-ao-thun-xam-da.jpg",
				"/models/model-ao-thun-xanh-than.jpg",
			},
		},
		{
			BaseName: "Áo thun Oversize đường phố",
			Materials: []string{
				"Cotton Heavyweight sợi se đôi",
				"Cotton Compact xử lý Enzyme mềm mượt",
				"Cotton chân cua mỏng thoáng khí",
			},
			Specs:     []string{"260gsm đứng vai", "280gsm dày mịn cao cấp", "250gsm ít nhăn giữ dáng"},
			Fits:      []string{"dáng rộng Oversize vai xuôi", "form Boxy hiện đại unisex", "tay lỡ rộng rãi tôn dáng"},
			Occasions: []string{"dạo phố phong cách đường phố", "đi học đại học năng động", "chụp ảnh du lịch cuối tuần", "mặc đôi nam nữ"},
			CareGuide: "Giặt riêng màu đậm trong lần đầu, phơi ngang trên móc rộng vai để giữ chuẩn phom Oversize.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 189000,
			MaxPriceVND: 549000,
			WeightMin:   280,
			WeightMax:   390,
			Images:      []string{"/products/ao-thun-oversize-than-chi.png", "/models/model-ao-thun-oversize.jpg"},
		},
		{
			BaseName: "Áo khoác gió chống nước",
			Materials: []string{
				"Nylon Ripstop phủ trượt nước DWR",
				"Polyester 2 lớp cản gió chống tia UV",
				"vải dù siêu nhẹ chống thấm mưa nhẹ",
				"Microfiber cao cấp lót lưới thoáng khí",
			},
			Specs:     []string{"115gsm gấp gọn bỏ túi", "chỉ số chống nắng UPF 50+", "khóa kéo chống nước đường ép nhiệt", "130gsm trượt nước bền bỉ"},
			Fits:      []string{"form Regular có mũ trùm tháo rời", "dáng thể thao bo tay chắc chắn", "form rộng thoải mái khoác ngoài"},
			Occasions: []string{"đi xe máy trời mưa phùn", "leo núi dã ngoại Đà Lạt", "chạy bộ sáng sớm trời gió", "du lịch công tác linh hoạt"},
			CareGuide: "Giặt tay hoặc giặt máy túi lưới ở nước mát, không sấy nhiệt cao để bảo vệ lớp phủ trượt nước.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 349000,
			MaxPriceVND: 1490000,
			WeightMin:   310,
			WeightMax:   520,
			Images:      []string{"/products/ao-khoac-gio-xam-tro.png", "/models/model-ao-khoac-gio.jpg"},
		},
	},
	"ao-polo": {
		{
			BaseName: "Áo polo Pique mắt chim",
			Materials: []string{
				"Cotton Pique dệt mắt chim thoáng khí",
				"Cotton pha sợi cafe khử mùi tự nhiên",
				"Pique CVC chống nhăn dễ ủi",
				"Cotton Supima mềm mượt cao cấp",
			},
			Specs:     []string{"210gsm rãnh dệt thoáng mát", "225gsm cổ dệt không quăn mép", "195gsm co giãn 4 chiều"},
			Fits:      []string{"form Regular công sở gọn gàng", "dáng Slim tôn vai nam tính", "tay bo dệt rib sắc nét"},
			Occasions: []string{"đi làm văn phòng hằng ngày", "gặp gỡ đối tác lịch sự", "đi tiệc cuối tuần", "đánh golf và du lịch"},
			CareGuide: "Cài cúc trước khi giặt máy chế độ nhẹ, phơi trong bóng râm để giữ cổ áo phẳng đẹp.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 229000,
			MaxPriceVND: 799000,
			WeightMin:   250,
			WeightMax:   340,
			Images: []string{
				"/products/ao-polo-pique-kem.png",
				"/products/ao-polo-pique-xanh-da.png",
				"/models/model-polo-kem.jpg",
				"/models/model-polo-xanh-da.jpg",
			},
		},
		{
			BaseName: "Áo polo dệt kim mịn",
			Materials: []string{
				"sợi Viscose dệt kim rủ nhẹ",
				"Cotton Mercerized bóng mịn cao cấp",
				"len tăm mỏng thoáng mát bốn mùa",
				"sợi tre Bamboo pha Cotton mềm dịu",
			},
			Specs:     []string{"230gsm bề mặt dệt mịn", "205gsm nhẹ mát không hầm bí", "240gsm ít nhăn khi ngồi lâu"},
			Fits:      []string{"dáng Semi-Slim thanh lịch", "form Regular cổ mở không cúc", "dệt gân nổi tinh tế ở gấu áo"},
			Occasions: []string{"công sở thay thế áo sơ mi", "hẹn hò buổi tối sang trọng", "công tác di chuyển đường dài"},
			CareGuide: "Nên giặt trong túi giặt riêng, vắt nhẹ tốc độ thấp và phơi ngang tránh giãn chiều dài áo.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 289000,
			MaxPriceVND: 1190000,
			WeightMin:   270,
			WeightMax:   370,
			Images:      []string{"/products/ao-polo-det-kim-den.png", "/models/model-polo-det-kim-den.jpg"},
		},
		{
			BaseName: "Áo polo thể thao đánh golf",
			Materials: []string{
				"Polyester Coolmax làm mát tức thì",
				"sợi tổng hợp co giãn 4 chiều kháng tia UV",
				"vải thể thao dệt lỗ siêu thoáng",
			},
			Specs:     []string{"165gsm thấm hút mồ hôi nhanh", "UPF 40+ chống nắng ngoài trời", "175gsm không bám dính da"},
			Fits:      []string{"dáng Athletic linh hoạt xoay vai", "form Regular thoáng mát"},
			Occasions: []string{"đánh golf cuối tuần", "chơi tennis và pickleball", "di chuyển ngoài trời nắng nóng"},
			CareGuide: "Giặt nước lạnh ngay sau khi vận động, phơi khô tự nhiên, không ủi trực tiếp lên logo ép nhiệt.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 259000,
			MaxPriceVND: 990000,
			WeightMin:   220,
			WeightMax:   300,
			Images:      []string{"/products/ao-polo-pique-xanh-da.png", "/models/model-polo-xanh-da.jpg"},
		},
	},
	"quan-short": {
		{
			BaseName: "Quần đùi chạy bộ",
			Materials: []string{
				"Polyamide Active co giãn 4 chiều",
				"Polyester tái chế siêu nhẹ thoáng khí",
				"vải dù xẻ tà chống thấm mồ hôi",
			},
			Specs:     []string{"118gsm siêu nhẹ 5 inch", "125gsm có lớp lót lưới chống ma sát", "túi khóa kéo sau lưng đựng điện thoại"},
			Fits:      []string{"dáng Athletic 5 inch xẻ tà", "form thể thao 7 inch cạp giấu dây rút", "đường may 4 kim 6 chỉ êm da"},
			Occasions: []string{"chạy bộ 5K đến marathon", "tập gym và cardio mỗi sáng", "đạp xe thể dục công viên", "chơi cầu lông trong nhà"},
			CareGuide: "Giặt nước lạnh không dùng chất tẩy trắng, phơi gió khô nhanh chỉ trong 45 phút.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 159000,
			MaxPriceVND: 499000,
			WeightMin:   160,
			WeightMax:   250,
			Images:      []string{"/products/quan-short-run-xam-dam.png", "/models/model-short-run-xam.jpg"},
		},
		{
			BaseName: "Quần short Daily 7 inch",
			Materials: []string{
				"Cotton Twill co giãn nhẹ",
				"Kaki mềm giặt mềm sẵn",
				"Cotton pha Spandex giữ dáng bền bỉ",
			},
			Specs:     []string{"240gsm đứng ống gọn gàng", "255gsm túi sâu an toàn", "230gsm không nhăn nhàu"},
			Fits:      []string{"form Regular dài 7 inch trên gối", "cạp chun sau phối dây rút tiện lợi", "dáng Relaxed mặc thoải mái"},
			Occasions: []string{"mặc hằng ngày dạo phố", "đi cà phê cuối tuần", "du lịch nghỉ dưỡng cùng gia đình"},
			CareGuide: "Giặt máy nước mát dưới 30°C, không ngâm lâu, phơi mặt trái để giữ màu vải bền đẹp.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 199000,
			MaxPriceVND: 589000,
			WeightMin:   270,
			WeightMax:   360,
			Images:      []string{"/products/quan-short-daily-be.png", "/models/model-short-daily-be.jpg"},
		},
		{
			BaseName: "Quần short Linen mùa hè",
			Materials: []string{
				"Linen tự nhiên pha Cotton mềm mát",
				"vải đũi lanh thoáng khí cao cấp",
				"Linen Nhật giảm nhăn nhẹ nhàng",
			},
			Specs:     []string{"185gsm thoáng mát khí hậu nhiệt đới", "195gsm sợi tự nhiên thân thiện làn da", "cạp chun êm ái không hằn bụng"},
			Fits:      []string{"dáng Relaxed rộng rãi mát mẻ", "form Regular dài 6.5 inch thanh lịch"},
			Occasions: []string{"đi biển Đà Nẵng Phú Quốc", "dạo phố mùa hè nắng nóng", "mặc nhà tiếp khách lịch sự"},
			CareGuide: "Giặt nhẹ bằng tay hoặc túi giặt, giũ phẳng trước khi phơi, ủi hơi nước khi còn hơi ẩm.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 219000,
			MaxPriceVND: 649000,
			WeightMin:   230,
			WeightMax:   310,
			Images:      []string{"/products/quan-short-linen-xam-da.png", "/models/model-short-linen-xam.jpg"},
		},
	},
	"quan-jogger": {
		{
			BaseName: "Quần jogger nỉ chân cua",
			Materials: []string{
				"nỉ chân cua French Terry cao cấp",
				"Cotton chân cua không xù mặt trong",
				"CVC French Terry ấm nhẹ thoáng khí",
			},
			Specs:     []string{"320gsm dày dặn đứng ống", "300gsm mềm mịn không gây ngứa", "340gsm giữ ấm thu đông"},
			Fits:      []string{"dáng Tapered thuôn gọn bo gấu", "form Regular thoải mái vùng đùi", "túi khóa kéo hai bên chắc chắn"},
			Occasions: []string{"mặc mùa thu đông miền Bắc", "đi du lịch máy bay đường dài", "tập luyện ngoài trời sáng sớm", "mặc nhà năng động"},
			CareGuide: "Lộn trái khi giặt máy ở chế độ thường, không sấy nhiệt quá cao tránh co rút sợi bông.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 299000,
			MaxPriceVND: 890000,
			WeightMin:   410,
			WeightMax:   560,
			Images:      []string{"/products/quan-jogger-terry-xam.png", "/models/model-jogger-terry-xam.jpg"},
		},
		{
			BaseName: "Quần jogger Tech trượt nước",
			Materials: []string{
				"Nylon Tech co giãn 4 chiều trượt nước",
				"Polyamide dệt chéo chống bám bụi",
				"vải công nghệ nhanh khô thoáng mát",
			},
			Specs:     []string{"175gsm nhẹ bền không nhăn", "phủ trượt nước nhẹ đi mưa nhỏ", "túi ẩn khóa kéo an toàn"},
			Fits:      []string{"dáng Slim Tapered hiện đại", "form Athletic ôm nhẹ bắp chân", "cạp co giãn tích hợp đai tiện dụng"},
			Occasions: []string{"đi làm công sở năng động", "di chuyển xe máy hằng ngày", "dã ngoại leo núi nhẹ", "tập gym mùa mưa"},
			CareGuide: "Giặt nước lạnh, không dùng nước xả vải hay chất tẩy, phơi nơi râm mát rất nhanh khô.",
			Colors:    commonColors,
			Sizes:     apparelSizes,
			MinPriceVND: 329000,
			MaxPriceVND: 1190000,
			WeightMin:   330,
			WeightMax:   460,
			Images:      []string{"/products/quan-jogger-tech-den.png", "/models/model-jogger-tech-den.jpg"},
		},
	},
	"do-lot": {
		{
			BaseName: "Đồ lót Boxer sợi gỗ sồi Modal",
			Materials: []string{
				"sợi gỗ sồi Modal siêu mềm mịn",
				"92% Modal pha 8% Spandex co giãn đàn hồi",
				"Modal kháng khuẩn khử mùi tự nhiên",
			},
			Specs:     []string{"180gsm mịn mát như lụa", "đường may phẳng Flatlock không hằn da", "bộ 3 chiếc tiết kiệm"},
			Fits:      []string{"dáng Boxer Brief ôm nâng đỡ tự nhiên", "cạp dệt vi sợi mềm không cuộn gập", "ống quần chống cuộn khi vận động"},
			Occasions: []string{"mặc hằng ngày suốt 12 giờ", "ngồi văn phòng không hầm bí", "vận động thể thao thoải mái", "đi công tác dài ngày"},
			CareGuide: "Giặt bằng túi giặt lưới ở nhiệt độ dưới 30°C, không vắt xoắn mạnh, thay mới định kỳ sau 6 tháng.",
			Colors:    commonColors,
			Sizes:     underwearSizes,
			MinPriceVND: 89000,
			MaxPriceVND: 399000,
			WeightMin:   150,
			WeightMax:   260,
			Images:      []string{"/products/do-lot-boxer-modal-3.png", "/models/model-mac-nha-modal.jpg"},
		},
		{
			BaseName: "Đồ lót Brief sợi tre Bamboo",
			Materials: []string{
				"sợi tre thiên nhiên Bamboo kháng khuẩn",
				"Bamboo pha Cotton hữu cơ mềm thoáng",
				"sợi tre cao cấp thấm hút vượt trội",
			},
			Specs:     []string{"190gsm thoáng khí kháng mùi", "bộ 3 chiếc đóng hộp gọn gàng", "cạp mềm êm ái không gây vết hằn"},
			Fits:      []string{"dáng Brief cổ điển ôm gọn", "thiết kế công thái học thoáng mát cả ngày"},
			Occasions: []string{"mặc hằng ngày mùa hè nóng ẩm", "mặc nhà ngủ ngon giấc", "tập luyện thể dục nhẹ nhàng"},
			CareGuide: "Giặt nhẹ bằng xà phòng dịu nhẹ, tránh phơi trực tiếp dưới nắng trưa gắt để sợi tre bền mềm.",
			Colors:    commonColors,
			Sizes:     underwearSizes,
			MinPriceVND: 79000,
			MaxPriceVND: 349000,
			WeightMin:   140,
			WeightMax:   240,
			Images:      []string{"/products/do-lot-brief-tre-3.png", "/models/model-mac-nha-bamboo.jpg"},
		},
		{
			BaseName: "Đồ lót thể thao nhanh khô",
			Materials: []string{
				"Polyamide Micro-mesh thoát ẩm tức thì",
				"sợi lạnh Ice Silk mát dịu làn da",
				"vải dệt không đường may Seamless",
			},
			Specs:     []string{"140gsm siêu nhẹ khô nhanh", "thiết kế lưới thoáng vùng nhiệt cao", "co giãn 4 chiều bền bỉ"},
			Fits:      []string{"dáng Boxer dài chống cọ xát đùi", "form thể thao ôm sát nhẹ nhàng"},
			Occasions: []string{"chạy bộ đường dài", "đạp xe và tập gym", "du lịch khám phá ngoài trời"},
			CareGuide: "Giặt nước mát ngay sau buổi tập, không sấy nóng và không ủi.",
			Colors:    commonColors,
			Sizes:     underwearSizes,
			MinPriceVND: 99000,
			MaxPriceVND: 429000,
			WeightMin:   130,
			WeightMax:   220,
			Images:      []string{"/products/do-lot-boxer-modal-3.png", "/products/do-lot-brief-tre-3.png"},
		},
	},
	"tat-phu-kien": {
		{
			BaseName: "Tất Cotton cổ trung khử mùi",
			Materials: []string{
				"Cotton chải kỹ pha sợi bạc kháng khuẩn",
				"Cotton hữu cơ dệt đệm gót êm chân",
				"sợi tre Bamboo mềm mịn thoáng khí",
			},
			Specs:     []string{"bộ 5 đôi đóng hộp", "đệm lòng bàn chân giảm chấn", "cổ bo đàn hồi không tụt gót"},
			Fits:      []string{"cỡ tự do 39-44 ôm chân", "thiết kế mũi tất khâu phẳng không cộm ngón"},
			Occasions: []string{"đi giày thể thao hằng ngày", "đi làm công sở giày tây", "chạy bộ và tập gym"},
			CareGuide: "Giặt máy túi lưới ở nước mát, không tẩy trắng, phơi nơi thoáng mát.",
			Colors:    commonColors,
			Sizes:     accessorySizes,
			MinPriceVND: 59000,
			MaxPriceVND: 249000,
			WeightMin:   120,
			WeightMax:   240,
			Images:      []string{"/products/tat-cotton-co-trung-5.png", "/models/model-tat-co-trung.jpg"},
		},
		{
			BaseName: "Tất thể thao cổ thấp đệm êm",
			Materials: []string{
				"sợi Coolmax thoát mồ hôi nhanh",
				"Cotton thể thao pha Nylon chống mài mòn",
				"dệt lưới mu bàn chân siêu thoáng",
			},
			Specs:     []string{"bộ 4 đôi thể thao", "vòng nâng vòm bàn chân chống trượt", "đệm gót hạn chế phồng rộp"},
			Fits:      []string{"cổ thấp giấu giày gọn gàng", "cỡ chân 38-43 co giãn ôm sát"},
			Occasions: []string{"chạy bộ công viên", "đánh cầu lông và bóng rổ", "tập luyện phòng gym"},
			CareGuide: "Giặt nhẹ sau mỗi buổi tập, phơi khô tự nhiên để duy trì độ đàn hồi của cổ tất.",
			Colors:    commonColors,
			Sizes:     accessorySizes,
			MinPriceVND: 69000,
			MaxPriceVND: 269000,
			WeightMin:   110,
			WeightMax:   220,
			Images:      []string{"/products/tat-the-thao-co-thap-4.png", "/models/model-tat-the-thao.jpg"},
		},
		{
			BaseName: "Đèn bàn học chống cận LED",
			Materials: []string{
				"nhựa ABS chống cháy kết hợp thân hợp kim nhôm",
				"chip LED chuẩn CRI > 95 ánh sáng tự nhiên",
				"chao đèn tản sáng chống chói lóa",
			},
			Specs:     []string{"công suất 12W 3 chế độ nhiệt màu 3000K-6000K", "cảm ứng điều chỉnh độ sáng mượt mà", "cổng sạc USB tiện lợi kèm hẹn giờ 45 phút"},
			Fits:      []string{"cần đèn gập xoay 180 độ linh hoạt", "chân đế chống trượt chắc chắn gọn bàn học"},
			Occasions: []string{"học bài buổi tối cho học sinh sinh viên", "đọc sách và làm việc văn phòng tại nhà", "vẽ kỹ thuật cần ánh sáng trung thực"},
			CareGuide: "Lau bụi nhẹ bằng khăn khô mềm, sử dụng đúng nguồn điện tiêu chuẩn và bảo hành chính hãng 12 tháng.",
			Colors:    []string{"Trắng tinh", "Đen tuyền", "Xám bạc", "Xanh pastel", "Be cát"},
			Sizes:     accessorySizes,
			MinPriceVND: 189000,
			MaxPriceVND: 1290000,
			WeightMin:   480,
			WeightMax:   950,
			Images:      []string{"/products/tat-cotton-co-trung-5.png"},
			IsTech:      true,
		},
		{
			BaseName: "Tai nghe không dây Bluetooth",
			Materials: []string{
				"vỏ phủ nhám chống bám vân tay cao cấp",
				"màng loa Dynamic 12mm âm trầm sâu sắc",
				"đệm tai silicon kháng khuẩn đeo êm ái",
			},
			Specs:     []string{"Bluetooth 5.3 kết nối ổn định chống ồn chủ động ANC", "pin dùng liên tục 30 giờ kèm hộp sạc nhanh USB-C", "chuẩn kháng nước IPX5 bền bỉ"},
			Fits:      []string{"thiết kế công thái học nhẹ chỉ 4.2g mỗi bên", "điều khiển cảm ứng chạm nhạy mượt mà"},
			Occasions: []string{"nghe nhạc khi chạy bộ và tập gym", "họp trực tuyến lọc tạp âm rõ giọng nói", "di chuyển công tác và xem phim thư giãn"},
			CareGuide: "Vệ sinh màng loa định kỳ bằng tăm bông khô, cất trong hộp sạc khi không dùng, bảo hành đổi mới 12 tháng.",
			Colors:    []string{"Đen tuyền", "Trắng tinh", "Xanh navy", "Xám đá", "Be cát"},
			Sizes:     accessorySizes,
			MinPriceVND: 299000,
			MaxPriceVND: 2490000,
			WeightMin:   150,
			WeightMax:   380,
			Images:      []string{"/products/tat-the-thao-co-thap-4.png"},
			IsTech:      true,
		},
		{
			BaseName: "Balo đựng laptop chống sốc",
			Materials: []string{
				"vải Oxford trượt nước mật độ cao",
				"Polyester tái chế chống rạch bền bỉ",
				"đệm lưng lưới thoáng khí chống đau vai",
			},
			Specs:     []string{"ngăn chống sốc vừa laptop 15.6 inch", "dung tích 24 lít nhiều ngăn khoa học", "khóa kéo kim loại bền mượt kèm cổng USB"},
			Fits:      []string{"quai đeo chữ S phân bổ trọng lực êm vai", "đai cài vali tiện lợi khi đi công tác"},
			Occasions: []string{"đi học đại học và đi làm văn phòng", "công tác ngắn ngày 2-3 ngày", "du lịch cuối tuần năng động"},
			CareGuide: "Lau sạch bằng khăn ẩm và xà phòng nhẹ, không giặt máy chế độ vắt mạnh, phơi nơi thoáng gió.",
			Colors:    []string{"Đen tuyền", "Xám đá", "Xanh navy", "Xanh rêu", "Nâu đất"},
			Sizes:     accessorySizes,
			MinPriceVND: 279000,
			MaxPriceVND: 1890000,
			WeightMin:   550,
			WeightMax:   1150,
			Images:      []string{"/products/ao-khoac-gio-xam-tro.png"},
			IsTech:      true,
		},
	},
}

// Generator deterministically produces realistic Vietnamese NexusMart products.
type Generator struct {
	seed       uint64
	shops      []ShopTarget
	categories []CategoryTarget
}

// New creates a deterministic product generator for the given seed, shops, and categories.
func New(seed uint64, shops []ShopTarget, categories []CategoryTarget) *Generator {
	if seed == 0 {
		seed = DefaultSeed
	}
	if len(shops) == 0 {
		shops = DefaultShops()
	}
	if len(categories) == 0 {
		categories = DefaultCategories()
	}
	shopsCopy := make([]ShopTarget, len(shops))
	copy(shopsCopy, shops)
	catsCopy := make([]CategoryTarget, len(categories))
	copy(catsCopy, categories)
	return &Generator{
		seed:       seed,
		shops:      shopsCopy,
		categories: catsCopy,
	}
}

// Generate returns count deterministically generated Vietnamese products evenly distributed
// across all configured shops and categories.
func (g *Generator) Generate(count int) []ProductSpec {
	if count <= 0 {
		return []ProductSpec{}
	}
	rng := rand.New(rand.NewPCG(g.seed, g.seed^0x9e3779b97f4a7c15))
	out := make([]ProductSpec, count)
	for i := 0; i < count; i++ {
		out[i] = g.generateOne(rng, i)
	}
	return out
}

func (g *Generator) generateOne(rng *rand.Rand, idx int) ProductSpec {
	shop := g.shops[idx%len(g.shops)]
	// Pairing (idx / len(shops)) % len(categories) guarantees every shop receives an evenly
	// balanced mix of every category even when gcd(len(shops), len(categories)) > 1.
	cat := g.categories[(idx/len(g.shops))%len(g.categories)]

	poolKey, pool := resolveArchetypePool(cat.Slug, cat.Name)
	arch := pool[(idx/(len(g.shops)*len(g.categories))+rng.IntN(len(pool)))%len(pool)]

	material := arch.Materials[rng.IntN(len(arch.Materials))]
	spec := arch.Specs[rng.IntN(len(arch.Specs))]
	fit := arch.Fits[rng.IntN(len(arch.Fits))]
	occasion := arch.Occasions[rng.IntN(len(arch.Occasions))]
	edition := styleEditions[rng.IntN(len(styleEditions))]

	// Compact Vietnamese product code for unique, realistic SKU/model naming.
	modelTag := fmt.Sprintf("NM%05d", idx+1)
	name := fmt.Sprintf("%s %s %s (%s)", arch.BaseName, edition, shortFeatureLabel(spec), modelTag)

	var description string
	if arch.IsTech {
		description = fmt.Sprintf(
			"%s hoàn thiện từ %s, tích hợp %s. Thiết kế %s, đáp ứng tối ưu nhu cầu %s. %s",
			arch.BaseName, material, spec, fit, occasion, arch.CareGuide,
		)
	} else {
		description = fmt.Sprintf(
			"%s may từ chất liệu %s định lượng %s, mềm mịn và thoáng khí. Thiết kế %s, phù hợp cho nhu cầu %s. %s",
			arch.BaseName, material, spec, fit, occasion, arch.CareGuide,
		)
	}

	skuCount := 1 + rng.IntN(3)
	outOfStock := rng.IntN(100) < 5 // ~5% out-of-stock products

	basePrice := randomPriceVND(rng, arch.MinPriceVND, arch.MaxPriceVND)
	weight := arch.WeightMin
	if arch.WeightMax > arch.WeightMin {
		weight += int32(rng.IntN(int(arch.WeightMax-arch.WeightMin) + 1))
	}

	primaryImg := arch.Images[rng.IntN(len(arch.Images))]
	images := []string{primaryImg}
	if len(arch.Images) > 1 {
		secondImg := arch.Images[(rng.IntN(len(arch.Images)-1)+1)%len(arch.Images)]
		if secondImg != primaryImg {
			images = append(images, secondImg)
		}
	}

	shopCode := shortShopCode(shop, idx%len(g.shops))
	skus := make([]SKUInput, 0, skuCount)
	usedVariants := make(map[string]bool, skuCount)

	for sIdx := 0; sIdx < skuCount; sIdx++ {
		color := arch.Colors[rng.IntN(len(arch.Colors))]
		size := arch.Sizes[rng.IntN(len(arch.Sizes))]
		varKey := color + "|" + size
		for attempts := 0; usedVariants[varKey] && attempts < 8; attempts++ {
			color = arch.Colors[(rng.IntN(len(arch.Colors))+sIdx+attempts)%len(arch.Colors)]
			size = arch.Sizes[(rng.IntN(len(arch.Sizes))+sIdx+attempts)%len(arch.Sizes)]
			varKey = color + "|" + size
		}
		usedVariants[varKey] = true

		skuPrice := basePrice + int64(sIdx)*10000
		if skuPrice > MaxPriceVND {
			skuPrice = MaxPriceVND
		}
		if skuPrice < MinPriceVND {
			skuPrice = MinPriceVND
		}

		var stock int32
		if !outOfStock {
			stock = int32(1 + rng.IntN(int(MaxStock)))
		}

		skus = append(skus, SKUInput{
			SKUCode: fmt.Sprintf("CG-%s-%06d-%02d", shopCode, idx+1, sIdx+1),
			Name:    fmt.Sprintf("%s / %s", color, size),
			Attributes: map[string]string{
				"color": color,
				"size":  size,
			},
			PriceVND:    skuPrice,
			Stock:       stock,
			WeightGrams: weight,
			ImageURL:    primaryImg,
		})
	}

	return ProductSpec{
		Index:       idx,
		ShopID:      shop.ID,
		ShopSlug:    shop.Slug,
		CategoryID:  cat.ID,
		CategoryKey: poolKey,
		Name:        name,
		Description: description,
		Images:      images,
		SKUs:        skus,
	}
}

func resolveArchetypePool(slug, name string) (string, []itemArchetype) {
	s := strings.ToLower(strings.TrimSpace(slug))
	if pool, ok := categoryArchetypes[s]; ok {
		return s, pool
	}
	combined := strings.ToLower(slug + " " + name)
	switch {
	case strings.Contains(combined, "polo"):
		return "ao-polo", categoryArchetypes["ao-polo"]
	case strings.Contains(combined, "short") || strings.Contains(combined, "đùi"):
		return "quan-short", categoryArchetypes["quan-short"]
	case strings.Contains(combined, "jogger") || strings.Contains(combined, "nỉ"):
		return "quan-jogger", categoryArchetypes["quan-jogger"]
	case strings.Contains(combined, "do-lot") || strings.Contains(combined, "đồ lót") || strings.Contains(combined, "boxer"):
		return "do-lot", categoryArchetypes["do-lot"]
	case strings.Contains(combined, "tat") || strings.Contains(combined, "tất") || strings.Contains(combined, "phu-kien") || strings.Contains(combined, "phụ kiện"):
		return "tat-phu-kien", categoryArchetypes["tat-phu-kien"]
	default:
		return "ao-thun", categoryArchetypes["ao-thun"]
	}
}

func randomPriceVND(rng *rand.Rand, minVND, maxVND int64) int64 {
	if minVND < MinPriceVND {
		minVND = MinPriceVND
	}
	if maxVND > MaxPriceVND {
		maxVND = MaxPriceVND
	}
	if maxVND <= minVND {
		return (minVND / PriceStep) * PriceStep
	}
	minSteps := minVND / PriceStep
	maxSteps := maxVND / PriceStep
	steps := minSteps + rng.Int64N(maxSteps-minSteps+1)
	return steps * PriceStep
}

func shortFeatureLabel(spec string) string {
	words := strings.Fields(spec)
	if len(words) <= 3 {
		return spec
	}
	return strings.Join(words[:3], " ")
}

func shortShopCode(shop ShopTarget, fallbackIdx int) string {
	src := strings.ToUpper(strings.TrimSpace(shop.Slug))
	if src == "" {
		src = strings.ToUpper(strings.TrimSpace(shop.ID))
	}
	src = strings.TrimPrefix(src, "SHOP-")
	var b strings.Builder
	for _, r := range src {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() >= 4 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return fmt.Sprintf("S%02d", fallbackIdx+1)
	}
	return b.String()
}
