package address

import (
	"strings"
	"testing"
)

func TestProvincesCountAndIntegrity(t *testing.T) {
	provinces := Provinces()
	if len(provinces) != 34 {
		t.Fatalf("expected exactly 34 provinces, got %d", len(provinces))
	}

	provMap := make(map[string]Province, len(provinces))
	for _, p := range provinces {
		if p.Code == "" || p.Name == "" || p.Region == "" {
			t.Fatalf("invalid province entry: %+v", p)
		}
		if _, dup := provMap[p.Code]; dup {
			t.Fatalf("duplicate province code: %s", p.Code)
		}
		provMap[p.Code] = p
	}

	wards := Wards()
	if len(wards) != 3321 {
		t.Fatalf("E15: expected exactly 3321 official wards per QD 19/2025/QD-TTg, got %d", len(wards))
	}
	wardByProv := make(map[string]int)
	wardMap := make(map[string]Ward, len(wards))
	codDisabled := make(map[string]string)
	for _, w := range wards {
		if _, ok := provMap[w.ProvinceCode]; !ok {
			t.Fatalf("ward %s references unknown province %s", w.Code, w.ProvinceCode)
		}
		if _, dup := wardMap[w.Code]; dup {
			t.Fatalf("duplicate ward code: %s", w.Code)
		}
		if strings.ContainsAny(w.Name, "\r\n") || strings.TrimSpace(w.Name) == "" {
			t.Fatalf("ward %s has unclean name: %q", w.Code, w.Name)
		}
		if !w.IsActive {
			t.Fatalf("official ward %s (%s) must have IsActive=true", w.Code, w.Name)
		}
		if !w.CODSupported {
			codDisabled[w.Code] = w.Name
		}
		wardMap[w.Code] = w
		wardByProv[w.ProvinceCode]++
	}

	for _, p := range provinces {
		if wardByProv[p.Code] == 0 {
			t.Fatalf("province %s (%s) has no seeded wards", p.Code, p.Name)
		}
	}

	// E16: Only Hoang Sa (20333) and Truong Sa (22736) have cod_supported=false.
	if len(codDisabled) != 2 || codDisabled["20333"] == "" || codDisabled["22736"] == "" {
		t.Fatalf("E16: expected cod_supported=false exclusively for Hoang Sa (20333) and Truong Sa (22736), got %v", codDisabled)
	}

	// Key GHN sandbox mappings preserved on official codes.
	for code, want := range map[string]struct {
		dist int
		ward string
	}{
		"00070": {1489, "1A0606"}, // Hoan Kiem
		"26740": {1442, "20101"},  // Sai Gon
		"26743": {1442, "20102"},  // Ben Thanh
		"20242": {1526, "40101"},  // Hai Chau
		"31135": {1572, "550101"}, // Ninh Kieu
	} {
		w, ok := wardMap[code]
		if !ok || w.GHNDistrictID != want.dist || w.GHNWardCode != want.ward {
			t.Fatalf("expected GHN mapping preserved for %s: got %+v, want dist=%d ward=%s", code, w, want.dist, want.ward)
		}
	}

	if chk := CatalogChecksum(); len(chk) != 64 {
		t.Fatalf("E15: CatalogChecksum() = %q, want 64-hex SHA256", chk)
	}

	legacyProvCodes := make(map[string]bool)
	for _, m := range LegacyMappings() {
		if _, ok := provMap[m.ProvinceCode]; !ok {
			t.Fatalf("legacy mapping %+v references unknown province %s", m, m.ProvinceCode)
		}
		w, ok := wardMap[m.WardCode]
		if !ok {
			t.Fatalf("legacy mapping %+v references unknown ward %s", m, m.WardCode)
		}
		if w.ProvinceCode != m.ProvinceCode {
			t.Fatalf("legacy mapping ward %s belongs to province %s, not %s", m.WardCode, w.ProvinceCode, m.ProvinceCode)
		}
		if m.LegacyProvinceCode != "" {
			legacyProvCodes[m.LegacyProvinceCode] = true
		}
	}

	if len(legacyProvCodes) != 63 {
		t.Fatalf("expected all 63 legacy province codes covered, got %d", len(legacyProvCodes))
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Thành phố Hồ Chí Minh", "ho chi minh"},
		{"TP.HCM", "ho chi minh"},
		{"Tỉnh Bình Dương", "binh duong"},
		{"Quận Hoàn Kiếm", "hoan kiem"},
		{"Phường Bến Nghé", "ben nghe"},
		{"Tỉnh Đắk Lắk", "dak lak"},
		{"Tỉnh Thừa Thiên Huế", "thua thien hue"},
	}

	for _, tc := range cases {
		got := NormalizeName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Extra shorthand & abbreviation normalization.
	extra := []struct {
		in   string
		want string
	}{
		{"Q1", "1"},
		{"Q.1", "1"},
		{"Q7", "7"},
		{"P1", "1"},
		{"P. Bến Nghé", "ben nghe"},
		{"BR-VT", "ba ria vung tau"},
		{"TP.HN", "ha noi"},
		{"TTH", "thua thien hue"},
		{"Đắc Lắc", "dak lak"},
		{"", ""},
	}
	for _, tc := range extra {
		if got := NormalizeName(tc.in); got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMapLegacyAddressAll63ProvincesAndDetailed(t *testing.T) {
	seenCodes := make(map[string]bool)
	for _, m := range LegacyMappings() {
		if m.LegacyDistrict != "" || m.LegacyWard != "" {
			continue
		}
		// Match full accented name.
		res := MapLegacyAddress(m.LegacyProvince, "", "")
		if !res.Found || res.ProvinceCode != m.ProvinceCode || res.WardCode != m.WardCode {
			t.Fatalf("MapLegacyAddress(%q) = %+v, want prov=%s ward=%s", m.LegacyProvince, res, m.ProvinceCode, m.WardCode)
		}
		// Match stripped/unaccented name.
		norm := NormalizeName(m.LegacyProvince)
		resNorm := MapLegacyAddress(norm, "", "")
		if !resNorm.Found || resNorm.ProvinceCode != m.ProvinceCode {
			t.Fatalf("MapLegacyAddress normalized(%q) = %+v, want prov=%s", norm, resNorm, m.ProvinceCode)
		}
		seenCodes[m.LegacyProvinceCode] = true
	}
	if len(seenCodes) != 63 {
		t.Fatalf("expected all 63 legacy provinces verified, got %d", len(seenCodes))
	}

	// Detailed district/ward & abbreviations (official QĐ 19/2025/QĐ-TTg 5-digit ward codes).
	detailedCases := []struct {
		prov, dist, ward string
		wantProv         string
		wantWard         string
	}{
		{"TP.HCM", "Q1", "P. Bến Nghé", "79", "26740"},
		{"Saigon", "Q.7", "Tan Phu", "79", "27487"},
		{"Binh Duong", "TP Thu Dau Mot", "Phu Cuong", "79", "25747"},
		{"BR-VT", "TP. Vung Tau", "P1", "79", "26506"},
		{"HN", "Hoan Kiem", "Hang Trong", "01", "00070"},
		{"Quang Nam", "Hoi An", "", "48", "20410"},
		{"TTH", "Hue", "Phu Hoi", "46", "19789"},
		{"TP.HCM", "", "Ben Thanh", "79", "26743"},
	}
	for _, tc := range detailedCases {
		res := MapLegacyAddress(tc.prov, tc.dist, tc.ward)
		if !res.Found || res.ProvinceCode != tc.wantProv || res.WardCode != tc.wantWard {
			t.Errorf("MapLegacyAddress(%q, %q, %q) = %+v, want prov=%s ward=%s", tc.prov, tc.dist, tc.ward, res, tc.wantProv, tc.wantWard)
		}
		if res.ProvinceName == "" || res.WardName == "" {
			t.Errorf("expected populated province & ward names in %+v", res)
		}
	}

	if miss := MapLegacyAddress("", "", ""); miss.Found {
		t.Fatalf("expected empty province not found, got %+v", miss)
	}
	if miss := MapLegacyAddress("Atlantis", "", ""); miss.Found {
		t.Fatalf("expected unknown province not found, got %+v", miss)
	}
}

func TestProvinceWardAndLegacyFallbackLookups(t *testing.T) {
	if p, ok := ProvinceByCode("79"); !ok || p.Name != "Thành phố Hồ Chí Minh" {
		t.Fatalf("unexpected ProvinceByCode(79): %+v ok=%v", p, ok)
	}
	if ProvinceName("01") != "Thành phố Hà Nội" || ProvinceName("99") != "" || ProvinceName("") != "" {
		t.Fatalf("unexpected ProvinceName lookups")
	}
	if w, ok := WardByCode("26740"); !ok || w.Name != "Phường Sài Gòn" {
		t.Fatalf("unexpected WardByCode(26740): %+v ok=%v", w, ok)
	}
	if WardName("00070") != "Phường Hoàn Kiếm" || WardName("99999") != "" || WardName("") != "" {
		t.Fatalf("unexpected WardName lookups")
	}

	// Detailed legacy reverse fallback (merged province: Binh Duong -> HCMC).
	fbMerged := LookupLegacyFallback("25747")
	if !fbMerged.Found || fbMerged.ProvinceName != "Thành phố Hồ Chí Minh" || fbMerged.WardName != "Phường Thủ Dầu Một" ||
		fbMerged.LegacyProvince != "Tỉnh Bình Dương" || fbMerged.LegacyDistrict != "Thành phố Thủ Dầu Một" {
		t.Fatalf("unexpected LookupLegacyFallback(25747): %+v", fbMerged)
	}

	// Ward without detailed 3-level entry falls back cleanly to province & ward name for district.
	fbSimple := LookupLegacyFallback("01273")
	if !fbSimple.Found || fbSimple.ProvinceName != "Tỉnh Cao Bằng" || fbSimple.LegacyProvince != "Tỉnh Cao Bằng" ||
		fbSimple.WardName == "" || fbSimple.LegacyDistrict != fbSimple.WardName {
		t.Fatalf("unexpected LookupLegacyFallback(01273): %+v", fbSimple)
	}

	if fbUnknown := LookupLegacyFallback("00000"); fbUnknown.Found {
		t.Fatalf("expected unknown ward fallback Found=false, got %+v", fbUnknown)
	}

	if RegionOfProvince("01") != RegionNorth || RegionOfProvince("48") != RegionCentral || RegionOfProvince("79") != RegionSouth || RegionOfProvince("unknown") != RegionCentral {
		t.Fatalf("unexpected RegionOfProvince results")
	}
}

func TestStripDiacritics(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Thành phố Hà Nội", "thanh pho ha noi"},
		{"Ha Noi", "ha noi"},
		{"Thành phố Hồ Chí Minh", "thanh pho ho chi minh"},
		{"Tỉnh Đắk Lắk", "tinh dak lak"},
		{"Phường Bến Thành", "phuong ben thanh"},
		{"Phường Xuân Hương - Đà Lạt", "phuong xuan huong da lat"},
	}
	for _, tc := range cases {
		if got := StripDiacritics(tc.in); got != tc.want {
			t.Errorf("StripDiacritics(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
