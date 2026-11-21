package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
)

// Compile-time check that MockProvider implements Provider.
var _ Provider = (*MockProvider)(nil)

// MockProvider is a fast, deterministic Vietnamese mock LLM supporting full
// multi-turn tool calling and structured completion tasks.
type MockProvider struct {
	inputVNDPer1K  int64
	outputVNDPer1K int64
	callSeq        atomic.Int64
}

// NewMockProvider constructs a MockProvider with configured per-1K-token VND rates.
func NewMockProvider(inputVNDPer1K, outputVNDPer1K int64) *MockProvider {
	if inputVNDPer1K < 0 {
		inputVNDPer1K = 0
	}
	if outputVNDPer1K < 0 {
		outputVNDPer1K = 0
	}
	return &MockProvider{
		inputVNDPer1K:  inputVNDPer1K,
		outputVNDPer1K: outputVNDPer1K,
	}
}

// Name returns the provider identifier.
func (m *MockProvider) Name() string {
	return "mock"
}

var (
	orderIDExtractRe = regexp.MustCompile(`(?i)\b(?:nm|ord|order)-[a-z0-9-]+\b`)
	skuIDExtractRe   = regexp.MustCompile(`(?i)\bsku-[a-z0-9-]+\b`)
	qtyExplicitRe    = regexp.MustCompile(`(?i)(?:so\s*luong|sl|x)\s*[:=]?\s*(\d{1,2})\b|\b(\d{1,2})\s*(?:cai|chiec|doi|bo|san pham|sp)\b`)

	priceRangeRe    = regexp.MustCompile(`(?:tu|khoang)\s+(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)?\s*(?:den|toi|-)\s*(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)?`)
	priceMaxRe      = regexp.MustCompile(`(?:duoi|toi da|khong qua|nho hon|<=|<)\s*(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)?`)
	priceMaxTrailRe = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)\s+tro\s+xuong`)
	priceMinRe      = regexp.MustCompile(`(?:tren|toi thieu|lon hon|>=|>)\s*(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)?`)
	priceMinTrailRe = regexp.MustCompile(`(?:tu\s+)?(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)\s+tro\s+len`)
	priceExactRe    = regexp.MustCompile(`(?:gia|tam)\s*(\d+(?:[.,]\d+)?)\s*(trieu|tr|k|nghin|ngan|vnd|dong|d)\b`)
)

// StreamChat runs a deterministic mock turn: emitting a tool call on Turn 0 or
// streaming a grounded Vietnamese response from the tool result on Turn 1.
func (m *MockProvider) StreamChat(ctx context.Context, req ChatRequest, onEvent func(StreamEvent) error) (Usage, error) {
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}

	inTokens := m.estimateRequestInputTokens(req)
	if len(req.Messages) == 0 {
		outText := "Xin chào! Tôi là trợ lý mua sắm NexusMart. Tôi có thể hỗ trợ bạn tìm sản phẩm, tra cứu chính sách, kiểm tra đơn hàng hoặc kết nối nhân viên hỗ trợ."
		if err := streamTextChunks(ctx, outText, onEvent); err != nil {
			return Usage{}, err
		}
		return m.makeUsage(inTokens, EstimateTokens(outText)), nil
	}

	lastMsg := req.Messages[len(req.Messages)-1]
	if lastMsg.Role == RoleTool {
		outText := m.renderToolResponse(req, lastMsg)
		if err := streamTextChunks(ctx, outText, onEvent); err != nil {
			return Usage{}, err
		}
		return m.makeUsage(inTokens, EstimateTokens(outText)), nil
	}

	lastUser := latestUserMessage(req.Messages)
	folded := foldVI(lastUser)

	// 1. Out-of-scope and forbidden auto-checkout checks FIRST (no tools, no hallucinated products).
	if refusal, matched := matchOutOfScope(folded); matched {
		if err := streamTextChunks(ctx, refusal, onEvent); err != nil {
			return Usage{}, err
		}
		return m.makeUsage(inTokens, EstimateTokens(refusal)), nil
	}

	// 2. Handoff / dispute / money issue (excluding policy-only questions about complaints).
	if isHandoffIntent(folded) {
		if hasTool(req.Tools, "handoff_to_agent") {
			tc := &ToolCall{
				ID:   m.nextToolCallID(),
				Name: "handoff_to_agent",
				Arguments: map[string]any{
					"shop_id": req.ShopID,
					"reason":  lastUser,
				},
			}
			if onEvent != nil {
				if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
					return Usage{}, err
				}
			}
			return m.makeUsage(inTokens, 24), nil
		}
	}

	// 3. Seller business report (strictly respecting RBAC tool allowlist).
	if isSellerReportIntent(folded) {
		if hasTool(req.Tools, "get_seller_report") {
			tc := &ToolCall{
				ID:   m.nextToolCallID(),
				Name: "get_seller_report",
				Arguments: map[string]any{
					"period":   parseReportPeriod(folded),
					"question": lastUser,
				},
			}
			if onEvent != nil {
				if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
					return Usage{}, err
				}
			}
			return m.makeUsage(inTokens, 20), nil
		}
		outText := "Xin lỗi, chỉ người bán đã đăng nhập và xác thực gian hàng mới có quyền xem báo cáo kinh doanh của chính gian hàng mình."
		if err := streamTextChunks(ctx, outText, onEvent); err != nil {
			return Usage{}, err
		}
		return m.makeUsage(inTokens, EstimateTokens(outText)), nil
	}

	// 4. Order status lookup.
	if isOrderLookupIntent(folded) && hasTool(req.Tools, "get_order_status") {
		args := map[string]any{}
		if orderID := extractOrderID(lastUser); orderID != "" {
			args["order_id"] = orderID
		}
		tc := &ToolCall{
			ID:        m.nextToolCallID(),
			Name:      "get_order_status",
			Arguments: args,
		}
		if onEvent != nil {
			if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
				return Usage{}, err
			}
		}
		return m.makeUsage(inTokens, 20), nil
	}

	// 5. Add to cart.
	if isAddToCartIntent(folded) && hasTool(req.Tools, "add_to_cart") {
		skuID := extractSKUIDFromHistory(req.Messages)
		qty := extractQuantity(folded)
		confirmed := hasAnySubstr(folded, "dong y", "xac nhan", "ok them", "chac chan")
		if skuID != "" || req.UserRole == "" {
			args := map[string]any{
				"sku_id":    skuID,
				"quantity":  qty,
				"confirmed": confirmed,
			}
			tc := &ToolCall{
				ID:        m.nextToolCallID(),
				Name:      "add_to_cart",
				Arguments: args,
			}
			if onEvent != nil {
				if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
					return Usage{}, err
				}
			}
			return m.makeUsage(inTokens, 24), nil
		}
		// Authenticated user named a product without SKU ID: search catalog first.
		if hasTool(req.Tools, "search_products") {
			args := buildSearchArgs(req, lastUser)
			tc := &ToolCall{
				ID:        m.nextToolCallID(),
				Name:      "search_products",
				Arguments: args,
			}
			if onEvent != nil {
				if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
					return Usage{}, err
				}
			}
			return m.makeUsage(inTokens, 24), nil
		}
	}

	// 6. Policy / FAQ.
	if isPolicyIntent(folded) && hasTool(req.Tools, "search_policies") {
		tc := &ToolCall{
			ID:   m.nextToolCallID(),
			Name: "search_policies",
			Arguments: map[string]any{
				"query": lastUser,
			},
		}
		if onEvent != nil {
			if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
				return Usage{}, err
			}
		}
		return m.makeUsage(inTokens, 20), nil
	}

	// 7. Default: Product search scoped to ShopID.
	if hasTool(req.Tools, "search_products") {
		args := buildSearchArgs(req, lastUser)
		tc := &ToolCall{
			ID:        m.nextToolCallID(),
			Name:      "search_products",
			Arguments: args,
		}
		if onEvent != nil {
			if err := onEvent(StreamEvent{Type: EventToolCall, ToolCall: tc}); err != nil {
				return Usage{}, err
			}
		}
		return m.makeUsage(inTokens, 24), nil
	}

	outText := "Hiện tại tôi chưa có công cụ phù hợp để xử lý yêu cầu này. Bạn vui lòng liên hệ nhân viên hỗ trợ NexusMart."
	if err := streamTextChunks(ctx, outText, onEvent); err != nil {
		return Usage{}, err
	}
	return m.makeUsage(inTokens, EstimateTokens(outText)), nil
}

// Complete handles single-shot structured generation tasks deterministically in Vietnamese.
func (m *MockProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	if err := ctx.Err(); err != nil {
		return CompleteResponse{}, err
	}

	inText := req.SystemPrompt + "\n" + req.Prompt
	if len(req.Metadata) > 0 {
		if b, err := json.Marshal(req.Metadata); err == nil {
			inText += "\n" + string(b)
		}
	}
	inTokens := EstimateTokens(inText)

	var outText string
	switch strings.ToLower(strings.TrimSpace(req.Task)) {
	case "review_summary":
		outText = completeReviewSummary(req)
	case "describe_product":
		outText = completeDescribeProduct(req)
	case "suggest_reply":
		outText = completeSuggestReply(req)
	case "handoff_summary":
		outText = completeHandoffSummary(req)
	default:
		outText = "Tôi đã tiếp nhận yêu cầu và tổng hợp thông tin dựa trên dữ liệu hợp lệ được cung cấp."
	}

	return CompleteResponse{
		Text:  outText,
		Usage: m.makeUsage(inTokens, EstimateTokens(outText)),
	}, nil
}

func (m *MockProvider) nextToolCallID() string {
	n := m.callSeq.Add(1)
	return fmt.Sprintf("toolu_mock_%d", n)
}

func (m *MockProvider) makeUsage(inTokens, outTokens int) Usage {
	return Usage{
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		TotalTokens:  inTokens + outTokens,
		CostVND:      EstimateCostVND(inTokens, outTokens, m.inputVNDPer1K, m.outputVNDPer1K),
	}
}

func (m *MockProvider) estimateRequestInputTokens(req ChatRequest) int {
	total := EstimateTokens(req.SystemPrompt)
	for _, msg := range req.Messages {
		total += EstimateTokens(msg.Content)
	}
	if total < 1 {
		total = 1
	}
	return total
}

func hasTool(tools []ToolDefinition, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func latestUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func matchOutOfScope(folded string) (string, bool) {
	autoPay := []string{
		"thanh toan ho", "tu dat hang", "checkout giup", "checkout ho",
		"tu thanh toan", "tu dong thanh toan", "tu checkout",
		"chuyen khoan ho", "chuyen khoan giup", "tu chuyen khoan", "tra tien ho",
	}
	if hasAnySubstr(folded, autoPay...) {
		return "Xin lỗi, trợ lý AI không thể tự đặt hàng, thanh toán hộ hay chuyển khoản thay bạn. Để bảo đảm an toàn, bạn vui lòng tự kiểm tra giỏ hàng và xác nhận thanh toán trực tiếp trên NexusMart.", true
	}

	outOfScope := []string{
		"thoi tiet", "du bao thoi tiet", "nhiet do hom nay",
		"viet code", "lap trinh", "python", "javascript", "golang", "thuat toan",
		"giai phuong trinh", "tinh tich phan", "dao ham", "bai tap toan", "so nguyen to",
		"dau dau uong thuoc", "uong thuoc gi", "chan doan benh", "tu van y te",
		"tu van luat", "luat su", "bo luat", "ly hon",
		"bong da", "ty so tran", "world cup", "bau cu", "chinh tri", "tong thong",
		"cong thuc nau an", "cach nau pho", "cach nau bun",
	}
	if hasAnySubstr(folded, outOfScope...) {
		return "Xin lỗi, tôi là trợ lý mua sắm của NexusMart nên chỉ hỗ trợ tư vấn sản phẩm trong gian hàng, tra cứu chính sách, kiểm tra đơn hàng và chuyển tiếp nhân viên hỗ trợ. Câu hỏi này nằm ngoài phạm vi của tôi.", true
	}
	return "", false
}

func isHandoffIntent(folded string) bool {
	explicitAgent := []string{
		"gap nhan vien", "chuyen nhan vien", "nhan vien ho tro",
		"ket noi nhan vien", "ho tro truc tiep", "tu van vien",
		"noi chuyen voi nguoi", "tong dai",
	}
	if hasAnySubstr(folded, explicitAgent...) {
		return true
	}
	dispute := []string{
		"mat tien", "chuyen nham", "chuyen khoan nham", "lua dao", "tranh chap", "khieu nai",
	}
	if !hasAnySubstr(folded, dispute...) {
		return false
	}
	// Policy questions about how complaints/disputes work belong in search_policies.
	policyCues := []string{"chinh sach", "quy dinh", "quy trinh", "nhu the nao", "ra sao", "bao lau", "phan xu"}
	if hasAnySubstr(folded, policyCues...) && !hasAnySubstr(folded, "toi bi", "don cua toi", "giup toi", "muon khieu nai", "chuyen nham", "mat tien") {
		return false
	}
	return true
}

func isSellerReportIntent(folded string) bool {
	reportKeys := []string{
		"doanh thu", "ban chay", "bao cao", "ty le huy", "ti le huy",
		"don cua gian hang", "don hang cua gian hang", "thong ke gian hang",
		"thong ke ban hang", "tinh hinh kinh doanh", "hieu suat gian hang",
		"so du cua toi", "so du gian hang", "so du hien tai", "so du kha dung",
	}
	if hasAnySubstr(folded, reportKeys...) {
		return true
	}
	if strings.Contains(folded, "so du") {
		policyCues := []string{"chinh sach", "quy dinh", "escrow", "khoa rut", "24 gio", "24h", "otp", "rut tien nhu the nao"}
		return !hasAnySubstr(folded, policyCues...)
	}
	return false
}

func parseReportPeriod(folded string) string {
	if hasAnySubstr(folded, "hom nay", "trong ngay", "today") {
		return "today"
	}
	if hasAnySubstr(folded, "30 ngay", "thang nay", "thang qua", "30d", "1 thang") {
		return "30d"
	}
	return "7d"
}

func isOrderLookupIntent(folded string) bool {
	if orderIDExtractRe.MatchString(folded) {
		return true
	}
	orderKeys := []string{
		"don hang cua toi", "don cua toi", "trang thai don",
		"tra don", "tra cuu don", "kiem tra don", "van don",
		"theo doi don", "don hang #", "order",
	}
	if !hasAnySubstr(folded, orderKeys...) {
		return false
	}
	policyCues := []string{"chinh sach", "quy dinh", "tu huy", "hoan voucher"}
	return !hasAnySubstr(folded, policyCues...)
}

func isAddToCartIntent(folded string) bool {
	return hasAnySubstr(
		folded,
		"them vao gio", "cho vao gio", "bo vao gio", "add to cart",
		"dong y them", "xac nhan them", "them san pham vao gio", "them sku", "dat vao gio",
	)
}

func isPolicyIntent(folded string) bool {
	policyKeys := []string{
		"doi tra", "hoan tien", "phi van chuyen", "phi ship", "van chuyen",
		"cod", "vietqr", "payos", "sepay", "thanh toan", "phi san", "hoa hong",
		"voucher", "xu thuong", "flash sale", "bao mat", "mat khau",
		"xoa tai khoan", "xuat du lieu", "chinh sach", "quy dinh", "het han",
		"bao hanh", "freeship", "dia chi", "34 tinh", "tinh thanh",
		"bom hang", "ghn", "ghtk", "escrow", "rut tien", "diem phat",
		"khoa tai khoan", "otp", "huy don", "sua dia chi", "tra hang",
		"phan xu", "thu tu ap dung", "hoan voucher", "jwt", "access token",
	}
	if !hasAnySubstr(folded, policyKeys...) {
		return false
	}
	// If the user explicitly asks to search for products with price/category shopping language,
	// prefer product search unless they explicitly mention policy/rule terms.
	explicitPolicy := hasAnySubstr(folded, "chinh sach", "quy dinh", "phi ", "bao lau", "toi da", "nhu the nao", "ra sao", "bao nhieu", "thu tu", "duoc khong", "co duoc")
	if explicitPolicy {
		return true
	}
	shoppingCues := hasAnySubstr(folded, "tim ao", "tim quan", "mua ao", "mua quan", "duoi 300k", "duoi 250k", "duoi 200k", "duoi 500k", "tu 200k")
	return !shoppingCues
}

func extractOrderID(text string) string {
	return orderIDExtractRe.FindString(text)
}

func extractSKUIDFromHistory(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if m := skuIDExtractRe.FindString(messages[i].Content); m != "" {
			return strings.ToLower(m)
		}
	}
	return ""
}

func extractQuantity(folded string) int {
	sub := qtyExplicitRe.FindStringSubmatch(folded)
	for i := 1; i < len(sub); i++ {
		if sub[i] != "" {
			if n, err := strconv.Atoi(sub[i]); err == nil && n > 0 {
				if n > 99 {
					return 99
				}
				return n
			}
		}
	}
	return 1
}

func buildSearchArgs(req ChatRequest, lastUser string) map[string]any {
	folded := foldVI(lastUser)
	minPrice, maxPrice, strippedPrice := extractPriceConstraints(folded)
	cleanQuery := cleanSearchQuery(strippedPrice)

	// For short follow-up turns (e.g. "còn size L không?"), enrich query from prior user turn.
	if isFollowUpQuery(cleanQuery) && len(req.Messages) >= 3 {
		for i := len(req.Messages) - 2; i >= 0; i-- {
			if req.Messages[i].Role == RoleUser {
				_, _, prevStripped := extractPriceConstraints(foldVI(req.Messages[i].Content))
				prevClean := cleanSearchQuery(prevStripped)
				if prevClean != "" && !isFollowUpQuery(prevClean) {
					if cleanQuery == "" {
						cleanQuery = prevClean
					} else {
						cleanQuery = prevClean + " " + cleanQuery
					}
					break
				}
			}
		}
	}

	args := map[string]any{
		"query": cleanQuery,
	}
	if req.ShopID != "" {
		args["shop_id"] = req.ShopID
	}
	if minPrice > 0 {
		args["min_price_vnd"] = minPrice
	}
	if maxPrice > 0 {
		args["max_price_vnd"] = maxPrice
	}
	return args
}

func extractPriceConstraints(folded string) (minVND, maxVND int64, cleaned string) {
	cleaned = folded

	if loc := priceRangeRe.FindStringSubmatchIndex(cleaned); loc != nil {
		sub := priceRangeRe.FindStringSubmatch(cleaned)
		u1, u2 := sub[2], sub[4]
		if u1 == "" {
			u1 = u2
		}
		minVND = parseVNPrice(sub[1], u1)
		maxVND = parseVNPrice(sub[3], u2)
		if minVND > maxVND && maxVND > 0 {
			minVND, maxVND = maxVND, minVND
		}
		cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
		return minVND, maxVND, strings.TrimSpace(cleaned)
	}

	if loc := priceMaxRe.FindStringSubmatchIndex(cleaned); loc != nil {
		sub := priceMaxRe.FindStringSubmatch(cleaned)
		maxVND = parseVNPrice(sub[1], sub[2])
		cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
	} else if loc := priceMaxTrailRe.FindStringSubmatchIndex(cleaned); loc != nil {
		sub := priceMaxTrailRe.FindStringSubmatch(cleaned)
		maxVND = parseVNPrice(sub[1], sub[2])
		cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
	}

	if loc := priceMinRe.FindStringSubmatchIndex(cleaned); loc != nil {
		sub := priceMinRe.FindStringSubmatch(cleaned)
		minVND = parseVNPrice(sub[1], sub[2])
		cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
	} else if loc := priceMinTrailRe.FindStringSubmatchIndex(cleaned); loc != nil {
		sub := priceMinTrailRe.FindStringSubmatch(cleaned)
		minVND = parseVNPrice(sub[1], sub[2])
		cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
	}

	if minVND == 0 && maxVND == 0 {
		if loc := priceExactRe.FindStringSubmatchIndex(cleaned); loc != nil {
			sub := priceExactRe.FindStringSubmatch(cleaned)
			val := parseVNPrice(sub[1], sub[2])
			if val > 0 {
				if strings.HasPrefix(sub[0], "tam") {
					minVND = val * 80 / 100
					maxVND = val * 120 / 100
				} else {
					maxVND = val
				}
				cleaned = cleaned[:loc[0]] + " " + cleaned[loc[1]:]
			}
		}
	}

	return minVND, maxVND, strings.TrimSpace(cleaned)
}

func parseVNPrice(numStr, unit string) int64 {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "trieu", "tr":
		s := strings.ReplaceAll(numStr, ",", ".")
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int64(math.Round(f * 1_000_000))
	case "k", "nghin", "ngan":
		s := strings.ReplaceAll(numStr, ",", ".")
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int64(math.Round(f * 1_000))
	default:
		// Strip thousands separators (e.g. "300.000" or "10,000").
		digits := strings.NewReplacer(".", "", ",", "").Replace(numStr)
		v, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return 0
		}
		// Bare small numbers with "duoi 300" usually mean 300k VND.
		if unit == "" && v > 0 && v < 1000 {
			return v * 1_000
		}
		return v
	}
}

func cleanSearchQuery(folded string) string {
	fillers := []string{
		"tim giup toi", "tim cho toi", "tim giup minh", "cho toi xem", "cho minh xem",
		"tu van giup minh", "tu van giup toi", "tu van cho minh", "tu van cho toi",
		"goi y giup minh", "goi y cho minh", "goi y cho toi", "gian hang co", "shop co",
		"ben minh co", "co ban", "khong ban", "khong shop", "khong a", "giup minh",
		"giup toi", "cho minh", "cho toi", "toi muon mua", "minh muon mua",
		"toi can tim", "minh can tim", "toi can", "minh can", "muon tim", "tim mua",
		"san pham nao", "co mau nao", "loai nao", "nao tot", "nao dep",
		"them vao gio hang", "them vao gio", "cho vao gio", "bo vao gio",
	}
	s := " " + folded + " "
	for _, f := range fillers {
		s = strings.ReplaceAll(s, " "+f+" ", " ")
	}
	var out []string
	stop := map[string]bool{
		"tim": true, "mua": true, "xem": true, "co": true, "khong": true,
		"nhe": true, "a": true, "voi": true, "la": true, "cua": true,
		"gia": true, "nao": true, "gi": true, "duoc": true, "hang": true,
		"shop": true, "minh": true, "toi": true, "ban": true, "em": true, "anh": true, "chi": true,
	}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	}) {
		if stop[tok] {
			continue
		}
		out = append(out, tok)
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

func isFollowUpQuery(q string) bool {
	if q == "" {
		return true
	}
	toks := strings.Fields(q)
	if len(toks) > 3 {
		return false
	}
	followCues := []string{"size", "mau", "con", "re", "hon", "khac", "so", "sanh", "cung", "them"}
	for _, t := range toks {
		matched := false
		for _, c := range followCues {
			if t == c {
				matched = true
				break
			}
		}
		if !matched && len(t) > 2 {
			return false
		}
	}
	return true
}

func (m *MockProvider) renderToolResponse(req ChatRequest, toolMsg Message) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(toolMsg.Content), &raw); err != nil {
		return "Xin lỗi, tôi không thể đọc dữ liệu trả về từ hệ thống lúc này. Bạn vui lòng thử lại sau."
	}

	toolName := toolMsg.ToolName
	if toolName == "" {
		toolName = strVal(raw["name"])
	}

	okVal := true
	if v, exists := raw["ok"]; exists {
		okVal = boolVal(v)
	}
	requiresAuth := boolVal(raw["requires_auth"])
	requiresConfirm := boolVal(raw["requires_confirmation"])
	errMsg := strVal(raw["error"])

	data := raw
	if d, ok := raw["data"].(map[string]any); ok {
		data = d
	}

	switch toolName {
	case "search_products":
		return renderSearchProducts(okVal, errMsg, data)
	case "search_policies":
		return renderSearchPolicies(okVal, errMsg, data)
	case "get_order_status":
		return renderOrderStatus(okVal, requiresAuth, errMsg, data)
	case "add_to_cart":
		return renderAddToCart(okVal, requiresAuth, requiresConfirm, errMsg, data)
	case "get_seller_report":
		return renderSellerReport(okVal, requiresAuth, errMsg, data)
	case "handoff_to_agent":
		return renderHandoff(okVal, requiresAuth, errMsg, data)
	default:
		if !okVal && errMsg != "" {
			return fmt.Sprintf("Không thể thực hiện yêu cầu: %s.", errMsg)
		}
		return "Tôi đã hoàn tất kiểm tra thông tin theo yêu cầu của bạn."
	}
}

func renderSearchProducts(okVal bool, errMsg string, data map[string]any) string {
	if !okVal && errMsg != "" {
		return fmt.Sprintf("Hiện chưa thể tìm kiếm sản phẩm (%s). Bạn vui lòng thử lại sau.", errMsg)
	}

	var products []struct {
		ID          string `json:"id"`
		ShopID      string `json:"shop_id"`
		ShopName    string `json:"shop_name"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
		MinPriceVND int64  `json:"min_price_vnd"`
		MaxPriceVND int64  `json:"max_price_vnd"`
		InStock     bool   `json:"in_stock"`
		SKUs        []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			PriceVND       int64  `json:"price_vnd"`
			AvailableStock int32  `json:"available_stock"`
		} `json:"skus"`
	}
	_ = decodeInto(data["products"], &products)

	if len(products) == 0 {
		return "Hiện tại gian hàng chưa có sản phẩm phù hợp với yêu cầu của bạn. Bạn có thể thử từ khóa hoặc khoảng giá khác nhé."
	}

	limit := len(products)
	if limit > 5 {
		limit = 5
	}

	var b strings.Builder
	b.WriteString("Dưới đây là các sản phẩm phù hợp hiện có tại gian hàng:\n")
	for i := 0; i < limit; i++ {
		p := products[i]
		priceStr := formatVND(p.MinPriceVND)
		if p.MaxPriceVND > p.MinPriceVND {
			priceStr = fmt.Sprintf("%s – %s", formatVND(p.MinPriceVND), formatVND(p.MaxPriceVND))
		}
		stockStr := "Còn hàng"
		if !p.InStock {
			stockStr = "Tạm hết hàng"
		}
		fmt.Fprintf(&b, "- **%s** (Mã: `%s`) — Giá: %s (%s)", p.Name, p.ID, priceStr, stockStr)

		if len(p.SKUs) > 0 {
			var skuParts []string
			for j, sku := range p.SKUs {
				if j >= 3 {
					break
				}
				label := sku.Name
				if label == "" {
					label = sku.ID
				}
				skuParts = append(skuParts, fmt.Sprintf("%s [%s, tồn: %d]", label, sku.ID, sku.AvailableStock))
			}
			if len(skuParts) > 0 {
				fmt.Fprintf(&b, " | Phân loại: %s", strings.Join(skuParts, ", "))
			}
		}

		if safeDesc := sanitizeDescriptionForEcho(p.Description); safeDesc != "" {
			fmt.Fprintf(&b, " — %s", safeDesc)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Nếu bạn muốn thêm sản phẩm vào giỏ hàng, hãy cho tôi biết mã SKU và số lượng mong muốn nhé.")
	return strings.TrimSpace(b.String())
}

func sanitizeDescriptionForEcho(desc string) string {
	s := strings.TrimSpace(desc)
	if s == "" {
		return ""
	}
	// Strip placeholder markers left by indirect-injection sanitization.
	s = strings.ReplaceAll(s, "[nội dung không hợp lệ đã bị lọc]", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	folded := foldVI(s)
	if hasAnySubstr(folded, "http://", "https://", "evil", "phishing", "ignore", "system:", "jwt", "api key") {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 120 {
		s = string(runes[:120]) + "..."
	}
	return s
}

func renderSearchPolicies(okVal bool, errMsg string, data map[string]any) string {
	if !okVal && errMsg != "" {
		return fmt.Sprintf("Hiện chưa thể tra cứu chính sách (%s). Bạn vui lòng liên hệ nhân viên hỗ trợ.", errMsg)
	}

	var chunks []struct {
		DocTitle string `json:"doc_title"`
		Section  string `json:"section"`
		Citation string `json:"citation"`
		Content  string `json:"content"`
	}
	_ = decodeInto(data["chunks"], &chunks)

	if len(chunks) == 0 {
		return "Hiện tôi chưa tìm thấy điều khoản chính sách phù hợp cho câu hỏi này. Bạn vui lòng liên hệ nhân viên hỗ trợ NexusMart để được tư vấn thêm."
	}

	limit := len(chunks)
	if limit > 3 {
		limit = 3
	}

	var b strings.Builder
	b.WriteString("Theo chính sách hiện hành của NexusMart:\n")
	var citations []string
	seenCite := make(map[string]struct{})
	for i := 0; i < limit; i++ {
		c := chunks[i]
		content := strings.TrimSpace(c.Content)
		runes := []rune(content)
		if len(runes) > 420 {
			content = string(runes[:420]) + "..."
		}
		section := strings.TrimSpace(c.Section)
		if section == "" {
			section = strings.TrimSpace(c.DocTitle)
		}
		if section == "" {
			section = "Chính sách NexusMart"
		}
		if c.Citation != "" {
			citeLink := fmt.Sprintf("[%s](%s)", section, c.Citation)
			fmt.Fprintf(&b, "- **%s**: %s (%s)\n", section, content, citeLink)
			if _, ok := seenCite[citeLink]; !ok {
				seenCite[citeLink] = struct{}{}
				citations = append(citations, citeLink)
			}
		} else {
			fmt.Fprintf(&b, "- **%s**: %s\n", section, content)
		}
	}
	if len(citations) > 0 {
		fmt.Fprintf(&b, "Nguồn tham khảo: %s.", strings.Join(citations, ", "))
	}
	return strings.TrimSpace(b.String())
}

func renderOrderStatus(okVal, requiresAuth bool, errMsg string, data map[string]any) string {
	if requiresAuth {
		return "Bạn cần đăng nhập để tra cứu đơn hàng của mình. Sau khi đăng nhập, hãy nhắn lại để tôi kiểm tra giúp bạn nhé."
	}
	if !okVal || errMsg != "" {
		return "Không tìm thấy đơn hàng hoặc bạn không có quyền truy cập đơn hàng này. Vui lòng kiểm tra lại mã đơn hàng của bạn."
	}

	var orders []map[string]any
	if single, ok := data["order"].(map[string]any); ok && len(single) > 0 {
		orders = append(orders, single)
	}
	if list, ok := data["orders"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				orders = append(orders, m)
			}
		}
	}
	if len(orders) == 0 {
		return "Hiện tại bạn chưa có đơn hàng nào khớp với yêu cầu tra cứu."
	}

	var b strings.Builder
	b.WriteString("Thông tin trạng thái đơn hàng của bạn:\n")
	for i, o := range orders {
		if i >= 5 {
			break
		}
		id := firstNonEmpty(strVal(o["order_number"]), strVal(o["order_id"]), strVal(o["id"]))
		status := firstNonEmpty(strVal(o["status"]), "unknown")
		total := firstPositiveInt64(int64Val(o["total_vnd"]), int64Val(o["total_amount_vnd"]), int64Val(o["grand_total_vnd"]))
		tracking := strVal(o["tracking_code"])

		fmt.Fprintf(&b, "- Đơn hàng #%s: trạng thái **%s**", id, status)
		if total > 0 {
			fmt.Fprintf(&b, ", tổng tiền %s", formatVND(total))
		}
		if tracking != "" {
			fmt.Fprintf(&b, ", mã vận đơn %s", tracking)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func renderAddToCart(okVal, requiresAuth, requiresConfirm bool, errMsg string, data map[string]any) string {
	if requiresAuth {
		return "Bạn cần đăng nhập để thêm sản phẩm vào giỏ hàng."
	}
	skuID := strVal(data["sku_id"])
	qty := int64Val(data["quantity"])
	if qty <= 0 {
		qty = 1
	}
	prodName := strVal(data["product_name"])
	price := int64Val(data["price_vnd"])

	if requiresConfirm {
		var b strings.Builder
		b.WriteString("Vui lòng xác nhận thêm sản phẩm vào giỏ hàng:\n")
		if prodName != "" {
			fmt.Fprintf(&b, "- Sản phẩm: **%s**\n", prodName)
		}
		if skuID != "" {
			fmt.Fprintf(&b, "- Mã SKU: `%s`\n", skuID)
		}
		fmt.Fprintf(&b, "- Số lượng: %d\n", qty)
		if price > 0 {
			fmt.Fprintf(&b, "- Đơn giá: %s\n", formatVND(price))
		}
		b.WriteString("Bạn có đồng ý thêm vào giỏ hàng không? (Lưu ý: trợ lý AI không tự động thanh toán hộ.)")
		return b.String()
	}

	if !okVal || errMsg != "" {
		if errMsg == "" {
			errMsg = "sản phẩm không khả dụng hoặc hết tồn kho"
		}
		return fmt.Sprintf("Không thể thêm sản phẩm vào giỏ hàng: %s.", errMsg)
	}

	label := skuID
	if prodName != "" && skuID != "" {
		label = fmt.Sprintf("%s (`%s`)", prodName, skuID)
	} else if prodName != "" {
		label = prodName
	}
	return fmt.Sprintf("Đã thêm vào giỏ hàng thành công: %s (số lượng: %d). Bạn có thể kiểm tra tại [Giỏ hàng](/cart). Lưu ý: trợ lý AI không tự động thanh toán, bạn vui lòng tự xác nhận đặt hàng trên hệ thống.", label, qty)
}

func renderSellerReport(okVal, requiresAuth bool, errMsg string, data map[string]any) string {
	if requiresAuth {
		return "Bạn cần đăng nhập bằng tài khoản người bán để xem báo cáo kinh doanh."
	}
	if !okVal || errMsg != "" {
		if errMsg == "" {
			errMsg = "chỉ người bán đã xác thực gian hàng mới có quyền xem báo cáo kinh doanh"
		}
		return fmt.Sprintf("Không thể truy xuất báo cáo kinh doanh: %s.", errMsg)
	}

	period := firstNonEmpty(strVal(data["period"]), "7d")
	totalOrders := int64Val(data["total_orders"])
	completed := int64Val(data["completed_orders"])
	cancelled := int64Val(data["cancelled_orders"])
	cancelRate := floatVal(data["cancel_rate_pct"])
	revenue := int64Val(data["gross_revenue_vnd"])
	availBal := int64Val(data["available_balance_vnd"])
	pendBal := int64Val(data["pending_balance_vnd"])

	var b strings.Builder
	fmt.Fprintf(&b, "Báo cáo kinh doanh gian hàng (kỳ %s):\n", period)
	fmt.Fprintf(&b, "- Tổng số đơn hàng: %d (hoàn tất: %d, đã hủy: %d, tỷ lệ hủy: %.1f%%)\n", totalOrders, completed, cancelled, cancelRate)
	fmt.Fprintf(&b, "- Doanh thu gộp: %s\n", formatVND(revenue))
	fmt.Fprintf(&b, "- Số dư khả dụng: %s | Số dư chờ đối soát: %s\n", formatVND(availBal), formatVND(pendBal))

	var top []struct {
		ProductID   string `json:"product_id"`
		ProductName string `json:"product_name"`
		UnitsSold   int64  `json:"units_sold"`
		RevenueVND  int64  `json:"revenue_vnd"`
	}
	_ = decodeInto(data["top_products"], &top)
	if len(top) > 0 {
		b.WriteString("- Sản phẩm bán chạy:\n")
		for i, item := range top {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  %d. %s (%d sản phẩm — %s)\n", i+1, item.ProductName, item.UnitsSold, formatVND(item.RevenueVND))
		}
	}
	return strings.TrimSpace(b.String())
}

func renderHandoff(okVal, requiresAuth bool, errMsg string, data map[string]any) string {
	if requiresAuth {
		return "Bạn cần đăng nhập để kết nối và chuyển tiếp hội thoại tới nhân viên hỗ trợ."
	}
	if !okVal || errMsg != "" {
		if errMsg == "" {
			errMsg = "hệ thống hỗ trợ đang bận"
		}
		return fmt.Sprintf("Hiện chưa thể chuyển tiếp yêu cầu tới nhân viên hỗ trợ (%s). Bạn vui lòng thử lại sau ít phút.", errMsg)
	}

	convID := firstNonEmpty(strVal(data["conversation_id"]), strVal(data["chat_conversation_id"]), strVal(data["ticket_id"]))
	est := firstNonEmpty(strVal(data["estimated_response"]), "trong khung giờ làm việc 08:00 – 22:00")
	status := firstNonEmpty(strVal(data["status"]), "handed_off")

	if convID != "" {
		return fmt.Sprintf("Tôi đã chuyển tiếp yêu cầu của bạn tới nhân viên hỗ trợ (mã hội thoại: `%s`, trạng thái: %s). Thời gian phản hồi dự kiến: %s.", convID, status, est)
	}
	return fmt.Sprintf("Tôi đã chuyển tiếp yêu cầu của bạn tới nhân viên hỗ trợ (trạng thái: %s). Thời gian phản hồi dự kiến: %s.", status, est)
}

func completeReviewSummary(req CompleteRequest) string {
	var reviews []struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if req.Metadata != nil {
		_ = decodeInto(req.Metadata["reviews"], &reviews)
	}

	var (
		totalRating int
		pros        []string
		cons        []string
		seenPro     = make(map[string]struct{})
		seenCon     = make(map[string]struct{})
	)
	for _, r := range reviews {
		totalRating += r.Rating
		comment := strings.TrimSpace(strings.ReplaceAll(r.Comment, "[nội dung không hợp lệ đã bị lọc]", ""))
		if comment == "" {
			continue
		}
		snippet := truncateRunes(comment, 100)
		key := foldVI(snippet)
		if r.Rating >= 4 {
			if _, ok := seenPro[key]; !ok && len(pros) < 4 {
				seenPro[key] = struct{}{}
				pros = append(pros, snippet)
			}
		} else if r.Rating > 0 && r.Rating <= 3 {
			if _, ok := seenCon[key]; !ok && len(cons) < 4 {
				seenCon[key] = struct{}{}
				cons = append(cons, snippet)
			}
		}
	}

	summary := "Chưa có đủ đánh giá chi tiết từ người mua."
	if len(reviews) > 0 {
		avg := float64(totalRating) / float64(len(reviews))
		summary = fmt.Sprintf("Tổng hợp từ %d đánh giá thực tế với điểm trung bình %.1f/5.", len(reviews), avg)
		if len(pros) > 0 {
			summary += " Người mua đánh giá tích cực về chất lượng và trải nghiệm thực tế."
		}
		if len(cons) > 0 {
			summary += " Một số ý kiến góp ý cần lưu ý thêm về kích thước hoặc giao nhận."
		}
	}

	if pros == nil {
		pros = []string{}
	}
	if cons == nil {
		cons = []string{}
	}

	out, _ := json.Marshal(map[string]any{
		"summary": summary,
		"pros":    pros,
		"cons":    cons,
	})
	return string(out)
}

func completeDescribeProduct(req CompleteRequest) string {
	name := strings.TrimSpace(metaString(req.Metadata, "name"))
	category := strings.TrimSpace(metaString(req.Metadata, "category"))
	tone := strings.TrimSpace(metaString(req.Metadata, "tone"))
	price := metaInt64(req.Metadata, "price_vnd")

	var attrs map[string]string
	if req.Metadata != nil {
		_ = decodeInto(req.Metadata["attributes"], &attrs)
	}
	if name == "" {
		name = "Sản phẩm NexusMart"
	}

	title := name
	if category != "" && !strings.Contains(foldVI(name), foldVI(category)) {
		title = fmt.Sprintf("%s - %s chính hãng", name, category)
	}

	var highlights []string
	if category != "" {
		highlights = append(highlights, "Danh mục: "+category)
	}
	for k, v := range attrs {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			highlights = append(highlights, fmt.Sprintf("%s: %s", k, v))
		}
	}
	if price > 0 {
		highlights = append(highlights, "Giá tham khảo: "+formatVND(price))
	}
	if len(highlights) == 0 {
		highlights = append(highlights, "Thông tin sản phẩm do người bán cung cấp trên NexusMart")
	}

	var desc strings.Builder
	fmt.Fprintf(&desc, "%s là lựa chọn phù hợp cho nhu cầu sử dụng hằng ngày", name)
	if category != "" {
		fmt.Fprintf(&desc, " thuộc nhóm %s", category)
	}
	desc.WriteString(". ")
	if len(attrs) > 0 {
		var parts []string
		for k, v := range attrs {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				parts = append(parts, fmt.Sprintf("%s %s", k, v))
			}
		}
		if len(parts) > 0 {
			fmt.Fprintf(&desc, "Thông số nổi bật gồm %s. ", strings.Join(parts, ", "))
		}
	}
	if price > 0 {
		fmt.Fprintf(&desc, "Giá niêm yết %s. ", formatVND(price))
	}
	if strings.EqualFold(tone, "ngan_gon") {
		desc.WriteString("Hỗ trợ đổi trả theo chính sách NexusMart.")
	} else {
		desc.WriteString("Sản phẩm được kiểm tra kỹ trước khi đóng gói và áp dụng chính sách đổi trả minh bạch trên NexusMart.")
	}

	out, _ := json.Marshal(map[string]any{
		"title_suggestion": title,
		"description":      strings.TrimSpace(desc.String()),
		"highlights":       highlights,
	})
	return string(out)
}

func completeSuggestReply(req CompleteRequest) string {
	custMsg := strings.TrimSpace(metaString(req.Metadata, "customer_message"))
	prodName := strings.TrimSpace(metaString(req.Metadata, "product_name"))
	prodPrice := metaInt64(req.Metadata, "product_price_vnd")
	policySummary := strings.TrimSpace(metaString(req.Metadata, "policy_summary"))
	injectionDetected := metaBool(req.Metadata, "injection_detected")

	cleanMsg := strings.TrimSpace(strings.ReplaceAll(custMsg, "[nội dung không hợp lệ đã bị lọc]", ""))
	needsMoreInfo := injectionDetected || cleanMsg == "" || (prodName == "" && policySummary == "")

	var s1, s2, s3 string
	if injectionDetected {
		s1 = "Chào bạn, cảm ơn bạn đã nhắn tin cho shop. Bạn vui lòng cho shop biết rõ sản phẩm, phân loại hoặc mã đơn hàng cần hỗ trợ để shop kiểm tra theo đúng chính sách NexusMart nhé!"
		s2 = "Chào bạn, shop cần thêm thông tin sản phẩm hoặc mã đơn hàng để hỗ trợ đúng quy định của NexusMart ạ."
		s3 = "Chào bạn, mọi yêu cầu về giá bán, đổi trả hay hoàn tiền đều được shop thực hiện trực tiếp trên hệ thống NexusMart theo chính sách sàn. Bạn cho shop xin tên sản phẩm hoặc mã đơn hàng để kiểm tra chi tiết nhé."
	} else if prodName != "" {
		priceNote := ""
		if prodPrice > 0 {
			priceNote = fmt.Sprintf(" với giá niêm yết %s", formatVND(prodPrice))
		}
		s1 = fmt.Sprintf("Chào bạn, cảm ơn bạn đã quan tâm đến %s%s! Bạn cho shop biết chiều cao/cân nặng hoặc màu sắc mong muốn để shop tư vấn phân loại phù hợp nhất nhé.", prodName, priceNote)
		s2 = fmt.Sprintf("Chào bạn, %s hiện có giá %s trên gian hàng. Bạn cần shop kiểm tra size hoặc màu nào ạ?", prodName, firstNonEmpty(formatVNDIfPositive(prodPrice), "theo niêm yết"))
		if policySummary != "" {
			s3 = fmt.Sprintf("Chào bạn, về %s%s: %s. Nếu bạn cần thêm thông tin chi tiết về phân loại còn hàng, cứ nhắn cho shop nhé!", prodName, priceNote, truncateRunes(policySummary, 160))
		} else {
			s3 = fmt.Sprintf("Chào bạn, sản phẩm %s%s được đóng gói cẩn thận và áp dụng chính sách đổi trả 7 ngày theo quy định NexusMart. Bạn cho shop biết thêm nhu cầu cụ thể để tư vấn chính xác nhé.", prodName, priceNote)
		}
	} else if policySummary != "" {
		needsMoreInfo = false
		snippet := truncateRunes(policySummary, 180)
		s1 = fmt.Sprintf("Chào bạn, về thắc mắc của bạn, theo chính sách NexusMart: %s. Bạn cần shop hỗ trợ thêm thông tin gì cứ nhắn nhé!", snippet)
		s2 = fmt.Sprintf("Chào bạn, shop xin thông tin ngắn gọn: %s.", snippet)
		s3 = fmt.Sprintf("Chào bạn, cảm ơn bạn đã liên hệ. Theo quy định hiện hành: %s. Bạn có thể gửi mã đơn hàng nếu cần shop kiểm tra cụ thể hơn ạ.", snippet)
	} else {
		s1 = "Chào bạn, cảm ơn bạn đã nhắn tin cho shop! Bạn vui lòng cung cấp thêm tên sản phẩm, size/màu hoặc mã đơn hàng để shop hỗ trợ nhanh nhất nhé."
		s2 = "Chào bạn, bạn cho shop xin thêm thông tin sản phẩm hoặc mã đơn hàng để kiểm tra giúp bạn ạ."
		s3 = "Chào bạn, để tư vấn chính xác nhất về tình trạng hàng hóa và giao nhận, bạn vui lòng chia sẻ thêm chi tiết sản phẩm hoặc mã đơn hàng trên NexusMart giúp shop nhé."
	}

	out, _ := json.Marshal(map[string]any{
		"suggestions": []map[string]string{
			{"id": "s1", "text": s1, "tone": "than_thien"},
			{"id": "s2", "text": s2, "tone": "ngan_gon"},
			{"id": "s3", "text": s3, "tone": "chi_tiet"},
		},
		"needs_more_info": needsMoreInfo,
	})
	return string(out)
}

func completeHandoffSummary(req CompleteRequest) string {
	reason := strings.TrimSpace(metaString(req.Metadata, "reason"))
	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if req.Metadata != nil {
		_ = decodeInto(req.Metadata["messages"], &msgs)
	}

	var parts []string
	if reason != "" {
		parts = append(parts, "Lý do yêu cầu hỗ trợ: "+truncateRunes(reason, 180))
	}
	var userNotes []string
	for _, msg := range msgs {
		if msg.Role == RoleUser && strings.TrimSpace(msg.Content) != "" {
			userNotes = append(userNotes, truncateRunes(strings.TrimSpace(msg.Content), 100))
		}
	}
	if len(userNotes) > 3 {
		userNotes = userNotes[len(userNotes)-3:]
	}
	if len(userNotes) > 0 {
		parts = append(parts, "Nội dung trao đổi gần nhất của khách hàng: "+strings.Join(userNotes, " | "))
	}
	if len(parts) == 0 {
		if p := strings.TrimSpace(req.Prompt); p != "" {
			return "Tóm tắt yêu cầu hỗ trợ: " + truncateRunes(p, 240)
		}
		return "Khách hàng yêu cầu kết nối nhân viên hỗ trợ trực tiếp."
	}
	return strings.Join(parts, ". ")
}

func streamTextChunks(ctx context.Context, text string, onEvent func(StreamEvent) error) error {
	if onEvent == nil || text == "" {
		return nil
	}
	words := strings.SplitAfter(text, " ")
	const chunkWords = 4
	for i := 0; i < len(words); i += chunkWords {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := i + chunkWords
		if end > len(words) {
			end = len(words)
		}
		chunk := strings.Join(words[i:end], "")
		if chunk == "" {
			continue
		}
		if err := onEvent(StreamEvent{Type: EventToken, Text: chunk}); err != nil {
			return err
		}
	}
	return nil
}

func formatVND(amount int64) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	s := strconv.FormatInt(amount, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte('.')
		}
	}
	for i := rem; i < len(s); i += 3 {
		if i > rem {
			b.WriteByte('.')
		}
		b.WriteString(s[i : i+3])
	}
	b.WriteString("đ")
	return b.String()
}

