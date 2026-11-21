package normalize

import (
	"reflect"
	"testing"
)

func TestNormalizeVietnamese(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "standard Vietnamese phrase with d-stroke",
			in:   "Áo thun thể thao đẹp",
			want: "ao thun the thao dep",
		},
		{
			name: "uppercase D-stroke and tone marks",
			in:   "ĐÈN BÀN HỌC CHỐNG CẬN ĐẸP",
			want: "den ban hoc chong can dep",
		},
		{
			name: "collapse tabs and extra whitespace",
			in:   "  Quần\t\nđùi   chạy   bộ  ",
			want: "quan dui chay bo",
		},
		{
			name: "already folded matches accented",
			in:   "ao thun the thao dep",
			want: "ao thun the thao dep",
		},
		{
			name: "preserve SKU hyphens",
			in:   "SKU-ÁO-001",
			want: "sku-ao-001",
		},
		{
			name: "empty and whitespace only",
			in:   "   \t\n ",
			want: "",
		},
		{
			name: "complex Vietnamese vowels",
			in:   "Ước gì có chiếc giường êm ái ở Đà Nẵng",
			want: "uoc gi co chiec giuong em ai o da nang",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeVietnamese(tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeVietnamese(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	if NormalizeVietnamese("Áo thun thể thao đẹp") != NormalizeVietnamese("ao thun the thao dep") {
		t.Fatal("expected accented and unaccented Vietnamese strings to normalize identically")
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("Áo thun (Thể thao) - Đẹp, 100% cotton!")
	want := []string{"ao", "thun", "the", "thao", "dep", "100", "cotton"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize() = %v, want %v", got, want)
	}
	if len(Tokenize("   ")) != 0 {
		t.Fatal("expected empty token list for whitespace")
	}
}

func TestIsSKUOrShortQuery(t *testing.T) {
	tests := []struct {
		q    string
		want bool
	}{
		{"", false},
		{"a", true},
		{"áo", true},
		{"SKU-1234", true},
		{"AT_001", true},
		{"SP2026", true},
		{"12345", true},
		{"áo thun", false},
		{"giày chạy bộ", false},
		{"đèn bàn học", false},
		{"laptop", false},
		{"áo khoác gió 2 lớp", false},
	}

	for _, tc := range tests {
		if got := IsSKUOrShortQuery(tc.q); got != tc.want {
			t.Errorf("IsSKUOrShortQuery(%q) = %v, want %v", tc.q, got, tc.want)
		}
	}
}
