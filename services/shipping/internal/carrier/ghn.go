package carrier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/address"
)

const (
	CodeGHN = "ghn"
	NameGHN = "Giao Hang Nhanh (GHN)"
)

// GHNConfig configures the GHN carrier adapter.
type GHNConfig struct {
	Mode         string // "mock" or "sandbox"
	BaseURL      string
	Token        string
	ShopID       int
	LabelBaseURL string
	HTTPClient   *http.Client
}

// GHNAdapter implements Carrier for Giao Hang Nhanh (sandbox HTTP + deterministic mock mode).
type GHNAdapter struct {
	mu           sync.RWMutex
	mode         string
	baseURL      string
	token        string
	shopID       int
	labelBaseURL string
	client       *http.Client
}

// NewGHNAdapter creates a GHN adapter in mock or sandbox mode.
func NewGHNAdapter(cfg GHNConfig) *GHNAdapter {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "sandbox" {
		mode = "mock"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	labelBase := strings.TrimRight(strings.TrimSpace(cfg.LabelBaseURL), "/")
	if labelBase == "" {
		labelBase = DefaultLabelBaseURL
	}
	return &GHNAdapter{
		mode:         mode,
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		token:        cfg.Token,
		shopID:       cfg.ShopID,
		labelBaseURL: labelBase,
		client:       httpClient,
	}
}

func (g *GHNAdapter) Code() string { return CodeGHN }
func (g *GHNAdapter) Name() string { return NameGHN }
func (g *GHNAdapter) IsMock() bool { return g.mode != "sandbox" }

func (g *GHNAdapter) SetToken(token string) {
	g.mu.Lock()
	g.token = strings.TrimSpace(token)
	g.mu.Unlock()
}

func (g *GHNAdapter) getToken() string {
	g.mu.RLock()
	tok := g.token
	g.mu.RUnlock()
	return tok
}

func (g *GHNAdapter) RequestRedelivery(ctx context.Context, trackingCode, _, _ string) error {
	if g.mode != "sandbox" {
		return nil
	}
	var resp ghnBasicResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/switch-status/storing", ghnCancelPayload{
		OrderCodes: []string{trackingCode},
	}, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return classifyGHNAPIError("redelivery", resp.Code, resp.Message)
	}
	return nil
}

func (g *GHNAdapter) TestConnection(ctx context.Context, token string) error {
	tok := strings.TrimSpace(token)
	if tok == "" {
		tok = g.getToken()
	}
	if g.mode != "sandbox" {
		if IsMockTokenRejected(tok, true) {
			return &NonRetryableError{Err: fmt.Errorf("%w: ghn rejected API token", ErrInvalidCarrierToken)}
		}
		return nil
	}
	if tok == "" {
		return &NonRetryableError{Err: fmt.Errorf("%w: ghn API token is required", ErrInvalidCarrierToken)}
	}
	var resp ghnBasicResponse
	if err := g.doJSONWithToken(ctx, http.MethodGet, "/master-data/province", nil, &resp, tok); err != nil {
		if errors.Is(err, ErrInvalidCarrierToken) {
			return err
		}
		if IsNonRetryable(err) {
			return &NonRetryableError{Err: fmt.Errorf("%w: ghn authentication failed", ErrInvalidCarrierToken)}
		}
		return err
	}
	if resp.Code != 200 {
		if resp.Code == http.StatusRequestTimeout || resp.Code == http.StatusTooManyRequests || resp.Code >= 500 {
			return fmt.Errorf("ghn test connection transient error code=%d", resp.Code)
		}
		return &NonRetryableError{Err: fmt.Errorf("%w: ghn test connection returned code=%d", ErrInvalidCarrierToken, resp.Code)}
	}
	return nil
}

func (g *GHNAdapter) CalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error) {
	if g.mode != "sandbox" {
		return g.mockQuotes(req), nil
	}
	return g.sandboxCalculateFee(ctx, req)
}

func (g *GHNAdapter) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	if g.mode != "sandbox" {
		return g.mockCreateShipment(req), nil
	}
	return g.sandboxCreateShipment(ctx, req)
}

func (g *GHNAdapter) CancelShipment(ctx context.Context, trackingCode string) error {
	if g.mode != "sandbox" {
		return nil
	}
	return g.sandboxCancelShipment(ctx, trackingCode)
}

func (g *GHNAdapter) GetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error) {
	if g.mode != "sandbox" {
		return &TrackingStatus{
			TrackingCode: trackingCode,
			Changed:      false,
		}, nil
	}
	return g.sandboxGetStatus(ctx, trackingCode)
}

