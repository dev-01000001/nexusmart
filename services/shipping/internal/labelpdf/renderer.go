package labelpdf

import (
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"github.com/signintech/gopdf"
	"golang.org/x/text/unicode/norm"
)

const (
	// ISO A6 paper size in millimeters (105 x 148 mm).
	a6WidthMM  = 105.0
	a6HeightMM = 148.0

	// Typographic unit conversion and embedded LiberationSans-Regular.ttf metrics
	// (UPEM = 2048, hhea/OS2 WinAscent = 1854, WinDescent = 434). Using WinAscent/WinDescent
	// bounds stacked Vietnamese diacritics accurately (e.g. "ẳ" reaches +1835, "|" reaches -434).
	mmPerPT          = 25.4 / 72.0
	fontAscentRatio  = 1854.0 / 2048.0
	fontDescentRatio = 434.0 / 2048.0

	fontFamilyRegular = "liberation-sans"
)

// vietnamLocation is fixed UTC+7 (ICT) so label timestamps never depend on host tzdata.
var vietnamLocation = time.FixedZone("ICT", 7*60*60)

//go:embed fonts/LiberationSans-Regular.ttf
var fontRegularTTF []byte

// placedLine records the physical bounding box (in mm from page top-left) of a drawn text line.
type placedLine struct {
	Page     int
	X        float64
	Right    float64
	Baseline float64
	Top      float64
	Bottom   float64
	FontPT   float64
	Text     string
}

type pageWriter struct {
	pdf        *gopdf.GoPdf
	pageNum    int
	fontSizePT float64
	onLine     func(placedLine)
}

func fontAscentMM(sizePT float64) float64 {
	return sizePT * mmPerPT * fontAscentRatio
}

func fontDescentMM(sizePT float64) float64 {
	return sizePT * mmPerPT * fontDescentRatio
}

// LabelData carries all fields rendered onto one A6 shipping label page.
type LabelData struct {
	TrackingCode      string
	OrderID           string
	ShopID            string
	Direction         string
	CarrierCode       string
	ServiceCode       string
	Status            string
	CODAmountVND      int64
	FeeVND            int64
	WeightGrams       int32
	FromName          string
	FromPhone         string
	FromStreetAddress string
	FromWardName      string
	FromProvinceName  string
	ToName            string
	ToPhone           string
	ToStreetAddress   string
	ToWardName        string
	ToProvinceName    string
	CreatedAt         time.Time
}

// RenderBatchA6 generates a single multi-page A6 (105x148 mm) PDF containing one
// Vietnamese-enabled shipping label per entry in labels (G5).
func RenderBatchA6(labels []LabelData) ([]byte, error) {
	return renderBatchA6(labels, nil)
}

func renderBatchA6(labels []LabelData, onLine func(placedLine)) ([]byte, error) {
	if len(labels) == 0 {
		return nil, errors.New("at least one label is required")
	}

	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		Unit:     gopdf.UnitMM,
		PageSize: gopdf.Rect{W: a6WidthMM, H: a6HeightMM},
	})
	pdf.SetInfo(gopdf.PdfInfo{
		Title:    "NexusMart - Phiếu vận đơn A6",
		Producer: "NexusMart Shipping Service",
	})
	if err := pdf.AddTTFFontData(fontFamilyRegular, fontRegularTTF); err != nil {
		return nil, fmt.Errorf("load embedded Vietnamese TTF font: %w", err)
	}

	total := len(labels)
	for i, lbl := range labels {
		w := &pageWriter{
			pdf:     &pdf,
			pageNum: i + 1,
			onLine:  onLine,
		}
		if err := renderA6Page(w, lbl, i+1, total); err != nil {
			return nil, fmt.Errorf("render label page %d (%s): %w", i+1, lbl.TrackingCode, err)
		}
	}

	out, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("encode batch PDF: %w", err)
	}
	return out, nil
}

