package labelpdf

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// inspectPDF parses page count, A6 MediaBox dimensions, font embedding markers,
// and decodes all rendered Unicode text via the PDF's /ToUnicode CMap and page streams.
type pdfInspection struct {
	PageCount      int
	MediaBoxes     [][4]float64
	HasFontFile2   bool
	HasToUnicode   bool
	HasIdentityH   bool
	ExtractedTexts []string
}

var (
	rePageType  = regexp.MustCompile(`/Type\s*/Page\b`)
	reMediaBox  = regexp.MustCompile(`/MediaBox\s*\[\s*([0-9.+-]+)\s+([0-9.+-]+)\s+([0-9.+-]+)\s+([0-9.+-]+)\s*\]`)
	reStream    = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	reBFChar    = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	reBFRange   = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	reHexPair   = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reRangeLine = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reHexText   = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj|\[([^\]]*)\]\s*TJ`)
	reHexToken  = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
	reBTBlock   = regexp.MustCompile(`(?s)BT\s*(.*?)\s*ET`)
	reTfOp      = regexp.MustCompile(`/F\d+\s+([0-9.]+)\s+Tf`)
	reTdOp      = regexp.MustCompile(`([0-9.+-]+)\s+([0-9.+-]+)\s+T[dD]`)
)

type pdfTextBox struct {
	Page     int
	X        float64 // mm from left
	Baseline float64 // mm from top
	Top      float64 // Baseline - ascent (mm)
	Bottom   float64 // Baseline + descent (mm)
	FontPT   float64
	Text     string
}

func extractPDFTextBoxes(t *testing.T, data []byte) [][]pdfTextBox {
	t.Helper()
	const (
		testMMPerPT          = 25.4 / 72.0
		testFontAscentRatio  = 1854.0 / 2048.0
		testFontDescentRatio = 434.0 / 2048.0
	)
	cmap := make(map[uint16]string)
	var contentStreams []string
	for _, m := range reStream.FindAllSubmatch(data, -1) {
		decoded := tryInflate(m[1])
		s := string(decoded)
		if strings.Contains(s, "begincmap") {
			parseToUnicodeCMap(s, cmap)
		} else if strings.Contains(s, "BT") && (strings.Contains(s, "Tj") || strings.Contains(s, "TJ")) {
			contentStreams = append(contentStreams, s)
		}
	}

	pages := make([][]pdfTextBox, 0, len(contentStreams))
	for pIdx, cs := range contentStreams {
		var boxes []pdfTextBox
		var curFontPT float64
		for _, bm := range reBTBlock.FindAllStringSubmatch(cs, -1) {
			block := bm[1]
			if fm := reTfOp.FindStringSubmatch(block); len(fm) == 2 {
				if v, err := strconv.ParseFloat(fm[1], 64); err == nil {
					curFontPT = v
				}
			}
			var xPT, yPT float64
			hasTd := false
			for _, tm := range reTdOp.FindAllStringSubmatch(block, -1) {
				dx, e1 := strconv.ParseFloat(tm[1], 64)
				dy, e2 := strconv.ParseFloat(tm[2], 64)
				if e1 == nil && e2 == nil {
					xPT += dx
					yPT += dy
					hasTd = true
				}
			}
			if !hasTd || curFontPT <= 0 {
				continue
			}
			var sb strings.Builder
			for _, hm := range reHexText.FindAllStringSubmatch(block, -1) {
				if hm[1] != "" {
					sb.WriteString(decodeCIDHex(hm[1], cmap))
				} else if hm[2] != "" {
					for _, tok := range reHexToken.FindAllStringSubmatch(hm[2], -1) {
						sb.WriteString(decodeCIDHex(tok[1], cmap))
					}
				}
			}
			txt := sb.String()
			if txt == "" {
				continue
			}
			xMM := xPT * testMMPerPT
			baselineMM := a6HeightMM - yPT*testMMPerPT
			ascentMM := curFontPT * testMMPerPT * testFontAscentRatio
			descentMM := curFontPT * testMMPerPT * testFontDescentRatio
			boxes = append(boxes, pdfTextBox{
				Page:     pIdx + 1,
				X:        xMM,
				Baseline: baselineMM,
				Top:      baselineMM - ascentMM,
				Bottom:   baselineMM + descentMM,
				FontPT:   curFontPT,
				Text:     txt,
			})
		}
		pages = append(pages, boxes)
	}
	return pages
}

func inspectPDF(t *testing.T, data []byte) pdfInspection {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("PDF missing %%PDF- header, got prefix %q", string(data[:minInt(len(data), 16)]))
	}
	if !bytes.Contains(data, []byte("%%EOF")) {
		t.Fatalf("PDF missing %%%%EOF trailer")
	}

	raw := string(data)
	out := pdfInspection{
		PageCount:    len(rePageType.FindAllString(raw, -1)),
		HasFontFile2: bytes.Contains(data, []byte("/FontFile2")),
		HasToUnicode: bytes.Contains(data, []byte("/ToUnicode")),
		HasIdentityH: bytes.Contains(data, []byte("/Identity-H")),
	}

	for _, m := range reMediaBox.FindAllStringSubmatch(raw, -1) {
		var box [4]float64
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseFloat(m[i+1], 64)
			if err != nil {
				t.Fatalf("parse MediaBox float %q: %v", m[i+1], err)
			}
			box[i] = v
		}
		out.MediaBoxes = append(out.MediaBoxes, box)
	}

	// Collect all decompressed streams (ToUnicode CMaps + page content streams).
	cmap := make(map[uint16]string)
	var contentStreams []string
	for _, m := range reStream.FindAllSubmatch(data, -1) {
		streamBytes := m[1]
		decoded := tryInflate(streamBytes)
		s := string(decoded)
		if strings.Contains(s, "begincmap") {
			parseToUnicodeCMap(s, cmap)
		} else if strings.Contains(s, "BT") && (strings.Contains(s, "Tj") || strings.Contains(s, "TJ")) {
			contentStreams = append(contentStreams, s)
		}
	}

	for _, cs := range contentStreams {
		var b strings.Builder
		for _, m := range reHexText.FindAllStringSubmatch(cs, -1) {
			if m[1] != "" {
				b.WriteString(decodeCIDHex(m[1], cmap))
				b.WriteByte('\n')
			} else if m[2] != "" {
				for _, tok := range reHexToken.FindAllStringSubmatch(m[2], -1) {
					b.WriteString(decodeCIDHex(tok[1], cmap))
				}
				b.WriteByte('\n')
			}
		}
		out.ExtractedTexts = append(out.ExtractedTexts, b.String())
	}
	return out
}

func tryInflate(raw []byte) []byte {
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	defer func() { _ = zr.Close() }()
	dec, err := io.ReadAll(zr)
	if err != nil {
		return raw
	}
	return dec
}

func parseToUnicodeCMap(s string, cmap map[uint16]string) {
	for _, block := range reBFChar.FindAllStringSubmatch(s, -1) {
		for _, pair := range reHexPair.FindAllStringSubmatch(block[1], -1) {
			cid, err := strconv.ParseUint(pair[1], 16, 16)
			if err != nil {
				continue
			}
			cmap[uint16(cid)] = decodeUTF16BEHex(pair[2])
		}
	}
	for _, block := range reBFRange.FindAllStringSubmatch(s, -1) {
		for _, r := range reRangeLine.FindAllStringSubmatch(block[1], -1) {
			startCID, err1 := strconv.ParseUint(r[1], 16, 16)
			endCID, err2 := strconv.ParseUint(r[2], 16, 16)
			baseUni, err3 := strconv.ParseUint(r[3], 16, 32)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			for cid := startCID; cid <= endCID; cid++ {
				cmap[uint16(cid)] = string(rune(baseUni + (cid - startCID)))
			}
		}
	}
}

func decodeUTF16BEHex(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw)%2 != 0 {
		return ""
	}
	u16 := make([]uint16, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		u16[i/2] = uint16(raw[i])<<8 | uint16(raw[i+1])
	}
	return string(utf16.Decode(u16))
}

func decodeCIDHex(h string, cmap map[uint16]string) string {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw)%2 != 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(raw); i += 2 {
		cid := uint16(raw[i])<<8 | uint16(raw[i+1])
		if str, ok := cmap[cid]; ok {
			b.WriteString(str)
		} else {
			b.WriteRune('\uFFFD')
		}
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestRenderBatchA6_MultiPageVietnameseDiacriticsAndA6Dimensions(t *testing.T) {
	labels := []LabelData{
		{
			TrackingCode:      "GHN-VIET-0001",
			OrderID:           "ord-viet-0001",
			ShopID:            "shop-a",
			Direction:         "FORWARD",
			CarrierCode:       "ghn",
			ServiceCode:       "STANDARD",
			Status:            "created",
			CODAmountVND:      350000,
			FeeVND:            22000,
			WeightGrams:       750,
			FromName:          "Xưởng Thời Trang Ánh Dương",
			FromPhone:         "0901234567",
			FromStreetAddress: "123 Đường Nguyễn Huệ, Tòa nhà Mặt Trời",
			FromWardName:      "Phường Bến Nghé",
			FromProvinceName:  "Thành phố Hồ Chí Minh",
			ToName:            "Nguyễn Thị Hương Quỳnh",
			ToPhone:           "0987654321",
			ToStreetAddress:   "88 Phố Hàng Trống, Ngõ 12",
			ToWardName:        "Phường Hàng Trống",
			ToProvinceName:    "Thành phố Hà Nội",
			CreatedAt:         time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC),
		},
		{
			TrackingCode:      "MOCK-VIET-0002",
			OrderID:           "ord-viet-0002",
			ShopID:            "shop-a",
			Direction:         "FORWARD",
			CarrierCode:       "mock_express",
			ServiceCode:       "FAST",
			Status:            "picking",
			CODAmountVND:      0,
			FeeVND:            34000,
			WeightGrams:       1250,
			FromName:          "Cửa Hàng Đắk Lắk Đặc Sản",
			FromPhone:         "0905112233",
			FromStreetAddress: "45 Đường Y Ngông",
			FromWardName:      "Phường Buôn Ma Thuột",
			FromProvinceName:  "Tỉnh Đắk Lắk",
			ToName:            "Trần Đức Mạnh",
			ToPhone:           "0912345678",
			ToStreetAddress:   "Số 9 Đường Bạch Đằng",
			ToWardName:        "Phường Hải Châu",
			ToProvinceName:    "Thành phố Đà Nẵng",
			CreatedAt:         time.Date(2026, 11, 25, 3, 5, 0, 0, time.UTC),
		},
		{
			// Legacy shipment with empty contact/street fields + very long Vietnamese name/address edge case.
			TrackingCode:     "GHN-LEGACY-0003",
			OrderID:          "ord-legacy-0003",
			ShopID:           "shop-a",
			Direction:        "RETURN",
			CarrierCode:      "ghn",
			ServiceCode:      "STANDARD",
			Status:           "created",
			CODAmountVND:     1250000,
			FeeVND:           38000,
			WeightGrams:      500,
			FromWardName:     "Phường Tân An",
			FromProvinceName: "Thành phố Cần Thơ",
			ToName:           "Võ Thị Kiều Oanh",
			ToPhone:          "0933445566",
			ToStreetAddress:  "Địa chỉ rất dài để kiểm tra tự động xuống dòng trên khổ giấy A6: Số 1024 Đường Điện Biên Phủ, Khu phố 5, Chung cư Hoàng Anh Gia Lai, Tầng 18 Căn hộ B1809",
			ToWardName:       "Phường Bến Thành",
			ToProvinceName:   "Thành phố Hồ Chí Minh",
			CreatedAt:        time.Date(2026, 11, 25, 3, 10, 0, 0, time.UTC),
		},
	}

	pdfBytes, err := RenderBatchA6(labels)
	if err != nil {
		t.Fatalf("RenderBatchA6 failed: %v", err)
	}

	info := inspectPDF(t, pdfBytes)
	if info.PageCount != len(labels) {
		t.Fatalf("PDF page count = %d, want %d", info.PageCount, len(labels))
	}
	if !info.HasFontFile2 || !info.HasToUnicode || !info.HasIdentityH {
		t.Fatalf("PDF missing embedded Unicode TrueType font markers: FontFile2=%v ToUnicode=%v IdentityH=%v",
			info.HasFontFile2, info.HasToUnicode, info.HasIdentityH)
	}

	// ISO A6 = 105mm x 148mm = 297.64pt x 419.53pt.
	if len(info.MediaBoxes) == 0 {
		t.Fatalf("PDF missing /MediaBox")
	}
	for i, box := range info.MediaBoxes {
		w := box[2] - box[0]
		h := box[3] - box[1]
		if math.Abs(w-297.64) > 1.0 || math.Abs(h-419.53) > 1.0 {
			t.Fatalf("MediaBox[%d] = %.2f x %.2f pt, want A6 (297.64 x 419.53 pt)", i, w, h)
		}
	}

	if len(info.ExtractedTexts) != len(labels) {
		t.Fatalf("extracted page text count = %d, want %d", len(info.ExtractedTexts), len(labels))
	}
	for i, pageText := range info.ExtractedTexts {
		if strings.ContainsRune(pageText, '\uFFFD') {
			t.Fatalf("page %d contains undecoded replacement char U+FFFD:\n%s", i+1, pageText)
		}
	}

	// Page 1 must retain exact Vietnamese diacritics for sender, recipient, province/ward, COD, weight, carrier.
	page1 := info.ExtractedTexts[0]
	for _, want := range []string{
		"GHN-VIET-0001",
		"ord-viet-0001",
		"GHN / STANDARD",
		"Xưởng Thời Trang Ánh Dương",
		"0901234567",
		"Phường Bến Nghé",
		"Thành phố Hồ Chí Minh",
		"Nguyễn Thị Hương Quỳnh",
		"0987654321",
		"Phường Hàng Trống",
		"Thành phố Hà Nội",
		"350.000 VND",
		"750 g",
	} {
		if !strings.Contains(page1, want) {
			t.Fatalf("page 1 missing Vietnamese string %q;\nextracted text:\n%s", want, page1)
		}
	}

	// Page 2 Vietnamese diacritics (Đắk Lắk, Đà Nẵng, Buôn Ma Thuột, Trần Đức Mạnh).
	page2 := info.ExtractedTexts[1]
	for _, want := range []string{
		"MOCK-VIET-0002",
		"Cửa Hàng Đắk Lắk Đặc Sản",
		"Tỉnh Đắk Lắk",
		"Trần Đức Mạnh",
		"Thành phố Đà Nẵng",
		"1250 g",
		"0 VND",
	} {
		if !strings.Contains(page2, want) {
			t.Fatalf("page 2 missing Vietnamese string %q;\nextracted text:\n%s", want, page2)
		}
	}

	// Page 3 legacy empty sender + long wrapped recipient stays on 1 page with diacritics intact.
	page3 := info.ExtractedTexts[2]
	for _, want := range []string{
		"GHN-LEGACY-0003",
		"Thành phố Cần Thơ",
		"Võ Thị Kiều Oanh",
		"Phường Bến Thành",
		"1.250.000 VND",
	} {
		if !strings.Contains(page3, want) {
			t.Fatalf("page 3 missing Vietnamese string %q;\nextracted text:\n%s", want, page3)
		}
	}
}

func TestRenderBatchA6_FiftyPagesMaxBatchAndEmptyError(t *testing.T) {
	if _, err := RenderBatchA6(nil); err == nil {
		t.Fatalf("RenderBatchA6(nil) must return error")
	}

	batch := make([]LabelData, 50)
	for i := range batch {
		batch[i] = LabelData{
			TrackingCode:     fmt.Sprintf("GHN-BATCH-%02d", i+1),
			OrderID:          fmt.Sprintf("ord-batch-%02d", i+1),
			ShopID:           "shop-50",
			Direction:        "FORWARD",
			CarrierCode:      "ghn",
			ServiceCode:      "STANDARD",
			Status:           "created",
			CODAmountVND:     int64((i + 1) * 10000),
			FeeVND:           22000,
			WeightGrams:      500,
			FromName:         "Kho Tổng Sài Gòn",
			FromProvinceName: "Thành phố Hồ Chí Minh",
			ToName:           fmt.Sprintf("Khách Hàng Số %d", i+1),
			ToWardName:       "Phường Bến Nghé",
			ToProvinceName:   "Thành phố Hồ Chí Minh",
			CreatedAt:        time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC),
		}
	}
	pdfBytes, err := RenderBatchA6(batch)
	if err != nil {
		t.Fatalf("RenderBatchA6(50): %v", err)
	}
	info := inspectPDF(t, pdfBytes)
	if info.PageCount != 50 {
		t.Fatalf("50-label batch PDF page count = %d, want 50", info.PageCount)
	}
}

func sampleShortAndLongestLabels() []LabelData {
	return []LabelData{
		{
			TrackingCode:      "GHN-SHORT-01",
			OrderID:           "ord-short-01",
			ShopID:            "shop-01",
			Direction:         "FORWARD",
			CarrierCode:       "ghn",
			ServiceCode:       "STANDARD",
			Status:            "created",
			CODAmountVND:      150000,
			FeeVND:            22000,
			WeightGrams:       500,
			FromName:          "Kho A",
			FromPhone:         "0901234567",
			FromStreetAddress: "12 Lê Lợi",
			FromWardName:      "Phường Sài Gòn",
			FromProvinceName:  "Thành phố Hồ Chí Minh",
			ToName:            "Nguyễn An",
			ToPhone:           "0987654321",
			ToStreetAddress:   "5 Hàng Bông",
			ToWardName:        "Phường Hoàn Kiếm",
			ToProvinceName:    "Thành phố Hà Nội",
			CreatedAt:         time.Date(2026, 11, 25, 7, 40, 0, 0, time.UTC), // 14:40 25/11/2026 ICT
		},
		{
			TrackingCode:      "GHN-LONG-20261125-999999",
			OrderID:           "0195c8e0-7c4d-7f3a-9b1e-123456789abc",
			ShopID:            "0195c8e0-shop-7f3a-9b1e-987654321def",
			Direction:         "RETURN",
			CarrierCode:       "mock_express",
			ServiceCode:       "EXPRESS",
			Status:            "delivering",
			CODAmountVND:      999999999,
			FeeVND:            123456789,
			WeightGrams:       25000,
			FromName:          "Công Ty Trách Nhiệm Hữu Hạn Sản Xuất Thời Trang Ánh Dương VN", // 60 runes
			FromPhone:         "0901234567",
			FromStreetAddress: "Số 1024 Đường Nguyễn Thị Minh Khai, Tòa nhà Văn phòng Cao cấp Mặt Trời Phương Nam, Tầng 12 Phòng 1208",
			FromWardName:      "Phường Tân Định Khu vực trung tâm hành chính mới số 1",
			FromProvinceName:  "Thành phố Hồ Chí Minh (Khu vực nội thành mở rộng phía Đông)",
			ToName:            "Nguyễn Thị Hoàng Minh Phương Anh Thư Quỳnh Trâm Ngọc Hân Yên", // 60 runes
			ToPhone:           "0987654321",
			ToStreetAddress:   "Số 888 Đại lộ Thăng Long, Khu đô thị Vinhomes Smart City, Tòa S4.02, Tầng 28 Căn hộ 2816, Lối vào cổng số 3",
			ToWardName:        "Phường Tây Mỗ Khu đô thị mới phía Tây thủ đô mở rộng",
			ToProvinceName:    "Thành phố Hà Nội (Khu vực giao nhận nhanh nội thành)",
			CreatedAt:         time.Date(2026, 11, 25, 19, 5, 0, 0, time.UTC), // 02:05 26/11/2026 ICT (midnight rollover)
		},
	}
}

func TestFormatCreatedAtVN(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "afternoon_vietnam_from_utc",
			in:   time.Date(2026, 11, 25, 7, 40, 0, 0, time.UTC),
			want: "14:40 25/11/2026",
		},
		{
			name: "midnight_day_rollover",
			in:   time.Date(2026, 11, 25, 19, 5, 0, 0, time.UTC),
			want: "02:05 26/11/2026",
		},
		{
			name: "year_rollover",
			in:   time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC),
			want: "00:00 01/01/2027",
		},
		{
			name: "non_utc_input_zone",
			in:   time.Date(2026, 11, 25, 2, 40, 0, 0, time.FixedZone("EST", -5*60*60)),
			want: "14:40 25/11/2026",
		},
		{
			name: "zero_time",
			in:   time.Time{},
			want: "—",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatCreatedAtVN(tc.in)
			if got != tc.want {
				t.Fatalf("formatCreatedAtVN(%v) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "UTC") {
				t.Fatalf("formatCreatedAtVN(%v) = %q must not contain UTC", tc.in, got)
			}
		})
	}
}

func TestRenderBatchA6_LayoutNoLineIntersectionsNoOverlapAndVietnamTime(t *testing.T) {
	labels := sampleShortAndLongestLabels()

	var recorded []placedLine
	pdfBytes, err := renderBatchA6(labels, func(pl placedLine) {
		recorded = append(recorded, pl)
	})
	if err != nil {
		t.Fatalf("renderBatchA6: %v", err)
	}

	t.Run("VietnamTimeUTC7_AndLongFieldsPreserved", func(t *testing.T) {
		info := inspectPDF(t, pdfBytes)
		if len(info.ExtractedTexts) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(info.ExtractedTexts))
		}
		for i, txt := range info.ExtractedTexts {
			if strings.Contains(txt, "UTC") {
				t.Errorf("page %d must NOT contain 'UTC'", i+1)
			}
		}
		if !strings.Contains(info.ExtractedTexts[0], "Ngày tạo: 14:40 25/11/2026") {
			t.Errorf("page 1 missing VN time 'Ngày tạo: 14:40 25/11/2026':\n%s", info.ExtractedTexts[0])
		}
		if !strings.Contains(info.ExtractedTexts[1], "Ngày tạo: 02:05 26/11/2026") {
			t.Errorf("page 2 missing VN time 'Ngày tạo: 02:05 26/11/2026' (UTC+7 rollover):\n%s", info.ExtractedTexts[1])
		}
		// Verify 9-digit COD/fee, 60-char sender/recipient phones, and footer page indicator are preserved.
		for _, want := range []string{"999.999.999 VND", "123.456.789 VND", "0987654321", "0901234567", "Trang 2 / 2"} {
			if !strings.Contains(info.ExtractedTexts[1], want) {
				t.Errorf("page 2 missing %q:\n%s", want, info.ExtractedTexts[1])
			}
		}
	})

	t.Run("Geometry_NoFrameOrDividerIntersection_AndNoColumnOverlap", func(t *testing.T) {
		// Frame x=5..100, y=5..143 (half stroke 0.225mm); horizontal dividers y={19,37,67,103,125} (half stroke 0.15mm);
		// Band 5 vertical divider x=55 (half stroke 0.125mm).
		const (
			frameLeft     = 5.0 + 0.225
			frameRight    = 100.0 - 0.225
			frameTop      = 5.0 + 0.225
			frameBottom   = 143.0 - 0.225
			dividerHalfW  = 0.15
			vDividerX     = 55.0
			vDividerHalfW = 0.125
			colSplitX     = 55.0
		)
		dividers := []float64{19.0, 37.0, 67.0, 103.0, 125.0}

		// Group hook-recorded lines by page and verify against actual emitted PDF text streams.
		byPage := make(map[int][]placedLine, len(labels))
		for _, pl := range recorded {
			byPage[pl.Page] = append(byPage[pl.Page], pl)
		}

		pdfPages := extractPDFTextBoxes(t, pdfBytes)
		if len(pdfPages) != len(labels) {
			t.Fatalf("extractPDFTextBoxes page count = %d, want %d", len(pdfPages), len(labels))
		}

		for pNum := 1; pNum <= len(labels); pNum++ {
			hookLines := byPage[pNum]
			pdfBoxes := pdfPages[pNum-1]
			if len(hookLines) == 0 {
				t.Fatalf("page %d recorded 0 lines from hook", pNum)
			}
			if len(hookLines) != len(pdfBoxes) {
				t.Fatalf("page %d hook lines (%d) != PDF stream text boxes (%d)", pNum, len(hookLines), len(pdfBoxes))
			}

			// Cross-check hook coordinates against actual emitted PDF TD/Tf coordinates.
			for i := range hookLines {
				h, p := hookLines[i], pdfBoxes[i]
				if h.Text != p.Text {
					t.Errorf("page %d line[%d] text mismatch: hook=%q pdf=%q", pNum, i, h.Text, p.Text)
				}
				if math.Abs(h.X-p.X) > 0.05 || math.Abs(h.Baseline-p.Baseline) > 0.05 ||
					math.Abs(h.Top-p.Top) > 0.05 || math.Abs(h.Bottom-p.Bottom) > 0.05 {
					t.Errorf("page %d line %q hook vs PDF coord drift: hook=(x=%.3f top=%.3f base=%.3f bot=%.3f) pdf=(x=%.3f top=%.3f base=%.3f bot=%.3f)",
						pNum, h.Text, h.X, h.Top, h.Baseline, h.Bottom, p.X, p.Top, p.Baseline, p.Bottom)
				}
			}

			// Verify every line stays inside outer frame and clear of all dividers.
			for _, b := range hookLines {
				if b.X <= frameLeft {
					t.Errorf("page %d line %q intersects/exceeds left frame x=%.3f: x=%.3f", pNum, b.Text, frameLeft, b.X)
				}
				if b.Right >= frameRight {
					t.Errorf("page %d line %q intersects/exceeds right frame x=%.3f: right=%.3f", pNum, b.Text, frameRight, b.Right)
				}
				if b.Top <= frameTop {
					t.Errorf("page %d line %q intersects/exceeds top frame y=%.3f: top=%.3f baseline=%.3f",
						pNum, b.Text, frameTop, b.Top, b.Baseline)
				}
				if b.Bottom >= frameBottom {
					t.Errorf("page %d line %q intersects/exceeds bottom frame y=%.3f: bottom=%.3f baseline=%.3f",
						pNum, b.Text, frameBottom, b.Bottom, b.Baseline)
				}
				for _, dy := range dividers {
					if b.Top < dy+dividerHalfW && b.Bottom > dy-dividerHalfW {
						t.Errorf("page %d line %q intersects divider y=%.1f: [top=%.3f, bottom=%.3f] (baseline=%.3f, %.1fpt)",
							pNum, b.Text, dy, b.Top, b.Bottom, b.Baseline, b.FontPT)
					}
				}
				// Band 5 vertical divider check (y in 103..125).
				if b.Top > 103.0 && b.Bottom < 125.0 {
					if b.X < vDividerX && b.Right >= vDividerX-vDividerHalfW {
						t.Errorf("page %d band 5 left line %q intersects vertical divider x=%.1f: right=%.3f", pNum, b.Text, vDividerX, b.Right)
					}
					if b.X >= vDividerX && b.X <= vDividerX+vDividerHalfW {
						t.Errorf("page %d band 5 right line %q intersects vertical divider x=%.1f: x=%.3f", pNum, b.Text, vDividerX, b.X)
					}
				}
			}

			// No two boxes in the same column may overlap vertically, and no two boxes anywhere may overlap in 2D.
			colOf := func(b placedLine) int {
				if b.X >= colSplitX {
					return 1
				}
				return 0
			}
			for i := 0; i < len(hookLines); i++ {
				for j := i + 1; j < len(hookLines); j++ {
					a, b := hookLines[i], hookLines[j]
					yOverlap := a.Top < b.Bottom && b.Top < a.Bottom
					if !yOverlap {
						continue
					}
					if colOf(a) == colOf(b) {
						t.Errorf("page %d vertical overlap in col %d between %q [%.3f..%.3f] and %q [%.3f..%.3f]",
							pNum, colOf(a), a.Text, a.Top, a.Bottom, b.Text, b.Top, b.Bottom)
					}
					xOverlap := a.X < b.Right && b.X < a.Right
					if xOverlap {
						t.Errorf("page %d 2D box overlap between %q [x=%.2f..%.2f y=%.2f..%.2f] and %q [x=%.2f..%.2f y=%.2f..%.2f]",
							pNum, a.Text, a.X, a.Right, a.Top, a.Bottom, b.Text, b.X, b.Right, b.Top, b.Bottom)
					}
				}
			}
		}

		// Confirm page 2 (worst-case longest input) genuinely filled 2 lines each for contact, street, and ward/province
		// in both Band 3 (sender: y 37..67) and Band 4 (recipient: y 67..103), i.e., 1 heading + 6 wrapped lines = 7 lines per band.
		countInBand := func(lines []placedLine, yMin, yMax float64) int {
			n := 0
			for _, l := range lines {
				if l.Top >= yMin && l.Bottom <= yMax {
					n++
				}
			}
			return n
		}
		if got := countInBand(byPage[2], 37.0, 67.0); got != 7 {
			t.Errorf("page 2 sender band line count = %d, want 7 (1 heading + 2 contact + 2 street + 2 ward/province)", got)
		}
		if got := countInBand(byPage[2], 67.0, 103.0); got != 7 {
			t.Errorf("page 2 recipient band line count = %d, want 7 (1 heading + 2 contact + 2 street + 2 ward/province)", got)
		}
	})

	t.Run("FooterPageCounter_FiftyPagesInsideFrame", func(t *testing.T) {
		var lastPageLines []placedLine
		batch := make([]LabelData, 50)
		for i := range batch {
			batch[i] = labels[1]
		}
		if _, err := renderBatchA6(batch, func(pl placedLine) {
			if pl.Page == 50 {
				lastPageLines = append(lastPageLines, pl)
			}
		}); err != nil {
			t.Fatalf("renderBatchA6(50): %v", err)
		}
		found := false
		for _, pl := range lastPageLines {
			if pl.Text == "Trang 50 / 50" {
				found = true
				if pl.X <= 66.0 || pl.Right >= 100.0-0.225 || pl.Bottom >= 143.0-0.225 {
					t.Errorf("Trang 50 / 50 out of safe bounds: x=%.3f right=%.3f top=%.3f bottom=%.3f",
						pl.X, pl.Right, pl.Top, pl.Bottom)
				}
			}
		}
		if !found {
			t.Fatalf("page 50 missing 'Trang 50 / 50'")
		}
	})
}
