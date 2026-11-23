package store

import "testing"

func TestNormalizeSearchText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace_only", "   \t\n ", ""},
		{"ao_thun", "Áo thun Cotton Compact", "ao thun cotton compact"},
		{"dam_d_stroke_upper", "Đầm Linen Dáng Suông", "dam linen dang suong"},
		{"dam_d_stroke_lower", "đầm đũi đỏ", "dam dui do"},
		{"all_vowels_lower", "áàảãạăắằẳẵặâấầẩẫậđéèẻẽẹêếềểễệíìỉĩịóòỏõọôốồổỗộơớờởỡợúùủũụưứừửữựýỳỷỹỵ", "aaaaaaaaaaaaaaaaadeeeeeeeeeeeiiiiiooooooooooooooooouuuuuuuuuuuyyyyy"},
		{"all_vowels_upper", "ÁÀẢÃẠĂẮẰẲẴẶÂẤẦẨẪẬĐÉÈẺẼẸÊẾỀỂỄỆÍÌỈĨỊÓÒỎÕỌÔỐỒỔỖỘƠỚỜỞỠỢÚÙỦŨỤƯỨỪỬỮỰÝỲỶỸỴ", "aaaaaaaaaaaaaaaaadeeeeeeeeeeeiiiiiooooooooooooooooouuuuuuuuuuuyyyyy"},
		{"nfd_decomposed_marks", "A\u0301o thun Đâ\u0300m u\u031b\u0301", "ao thun dam u"},
		{"preserves_digits_and_symbols", "Giảm 50% chất_liệu \\ cotton", "giam 50% chat_lieu \\ cotton"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeSearchText(tc.in); got != tc.want {
				t.Fatalf("NormalizeSearchText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildProductSearchText(t *testing.T) {
	if got := BuildProductSearchText("Áo thun", "Đẹp mềm"); got != "ao thun\ndep mem" {
		t.Fatalf("unexpected BuildProductSearchText: %q", got)
	}
	if got := BuildProductSearchText("Đầm", ""); got != "dam" {
		t.Fatalf("unexpected BuildProductSearchText with empty desc: %q", got)
	}
}

func TestEscapeLikePattern(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ao thun", "ao thun"},
		{"%", `\%`},
		{"_", `\_`},
		{`\`, `\\`},
		{`50%_a\b`, `50\%\_a\\b`},
	}
	for _, tc := range cases {
		if got := EscapeLikePattern(tc.in); got != tc.want {
			t.Fatalf("EscapeLikePattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
