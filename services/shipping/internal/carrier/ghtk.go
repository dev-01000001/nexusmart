package carrier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/shipping/internal/address"
)

const (
	CodeGHTK               = "ghtk"
	NameGHTK               = "Giao Hang Tiet Kiem (GHTK)"
	defaultGHTKBaseURL     = "https://services-staging.ghtklab.com"
	placeholderGHTKToken   = "sandbox-ghtk-token"
	ghtkServiceRoad        = "ROAD"
	ghtkServiceXFast       = "XFAST"
	ghtkServiceRoadName    = "GHTK Đường Bộ"
	ghtkServiceXFastName   = "GHTK Nhanh"
	ghtkTimestampFormatSQL = "2006-01-02 15:04:05"
)

// GHTKConfig configures the GHTK carrier adapter.
type GHTKConfig struct {
	Mode         string // "mock" or "sandbox"
	BaseURL      string
	Token        string
	LabelBaseURL string
	HTTPClient   *http.Client
}

type ghtkMockStatus struct {
	rawStatus  string
	occurredAt time.Time
}

// GHTKAdapter implements Carrier for Giao Hang Tiet Kiem (sandbox HTTP + deterministic mock mode).
type GHTKAdapter struct {
	configuredMode string
	baseURL        string
	token          string
	labelBaseURL   string
	client         *http.Client
	mu             sync.RWMutex
	mockStatuses   map[string]ghtkMockStatus
	faults         mockFaultInjector
}

func isUsableGHTKToken(token string) bool {
	tok := strings.TrimSpace(token)
	return tok != "" && tok != placeholderGHTKToken
}

func (g *GHTKAdapter) isSandboxLocked() bool {
	return g.configuredMode == "sandbox" && isUsableGHTKToken(g.token)
}

func (g *GHTKAdapter) isSandbox() bool {
	g.mu.RLock()
	ok := g.isSandboxLocked()
	g.mu.RUnlock()
	return ok
}

// sandboxToken atomically reads the runtime token and effective sandbox mode under g.mu
// so concurrent SetToken calls cannot cause a sandbox request to send an empty/placeholder token.
func (g *GHTKAdapter) sandboxToken() (string, bool) {
	g.mu.RLock()
	tok := g.token
	sandbox := g.configuredMode == "sandbox" && isUsableGHTKToken(tok)
	g.mu.RUnlock()
	return tok, sandbox
}

// SetTestFaultInjectionEnabled toggles test-only fault injection in mock mode (never active in sandbox mode).
func (g *GHTKAdapter) SetTestFaultInjectionEnabled(enabled bool) {
	if g.isSandbox() {
		g.faults.setEnabled(false)
		return
	}
	g.faults.setEnabled(enabled)
}

// InjectCreateFault schedules count simulated HTTP error responses in mock mode for orderID ("" = any order).
func (g *GHTKAdapter) InjectCreateFault(orderID string, statusCode, count int) error {
	if g.isSandbox() {
		return errors.New("fault injection unavailable in ghtk sandbox mode")
	}
	return g.faults.inject(orderID, statusCode, count)
}

// NewGHTKAdapter creates a GHTK adapter in mock or sandbox mode.
// Effective mode is evaluated dynamically from configured Mode and current runtime Token:
// sandbox HTTP is active only when Mode == "sandbox" and Token is non-empty and not "sandbox-ghtk-token".
func NewGHTKAdapter(cfg GHTKConfig) *GHTKAdapter {
	configuredMode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if configuredMode != "sandbox" {
		configuredMode = "mock"
	}
	token := strings.TrimSpace(cfg.Token)
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGHTKBaseURL
	}
	labelBase := strings.TrimRight(strings.TrimSpace(cfg.LabelBaseURL), "/")
	if labelBase == "" {
		labelBase = DefaultLabelBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &GHTKAdapter{
		configuredMode: configuredMode,
		baseURL:        baseURL,
		token:          token,
		labelBaseURL:   labelBase,
		client:         httpClient,
		mockStatuses:   make(map[string]ghtkMockStatus),
	}
}