func (g *GHNAdapter) mockQuotes(req FeeRequest) []Quote {
	baseFee, stdDays := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 22000, 30000, 38000)
	weightSurcharge := weightFeeVND(req.WeightGrams, 1000, 500, 5000)
	codFee := int64(0)
	if req.CODAmountVND > 0 {
		codFee = 2000
	}
	stdTotal := baseFee + weightSurcharge + codFee
	expDays := stdDays - 1
	if expDays < 1 {
		expDays = 1
	}

	return []Quote{
		{
			CarrierCode:   CodeGHN,
			CarrierName:   NameGHN,
			ServiceCode:   "STANDARD",
			ServiceName:   "GHN Chuẩn",
			FeeVND:        stdTotal,
			EstimatedDays: stdDays,
			IsFallback:    false,
		},
		{
			CarrierCode:   CodeGHN,
			CarrierName:   NameGHN,
			ServiceCode:   "EXPRESS",
			ServiceName:   "GHN Nhanh",
			FeeVND:        stdTotal + 12000,
			EstimatedDays: expDays,
			IsFallback:    false,
		},
	}
}

func (g *GHNAdapter) mockCreateShipment(req ShipmentRequest) *ShipmentResult {
	svc := strings.ToUpper(strings.TrimSpace(req.ServiceCode))
	if svc == "" {
		svc = "STANDARD"
	}
	quotes := g.mockQuotes(FeeRequest{
		FromProvinceCode: req.FromAddress.ProvinceCode,
		ToProvinceCode:   req.ToAddress.ProvinceCode,
		WeightGrams:      req.WeightGrams,
		CODAmountVND:     req.CODAmountVND,
	})
	fee := quotes[0].FeeVND
	for _, q := range quotes {
		if strings.EqualFold(q.ServiceCode, svc) {
			fee = q.FeeVND
			break
		}
	}
	tracking := mockTrackingCode("GHN", req.ClientOrderCode)
	return &ShipmentResult{
		CarrierCode:  CodeGHN,
		ServiceCode:  svc,
		TrackingCode: tracking,
		LabelURL:     fmt.Sprintf("%s/%s", g.labelBaseURL, tracking),
		FeeVND:       fee,
		Status:       StatusCreated,
	}
}

type ghnFeePayload struct {
	ServiceTypeID  int    `json:"service_type_id"`
	FromDistrictID int    `json:"from_district_id"`
	ToDistrictID   int    `json:"to_district_id"`
	ToWardCode     string `json:"to_ward_code"`
	Weight         int32  `json:"weight"`
	InsuranceValue int64  `json:"insurance_value"`
}

type ghnFeeResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Total int64 `json:"total"`
	} `json:"data"`
}

func (g *GHNAdapter) sandboxCalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error) {
	weight := req.WeightGrams
	if weight <= 0 {
		weight = 500
	}
	payload := ghnFeePayload{
		ServiceTypeID:  2, // Standard GHN e-commerce service
		FromDistrictID: req.FromDistrictID,
		ToDistrictID:   req.ToDistrictID,
		ToWardCode:     req.ToGHNWardCode,
		Weight:         weight,
		InsuranceValue: req.CODAmountVND,
	}

	var resp ghnFeeResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/shipping-order/fee", payload, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, classifyGHNAPIError("fee", resp.Code, resp.Message)
	}
	if resp.Data.Total <= 0 {
		return nil, fmt.Errorf("ghn fee api returned non-positive total=%d", resp.Data.Total)
	}

	_, days := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 0, 0, 0)
	return []Quote{
		{
			CarrierCode:   CodeGHN,
			CarrierName:   NameGHN,
			ServiceCode:   "STANDARD",
			ServiceName:   "GHN Chuẩn",
			FeeVND:        resp.Data.Total,
			EstimatedDays: days,
			IsFallback:    false,
		},
	}, nil
}

type ghnCreatePayload struct {
	ClientOrderCode string `json:"client_order_code"`
	ToName          string `json:"to_name"`
	ToPhone         string `json:"to_phone"`
	ToAddress       string `json:"to_address"`
	ToWardCode      string `json:"to_ward_code"`
	ToDistrictID    int    `json:"to_district_id"`
	CODAmount       int64  `json:"cod_amount"`
	Weight          int32  `json:"weight"`
	ServiceTypeID   int    `json:"service_type_id"`
	PaymentTypeID   int    `json:"payment_type_id"`
	RequiredNote    string `json:"required_note"`
	Content         string `json:"content"`
}

type ghnCreateResponse struct {
	Code        int    `json:"code"`
	CodeMessage string `json:"code_message"`
	Message     string `json:"message"`
	Data        struct {
		OrderCode string `json:"order_code"`
		TotalFee  int64  `json:"total_fee"`
	} `json:"data"`
}

