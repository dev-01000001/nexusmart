package db

import (
	"strings"
	"testing"
)

func TestShopCategoryQueries_SingleScanShape(t *testing.T) {
	cases := []struct {
		name         string
		sql          string
		mustContains []string
	}{
		{
			name: "ListCategoriesByShop",
			sql:  listCategoriesByShop,
			mustContains: []string{
				"pe.shop_id = $1",
				"pe.deleted = FALSE",
				"pe.status IN ('APPROVED', 'OUT_OF_STOCK')",
				"unnest(array_append(s.category_ancestor_ids, s.category_id))",
				"JOIN shop_cats sc ON sc.cat_id = c.category_id",
				"ORDER BY c.sort_order ASC, c.name ASC",
			},
		},
		{
			name: "SuggestCategoriesByShop",
			sql:  suggestCategoriesByShop,
			mustContains: []string{
				"pe.shop_id = $1",
				"pe.deleted = FALSE",
				"pe.status IN ('APPROVED', 'OUT_OF_STOCK')",
				"unnest(array_append(s.category_ancestor_ids, s.category_id))",
				"JOIN shop_cats sc ON sc.cat_id = c.category_id",
				"LIMIT $3",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(strings.ToUpper(tc.sql), "WHERE EXISTS") {
				t.Fatalf("%s must not use per-category WHERE EXISTS correlated subquery:\n%s", tc.name, tc.sql)
			}
			if cnt := strings.Count(tc.sql, "product_embeddings"); cnt != 1 {
				t.Fatalf("%s must scan product_embeddings once, found %d references:\n%s", tc.name, cnt, tc.sql)
			}
			for _, sub := range tc.mustContains {
				if !strings.Contains(tc.sql, sub) {
					t.Fatalf("%s missing expected clause %q:\n%s", tc.name, sub, tc.sql)
				}
			}
		})
	}
}
