package textnorm

import (
	"strings"
	"unicode"
)

// FoldVietnameseRune maps a Vietnamese letter (accented vowel or đ/Đ) to its lowercase
// ASCII base letter. Non-Vietnamese runes are returned unchanged.
func FoldVietnameseRune(r rune) rune {
	switch r {
	case 'á', 'à', 'ả', 'ã', 'ạ', 'ă', 'ắ', 'ằ', 'ẳ', 'ẵ', 'ặ', 'â', 'ấ', 'ầ', 'ẩ', 'ẫ', 'ậ',
		'Á', 'À', 'Ả', 'Ã', 'Ạ', 'Ă', 'Ắ', 'Ằ', 'Ẳ', 'Ẵ', 'Ặ', 'Â', 'Ấ', 'Ầ', 'Ẩ', 'Ẫ', 'Ậ':
		return 'a'
	case 'đ', 'Đ':
		return 'd'
	case 'é', 'è', 'ẻ', 'ẽ', 'ẹ', 'ê', 'ế', 'ề', 'ể', 'ễ', 'ệ',
		'É', 'È', 'Ẻ', 'Ẽ', 'Ẹ', 'Ê', 'Ế', 'Ề', 'Ể', 'Ễ', 'Ệ':
		return 'e'
	case 'í', 'ì', 'ỉ', 'ĩ', 'ị',
		'Í', 'Ì', 'Ỉ', 'Ĩ', 'Ị':
		return 'i'
	case 'ó', 'ò', 'ỏ', 'õ', 'ọ', 'ô', 'ố', 'ồ', 'ổ', 'ỗ', 'ộ', 'ơ', 'ớ', 'ờ', 'ở', 'ỡ', 'ợ',
		'Ó', 'Ò', 'Ỏ', 'Õ', 'Ọ', 'Ô', 'Ố', 'Ồ', 'Ổ', 'Ỗ', 'Ộ', 'Ơ', 'Ớ', 'Ờ', 'Ở', 'Ỡ', 'Ợ':
		return 'o'
	case 'ú', 'ù', 'ủ', 'ũ', 'ụ', 'ư', 'ứ', 'ừ', 'ử', 'ữ', 'ự',
		'Ú', 'Ù', 'Ủ', 'Ũ', 'Ụ', 'Ư', 'Ứ', 'Ừ', 'Ử', 'Ữ', 'Ự':
		return 'u'
	case 'ý', 'ỳ', 'ỷ', 'ỹ', 'ỵ',
		'Ý', 'Ỳ', 'Ỷ', 'Ỹ', 'Ỵ':
		return 'y'
	default:
		return r
	}
}

// NormalizeSearchText trims, lowercases, folds Vietnamese letters (including đ/Đ),
// and strips combining diacritical marks (unicode.Mn) so accented and unaccented queries match.
func NormalizeSearchText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(FoldVietnameseRune(r))
	}
	return b.String()
}

// BuildProductSearchText builds the normalized searchable text for a product,
// separating name and description by a newline so phrases do not cross fields.
func BuildProductSearchText(name, description string) string {
	n := NormalizeSearchText(name)
	d := NormalizeSearchText(description)
	if d == "" {
		return n
	}
	if n == "" {
		return d
	}
	return n + "\n" + d
}

// EscapeLikePattern escapes PostgreSQL LIKE wildcards (%, _) and backslash (\).
func EscapeLikePattern(s string) string {
	return likeEscaper.Replace(s)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
