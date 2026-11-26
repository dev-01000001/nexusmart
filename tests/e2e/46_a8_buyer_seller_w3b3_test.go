package e2e

import (
	"bytes"
	"compress/zlib"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// --- W3B3 A8 (G1-G8) DTOs & Helpers ---

type w3b46ShopAllocationDTO struct {
	OrderID             string `json:"order_id"`
	ShopID              string `json:"shop_id"`
	SubtotalVND         int64  `json:"subtotal_vnd"`
	CommissionVND       int64  `json:"commission_vnd"`
	ShippingFeeVND      int64  `json:"shipping_fee_vnd"`
	PlatformDiscountVND int64  `json:"platform_discount_vnd"`
}

type w3b46PaymentDetailDTO struct {
	PaymentID       string                   `json:"payment_id"`
	ID              string                   `json:"id"`
	OrderGroupID    string                   `json:"order_group_id"`
	OrderID         string                   `json:"order_id"`
	AmountVND       int64                    `json:"amount_vnd"`
	Method          string                   `json:"method"`
	Status          string                   `json:"status"`
	ProviderRef     string                   `json:"provider_ref"`
	PayURL          string                   `json:"pay_url"`
	ShopAllocations []w3b46ShopAllocationDTO `json:"shop_allocations"`
}

type w3b46PaymentDetailResponse struct {
	Payment w3b46PaymentDetailDTO `json:"payment"`
}

type w3b46ShipmentDTO struct {
	ShipmentID   string `json:"shipment_id"`
	OrderID      string `json:"order_id"`
	ShopID       string `json:"shop_id"`
	CarrierCode  string `json:"carrier_code"`
	ServiceCode  string `json:"service_code"`
	TrackingCode string `json:"tracking_code"`
	LabelURL     string `json:"label_url"`
	Status       string `json:"status"`
	FeeVND       int64  `json:"fee_vnd"`
	CODAmountVND int64  `json:"cod_amount_vnd"`
}

type w3b46ShipmentResponse struct {
	Shipment w3b46ShipmentDTO `json:"shipment"`
}

type w3b46OrderAddressSnapshotDTO struct {
	RecipientName  string `json:"recipient_name"`
	RecipientPhone string `json:"recipient_phone"`
	ProvinceCode   string `json:"province_code"`
	ProvinceName   string `json:"province_name"`
	WardCode       string `json:"ward_code"`
	WardName       string `json:"ward_name"`
	StreetAddress  string `json:"street_address"`
}

type w3b46OrderWithAddressDTO struct {
	OrderDTO
	ShippingAddress *w3b46OrderAddressSnapshotDTO `json:"shipping_address"`
}

type w3b46OrderWithAddressResponse struct {
	Order w3b46OrderWithAddressDTO `json:"order"`
}

type w3b46UpdateAddressRequest struct {
	AddressID                 string `json:"address_id"`
	PreviewOnly               bool   `json:"preview_only,omitempty"`
	AcceptFeeIncrease         bool   `json:"accept_fee_increase,omitempty"`
	ExpectedNewShippingFeeVND int64  `json:"expected_new_shipping_fee_vnd,omitempty"`
	IdempotencyKey            string `json:"idempotency_key,omitempty"`
}

type w3b46UpdateAddressResponse struct {
	Order                   w3b46OrderWithAddressDTO      `json:"order"`
	OldShippingFeeVND       int64                         `json:"old_shipping_fee_vnd"`
	NewShippingFeeVND       int64                         `json:"new_shipping_fee_vnd"`
	FeeDeltaVND             int64                         `json:"fee_delta_vnd"`
	OldTotalPayableVND      int64                         `json:"old_total_payable_vnd"`
	NewTotalPayableVND      int64                         `json:"new_total_payable_vnd"`
	PreviewOnly             bool                          `json:"preview_only"`
	FeeAdjusted             bool                          `json:"fee_adjusted"`
	RequiresFeeConfirmation bool                          `json:"requires_fee_confirmation"`
	BlockedPrepaidIncrease  bool                          `json:"blocked_prepaid_increase"`
	ShipmentRecreated       bool                          `json:"shipment_recreated"`
	ShipmentQueued          bool                          `json:"shipment_queued"`
	ShipmentJobID           string                        `json:"shipment_job_id"`
	ResolvedAddress         *w3b46OrderAddressSnapshotDTO `json:"resolved_address"`
}

type w3b46FeeChangedEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
	Quote *struct {
		OldShippingFeeVND  int64 `json:"old_shipping_fee_vnd"`
		NewShippingFeeVND  int64 `json:"new_shipping_fee_vnd"`
		FeeDeltaVND        int64 `json:"fee_delta_vnd"`
		NewTotalPayableVND int64 `json:"new_total_payable_vnd"`
	} `json:"quote"`
}

type w3b46BatchShipResultDTO struct {
	OrderID       string `json:"order_id"`
	OrderCode     string `json:"order_code"`
	Outcome       string `json:"outcome"`
	ReasonCode    string `json:"reason_code"`
	Message       string `json:"message"`
	ShipmentJobID string `json:"shipment_job_id"`
	TrackingCode  string `json:"tracking_code"`
}

type w3b46BatchShipResponse struct {
	Results      []w3b46BatchShipResultDTO `json:"results"`
	QueuedCount  int32                     `json:"queued_count"`
	SkippedCount int32                     `json:"skipped_count"`
}

type w3b46PickingListItemDTO struct {
	SKUID         string   `json:"sku_id"`
	ProductID     string   `json:"product_id"`
	ProductName   string   `json:"product_name"`
	SKUName       string   `json:"sku_name"`
	TotalQuantity int32    `json:"total_quantity"`
	OrderCount    int32    `json:"order_count"`
	OrderCodes    []string `json:"order_codes"`
}

type w3b46PickingListResponse struct {
	Items      []w3b46PickingListItemDTO `json:"items"`
	OrderCount int32                     `json:"order_count"`
	TotalUnits int32                     `json:"total_units"`
}

type w3b46ImportJobDTO struct {
	ID           string `json:"id"`
	ShopID       string `json:"shop_id"`
	CreatedBy    string `json:"created_by"`
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	TotalRows    int32  `json:"total_rows"`
	ValidRows    int32  `json:"valid_rows"`
	InvalidRows  int32  `json:"invalid_rows"`
	CreatedRows  int32  `json:"created_rows"`
	FailedRows   int32  `json:"failed_rows"`
	ErrorMessage string `json:"error_message"`
}

type w3b46ImportJobResponse struct {
	Job              w3b46ImportJobDTO `json:"job"`
	AlreadyConfirmed bool              `json:"already_confirmed"`
}

type w3b46StockHistoryItemDTO struct {
	ID             string `json:"id"`
	SKUID          string `json:"sku_id"`
	ProductID      string `json:"product_id"`
	ShopID         string `json:"shop_id"`
	OrderID        string `json:"order_id"`
	ChangeType     string `json:"change_type"`
	Delta          int32  `json:"delta"`
	StockBefore    int32  `json:"stock_before"`
	StockAfter     int32  `json:"stock_after"`
	ReservedBefore int32  `json:"reserved_before"`
	ReservedAfter  int32  `json:"reserved_after"`
	ActorID        string `json:"actor_id"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"created_at"`
}

type w3b46StockHistoryResponse struct {
	Items    []w3b46StockHistoryItemDTO `json:"items"`
	Page     int32                      `json:"page"`
	PageSize int32                      `json:"page_size"`
	Total    int64                      `json:"total"`
}

type w3b46SellerOrdersResponse struct {
	Items        []OrderDTO `json:"items"`
	Page         int32      `json:"page"`
	PageSize     int32      `json:"page_size"`
	Total        int64      `json:"total"`
	StatusCounts []struct {
		Status string `json:"status"`
		Count  int64  `json:"count"`
	} `json:"status_counts"`
}

type w3b46PublicOrderSummaryDTO struct {
	OrderCode            string `json:"order_code"`
	Status               string `json:"status"`
	PaymentMethod        string `json:"payment_method"`
	PaymentStatus        string `json:"payment_status"`
	SubtotalVND          int64  `json:"subtotal_vnd"`
	ShippingFeeVND       int64  `json:"shipping_fee_vnd"`
	TotalPayableVND      int64  `json:"total_payable_vnd"`
	CarrierCode          string `json:"carrier_code"`
	TrackingCode         string `json:"tracking_code"`
	ProvinceName         string `json:"province_name"`
	WardName             string `json:"ward_name"`
	RecipientNameMasked  string `json:"recipient_name_masked"`
	RecipientPhoneMasked string `json:"recipient_phone_masked"`
	Items                []struct {
		ProductName  string `json:"product_name"`
		SKUName      string `json:"sku_name"`
		Quantity     int32  `json:"quantity"`
		LineTotalVND int64  `json:"line_total_vnd"`
	} `json:"items"`
	Timeline []struct {
		ToStatus  string `json:"to_status"`
		CreatedAt string `json:"created_at"`
	} `json:"timeline"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	DeliveredAt string `json:"delivered_at"`
}

type w3b46LookupOrderResponse struct {
	Order w3b46PublicOrderSummaryDTO `json:"order"`
}

func w3b46DialOrderGRPC(t *testing.T, env *testEnv) orderv1.OrderServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.OrderGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial OrderService gRPC %s: %v", env.cfg.OrderGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return orderv1.NewOrderServiceClient(conn)
}

func w3b46DialProductGRPC(t *testing.T, env *testEnv) productv1.ProductServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.ProductGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial ProductService gRPC %s: %v", env.cfg.ProductGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return productv1.NewProductServiceClient(conn)
}

func w3b46QuoteForRoute(
	t *testing.T,
	env *testEnv,
	buyerToken, shopID, fromProv, fromWard, toProv, toWard string,
	weightGrams int32,
	codVND int64,
	wantCarrier, wantService string,
) CarrierQuoteDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyerToken, nil, CalculateFeeRequest{
		ShopID:           shopID,
		FromProvinceCode: fromProv,
		FromWardCode:     fromWard,
		ToProvinceCode:   toProv,
		ToWardCode:       toWard,
		WeightGrams:      weightGrams,
		CODAmountVND:     codVND,
	}, http.StatusOK)
	var out CalculateFeeResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode calculate-fee: %v", err)
	}
	for _, q := range out.Quotes {
		if strings.EqualFold(q.CarrierCode, wantCarrier) && (wantService == "" || strings.EqualFold(q.ServiceCode, wantService)) {
			return q
		}
	}
	t.Fatalf("carrier %s/%s not found in quotes for %s->%s: %+v", wantCarrier, wantService, fromProv, toProv, out.Quotes)
	return CarrierQuoteDTO{}
}

func w3b46CreateAddress(
	t *testing.T,
	env *testEnv,
	u *userFixture,
	name, phone string,
	prov ProvinceDTO,
	ward WardDTO,
	street string,
) AddressDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  name,
		RecipientPhone: phone,
		ProvinceCode:   prov.Code,
		ProvinceName:   prov.Name,
		WardCode:       ward.Code,
		WardName:       ward.Name,
		StreetAddress:  street,
		IsDefault:      false,
	}, http.StatusCreated, http.StatusOK)
	var out CreateAddressResponse
	if err := DecodeJSON(resp, &out); err != nil || out.Address.ID == "" {
		t.Fatalf("create address failed: err=%v body=%s", err, string(resp.Body))
	}
	return out.Address
}

// w3b46FindHigherFeeDestination discovers a COD-supported province/ward whose mock_express STANDARD fee
// is strictly higher than the same-province quote (dynamically avoiding hardcoded province assumptions).
func w3b46FindHigherFeeDestination(
	t *testing.T,
	env *testEnv,
	buyerToken, shopID string,
	baseProv ProvinceDTO,
	baseWard WardDTO,
	baseFeeVND int64,
) (ProvinceDTO, WardDTO, int64) {
	t.Helper()
	provResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces", "", nil, nil, http.StatusOK)
	var provOut ProvincesResponse
	if err := DecodeJSON(provResp, &provOut); err != nil {
		t.Fatalf("decode provinces: %v", err)
	}

	// Prefer Ho Chi Minh City (79) or Ha Noi (01) when different from baseProv for deterministic cross-region delta.
	preferred := []string{"79", "01", "48", "92"}
	candidates := make([]ProvinceDTO, 0, len(provOut.Provinces))
	seen := make(map[string]bool)
	for _, code := range preferred {
		for _, p := range provOut.Provinces {
			if p.Code == code && p.Code != baseProv.Code {
				candidates = append(candidates, p)
				seen[p.Code] = true
			}
		}
	}
	for _, p := range provOut.Provinces {
		if p.Code != baseProv.Code && !seen[p.Code] {
			candidates = append(candidates, p)
		}
	}

	for _, p := range candidates {
		wResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces/"+url.PathEscape(p.Code)+"/wards", "", nil, nil, http.StatusOK)
		var wOut WardsResponse
		if err := DecodeJSON(wResp, &wOut); err != nil {
			t.Fatalf("decode wards for %s: %v", p.Code, err)
		}
		var chosen WardDTO
		for _, w := range wOut.Wards {
			if w.CODSupported {
				chosen = w
				break
			}
		}
		if chosen.Code == "" {
			continue
		}
		q := w3b46QuoteForRoute(t, env, buyerToken, shopID, baseProv.Code, baseWard.Code, p.Code, chosen.Code, 500, 200000, "mock_express", "STANDARD")
		if q.CODSupported && q.FeeVND > baseFeeVND {
			return p, chosen, q.FeeVND
		}
	}
	t.Fatalf("could not find any COD-supported province with mock_express fee > %d (base province %s)", baseFeeVND, baseProv.Code)
	return ProvinceDTO{}, WardDTO{}, 0
}

func w3b46GetOrderWithAddress(t *testing.T, env *testEnv, token, orderID string) w3b46OrderWithAddressDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders/"+url.PathEscape(orderID), token, nil, nil, http.StatusOK)
	var out w3b46OrderWithAddressResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode order %s: %v", orderID, err)
	}
	return out.Order
}

func w3b46GetPaymentDetail(t *testing.T, env *testEnv, token, paymentID string) w3b46PaymentDetailDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/payments/"+url.PathEscape(paymentID), token, nil, nil, http.StatusOK)
	var out w3b46PaymentDetailResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode payment %s: %v", paymentID, err)
	}
	return out.Payment
}

func w3b46GetShipment(t *testing.T, env *testEnv, token, orderID string) w3b46ShipmentDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/shipments/"+url.PathEscape(orderID), token, nil, nil, http.StatusOK)
	var out w3b46ShipmentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode shipment for order %s: %v", orderID, err)
	}
	return out.Shipment
}

func w3b46WaitNotificationByLinkAndSubstr(
	t *testing.T,
	env *testEnv,
	token, wantLink, wantSubstr string,
) NotificationDTO {
	t.Helper()
	var matched NotificationDTO
	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, fmt.Sprintf("notification link=%q substr=%q", wantLink, wantSubstr), func() (bool, string) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=50", token, nil, nil, http.StatusOK)
		var out NotificationsListResponse
		if err := DecodeJSON(resp, &out); err != nil {
			return false, err.Error()
		}
		for _, n := range out.Items {
			if wantLink != "" && n.Link != wantLink {
				continue
			}
			combined := strings.ToLower(n.Title + "\n" + n.Body)
			if wantSubstr == "" || strings.Contains(combined, strings.ToLower(wantSubstr)) {
				matched = n
				return true, ""
			}
		}
		return false, fmt.Sprintf("scanned %d notifications", len(out.Items))
	})
	return matched
}

func w3b46WaitNewMailpitEmail(
	t *testing.T,
	env *testEnv,
	toEmail string,
	seenIDs map[string]struct{},
	wantSubstr string,
) *MailpitMessage {
	t.Helper()
	var matched *MailpitMessage
	target := strings.ToLower(strings.TrimSpace(toEmail))
	needle := strings.ToLower(strings.TrimSpace(wantSubstr))
	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, fmt.Sprintf("new Mailpit email to %s containing %q", toEmail, wantSubstr), func() (bool, string) {
		list, err := env.mailpit.ListMessages(env.ctx(t), 100)
		if err != nil {
			return false, err.Error()
		}
		for _, summary := range list.Messages {
			if _, old := seenIDs[summary.ID]; old {
				continue
			}
			isRecip := false
			for _, a := range summary.To {
				if strings.ToLower(strings.TrimSpace(a.Address)) == target {
					isRecip = true
					break
				}
			}
			if !isRecip {
				continue
			}
			full, getErr := env.mailpit.GetMessage(env.ctx(t), summary.ID)
			if getErr != nil {
				continue
			}
			hay := strings.ToLower(full.Subject + "\n" + full.Text + "\n" + full.HTML + "\n" + summary.Snippet)
			if needle == "" || strings.Contains(hay, needle) {
				matched = full
				return true, ""
			}
		}
		return false, fmt.Sprintf("checked %d messages", len(list.Messages))
	})
	return matched
}

// --- Pure-Go PDF A6 & Vietnamese Unicode Text Inspector (for UC-S15 / G5) ---

var (
	w3b46RePDFPage    = regexp.MustCompile(`/Type\s*/Page\b`)
	w3b46ReMediaBox   = regexp.MustCompile(`/MediaBox\s*\[\s*([0-9.+-]+)\s+([0-9.+-]+)\s+([0-9.+-]+)\s+([0-9.+-]+)\s*\]`)
	w3b46RePDFStream  = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	w3b46ReBFChar     = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	w3b46ReBFRange    = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	w3b46ReHexPair    = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	w3b46ReRangeLine  = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	w3b46ReHexText    = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj|\[([^\]]*)\]\s*TJ`)
	w3b46ReHexToken   = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
	w3b46ReASCIILitTJ = regexp.MustCompile(`\(([^()]*)\)\s*Tj`)
)