func (g *GHNAdapter) sandboxCreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	weight := req.WeightGrams
	if weight <= 0 {
		weight = 500
	}
	clientOrderCode := strings.TrimSpace(req.ClientOrderCode)
	if clientOrderCode == "" {
		clientOrderCode = req.OrderID
	}
	payload := ghnCreatePayload{
		ClientOrderCode: clientOrderCode,
		ToName:          req.ToAddress.Name,
		ToPhone:         req.ToAddress.Phone,
		ToAddress:       req.ToAddress.StreetAddress,
		ToWardCode:      req.ToAddress.GHNWardCode,
		ToDistrictID:    req.ToAddress.GHNDistrictID,
		CODAmount:       req.CODAmountVND,
		Weight:          weight,
		ServiceTypeID:   2,
		PaymentTypeID:   1,
		RequiredNote:    "CHOXEMHANGKHONGTHU",
		Content:         req.ItemsSummary,
	}

	var resp ghnCreateResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/shipping-order/create", payload, &resp); err != nil {
		if IsDuplicateClientOrderCodeMessage(err.Error()) {
			return g.sandboxGetByClientOrderCode(ctx, clientOrderCode, req.ServiceCode)
		}
		return nil, err
	}
	if resp.Code != 200 {
		if IsDuplicateClientOrderCodeMessage(resp.Message) || IsDuplicateClientOrderCodeMessage(resp.CodeMessage) {
			return g.sandboxGetByClientOrderCode(ctx, clientOrderCode, req.ServiceCode)
		}
		return nil, classifyGHNAPIError("create", resp.Code, resp.Message)
	}
	if resp.Data.OrderCode == "" {
		return nil, fmt.Errorf("ghn create api returned empty order_code message=%s", resp.Message)
	}
	svc := req.ServiceCode
	if svc == "" {
		svc = "STANDARD"
	}
	return &ShipmentResult{
		CarrierCode:  CodeGHN,
		ServiceCode:  svc,
		TrackingCode: resp.Data.OrderCode,
		LabelURL:     fmt.Sprintf("%s/%s", g.labelBaseURL, resp.Data.OrderCode),
		FeeVND:       resp.Data.TotalFee,
		Status:       StatusCreated,
	}, nil
}

type ghnDetailByClientCodePayload struct {
	ClientOrderCode string `json:"client_order_code"`
}

type ghnDetailByClientCodeResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		OrderCode string `json:"order_code"`
		Status    string `json:"status"`
		TotalFee  int64  `json:"total_fee"`
		Fee       struct {
			MainService int64 `json:"main_service"`
		} `json:"fee"`
	} `json:"data"`
}

// sandboxGetByClientOrderCode recovers an existing GHN order after a duplicate client_order_code 400.
// Lookup errors are intentionally returned as retryable (never NonRetryable, except auth errors) so callers
// retry or return Unavailable instead of falling back to another carrier and creating a duplicate shipment.
func (g *GHNAdapter) sandboxGetByClientOrderCode(ctx context.Context, clientOrderCode, serviceCode string) (*ShipmentResult, error) {
	var resp ghnDetailByClientCodeResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/shipping-order/detail-by-client-code", ghnDetailByClientCodePayload{
		ClientOrderCode: clientOrderCode,
	}, &resp); err != nil {
		if errors.Is(err, ErrInvalidCarrierToken) {
			return nil, err
		}
		var nr *NonRetryableError
		if errors.As(err, &nr) {
			return nil, fmt.Errorf("ghn lookup duplicate client_order_code %s failed: %w", clientOrderCode, nr.Err)
		}
		return nil, fmt.Errorf("ghn lookup duplicate client_order_code %s failed: %w", clientOrderCode, err)
	}
	if resp.Code != 200 || resp.Data.OrderCode == "" {
		if resp.Code == http.StatusUnauthorized || resp.Code == http.StatusForbidden {
			return nil, classifyGHNAPIError("detail-by-client-code", resp.Code, resp.Message)
		}
		return nil, fmt.Errorf("ghn lookup duplicate client_order_code %s error code=%d message=%s", clientOrderCode, resp.Code, resp.Message)
	}
	svc := serviceCode
	if svc == "" {
		svc = "STANDARD"
	}
	fee := resp.Data.TotalFee
	if fee <= 0 {
		fee = resp.Data.Fee.MainService
	}
	st := StatusCreated
	if mapped, ok := MapCarrierStatus(resp.Data.Status); ok && mapped != "" {
		st = mapped
	}
	return &ShipmentResult{
		CarrierCode:  CodeGHN,
		ServiceCode:  svc,
		TrackingCode: resp.Data.OrderCode,
		LabelURL:     fmt.Sprintf("%s/%s", g.labelBaseURL, resp.Data.OrderCode),
		FeeVND:       fee,
		Status:       st,
	}, nil
}