func (g *GHTKAdapter) Code() string { return CodeGHTK }
func (g *GHTKAdapter) Name() string { return NameGHTK }
func (g *GHTKAdapter) IsMock() bool { return !g.isSandbox() }

// SetToken updates the runtime API token used by the GHTK adapter.
func (g *GHTKAdapter) SetToken(token string) {
	g.mu.Lock()
	g.token = strings.TrimSpace(token)
	sandbox := g.isSandboxLocked()
	g.mu.Unlock()
	if sandbox {
		g.faults.setEnabled(false)
	}
}

func (g *GHTKAdapter) getToken() string {
	g.mu.RLock()
	tok := g.token
	g.mu.RUnlock()
	return tok
}

// RequestRedelivery succeeds in mock mode for deterministic testing, and fails fast with
// a non-retryable ErrRedeliveryUnsupported in non-mock modes because GHTK has no redelivery API endpoint.
func (g *GHTKAdapter) RequestRedelivery(_ context.Context, trackingCode, _, _ string) error {
	if !g.IsMock() {
		return &NonRetryableError{Err: fmt.Errorf("%w: GHTK không hỗ trợ API yêu cầu giao lại", ErrRedeliveryUnsupported)}
	}
	if strings.TrimSpace(trackingCode) == "" {
		return &NonRetryableError{Err: errors.New("ghtk redelivery requires tracking code")}
	}
	return nil
}

// TestConnection validates GHTK credentials/connectivity without tripping the production circuit breaker.
func (g *GHTKAdapter) TestConnection(ctx context.Context, token string) error {
	tok := strings.TrimSpace(token)
	if tok == "" {
		tok = g.getToken()
	}
	if g.configuredMode != "sandbox" {
		if IsMockTokenRejected(tok, true) {
			return &NonRetryableError{Err: fmt.Errorf("%w: ghtk rejected API token", ErrInvalidCarrierToken)}
		}
		return nil
	}
	if !isUsableGHTKToken(tok) {
		return &NonRetryableError{Err: fmt.Errorf("%w: ghtk sandbox API token is required", ErrInvalidCarrierToken)}
	}
	params := url.Values{}
	params.Set("pick_province", "Thành phố Hà Nội")
	params.Set("pick_district", "Quận Hoàn Kiếm")
	params.Set("province", "Thành phố Hồ Chí Minh")
	params.Set("district", "Quận 1")
	params.Set("weight", "500")
	params.Set("transport", "road")

	var resp ghtkFeeResponse
	if err := g.doRequestWithToken(ctx, http.MethodGet, "/services/shipment/fee?"+params.Encode(), nil, &resp, tok); err != nil {
		if errors.Is(err, ErrInvalidCarrierToken) {
			return err
		}
		if IsNonRetryable(err) {
			return &NonRetryableError{Err: fmt.Errorf("%w: ghtk authentication failed", ErrInvalidCarrierToken)}
		}
		return err
	}
	if !resp.Success {
		return &NonRetryableError{Err: fmt.Errorf("%w: ghtk test connection failed: %s", ErrInvalidCarrierToken, resp.Message)}
	}
	return nil
}

// CanPoll reports true in sandbox mode or when mock status overrides are staged for polling tests.
func (g *GHTKAdapter) CanPoll() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.isSandboxLocked() {
		return true
	}
	return len(g.mockStatuses) > 0
}

// SetMockStatus stages a carrier status for trackingCode in mock mode so polling tests can observe status changes.
// Passing an empty status removes any staged status for trackingCode.
func (g *GHTKAdapter) SetMockStatus(trackingCode, status string) {
	code := strings.TrimSpace(trackingCode)
	if code == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := strings.TrimSpace(status)
	if st == "" {
		delete(g.mockStatuses, code)
		return
	}
	g.mockStatuses[code] = ghtkMockStatus{
		rawStatus:  st,
		occurredAt: time.Now().UTC(),
	}
}

func (g *GHTKAdapter) CalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error) {
	tok, sandbox := g.sandboxToken()
	if !sandbox {
		return g.mockQuotes(req), nil
	}
	return g.sandboxCalculateFee(ctx, req, tok)
}

