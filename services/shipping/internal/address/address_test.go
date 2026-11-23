package address

import "testing"

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
	wardByProv := make(map[string]int)
	wardMap := make(map[string]Ward, len(wards))
	for _, w := range wards {
		if _, ok := provMap[w.ProvinceCode]; !ok {
			t.Fatalf("ward %s references unknown province %s", w.Code, w.ProvinceCode)
		}
		if _, dup := wardMap[w.Code]; dup {
			t.Fatalf("duplicate ward code: %s", w.Code)
		}
		wardMap[w.Code] = w
		wardByProv[w.ProvinceCode]++
	}

	for _, p := range provinces {
		if wardByProv[p.Code] == 0 {
			t.Fatalf("province %s (%s) has no seeded wards", p.Code, p.Name)
		}
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
