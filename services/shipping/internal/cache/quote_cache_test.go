package cache

import (
	"strings"
	"testing"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
)

func TestQuoteCacheKeyRequiresRealOriginAndDestination(t *testing.T) {
	// Empty origin must NOT produce an empty-origin cache key.
	key := quoteCacheKey(carrier.FeeRequest{
		ShopID:         "shop-1",
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
		CODAmountVND:   200000,
	})
	if strings.Contains(key, ":::") || key != "" {
		t.Fatalf("expected empty cache key when origin is missing, got %q", key)
	}

	// Real origin must be included in the cache key.
	validKey := quoteCacheKey(carrier.FeeRequest{
		ShopID:           "shop-1",
		FromProvinceCode: "79",
		FromWardCode:     "79001",
		ToProvinceCode:   "79",
		ToWardCode:       "79001",
		WeightGrams:      250,
		CODAmountVND:     200000,
	})
	want := "shipping:quotes:v2:all:79:79001:79:79001:300:1"
	if validKey != want {
		t.Fatalf("quoteCacheKey() = %q, want %q", validKey, want)
	}

	// AllowedCarriers must be order-independent and isolate distinct carrier subsets.
	k1 := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001",
		WeightGrams: 500, AllowedCarriers: []string{"mock_express", "ghn"},
	})
	k2 := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001",
		WeightGrams: 500, AllowedCarriers: []string{"ghn", "mock_express"},
	})
	kOnlyGHN := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001",
		WeightGrams: 500, AllowedCarriers: []string{"ghn"},
	})
	if k1 != k2 {
		t.Fatalf("carrier order should not affect cache key: %q != %q", k1, k2)
	}
	if k1 == kOnlyGHN {
		t.Fatalf("distinct carrier sets must not collide in cache: %q == %q", k1, kOnlyGHN)
	}
}
