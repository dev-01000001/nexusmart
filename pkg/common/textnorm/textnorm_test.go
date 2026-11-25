package textnorm

import "testing"

func TestFoldVietnameseRuneCharacterByCharacter(t *testing.T) {
	groups := []struct {
		want  rune
		runes string
	}{
		{'a', "áàảãạăắằẳẵặâấầẩẫậÁÀẢÃẠĂẮẰẲẴẶÂẤẦẨẪẬ"},
		{'d', "đĐ"},
		{'e', "éèẻẽẹêếềểễệÉÈẺẼẸÊẾỀỂỄỆ"},
		{'i', "íìỉĩịÍÌỈĨỊ"},
		{'o', "óòỏõọôốồổỗộơớờởỡợÓÒỎÕỌÔỐỒỔỖỘƠỚỜỞỠỢ"},
		{'u', "úùủũụưứừửữựÚÙỦŨỤƯỨỪỬỮỰ"},
		{'y', "ýỳỷỹỵÝỲỶỸỴ"},
	}

	seen := make(map[rune]bool)
	for _, g := range groups {
		for _, r := range g.runes {
			seen[r] = true
			if got := FoldVietnameseRune(r); got != g.want {
				t.Errorf("FoldVietnameseRune(%q U+%04X) = %q, want %q", r, r, got, g.want)
			}
		}
	}
	if len(seen) != 134 {
		t.Fatalf("expected 134 Vietnamese precomposed runes covered (67 lower + 67 upper), got %d", len(seen))
	}

	// ASCII printable characters and unrelated Unicode runes must pass through unchanged.
	for r := rune(0x20); r <= 0x7E; r++ {
		if got := FoldVietnameseRune(r); got != r {
			t.Errorf("FoldVietnameseRune(ASCII %q) = %q, want unchanged", r, got)
		}
	}
	for _, r := range []rune{'ñ', 'ü', 'ç', 'ß', '中', '₫'} {
		if got := FoldVietnameseRune(r); got != r {
			t.Errorf("FoldVietnameseRune(%q U+%04X) = %q, want unchanged", r, r, got)
		}
	}
}

func TestNormalizeSearchTextMatchesProductStoreBehavior(t *testing.T) {
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
		{"sku_code_and_dashes", "  NM-AO-THUN-01  ", "nm-ao-thun-01"},
		{"duong_d_vs_d_stroke", "Đường Nguyễn Huệ", "duong nguyen hue"},
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
		t.Fatalf("BuildProductSearchText = %q, want %q", got, "ao thun\ndep mem")
	}
	if got := BuildProductSearchText("Đầm", ""); got != "dam" {
		t.Fatalf("BuildProductSearchText empty desc = %q, want %q", got, "dam")
	}
	if got := BuildProductSearchText("", "Đầm đỏ"); got != "dam do" {
		t.Fatalf("BuildProductSearchText empty name = %q, want %q", got, "dam do")
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