func (g *GHTKAdapter) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	tok, sandbox := g.sandboxToken()
	if !sandbox {
		if err := g.faults.consume(CodeGHTK, req.OrderID); err != nil {
			return nil, err
		}
		return g.mockCreateShipment(req), nil
	}
	return g.sandboxCreateShipment(ctx, req, tok)
}

func (g *GHTKAdapter) CancelShipment(ctx context.Context, trackingCode string) error {
	tok, sandbox := g.sandboxToken()
	if !sandbox {
		return nil
	}
	return g.sandboxCancelShipment(ctx, trackingCode, tok)
}

func (g *GHTKAdapter) GetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error) {
	tok, sandbox := g.sandboxToken()
	if !sandbox {
		return g.mockGetStatus(trackingCode), nil
	}
	return g.sandboxGetStatus(ctx, trackingCode, tok)
}

func (g *GHTKAdapter) mockQuotes(req FeeRequest) []Quote {
	baseFee, roadDays := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 20000, 28000, 35000)
	weightSurcharge := weightFeeVND(req.WeightGrams, 1000, 500, 4500)
	codFee := int64(0)
	if req.CODAmountVND > 0 {
		codFee = 1500
	}
	roadTotal := baseFee + weightSurcharge + codFee
	xfastDays := roadDays - 1
	if xfastDays < 1 {
		xfastDays = 1
	}

	return []Quote{
		{
			CarrierCode:   CodeGHTK,
			CarrierName:   NameGHTK,
			ServiceCode:   ghtkServiceRoad,
			ServiceName:   ghtkServiceRoadName,
			FeeVND:        roadTotal,
			EstimatedDays: roadDays,
			IsFallback:    false,
		},
		{
			CarrierCode:   CodeGHTK,
			CarrierName:   NameGHTK,
			ServiceCode:   ghtkServiceXFast,
			ServiceName:   ghtkServiceXFastName,
			FeeVND:        roadTotal + 11000,
			EstimatedDays: xfastDays,
			IsFallback:    false,
		},
	}
}

func (g *GHTKAdapter) mockCreateShipment(req ShipmentRequest) *ShipmentResult {
	svc := strings.ToUpper(strings.TrimSpace(req.ServiceCode))
	if svc == "" {
		svc = ghtkServiceRoad
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

	tracking := ""
	if strings.TrimSpace(req.ClientOrderCode) != "" {
		tracking = mockTrackingCode("GHTK", req.ClientOrderCode)
	} else {
		tracking = deterministicGHTKTracking(req.OrderID, req.Direction, req.ReturnRequestID, svc)
	}
	return &ShipmentResult{
		CarrierCode:  CodeGHTK,
		ServiceCode:  svc,
		TrackingCode: tracking,
		LabelURL:     fmt.Sprintf("%s/%s", g.labelBaseURL, tracking),
		FeeVND:       fee,
		Status:       StatusCreated,
	}
}

func (g *GHTKAdapter) mockGetStatus(trackingCode string) *TrackingStatus {
	g.mu.RLock()
	entry, ok := g.mockStatuses[strings.TrimSpace(trackingCode)]
	g.mu.RUnlock()
	if !ok {
		return &TrackingStatus{
			TrackingCode: trackingCode,
			Changed:      false,
		}
	}
	mapped, valid := MapCarrierStatus(entry.rawStatus)
	if !valid {
		return &TrackingStatus{
			TrackingCode: trackingCode,
			RawStatus:    entry.rawStatus,
			Changed:      false,
		}
	}
	return &TrackingStatus{
		TrackingCode: trackingCode,
		Status:       mapped,
		RawStatus:    entry.rawStatus,
		Description:  "Synced from GHTK status polling",
		OccurredAt:   entry.occurredAt,
		Changed:      true,
	}
}

func deterministicGHTKTracking(orderID, direction, returnRequestID, serviceCode string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		shortID := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
		return "GHTK-" + shortID
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "forward"
	}
	seed := strings.Join([]string{orderID, dir, strings.TrimSpace(returnRequestID), strings.ToUpper(strings.TrimSpace(serviceCode))}, "|")
	sum := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("GHTK-%010X", sum[:5])
}

type ghtkResolvedLocation struct {
	Province string
	District string
	Ward     string
}

