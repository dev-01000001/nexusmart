package server

import (
	"context"
	"fmt"
	"sort"
	"testing"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/store"
)

// indexedBenchStore embeds *store.MemoryStore and overrides the two moderation scan reads
// with O(batch) indexed lookups so PreviewBannedKeyword benchmarks isolate CPU cost
// instead of MemoryStore's unindexed O(N) map scan per batch.
type indexedBenchStore struct {
	*store.MemoryStore
	products  []store.Product
	skusByPID map[string][]store.SKU
}

func (s *indexedBenchStore) ListProductsForModerationScan(_ context.Context, cursorID string, limit int32) ([]store.Product, error) {
	if limit <= 0 {
		limit = defaultRescanBatchSize
	}
	start := 0
	if cursorID != "" {
		start = sort.Search(len(s.products), func(i int) bool {
			return s.products[i].ID > cursorID
		})
	}
	if start >= len(s.products) {
		return nil, nil
	}
	end := start + int(limit)
	if end > len(s.products) {
		end = len(s.products)
	}
	return s.products[start:end], nil
}

func (s *indexedBenchStore) ListSKUsByProductIDs(_ context.Context, productIDs []string) ([]store.SKU, error) {
	out := make([]store.SKU, 0, len(productIDs)*2)
	for _, pid := range productIDs {
		out = append(out, s.skusByPID[pid]...)
	}
	return out, nil
}

func (s *indexedBenchStore) ListModerationSKUsByProductIDs(_ context.Context, productIDs []string) ([]store.ModerationSKU, error) {
	out := make([]store.ModerationSKU, 0, len(productIDs)*2)
	for _, pid := range productIDs {
		for _, sk := range s.skusByPID[pid] {
			out = append(out, store.ModerationSKU{
				ProductID:  sk.ProductID,
				Name:       sk.Name,
				Attributes: sk.Attributes,
			})
		}
	}
	return out, nil
}