func renderA6Page(w *pageWriter, l LabelData, pageNum, totalPages int) error {
	pdf := w.pdf
	pdf.AddPage()

	const (
		marginX    = 5.0
		marginY    = 5.0
		boxW       = a6WidthMM - 2*marginX  // 95 mm
		boxH       = a6HeightMM - 2*marginY // 138 mm
		padX       = 3.0
		textX      = marginX + padX   // 8 mm
		contentW   = boxW - 2*padX    // 89 mm
		dividerMid = marginX + 50.0   // 55 mm
		rightColX  = dividerMid + 3.0 // 58 mm
		rightColW  = (marginX + boxW) - rightColX - padX
		leftColW   = dividerMid - textX - 2.0
	)

	// Outer frame.
	pdf.SetStrokeColor(25, 25, 25)
	pdf.SetTextColor(15, 15, 15)
	pdf.SetLineWidth(0.45)
	pdf.RectFromUpperLeftWithStyle(marginX, marginY, boxW, boxH, "D")

	// 1. Header band (y: 5 -> 19 mm). All y values passed to w.draw* are TOP edges in mm.
	if err := w.setFont(10.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 7.2, contentW, "NEXUSMART — PHIẾU VẬN ĐƠN"); err != nil {
		return err
	}
	if err := w.setFont(8.0); err != nil {
		return err
	}
	carrierLine := fmt.Sprintf("Hãng / Dịch vụ: %s  |  %s", formatCarrierService(l.CarrierCode, l.ServiceCode), formatDirectionVI(l.Direction))
	if err := w.drawSingleLine(textX, 13.2, contentW, carrierLine); err != nil {
		return err
	}
	drawHLine(pdf, marginX, 19.0, marginX+boxW, 0.3)

	// 2. Tracking & Order band (y: 19 -> 37 mm).
	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 20.6, contentW, "MÃ VẬN ĐƠN"); err != nil {
		return err
	}
	if err := w.setFont(13.0); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 24.8, contentW, l.TrackingCode); err != nil {
		return err
	}
	if err := w.setFont(8.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 31.6, contentW, "Mã đơn hàng: "+strings.TrimSpace(l.OrderID)); err != nil {
		return err
	}
	drawHLine(pdf, marginX, 37.0, marginX+boxW, 0.3)

	// 3. Sender band (y: 37 -> 67 mm).
	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 38.2, contentW, "NGƯỜI GỬI"); err != nil {
		return err
	}
	if err := w.setFont(8.5); err != nil {
		return err
	}
	y := 41.9
	var err error
	y, err = w.drawWrappedLines(textX, y, contentW, 3.85, formatContact(l.FromName, l.FromPhone), 2)
	if err != nil {
		return err
	}
	y += 0.3
	if err := w.setFont(8.0); err != nil {
		return err
	}
	if street := sanitizePDFText(l.FromStreetAddress); street != "" {
		y, err = w.drawWrappedLines(textX, y, contentW, 3.7, street, 2)
		if err != nil {
			return err
		}
		y += 0.3
	}
	if loc := formatLocation(l.FromWardName, l.FromProvinceName); loc != "" {
		if _, err := w.drawWrappedLines(textX, y, contentW, 3.7, loc, 2); err != nil {
			return err
		}
	}
	drawHLine(pdf, marginX, 67.0, marginX+boxW, 0.3)

	// 4. Recipient band (y: 67 -> 103 mm).
	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 68.5, contentW, "NGƯỜI NHẬN"); err != nil {
		return err
	}
	if err := w.setFont(9.5); err != nil {
		return err
	}
	y = 72.4
	y, err = w.drawWrappedLines(textX, y, contentW, 4.35, formatContact(l.ToName, l.ToPhone), 2)
	if err != nil {
		return err
	}
	y += 0.5
	if err := w.setFont(8.5); err != nil {
		return err
	}
	if street := sanitizePDFText(l.ToStreetAddress); street != "" {
		y, err = w.drawWrappedLines(textX, y, contentW, 4.1, street, 2)
		if err != nil {
			return err
		}
		y += 0.5
	}
	if loc := formatLocation(l.ToWardName, l.ToProvinceName); loc != "" {
		if _, err := w.drawWrappedLines(textX, y, contentW, 4.1, loc, 2); err != nil {
			return err
		}
	}
	drawHLine(pdf, marginX, 103.0, marginX+boxW, 0.3)

	// 5. COD & Weight two-column band (y: 103 -> 125 mm).
	pdf.SetLineWidth(0.25)
	pdf.Line(dividerMid, 103.0, dividerMid, 125.0)

	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 104.8, leftColW, "THU HỘ (COD)"); err != nil {
		return err
	}
	if err := w.setFont(11.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 109.6, leftColW, formatVND(l.CODAmountVND)); err != nil {
		return err
	}
	if err := w.setFont(7.5); err != nil {
		return err
	}
	codHint := "Đã thanh toán / Không thu hộ"
	if l.CODAmountVND > 0 {
		codHint = "Thu tiền mặt khi giao hàng"
	}
	if err := w.drawSingleLine(textX, 117.2, leftColW, codHint); err != nil {
		return err
	}

	if err := w.setFont(8.0); err != nil {
		return err
	}
	if err := w.drawSingleLine(rightColX, 104.8, rightColW, fmt.Sprintf("Khối lượng: %d g", l.WeightGrams)); err != nil {
		return err
	}
	if err := w.setFont(7.8); err != nil {
		return err
	}
	if err := w.drawSingleLine(rightColX, 111.0, rightColW, "Phí ship: "+formatVND(l.FeeVND)); err != nil {
		return err
	}
	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(rightColX, 117.2, rightColW, "Ngày tạo: "+formatCreatedAtVN(l.CreatedAt)); err != nil {
		return err
	}
	drawHLine(pdf, marginX, 125.0, marginX+boxW, 0.3)

	// 6. Footer band (y: 125 -> 143 mm).
	if err := w.setFont(8.0); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 126.8, contentW, "Trạng thái: "+carrier.StatusDescriptionVI(l.Status)); err != nil {
		return err
	}
	if err := w.setFont(7.5); err != nil {
		return err
	}
	if err := w.drawSingleLine(textX, 131.6, contentW, "Mã gian hàng: "+strings.TrimSpace(l.ShopID)); err != nil {
		return err
	}
	footerRight := fmt.Sprintf("Trang %d / %d", pageNum, totalPages)
	if err := w.drawSingleLine(textX, 137.0, 58.0, "Chữ ký người nhận: ______________"); err != nil {
		return err
	}
	if err := w.drawRightAlignedLine(marginX+boxW-padX, 137.0, 28.0, footerRight); err != nil {
		return err
	}
	return nil
}

