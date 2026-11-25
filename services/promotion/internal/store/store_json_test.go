package store

import (
	"testing"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

func TestMarshalJSONArray_NeverWritesNullForNilSlices(t *testing.T) {
	t.Run("string_slice", func(t *testing.T) {
		var nilSlice []string
		got, err := marshalJSONArray(nilSlice)
		if err != nil {
			t.Fatalf("marshalJSONArray(nil []string): %v", err)
		}
		if string(got) != "[]" {
			t.Fatalf("nil []string: want %q, got %q", "[]", string(got))
		}

		empty, err := marshalJSONArray([]string{})
		if err != nil {
			t.Fatalf("marshalJSONArray(empty []string): %v", err)
		}
		if string(empty) != "[]" {
			t.Fatalf("empty []string: want %q, got %q", "[]", string(empty))
		}

		populated, err := marshalJSONArray([]string{"cat-1", "cat-2"})
		if err != nil {
			t.Fatalf("marshalJSONArray(populated []string): %v", err)
		}
		if string(populated) != `["cat-1","cat-2"]` {
			t.Fatalf("populated []string: want %q, got %q", `["cat-1","cat-2"]`, string(populated))
		}
	})

	t.Run("reserved_vouchers", func(t *testing.T) {
		var nilSlice []domain.ReservedVoucher
		got, err := marshalJSONArray(nilSlice)
		if err != nil {
			t.Fatalf("marshalJSONArray(nil []ReservedVoucher): %v", err)
		}
		if string(got) != "[]" {
			t.Fatalf("nil []ReservedVoucher: want %q, got %q", "[]", string(got))
		}

		empty, err := marshalJSONArray([]domain.ReservedVoucher{})
		if err != nil {
			t.Fatalf("marshalJSONArray(empty []ReservedVoucher): %v", err)
		}
		if string(empty) != "[]" {
			t.Fatalf("empty []ReservedVoucher: want %q, got %q", "[]", string(empty))
		}

		populated, err := marshalJSONArray([]domain.ReservedVoucher{{
			VoucherID:   "v-1",
			Scope:       domain.VoucherScopeShop,
			ShopID:      "shop-1",
			DiscountVND: 10000,
			HasBudget:   true,
		}})
		if err != nil {
			t.Fatalf("marshalJSONArray(populated []ReservedVoucher): %v", err)
		}
		if gotStr := string(populated); gotStr[0] != '[' || gotStr[len(gotStr)-1] != ']' {
			t.Fatalf("populated []ReservedVoucher: expected JSON array, got %q", gotStr)
		}
	})

	t.Run("reserved_flash_items", func(t *testing.T) {
		var nilSlice []domain.ReservedFlashItem
		got, err := marshalJSONArray(nilSlice)
		if err != nil {
			t.Fatalf("marshalJSONArray(nil []ReservedFlashItem): %v", err)
		}
		if string(got) != "[]" {
			t.Fatalf("nil []ReservedFlashItem: want %q, got %q", "[]", string(got))
		}

		empty, err := marshalJSONArray([]domain.ReservedFlashItem{})
		if err != nil {
			t.Fatalf("marshalJSONArray(empty []ReservedFlashItem): %v", err)
		}
		if string(empty) != "[]" {
			t.Fatalf("empty []ReservedFlashItem: want %q, got %q", "[]", string(empty))
		}

		populated, err := marshalJSONArray([]domain.ReservedFlashItem{{
			FlashSaleItemID: "fi-1",
			SKUID:           "sku-1",
			Quantity:        2,
			ShopID:          "shop-1",
		}})
		if err != nil {
			t.Fatalf("marshalJSONArray(populated []ReservedFlashItem): %v", err)
		}
		if gotStr := string(populated); gotStr[0] != '[' || gotStr[len(gotStr)-1] != ']' {
			t.Fatalf("populated []ReservedFlashItem: expected JSON array, got %q", gotStr)
		}
	})

	t.Run("reserved_shop_program_items", func(t *testing.T) {
		var nilSlice []domain.ReservedShopProgramItem
		got, err := marshalJSONArray(nilSlice)
		if err != nil {
			t.Fatalf("marshalJSONArray(nil []ReservedShopProgramItem): %v", err)
		}
		if string(got) != "[]" {
			t.Fatalf("nil []ReservedShopProgramItem: want %q, got %q", "[]", string(got))
		}

		empty, err := marshalJSONArray([]domain.ReservedShopProgramItem{})
		if err != nil {
			t.Fatalf("marshalJSONArray(empty []ReservedShopProgramItem): %v", err)
		}
		if string(empty) != "[]" {
			t.Fatalf("empty []ReservedShopProgramItem: want %q, got %q", "[]", string(empty))
		}

		populated, err := marshalJSONArray([]domain.ReservedShopProgramItem{{
			ShopDiscountProgramItemID: "sdi-1",
			SKUID:                     "sku-1",
			Quantity:                  1,
			ShopID:                    "shop-1",
		}})
		if err != nil {
			t.Fatalf("marshalJSONArray(populated []ReservedShopProgramItem): %v", err)
		}
		if gotStr := string(populated); gotStr[0] != '[' || gotStr[len(gotStr)-1] != ']' {
			t.Fatalf("populated []ReservedShopProgramItem: expected JSON array, got %q", gotStr)
		}
	})
}
