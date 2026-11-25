package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/labelpdf"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

const (
	maxBatchLabelCodes     = 50
	maxBatchLabelBodyBytes = 64 * 1024
)

type batchLabelsRequest struct {
	TrackingCodes []string `json:"tracking_codes"`
}

// BatchLabelHandler handles POST /labels/batch returning a single multi-page A6 Vietnamese PDF (G5).
type BatchLabelHandler struct {
	svc *service.ShippingService
}

// NewBatchLabelHandler constructs a batch PDF label HTTP handler.
func NewBatchLabelHandler(svc *service.ShippingService) *BatchLabelHandler {
	return &BatchLabelHandler{svc: svc}
}

// ServeHTTP handles POST /labels/batch (and gateway-proxied /api/v1/shipping/labels/batch).
// Expects auth.HTTPMiddleware(jwtMgr, true) to have validated an end-user JWT; service tokens are rejected.
func (h *BatchLabelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok || claims.Service != "" {
		writeLabelJSONError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	canStaffRead := auth.HasCapability(claims, auth.CapOrdersRead) || auth.HasCapability(claims, auth.CapCarriersRead)
	isSeller := auth.HasRole(claims, auth.RoleSeller)
	if !canStaffRead && !isSeller {
		writeLabelJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !canStaffRead && strings.TrimSpace(claims.ShopID) == "" {
		writeLabelJSONError(w, http.StatusForbidden, "forbidden")
		return
	}

	var req batchLabelsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBatchLabelBodyBytes))
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeLabelJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeLabelJSONError(w, http.StatusBadRequest, "invalid json request body")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeLabelJSONError(w, http.StatusBadRequest, "invalid json request body")
		return
	}

	if len(req.TrackingCodes) == 0 || len(req.TrackingCodes) > maxBatchLabelCodes {
		writeLabelJSONError(w, http.StatusBadRequest, "tracking_codes must contain between 1 and 50 codes")
		return
	}

	orderedCodes := make([]string, 0, len(req.TrackingCodes))
	seen := make(map[string]bool, len(req.TrackingCodes))
	for _, raw := range req.TrackingCodes {
		code := strings.TrimSpace(raw)
		if code == "" {
			writeLabelJSONError(w, http.StatusBadRequest, "tracking_codes must not contain empty values")
			return
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		orderedCodes = append(orderedCodes, code)
	}

	shipments, err := h.svc.GetShipmentsByTrackingCodes(r.Context(), orderedCodes)
	if err != nil {
		writeLabelJSONError(w, http.StatusInternalServerError, "failed to load shipments")
		return
	}

	byCode := make(map[string]store.Shipment, len(shipments))
	for _, sh := range shipments {
		if !canStaffRead && sh.ShopID != claims.ShopID {
			writeLabelJSONError(w, http.StatusForbidden, "forbidden: one or more shipments belong to another shop")
			return
		}
		byCode[sh.TrackingCode] = sh
	}

	for _, code := range orderedCodes {
		if _, exists := byCode[code]; !exists {
			writeLabelJSONError(w, http.StatusNotFound, "one or more shipments not found")
			return
		}
	}

	type locKey struct {
		prov string
		ward string
	}
	type locNames struct {
		prov string
		ward string
	}
	locCache := make(map[locKey]locNames, len(orderedCodes)*2)
	resolveLoc := func(provCode, wardCode string) (string, string) {
		k := locKey{prov: strings.TrimSpace(provCode), ward: strings.TrimSpace(wardCode)}
		if cached, ok := locCache[k]; ok {
			return cached.prov, cached.ward
		}
		pName, wName := h.svc.ResolveLocationNames(r.Context(), k.prov, k.ward)
		locCache[k] = locNames{prov: pName, ward: wName}
		return pName, wName
	}

	labels := make([]labelpdf.LabelData, 0, len(orderedCodes))
	for _, code := range orderedCodes {
		sh := byCode[code]
		fromProv, fromWard := resolveLoc(sh.FromProvinceCode, sh.FromWardCode)
		toProv, toWard := resolveLoc(sh.ToProvinceCode, sh.ToWardCode)
		labels = append(labels, labelpdf.LabelData{
			TrackingCode:      sh.TrackingCode,
			OrderID:           sh.OrderID,
			ShopID:            sh.ShopID,
			Direction:         sh.Direction,
			CarrierCode:       sh.CarrierCode,
			ServiceCode:       sh.ServiceCode,
			Status:            sh.Status,
			CODAmountVND:      sh.CODAmountVND,
			FeeVND:            sh.FeeVND,
			WeightGrams:       sh.WeightGrams,
			FromName:          sh.FromName,
			FromPhone:         sh.FromPhone,
			FromStreetAddress: sh.FromStreetAddress,
			FromWardName:      fromWard,
			FromProvinceName:  fromProv,
			ToName:            sh.ToName,
			ToPhone:           sh.ToPhone,
			ToStreetAddress:   sh.ToStreetAddress,
			ToWardName:        toWard,
			ToProvinceName:    toProv,
			CreatedAt:         sh.CreatedAt,
		})
	}

	pdfBytes, err := labelpdf.RenderBatchA6(labels)
	if err != nil {
		writeLabelJSONError(w, http.StatusInternalServerError, "failed to render batch shipping labels PDF")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="shipping-labels-batch.pdf"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}