func (w *pageWriter) setFont(sizePT float64) error {
	if err := w.pdf.SetFont(fontFamilyRegular, "", sizePT); err != nil {
		return err
	}
	w.fontSizePT = sizePT
	return nil
}

func drawHLine(pdf *gopdf.GoPdf, x1, y, x2, widthMM float64) {
	pdf.SetLineWidth(widthMM)
	pdf.Line(x1, y, x2, y)
}

// drawTextAtTop converts a top-edge y coordinate (mm) into the gopdf.Text baseline
// coordinate by adding the current font's ascent, and optionally emits the placed box.
func (w *pageWriter) drawTextAtTop(x, topY float64, text string) error {
	ascent := fontAscentMM(w.fontSizePT)
	descent := fontDescentMM(w.fontSizePT)
	baseline := topY + ascent
	w.pdf.SetXY(x, baseline)
	if err := w.pdf.Text(text); err != nil {
		return err
	}
	if w.onLine != nil {
		widthMM, err := w.pdf.MeasureTextWidth(text)
		if err != nil {
			return err
		}
		w.onLine(placedLine{
			Page:     w.pageNum,
			X:        x,
			Right:    x + widthMM,
			Baseline: baseline,
			Top:      baseline - ascent,
			Bottom:   baseline + descent,
			FontPT:   w.fontSizePT,
			Text:     text,
		})
	}
	return nil
}

func (w *pageWriter) fitSingleLine(text string, maxWidth float64) (string, error) {
	clean := sanitizePDFText(text)
	if clean == "" {
		return "", nil
	}
	lines, err := splitToWidth(w.pdf, clean, maxWidth)
	if err != nil {
		return "", err
	}
	if len(lines) > 1 {
		return truncateLine(w.pdf, lines[0], maxWidth), nil
	}
	if len(lines) == 1 {
		return lines[0], nil
	}
	return clean, nil
}

func (w *pageWriter) drawSingleLine(x, topY, maxWidth float64, text string) error {
	line, err := w.fitSingleLine(text, maxWidth)
	if err != nil || line == "" {
		return err
	}
	return w.drawTextAtTop(x, topY, line)
}

