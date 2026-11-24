package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

const labelContentSecurityPolicy = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"

// LabelHandler renders a printable HTML shipping label with JWT object-level authorization (SHP-B8).
type LabelHandler struct {
	svc *service.ShippingService
}

// NewLabelHandler constructs a shipping label HTTP handler.
func NewLabelHandler(svc *service.ShippingService) *LabelHandler {
	return &LabelHandler{svc: svc}
}

func writeLabelJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ServeHTTP handles GET /labels/{tracking_code} (and optional .html/.pdf suffix).
// Expects auth.HTTPMiddleware(jwtMgr, true) to have validated the end-user JWT.
func (h *LabelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok || claims.Service != "" {
		writeLabelJSONError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	rawCode := strings.TrimSpace(chi.URLParam(r, "tracking_code"))
	rawCode = strings.TrimSuffix(rawCode, ".html")
	rawCode = strings.TrimSuffix(rawCode, ".pdf")
	if rawCode == "" {
		writeLabelJSONError(w, http.StatusBadRequest, "tracking_code is required")
		return
	}

	sh, _, err := h.svc.GetShipment(r.Context(), "", rawCode, "")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeLabelJSONError(w, http.StatusNotFound, "shipment not found")
			return
		}
		writeLabelJSONError(w, http.StatusInternalServerError, "failed to load shipment")
		return
	}

	// Object-level authorization: staff with orders.read or carriers.read, seller owning the shop,
	// or buyer_id owner (independent of role). Service tokens are rejected above.
	canAdminRead := auth.HasCapability(claims, auth.CapOrdersRead) || auth.HasCapability(claims, auth.CapCarriersRead)
	isShopOwner := auth.HasRole(claims, auth.RoleSeller) && claims.ShopID != "" && claims.ShopID == sh.ShopID
	isBuyerOwner := sh.BuyerID != "" && sh.BuyerID == claims.UserID()
	if !canAdminRead && !isShopOwner && !isBuyerOwner {
		writeLabelJSONError(w, http.StatusForbidden, "forbidden")
		return
	}

	dirLabel := "Giao hàng (Chiều đi)"
	if store.NormalizeDirection(sh.Direction) == store.DirectionReturn {
		dirLabel = "Trả hàng (Chiều về)"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", labelContentSecurityPolicy)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)

	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="vi">
<head>
<meta charset="utf-8">
<title>Nhãn vận đơn %s</title>
<style>
body{font-family:system-ui,sans-serif;max-width:560px;margin:24px auto;padding:16px;color:#111}
.card{border:2px solid #111;padding:20px}
h1{font-size:20px;margin:0 0 12px}
.code{font-family:monospace;font-size:24px;font-weight:700;letter-spacing:1px;padding:8px 0;border-top:1px dashed #666;border-bottom:1px dashed #666;margin:12px 0}
dl{display:grid;grid-template-columns:160px 1fr;gap:6px 12px;margin:12px 0;font-size:14px}
dt{color:#555}
dd{margin:0;font-weight:600}
.hint{margin:16px 0 0;font-size:12px;color:#555}
@media print{.no-print{display:none}}
</style>
</head>
<body>
<div class="card">
  <h1>NexusMart — Phiếu vận đơn</h1>
  <div class="code">%s</div>
  <dl>
    <dt>Chiều vận đơn</dt><dd>%s</dd>
    <dt>Hãng / Dịch vụ</dt><dd>%s / %s</dd>
    <dt>Trạng thái</dt><dd>%s</dd>
    <dt>Mã đơn hàng</dt><dd>%s</dd>
    <dt>Mã gian hàng</dt><dd>%s</dd>
    <dt>Tuyến (Tỉnh/Phường)</dt><dd>%s/%s &rarr; %s/%s</dd>
    <dt>Khối lượng</dt><dd>%d g</dd>
    <dt>Thu hộ (COD)</dt><dd>%d VND</dd>
    <dt>Phí vận chuyển</dt><dd>%d VND</dd>
    <dt>Ngày tạo</dt><dd>%s</dd>
  </dl>
  <p class="hint no-print">Sử dụng chức năng in của trình duyệt (Ctrl+P / Cmd+P) để in nhãn.</p>
</div>
</body>
</html>`,
		html.EscapeString(sh.TrackingCode),
		html.EscapeString(sh.TrackingCode),
		html.EscapeString(dirLabel),
		html.EscapeString(strings.ToUpper(sh.CarrierCode)),
		html.EscapeString(sh.ServiceCode),
		html.EscapeString(carrier.StatusDescriptionVI(sh.Status)),
		html.EscapeString(sh.OrderID),
		html.EscapeString(sh.ShopID),
		html.EscapeString(sh.FromProvinceCode),
		html.EscapeString(sh.FromWardCode),
		html.EscapeString(sh.ToProvinceCode),
		html.EscapeString(sh.ToWardCode),
		sh.WeightGrams,
		sh.CODAmountVND,
		sh.FeeVND,
		html.EscapeString(sh.CreatedAt.UTC().Format(time.RFC3339)),
	)
}
