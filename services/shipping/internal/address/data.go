package address

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

//go:embed data/vn_admin_2025.json.gz
var embeddedCatalogGZ []byte

// Province represents one of the 34 provincial-level administrative units of Vietnam.
type Province struct {
	Code          string
	Name          string
	Region        string
	GHNProvinceID int
}

// Ward represents a 2-level ward/commune/special zone belonging to a Province, along with carrier & COD metadata.
type Ward struct {
	Code          string
	ProvinceCode  string
	Name          string
	GHNDistrictID int
	GHNWardCode   string
	CODSupported  bool
	IsActive      bool
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

type embeddedCatalogPayload struct {
	Provinces []struct {
		Code          string `json:"code"`
		Name          string `json:"name"`
		Region        string `json:"region"`
		GHNProvinceID int    `json:"ghn_province_id"`
	} `json:"provinces"`
	Wards []struct {
		Code          string `json:"code"`
		ProvinceCode  string `json:"province_code"`
		Name          string `json:"name"`
		GHNDistrictID int    `json:"ghn_district_id"`
		GHNWardCode   string `json:"ghn_ward_code"`
		CODSupported  bool   `json:"cod_supported"`
	} `json:"wards"`
}

var (
	vietnamProvinces []Province
	vietnamWards     []Ward
	provinceRegions  map[string]string
	catalogChecksum  string
)

func init() {
	gzr, err := gzip.NewReader(bytes.NewReader(embeddedCatalogGZ))
	if err != nil {
		panic(fmt.Sprintf("address: open embedded vn_admin_2025.json.gz: %v", err))
	}
	rawJSON, err := io.ReadAll(gzr)
	_ = gzr.Close()
	if err != nil {
		panic(fmt.Sprintf("address: decompress embedded vn_admin_2025.json.gz: %v", err))
	}

	var payload embeddedCatalogPayload
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		panic(fmt.Sprintf("address: unmarshal embedded vn_admin_2025.json.gz: %v", err))
	}

	vietnamProvinces = make([]Province, len(payload.Provinces))
	provinceRegions = make(map[string]string, len(payload.Provinces))
	for i, p := range payload.Provinces {
		vietnamProvinces[i] = Province{
			Code:          p.Code,
			Name:          p.Name,
			Region:        p.Region,
			GHNProvinceID: p.GHNProvinceID,
		}
		provinceRegions[p.Code] = p.Region
	}

	vietnamWards = make([]Ward, len(payload.Wards))
	for i, w := range payload.Wards {
		vietnamWards[i] = Ward{
			Code:          w.Code,
			ProvinceCode:  w.ProvinceCode,
			Name:          w.Name,
			GHNDistrictID: w.GHNDistrictID,
			GHNWardCode:   w.GHNWardCode,
			CODSupported:  w.CODSupported,
			IsActive:      true,
		}
	}

	// Deterministic SHA256 over embedded catalog + legacy mappings so any catalog update triggers re-seed,
	// while unchanged restarts skip upsert entirely (E15).
	h := sha256.New()
	_, _ = h.Write(rawJSON)
	if legacyJSON, mErr := json.Marshal(legacyMappings); mErr == nil {
		_, _ = h.Write(legacyJSON)
	}
	catalogChecksum = hex.EncodeToString(h.Sum(nil))
}

// CatalogChecksum returns the deterministic SHA256 hex digest of the embedded address catalog and legacy mappings.
func CatalogChecksum() string {
	return catalogChecksum
}

// Provinces returns all 34 Vietnam provinces and centrally-run municipalities.
func Provinces() []Province {
	out := make([]Province, len(vietnamProvinces))
	copy(out, vietnamProvinces)
	return out
}