type w3b46PDFInfo struct {
	PageCount      int
	MediaBoxes     [][4]float64
	HasFontFile2   bool
	HasToUnicode   bool
	ExtractedText  string
	ExtractedPages []string
}

func w3b46InspectPDF(t *testing.T, data []byte) w3b46PDFInfo {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		prefix := data
		if len(prefix) > 32 {
			prefix = prefix[:32]
		}
		t.Fatalf("PDF missing %%PDF- header, got prefix %q", string(prefix))
	}
	if !bytes.Contains(data, []byte("%%EOF")) {
		t.Fatalf("PDF missing %%%%EOF trailer")
	}

	raw := string(data)
	out := w3b46PDFInfo{
		PageCount:    len(w3b46RePDFPage.FindAllString(raw, -1)),
		HasFontFile2: bytes.Contains(data, []byte("/FontFile2")),
		HasToUnicode: bytes.Contains(data, []byte("/ToUnicode")),
	}
	for _, m := range w3b46ReMediaBox.FindAllStringSubmatch(raw, -1) {
		var box [4]float64
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseFloat(m[i+1], 64)
			if err != nil {
				t.Fatalf("parse PDF MediaBox float %q: %v", m[i+1], err)
			}
			box[i] = v
		}
		out.MediaBoxes = append(out.MediaBoxes, box)
	}

	cmap := make(map[uint16]string)
	var contentStreams []string
	for _, m := range w3b46RePDFStream.FindAllSubmatch(data, -1) {
		streamBytes := m[1]
		if zr, err := zlib.NewReader(bytes.NewReader(streamBytes)); err == nil {
			if dec, rErr := io.ReadAll(zr); rErr == nil {
				streamBytes = dec
			}
			_ = zr.Close()
		}
		s := string(streamBytes)
		if strings.Contains(s, "begincmap") {
			w3b46ParseToUnicode(s, cmap)
		} else if strings.Contains(s, "BT") && (strings.Contains(s, "Tj") || strings.Contains(s, "TJ")) {
			contentStreams = append(contentStreams, s)
		}
	}

	var allText strings.Builder
	for _, cs := range contentStreams {
		var pageText strings.Builder
		for _, m := range w3b46ReHexText.FindAllStringSubmatch(cs, -1) {
			if m[1] != "" {
				pageText.WriteString(w3b46DecodeCIDHex(m[1], cmap))
				pageText.WriteByte('\n')
			} else if m[2] != "" {
				for _, tok := range w3b46ReHexToken.FindAllStringSubmatch(m[2], -1) {
					pageText.WriteString(w3b46DecodeCIDHex(tok[1], cmap))
				}
				pageText.WriteByte('\n')
			}
		}
		for _, m := range w3b46ReASCIILitTJ.FindAllStringSubmatch(cs, -1) {
			pageText.WriteString(m[1])
			pageText.WriteByte('\n')
		}
		txt := pageText.String()
		out.ExtractedPages = append(out.ExtractedPages, txt)
		allText.WriteString(txt)
	}
	out.ExtractedText = allText.String()
	return out
}

func w3b46ParseToUnicode(s string, cmap map[uint16]string) {
	decodeUTF16BE := func(h string) string {
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
	for _, blk := range w3b46ReBFChar.FindAllStringSubmatch(s, -1) {
		for _, pair := range w3b46ReHexPair.FindAllStringSubmatch(blk[1], -1) {
			cid, err := strconv.ParseUint(pair[1], 16, 16)
			if err == nil {
				cmap[uint16(cid)] = decodeUTF16BE(pair[2])
			}
		}
	}
	for _, blk := range w3b46ReBFRange.FindAllStringSubmatch(s, -1) {
		for _, r := range w3b46ReRangeLine.FindAllStringSubmatch(blk[1], -1) {
			startCID, e1 := strconv.ParseUint(r[1], 16, 16)
			endCID, e2 := strconv.ParseUint(r[2], 16, 16)
			baseUni, e3 := strconv.ParseUint(r[3], 16, 32)
			if e1 == nil && e2 == nil && e3 == nil {
				for cid := startCID; cid <= endCID; cid++ {
					cmap[uint16(cid)] = string(rune(baseUni + (cid - startCID)))
				}
			}
		}
	}
}

func w3b46DecodeCIDHex(h string, cmap map[uint16]string) string {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw)%2 != 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(raw); i += 2 {
		cid := uint16(raw[i])<<8 | uint16(raw[i+1])
		if str, ok := cmap[cid]; ok {
			b.WriteString(str)
		}
	}
	return b.String()
}

func w3b46UploadMultipartCSV(
	t *testing.T,
	env *testEnv,
	token string,
	filename string,
	csvBytes []byte,
) (int, http.Header, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := fw.Write(csvBytes); err != nil {
		t.Fatalf("write multipart csv: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/seller/products/import", token, mw.FormDataContentType(), buf.Bytes())
}

// w3b46EnsureKnownShippingAndPaymentState resets shared seller shipping-settings to platform defaults
// (both ghn and mock_express enabled, freeship disabled, default parcel 500g / 20x15x10cm) and verifies
// platform carriers & payment methods are enabled so tests are independent of execution order.
func w3b46EnsureKnownShippingAndPaymentState(t *testing.T, env *testEnv, adminTok string, sellers ...*sellerFixture) {
	t.Helper()
	for _, s := range sellers {
		if s == nil {
			continue
		}
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", s.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn", "mock_express"},
			"default_weight_grams":    500,
			"default_length_cm":       20,
			"default_width_cm":        15,
			"default_height_cm":       10,
			"free_ship_enabled":       false,
			"free_ship_min_order_vnd": 0,
			"free_ship_max_vnd":       0,
		}, http.StatusOK)
	}
	if adminTok == "" {
		return
	}
	// Self-heal platform carriers (ghn, mock_express) only if a prior test left one disabled.
	cResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/carriers", adminTok, nil, nil, http.StatusOK)
	var cList struct {
		Carriers []struct {
			CarrierCode string `json:"carrier_code"`
			Enabled     bool   `json:"enabled"`
		} `json:"carriers"`
	}
	if err := DecodeJSON(cResp, &cList); err != nil {
		t.Fatalf("decode admin carriers: %v", err)
	}
	for _, c := range cList.Carriers {
		code := strings.ToLower(strings.TrimSpace(c.CarrierCode))
		if (code == "ghn" || code == "mock_express") && !c.Enabled {
			env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/"+url.PathEscape(code), adminTok, nil, map[string]any{
				"enabled": true,
			}, http.StatusOK)
		}
	}
	// Self-heal payment_method_configs (COD, VIETQR, ZALOPAY) only if a prior test left one disabled or narrowed.
	pmResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payment-methods", adminTok, nil, nil, http.StatusOK)
	var pmList struct {
		Methods []struct {
			Method        string `json:"method"`
			Enabled       bool   `json:"enabled"`
			MinOrderVND   int64  `json:"min_order_vnd"`
			MaxOrderVND   int64  `json:"max_order_vnd"`
			ExpiryMinutes int32  `json:"expiry_minutes"`
			Note          string `json:"note"`
			Version       int64  `json:"version"`
		} `json:"methods"`
	}
	if err := DecodeJSON(pmResp, &pmList); err != nil {
		t.Fatalf("decode admin payment-methods: %v", err)
	}
	for _, m := range pmList.Methods {
		method := NormalizeStatus(m.Method)
		needsFix := !m.Enabled
		minV := m.MinOrderVND
		maxV := m.MaxOrderVND
		if method == "COD" && (maxV > 0 && maxV < 5000000) {
			maxV = 5000000
			needsFix = true
		}
		if (method == "VIETQR" || method == "ZALOPAY") && minV > 0 {
			minV = 0
			needsFix = true
		}
		if needsFix {
			env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/"+url.PathEscape(method), adminTok, nil, map[string]any{
				"enabled":          true,
				"min_order_vnd":    minV,
				"max_order_vnd":    maxV,
				"expiry_minutes":   m.ExpiryMinutes,
				"note":             m.Note,
				"expected_version": m.Version,
			}, http.StatusOK)
		}
	}
}

// =================================================================================================
// 1. UC-B26: Update Order Address Before Carrier Pickup (G1–G3 + Manager Review Additions)
// =================================================================================================