func buildPreviewBenchStore() (*indexedBenchStore, int64) {
	const (
		numProducts = 20661
		numSKUs     = 40888
	)

	colors := []string{
		"Đen than", "Trắng kem", "Xanh navy", "Xanh rêu", "Xám tiêu",
		"Be sáng", "Nâu đất", "Đỏ đô", "Xanh dương", "Vàng nhạt",
		"Hồng phấn", "Tím than", "Cam đất", "Ghi sáng", "Xanh mint",
	}
	sizes := []string{
		"XS", "S", "M", "L", "XL", "2XL", "3XL",
		"S (45-52kg)", "M (53-60kg)", "L (61-68kg)", "XL (69-76kg)", "2XL (77-85kg)",
	}
	materials := []string{
		"Cotton 100%", "Cotton Compact", "Polyester thoáng khí", "Modal gỗ sồi",
		"Bamboo sợi tre", "Linen tự nhiên", "Kaki co giãn", "Denim dày dặn",
		"Nỉ bông mềm mịn", "Lụa lạnh cao cấp", "Len lông cừu", "Vải dù chống gió",
	}
	skuNames := make([]string, 0, len(colors)*10)
	for _, c := range colors {
		for _, s := range sizes[:10] {
			skuNames = append(skuNames, c+" - "+s)
		}
	}

	namePrefixes := []string{
		"Áo thun nam cổ tròn cotton compact thoáng mát form rộng mặc hàng ngày",
		"Quần short thể thao nam vải dù co giãn bốn chiều chạy bộ tập gym nhẹ",
		"Áo khoác gió hai lớp chống nắng trượt nước có mũ trùm đầu tiện lợi",
		"Áo sơ mi dài tay vải sợi tre chống nhăn thanh lịch công sở dáng vừa",
		"Quần dài kaki ống đứng co giãn nhẹ lịch sự phối đồ đi làm dạo phố",
		"Áo polo nam dệt pique thấm hút mồ hôi cổ bẻ đứng phom bền màu đẹp",
		"Bộ đồ mặc nhà chất liệu modal mềm mịn thoáng khí thấm hút cực tốt",
		"Quần lót nam boxer sợi gỗ sồi kháng khuẩn đai lưng bản rộng êm ái",
	}
	descTemplates := []string{
		"Sản phẩm được sản xuất từ chất liệu cotton hữu cơ cao cấp giúp bề mặt vải mềm mịn, thấm hút mồ hôi nhanh và hạn chế xù lông sau nhiều lần giặt máy. Đường may tỉ mỉ chắc chắn ở vai, cổ áo và lai áo mang lại cảm giác thoải mái suốt cả ngày dài vận động. Phù hợp mặc đi làm, đi học, dạo phố hoặc phối cùng áo khoác nhẹ.",
		"Thiết kế tối giản hiện đại theo phong cách ứng dụng hằng ngày, dễ dàng kết hợp với nhiều loại trang phục khác nhau từ năng động đến lịch sự. Chất vải thoáng khí co giãn tốt giúp giữ phom dáng ổn định, không bai dão và hạn chế nhăn nhàu khi di chuyển. Hỗ trợ đổi kích cỡ linh hoạt trong vòng bảy ngày nếu chưa vừa.",
		"Chất liệu sợi tổng hợp thế hệ mới có khả năng khô nhanh, nhẹ thoáng và chống tia cực tím hiệu quả khi hoạt động ngoài trời. Phần cạp chun co giãn êm ái đi kèm dây rút điều chỉnh tiện dụng cùng túi khóa kéo an toàn đựng điện thoại, chìa khóa. Bảo quản sử dụng đơn giản bằng nước mát và phơi nơi râm thoáng.",
		"Phiên bản nâng cấp cải tiến định lượng vải dày dặn hơn nhưng vẫn đảm bảo độ thông thoáng và mềm mại tiếp xúc trực tiếp với làn da. Công nghệ nhuộm giữ màu bền bỉ giúp trang phục luôn tươi mới, không phai màu sang đồ khác khi giặt chung. Sản phẩm chính hãng có tem nhãn đầy đủ và hướng dẫn sử dụng chi tiết.",
	}

	products := make([]store.Product, numProducts)
	skusByPID := make(map[string][]store.SKU, numProducts)
	const twoSKUProducts = numSKUs - numProducts
	skuIdx := 0
	var wantAffected int64

	for i := 0; i < numProducts; i++ {
		pid := fmt.Sprintf("prod-%08d", i)
		name := fmt.Sprintf("%s #%05d", namePrefixes[i%len(namePrefixes)], i)
		desc := fmt.Sprintf("%s Mã dòng sản phẩm %05d bảo hành chuẩn.", descTemplates[i%len(descTemplates)], i)
		slug := slugify(name, "prod-"+pid[:8])

		nSKU := 1
		if i < twoSKUProducts {
			nSKU = 2
		}
		skus := make([]store.SKU, nSKU)
		for k := 0; k < nSKU; k++ {
			skus[k] = store.SKU{
				ID:        fmt.Sprintf("sku-%08d", skuIdx),
				ProductID: pid,
				Name:      skuNames[skuIdx%len(skuNames)],
				Attributes: map[string]string{
					"color":    colors[skuIdx%len(colors)],
					"size":     sizes[skuIdx%len(sizes)],
					"material": materials[skuIdx%len(materials)],
				},
				Status: store.SKUStatusActive,
			}
			skuIdx++
		}

		if i > 0 && i%2000 == 0 {
			switch (i / 2000) % 4 {
			case 0:
				name = name + " hàng giả"
				slug = slugify(name, "prod-"+pid[:8])
			case 1:
				desc = desc + " Cam kết không bán hàng giả loại một."
			case 2:
				skus[0].Name = "Mẫu h.à.n.g g.i.ả đặc biệt"
			case 3:
				slug = fmt.Sprintf("custom-hang-gia-%05d", i)
			}
			wantAffected++
		}

		products[i] = store.Product{
			ID:          pid,
			ShopID:      "shop-1",
			CategoryID:  "cat-1",
			Name:        name,
			Slug:        slug,
			Description: desc,
			Status:      store.ProductStatusApproved,
		}
		skusByPID[pid] = skus
	}

	return &indexedBenchStore{
		MemoryStore: store.NewMemoryStore(),
		products:    products,
		skusByPID:   skusByPID,
	}, wantAffected
}

func BenchmarkPreviewBannedKeyword_Catalog20K(b *testing.B) {
	st, wantAffected := buildPreviewBenchStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")

	b.Run("HangGia", func(b *testing.B) {
		req := &productv1.PreviewBannedKeywordRequest{Term: "hàng giả"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := srv.PreviewBannedKeyword(adminCtx, req)
			if err != nil {
				b.Fatalf("PreviewBannedKeyword: %v", err)
			}
			if resp.GetAffectedCount() != wantAffected {
				b.Fatalf("AffectedCount=%d, want %d", resp.GetAffectedCount(), wantAffected)
			}
		}
	})

	b.Run("Sung_CommonBigrams", func(b *testing.B) {
		req := &productv1.PreviewBannedKeywordRequest{Term: "súng"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := srv.PreviewBannedKeyword(adminCtx, req)
			if err != nil {
				b.Fatalf("PreviewBannedKeyword: %v", err)
			}
			if resp.GetAffectedCount() != 0 {
				b.Fatalf("AffectedCount=%d, want 0", resp.GetAffectedCount())
			}
		}
	})
}