// Wards returns all 3,321 official active wards/communes/special zones per Decision 19/2025/QD-TTg.
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
	if r, ok := provinceRegions[provinceCode]; ok {
		return r
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
		"dac khu ",
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

// legacyMappings covers all 63 pre-2025 provinces/cities plus detailed urban district/ward examples,
// targeting official 5-digit ward codes from Decision 19/2025/QD-TTg.
var legacyMappings = []LegacyMapping{
	// Detailed urban legacy 3-level examples (HCMC, Hanoi, Da Nang, Hai Phong, Can Tho, merged provinces)
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 1", LegacyWard: "Phường Bến Nghé", ProvinceCode: "79", WardCode: "26740", LegacyProvinceCode: "79", LegacyDistrictCode: "760", LegacyWardCode: "26740"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 1", LegacyWard: "Phường Bến Thành", ProvinceCode: "79", WardCode: "26743", LegacyProvinceCode: "79", LegacyDistrictCode: "760", LegacyWardCode: "26743"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận 7", LegacyWard: "Phường Tân Phú", ProvinceCode: "79", WardCode: "27487", LegacyProvinceCode: "79", LegacyDistrictCode: "778", LegacyWardCode: "27487"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Thành phố Thủ Đức", LegacyWard: "Phường Linh Trung", ProvinceCode: "79", WardCode: "26824", LegacyProvinceCode: "79", LegacyDistrictCode: "769", LegacyWardCode: "26815"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", LegacyDistrict: "Quận Bình Thạnh", LegacyWard: "Phường 1", ProvinceCode: "79", WardCode: "26929", LegacyProvinceCode: "79", LegacyDistrictCode: "765", LegacyWardCode: "26905"},
	{LegacyProvince: "Tỉnh Bình Dương", LegacyDistrict: "Thành phố Thủ Dầu Một", LegacyWard: "Phường Phú Cường", ProvinceCode: "79", WardCode: "25747", LegacyProvinceCode: "74", LegacyDistrictCode: "718", LegacyWardCode: "25741"},
	{LegacyProvince: "Tỉnh Bình Dương", LegacyDistrict: "Thành phố Dĩ An", LegacyWard: "Phường Dĩ An", ProvinceCode: "79", WardCode: "25942", LegacyProvinceCode: "74", LegacyDistrictCode: "724", LegacyWardCode: "25942"},
	{LegacyProvince: "Tỉnh Bà Rịa - Vũng Tàu", LegacyDistrict: "Thành phố Vũng Tàu", LegacyWard: "Phường 1", ProvinceCode: "79", WardCode: "26506", LegacyProvinceCode: "77", LegacyDistrictCode: "747", LegacyWardCode: "26506"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Hoàn Kiếm", LegacyWard: "Phường Hàng Trống", ProvinceCode: "01", WardCode: "00070", LegacyProvinceCode: "01", LegacyDistrictCode: "002", LegacyWardCode: "00070"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Ba Đình", LegacyWard: "Phường Điện Biên", ProvinceCode: "01", WardCode: "00004", LegacyProvinceCode: "01", LegacyDistrictCode: "001", LegacyWardCode: "00019"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Cầu Giấy", LegacyWard: "Phường Dịch Vọng", ProvinceCode: "01", WardCode: "00166", LegacyProvinceCode: "01", LegacyDistrictCode: "005", LegacyWardCode: "00166"},
	{LegacyProvince: "Thành phố Hà Nội", LegacyDistrict: "Quận Đống Đa", LegacyWard: "Phường Láng Thượng", ProvinceCode: "01", WardCode: "00235", LegacyProvinceCode: "01", LegacyDistrictCode: "006", LegacyWardCode: "00190"},
	{LegacyProvince: "Thành phố Đà Nẵng", LegacyDistrict: "Quận Hải Châu", LegacyWard: "Phường Hải Châu 1", ProvinceCode: "48", WardCode: "20242", LegacyProvinceCode: "48", LegacyDistrictCode: "492", LegacyWardCode: "20230"},
	{LegacyProvince: "Tỉnh Quảng Nam", LegacyDistrict: "Thành phố Hội An", LegacyWard: "Phường Minh An", ProvinceCode: "48", WardCode: "20410", LegacyProvinceCode: "49", LegacyDistrictCode: "503", LegacyWardCode: "20365"},
	{LegacyProvince: "Tỉnh Quảng Nam", LegacyDistrict: "Thành phố Tam Kỳ", LegacyWard: "Phường An Mỹ", ProvinceCode: "48", WardCode: "20341", LegacyProvinceCode: "49", LegacyDistrictCode: "502", LegacyWardCode: "20335"},
	{LegacyProvince: "Thành phố Hải Phòng", LegacyDistrict: "Quận Hồng Bàng", LegacyWard: "Phường Hoàng Văn Thụ", ProvinceCode: "31", WardCode: "11311", LegacyProvinceCode: "31", LegacyDistrictCode: "303", LegacyWardCode: "11299"},
	{LegacyProvince: "Tỉnh Hải Dương", LegacyDistrict: "Thành phố Hải Dương", LegacyWard: "Phường Trần Hưng Đạo", ProvinceCode: "31", WardCode: "10525", LegacyProvinceCode: "30", LegacyDistrictCode: "288", LegacyWardCode: "10531"},
	{LegacyProvince: "Thành phố Cần Thơ", LegacyDistrict: "Quận Ninh Kiều", LegacyWard: "Phường Tân An", ProvinceCode: "92", WardCode: "31135", LegacyProvinceCode: "92", LegacyDistrictCode: "916", LegacyWardCode: "31117"},
	{LegacyProvince: "Tỉnh Thừa Thiên Huế", LegacyDistrict: "Thành phố Huế", LegacyWard: "Phường Phú Hội", ProvinceCode: "46", WardCode: "19789", LegacyProvinceCode: "46", LegacyDistrictCode: "474", LegacyWardCode: "19813"},

	// Province-level default mappings for ALL 63 legacy provinces (34 continuing + 29 merged)
	{LegacyProvince: "Thành phố Hà Nội", ProvinceCode: "01", WardCode: "00070", LegacyProvinceCode: "01"},
	{LegacyProvince: "Tỉnh Hà Giang", ProvinceCode: "08", WardCode: "00694", LegacyProvinceCode: "02"},
	{LegacyProvince: "Tỉnh Cao Bằng", ProvinceCode: "04", WardCode: "01273", LegacyProvinceCode: "04"},
	{LegacyProvince: "Tỉnh Bắc Kạn", ProvinceCode: "19", WardCode: "01843", LegacyProvinceCode: "06"},
	{LegacyProvince: "Tỉnh Tuyên Quang", ProvinceCode: "08", WardCode: "02215", LegacyProvinceCode: "08"},
	{LegacyProvince: "Tỉnh Lào Cai", ProvinceCode: "15", WardCode: "02647", LegacyProvinceCode: "10"},
	{LegacyProvince: "Tỉnh Điện Biên", ProvinceCode: "11", WardCode: "03127", LegacyProvinceCode: "11"},
	{LegacyProvince: "Tỉnh Lai Châu", ProvinceCode: "12", WardCode: "03408", LegacyProvinceCode: "12"},
	{LegacyProvince: "Tỉnh Sơn La", ProvinceCode: "14", WardCode: "03646", LegacyProvinceCode: "14"},
	{LegacyProvince: "Tỉnh Yên Bái", ProvinceCode: "15", WardCode: "04252", LegacyProvinceCode: "15"},
	{LegacyProvince: "Tỉnh Hòa Bình", ProvinceCode: "25", WardCode: "04795", LegacyProvinceCode: "17"},
	{LegacyProvince: "Tỉnh Thái Nguyên", ProvinceCode: "19", WardCode: "05443", LegacyProvinceCode: "19"},
	{LegacyProvince: "Tỉnh Lạng Sơn", ProvinceCode: "20", WardCode: "05983", LegacyProvinceCode: "20"},
	{LegacyProvince: "Tỉnh Quảng Ninh", ProvinceCode: "22", WardCode: "06685", LegacyProvinceCode: "22"},
	{LegacyProvince: "Tỉnh Bắc Giang", ProvinceCode: "24", WardCode: "07210", LegacyProvinceCode: "24"},
	{LegacyProvince: "Tỉnh Phú Thọ", ProvinceCode: "25", WardCode: "07900", LegacyProvinceCode: "25"},
	{LegacyProvince: "Tỉnh Vĩnh Phúc", ProvinceCode: "25", WardCode: "08707", LegacyProvinceCode: "26"},
	{LegacyProvince: "Tỉnh Bắc Ninh", ProvinceCode: "24", WardCode: "09187", LegacyProvinceCode: "27"},
	{LegacyProvince: "Tỉnh Hải Dương", ProvinceCode: "31", WardCode: "10525", LegacyProvinceCode: "30"},
	{LegacyProvince: "Thành phố Hải Phòng", ProvinceCode: "31", WardCode: "11311", LegacyProvinceCode: "31"},
	{LegacyProvince: "Tỉnh Hưng Yên", ProvinceCode: "33", WardCode: "11953", LegacyProvinceCode: "33"},
	{LegacyProvince: "Tỉnh Thái Bình", ProvinceCode: "33", WardCode: "13225", LegacyProvinceCode: "34"},
	{LegacyProvince: "Tỉnh Hà Nam", ProvinceCode: "37", WardCode: "13285", LegacyProvinceCode: "35"},
	{LegacyProvince: "Tỉnh Nam Định", ProvinceCode: "37", WardCode: "13669", LegacyProvinceCode: "36"},
	{LegacyProvince: "Tỉnh Ninh Bình", ProvinceCode: "37", WardCode: "14329", LegacyProvinceCode: "37"},
	{LegacyProvince: "Tỉnh Thanh Hóa", ProvinceCode: "38", WardCode: "14797", LegacyProvinceCode: "38"},
	{LegacyProvince: "Tỉnh Nghệ An", ProvinceCode: "40", WardCode: "16690", LegacyProvinceCode: "40"},
	{LegacyProvince: "Tỉnh Hà Tĩnh", ProvinceCode: "42", WardCode: "18073", LegacyProvinceCode: "42"},
	{LegacyProvince: "Tỉnh Quảng Bình", ProvinceCode: "44", WardCode: "18880", LegacyProvinceCode: "44"},
	{LegacyProvince: "Tỉnh Quảng Trị", ProvinceCode: "44", WardCode: "19333", LegacyProvinceCode: "45"},
	{LegacyProvince: "Tỉnh Thừa Thiên Huế", ProvinceCode: "46", WardCode: "19753", LegacyProvinceCode: "46"},
	{LegacyProvince: "Thành phố Huế", ProvinceCode: "46", WardCode: "19753", LegacyProvinceCode: "46"},
	{LegacyProvince: "Thành phố Đà Nẵng", ProvinceCode: "48", WardCode: "20242", LegacyProvinceCode: "48"},
	{LegacyProvince: "Tỉnh Quảng Nam", ProvinceCode: "48", WardCode: "20410", LegacyProvinceCode: "49"},
	{LegacyProvince: "Tỉnh Quảng Ngãi", ProvinceCode: "51", WardCode: "21025", LegacyProvinceCode: "51"},
	{LegacyProvince: "Tỉnh Bình Định", ProvinceCode: "52", WardCode: "21583", LegacyProvinceCode: "52"},
	{LegacyProvince: "Tỉnh Phú Yên", ProvinceCode: "66", WardCode: "22015", LegacyProvinceCode: "54"},
	{LegacyProvince: "Tỉnh Khánh Hòa", ProvinceCode: "56", WardCode: "22366", LegacyProvinceCode: "56"},
	{LegacyProvince: "Tỉnh Ninh Thuận", ProvinceCode: "56", WardCode: "22759", LegacyProvinceCode: "58"},
	{LegacyProvince: "Tỉnh Bình Thuận", ProvinceCode: "68", WardCode: "22945", LegacyProvinceCode: "60"},
	{LegacyProvince: "Tỉnh Kon Tum", ProvinceCode: "51", WardCode: "23293", LegacyProvinceCode: "62"},
	{LegacyProvince: "Tỉnh Gia Lai", ProvinceCode: "52", WardCode: "23575", LegacyProvinceCode: "64"},
	{LegacyProvince: "Tỉnh Đắk Lắk", ProvinceCode: "66", WardCode: "24133", LegacyProvinceCode: "66"},
	{LegacyProvince: "Tỉnh Đắk Nông", ProvinceCode: "68", WardCode: "24611", LegacyProvinceCode: "67"},
	{LegacyProvince: "Tỉnh Lâm Đồng", ProvinceCode: "68", WardCode: "24781", LegacyProvinceCode: "68"},
	{LegacyProvince: "Tỉnh Bình Phước", ProvinceCode: "75", WardCode: "25210", LegacyProvinceCode: "70"},
	{LegacyProvince: "Tỉnh Tây Ninh", ProvinceCode: "80", WardCode: "25459", LegacyProvinceCode: "72"},
	{LegacyProvince: "Tỉnh Bình Dương", ProvinceCode: "79", WardCode: "25747", LegacyProvinceCode: "74"},
	{LegacyProvince: "Tỉnh Đồng Nai", ProvinceCode: "75", WardCode: "26041", LegacyProvinceCode: "75"},
	{LegacyProvince: "Tỉnh Bà Rịa - Vũng Tàu", ProvinceCode: "79", WardCode: "26506", LegacyProvinceCode: "77"},
	{LegacyProvince: "Thành phố Hồ Chí Minh", ProvinceCode: "79", WardCode: "26740", LegacyProvinceCode: "79"},
	{LegacyProvince: "Tỉnh Long An", ProvinceCode: "80", WardCode: "27694", LegacyProvinceCode: "80"},
	{LegacyProvince: "Tỉnh Tiền Giang", ProvinceCode: "82", WardCode: "28261", LegacyProvinceCode: "82"},
	{LegacyProvince: "Tỉnh Bến Tre", ProvinceCode: "86", WardCode: "28789", LegacyProvinceCode: "83"},
	{LegacyProvince: "Tỉnh Trà Vinh", ProvinceCode: "86", WardCode: "29242", LegacyProvinceCode: "84"},
	{LegacyProvince: "Tỉnh Vĩnh Long", ProvinceCode: "86", WardCode: "29551", LegacyProvinceCode: "86"},
	{LegacyProvince: "Tỉnh Đồng Tháp", ProvinceCode: "82", WardCode: "29869", LegacyProvinceCode: "87"},
	{LegacyProvince: "Tỉnh An Giang", ProvinceCode: "91", WardCode: "30307", LegacyProvinceCode: "89"},
	{LegacyProvince: "Tỉnh Kiên Giang", ProvinceCode: "91", WardCode: "30742", LegacyProvinceCode: "91"},
	{LegacyProvince: "Thành phố Cần Thơ", ProvinceCode: "92", WardCode: "31135", LegacyProvinceCode: "92"},
	{LegacyProvince: "Tỉnh Hậu Giang", ProvinceCode: "92", WardCode: "31321", LegacyProvinceCode: "93"},
	{LegacyProvince: "Tỉnh Sóc Trăng", ProvinceCode: "92", WardCode: "31507", LegacyProvinceCode: "94"},
	{LegacyProvince: "Tỉnh Bạc Liêu", ProvinceCode: "96", WardCode: "31825", LegacyProvinceCode: "95"},
	{LegacyProvince: "Tỉnh Cà Mau", ProvinceCode: "96", WardCode: "32002", LegacyProvinceCode: "96"},
}
