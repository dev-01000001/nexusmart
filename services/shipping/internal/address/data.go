package address

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	RegionNorth   = "north"
	RegionCentral = "central"
	RegionSouth   = "south"
)

// Province represents one of the 34 provincial-level administrative units of Vietnam.
type Province struct {
	Code          string
	Name          string
	Region        string
	GHNProvinceID int
}

// Ward represents a 2-level ward/commune belonging to a Province, along with carrier mapping codes.
type Ward struct {
	Code          string
	ProvinceCode  string
	Name          string
	GHNDistrictID int
	GHNWardCode   string
}

// LegacyMapping maps a legacy 3-level (63-province / district / ward) address to the new 2-level structure.
type LegacyMapping struct {
	LegacyProvince     string
	LegacyDistrict     string
	LegacyWard         string
	ProvinceCode       string
	WardCode           string
	LegacyProvinceCode string
	LegacyDistrictCode string
	LegacyWardCode     string
}

// LegacyMatch is the resolved result for MapLegacyAddress.
type LegacyMatch struct {
	Found              bool
	ProvinceCode       string
	ProvinceName       string
	WardCode           string
	WardName           string
	LegacyProvinceCode string
	LegacyDistrictCode string
	LegacyWardCode     string
}

// Provinces returns all 34 Vietnam provinces and centrally-run municipalities.
func Provinces() []Province {
	out := make([]Province, len(vietnamProvinces))
	copy(out, vietnamProvinces)
	return out
}

// Wards returns seeded representative wards across all 34 provinces.
func Wards() []Ward {
	out := make([]Ward, len(vietnamWards))
	copy(out, vietnamWards)
	return out
}

// LegacyMappings returns seeded legacy 63-province/district/ward mappings to the 34-province 2-level structure.
func LegacyMappings() []LegacyMapping {
	out := make([]LegacyMapping, len(legacyMappings))
	copy(out, legacyMappings)
	return out
}

// RegionOfProvince returns the macro-region (north, central, south) for a province code.
func RegionOfProvince(provinceCode string) string {
	for _, p := range vietnamProvinces {
		if p.Code == provinceCode {
			return p.Region
		}
	}
	return RegionCentral
}

// StripDiacritics lowercases input, removes Vietnamese diacritics/d-stroke, and collapses punctuation/whitespace.
// Suitable for accent-insensitive substring search (SHP-B8).
func StripDiacritics(input string) string {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return ""
	}
	// Map Vietnamese d-stroke before NFD decomposition.
	s = strings.ReplaceAll(s, "đ", "d")
	s = strings.ReplaceAll(s, "Đ", "d")

	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	res, _, err := transform.String(t, s)
	if err == nil {
		s = res
	}

	// Replace punctuation separators with spaces.
	replacer := strings.NewReplacer(".", " ", "-", " ", ",", " ", "_", " ")
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeName strips Vietnamese diacritics, lowercases, and strips common administrative prefixes.
func NormalizeName(input string) string {
	s := StripDiacritics(input)
	if s == "" {
		return ""
	}

	// Handle common abbreviations and administrative prefixes.
	aliases := map[string]string{
		"hcm":     "ho chi minh",
		"tphcm":   "ho chi minh",
		"tp hcm":  "ho chi minh",
		"sai gon": "ho chi minh",
		"saigon":  "ho chi minh",
		"hn":      "ha noi",
		"dn":      "da nang",
		"hp":      "hai phong",
		"ct":      "can tho",
		"brvt":    "ba ria vung tau",
		"br vt":   "ba ria vung tau",
	}
	if mapped, ok := aliases[s]; ok {
		return mapped
	}

	prefixes := []string{
		"thanh pho ",
		"tp ",
		"tinh ",
		"quan ",
		"q ",
		"huyen ",
		"h ",
		"thi xa ",
		"tx ",
		"phuong ",
		"p ",
		"xa ",
		"thi tran ",
		"tt ",
	}
	changed := true
	for changed {
		changed = false
		for _, pref := range prefixes {
			if strings.HasPrefix(s, pref) {
				trimmed := strings.TrimSpace(strings.TrimPrefix(s, pref))
				if trimmed != "" {
					s = trimmed
					changed = true
					break
				}
			}
		}
	}
	if mapped, ok := aliases[s]; ok {
		return mapped
	}
	return s
}