func TestE2E_W3B3_A8_UC_B26_UpdateOrderAddress(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	w3b46EnsureKnownShippingAndPaymentState(t, env, adminTok, seller1, seller2)
	buyer := env.ensureVerifiedBuyer(t)
	otherBuyer := env.registerVerifyAndLogin(t, "w3b46-other-buyer")
	otherAddr := env.addAddress(t, otherBuyer)

	baseProv, baseWard := env.ensureProvinceAndWard(t)
	baseQuote := w3b46QuoteForRoute(t, env, buyer.AccessToken, seller1.ShopID, baseProv.Code, baseWard.Code, baseProv.Code, baseWard.Code, 500, 200000, "mock_express", "STANDARD")
	farProv, farWard, farFee := w3b46FindHigherFeeDestination(t, env, buyer.AccessToken, seller1.ShopID, baseProv, baseWard, baseQuote.FeeVND)

	// Prepare buyer saved addresses (address_id only — no custom_address per manager review).
	secretSameStreet := "Số 77 Ngõ Bí Mật " + env.runID
	secretFarStreet := "Số 999 Đường Không Lộ PII " + env.runID
	secretSame2Street := "Số 88 Phố Cập Nhật " + env.runID
	secretPhone := "0912345678"

	addrSame := w3b46CreateAddress(t, env, buyer, "Nguyễn Văn An", buyer.Phone, baseProv, baseWard, secretSameStreet)
	addrSame2 := w3b46CreateAddress(t, env, buyer, "Nguyễn Văn Bình", secretPhone, baseProv, baseWard, secretSame2Street)
	addrFar := w3b46CreateAddress(t, env, buyer, "Trần Thị Cẩm", secretPhone, farProv, farWard, secretFarStreet)
	// Hoang Sa (48/20333) has cod_supported=false (E16/G2).
	addrIsland := w3b46CreateAddress(t, env, buyer, "Lê Đảo Xa", buyer.Phone, ProvinceDTO{Code: "48", Name: "Thành phố Đà Nẵng"}, WardDTO{Code: "20333", ProvinceCode: "48", Name: "Đặc khu Hoàng Sa"}, "Số 1 Đảo Hoàng Sa")

	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "W3B3 B26 Áo Khoác "+env.runID, 250000, 100, 500)
	_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "W3B3 B26 Quần Kaki "+env.runID, 200000, 100, 500)

	// -----------------------------------------------------------------------------------------
	// 1a. COD PREPARING with live mock_express shipment: preview -> fee guards -> increase ->
	//     shipment recreated + COD/payment synced + PII-free notification -> deliver & balanced ledger.
	// -----------------------------------------------------------------------------------------
	t.Run("COD_Preparing_IncreaseFee_RecreateShipment_Notification_And_Ledger", func(t *testing.T) {
		co := w3b42PlaceCODOrder(t, env, buyer, addrSame.ID, seller1, sku1, "mock_express", "STANDARD")
		ord := co.Orders[0]

		// Confirm & ship to reach PREPARING with a live forward shipment.
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID)), seller1.AccessToken, nil, nil, http.StatusOK)
		shipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil || shipOut.TrackingCode == "" {
			t.Fatalf("ship COD order failed: err=%v body=%s", err, string(shipResp.Body))
		}
		oldTracking := shipOut.TrackingCode
		oldFee := ord.ShippingFeeVND
		oldTotal := ord.TotalPayableVND
		addrPath := fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(ord.ID))

		// Snapshot seller & buyer Mailpit message IDs before address update.
		sellerMailSeen, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), seller1.Email)
		if err != nil {
			t.Fatalf("snapshot seller1 mailpit: %v", err)
		}
		buyerMailSeen, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), buyer.Email)
		if err != nil {
			t.Fatalf("snapshot buyer mailpit: %v", err)
		}

		// Preview only -> returns positive delta without mutating DB or shipment.
		prevResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID:   addrFar.ID,
			PreviewOnly: true,
		}, http.StatusOK)
		var prev w3b46UpdateAddressResponse
		if err := DecodeJSON(prevResp, &prev); err != nil {
			t.Fatalf("decode preview address update: %v", err)
		}
		if !prev.PreviewOnly || prev.FeeDeltaVND <= 0 || !prev.RequiresFeeConfirmation || prev.NewShippingFeeVND <= oldFee {
			t.Fatalf("G2 preview mismatch: got %+v (oldFee=%d expected farFee=%d)", prev, oldFee, farFee)
		}
		if prev.NewTotalPayableVND != oldTotal+prev.FeeDeltaVND {
			t.Fatalf("G2 preview total=%d, want %d", prev.NewTotalPayableVND, oldTotal+prev.FeeDeltaVND)
		}
		// Verify DB untouched after preview.
		afterPrev := w3b46GetOrderWithAddress(t, env, buyer.AccessToken, ord.ID)
		if afterPrev.ShippingFeeVND != oldFee || afterPrev.TotalPayableVND != oldTotal || afterPrev.TrackingCode != oldTracking {
			t.Fatalf("G2 preview mutated order: got fee=%d total=%d trk=%s, want %d/%d/%s",
				afterPrev.ShippingFeeVND, afterPrev.TotalPayableVND, afterPrev.TrackingCode, oldFee, oldTotal, oldTracking)
		}

		// Missing accept_fee_increase -> 409 ADDRESS_FEE_CONFIRMATION_REQUIRED.
		noAcceptResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID:         addrFar.ID,
			AcceptFeeIncrease: false,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "COD increase without accept_fee_increase", noAcceptResp, "ADDRESS_FEE_CONFIRMATION_REQUIRED")

		// Wrong expected_new_shipping_fee_vnd -> 409 ADDRESS_FEE_CHANGED with refreshed quote object.
		wrongExpResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID:                 addrFar.ID,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVND: prev.NewShippingFeeVND - 1000,
		}, http.StatusConflict)
		var feeChanged w3b46FeeChangedEnvelope
		if err := DecodeJSON(wrongExpResp, &feeChanged); err != nil {
			t.Fatalf("decode ADDRESS_FEE_CHANGED: %v body=%s", err, string(wrongExpResp.Body))
		}
		if feeChanged.Error.Code != "ADDRESS_FEE_CHANGED" {
			t.Fatalf("expected error.code=ADDRESS_FEE_CHANGED, got %q (body=%s)", feeChanged.Error.Code, string(wrongExpResp.Body))
		}
		if feeChanged.Quote == nil ||
			feeChanged.Quote.OldShippingFeeVND != oldFee ||
			feeChanged.Quote.NewShippingFeeVND != prev.NewShippingFeeVND ||
			feeChanged.Quote.FeeDeltaVND != prev.FeeDeltaVND ||
			feeChanged.Quote.NewTotalPayableVND != prev.NewTotalPayableVND {
			t.Fatalf("G2 ADDRESS_FEE_CHANGED missing/mismatched quote: got %+v, want old=%d new=%d delta=%d total=%d (body=%s)",
				feeChanged.Quote, oldFee, prev.NewShippingFeeVND, prev.FeeDeltaVND, prev.NewTotalPayableVND, string(wrongExpResp.Body))
		}

		// COD unsupported island ward -> 409 COD_AREA_NOT_SUPPORTED.
		islandResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID:                 addrIsland.ID,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVND: prev.NewShippingFeeVND,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "COD address update to Hoang Sa", islandResp, "COD_AREA_NOT_SUPPORTED")

		// Apply with exact expected_new_shipping_fee_vnd + Idempotency-Key -> 200.
		idemKey := "w3b46-b26-inc-" + uuid.NewString()
		applyReq := w3b46UpdateAddressRequest{
			AddressID:                 addrFar.ID,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVND: prev.NewShippingFeeVND,
			IdempotencyKey:            idemKey,
		}
		applyResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, applyReq, http.StatusOK)
		var applied w3b46UpdateAddressResponse
		if err := DecodeJSON(applyResp, &applied); err != nil {
			t.Fatalf("decode apply address update: %v", err)
		}
		if !applied.ShipmentRecreated || !applied.FeeAdjusted || applied.FeeDeltaVND != prev.FeeDeltaVND {
			t.Fatalf("G2/G3 apply mismatch: %+v", applied)
		}
		newTracking := applied.Order.TrackingCode
		if newTracking == "" || newTracking == oldTracking {
			t.Fatalf("G3 expected new tracking code != %q, got %q", oldTracking, newTracking)
		}
		if applied.Order.ShippingFeeVND != prev.NewShippingFeeVND || applied.Order.TotalPayableVND != prev.NewTotalPayableVND {
			t.Fatalf("G2 updated order fee/total=%d/%d, want %d/%d",
				applied.Order.ShippingFeeVND, applied.Order.TotalPayableVND, prev.NewShippingFeeVND, prev.NewTotalPayableVND)
		}

		// Idempotent replay with same Idempotency-Key must NOT double-apply fee delta.
		replayResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, applyReq, http.StatusOK)
		var replayed w3b46UpdateAddressResponse
		if err := DecodeJSON(replayResp, &replayed); err != nil {
			t.Fatalf("decode replay address update: %v", err)
		}
		if replayed.Order.TotalPayableVND != prev.NewTotalPayableVND || replayed.Order.TrackingCode != newTracking {
			t.Fatalf("idempotent replay drifted: total=%d trk=%s, want %d/%s",
				replayed.Order.TotalPayableVND, replayed.Order.TrackingCode, prev.NewTotalPayableVND, newTracking)
		}

		// Payment group total & shop allocation shipping fee MUST match order.total_payable_vnd / new_shipping_fee_vnd.
		payDetail := w3b46GetPaymentDetail(t, env, adminTok, co.PaymentID)
		if payDetail.AmountVND != prev.NewTotalPayableVND {
			t.Fatalf("G2 payment.amount_vnd=%d, want %d", payDetail.AmountVND, prev.NewTotalPayableVND)
		}
		foundAlloc := false
		for _, a := range payDetail.ShopAllocations {
			if a.OrderID == ord.ID {
				foundAlloc = true
				if a.ShippingFeeVND != prev.NewShippingFeeVND {
					t.Fatalf("G2 payment allocation shipping_fee_vnd=%d, want %d", a.ShippingFeeVND, prev.NewShippingFeeVND)
				}
			}
		}
		if !foundAlloc {
			t.Fatalf("G2 payment %s missing allocation for order %s: %+v", co.PaymentID, ord.ID, payDetail.ShopAllocations)
		}

		// New forward shipment COD amount MUST match order.total_payable_vnd (manager review requirement).
		shipDetail := w3b46GetShipment(t, env, seller1.AccessToken, ord.ID)
		if shipDetail.TrackingCode != newTracking || shipDetail.CODAmountVND != prev.NewTotalPayableVND {
			t.Fatalf("G3 new shipment tracking=%q cod=%d, want %q / %d",
				shipDetail.TrackingCode, shipDetail.CODAmountVND, newTracking, prev.NewTotalPayableVND)
		}

		// Seller notification (in-app + Mailpit email) on order.address_updated:
		// Emitted ONCE at Step 4 (before Step 5 recreates the shipment) with shipment_queued=true,
		// shipment_recreated=false, tracking_code=""; reminds seller to reprint label, ZERO PII!
		sellerNotif := w3b46WaitNotificationByLinkAndSubstr(t, env, seller1.AccessToken, "/seller/don-hang/"+ord.ID, ord.OrderCode)
		notifText := sellerNotif.Title + "\n" + sellerNotif.Body
		if !strings.Contains(strings.ToLower(notifText), "in lại nhãn") {
			t.Fatalf("G3 seller notification missing label-reprint reminder: %q", notifText)
		}
		if strings.Contains(notifText, secretFarStreet) || strings.Contains(notifText, secretPhone) {
			t.Fatalf("G3 seller notification leaked street_address or phone PII: %q", notifText)
		}
		sellerMail := w3b46WaitNewMailpitEmail(t, env, seller1.Email, sellerMailSeen, ord.OrderCode)
		mailAll := sellerMail.Subject + "\n" + sellerMail.Text + "\n" + sellerMail.HTML
		if !strings.Contains(strings.ToLower(mailAll), "in lại nhãn") || !strings.Contains(mailAll, ord.OrderCode) {
			t.Fatalf("G3 seller email missing reprint reminder or order code %s: subject=%q body=%q", ord.OrderCode, sellerMail.Subject, sellerMail.Text)
		}
		if strings.Contains(mailAll, secretFarStreet) || strings.Contains(mailAll, secretPhone) {
			t.Fatalf("G3 seller email leaked street_address or phone PII: body=%q", sellerMail.Text)
		}

		// Buyer receives in-app confirmation (no PII leak) and no duplicate email for address update.
		// Per sitelink.go (merged notification contract), MARKETPLACE orders carry an empty buyer link "".
		buyerNotif := w3b46WaitNotificationByLinkAndSubstr(t, env, buyer.AccessToken, "", ord.OrderCode)
		if buyerNotif.Link != "" {
			t.Fatalf("G3 MARKETPLACE buyer notification link = %q, want empty \"\" (no platform buyer order page)", buyerNotif.Link)
		}
		buyerNotifText := buyerNotif.Title + "\n" + buyerNotif.Body
		if strings.Contains(buyerNotifText, secretFarStreet) || strings.Contains(buyerNotifText, secretPhone) {
			t.Fatalf("G3 buyer notification leaked PII: %q", buyerNotifText)
		}
		if list, lErr := env.mailpit.ListMessages(env.ctx(t), 100); lErr == nil {
			for _, m := range list.Messages {
				if _, old := buyerMailSeen[m.ID]; old {
					continue
				}
				for _, rcp := range m.To {
					if strings.EqualFold(strings.TrimSpace(rcp.Address), buyer.Email) &&
						strings.Contains(strings.ToLower(m.Subject), "địa chỉ") {
						t.Fatalf("G3 buyer should not receive email for order.address_updated: subject=%q", m.Subject)
					}
				}
			}
		}

		// Deliver via signed webhook on NEW tracking code, confirm received -> COMPLETED, check balanced ledger.
		ledgerBefore := w3b44AssertLedgerBalanced(t, env)
		codRecBefore := w3b42FindAccountBalance(ledgerBefore, "cod_receivable")
		shipPayBefore := w3b42FindAccountBalance(ledgerBefore, "shipping_fee_payable")

		w3b42PostShippingWebhook(t, env, "mock_express", ord.ID, newTracking, "delivered", "Giao thành công sau đổi địa chỉ")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "COD order DELIVERED", func() (bool, string) {
			cur := env.getOrder(t, buyer.AccessToken, ord.ID)
			return StatusEquals(cur.Status, "DELIVERED") && StatusEquals(cur.PaymentStatus, "SUCCEEDED"), cur.Status + "/" + cur.PaymentStatus
		})

		// Delivered order rejects address update (409 ADDRESS_UPDATE_NOT_ALLOWED).
		delivUpdResp := env.mustDoJSON(t, http.MethodPatch, addrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame.ID,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "address update on DELIVERED order", delivUpdResp, "ADDRESS_UPDATE_NOT_ALLOWED")

		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord.ID)), buyer.AccessToken, nil, nil, http.StatusOK)
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ledger reflects adjusted COD delivery + settlement", func() (bool, string) {
			sum := env.getLedgerSummary(t)
			codDelta := w3b42FindAccountBalance(sum, "cod_receivable") - codRecBefore
			shipDelta := w3b42FindAccountBalance(sum, "shipping_fee_payable") - shipPayBefore
			return sum.IsBalanced && codDelta >= prev.NewTotalPayableVND && shipDelta >= prev.NewShippingFeeVND,
				fmt.Sprintf("balanced=%v codDelta=%d (want>=%d) shipDelta=%d (want>=%d)",
					sum.IsBalanced, codDelta, prev.NewTotalPayableVND, shipDelta, prev.NewShippingFeeVND)
		})
		w3b44AssertLedgerBalanced(t, env)
	})

	// -----------------------------------------------------------------------------------------
	// 1b. Multi-shop COD order + COD fee DECREASE (no shipment yet -> no reprint reminder)
	//     while sibling sub-order is already DELIVERED (held); ledger balanced after completion.
	// -----------------------------------------------------------------------------------------
	t.Run("COD_DecreaseFee_MultiShop_NoShipmentNotification_And_Ledger", func(t *testing.T) {
		platVoucher := env.createAdminVoucher(t, "PLATFORM", 20000, 100000, 5)
		env.saveVoucher(t, buyer.AccessToken, platVoucher)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b46-b26-multi-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:      "MARKETPLACE",
			AddressID:         addrFar.ID,
			FromCart:          false,
			Items:             []CheckoutItemInputDTO{{SKUID: sku1.ID, Quantity: 1}, {SKUID: sku2.ID, Quantity: 1}},
			PlatformVoucherID: platVoucher.ID,
			PaymentMethod:     "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
				{ShopID: seller2.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated)
		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 2 {
			t.Fatalf("multi-shop COD checkout failed: err=%v body=%s", err, string(coResp.Body))
		}
		var ord1, ord2 OrderDTO
		for _, o := range co.Orders {
			if o.ShopID == seller1.ShopID {
				ord1 = o
			} else {
				ord2 = o
			}
		}

		// Deliver sub-order 2 first so its allocation transitions awaiting_collection -> held.
		ship2Resp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord2.ID)), seller2.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express", ServiceCode: "STANDARD",
		}, http.StatusOK)
		var ship2Out SellerShipResponse
		if err := DecodeJSON(ship2Resp, &ship2Out); err != nil || ship2Out.TrackingCode == "" {
			t.Fatalf("ship ord2 failed: err=%v body=%s", err, string(ship2Resp.Body))
		}
		w3b42PostShippingWebhook(t, env, "mock_express", ord2.ID, ship2Out.TrackingCode, "delivered", "Delivered sibling")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "ord2 DELIVERED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ord2.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})

		// Sub-order 1 is CONFIRMED with NO shipment yet: switch from addrFar -> addrSame (fee DECREASE).
		seller1MailSeen, _ := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), seller1.Email)
		oldFee1 := ord1.ShippingFeeVND
		oldTotal1 := ord1.TotalPayableVND

		decResp := env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(ord1.ID)), buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame.ID,
		}, http.StatusOK)
		var dec w3b46UpdateAddressResponse
		if err := DecodeJSON(decResp, &dec); err != nil {
			t.Fatalf("decode COD decrease: %v", err)
		}
		if !dec.FeeAdjusted || dec.FeeDeltaVND >= 0 || dec.NewShippingFeeVND >= oldFee1 {
			t.Fatalf("G2 COD decrease expected negative delta: got %+v (oldFee=%d)", dec, oldFee1)
		}
		wantTotal1 := oldTotal1 + dec.FeeDeltaVND
		if dec.NewTotalPayableVND != wantTotal1 || dec.Order.TotalPayableVND != wantTotal1 {
			t.Fatalf("G2 COD decrease total=%d/%d, want %d", dec.NewTotalPayableVND, dec.Order.TotalPayableVND, wantTotal1)
		}
		if dec.ShipmentRecreated || dec.ShipmentQueued {
			t.Fatalf("G3 CONFIRMED order without shipment should have recreated=false queued=false, got %+v", dec)
		}

		// Group payment amount must equal adjusted ord1 + unchanged ord2.
		payDetail := w3b46GetPaymentDetail(t, env, adminTok, co.PaymentID)
		if payDetail.AmountVND != wantTotal1+ord2.TotalPayableVND {
			t.Fatalf("G2 multi-shop payment.amount_vnd=%d, want %d (%d+%d)",
				payDetail.AmountVND, wantTotal1+ord2.TotalPayableVND, wantTotal1, ord2.TotalPayableVND)
		}

		// Seller1 notification & email must NOT remind to reprint shipping label (no shipment existed).
		n1 := w3b46WaitNotificationByLinkAndSubstr(t, env, seller1.AccessToken, "/seller/don-hang/"+ord1.ID, ord1.OrderCode)
		if strings.Contains(strings.ToLower(n1.Title+"\n"+n1.Body), "in lại nhãn") {
			t.Fatalf("notification-r0 ca #2: CONFIRMED order without shipment should not ask to reprint label: %+v", n1)
		}
		m1 := w3b46WaitNewMailpitEmail(t, env, seller1.Email, seller1MailSeen, ord1.OrderCode)
		if strings.Contains(strings.ToLower(m1.Subject+"\n"+m1.Text), "in lại nhãn") {
			t.Fatalf("notification-r0 ca #2: email without shipment should not ask to reprint label: subject=%q body=%q", m1.Subject, m1.Text)
		}

		// No-op update to the SAME address the order already has -> 200, no fee change, no second event/email.
		seller1AfterDecMailSeen, _ := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), seller1.Email)
		noopResp := env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(ord1.ID)), buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame.ID,
		}, http.StatusOK)
		var noop w3b46UpdateAddressResponse
		if err := DecodeJSON(noopResp, &noop); err != nil {
			t.Fatalf("decode same-address no-op: %v", err)
		}
		if noop.FeeAdjusted || noop.FeeDeltaVND != 0 || noop.ShipmentRecreated || noop.ShipmentQueued || noop.Order.TotalPayableVND != wantTotal1 {
			t.Fatalf("G3 same-address no-op mutated state: %+v", noop)
		}

		// Complete both orders and verify balanced ledger.
		ship1Resp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord1.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express", ServiceCode: "STANDARD",
		}, http.StatusOK)
		var ship1Out SellerShipResponse
		if err := DecodeJSON(ship1Resp, &ship1Out); err != nil || ship1Out.TrackingCode == "" {
			t.Fatalf("ship ord1 after address decrease failed: err=%v body=%s", err, string(ship1Resp.Body))
		}
		// New shipment created after decrease must carry the reduced COD total.
		if sh1 := w3b46GetShipment(t, env, seller1.AccessToken, ord1.ID); sh1.CODAmountVND != wantTotal1 {
			t.Fatalf("shipment COD after decrease=%d, want %d", sh1.CODAmountVND, wantTotal1)
		}
		w3b42PostShippingWebhook(t, env, "mock_express", ord1.ID, ship1Out.TrackingCode, "delivered", "Delivered reduced COD")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "ord1 DELIVERED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ord1.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord1.ID)), buyer.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord2.ID)), buyer.AccessToken, nil, nil, http.StatusOK)
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "multi-shop orders COMPLETED", func() (bool, string) {
			o1 := env.getOrder(t, buyer.AccessToken, ord1.ID)
			o2 := env.getOrder(t, buyer.AccessToken, ord2.ID)
			return StatusEquals(o1.Status, "COMPLETED") && StatusEquals(o2.Status, "COMPLETED"), o1.Status + "/" + o2.Status
		})
		// After the full ship+deliver+complete round-trip, confirm the no-op same-address update emitted 0 address emails.
		if list, lErr := env.mailpit.ListMessages(env.ctx(t), 100); lErr == nil {
			for _, m := range list.Messages {
				if _, old := seller1AfterDecMailSeen[m.ID]; old {
					continue
				}
				for _, rcp := range m.To {
					if strings.EqualFold(strings.TrimSpace(rcp.Address), seller1.Email) &&
						strings.Contains(m.Subject, ord1.OrderCode) &&
						strings.Contains(strings.ToLower(m.Subject), "địa chỉ") {
						t.Fatalf("G3 same-address no-op unexpectedly emitted order.address_updated email: subject=%q", m.Subject)
					}
				}
			}
		}
		w3b44AssertLedgerBalanced(t, env)
	})

	// -----------------------------------------------------------------------------------------
	// 1c. Prepaid (VIETQR + ZALOPAY), Invalid States (PENDING/SHIPPING), RBAC,
	//     Address Ownership, No custom_address, Direct gRPC Auth, Concurrent Double-Click.
	// -----------------------------------------------------------------------------------------
	t.Run("Prepaid_States_RBAC_AddressOwnership_And_ConcurrentDoubleClick", func(t *testing.T) {
		// VietQR PENDING order -> 409 ADDRESS_UPDATE_NOT_ALLOWED.
		vqrResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b46-vqr-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addrSame.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku1.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated)
		var vqrCo CheckoutResponse
		if err := DecodeJSON(vqrResp, &vqrCo); err != nil || len(vqrCo.Orders) != 1 {
			t.Fatalf("VietQR checkout failed: err=%v body=%s", err, string(vqrResp.Body))
		}
		vqrOrd := vqrCo.Orders[0]
		vqrAddrPath := fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(vqrOrd.ID))

		pendResp := env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame2.ID,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "PENDING VietQR address update", pendResp, "ADDRESS_UPDATE_NOT_ALLOWED")

		// Pay VietQR -> CONFIRMED.
		whResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "W3B46-VQR-" + uuid.NewString(),
			PaymentID:    vqrCo.PaymentID,
			OrderGroupID: vqrCo.CheckoutGroupID,
			AmountVND:    vqrCo.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + vqrCo.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("VietQR payment webhook failed: err=%v resp=%+v", err, whResp)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "VietQR order CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, vqrOrd.ID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})

		// Prepaid VietQR fee increase -> 409 ADDRESS_FEE_INCREASE_PREPAID, keeps old address.
		vqrIncResp := env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID:         addrFar.ID,
			AcceptFeeIncrease: true,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "VietQR prepaid fee increase", vqrIncResp, "ADDRESS_FEE_INCREASE_PREPAID")
		if got := w3b46GetOrderWithAddress(t, env, buyer.AccessToken, vqrOrd.ID); got.ShippingAddress != nil && got.ShippingAddress.ProvinceCode != baseProv.Code {
			t.Fatalf("VietQR prepaid increase mutated province to %s", got.ShippingAddress.ProvinceCode)
		}

		// Prepaid VietQR equal fee -> 200, total_payable_vnd unchanged.
		vqrEqResp := env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame2.ID,
		}, http.StatusOK)
		var vqrEq w3b46UpdateAddressResponse
		if err := DecodeJSON(vqrEqResp, &vqrEq); err != nil {
			t.Fatalf("decode VietQR equal-fee update: %v", err)
		}
		if vqrEq.FeeAdjusted || vqrEq.Order.TotalPayableVND != vqrOrd.TotalPayableVND {
			t.Fatalf("G2 VietQR equal-fee update must keep total=%d, got adjusted=%v total=%d",
				vqrOrd.TotalPayableVND, vqrEq.FeeAdjusted, vqrEq.Order.TotalPayableVND)
		}

		// Manager review: only address_id allowed (custom_address rejected); foreign address_id -> 403/404; other buyer -> 403.
		env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, map[string]any{
			"custom_address": map[string]any{
				"recipient_name":  "Freeform Blocked",
				"recipient_phone": buyer.Phone,
				"province_code":   baseProv.Code,
				"ward_code":       baseWard.Code,
				"street_address":  "1 Freeform St",
			},
		}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: otherAddr.ID,
		}, http.StatusForbidden, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, otherBuyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: otherAddr.ID,
		}, http.StatusForbidden)

		// Ship VietQR order + move to SHIPPING -> 409 ADDRESS_UPDATE_NOT_ALLOWED.
		vqrShipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(vqrOrd.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express", ServiceCode: "STANDARD",
		}, http.StatusOK)
		var vqrShipOut SellerShipResponse
		if err := DecodeJSON(vqrShipResp, &vqrShipOut); err != nil || vqrShipOut.TrackingCode == "" {
			t.Fatalf("ship VietQR order: err=%v body=%s", err, string(vqrShipResp.Body))
		}
		w3b42PostShippingWebhook(t, env, "mock_express", vqrOrd.ID, vqrShipOut.TrackingCode, "delivering", "Carrier picked up")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "VietQR order SHIPPING", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, vqrOrd.ID)
			return StatusEquals(o.Status, "SHIPPING"), o.Status
		})
		shipStateResp := env.mustDoJSON(t, http.MethodPatch, vqrAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame.ID,
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "SHIPPING order address update", shipStateResp, "ADDRESS_UPDATE_NOT_ALLOWED")

		// ZaloPay paid order (started at addrFar): decrease to addrSame -> 200 keeping paid fee & total unchanged;
		// switching back to addrFar has delta=0 against the original paid fee -> 200 keeping total unchanged.
		// (Prepaid fee increase -> 409 ADDRESS_FEE_INCREASE_PREPAID is already verified above on VietQR.)
		zpResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b46-zp-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addrFar.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku1.ID, Quantity: 1}},
			PaymentMethod: "ZALOPAY",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated)
		var zpCo CheckoutResponse
		if err := DecodeJSON(zpResp, &zpCo); err != nil || len(zpCo.Orders) != 1 {
			t.Fatalf("ZaloPay checkout failed: err=%v body=%s", err, string(zpResp.Body))
		}
		zpOrd := zpCo.Orders[0]
		zpPay := w3b46GetPaymentDetail(t, env, buyer.AccessToken, zpCo.PaymentID)
		cbBody, _ := w3b44BuildZaloPayCallbackWithAppID(t, env, env.cfg.ZaloPayAppID, zpPay.ProviderRef, w3b44NextZPTransID(env), zpCo.TotalPayableVND, 0, "")
		env.mustDoJSON(t, http.MethodPost, "/webhooks/zalopay", "", nil, cbBody, http.StatusOK)
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "ZaloPay order CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, zpOrd.ID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})
		zpAddrPath := fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(zpOrd.ID))
		zpDecResp := env.mustDoJSON(t, http.MethodPatch, zpAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame.ID,
		}, http.StatusOK)
		var zpDec w3b46UpdateAddressResponse
		if err := DecodeJSON(zpDecResp, &zpDec); err != nil {
			t.Fatalf("decode ZaloPay decrease: %v", err)
		}
		if zpDec.FeeAdjusted || zpDec.Order.TotalPayableVND != zpOrd.TotalPayableVND {
			t.Fatalf("G2 ZaloPay decrease must preserve total_payable_vnd=%d, got adjusted=%v total=%d",
				zpOrd.TotalPayableVND, zpDec.FeeAdjusted, zpDec.Order.TotalPayableVND)
		}
		zpBackResp := env.mustDoJSON(t, http.MethodPatch, zpAddrPath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrFar.ID,
		}, http.StatusOK)
		var zpBack w3b46UpdateAddressResponse
		if err := DecodeJSON(zpBackResp, &zpBack); err != nil {
			t.Fatalf("decode ZaloPay back to original fee: %v", err)
		}
		if zpBack.FeeAdjusted || zpBack.Order.TotalPayableVND != zpOrd.TotalPayableVND {
			t.Fatalf("G2 ZaloPay switch back to original paid-fee destination must keep total_payable_vnd=%d unchanged, got adjusted=%v total=%d",
				zpOrd.TotalPayableVND, zpBack.FeeAdjusted, zpBack.Order.TotalPayableVND)
		}

		// Manager review: concurrent double-click WITHOUT Idempotency-Key -> at most one address/fee change.
		raceCo := w3b42PlaceCODOrder(t, env, buyer, addrSame.ID, seller1, sku1, "mock_express", "STANDARD")
		raceOrd := raceCo.Orders[0]
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(raceOrd.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express", ServiceCode: "STANDARD",
		}, http.StatusOK)
		racePath := fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(raceOrd.ID))
		racePrevResp := env.mustDoJSON(t, http.MethodPatch, racePath, buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrFar.ID, PreviewOnly: true,
		}, http.StatusOK)
		var racePrev w3b46UpdateAddressResponse
		if err := DecodeJSON(racePrevResp, &racePrev); err != nil {
			t.Fatalf("decode race preview: %v", err)
		}

		startCh := make(chan struct{})
		type raceRes struct {
			code int
			body []byte
		}
		results := make([]raceRes, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-startCh
				payload, _ := json.Marshal(w3b46UpdateAddressRequest{
					AddressID:                 addrFar.ID,
					AcceptFeeIncrease:         true,
					ExpectedNewShippingFeeVND: racePrev.NewShippingFeeVND,
				})
				c, _, b := w3b42RawHTTP(env.ctx(t), t, http.MethodPatch, env.cfg.GatewayURL+racePath, buyer.AccessToken, "application/json", payload)
				results[idx] = raceRes{code: c, body: b}
			}(i)
		}
		close(startCh)
		wg.Wait()

		okCount := 0
		for _, r := range results {
			switch r.code {
			case http.StatusOK:
				okCount++
			case http.StatusConflict:
				// Expected when concurrent intent or post-update state collides.
			default:
				t.Fatalf("concurrent address update without Idempotency-Key returned unexpected HTTP %d: %s", r.code, string(r.body))
			}
		}
		if okCount == 0 {
			t.Fatalf("concurrent address update expected >=1 200 OK, got %+v", results)
		}
		finalRaceOrd := env.getOrder(t, buyer.AccessToken, raceOrd.ID)
		if finalRaceOrd.TotalPayableVND != racePrev.NewTotalPayableVND || finalRaceOrd.ShippingFeeVND != racePrev.NewShippingFeeVND {
			t.Fatalf("concurrent double-click mutated fee/total unexpectedly: got %d/%d, want %d/%d",
				finalRaceOrd.ShippingFeeVND, finalRaceOrd.TotalPayableVND, racePrev.NewShippingFeeVND, racePrev.NewTotalPayableVND)
		}
		finalRacePay := w3b46GetPaymentDetail(t, env, adminTok, raceCo.PaymentID)
		if finalRacePay.AmountVND != racePrev.NewTotalPayableVND {
			t.Fatalf("concurrent double-click payment.amount_vnd=%d, want %d", finalRacePay.AmountVND, racePrev.NewTotalPayableVND)
		}
		finalRaceShip := w3b46GetShipment(t, env, seller1.AccessToken, raceOrd.ID)
		if finalRaceShip.CODAmountVND != racePrev.NewTotalPayableVND {
			t.Fatalf("concurrent double-click shipment COD=%d, want %d", finalRaceShip.CODAmountVND, racePrev.NewTotalPayableVND)
		}

		// Direct gRPC auth guards (G1, G2, G3):
		// 1) OrderService.UpdateOrderAddress rejects service tokens (G1).
		ordGRPC := w3b46DialOrderGRPC(t, env)
		ordSvcCtx := w3b45AuthCtx(env.ctx(t), promoMintServiceToken("order-service"))
		if _, gErr := ordGRPC.UpdateOrderAddress(ordSvcCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: raceOrd.ID, AddressId: addrSame.ID,
		}); status.Code(gErr) != codes.PermissionDenied {
			t.Fatalf("G1 service-token UpdateOrderAddress want PermissionDenied, got %v", gErr)
		}
		// 2) PaymentService.AdjustCODShippingFee rejects buyer JWT, admin JWT, and non-order service token (payment-r2 ca #5).
		payGRPC := w3b45DialPaymentGRPC(t, env)
		for _, tc := range []struct {
			name  string
			token string
		}{
			{"buyer_jwt", buyer.AccessToken},
			{"admin_jwt", adminTok},
			{"product_service_token", promoMintServiceToken("product-service")},
		} {
			if _, gErr := payGRPC.AdjustCODShippingFee(w3b45AuthCtx(env.ctx(t), tc.token), &paymentv1.AdjustCODShippingFeeRequest{
				OrderId:           raceOrd.ID,
				NewShippingFeeVnd: 20000,
				IdempotencyKey:    "w3b46-attack-" + uuid.NewString(),
			}); status.Code(gErr) != codes.PermissionDenied {
				t.Fatalf("G2 AdjustCODShippingFee with %s want PermissionDenied, got %v", tc.name, gErr)
			}
		}
		// 3) ShippingService.CreateShipment rejects non-order service token (shipping-r0 ca #1).
		shipGRPC := shpW2BDialShipping(t)
		if _, gErr := shipGRPC.CreateShipment(w3b45AuthCtx(env.ctx(t), promoMintServiceToken("product-service")), &shippingv1.CreateShipmentRequest{
			OrderId: uuid.NewString(), ShopId: seller1.ShopID,
		}); status.Code(gErr) != codes.PermissionDenied {
			t.Fatalf("G3 CreateShipment with product-service token want PermissionDenied, got %v", gErr)
		}
	})

	// -----------------------------------------------------------------------------------------
	// 1d. Optional transient carrier fault during address update -> shipment_queued=true (G3 + notification-r0 ca #3).
	// -----------------------------------------------------------------------------------------
	t.Run("COD_AddressUpdate_TransientCarrierFault_QueuedFlag", func(t *testing.T) {
		if !env.cfg.ShippingTestFaultInjection {
			t.Skip("SHIPPING_TEST_FAULT_INJECTION is not enabled in this environment")
		}
		_, carrierMaxRetries, estBackoff := w3b42RequireFaultInjectionTestConfig(t, env.cfg)

		co := w3b42PlaceCODOrder(t, env, buyer, addrSame.ID, seller1, sku1, "mock_express", "STANDARD")
		ord := co.Orders[0]
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express", ServiceCode: "STANDARD",
		}, http.StatusOK)

		sellerMailSeen, _ := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), seller1.Email)
		// Inject deterministic 503 on CreateShipment so Step 5 queues into shipment_create_jobs.
		faultCount := carrierMaxRetries
		if faultCount < 1 {
			faultCount = 1
		}
		w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, faultCount)

		// Step 4 already committed -> REST always returns 200 immediately with shipment_queued=true.
		updResp := env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/orders/%s/address", url.PathEscape(ord.ID)), buyer.AccessToken, nil, w3b46UpdateAddressRequest{
			AddressID: addrSame2.ID,
		}, http.StatusOK)
		var upd w3b46UpdateAddressResponse
		if err := DecodeJSON(updResp, &upd); err != nil {
			t.Fatalf("decode queued address update: %v", err)
		}
		if !upd.ShipmentQueued || upd.ShipmentRecreated {
			t.Fatalf("G3 expected shipment_queued=true shipment_recreated=false, got %+v", upd)
		}
		if StatusEquals(upd.Order.ShipmentJobStatus, "QUEUED") && upd.ShipmentJobID == "" {
			t.Fatalf("G3 queued shipping job missing shipment_job_id: %+v", upd)
		}

		// Seller still receives reprint-label reminder when shipment_queued=true (notification-r0 ca #3).
		n := w3b46WaitNotificationByLinkAndSubstr(t, env, seller1.AccessToken, "/seller/don-hang/"+ord.ID, "in lại nhãn")
		if strings.Contains(n.Body, secretSame2Street) || strings.Contains(n.Body, secretPhone) {
			t.Fatalf("G3 queued notification leaked PII: %+v", n)
		}
		m := w3b46WaitNewMailpitEmail(t, env, seller1.Email, sellerMailSeen, ord.OrderCode)
		if !strings.Contains(strings.ToLower(m.Subject+"\n"+m.Text), "in lại nhãn") {
			t.Fatalf("G3 queued email missing reprint reminder: subject=%q body=%q", m.Subject, m.Text)
		}

		// Wait for A5 worker to recreate the shipment.
		waitDur := env.cfg.PollTimeout
		if estBackoff+10*time.Second > waitDur {
			waitDur = estBackoff + 10*time.Second
		}
		env.pollUntil(t, waitDur, 300*time.Millisecond, "queued address-update shipment created", func() (bool, string) {
			cur := env.getOrder(t, seller1.AccessToken, ord.ID)
			return cur.TrackingCode != "" && cur.ShipmentJobStatus == "", fmt.Sprintf("trk=%q job=%q", cur.TrackingCode, cur.ShipmentJobStatus)
		})
	})
}

