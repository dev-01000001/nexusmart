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