var vietnamProvinces = []Province{
	{Code: "01", Name: "Thành phố Hà Nội", Region: RegionNorth, GHNProvinceID: 201},
	{Code: "04", Name: "Tỉnh Cao Bằng", Region: RegionNorth, GHNProvinceID: 247},
	{Code: "08", Name: "Tỉnh Tuyên Quang", Region: RegionNorth, GHNProvinceID: 229},
	{Code: "11", Name: "Tỉnh Điện Biên", Region: RegionNorth, GHNProvinceID: 265},
	{Code: "12", Name: "Tỉnh Lai Châu", Region: RegionNorth, GHNProvinceID: 264},
	{Code: "14", Name: "Tỉnh Sơn La", Region: RegionNorth, GHNProvinceID: 267},
	{Code: "15", Name: "Tỉnh Lào Cai", Region: RegionNorth, GHNProvinceID: 269},
	{Code: "19", Name: "Tỉnh Thái Nguyên", Region: RegionNorth, GHNProvinceID: 244},
	{Code: "20", Name: "Tỉnh Lạng Sơn", Region: RegionNorth, GHNProvinceID: 248},
	{Code: "22", Name: "Tỉnh Quảng Ninh", Region: RegionNorth, GHNProvinceID: 230},
	{Code: "24", Name: "Tỉnh Bắc Ninh", Region: RegionNorth, GHNProvinceID: 249},
	{Code: "25", Name: "Tỉnh Phú Thọ", Region: RegionNorth, GHNProvinceID: 228},
	{Code: "31", Name: "Thành phố Hải Phòng", Region: RegionNorth, GHNProvinceID: 224},
	{Code: "33", Name: "Tỉnh Hưng Yên", Region: RegionNorth, GHNProvinceID: 268},
	{Code: "37", Name: "Tỉnh Ninh Bình", Region: RegionNorth, GHNProvinceID: 233},
	{Code: "38", Name: "Tỉnh Thanh Hóa", Region: RegionCentral, GHNProvinceID: 234},
	{Code: "40", Name: "Tỉnh Nghệ An", Region: RegionCentral, GHNProvinceID: 235},
	{Code: "42", Name: "Tỉnh Hà Tĩnh", Region: RegionCentral, GHNProvinceID: 236},
	{Code: "44", Name: "Tỉnh Quảng Trị", Region: RegionCentral, GHNProvinceID: 238},
	{Code: "46", Name: "Thành phố Huế", Region: RegionCentral, GHNProvinceID: 223},
	{Code: "48", Name: "Thành phố Đà Nẵng", Region: RegionCentral, GHNProvinceID: 203},
	{Code: "51", Name: "Tỉnh Quảng Ngãi", Region: RegionCentral, GHNProvinceID: 242},
	{Code: "52", Name: "Tỉnh Gia Lai", Region: RegionCentral, GHNProvinceID: 259},
	{Code: "56", Name: "Tỉnh Khánh Hòa", Region: RegionCentral, GHNProvinceID: 208},
	{Code: "66", Name: "Tỉnh Đắk Lắk", Region: RegionCentral, GHNProvinceID: 210},
	{Code: "68", Name: "Tỉnh Lâm Đồng", Region: RegionCentral, GHNProvinceID: 209},
	{Code: "75", Name: "Tỉnh Đồng Nai", Region: RegionSouth, GHNProvinceID: 204},
	{Code: "79", Name: "Thành phố Hồ Chí Minh", Region: RegionSouth, GHNProvinceID: 202},
	{Code: "80", Name: "Tỉnh Tây Ninh", Region: RegionSouth, GHNProvinceID: 213},
	{Code: "82", Name: "Tỉnh Đồng Tháp", Region: RegionSouth, GHNProvinceID: 216},
	{Code: "86", Name: "Tỉnh Vĩnh Long", Region: RegionSouth, GHNProvinceID: 215},
	{Code: "91", Name: "Tỉnh An Giang", Region: RegionSouth, GHNProvinceID: 217},
	{Code: "92", Name: "Thành phố Cần Thơ", Region: RegionSouth, GHNProvinceID: 220},
	{Code: "96", Name: "Tỉnh Cà Mau", Region: RegionSouth, GHNProvinceID: 252},
}