// =================================================================================================
// 2. UC-S15: Batch Ship Orders, Picking List & Batch A6 Vietnamese PDF Labels (G4–G5)
// =================================================================================================

func TestE2E_W3B3_A8_UC_S15_BatchShipPickingListAndBatchPDFLabels(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	w3b46EnsureKnownShippingAndPaymentState(t, env, adminTok, seller1, seller2)
	buyer := env.ensureVerifiedBuyer(t)

	_, skuA := env.createApprovedProduct(t, seller1, cat.ID, "W3B3 S15 Áo Phông "+env.runID, 150000, 100, 300)
	_, skuB := env.createApprovedProduct(t, seller1, cat.ID, "W3B3 S15 Quần Short "+env.runID, 180000, 100, 350)
	_, skuS2 := env.createApprovedProduct(t, seller2, cat.ID, "W3B3 S15 Shop2 Item "+env.runID, 120000, 50, 300)

	// Buyer address with full Vietnamese diacritics to assert on rendered A6 PDF labels.
	prov, ward := env.ensureProvinceAndWard(t)
	vietAddr := w3b46CreateAddress(t, env, buyer, "Nguyễn Thị Phượng", buyer.Phone, prov, ward, "123 Đường Đống Đa "+env.runID)

	placeOrder := func(seller *sellerFixture, items []CheckoutItemInputDTO) OrderDTO {
		t.Helper()
		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b46-s15-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     vietAddr.ID,
			Items:         items,
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated)
		var co CheckoutResponse
		if err := DecodeJSON(resp, &co); err != nil || len(co.Orders) != 1 {
			t.Fatalf("S15 checkout failed: err=%v body=%s", err, string(resp.Body))
		}
		return co.Orders[0]
	}

	// 5 CONFIRMED orders for seller1 (cancel #5 -> 4 CONFIRMED + 1 CANCELLED) + 1 foreign order for seller2.
	ord1 := placeOrder(seller1, []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 2}})
	ord2 := placeOrder(seller1, []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}, {SKUID: skuB.ID, Quantity: 2}})
	ord3 := placeOrder(seller1, []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 3}})
	ord4 := placeOrder(seller1, []CheckoutItemInputDTO{{SKUID: skuB.ID, Quantity: 1}})
	ord5 := placeOrder(seller1, []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}})
	env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord5.ID)), buyer.AccessToken, nil, map[string]string{
		"reason": "buyer cancelled before batch ship",
	}, http.StatusOK)

	foreignOrd := placeOrder(seller2, []CheckoutItemInputDTO{{SKUID: skuS2.ID, Quantity: 1}})
	foreignShipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(foreignOrd.ID)), seller2.AccessToken, nil, SellerShipRequest{
		CarrierCode: "mock_express", ServiceCode: "STANDARD",
	}, http.StatusOK)
	var foreignShipOut SellerShipResponse
	if err := DecodeJSON(foreignShipResp, &foreignShipOut); err != nil || foreignShipOut.TrackingCode == "" {
		t.Fatalf("ship foreign order failed: err=%v body=%s", err, string(foreignShipResp.Body))
	}

	// 1. Picking list: cross-shop order -> 403; valid 4 orders -> exact SKU sums (skuA=6, skuB=3, total_units=9).
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/orders/picking-list", seller1.AccessToken, nil, map[string]any{
		"order_ids": []string{ord1.ID, foreignOrd.ID},
	}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/orders/picking-list", seller1.AccessToken, nil, map[string]any{
		"order_ids": []string{},
	}, http.StatusBadRequest)

	pickResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/orders/picking-list", seller1.AccessToken, nil, map[string]any{
		"order_ids": []string{ord1.ID, ord2.ID, ord3.ID, ord4.ID},
	}, http.StatusOK)
	var pickOut w3b46PickingListResponse
	if err := DecodeJSON(pickResp, &pickOut); err != nil {
		t.Fatalf("decode picking list: %v", err)
	}
	if pickOut.OrderCount != 4 || pickOut.TotalUnits != 9 {
		t.Fatalf("G4 picking list count/units=%d/%d, want 4/9 (body=%s)", pickOut.OrderCount, pickOut.TotalUnits, string(pickResp.Body))
	}
	qtyBySKU := make(map[string]int32)
	for _, it := range pickOut.Items {
		qtyBySKU[it.SKUID] = it.TotalQuantity
	}
	if qtyBySKU[skuA.ID] != 6 || qtyBySKU[skuB.ID] != 3 {
		t.Fatalf("G4 picking list SKU sums=%v, want skuA=6 skuB=3", qtyBySKU)
	}

	// 2. Batch ship: mixing 1 foreign order -> 403 whole batch; sending 4 CONFIRMED + 1 CANCELLED -> 4 QUEUED + 1 SKIPPED(ORDER_CANCELLED).
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/orders/batch-ship", seller1.AccessToken, nil, map[string]any{
		"order_ids":    []string{ord1.ID, ord2.ID, foreignOrd.ID},
		"carrier_code": "mock_express",
		"service_code": "STANDARD",
	}, http.StatusForbidden)

	batchResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/orders/batch-ship", seller1.AccessToken, nil, map[string]any{
		"order_ids":    []string{ord1.ID, ord2.ID, ord3.ID, ord4.ID, ord5.ID},
		"carrier_code": "mock_express",
		"service_code": "STANDARD",
	}, http.StatusOK)
	var batchOut w3b46BatchShipResponse
	if err := DecodeJSON(batchResp, &batchOut); err != nil {
		t.Fatalf("decode batch-ship: %v", err)
	}
	if batchOut.QueuedCount != 4 || batchOut.SkippedCount != 1 || len(batchOut.Results) != 5 {
		t.Fatalf("G4 batch-ship counts queued=%d skipped=%d results=%d, want 4/1/5 (body=%s)",
			batchOut.QueuedCount, batchOut.SkippedCount, len(batchOut.Results), string(batchResp.Body))
	}
	resByOrder := make(map[string]w3b46BatchShipResultDTO)
	for _, r := range batchOut.Results {
		resByOrder[r.OrderID] = r
	}
	for _, oid := range []string{ord1.ID, ord2.ID, ord3.ID, ord4.ID} {
		r := resByOrder[oid]
		if !StatusEquals(r.Outcome, "QUEUED") || r.ShipmentJobID == "" {
			t.Fatalf("G4 order %s expected QUEUED with job_id, got %+v", oid, r)
		}
	}
	if r5 := resByOrder[ord5.ID]; !StatusEquals(r5.Outcome, "SKIPPED") || r5.ReasonCode != "ORDER_CANCELLED" {
		t.Fatalf("G4 cancelled order %s expected SKIPPED/ORDER_CANCELLED, got %+v", ord5.ID, r5)
	}

	// Wait for A5 worker to materialize all 4 forward shipments.
	activeIDs := []string{ord1.ID, ord2.ID, ord3.ID, ord4.ID}
	trackingCodes := make([]string, 0, 4)
	for _, oid := range activeIDs {
		var trk string
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "batch-ship order "+oid+" tracking_code", func() (bool, string) {
			cur := env.getOrder(t, seller1.AccessToken, oid)
			if cur.TrackingCode != "" && StatusEquals(cur.Status, "PREPARING") && cur.ShipmentJobStatus == "" {
				trk = cur.TrackingCode
				return true, ""
			}
			return false, fmt.Sprintf("status=%s trk=%q job=%q", cur.Status, cur.TrackingCode, cur.ShipmentJobStatus)
		})
		trackingCodes = append(trackingCodes, trk)
	}
	// Verify cancelled order #5 remained CANCELLED with no tracking code.
	if o5 := env.getOrder(t, seller1.AccessToken, ord5.ID); !StatusEquals(o5.Status, "CANCELLED") || o5.TrackingCode != "" {
		t.Fatalf("G4 cancelled order #5 unexpectedly mutated: status=%s trk=%q", o5.Status, o5.TrackingCode)
	}

	// 3. Batch PDF A6 labels (POST /api/v1/shipping/labels/batch, G5 + shipping-r0 ca #3).
	// Validation: 0 codes or 51 codes -> 400; unknown code -> 404.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", seller1.AccessToken, nil, map[string]any{
		"tracking_codes": []string{},
	}, http.StatusBadRequest)
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("MOCK-OVER-%02d", i)
	}
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", seller1.AccessToken, nil, map[string]any{
		"tracking_codes": tooMany,
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", seller1.AccessToken, nil, map[string]any{
		"tracking_codes": []string{"MOCK-DOES-NOT-EXIST-" + env.runID},
	}, http.StatusNotFound)

	// RBAC: foreign shop code mixed in -> 403 whole batch; buyer -> 403; service token -> 401/403.
	mixedCodes := append(append([]string(nil), trackingCodes...), foreignShipOut.TrackingCode)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", seller1.AccessToken, nil, map[string]any{
		"tracking_codes": mixedCodes,
	}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", buyer.AccessToken, nil, map[string]any{
		"tracking_codes": trackingCodes,
	}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/labels/batch", promoMintServiceToken("order-service"), nil, map[string]any{
		"tracking_codes": trackingCodes,
	}, http.StatusUnauthorized, http.StatusForbidden)

	// Valid 4-label batch PDF (even with ?format=html -> still PDF per G5!).
	pdfPayload, _ := json.Marshal(map[string]any{"tracking_codes": trackingCodes})
	pdfStatus, pdfHdr, pdfBody := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/shipping/labels/batch?format=html", seller1.AccessToken, "application/json", pdfPayload)
	if pdfStatus != http.StatusOK {
		t.Fatalf("G5 batch PDF returned HTTP %d: %s", pdfStatus, string(pdfBody))
	}
	if ct := strings.ToLower(pdfHdr.Get("Content-Type")); !strings.HasPrefix(ct, "application/pdf") {
		t.Fatalf("G5 Content-Type=%q, want application/pdf", pdfHdr.Get("Content-Type"))
	}
	if cd := pdfHdr.Get("Content-Disposition"); !strings.Contains(cd, "shipping-labels-batch.pdf") {
		t.Fatalf("G5 Content-Disposition=%q, want shipping-labels-batch.pdf", cd)
	}
	if nosniff := strings.ToLower(pdfHdr.Get("X-Content-Type-Options")); nosniff != "nosniff" {
		t.Fatalf("G5 X-Content-Type-Options=%q, want nosniff", pdfHdr.Get("X-Content-Type-Options"))
	}
	if cc := strings.ToLower(pdfHdr.Get("Cache-Control")); !strings.Contains(cc, "no-store") {
		t.Fatalf("G5 Cache-Control=%q, want private, no-store", pdfHdr.Get("Cache-Control"))
	}

	pdfInfo := w3b46InspectPDF(t, pdfBody)
	if pdfInfo.PageCount != 4 {
		t.Fatalf("G5 PDF page count=%d, want 4", pdfInfo.PageCount)
	}
	// ISO A6 = 105x148 mm ≈ 297.64 x 419.53 pt.
	if len(pdfInfo.MediaBoxes) == 0 {
		t.Fatalf("G5 PDF missing /MediaBox dimensions")
	}
	for i, box := range pdfInfo.MediaBoxes {
		wPt := box[2] - box[0]
		hPt := box[3] - box[1]
		if math.Abs(wPt-297.64) > 4.0 || math.Abs(hPt-419.53) > 4.0 {
			t.Fatalf("G5 MediaBox[%d]=%v (%.2fx%.2f pt) is not ISO A6 (297.64x419.53 pt)", i, box, wPt, hPt)
		}
	}
	if !pdfInfo.HasFontFile2 || !pdfInfo.HasToUnicode {
		t.Fatalf("G5 PDF missing embedded TrueType font (/FontFile2=%v /ToUnicode=%v)", pdfInfo.HasFontFile2, pdfInfo.HasToUnicode)
	}
	for _, trk := range trackingCodes {
		if !strings.Contains(pdfInfo.ExtractedText, trk) {
			t.Fatalf("G5 PDF decoded text missing tracking code %q", trk)
		}
	}
	if !strings.Contains(pdfInfo.ExtractedText, "Nguyễn Thị Phượng") {
		t.Fatalf("G5 PDF decoded text missing full Vietnamese recipient diacritics 'Nguyễn Thị Phượng'")
	}

	// Admin staff with orders.read can print multi-shop batch PDF (2 pages).
	adminPDFPayload, _ := json.Marshal(map[string]any{"tracking_codes": []string{trackingCodes[0], foreignShipOut.TrackingCode}})
	adminPDFStatus, _, adminPDFBody := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/shipping/labels/batch", adminTok, "application/json", adminPDFPayload)
	if adminPDFStatus != http.StatusOK {
		t.Fatalf("G5 admin multi-shop PDF returned HTTP %d: %s", adminPDFStatus, string(adminPDFBody))
	}
	if info := w3b46InspectPDF(t, adminPDFBody); info.PageCount != 2 {
		t.Fatalf("G5 admin multi-shop PDF page count=%d, want 2", info.PageCount)
	}
}

