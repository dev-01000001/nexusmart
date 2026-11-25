package meili

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// Strict identifier pattern (UUID, ulid, alphanumeric IDs with - _ :).
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,128}$`)
	// Strict slug pattern.
	slugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// ValidateIdentifier rejects malformed IDs or filter-injection payloads (e.g. `x" OR status="HIDDEN`).
func ValidateIdentifier(field, val string) error {
	if val == "" {
		return nil
	}
	if !identifierPattern.MatchString(val) {
		return fmt.Errorf("invalid %s", field)
	}
	return nil
}

// ValidateSlug rejects malformed slugs or filter-injection payloads.
func ValidateSlug(field, val string) error {
	if val == "" {
		return nil
	}
	if !slugPattern.MatchString(val) {
		return fmt.Errorf("invalid %s", field)
	}
	return nil
}

// ValidateAttrFilterValue enforces only max length and control-character rejection (S11).
// Valid facet values like "XL (60kg)" or "Black AND White" are accepted because BuildFilters
// always quotes and escapes every value via EscapeMeiliString.
func ValidateAttrFilterValue(field, val string) error {
	if val == "" {
		return nil
	}
	if utf8.RuneCountInString(val) > 64 {
		return fmt.Errorf("%s exceeds maximum length", field)
	}
	for _, r := range val {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid control character in %s", field)
		}
	}
	return nil
}

// EscapeMeiliString quotes and escapes a string literal for Meilisearch filter expressions.
func EscapeMeiliString(val string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(val) + `"`
}

// BuildFilters constructs structured Meilisearch filter clauses with mandatory public-visibility
// enforcement and strict escaping to make filter injection structurally impossible (S11/S14).
func BuildFilters(q SearchQuery) ([]string, error) {
	if err := ValidateIdentifier("category_id", q.CategoryID); err != nil {
		return nil, err
	}
	if err := ValidateIdentifier("shop_id", q.ShopID); err != nil {
		return nil, err
	}
	if err := ValidateAttrFilterValue("size", q.SizeFolded); err != nil {
		return nil, err
	}
	if err := ValidateAttrFilterValue("color", q.ColorFolded); err != nil {
		return nil, err
	}

	filters := make([]string, 0, 10)
	if q.OnlyApprovedInStock {
		filters = append(filters, `status = "APPROVED"`, `in_stock = true`)
	} else {
		filters = append(filters, `status IN ["APPROVED", "OUT_OF_STOCK"]`)
	}

	if q.CategoryID != "" {
		filters = append(filters, "category_ancestor_ids = "+EscapeMeiliString(q.CategoryID))
	}
	if q.ShopID != "" {
		filters = append(filters, "shop_id = "+EscapeMeiliString(q.ShopID))
	}
	if len(q.ExcludedShopIDs) > 0 {
		// Cap filter size so pathological suspended counts do not exceed Meilisearch query limits;
		// callers also post-filter against the full suspended set.
		limit := len(q.ExcludedShopIDs)
		if limit > 500 {
			limit = 500
		}
		quoted := make([]string, 0, limit)
		for _, sid := range q.ExcludedShopIDs[:limit] {
			if sid == "" {
				continue
			}
			quoted = append(quoted, EscapeMeiliString(sid))
		}
		if len(quoted) > 0 {
			filters = append(filters, "shop_id NOT IN ["+strings.Join(quoted, ", ")+"]")
		}
	}
	if q.MinPriceVND > 0 {
		filters = append(filters, fmt.Sprintf("max_price_vnd >= %d", q.MinPriceVND))
	}
	if q.MaxPriceVND > 0 {
		filters = append(filters, fmt.Sprintf("min_price_vnd <= %d", q.MaxPriceVND))
	}
	if q.MinRatingX100 > 0 {
		filters = append(filters, fmt.Sprintf("rating_avg_x100 >= %d", q.MinRatingX100))
	}
	if q.InStockOnly && !q.OnlyApprovedInStock {
		filters = append(filters, "in_stock = true")
	}
	if q.SizeFolded != "" {
		filters = append(filters, "sizes_folded = "+EscapeMeiliString(q.SizeFolded))
	}
	if q.ColorFolded != "" {
		esc := EscapeMeiliString(q.ColorFolded)
		filters = append(filters, "(colors_folded = "+esc+" OR color_codes = "+esc+")")
	}
	return filters, nil
}