var vietnamWards = []Ward{
	// 01 - Hà Nội
	{Code: "01001", ProvinceCode: "01", Name: "Phường Hoàn Kiếm", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
	{Code: "01002", ProvinceCode: "01", Name: "Phường Ba Đình", GHNDistrictID: 1484, GHNWardCode: "1A0101"},
	{Code: "01003", ProvinceCode: "01", Name: "Phường Cầu Giấy", GHNDistrictID: 1485, GHNWardCode: "1A0201"},
	{Code: "01004", ProvinceCode: "01", Name: "Phường Đống Đa", GHNDistrictID: 1486, GHNWardCode: "1A0301"},
	{Code: "01005", ProvinceCode: "01", Name: "Phường Hai Bà Trưng", GHNDistrictID: 1488, GHNWardCode: "1A0501"},
	{Code: "01006", ProvinceCode: "01", Name: "Phường Thanh Xuân", GHNDistrictID: 1493, GHNWardCode: "1A1001"},
	// 04 - Cao Bằng
	{Code: "04001", ProvinceCode: "04", Name: "Phường Thục Phán", GHNDistrictID: 1711, GHNWardCode: "04001"},
	{Code: "04002", ProvinceCode: "04", Name: "Phường Nùng Trí Cao", GHNDistrictID: 1711, GHNWardCode: "04002"},
	{Code: "04003", ProvinceCode: "04", Name: "Xã Trùng Khánh", GHNDistrictID: 1712, GHNWardCode: "04003"},
	// 08 - Tuyên Quang (merged Tuyên Quang + Hà Giang)
	{Code: "08001", ProvinceCode: "08", Name: "Phường Minh Xuân", GHNDistrictID: 1721, GHNWardCode: "08001"},
	{Code: "08002", ProvinceCode: "08", Name: "Phường Hà Giang 1", GHNDistrictID: 1722, GHNWardCode: "08002"},
	{Code: "08003", ProvinceCode: "08", Name: "Xã Đồng Văn", GHNDistrictID: 1723, GHNWardCode: "08003"},
	// 11 - Điện Biên
	{Code: "11001", ProvinceCode: "11", Name: "Phường Điện Biên Phủ", GHNDistrictID: 1731, GHNWardCode: "11001"},
	{Code: "11002", ProvinceCode: "11", Name: "Phường Mường Thanh", GHNDistrictID: 1731, GHNWardCode: "11002"},
	{Code: "11003", ProvinceCode: "11", Name: "Xã Tuần Giáo", GHNDistrictID: 1732, GHNWardCode: "11003"},
	// 12 - Lai Châu
	{Code: "12001", ProvinceCode: "12", Name: "Phường Tân Phong", GHNDistrictID: 1741, GHNWardCode: "12001"},
	{Code: "12002", ProvinceCode: "12", Name: "Phường Đoàn Kết", GHNDistrictID: 1741, GHNWardCode: "12002"},
	{Code: "12003", ProvinceCode: "12", Name: "Xã Tam Đường", GHNDistrictID: 1742, GHNWardCode: "12003"},
	// 14 - Sơn La
	{Code: "14001", ProvinceCode: "14", Name: "Phường Tô Hiệu", GHNDistrictID: 1751, GHNWardCode: "14001"},
	{Code: "14002", ProvinceCode: "14", Name: "Phường Chiềng Cơi", GHNDistrictID: 1751, GHNWardCode: "14002"},
	{Code: "14003", ProvinceCode: "14", Name: "Phường Mộc Châu", GHNDistrictID: 1752, GHNWardCode: "14003"},
	// 15 - Lào Cai (merged Lào Cai + Yên Bái)
	{Code: "15001", ProvinceCode: "15", Name: "Phường Lào Cai", GHNDistrictID: 1761, GHNWardCode: "15001"},
	{Code: "15002", ProvinceCode: "15", Name: "Phường Sa Pa", GHNDistrictID: 1762, GHNWardCode: "15002"},
	{Code: "15003", ProvinceCode: "15", Name: "Phường Yên Bái", GHNDistrictID: 1763, GHNWardCode: "15003"},
	// 19 - Thái Nguyên (merged Thái Nguyên + Bắc Kạn)
	{Code: "19001", ProvinceCode: "19", Name: "Phường Phan Đình Phùng", GHNDistrictID: 1771, GHNWardCode: "19001"},
	{Code: "19002", ProvinceCode: "19", Name: "Phường Sông Công", GHNDistrictID: 1772, GHNWardCode: "19002"},
	{Code: "19003", ProvinceCode: "19", Name: "Phường Bắc Kạn", GHNDistrictID: 1773, GHNWardCode: "19003"},
	// 20 - Lạng Sơn
	{Code: "20001", ProvinceCode: "20", Name: "Phường Lương Văn Tri", GHNDistrictID: 1781, GHNWardCode: "20001"},
	{Code: "20002", ProvinceCode: "20", Name: "Phường Đông Kinh", GHNDistrictID: 1781, GHNWardCode: "20002"},
	{Code: "20003", ProvinceCode: "20", Name: "Xã Đồng Đăng", GHNDistrictID: 1782, GHNWardCode: "20003"},
	// 22 - Quảng Ninh
	{Code: "22001", ProvinceCode: "22", Name: "Phường Hồng Gai", GHNDistrictID: 1791, GHNWardCode: "22001"},
	{Code: "22002", ProvinceCode: "22", Name: "Phường Bãi Cháy", GHNDistrictID: 1791, GHNWardCode: "22002"},
	{Code: "22003", ProvinceCode: "22", Name: "Phường Cẩm Phả", GHNDistrictID: 1792, GHNWardCode: "22003"},
	// 24 - Bắc Ninh (merged Bắc Ninh + Bắc Giang)
	{Code: "24001", ProvinceCode: "24", Name: "Phường Kinh Bắc", GHNDistrictID: 1801, GHNWardCode: "24001"},
	{Code: "24002", ProvinceCode: "24", Name: "Phường Từ Sơn", GHNDistrictID: 1802, GHNWardCode: "24002"},
	{Code: "24003", ProvinceCode: "24", Name: "Phường Bắc Giang", GHNDistrictID: 1803, GHNWardCode: "24003"},
	// 25 - Phú Thọ (merged Phú Thọ + Vĩnh Phúc + Hòa Bình)
	{Code: "25001", ProvinceCode: "25", Name: "Phường Việt Trì", GHNDistrictID: 1811, GHNWardCode: "25001"},
	{Code: "25002", ProvinceCode: "25", Name: "Phường Vĩnh Yên", GHNDistrictID: 1812, GHNWardCode: "25002"},
	{Code: "25003", ProvinceCode: "25", Name: "Phường Hòa Bình", GHNDistrictID: 1813, GHNWardCode: "25003"},
	// 31 - Hải Phòng (merged Hải Phòng + Hải Dương)
	{Code: "31001", ProvinceCode: "31", Name: "Phường Hồng Bàng", GHNDistrictID: 1618, GHNWardCode: "31001"},
	{Code: "31002", ProvinceCode: "31", Name: "Phường Ngô Quyền", GHNDistrictID: 1619, GHNWardCode: "31002"},
	{Code: "31003", ProvinceCode: "31", Name: "Phường Lê Chân", GHNDistrictID: 1620, GHNWardCode: "31003"},
	{Code: "31004", ProvinceCode: "31", Name: "Phường Hải Dương", GHNDistrictID: 1821, GHNWardCode: "31004"},
	// 33 - Hưng Yên (merged Hưng Yên + Thái Bình)
	{Code: "33001", ProvinceCode: "33", Name: "Phường Phố Hiến", GHNDistrictID: 1831, GHNWardCode: "33001"},
	{Code: "33002", ProvinceCode: "33", Name: "Xã Văn Giang", GHNDistrictID: 1832, GHNWardCode: "33002"},
	{Code: "33003", ProvinceCode: "33", Name: "Phường Thái Bình", GHNDistrictID: 1833, GHNWardCode: "33003"},
	// 37 - Ninh Bình (merged Ninh Bình + Hà Nam + Nam Định)
	{Code: "37001", ProvinceCode: "37", Name: "Phường Hoa Lư", GHNDistrictID: 1841, GHNWardCode: "37001"},
	{Code: "37002", ProvinceCode: "37", Name: "Phường Phủ Lý", GHNDistrictID: 1842, GHNWardCode: "37002"},
	{Code: "37003", ProvinceCode: "37", Name: "Phường Nam Định", GHNDistrictID: 1843, GHNWardCode: "37003"},
	// 38 - Thanh Hóa
	{Code: "38001", ProvinceCode: "38", Name: "Phường Hạc Thành", GHNDistrictID: 1851, GHNWardCode: "38001"},
	{Code: "38002", ProvinceCode: "38", Name: "Phường Sầm Sơn", GHNDistrictID: 1852, GHNWardCode: "38002"},
	{Code: "38003", ProvinceCode: "38", Name: "Phường Bỉm Sơn", GHNDistrictID: 1853, GHNWardCode: "38003"},
	// 40 - Nghệ An
	{Code: "40001", ProvinceCode: "40", Name: "Phường Trường Vinh", GHNDistrictID: 1861, GHNWardCode: "40001"},
	{Code: "40002", ProvinceCode: "40", Name: "Phường Thành Vinh", GHNDistrictID: 1861, GHNWardCode: "40002"},
	{Code: "40003", ProvinceCode: "40", Name: "Phường Cửa Lò", GHNDistrictID: 1862, GHNWardCode: "40003"},
	// 42 - Hà Tĩnh
	{Code: "42001", ProvinceCode: "42", Name: "Phường Thành Sen", GHNDistrictID: 1871, GHNWardCode: "42001"},
	{Code: "42002", ProvinceCode: "42", Name: "Phường Bắc Hồng Lĩnh", GHNDistrictID: 1872, GHNWardCode: "42002"},
	{Code: "42003", ProvinceCode: "42", Name: "Phường Sông Trí", GHNDistrictID: 1873, GHNWardCode: "42003"},
	// 44 - Quảng Trị (merged Quảng Trị + Quảng Bình)
	{Code: "44001", ProvinceCode: "44", Name: "Phường Đông Hà", GHNDistrictID: 1881, GHNWardCode: "44001"},
	{Code: "44002", ProvinceCode: "44", Name: "Phường Đồng Hới", GHNDistrictID: 1882, GHNWardCode: "44002"},
	{Code: "44003", ProvinceCode: "44", Name: "Xã Phong Nha", GHNDistrictID: 1883, GHNWardCode: "44003"},
	// 46 - Thành phố Huế
	{Code: "46001", ProvinceCode: "46", Name: "Phường Phú Xuân", GHNDistrictID: 1581, GHNWardCode: "46001"},
	{Code: "46002", ProvinceCode: "46", Name: "Phường Thuận Hóa", GHNDistrictID: 1581, GHNWardCode: "46002"},
	{Code: "46003", ProvinceCode: "46", Name: "Phường Hương Thủy", GHNDistrictID: 1582, GHNWardCode: "46003"},
	// 48 - Thành phố Đà Nẵng (merged Đà Nẵng + Quảng Nam)
	{Code: "48001", ProvinceCode: "48", Name: "Phường Hải Châu", GHNDistrictID: 1526, GHNWardCode: "40101"},
	{Code: "48002", ProvinceCode: "48", Name: "Phường Sơn Trà", GHNDistrictID: 1530, GHNWardCode: "40501"},
	{Code: "48003", ProvinceCode: "48", Name: "Phường Thanh Khê", GHNDistrictID: 1527, GHNWardCode: "40201"},
	{Code: "48004", ProvinceCode: "48", Name: "Phường Ngũ Hành Sơn", GHNDistrictID: 1529, GHNWardCode: "40401"},
	{Code: "48005", ProvinceCode: "48", Name: "Phường Hội An", GHNDistrictID: 1891, GHNWardCode: "48005"},
	{Code: "48006", ProvinceCode: "48", Name: "Phường Tam Kỳ", GHNDistrictID: 1892, GHNWardCode: "48006"},
	// 51 - Quảng Ngãi (merged Quảng Ngãi + Kon Tum)
	{Code: "51001", ProvinceCode: "51", Name: "Phường Cẩm Thành", GHNDistrictID: 1901, GHNWardCode: "51001"},
	{Code: "51002", ProvinceCode: "51", Name: "Phường Kon Tum", GHNDistrictID: 1902, GHNWardCode: "51002"},
	{Code: "51003", ProvinceCode: "51", Name: "Xã Lý Sơn", GHNDistrictID: 1903, GHNWardCode: "51003"},
	// 52 - Gia Lai (merged Gia Lai + Bình Định)
	{Code: "52001", ProvinceCode: "52", Name: "Phường Quy Nhơn", GHNDistrictID: 1911, GHNWardCode: "52001"},
	{Code: "52002", ProvinceCode: "52", Name: "Phường Pleiku", GHNDistrictID: 1912, GHNWardCode: "52002"},
	{Code: "52003", ProvinceCode: "52", Name: "Phường An Nhơn", GHNDistrictID: 1913, GHNWardCode: "52003"},
	// 56 - Khánh Hòa (merged Khánh Hòa + Ninh Thuận)
	{Code: "56001", ProvinceCode: "56", Name: "Phường Nha Trang", GHNDistrictID: 1542, GHNWardCode: "56001"},
	{Code: "56002", ProvinceCode: "56", Name: "Phường Cam Ranh", GHNDistrictID: 1543, GHNWardCode: "56002"},
	{Code: "56003", ProvinceCode: "56", Name: "Phường Phan Rang", GHNDistrictID: 1921, GHNWardCode: "56003"},
	// 66 - Đắk Lắk (merged Đắk Lắk + Phú Yên)
	{Code: "66001", ProvinceCode: "66", Name: "Phường Buôn Ma Thuột", GHNDistrictID: 1552, GHNWardCode: "66001"},
	{Code: "66002", ProvinceCode: "66", Name: "Phường Tuy Hòa", GHNDistrictID: 1931, GHNWardCode: "66002"},
	{Code: "66003", ProvinceCode: "66", Name: "Xã Ea Kar", GHNDistrictID: 1932, GHNWardCode: "66003"},
	// 68 - Lâm Đồng (merged Lâm Đồng + Đắk Nông + Bình Thuận)
	{Code: "68001", ProvinceCode: "68", Name: "Phường Xuân Hương - Đà Lạt", GHNDistrictID: 1550, GHNWardCode: "68001"},
	{Code: "68002", ProvinceCode: "68", Name: "Phường Phan Thiết", GHNDistrictID: 1941, GHNWardCode: "68002"},
	{Code: "68003", ProvinceCode: "68", Name: "Phường Gia Nghĩa", GHNDistrictID: 1942, GHNWardCode: "68003"},
	// 75 - Đồng Nai (merged Đồng Nai + Bình Phước)
	{Code: "75001", ProvinceCode: "75", Name: "Phường Trấn Biên", GHNDistrictID: 1536, GHNWardCode: "75001"},
	{Code: "75002", ProvinceCode: "75", Name: "Phường Long Khánh", GHNDistrictID: 1537, GHNWardCode: "75002"},
	{Code: "75003", ProvinceCode: "75", Name: "Phường Đồng Xoài", GHNDistrictID: 1951, GHNWardCode: "75003"},
	// 79 - Thành phố Hồ Chí Minh (merged TP.HCM + Bình Dương + Bà Rịa - Vũng Tàu)
	{Code: "79001", ProvinceCode: "79", Name: "Phường Sài Gòn", GHNDistrictID: 1442, GHNWardCode: "20101"},
	{Code: "79002", ProvinceCode: "79", Name: "Phường Bến Thành", GHNDistrictID: 1442, GHNWardCode: "20102"},
	{Code: "79003", ProvinceCode: "79", Name: "Phường Tân Mỹ", GHNDistrictID: 1449, GHNWardCode: "20706"},
	{Code: "79004", ProvinceCode: "79", Name: "Phường Thủ Đức", GHNDistrictID: 3695, GHNWardCode: "90737"},
	{Code: "79005", ProvinceCode: "79", Name: "Phường Bình Thạnh", GHNDistrictID: 1462, GHNWardCode: "21601"},
	{Code: "79006", ProvinceCode: "79", Name: "Phường Thủ Dầu Một", GHNDistrictID: 1538, GHNWardCode: "79006"},
	{Code: "79007", ProvinceCode: "79", Name: "Phường Dĩ An", GHNDistrictID: 1539, GHNWardCode: "79007"},
	{Code: "79008", ProvinceCode: "79", Name: "Phường Vũng Tàu", GHNDistrictID: 1540, GHNWardCode: "79008"},
	// 80 - Tây Ninh (merged Tây Ninh + Long An)
	{Code: "80001", ProvinceCode: "80", Name: "Phường Tân Ninh", GHNDistrictID: 1961, GHNWardCode: "80001"},
	{Code: "80002", ProvinceCode: "80", Name: "Phường Long An", GHNDistrictID: 1962, GHNWardCode: "80002"},
	{Code: "80003", ProvinceCode: "80", Name: "Xã Bến Lức", GHNDistrictID: 1963, GHNWardCode: "80003"},
	// 82 - Đồng Tháp (merged Đồng Tháp + Tiền Giang)
	{Code: "82001", ProvinceCode: "82", Name: "Phường Cao Lãnh", GHNDistrictID: 1971, GHNWardCode: "82001"},
	{Code: "82002", ProvinceCode: "82", Name: "Phường Mỹ Tho", GHNDistrictID: 1972, GHNWardCode: "82002"},
	{Code: "82003", ProvinceCode: "82", Name: "Phường Sa Đéc", GHNDistrictID: 1973, GHNWardCode: "82003"},
	// 86 - Vĩnh Long (merged Vĩnh Long + Bến Tre + Trà Vinh)
	{Code: "86001", ProvinceCode: "86", Name: "Phường Long Châu", GHNDistrictID: 1981, GHNWardCode: "86001"},
	{Code: "86002", ProvinceCode: "86", Name: "Phường Bến Tre", GHNDistrictID: 1982, GHNWardCode: "86002"},
	{Code: "86003", ProvinceCode: "86", Name: "Phường Trà Vinh", GHNDistrictID: 1983, GHNWardCode: "86003"},
	// 91 - An Giang (merged An Giang + Kiên Giang)
	{Code: "91001", ProvinceCode: "91", Name: "Phường Long Xuyên", GHNDistrictID: 1991, GHNWardCode: "91001"},
	{Code: "91002", ProvinceCode: "91", Name: "Phường Rạch Giá", GHNDistrictID: 1992, GHNWardCode: "91002"},
	{Code: "91003", ProvinceCode: "91", Name: "Đặc khu Phú Quốc", GHNDistrictID: 1993, GHNWardCode: "91003"},
	// 92 - Thành phố Cần Thơ (merged Cần Thơ + Sóc Trăng + Hậu Giang)
	{Code: "92001", ProvinceCode: "92", Name: "Phường Ninh Kiều", GHNDistrictID: 1572, GHNWardCode: "550101"},
	{Code: "92002", ProvinceCode: "92", Name: "Phường Cái Răng", GHNDistrictID: 1573, GHNWardCode: "550201"},
	{Code: "92003", ProvinceCode: "92", Name: "Phường Sóc Trăng", GHNDistrictID: 2001, GHNWardCode: "92003"},
	{Code: "92004", ProvinceCode: "92", Name: "Phường Vị Thanh", GHNDistrictID: 2002, GHNWardCode: "92004"},
	// 96 - Cà Mau (merged Cà Mau + Bạc Liêu)
	{Code: "96001", ProvinceCode: "96", Name: "Phường An Xuyên", GHNDistrictID: 2011, GHNWardCode: "96001"},
	{Code: "96002", ProvinceCode: "96", Name: "Phường Bạc Liêu", GHNDistrictID: 2012, GHNWardCode: "96002"},
	{Code: "96003", ProvinceCode: "96", Name: "Xã Năm Căn", GHNDistrictID: 2013, GHNWardCode: "96003"},
}

// legacyMappings covers all 63 pre-2025 provinces/cities plus detailed urban district/ward examples.
var legacyMappings = []LegacyMapping{
	// Detailed urban legacy 3-level examples (HCMC, Hanoi, Da Nang, Hai Phong, Can Tho, merged provinces)
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 1", LegacyWard: "Phường Bến Nghé", ProvinceCode: "79", WardCode: "79001", LegacyProvinceCode: "79", LegacyDistrictCode: "760", LegacyWardCode: "26740"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 1", LegacyWard: "Phường Bến Thành", ProvinceCode: "79", WardCode: "79002", LegacyProvinceCode: "79", LegacyDistrictCode: "760", LegacyWardCode: "26743"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 7", LegacyWard: "Phường Tân Phú", ProvinceCode: "79", WardCode: "79003", LegacyProvinceCode: "79", LegacyDistrictCode: "778", LegacyWardCode: "27487"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Thành phố Thủ Đức", LegacyWard: "Phường Linh Trung", ProvinceCode: "79", WardCode: "79004", LegacyProvinceCode: "79", LegacyDistrictCode: "769", LegacyWardCode: "26815"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận Bình Thạnh", LegacyWard: "Phường 1", ProvinceCode: "79", WardCode: "79005", LegacyProvinceCode: "79", LegacyDistrictCode: "765", LegacyWardCode: "26905"},
	{LegacyProvince: "Tỉnh Bình Dương", LegacyDistrict: "Thành phố Thủ Dầu Một", LegacyWard: "Phường Phú Cường", ProvinceCode: "79", WardCode: "79006", LegacyProvinceCode: "74", LegacyDistrictCode: "718", LegacyWardCode: "25741"},
	{LegacyProvince: "Tỉnh Bình Dương", LegacyDistrict: "Thành phố Dĩ An", LegacyWard: "Phường Dĩ An", ProvinceCode: "79", WardCode: "79007", LegacyProvinceCode: "74", LegacyDistrictCode: "724", LegacyWardCode: "25942"},
	{LegacyProvince: "Tỉnh Bà Rịa - Vũng Tàu", LegacyDistrict: "Thành phố Vũng Tàu", LegacyWard: "Phường 1", ProvinceCode: "79", WardCode: "79008", LegacyProvinceCode: "77", LegacyDistrictCode: "747", LegacyWardCode: "26506"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Hoàn Kiếm", LegacyWard: "Phường Hàng Trống", ProvinceCode: "01", WardCode: "01001", LegacyProvinceCode: "01", LegacyDistrictCode: "002", LegacyWardCode: "00070"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Ba Đình", LegacyWard: "Phường Điện Biên", ProvinceCode: "01", WardCode: "01002", LegacyProvinceCode: "01", LegacyDistrictCode: "001", LegacyWardCode: "00019"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Cầu Giấy", LegacyWard: "Phường Dịch Vọng", ProvinceCode: "01", WardCode: "01003", LegacyProvinceCode: "01", LegacyDistrictCode: "005", LegacyWardCode: "00166"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Đống Đa", LegacyWard: "Phường Láng Thượng", ProvinceCode: "01", WardCode: "01004", LegacyProvinceCode: "01", LegacyDistrictCode: "006", LegacyWardCode: "00190"},
	{LegacyProvince: "Thành phố Đà Nẵng", LegacyDistrict: "Quận Hải Châu", LegacyWard: "Phường Hải Châu 1", ProvinceCode: "48", WardCode: "48001", LegacyProvinceCode: "48", LegacyDistrictCode: "492", LegacyWardCode: "20230"},
	{LegacyProvince: "Tỉnh Quảng Nam", LegacyDistrict: "Thành phố Hội An", LegacyWard: "Phường Minh An", ProvinceCode: "48", WardCode: "48005", LegacyProvinceCode: "49", LegacyDistrictCode: "503", LegacyWardCode: "20365"},
	{LegacyProvince: "Tỉnh Quảng Nam", LegacyDistrict: "Thành phố Tam Kỳ", LegacyWard: "Phường An Mỹ", ProvinceCode: "48", WardCode: "48006", LegacyProvinceCode: "49", LegacyDistrictCode: "502", LegacyWardCode: "20335"},
	{LegacyProvince: "Thành phố Hải Phòng", LegacyDistrict: "Quận Hồng Bàng", LegacyWard: "Phường Hoàng Văn Thụ", ProvinceCode: "31", WardCode: "31001", LegacyProvinceCode: "31", LegacyDistrictCode: "303", LegacyWardCode: "11299"},
	{LegacyProvince: "Tỉnh Hải Dương", LegacyDistrict: "Thành phố Hải Dương", LegacyWard: "Phường Trần Hưng Đạo", ProvinceCode: "31", WardCode: "31004", LegacyProvinceCode: "30", LegacyDistrictCode: "288", LegacyWardCode: "10531"},
	{LegacyProvince: "Thành phố Cần Thơ", LegacyDistrict: "Quận Ninh Kiều", LegacyWard: "Phường Tân An", ProvinceCode: "92", WardCode: "92001", LegacyProvinceCode: "92", LegacyDistrictCode: "916", LegacyWardCode: "31117"},
	{LegacyProvince: "Tỉnh Thừa Thiên Huế", LegacyDistrict: "Thành phố Huế", LegacyWard: "Phường Phú Hội", ProvinceCode: "46", WardCode: "46002", LegacyProvinceCode: "46", LegacyDistrictCode: "474", LegacyWardCode: "19813"},

	// Province-level default mappings for ALL 63 legacy provinces (34 continuing + 29 merged)
	{LegacyProvince: "Thành phố Hà Nội", ProvinceCode: "01", WardCode: "01001", LegacyProvinceCode: "01"},
	{LegacyProvince: "Tỉnh Hà Giang", ProvinceCode: "08", WardCode: "08002", LegacyProvinceCode: "02"},
	{LegacyProvince: "Tỉnh Cao Bằng", ProvinceCode: "04", WardCode: "04001", LegacyProvinceCode: "04"},
	{LegacyProvince: "Tỉnh Bắc Kạn", ProvinceCode: "19", WardCode: "19003", LegacyProvinceCode: "06"},
	{LegacyProvince: "Tỉnh Tuyên Quang", ProvinceCode: "08", WardCode: "08001", LegacyProvinceCode: "08"},
	{LegacyProvince: "Tỉnh Lào Cai", ProvinceCode: "15", WardCode: "15001", LegacyProvinceCode: "10"},
	{LegacyProvince: "Tỉnh Điện Biên", ProvinceCode: "11", WardCode: "11001", LegacyProvinceCode: "11"},
	{LegacyProvince: "Tỉnh Lai Châu", ProvinceCode: "12", WardCode: "12001", LegacyProvinceCode: "12"},
	{LegacyProvince: "Tỉnh Sơn La", ProvinceCode: "14", WardCode: "14001", LegacyProvinceCode: "14"},
	{LegacyProvince: "Tỉnh Yên Bái", ProvinceCode: "15", WardCode: "15003", LegacyProvinceCode: "15"},
	{LegacyProvince: "Tỉnh Hòa Bình", ProvinceCode: "25", WardCode: "25003", LegacyProvinceCode: "17"},
	{LegacyProvince: "Tỉnh Thái Nguyên", ProvinceCode: "19", WardCode: "19001", LegacyProvinceCode: "19"},
	{LegacyProvince: "Tỉnh Lạng Sơn", ProvinceCode: "20", WardCode: "20001", LegacyProvinceCode: "20"},
	{LegacyProvince: "Tỉnh Quảng Ninh", ProvinceCode: "22", WardCode: "22001", LegacyProvinceCode: "22"},
	{LegacyProvince: "Tỉnh Bắc Giang", ProvinceCode: "24", WardCode: "24003", LegacyProvinceCode: "24"},
	{LegacyProvince: "Tỉnh Phú Thọ", ProvinceCode: "25", WardCode: "25001", LegacyProvinceCode: "25"},
	{LegacyProvince: "Tỉnh Vĩnh Phúc", ProvinceCode: "25", WardCode: "25002", LegacyProvinceCode: "26"},
	{LegacyProvince: "Tỉnh Bắc Ninh", ProvinceCode: "24", WardCode: "24001", LegacyProvinceCode: "27"},
	{LegacyProvince: "Tỉnh Hải Dương", ProvinceCode: "31", WardCode: "31004", LegacyProvinceCode: "30"},
	{LegacyProvince: "Thành phố Hải Phòng", ProvinceCode: "31", WardCode: "31001", LegacyProvinceCode: "31"},
	{LegacyProvince: "Tỉnh Hưng Yên", ProvinceCode: "33", WardCode: "33001", LegacyProvinceCode: "33"},
	{LegacyProvince: "Tỉnh Thái Bình", ProvinceCode: "33", WardCode: "33003", LegacyProvinceCode: "34"},
	{LegacyProvince: "Tỉnh Hà Nam", ProvinceCode: "37", WardCode: "37002", LegacyProvinceCode: "35"},
	{LegacyProvince: "Tỉnh Nam Định", ProvinceCode: "37", WardCode: "37003", LegacyProvinceCode: "36"},
	{LegacyProvince: "Tỉnh Ninh Bình", ProvinceCode: "37", WardCode: "37001", LegacyProvinceCode: "37"},
	{LegacyProvince: "Tỉnh Thanh Hóa", ProvinceCode: "38", WardCode: "38001", LegacyProvinceCode: "38"},
	{LegacyProvince: "Tỉnh Nghệ An", ProvinceCode: "40", WardCode: "40001", LegacyProvinceCode: "40"},
	{LegacyProvince: "Tỉnh Hà Tĩnh", ProvinceCode: "42", WardCode: "42001", LegacyProvinceCode: "42"},
	{LegacyProvince: "Tỉnh Quảng Bình", ProvinceCode: "44", WardCode: "44002", LegacyProvinceCode: "44"},
	{LegacyProvince: "Tỉnh Quảng Trị", ProvinceCode: "44", WardCode: "44001", LegacyProvinceCode: "45"},
	{LegacyProvince: "Tỉnh Thừa Thiên Huế", ProvinceCode: "46", WardCode: "46001", LegacyProvinceCode: "46"},
	{LegacyProvince: "Thành phố Huế", ProvinceCode: "46", WardCode: "46001", LegacyProvinceCode: "46"},
	{LegacyProvince: "Thành phố Đà Nẵng", ProvinceCode: "48", WardCode: "48001", LegacyProvinceCode: "48"},
	{LegacyProvince: "Tỉnh Quảng Nam", ProvinceCode: "48", WardCode: "48005", LegacyProvinceCode: "49"},
	{LegacyProvince: "Tỉnh Quảng Ngãi", ProvinceCode: "51", WardCode: "51001", LegacyProvinceCode: "51"},
	{LegacyProvince: "Tỉnh Bình Định", ProvinceCode: "52", WardCode: "52001", LegacyProvinceCode: "52"},
	{LegacyProvince: "Tỉnh Phú Yên", ProvinceCode: "66", WardCode: "66002", LegacyProvinceCode: "54"},
	{LegacyProvince: "Tỉnh Khánh Hòa", ProvinceCode: "56", WardCode: "56001", LegacyProvinceCode: "56"},
	{LegacyProvince: "Tỉnh Ninh Thuận", ProvinceCode: "56", WardCode: "56003", LegacyProvinceCode: "58"},
	{LegacyProvince: "Tỉnh Bình Thuận", ProvinceCode: "68", WardCode: "68002", LegacyProvinceCode: "60"},
	{LegacyProvince: "Tỉnh Kon Tum", ProvinceCode: "51", WardCode: "51002", LegacyProvinceCode: "62"},
	{LegacyProvince: "Tỉnh Gia Lai", ProvinceCode: "52", WardCode: "52002", LegacyProvinceCode: "64"},
	{LegacyProvince: "Tỉnh Đắk Lắk", ProvinceCode: "66", WardCode: "66001", LegacyProvinceCode: "66"},
	{LegacyProvince: "Tỉnh Đắk Nông", ProvinceCode: "68", WardCode: "68003", LegacyProvinceCode: "67"},
	{LegacyProvince: "Tỉnh Lâm Đồng", ProvinceCode: "68", WardCode: "68001", LegacyProvinceCode: "68"},
	{LegacyProvince: "Tỉnh Bình Phước", ProvinceCode: "75", WardCode: "75003", LegacyProvinceCode: "70"},
	{LegacyProvince: "Tỉnh Tây Ninh", ProvinceCode: "80", WardCode: "80001", LegacyProvinceCode: "72"},
	{LegacyProvince: "Tỉnh Bình Dương", ProvinceCode: "79", WardCode: "79006", LegacyProvinceCode: "74"},
	{LegacyProvince: "Tỉnh Đồng Nai", ProvinceCode: "75", WardCode: "75001", LegacyProvinceCode: "75"},
	{LegacyProvince: "Tỉnh Bà Rịa - Vũng Tàu", ProvinceCode: "79", WardCode: "79008", LegacyProvinceCode: "77"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", ProvinceCode: "79", WardCode: "79001", LegacyProvinceCode: "79"},
	{LegacyProvince: "Tỉnh Long An", ProvinceCode: "80", WardCode: "80002", LegacyProvinceCode: "80"},
	{LegacyProvince: "Tỉnh Tiền Giang", ProvinceCode: "82", WardCode: "82002", LegacyProvinceCode: "82"},
	{LegacyProvince: "Tỉnh Bến Tre", ProvinceCode: "86", WardCode: "86002", LegacyProvinceCode: "83"},
	{LegacyProvince: "Tỉnh Trà Vinh", ProvinceCode: "86", WardCode: "86003", LegacyProvinceCode: "84"},
	{LegacyProvince: "Tỉnh Vĩnh Long", ProvinceCode: "86", WardCode: "86001", LegacyProvinceCode: "86"},
	{LegacyProvince: "Tỉnh Đồng Tháp", ProvinceCode: "82", WardCode: "82001", LegacyProvinceCode: "87"},
	{LegacyProvince: "Tỉnh An Giang", ProvinceCode: "91", WardCode: "91001", LegacyProvinceCode: "89"},
	{LegacyProvince: "Tỉnh Kiên Giang", ProvinceCode: "91", WardCode: "91002", LegacyProvinceCode: "91"},
	{LegacyProvince: "Thành phố Cần Thơ", ProvinceCode: "92", WardCode: "92001", LegacyProvinceCode: "92"},
	{LegacyProvince: "Tỉnh Hậu Giang", ProvinceCode: "92", WardCode: "92004", LegacyProvinceCode: "93"},
	{LegacyProvince: "Tỉnh Sóc Trăng", ProvinceCode: "92", WardCode: "92003", LegacyProvinceCode: "94"},
	{LegacyProvince: "Tỉnh Bạc Liêu", ProvinceCode: "96", WardCode: "96002", LegacyProvinceCode: "95"},
	{LegacyProvince: "Tỉnh Cà Mau", ProvinceCode: "96", WardCode: "96001", LegacyProvinceCode: "96"},
}