type ghnCancelPayload struct {
	OrderCodes []string `json:"order_codes"`
}

type ghnBasicResponse struct {
	Code        int    `json:"code"`
	CodeMessage string `json:"code_message"`
	Message     string `json:"message"`
}

func (g *GHNAdapter) sandboxCancelShipment(ctx context.Context, trackingCode string) error {
	var resp ghnBasicResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/switch-status/cancel", ghnCancelPayload{
		OrderCodes: []string{trackingCode},
	}, &resp); err != nil {
		if IsAlreadyCancelledMessage(err.Error()) {
			return nil
		}
		return err
	}
	if resp.Code != 200 {
		if IsAlreadyCancelledMessage(resp.Message) || IsAlreadyCancelledMessage(resp.CodeMessage) {
			return nil
		}
		return classifyGHNAPIError("cancel", resp.Code, resp.Message)
	}
	return nil
}

type ghnDetailPayload struct {
	OrderCode string `json:"order_code"`
}

type ghnDetailResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		OrderCode   string `json:"order_code"`
		Status      string `json:"status"`
		UpdatedDate string `json:"updated_date"`
	} `json:"data"`
}

func (g *GHNAdapter) sandboxGetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error) {
	var resp ghnDetailResponse
	if err := g.doJSON(ctx, http.MethodPost, "/v2/shipping-order/detail", ghnDetailPayload{
		OrderCode: trackingCode,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, classifyGHNAPIError("detail", resp.Code, resp.Message)
	}
	mapped, ok := MapCarrierStatus(resp.Data.Status)
	if !ok {
		return &TrackingStatus{TrackingCode: trackingCode, Changed: false}, nil
	}
	ts, err := time.Parse(time.RFC3339, resp.Data.UpdatedDate)
	if err != nil {
		ts = time.Now().UTC()
	}
	return &TrackingStatus{
		TrackingCode: trackingCode,
		Status:       mapped,
		RawStatus:    resp.Data.Status,
		Description:  StatusDescriptionVI(mapped),
		OccurredAt:   ts,
		Changed:      true,
	}, nil
}

func (g *GHNAdapter) doJSON(ctx context.Context, method, path string, body any, dst any) error {
	return g.doJSONWithToken(ctx, method, path, body, dst, g.getToken())
}

func (g *GHNAdapter) doJSONWithToken(ctx context.Context, method, path string, body any, dst any, token string) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &NonRetryableError{Err: fmt.Errorf("marshal ghn request: %w", err)}
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, bodyReader)
	if err != nil {
		return &NonRetryableError{Err: fmt.Errorf("build ghn http request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)
	if g.shopID > 0 {
		req.Header.Set("ShopId", strconv.Itoa(g.shopID))
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("ghn http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read ghn response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &NonRetryableError{Err: fmt.Errorf("%w: ghn auth/config rejected status=%d", ErrInvalidCarrierToken, resp.StatusCode)}
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return fmt.Errorf("ghn transient error status=%d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &NonRetryableError{Err: fmt.Errorf("ghn rejected request status=%d body=%s", resp.StatusCode, string(respBody))}
	}
	if err := json.Unmarshal(respBody, dst); err != nil {
		return fmt.Errorf("decode ghn response: %w", err)
	}
	return nil
}

func classifyGHNAPIError(op string, code int, message string) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &NonRetryableError{Err: fmt.Errorf("%w: ghn %s auth/config error code=%d", ErrInvalidCarrierToken, op, code)}
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return fmt.Errorf("ghn %s transient error code=%d message=%s", op, code, message)
	case code >= 400 && code < 500:
		return &NonRetryableError{Err: fmt.Errorf("ghn %s api rejected code=%d message=%s", op, code, message)}
	default:
		return fmt.Errorf("ghn %s api error code=%d message=%s", op, code, message)
	}
}

func routeZoneFee(fromProv, toProv string, intraProv, intraRegion, interRegion int64) (int64, int32) {
	if fromProv != "" && fromProv == toProv {
		return intraProv, 2
	}
	if address.RegionOfProvince(fromProv) == address.RegionOfProvince(toProv) {
		return intraRegion, 3
	}
	return interRegion, 4
}

func weightFeeVND(weightGrams, includedGrams, stepGrams int32, stepFeeVND int64) int64 {
	if weightGrams <= includedGrams {
		return 0
	}
	extra := weightGrams - includedGrams
	steps := int64((extra + stepGrams - 1) / stepGrams)
	return steps * stepFeeVND
}