// =================================================================================================
// 3. UC-S13: Bulk CSV Product Import (G6 + Manager Review Additions)
// =================================================================================================

func TestE2E_W3B3_A8_UC_S13_BulkCSVImport(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, _, seller2 := env.ensureBaseMarketplace(t)

	// Dedicated seller guarantees exact PENDING product count (490) across repeated runs.
	importSeller := env.createApprovedSeller(t, "w3b46-csv-seller")

	// Pre-create 1 product in importSeller to test duplicate SKU against existing shop catalog.
	_, existingSKU := env.createApprovedProduct(t, importSeller, cat.ID, "W3B3 Existing SKU Product "+env.runID, 120000, 10, 250)

	// Temporary BLOCK banned keyword (auto-deleted in t.Cleanup via w3b41CreateBannedKeyword).
	bannedTag := "camcsv" + w3b41AlphaTag(env)
	w3b41CreateBannedKeyword(t, env, adminTok, bannedTag, "e2e-csv", "BLOCK")

	// 1. Download CSV template (UTF-8 BOM + required headers).
	tplStatus, tplHdr, tplBody := w3b42RawHTTP(env.ctx(t), t, http.MethodGet,
		env.cfg.GatewayURL+"/api/v1/seller/products/import/template?category_id="+url.QueryEscape(cat.ID),
		importSeller.AccessToken, "", nil)
	if tplStatus != http.StatusOK {
		t.Fatalf("G6 template returned HTTP %d: %s", tplStatus, string(tplBody))
	}
	if !strings.Contains(strings.ToLower(tplHdr.Get("Content-Type")), "text/csv") {
		t.Fatalf("G6 template Content-Type=%q, want text/csv", tplHdr.Get("Content-Type"))
	}
	if !bytes.HasPrefix(tplBody, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatalf("G6 template missing UTF-8 BOM")
	}
	if !strings.Contains(string(tplBody), "product_name,sku_code,category_id,price_vnd,stock") {
		t.Fatalf("G6 template missing required columns: %s", string(tplBody))
	}

	// 2. Synchronous whole-file rejections: bad headers & 1,001 rows -> 400.
	badHdrStatus, _, badHdrBody := w3b42RawHTTP(env.ctx(t), t, http.MethodPost,
		env.cfg.GatewayURL+"/api/v1/seller/products/import", importSeller.AccessToken, "text/csv",
		[]byte("wrong_a,wrong_b\nval1,val2\n"))
	if badHdrStatus != http.StatusBadRequest {
		t.Fatalf("G6 bad header want 400, got %d: %s", badHdrStatus, string(badHdrBody))
	}
	w3b42AssertRawErrorCode(t, "CSV bad header", badHdrBody, "IMPORT_INVALID_HEADER")

	var tooManyBuf bytes.Buffer
	tooManyBuf.WriteString("product_name,sku_code,category_id,price_vnd,stock\n")
	for i := 1; i <= 1001; i++ {
		fmt.Fprintf(&tooManyBuf, "SP Over %d,OVR-%s-%04d,%s,100000,10\n", i, env.runID, i, cat.ID)
	}
	overStatus, _, overBody := w3b42RawHTTP(env.ctx(t), t, http.MethodPost,
		env.cfg.GatewayURL+"/api/v1/seller/products/import", importSeller.AccessToken, "text/csv", tooManyBuf.Bytes())
	if overStatus != http.StatusBadRequest {
		t.Fatalf("G6 1001 rows want 400, got %d: %s", overStatus, string(overBody))
	}
	w3b42AssertRawErrorCode(t, "CSV 1001 rows", overBody, "IMPORT_TOO_MANY_ROWS")

	// 3. Manager review additions:
	//    (a) Row containing JSON \u0000 in attributes must NOT stall the job (reaches READY cleanly).
	//    (b) Long accented Vietnamese filename must preserve valid UTF-8 (no mid-rune byte truncation).
	t.Run("NullEscapeInAttributes_And_LongAccentedFilenameUTF8", func(t *testing.T) {
		var nulCSV bytes.Buffer
		nulCSV.Write([]byte{0xef, 0xbb, 0xbf})
		cw := csv.NewWriter(&nulCSV)
		_ = cw.Write([]string{"product_name", "sku_code", "category_id", "price_vnd", "stock", "weight_grams", "attributes"})
		_ = cw.Write([]string{"SP Kiểm Thử Null " + env.runID, env.uniqueCode("NUL"), cat.ID, "150000", "5", "300", `{"note":"abc\u0000def"}`})
		cw.Flush()

		// Long Vietnamese accented filename (~210 bytes UTF-8).
		longVietName := strings.Repeat("bảng-nhập-sản-phẩm-việt-", 6) + ".csv"
		st, _, body := w3b46UploadMultipartCSV(t, env, importSeller.AccessToken, longVietName, nulCSV.Bytes())
		if st != http.StatusAccepted {
			t.Fatalf("G6 \\u0000 + long accented filename upload want 202, got %d: %s", st, string(body))
		}
		var jobOut w3b46ImportJobResponse
		if err := json.Unmarshal(body, &jobOut); err != nil || jobOut.Job.ID == "" {
			t.Fatalf("decode \\u0000 import job: err=%v body=%s", err, string(body))
		}
		if !utf8.ValidString(jobOut.Job.Filename) {
			t.Fatalf("G6 accented filename corrupted UTF-8: %q", jobOut.Job.Filename)
		}
		// Poll until job reaches READY (must NOT get stuck in VALIDATING or FAILED).
		env.pollUntil(t, 20*time.Second, 200*time.Millisecond, "\\u0000 job reaches READY", func() (bool, string) {
			resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobOut.Job.ID), importSeller.AccessToken, nil, nil, http.StatusOK)
			var cur w3b46ImportJobResponse
			if err := DecodeJSON(resp, &cur); err != nil {
				return false, err.Error()
			}
			if StatusEquals(cur.Job.Status, "FAILED") {
				t.Fatalf("G6 \\u0000 in attributes caused job FAILED: %s", cur.Job.ErrorMessage)
			}
			return StatusEquals(cur.Job.Status, "READY"), cur.Job.Status
		})

		// Direct gRPC check with >255-byte multi-byte Vietnamese filename: must never produce invalid UTF-8.
		prodGRPC := w3b46DialProductGRPC(t, env)
		overLongName := strings.Repeat("áo-thun-đẹp-tiếng-việt-", 12) + ".csv" // >300 bytes
		if len(overLongName) <= 255 {
			t.Fatalf("test setup error: expected >255 bytes, got %d", len(overLongName))
		}
		var tinyCSV bytes.Buffer
		tinyCSV.WriteString("product_name,sku_code,category_id,price_vnd,stock\n")
		fmt.Fprintf(&tinyCSV, "SP UTF8 %s,%s,%s,100000,5\n", env.runID, env.uniqueCode("UTF"), cat.ID)
		gResp, gErr := prodGRPC.CreateProductImportJob(w3b45AuthCtx(env.ctx(t), importSeller.AccessToken), &productv1.CreateProductImportJobRequest{
			Filename:   overLongName,
			CsvContent: tinyCSV.Bytes(),
		})
		if gErr == nil {
			if !utf8.ValidString(gResp.GetJob().GetFilename()) {
				t.Fatalf("G6 >255-byte Vietnamese filename truncated mid-rune (invalid UTF-8): %x", []byte(gResp.GetJob().GetFilename()))
			}
			// Wait for this small validation job to leave active status before main 500-row upload.
			env.pollUntil(t, 15*time.Second, 200*time.Millisecond, "utf8 test job reaches READY", func() (bool, string) {
				r := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(gResp.GetJob().GetId()), importSeller.AccessToken, nil, nil, http.StatusOK)
				var cur w3b46ImportJobResponse
				_ = DecodeJSON(r, &cur)
				return StatusEquals(cur.Job.Status, "READY") || StatusEquals(cur.Job.Status, "FAILED"), cur.Job.Status
			})
		} else if status.Code(gErr) != codes.InvalidArgument {
			t.Fatalf("G6 >255-byte Vietnamese filename unexpected gRPC error: %v", gErr)
		}
	})

	// 4. Main 500-row CSV (490 valid + 10 invalid incl. formula injection "=CMD|...").
	var csvBuf bytes.Buffer
	csvBuf.Write([]byte{0xef, 0xbb, 0xbf})
	w := csv.NewWriter(&csvBuf)
	_ = w.Write([]string{"product_name", "sku_code", "category_id", "price_vnd", "stock", "weight_grams", "description", "color", "size"})

	firstValidSKU := fmt.Sprintf("CSV%s-%04d", strings.ToUpper(env.runID), 1)
	for i := 1; i <= 490; i++ {
		sku := fmt.Sprintf("CSV%s-%04d", strings.ToUpper(env.runID), i)
		price := strconv.FormatInt(100000+int64(i)*1000, 10)
		_ = w.Write([]string{
			fmt.Sprintf("Sản phẩm nhập lô %s #%03d", env.runID, i),
			sku,
			cat.ID,
			price,
			"20",
			"300",
			"Mô tả tiếng Việt đầy đủ dấu",
			"Đen",
			"M",
		})
	}
	// 10 invalid rows covering all failure modes in G6 (including >500-rune name -> INVALID_PRODUCT_TEXT):
	overLongNameSKU := env.uniqueCode("LONG")
	emptyNameSKU := env.uniqueCode("EMPT")
	invalidRows := [][]string{
		{"SP Giá Âm", env.uniqueCode("BAD"), cat.ID, "-50000", "10", "300", "desc", "Đen", "M"},
		{strings.Repeat("Á", 505), overLongNameSKU, cat.ID, "120000", "10", "300", "name > 500 runes -> INVALID_PRODUCT_TEXT", "Đen", "M"},
		{"SP Giá Chữ", env.uniqueCode("BAD"), cat.ID, "not-a-number", "10", "300", "desc", "Đen", "M"},
		{"SP Sai Danh Mục", env.uniqueCode("BAD"), "00000000-0000-0000-0000-000000000000", "120000", "10", "300", "desc", "Đen", "M"},
		{"SP Trùng SKU Trong File", firstValidSKU, cat.ID, "120000", "10", "300", "desc", "Đen", "M"},
		{"SP Trùng SKU Trong Shop", existingSKU.SKUCode, cat.ID, "120000", "10", "300", "desc", "Đen", "M"},
		{"SP Chứa Từ Cấm " + bannedTag, env.uniqueCode("BAD"), cat.ID, "120000", "10", "300", "desc", "Đen", "M"},
		{"SP Tồn Kho Âm", env.uniqueCode("BAD"), cat.ID, "120000", "-5", "300", "desc", "Đen", "M"},
		{"=CMD|' /C calc'!A0", env.uniqueCode("BAD"), cat.ID, "-100", "10", "300", "formula injection test", "Đen", "M"},
		{"", emptyNameSKU, cat.ID, "120000", "10", "300", "missing product_name", "Đen", "M"},
	}
	for _, r := range invalidRows {
		_ = w.Write(r)
	}
	w.Flush()

	upStatus, _, upBody := w3b46UploadMultipartCSV(t, env, importSeller.AccessToken, "nhập-500-sản-phẩm.csv", csvBuf.Bytes())
	if upStatus != http.StatusAccepted {
		t.Fatalf("G6 500-row upload want 202, got %d: %s", upStatus, string(upBody))
	}
	var createdJob w3b46ImportJobResponse
	if err := json.Unmarshal(upBody, &createdJob); err != nil || createdJob.Job.ID == "" {
		t.Fatalf("decode 500-row import job: err=%v body=%s", err, string(upBody))
	}
	jobID := createdJob.Job.ID

	// Poll until READY (490 valid / 10 invalid).
	var readyJob w3b46ImportJobDTO
	env.pollUntil(t, 45*time.Second, 250*time.Millisecond, "500-row CSV job READY", func() (bool, string) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID), importSeller.AccessToken, nil, nil, http.StatusOK)
		var out w3b46ImportJobResponse
		if err := DecodeJSON(resp, &out); err != nil {
			return false, err.Error()
		}
		readyJob = out.Job
		return StatusEquals(out.Job.Status, "READY"), fmt.Sprintf("status=%s valid=%d invalid=%d err=%s", out.Job.Status, out.Job.ValidRows, out.Job.InvalidRows, out.Job.ErrorMessage)
	})
	if readyJob.TotalRows != 500 || readyJob.ValidRows != 490 || readyJob.InvalidRows != 10 {
		t.Fatalf("G6 READY counts=%d/%d/%d, want 500/490/10", readyJob.TotalRows, readyJob.ValidRows, readyJob.InvalidRows)
	}

	// Verify per-row error codes via GET /rows?errors_only=true:
	// >500-rune name MUST be INVALID_PRODUCT_TEXT (not INVALID_PRODUCT_NAME); empty name -> INVALID_PRODUCT_NAME.
	errRowsResp := env.mustDoJSON(t, http.MethodGet,
		"/api/v1/seller/products/import/"+url.PathEscape(jobID)+"/rows?errors_only=true&page=1&page_size=20",
		importSeller.AccessToken, nil, nil, http.StatusOK)
	var errRowsOut struct {
		Items []struct {
			SKUCode   string `json:"sku_code"`
			Status    string `json:"status"`
			ErrorCode string `json:"error_code"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := DecodeJSON(errRowsResp, &errRowsOut); err != nil || errRowsOut.Total != 10 {
		t.Fatalf("G6 GET /rows?errors_only=true: err=%v total=%d body=%s", err, errRowsOut.Total, string(errRowsResp.Body))
	}
	errCodeBySKU := make(map[string]string, len(errRowsOut.Items))
	for _, r := range errRowsOut.Items {
		errCodeBySKU[r.SKUCode] = r.ErrorCode
	}
	if got := errCodeBySKU[overLongNameSKU]; got != "INVALID_PRODUCT_TEXT" {
		t.Fatalf("G6 >500-rune product_name error_code=%q, want INVALID_PRODUCT_TEXT (rows=%+v)", got, errRowsOut.Items)
	}
	if got := errCodeBySKU[emptyNameSKU]; got != "INVALID_PRODUCT_NAME" {
		t.Fatalf("G6 empty product_name error_code=%q, want INVALID_PRODUCT_NAME (rows=%+v)", got, errRowsOut.Items)
	}

	// Cross-shop RBAC: seller2 cannot read, export errors, or confirm importSeller's job -> 403.
	env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID), seller2.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID)+"/rows", seller2.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID)+"/errors.csv", seller2.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products/import/"+url.PathEscape(jobID)+"/confirm", seller2.AccessToken, nil, nil, http.StatusForbidden)

	// Download errors.csv: 10 error rows and formula cell prefixed with '.
	errCSVStatus, errCSVHdr, errCSVBody := w3b42RawHTTP(env.ctx(t), t, http.MethodGet,
		env.cfg.GatewayURL+"/api/v1/seller/products/import/"+url.PathEscape(jobID)+"/errors.csv",
		importSeller.AccessToken, "", nil)
	if errCSVStatus != http.StatusOK {
		t.Fatalf("G6 errors.csv returned HTTP %d: %s", errCSVStatus, string(errCSVBody))
	}
	if !strings.Contains(strings.ToLower(errCSVHdr.Get("Content-Type")), "text/csv") {
		t.Fatalf("G6 errors.csv Content-Type=%q, want text/csv", errCSVHdr.Get("Content-Type"))
	}
	trimmedErrCSV := bytes.TrimPrefix(errCSVBody, []byte{0xef, 0xbb, 0xbf})
	errRecords, parseErr := csv.NewReader(bytes.NewReader(trimmedErrCSV)).ReadAll()
	if parseErr != nil {
		t.Fatalf("parse errors.csv: %v", parseErr)
	}
	if len(errRecords) != 11 { // 1 header + 10 error rows
		t.Fatalf("G6 errors.csv row count=%d (incl header), want 11", len(errRecords))
	}
	foundEscapedFormula := false
	for _, rec := range errRecords[1:] {
		for _, cell := range rec {
			if strings.Contains(cell, "CMD|") {
				if !strings.HasPrefix(cell, "'=CMD") {
					t.Fatalf("G6 formula injection cell not prefixed with ': got %q", cell)
				}
				foundEscapedFormula = true
			}
		}
	}
	if !foundEscapedFormula {
		t.Fatalf("G6 errors.csv missing formula injection error row (=CMD|...)")
	}

	// 5. Concurrent double confirm + second import job while first is CONFIRMING -> 409 IMPORT_JOB_IN_PROGRESS.
	confirmPath := "/api/v1/seller/products/import/" + url.PathEscape(jobID) + "/confirm"
	startConfirm := make(chan struct{})
	confirmOuts := make([]w3b46ImportJobResponse, 2)
	var cwg sync.WaitGroup
	for i := 0; i < 2; i++ {
		cwg.Add(1)
		go func(idx int) {
			defer cwg.Done()
			<-startConfirm
			resp := env.mustDoJSON(t, http.MethodPost, confirmPath, importSeller.AccessToken, nil, nil, http.StatusOK)
			var out w3b46ImportJobResponse
			if err := DecodeJSON(resp, &out); err != nil {
				t.Errorf("decode concurrent confirm #%d: %v", idx, err)
				return
			}
			confirmOuts[idx] = out
		}(i)
	}
	close(startConfirm)
	cwg.Wait()

	// Immediately attempt a second import job while the 490-row job is CONFIRMING (manager review addition).
	var concurrentJobCSV bytes.Buffer
	concurrentJobCSV.WriteString("product_name,sku_code,category_id,price_vnd,stock\n")
	fmt.Fprintf(&concurrentJobCSV, "SP Second Job %s,%s,%s,100000,5\n", env.runID, env.uniqueCode("SEC"), cat.ID)
	secStatus, _, secBody := w3b42RawHTTP(env.ctx(t), t, http.MethodPost,
		env.cfg.GatewayURL+"/api/v1/seller/products/import", importSeller.AccessToken, "text/csv", concurrentJobCSV.Bytes())
	if secStatus == http.StatusConflict {
		w3b42AssertRawErrorCode(t, "second import job while first active", secBody, "IMPORT_JOB_IN_PROGRESS")
	} else {
		// Only permissible if the 490-row job already finished COMPLETED before the HTTP request arrived.
		chkResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID), importSeller.AccessToken, nil, nil, http.StatusOK)
		var chk w3b46ImportJobResponse
		_ = DecodeJSON(chkResp, &chk)
		if !StatusEquals(chk.Job.Status, "COMPLETED") {
			t.Fatalf("G6 second import job while first is %q returned HTTP %d (want 409 IMPORT_JOB_IN_PROGRESS): %s",
				chk.Job.Status, secStatus, string(secBody))
		}
	}

	// Sequential repeat confirm MUST return 200 with already_confirmed=true.
	repResp := env.mustDoJSON(t, http.MethodPost, confirmPath, importSeller.AccessToken, nil, nil, http.StatusOK)
	var repOut w3b46ImportJobResponse
	if err := DecodeJSON(repResp, &repOut); err != nil || !repOut.AlreadyConfirmed {
		t.Fatalf("G6 repeat confirm expected already_confirmed=true, got err=%v out=%+v", err, repOut)
	}

	// Poll until COMPLETED (490 created, 0 failed).
	var doneJob w3b46ImportJobDTO
	env.pollUntil(t, 90*time.Second, 300*time.Millisecond, "500-row CSV job COMPLETED", func() (bool, string) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/import/"+url.PathEscape(jobID), importSeller.AccessToken, nil, nil, http.StatusOK)
		var out w3b46ImportJobResponse
		if err := DecodeJSON(resp, &out); err != nil {
			return false, err.Error()
		}
		doneJob = out.Job
		return StatusEquals(out.Job.Status, "COMPLETED"), fmt.Sprintf("status=%s created=%d failed=%d", out.Job.Status, out.Job.CreatedRows, out.Job.FailedRows)
	})
	if doneJob.CreatedRows != 490 || doneJob.FailedRows != 0 {
		t.Fatalf("G6 COMPLETED counts created=%d failed=%d, want 490/0", doneJob.CreatedRows, doneJob.FailedRows)
	}

	// Verify seller catalog contains EXACTLY 490 PENDING products (0 duplicates).
	pendListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products?status=PENDING&page=1&page_size=20", importSeller.AccessToken, nil, nil, http.StatusOK)
	var pendList struct {
		Items []ProductDTO `json:"items"`
		Total int64        `json:"total"`
	}
	if err := DecodeJSON(pendListResp, &pendList); err != nil {
		t.Fatalf("decode seller PENDING products: %v", err)
	}
	if pendList.Total != 490 {
		t.Fatalf("G6 seller PENDING product total=%d, want exactly 490 (no duplicates)", pendList.Total)
	}
}

// =================================================================================================
// 4. S03 (Stock History), S04 (Seller Order Pagination + status_counts), B15 (Removed Vouchers) (G8)
// =================================================================================================

func TestE2E_W3B3_A8_S03_S04_B15_StockHistoryPaginationAndVouchers(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	w3b46EnsureKnownShippingAndPaymentState(t, env, adminTok, seller1, seller2)
	buyer := env.ensureVerifiedBuyer(t)

	// --- S03: Stock History (MANUAL_UPDATE / RESERVE / RELEASE + cross-shop 403) ---
	t.Run("S03_StockHistory_AuditAndRBAC", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B3 S03 Stock Ledger "+env.runID, 140000, 50, 300)

		// 1) MANUAL_UPDATE: 50 -> 42.
		updResp := env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/seller/skus/%s/stock", url.PathEscape(sku.ID)), seller1.AccessToken, nil, map[string]any{
			"new_stock":        42,
			"expected_version": sku.Version,
		}, http.StatusOK)
		if updResp.StatusCode != http.StatusOK {
			t.Fatalf("manual stock update failed")
		}

		// 2) RESERVE via VietQR PENDING checkout (qty 2), then 3) RELEASE via buyer cancel.
		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b46-s03-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     buyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "mock_express", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated)
		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 1 {
			t.Fatalf("S03 VietQR checkout: err=%v body=%s", err, string(coResp.Body))
		}
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(co.Orders[0].ID)), buyer.AccessToken, nil, map[string]string{
			"reason": "S03 test stock release",
		}, http.StatusOK)

		histPath := fmt.Sprintf("/api/v1/seller/skus/%s/stock-history?page=1&page_size=50", url.PathEscape(sku.ID))
		var histOut w3b46StockHistoryResponse
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "stock history contains MANUAL_UPDATE, RESERVE, RELEASE", func() (bool, string) {
			resp := env.mustDoJSON(t, http.MethodGet, histPath, seller1.AccessToken, nil, nil, http.StatusOK)
			if err := DecodeJSON(resp, &histOut); err != nil {
				return false, err.Error()
			}
			seen := make(map[string]bool)
			for _, it := range histOut.Items {
				seen[strings.ToUpper(it.ChangeType)] = true
			}
			return seen["MANUAL_UPDATE"] && seen["RESERVE"] && seen["RELEASE"], fmt.Sprintf("seen=%v", seen)
		})

		// Filter by change_type=MANUAL_UPDATE.
		fResp := env.mustDoJSON(t, http.MethodGet, histPath+"&change_type=MANUAL_UPDATE", seller1.AccessToken, nil, nil, http.StatusOK)
		var fOut w3b46StockHistoryResponse
		if err := DecodeJSON(fResp, &fOut); err != nil || len(fOut.Items) == 0 {
			t.Fatalf("S03 filtered MANUAL_UPDATE failed: err=%v body=%s", err, string(fResp.Body))
		}
		for _, it := range fOut.Items {
			if it.ChangeType != "MANUAL_UPDATE" || it.StockBefore != 50 || it.StockAfter != 42 || it.Delta != -8 {
				t.Fatalf("S03 MANUAL_UPDATE entry mismatch: %+v", it)
			}
		}

		// Cross-shop RBAC -> 403.
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/seller/skus/%s/stock-history", url.PathEscape(sku.ID)), seller2.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/stock-history?sku_id="+url.QueryEscape(sku.ID), seller2.AccessToken, nil, nil, http.StatusForbidden)
	})

	// --- S04: 25 Seller Orders -> Server Pagination page=1/2 + status_counts ---
	t.Run("S04_SellerOrderPagination_And_StatusCounts", func(t *testing.T) {
		pageSeller := env.createApprovedSeller(t, "w3b46-s04-seller")
		_, sku := env.createApprovedProduct(t, pageSeller, cat.ID, "W3B3 S04 Paginated Item "+env.runID, 100000, 200, 300)

		orderIDs := make([]string, 0, 25)
		for i := 0; i < 25; i++ {
			co := w3b42PlaceCODOrder(t, env, buyer, buyer.AddressID, pageSeller, sku, "mock_express", "STANDARD")
			orderIDs = append(orderIDs, co.Orders[0].ID)
		}
		// Transition 3 -> PREPARING and 2 -> CANCELLED (leaving 20 CONFIRMED).
		for i := 0; i < 3; i++ {
			env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(orderIDs[i])), pageSeller.AccessToken, nil, nil, http.StatusOK)
		}
		for i := 3; i < 5; i++ {
			env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(orderIDs[i])), buyer.AccessToken, nil, map[string]string{
				"reason": "S04 status count test",
			}, http.StatusOK)
		}

		p1Resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/orders?page=1&page_size=20&include_status_counts=true", pageSeller.AccessToken, nil, nil, http.StatusOK)
		var p1 w3b46SellerOrdersResponse
		if err := DecodeJSON(p1Resp, &p1); err != nil {
			t.Fatalf("decode seller orders page 1: %v", err)
		}
		if len(p1.Items) != 20 || p1.Total != 25 || p1.Page != 1 || p1.PageSize != 20 {
			t.Fatalf("S04 page 1 mismatch: len=%d total=%d page=%d size=%d", len(p1.Items), p1.Total, p1.Page, p1.PageSize)
		}
		counts := make(map[string]int64)
		for _, sc := range p1.StatusCounts {
			counts[NormalizeStatus(sc.Status)] = sc.Count
		}
		if counts["CONFIRMED"] != 20 || counts["PREPARING"] != 3 || counts["CANCELLED"] != 2 {
			t.Fatalf("S04 status_counts=%v, want CONFIRMED=20 PREPARING=3 CANCELLED=2", counts)
		}

		p2Resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/orders?page=2&page_size=20&include_status_counts=true", pageSeller.AccessToken, nil, nil, http.StatusOK)
		var p2 w3b46SellerOrdersResponse
		if err := DecodeJSON(p2Resp, &p2); err != nil {
			t.Fatalf("decode seller orders page 2: %v", err)
		}
		if len(p2.Items) != 5 || p2.Total != 25 || p2.Page != 2 {
			t.Fatalf("S04 page 2 mismatch: len=%d total=%d page=%d", len(p2.Items), p2.Total, p2.Page)
		}
		seenP1 := make(map[string]bool, len(p1.Items))
		for _, o := range p1.Items {
			seenP1[o.ID] = true
		}
		for _, o := range p2.Items {
			if seenP1[o.ID] {
				t.Fatalf("S04 pagination overlap: order %s appeared on both page 1 and page 2", o.ID)
			}
		}
	})

	// --- B15: Legacy Tier/Promo Vouchers EDU15, SINHNHAT50, NEXUSVIP12 Removed ---
	t.Run("B15_LegacyVouchersRemoved", func(t *testing.T) {
		forbidden := map[string]bool{
			"EDU15":      true,
			"SINHNHAT50": true,
			"NEXUSVIP12": true,
		}
		for _, tc := range []struct {
			name  string
			path  string
			token string
		}{
			{"public_vouchers", "/api/v1/vouchers?sales_channel=MARKETPLACE", ""},
			{"admin_vouchers_all", "/api/v1/admin/vouchers?include_inactive=true", adminTok},
		} {
			resp := env.mustDoJSON(t, http.MethodGet, tc.path, tc.token, nil, nil, http.StatusOK)
			var out VouchersListResponse
			if err := DecodeJSON(resp, &out); err != nil {
				t.Fatalf("decode %s: %v", tc.name, err)
			}
			for _, v := range out.Vouchers {
				code := strings.ToUpper(strings.TrimSpace(v.Code))
				if forbidden[code] && v.IsActive {
					t.Fatalf("B15 forbidden voucher %q still present & active in %s: %+v", code, tc.name, v)
				}
				if tc.name == "public_vouchers" && forbidden[code] {
					t.Fatalf("B15 forbidden voucher %q still returned by public GET /vouchers: %+v", code, v)
				}
			}
		}
	})
}

// =================================================================================================
// 5. UC-B08: Public Guest Order Lookup & Dual Rate Limits (G7 + Manager Review Additions)
//
// IMPORTANT: Gateway only trusts X-Forwarded-For from TRUSTED_PROXIES=web (not the e2e container),
// so the 10/min/IP limit exhausts the e2e container's real IP bucket. This test is intentionally
// named with `_Z_` and placed LAST in 46_ (and 47_ never calls /api/v1/orders/lookup) so earlier
// and later tests are completely unaffected.
// =================================================================================================

func TestE2E_W3B3_A8_Z_UC_B08_GuestOrderLookupAndRateLimits(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	w3b46EnsureKnownShippingAndPaymentState(t, env, adminTok, seller1)
	buyer := env.ensureVerifiedBuyer(t)

	prov, ward := env.ensureProvinceAndWard(t)
	secretStreet := "Số 404 Ngõ Kín Đáo " + env.runID
	lookupAddr := w3b46CreateAddress(t, env, buyer, "Phạm Minh Tuấn", buyer.Phone, prov, ward, secretStreet)
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B3 B08 Lookup Item "+env.runID, 160000, 50, 300)

	coA := w3b42PlaceCODOrder(t, env, buyer, lookupAddr.ID, seller1, sku, "mock_express", "STANDARD")
	ordA := coA.Orders[0]
	coB := w3b42PlaceCODOrder(t, env, buyer, lookupAddr.ID, seller1, sku, "mock_express", "STANDARD")
	ordB := coB.Orders[0]

	// Ship ordA so tracking_code & label_url exist in DB (verifying label_url is NOT leaked in lookup).
	env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordA.ID)), seller1.AccessToken, nil, SellerShipRequest{
		CarrierCode: "mock_express", ServiceCode: "STANDARD",
	}, http.StatusOK)

	// GET /api/v1/orders/lookup must be rejected with 405 (phone must never travel in URL/access logs).
	env.mustDoJSON(t, http.MethodGet, "/api/v1/orders/lookup?order_code="+url.QueryEscape(ordA.OrderCode), "", nil, nil, http.StatusMethodNotAllowed)

	lookupRaw := func(rawBody []byte) (int, http.Header, []byte) {
		t.Helper()
		return w3b42RawHTTPNoRetry(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/orders/lookup", "", "application/json", rawBody)
	}
	lookupJSON := func(code, phone string) (int, http.Header, []byte) {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"order_code": code, "phone": phone})
		return lookupRaw(b)
	}

	totalIPCalls := 0

	// Call #1 (IP 1/10, codeA 1/5): Valid (order_code, phone) -> 200, strictly PII-safe & fee-free.
	c1, _, b1 := lookupJSON(ordA.OrderCode, buyer.Phone)
	totalIPCalls++
	if c1 != http.StatusOK {
		t.Fatalf("G7 valid lookup want 200, got %d: %s", c1, string(b1))
	}
	var out1 w3b46LookupOrderResponse
	if err := json.Unmarshal(b1, &out1); err != nil {
		t.Fatalf("decode lookup response: %v (body=%s)", err, string(b1))
	}
	if out1.Order.OrderCode != ordA.OrderCode || out1.Order.TrackingCode == "" || out1.Order.ProvinceName == "" || out1.Order.WardName == "" {
		t.Fatalf("G7 lookup missing expected summary fields: %+v", out1.Order)
	}
	if out1.Order.RecipientNameMasked == "" || out1.Order.RecipientPhoneMasked == "" {
		t.Fatalf("G7 lookup missing masked recipient fields: %+v", out1.Order)
	}
	// Strict non-leakage checks on raw JSON body and object keys.
	rawStr1 := string(b1)
	for _, forbiddenSubstr := range []string{
		secretStreet,
		buyer.Phone,
		buyer.Email,
		buyer.UserID,
		"Phạm Minh Tuấn",
	} {
		if forbiddenSubstr != "" && strings.Contains(rawStr1, forbiddenSubstr) {
			t.Fatalf("G7 lookup leaked sensitive value %q in body=%s", forbiddenSubstr, rawStr1)
		}
	}
	var rawMap struct {
		Order map[string]any `json:"order"`
	}
	if err := json.Unmarshal(b1, &rawMap); err != nil {
		t.Fatalf("decode raw lookup map: %v", err)
	}
	for _, forbiddenKey := range []string{
		"street_address", "email", "label_url", "buyer_id",
		"commission_vnd", "payment_fee_vnd", "payment_fee_bps",
	} {
		if _, exists := rawMap.Order[forbiddenKey]; exists {
			t.Fatalf("G7 lookup response must omit %q, got body=%s", forbiddenKey, rawStr1)
		}
	}

	// Call #2 (IP 2/10, codeA 2/5): Valid code + WRONG phone -> 404 ORDER_LOOKUP_NOT_FOUND.
	c2, _, b2 := lookupJSON(ordA.OrderCode, "0999999999")
	totalIPCalls++
	if c2 != http.StatusNotFound {
		t.Fatalf("G7 wrong phone want 404, got %d: %s", c2, string(b2))
	}
	var errWrongPhone w3b41ErrorEnvelope
	if err := json.Unmarshal(b2, &errWrongPhone); err != nil || errWrongPhone.Error.Code != "ORDER_LOOKUP_NOT_FOUND" {
		t.Fatalf("G7 wrong phone expected ORDER_LOOKUP_NOT_FOUND, got %s", string(b2))
	}

	// Call #3 (IP 3/10, codeA 3/5): Trailing garbage after JSON (`{...} x`) MUST still be counted toward codeA's 5/10min bucket!
	trailingPayload := []byte(fmt.Sprintf(`{"order_code":%q,"phone":%q} x`, ordA.OrderCode, "0999999998"))
	c3, _, b3 := lookupRaw(trailingPayload)
	totalIPCalls++
	if c3 == http.StatusTooManyRequests {
		t.Fatalf("G7 call #3 unexpectedly rate limited early: %s", string(b3))
	}

	// Call #4 (IP 4/10, codeA 4/5): Duplicate key + mixed-case JSON key cannot bypass per-code limit.
	dupKeyPayload := []byte(fmt.Sprintf(`{"order_code":"FAKE-BYPASS","ORDER_CODE":%q,"phone":"0999999997"}`, strings.ToLower(ordA.OrderCode)))
	c4, _, b4 := lookupRaw(dupKeyPayload)
	totalIPCalls++
	if c4 == http.StatusOK {
		t.Fatalf("G7 duplicate-key wrong phone unexpectedly succeeded: %s", string(b4))
	}

	// Call #5 (IP 5/10, codeA 5/5): Lowercase + padded order_code counts in the SAME normalized bucket.
	c5, _, b5 := lookupJSON("  "+strings.ToLower(ordA.OrderCode)+"  ", "0999999996")
	totalIPCalls++
	if c5 != http.StatusNotFound {
		t.Fatalf("G7 call #5 (lowercase codeA, wrong phone) want 404, got %d: %s", c5, string(b5))
	}

	// Call #6 (IP 6/10, codeA 6/5): MUST hit per-code 429 + Retry-After!
	// (If call #3 or #4 failed to count against codeA, this call would return 200 instead of 429.)
	c6, h6, b6 := lookupJSON(ordA.OrderCode, buyer.Phone)
	totalIPCalls++
	if c6 != http.StatusTooManyRequests {
		t.Fatalf("G7 6th lookup for %s MUST return 429 (verifies trailing garbage & duplicate/case keys counted!), got HTTP %d: %s",
			ordA.OrderCode, c6, string(b6))
	}
	if ra6, _ := strconv.Atoi(strings.TrimSpace(h6.Get("Retry-After"))); ra6 < 1 {
		t.Fatalf("G7 per-code 429 missing Retry-After >= 1, got %q", h6.Get("Retry-After"))
	}
	w3b42AssertRawErrorCode(t, "per-code lookup 429", b6, "RATE_LIMIT_EXCEEDED")

	// Call #7 (IP 7/10): Unknown order_code -> identical 404 ORDER_LOOKUP_NOT_FOUND + identical error.message as wrong phone!
	c7, _, b7 := lookupJSON("NM-NONEXISTENT-"+env.runID, buyer.Phone)
	totalIPCalls++
	if c7 != http.StatusNotFound {
		t.Fatalf("G7 unknown code want 404, got %d: %s", c7, string(b7))
	}
	var errUnknownCode w3b41ErrorEnvelope
	if err := json.Unmarshal(b7, &errUnknownCode); err != nil {
		t.Fatalf("decode unknown code error: %v", err)
	}
	if errUnknownCode.Error.Code != errWrongPhone.Error.Code || errUnknownCode.Error.Message != errWrongPhone.Error.Message {
		t.Fatalf("G7 anti-enumeration violated: wrong_phone=%+v vs unknown_code=%+v", errWrongPhone.Error, errUnknownCode.Error)
	}

	// Call #8 (IP 8/10): Distinct order ordB with +84 formatted phone -> 200 (unaffected by ordA's per-code 429).
	intlPhone := "+84 " + strings.TrimPrefix(buyer.Phone, "0")
	c8, _, b8 := lookupJSON(ordB.OrderCode, intlPhone)
	totalIPCalls++
	if c8 != http.StatusOK {
		t.Fatalf("G7 distinct order ordB with +84 phone want 200, got %d: %s", c8, string(b8))
	}

	// Calls #9..#11 (using fresh unused order codes): 11th call within 1 minute from same IP -> 429 + Retry-After!
	hitIP429 := false
	for totalIPCalls < 12 {
		totalIPCalls++
		code, hdr, body := lookupJSON(fmt.Sprintf("NM-IPLIM-%s-%d", env.runID, totalIPCalls), "0900000001")
		if code == http.StatusTooManyRequests {
			if lim, _ := strconv.Atoi(hdr.Get("X-RateLimit-Limit")); lim > 60 {
				// Global IP limit (>60/min) tripped before lookup limit; wait and retry.
				retrySec, _ := strconv.Atoi(hdr.Get("Retry-After"))
				if retrySec < 1 {
					retrySec = 1
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				totalIPCalls--
				continue
			}
			if ra, _ := strconv.Atoi(strings.TrimSpace(hdr.Get("Retry-After"))); ra < 1 {
				t.Fatalf("G7 per-IP 429 missing Retry-After >= 1, got %q", hdr.Get("Retry-After"))
			}
			w3b42AssertRawErrorCode(t, "per-IP lookup 429", body, "RATE_LIMIT_EXCEEDED")
			if totalIPCalls != 11 {
				t.Logf("G7 per-IP 429 triggered at call #%d (budget expected #11)", totalIPCalls)
			}
			hitIP429 = true
			break
		}
		if code != http.StatusNotFound {
			t.Fatalf("G7 IP-limit probe #%d unexpected HTTP %d: %s", totalIPCalls, code, string(body))
		}
	}
	if !hitIP429 {
		t.Fatalf("G7 per-IP 10/min rate limit did not trigger 429 within %d calls", totalIPCalls)
	}
}