func resolveGHTKLocation(provinceCode, provinceName, wardCode, wardName, legacyProvince, legacyDistrict string) ghtkResolvedLocation {
	fb := address.LookupLegacyFallback(wardCode)

	prov := strings.TrimSpace(legacyProvince)
	if prov == "" && fb.Found && fb.LegacyProvince != "" {
		prov = fb.LegacyProvince
	}
	if prov == "" {
		prov = strings.TrimSpace(provinceName)
	}
	if prov == "" && fb.Found {
		prov = fb.ProvinceName
	}
	if prov == "" {
		prov = address.ProvinceName(provinceCode)
	}
	if prov == "" {
		prov = strings.TrimSpace(provinceCode)
	}

	ward := strings.TrimSpace(wardName)
	if ward == "" && fb.Found {
		ward = fb.WardName
	}
	if ward == "" {
		ward = address.WardName(wardCode)
	}
	if ward == "" {
		ward = strings.TrimSpace(wardCode)
	}

	dist := strings.TrimSpace(legacyDistrict)
	if dist == "" && fb.Found && fb.LegacyDistrict != "" {
		dist = fb.LegacyDistrict
	}
	if dist == "" {
		dist = ward
	}
	if dist == "" {
		dist = prov
	}

	return ghtkResolvedLocation{
		Province: prov,
		District: dist,
		Ward:     ward,
	}
}

type ghtkFeeResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Fee     struct {
		Name         string `json:"name"`
		Fee          int64  `json:"fee"`
		InsuranceFee int64  `json:"insurance_fee"`
		Delivery     bool   `json:"delivery"`
	} `json:"fee"`
}

func (g *GHTKAdapter) sandboxCalculateFee(ctx context.Context, req FeeRequest, token string) ([]Quote, error) {
	weight := req.WeightGrams
	if weight <= 0 {
		weight = 500
	}
	fromLoc := resolveGHTKLocation(
		req.FromProvinceCode,
		req.FromProvinceName,
		req.FromWardCode,
		req.FromWardName,
		req.FromLegacyProvince,
		req.FromLegacyDistrict,
	)
	toLoc := resolveGHTKLocation(
		req.ToProvinceCode,
		req.ToProvinceName,
		req.ToWardCode,
		req.ToWardName,
		req.ToLegacyProvince,
		req.ToLegacyDistrict,
	)

	params := url.Values{}
	params.Set("pick_province", fromLoc.Province)
	params.Set("pick_district", fromLoc.District)
	if fromLoc.Ward != "" {
		params.Set("pick_ward", fromLoc.Ward)
	}
	params.Set("province", toLoc.Province)
	params.Set("district", toLoc.District)
	if toLoc.Ward != "" {
		params.Set("ward", toLoc.Ward)
	}
	params.Set("weight", strconv.Itoa(int(weight)))
	if req.CODAmountVND > 0 {
		params.Set("value", strconv.FormatInt(req.CODAmountVND, 10))
	}
	params.Set("transport", "road")

	var resp ghtkFeeResponse
	if err := g.doRequestWithToken(ctx, http.MethodGet, "/services/shipment/fee?"+params.Encode(), nil, &resp, token); err != nil {
		return nil, err
	}
	totalFee := resp.Fee.Fee + resp.Fee.InsuranceFee
	if !resp.Success || totalFee <= 0 {
		return nil, fmt.Errorf("ghtk fee api error: %s", resp.Message)
	}

	_, days := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 0, 0, 0)
	return []Quote{
		{
			CarrierCode:   CodeGHTK,
			CarrierName:   NameGHTK,
			ServiceCode:   ghtkServiceRoad,
			ServiceName:   ghtkServiceRoadName,
			FeeVND:        totalFee,
			EstimatedDays: days,
			IsFallback:    false,
		},
	}, nil
}

type ghtkOrderProduct struct {
	Name     string  `json:"name"`
	Weight   float64 `json:"weight"`
	Quantity int     `json:"quantity"`
}

