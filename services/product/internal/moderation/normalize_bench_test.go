package moderation

import (
	"fmt"
	"testing"
)

type syntheticCatalogItem struct {
	name      string
	desc      string
	skuFields []string
}

func buildSyntheticCatalog20K() ([]syntheticCatalogItem, int) {
	const (
		numProducts = 20661
		numSKUs     = 40888
	)

	// ~192 distinct SKU strings (names + attribute keys + attribute values), matching production distribution.
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
	// All 4 templates contain bigrams "su", "un", "ng" (e.g. suốt/sự/sử, cùng/dụng/chung) to stress the bigram filter on "súng".
	descTemplates := []string{
		"Sản phẩm được sản xuất từ chất liệu cotton hữu cơ cao cấp giúp bề mặt vải mềm mịn, thấm hút mồ hôi nhanh và hạn chế xù lông sau nhiều lần giặt máy. Đường may tỉ mỉ chắc chắn ở vai, cổ áo và lai áo mang lại cảm giác thoải mái suốt cả ngày dài vận động. Phù hợp mặc đi làm, đi học, dạo phố hoặc phối cùng áo khoác nhẹ.",
		"Thiết kế tối giản hiện đại theo phong cách ứng dụng hằng ngày, dễ dàng kết hợp với nhiều loại trang phục khác nhau từ năng động đến lịch sự. Chất vải thoáng khí co giãn tốt giúp giữ phom dáng ổn định, không bai dão và hạn chế nhăn nhàu khi di chuyển. Hỗ trợ đổi kích cỡ linh hoạt trong vòng bảy ngày nếu chưa vừa.",
		"Chất liệu sợi tổng hợp thế hệ mới có khả năng khô nhanh, nhẹ thoáng và chống tia cực tím hiệu quả khi hoạt động ngoài trời. Phần cạp chun co giãn êm ái đi kèm dây rút điều chỉnh tiện dụng cùng túi khóa kéo an toàn đựng điện thoại, chìa khóa. Bảo quản sử dụng đơn giản bằng nước mát và phơi nơi râm thoáng.",
		"Phiên bản nâng cấp cải tiến định lượng vải dày dặn hơn nhưng vẫn đảm bảo độ thông thoáng và mềm mại tiếp xúc trực tiếp với làn da. Công nghệ nhuộm giữ màu bền bỉ giúp trang phục luôn tươi mới, không phai màu sang đồ khác khi giặt chung. Sản phẩm chính hãng có tem nhãn đầy đủ và hướng dẫn sử dụng chi tiết.",
	}

	items := make([]syntheticCatalogItem, numProducts)
	const twoSKUProducts = numSKUs - numProducts
	skuIdx := 0
	matched := 0
	for i := 0; i < numProducts; i++ {
		name := fmt.Sprintf("%s #%05d", namePrefixes[i%len(namePrefixes)], i)
		desc := fmt.Sprintf("%s Mã dòng sản phẩm %05d bảo hành chuẩn.", descTemplates[i%len(descTemplates)], i)
		nSKU := 1
		if i < twoSKUProducts {
			nSKU = 2
		}
		fields := make([]string, 0, nSKU*7)
		for k := 0; k < nSKU; k++ {
			sn := skuNames[skuIdx%len(skuNames)]
			col := colors[skuIdx%len(colors)]
			sz := sizes[skuIdx%len(sizes)]
			mat := materials[skuIdx%len(materials)]
			fields = append(fields, sn, "color", col, "material", mat, "size", sz)
			skuIdx++
		}
		if i > 0 && i%2000 == 0 {
			switch (i / 2000) % 3 {
			case 0:
				name = name + " hàng giả"
			case 1:
				desc = desc + " Cam kết không bán hàng giả loại một."
			case 2:
				fields[0] = "Mẫu h.à.n.g g.i.ả đặc biệt"
			}
			matched++
		}
		items[i] = syntheticCatalogItem{name: name, desc: desc, skuFields: fields}
	}
	return items, matched
}

func BenchmarkMatchCompiled_Catalog20K(b *testing.B) {
	items, wantHangGia := buildSyntheticCatalog20K()

	run := func(b *testing.B, compiled []CompiledRule, wantMinHits int) {
		fieldsBuf := make([]string, 0, 16)
		b.ResetTimer()
		for iter := 0; iter < b.N; iter++ {
			hits := 0
			for i := range items {
				fieldsBuf = append(fieldsBuf[:0], items[i].name, items[i].desc)
				fieldsBuf = append(fieldsBuf, items[i].skuFields...)
				if MatchCompiled(compiled, fieldsBuf...).Blocked {
					hits++
				}
			}
			if hits < wantMinHits {
				b.Fatalf("matched=%d, want >= %d", hits, wantMinHits)
			}
		}
	}

	b.Run("1Rule_HangGia", func(b *testing.B) {
		compiled := CompileRules([]Rule{{ID: "preview", Term: "hàng giả", Action: ActionBlock}})
		run(b, compiled, wantHangGia)
	})

	b.Run("1Rule_Sung_CommonBigrams", func(b *testing.B) {
		// Worst case for bigram early-skip: "su", "un", "ng" appear in every description.
		compiled := CompileRules([]Rule{{ID: "preview", Term: "súng", Action: ActionBlock}})
		run(b, compiled, 0)
	})

	b.Run("20Rules", func(b *testing.B) {
		terms := []string{
			"hàng giả", "ma túy", "cần sa", "thuốc lắc", "vũ khí",
			"súng", "cá độ", "nhập lậu", "chữa bách bệnh", "cam kết 100%",
			"thuốc cấm", "hàng cấm", "đánh bạc", "tiền giả", "pháo nổ",
			"kích dục", "ngà voi", "sừng tê giác", "mã độc", "lừa đảo",
		}
		rules := make([]Rule, len(terms))
		for i, t := range terms {
			rules[i] = Rule{ID: fmt.Sprintf("r-%d", i), Term: t, Action: ActionBlock}
		}
		run(b, CompileRules(rules), wantHangGia)
	})
}