func (w *pageWriter) drawRightAlignedLine(rightEdge, topY, maxWidth float64, text string) error {
	line, err := w.fitSingleLine(text, maxWidth)
	if err != nil || line == "" {
		return err
	}
	widthMM, err := w.pdf.MeasureTextWidth(line)
	if err != nil {
		return err
	}
	x := rightEdge - widthMM
	if minX := rightEdge - maxWidth; x < minX {
		x = minX
	}
	return w.drawTextAtTop(x, topY, line)
}

func (w *pageWriter) drawWrappedLines(x, topY, maxWidth, lineHeight float64, text string, maxLines int) (float64, error) {
	clean := sanitizePDFText(text)
	if clean == "" || maxLines <= 0 {
		return topY, nil
	}
	lines, err := splitToWidth(w.pdf, clean, maxWidth)
	if err != nil {
		return topY, err
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncateLine(w.pdf, lines[maxLines-1], maxWidth)
	}
	curY := topY
	for _, line := range lines {
		if err := w.drawTextAtTop(x, curY, line); err != nil {
			return curY, err
		}
		curY += lineHeight
	}
	return curY, nil
}

func formatCreatedAtVN(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(vietnamLocation).Format("15:04 02/01/2006")
}

func splitToWidth(pdf *gopdf.GoPdf, text string, maxWidth float64) ([]string, error) {
	lines, err := pdf.SplitTextWithWordWrap(text, maxWidth)
	if err != nil {
		return pdf.SplitText(text, maxWidth)
	}
	// Ensure no individual unbroken token (e.g. long tracking/order UUID) exceeds maxWidth.
	var out []string
	for _, l := range lines {
		w, mErr := pdf.MeasureTextWidth(l)
		if mErr == nil && w > maxWidth {
			sub, sErr := pdf.SplitText(l, maxWidth)
			if sErr == nil && len(sub) > 0 {
				out = append(out, sub...)
				continue
			}
		}
		out = append(out, l)
	}
	return out, nil
}

func truncateLine(pdf *gopdf.GoPdf, line string, maxWidth float64) string {
	const ellipsis = "..."
	rs := []rune(strings.TrimSpace(line))
	for len(rs) > 0 {
		cand := strings.TrimSpace(string(rs)) + ellipsis
		w, err := pdf.MeasureTextWidth(cand)
		if err == nil && w <= maxWidth {
			return cand
		}
		rs = rs[:len(rs)-1]
	}
	return ellipsis
}

// sanitizePDFText normalizes Unicode to NFC (so Vietnamese diacritics use precomposed glyphs)
// and collapses control/newline characters to single spaces.
func sanitizePDFText(s string) string {
	s = norm.NFC.String(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' || unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

func formatCarrierService(carrierCode, serviceCode string) string {
	c := strings.ToUpper(strings.TrimSpace(carrierCode))
	s := strings.TrimSpace(serviceCode)
	switch {
	case c != "" && s != "":
		return c + " / " + s
	case c != "":
		return c
	case s != "":
		return s
	default:
		return "—"
	}
}

func formatDirectionVI(direction string) string {
	if store.NormalizeDirection(direction) == store.DirectionReturn {
		return "Trả hàng (Chiều về)"
	}
	return "Giao hàng (Chiều đi)"
}

func formatContact(name, phone string) string {
	n := sanitizePDFText(name)
	p := sanitizePDFText(phone)
	switch {
	case n != "" && p != "":
		return n + " — " + p
	case n != "":
		return n
	case p != "":
		return p
	default:
		return "—"
	}
}

func formatLocation(ward, province string) string {
	w := sanitizePDFText(ward)
	p := sanitizePDFText(province)
	switch {
	case w != "" && p != "":
		return w + ", " + p
	case w != "":
		return w
	case p != "":
		return p
	default:
		return ""
	}
}

// formatVND formats an integer VND amount with dot thousands separators (never uses floats).
func formatVND(amount int64) string {
	neg := amount < 0
	var digits string
	if neg {
		digits = strconv.FormatUint(uint64(-amount), 10)
	} else {
		digits = strconv.FormatUint(uint64(amount), 10)
	}
	n := len(digits)
	if n <= 3 {
		if neg {
			return "-" + digits + " VND"
		}
		return digits + " VND"
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := n % 3
	if rem == 0 {
		rem = 3
	}
	b.WriteString(digits[:rem])
	for i := rem; i < n; i += 3 {
		b.WriteByte('.')
		b.WriteString(digits[i : i+3])
	}
	b.WriteString(" VND")
	return b.String()
}