func formatVNDIfPositive(v int64) string {
	if v <= 0 {
		return ""
	}
	return formatVND(v)
}

func foldVI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case 'á', 'à', 'ả', 'ã', 'ạ', 'ă', 'ắ', 'ằ', 'ẳ', 'ẵ', 'ặ', 'â', 'ấ', 'ầ', 'ẩ', 'ẫ', 'ậ':
			r = 'a'
		case 'é', 'è', 'ẻ', 'ẽ', 'ẹ', 'ê', 'ế', 'ề', 'ể', 'ễ', 'ệ':
			r = 'e'
		case 'í', 'ì', 'ỉ', 'ĩ', 'ị':
			r = 'i'
		case 'ó', 'ò', 'ỏ', 'õ', 'ọ', 'ô', 'ố', 'ồ', 'ổ', 'ỗ', 'ộ', 'ơ', 'ớ', 'ờ', 'ở', 'ỡ', 'ợ':
			r = 'o'
		case 'ú', 'ù', 'ủ', 'ũ', 'ụ', 'ư', 'ứ', 'ừ', 'ử', 'ữ', 'ự':
			r = 'u'
		case 'ý', 'ỳ', 'ỷ', 'ỹ', 'ỵ':
			r = 'y'
		case 'đ':
			r = 'd'
		}
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func hasAnySubstr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func decodeInto(src any, dst any) error {
	if src == nil {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolVal(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func int64Val(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return 0
}

func floatVal(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	return strVal(meta[key])
}

func metaInt64(meta map[string]any, key string) int64 {
	if meta == nil {
		return 0
	}
	return int64Val(meta[key])
}

func metaBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	return boolVal(meta[key])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstPositiveInt64(vals ...int64) int64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
