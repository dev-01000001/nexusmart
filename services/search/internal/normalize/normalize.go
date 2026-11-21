package normalize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var vietReplacer = strings.NewReplacer("đ", "d", "Đ", "d")

// NormalizeVietnamese folds Vietnamese diacritics and đ/Đ to ASCII equivalents,
// lowercases all runes, and collapses contiguous whitespace into single spaces.
func NormalizeVietnamese(s string) string {
	if s == "" {
		return ""
	}
	replaced := vietReplacer.Replace(s)
	decomposed := norm.NFD.String(replaced)

	var b strings.Builder
	b.Grow(len(decomposed))

	needSpace := false
	wroteAny := false

	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsSpace(r) {
			if wroteAny {
				needSpace = true
			}
			continue
		}
		if needSpace {
			b.WriteByte(' ')
			needSpace = false
		}
		b.WriteRune(unicode.ToLower(r))
		wroteAny = true
	}
	return b.String()
}

// Tokenize normalizes s and splits it into alphanumeric words.
func Tokenize(s string) []string {
	folded := NormalizeVietnamese(s)
	if folded == "" {
		return nil
	}
	return strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// IsSKUOrShortQuery reports whether a query is better served by pure keyword search
// (very short prefix queries or single-token SKU codes) rather than hybrid vector fusion.
func IsSKUOrShortQuery(q string) bool {
	folded := NormalizeVietnamese(q)
	if folded == "" {
		return false
	}
	if utf8.RuneCountInString(folded) <= 2 {
		return true
	}
	if strings.ContainsRune(folded, ' ') {
		return false
	}

	var hasDigit, hasLetter, hasSep bool
	for _, r := range folded {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsLetter(r):
			hasLetter = true
		case r == '-' || r == '_' || r == '/':
			hasSep = true
		}
	}
	// Pure numeric codes (e.g. "1024") or mixed alphanumeric SKU codes (e.g. "sku-001", "at1024").
	if hasDigit && (!hasLetter || hasSep || len(folded) >= 4) {
		return true
	}
	return false
}