type ghtkOrderDetail struct {
	ID           string `json:"id"`
	PickName     string `json:"pick_name"`
	PickAddress  string `json:"pick_address"`
	PickProvince string `json:"pick_province"`
	PickDistrict string `json:"pick_district"`
	PickWard     string `json:"pick_ward,omitempty"`
	PickTel      string `json:"pick_tel"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	Province     string `json:"province"`
	District     string `json:"district"`
	Ward         string `json:"ward,omitempty"`
	Hamlet       string `json:"hamlet"`
	Tel          string `json:"tel"`
	PickMoney    int64  `json:"pick_money"`
	Value        int64  `json:"value,omitempty"`
	Transport    string `json:"transport"`
	IsFreeship   int    `json:"is_freeship"`
}

type ghtkCreatePayload struct {
	Products []ghtkOrderProduct `json:"products"`
	Order    ghtkOrderDetail    `json:"order"`
}

type ghtkCreateResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Order   struct {
		PartnerID string `json:"partner_id"`
		Label     string `json:"label"`
		Fee       int64  `json:"fee"`
		StatusID  int    `json:"status_id"`
	} `json:"order"`
}

func (g *GHTKAdapter) sandboxCreateShipment(ctx context.Context, req ShipmentRequest, token string) (*ShipmentResult, error) {
	weight := req.WeightGrams
	if weight <= 0 {
		weight = 500
	}
	fromLoc := resolveGHTKLocation(
		req.FromAddress.ProvinceCode,
		req.FromAddress.ProvinceName,
		req.FromAddress.WardCode,
		req.FromAddress.WardName,
		req.FromAddress.LegacyProvince,
		req.FromAddress.LegacyDistrict,
	)
	toLoc := resolveGHTKLocation(
		req.ToAddress.ProvinceCode,
		req.ToAddress.ProvinceName,
		req.ToAddress.WardCode,
		req.ToAddress.WardName,
		req.ToAddress.LegacyProvince,
		req.ToAddress.LegacyDistrict,
	)

	itemName := strings.TrimSpace(req.ItemsSummary)
	if itemName == "" {
		itemName = "NexusMart Order"
	}
	svc := strings.ToUpper(strings.TrimSpace(req.ServiceCode))
	if svc == "" {
		svc = ghtkServiceRoad
	}
	transport := "road"
	if svc == ghtkServiceXFast || svc == "FLY" || svc == "EXPRESS" {
		transport = "fly"
	}

	partnerOrderID := strings.TrimSpace(req.ClientOrderCode)
	if partnerOrderID == "" {
		partnerOrderID = req.OrderID
		if strings.EqualFold(strings.TrimSpace(req.Direction), "return") {
			if strings.TrimSpace(req.ReturnRequestID) != "" {
				partnerOrderID = req.OrderID + "-return-" + strings.TrimSpace(req.ReturnRequestID)
			} else {
				partnerOrderID = req.OrderID + "-return"
			}
		}
	}

	payload := ghtkCreatePayload{
		Products: []ghtkOrderProduct{
			{
				Name:     itemName,
				Weight:   float64(weight) / 1000.0,
				Quantity: 1,
			},
		},
		Order: ghtkOrderDetail{
			ID:           partnerOrderID,
			PickName:     req.FromAddress.Name,
			PickAddress:  req.FromAddress.StreetAddress,
			PickProvince: fromLoc.Province,
			PickDistrict: fromLoc.District,
			PickWard:     fromLoc.Ward,
			PickTel:      req.FromAddress.Phone,
			Name:         req.ToAddress.Name,
			Address:      req.ToAddress.StreetAddress,
			Province:     toLoc.Province,
			District:     toLoc.District,
			Ward:         toLoc.Ward,
			Hamlet:       "Khác",
			Tel:          req.ToAddress.Phone,
			PickMoney:    req.CODAmountVND,
			Value:        req.CODAmountVND,
			Transport:    transport,
			IsFreeship:   1,
		},
	}

	var resp ghtkCreateResponse
	if err := g.doRequestWithToken(ctx, http.MethodPost, "/services/shipment/order", payload, &resp, token); err != nil {
		return nil, err
	}
	if !resp.Success || strings.TrimSpace(resp.Order.Label) == "" {
		return nil, fmt.Errorf("ghtk create api error: %s", resp.Message)
	}

	return &ShipmentResult{
		CarrierCode:  CodeGHTK,
		ServiceCode:  svc,
		TrackingCode: resp.Order.Label,
		LabelURL:     fmt.Sprintf("%s/%s", g.labelBaseURL, url.PathEscape(resp.Order.Label)),
		FeeVND:       resp.Order.Fee,
		Status:       StatusCreated,
	}, nil
}

type ghtkBasicResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (g *GHTKAdapter) sandboxCancelShipment(ctx context.Context, trackingCode, token string) error {
	var resp ghtkBasicResponse
	path := "/services/shipment/cancel/" + url.PathEscape(strings.TrimSpace(trackingCode))
	if err := g.doRequestWithToken(ctx, http.MethodPost, path, nil, &resp, token); err != nil {
		if IsAlreadyCancelledMessage(err.Error()) {
			return nil
		}
		return err
	}
	if !resp.Success {
		if IsAlreadyCancelledMessage(resp.Message) {
			return nil
		}
		return fmt.Errorf("ghtk cancel api error: %s", resp.Message)
	}
	return nil
}

type ghtkStatusResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Order   struct {
		LabelID    string          `json:"label_id"`
		PartnerID  string          `json:"partner_id"`
		Status     json.RawMessage `json:"status"`
		StatusText string          `json:"status_text"`
		Modified   string          `json:"modified"`
	} `json:"order"`
}

func (g *GHTKAdapter) sandboxGetStatus(ctx context.Context, trackingCode, token string) (*TrackingStatus, error) {
	var resp ghtkStatusResponse
	path := "/services/shipment/v2/" + url.PathEscape(strings.TrimSpace(trackingCode))
	if err := g.doRequestWithToken(ctx, http.MethodGet, path, nil, &resp, token); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("ghtk status api error: %s", resp.Message)
	}

	rawStatus := parseGHTKRawStatus(resp.Order.Status)
	mapped, ok := MapCarrierStatus(rawStatus)
	if !ok {
		return &TrackingStatus{
			TrackingCode: trackingCode,
			RawStatus:    rawStatus,
			Changed:      false,
		}, nil
	}

	ts := parseGHTKTimestamp(resp.Order.Modified)
	desc := strings.TrimSpace(resp.Order.StatusText)
	if desc == "" {
		desc = "Synced from GHTK status polling"
	}
	return &TrackingStatus{
		TrackingCode: trackingCode,
		Status:       mapped,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   ts,
		Changed:      true,
	}, nil
}

func parseGHTKRawStatus(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		return strings.TrimSpace(asStr)
	}
	var asNum int64
	if err := json.Unmarshal(raw, &asNum); err == nil {
		return strconv.FormatInt(asNum, 10)
	}
	return strings.Trim(trimmed, "\"")
}

// vietnamLocation is fixed UTC+7 (ICT) so timezone-less GHTK SQL timestamps never depend on host tzdata.
var vietnamLocation = time.FixedZone("ICT", 7*60*60)

func parseGHTKTimestamp(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Now().UTC()
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC()
	}
	if ts, err := time.ParseInLocation(ghtkTimestampFormatSQL, s, vietnamLocation); err == nil {
		return ts.UTC()
	}
	return time.Now().UTC()
}

func (g *GHTKAdapter) doRequestWithToken(ctx context.Context, method, path string, body any, dst any, token string) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &NonRetryableError{Err: fmt.Errorf("marshal ghtk request: %w", err)}
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, bodyReader)
	if err != nil {
		return &NonRetryableError{Err: fmt.Errorf("build ghtk http request: %w", err)}
	}
	req.Header.Set("Token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("ghtk http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read ghtk response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &NonRetryableError{Err: fmt.Errorf("%w: ghtk rejected request status=%d body=%s", ErrInvalidCarrierToken, resp.StatusCode, string(respBody))}
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("ghtk transient error status=%d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &NonRetryableError{Err: fmt.Errorf("ghtk rejected request status=%d body=%s", resp.StatusCode, string(respBody))}
	}
	if err := json.Unmarshal(respBody, dst); err != nil {
		return fmt.Errorf("decode ghtk response: %w", err)
	}
	return nil
}
