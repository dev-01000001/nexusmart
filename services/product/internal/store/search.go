package store

import "github.com/nexusmart/core/pkg/common/textnorm"

// FoldVietnameseRune maps a Vietnamese letter (accented or đ/Đ) to its lowercase
// ASCII base letter. Non-Vietnamese runes are returned unchanged.
func FoldVietnameseRune(r rune) rune {
	return textnorm.FoldVietnameseRune(r)
}

// NormalizeSearchText trims, lowercases, folds Vietnamese letters (including đ/Đ),
// and strips combining diacritical marks so accented and unaccented searches match.
func NormalizeSearchText(s string) string {
	return textnorm.NormalizeSearchText(s)
}

// BuildProductSearchText builds the normalized searchable text for a product,
// separating name and description by a newline so phrases do not cross fields.
func BuildProductSearchText(name, description string) string {
	return textnorm.BuildProductSearchText(name, description)
}

// EscapeLikePattern escapes PostgreSQL LIKE wildcards (%, _) and backslash (\).
func EscapeLikePattern(s string) string {
	return textnorm.EscapeLikePattern(s)
}
