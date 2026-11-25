package webhook

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

func TestBatchLabelHandler_W3B3_G5_SecurityValidationAndVietnamesePDF(t *testing.T) {
	repo := newStubRepo()
	now := time.Date(2026, 11, 25, 4, 0, 0, 0, time.UTC)

	seed := func(shopID, buyerID, orderID, tracking, toName, toStreet, toProv, toWard string, cod int64) {
		_, _, err := repo.CreateShipment(context.Background(), &store.Shipment{
			ID:                uuid.NewString(),
			OrderID:           orderID,
			ShopID:            shopID,
			BuyerID:           buyerID,
			Direction:         store.DirectionForward,
			CarrierCode:       carrier.CodeGHN,
			ServiceCode:       "STANDARD",
			TrackingCode:      tracking,
			Status:            carrier.StatusCreated,
			FeeVND:            22000,
			CODAmountVND:      cod,
			WeightGrams:       650,
			FromName:          "Xưởng Áo Thun Việt",
			FromPhone:         "0901234567",
			FromStreetAddress: "12 Nguyễn Huệ",
			FromProvinceCode:  "79",
			FromWardCode:      "26740",
			ToName:            toName,
			ToPhone:           "0987654321",
			ToStreetAddress:   toStreet,
			ToProvinceCode:    toProv,
			ToWardCode:        toWard,
			CreatedAt:         now,
			UpdatedAt:         now,
		}, nil)
		if err != nil {
			t.Fatalf("seed shipment %s: %v", tracking, err)
		}
	}

	seed("shop-A", "buyer-1", "ord-a1", "GHN-SHOPA-1", "Nguyễn Văn Ánh", "10 Đường Lê Lợi", "01", "00070", 250000)
	seed("shop-A", "buyer-2", "ord-a2", "GHN-SHOPA-2", "Trần Thị Bích Ngọc", "20 Phố Huế", "48", "20242", 180000)
	seed("shop-A", "buyer-3", "ord-a3", "GHN-SHOPA-3", "Lê Hoàng Phúc", "30 Đường Hai Bà Trưng", "79", "26743", 0)
	seed("shop-A", "buyer-4", "ord-a4", "GHN-SHOPA-4", "Phạm Minh Đức", "40 Trần Phú", "31", "11311", 420000)
	seed("shop-B", "buyer-5", "ord-b1", "GHN-SHOPB-1", "Đỗ Thu Hà", "50 Bạch Đằng", "92", "31135", 500000)

	jwtMgr, err := auth.NewManager("test-w3b3-g5-user-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-w3b3-g5-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bh := NewBatchLabelHandler(svc)

	r := chi.NewRouter()
	r.Group(func(authed chi.Router) {
		authed.Use(auth.HTTPMiddleware(jwtMgr, true))
		authed.Post("/labels/batch", bh.ServeHTTP)
		authed.Post("/api/v1/shipping/labels/batch", bh.ServeHTTP)
	})

	sign := func(uid, role, shopID string) string {
		tok, _, _, sErr := jwtMgr.SignAccessToken(uid, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return tok
	}

	sellerATok := sign("seller-a", auth.RoleSeller, "shop-A")
	sellerEmptyShopTok := sign("seller-empty", auth.RoleSeller, "")
	buyerOwnerTok := sign("buyer-1", auth.RoleBuyer, "")
	modTok := sign("mod-1", auth.RoleModerator, "")
	superAdminTok := sign("sa-1", auth.RoleSuperAdmin, "")
	adminTok := sign("admin-1", auth.RoleAdmin, "")
	opsTok := sign("ops-1", auth.RoleOps, "")
	cskhTok := sign("cskh-1", auth.RoleCSKH, "")
	accountantTok := sign("acc-1", auth.RoleAccountant, "")
	svcTok, _ := svcMgr.SignServiceToken("order-service")
	sameKeySvcTok, _ := jwtMgr.SignServiceToken("order-service")

	postBatch := func(path, token string, body any) *httptest.ResponseRecorder {
		var raw []byte
		switch v := body.(type) {
		case string:
			raw = []byte(v)
		default:
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1. G5 attacks: 0 codes or 51 codes -> 400.
	t.Run("Validation_0And51CodesAndInvalidPayloadReturn400", func(t *testing.T) {
		if rec := postBatch("/labels/batch", sellerATok, map[string]any{"tracking_codes": []string{}}); rec.Code != http.StatusBadRequest {
			t.Fatalf("0 codes status=%d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		codes51 := make([]string, 51)
		for i := range codes51 {
			codes51[i] = fmt.Sprintf("GHN-X-%02d", i)
		}
		if rec := postBatch("/labels/batch", sellerATok, map[string]any{"tracking_codes": codes51}); rec.Code != http.StatusBadRequest {
			t.Fatalf("51 codes status=%d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if rec := postBatch("/labels/batch", sellerATok, map[string]any{"tracking_codes": []string{"GHN-SHOPA-1", "   "}}); rec.Code != http.StatusBadRequest {
			t.Fatalf("blank code status=%d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if rec := postBatch("/labels/batch", sellerATok, "{bad json"); rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed json status=%d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// 2. G5 attack: 4 shop A + 1 shop B with shop A JWT -> 403 whole batch.
	t.Run("CrossShopBatchRejected403WholeBatch", func(t *testing.T) {
		rec := postBatch("/labels/batch", sellerATok, map[string]any{
			"tracking_codes": []string{"GHN-SHOPA-1", "GHN-SHOPA-2", "GHN-SHOPA-3", "GHN-SHOPA-4", "GHN-SHOPB-1"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("4 shopA + 1 shopB status=%d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("403 must return application/json, got %q", rec.Header().Get("Content-Type"))
		}

		// Mixed foreign shop B + missing code -> 403 (must not leak missing vs existing of foreign shop).
		recMixed := postBatch("/labels/batch", sellerATok, map[string]any{
			"tracking_codes": []string{"GHN-SHOPB-1", "GHN-MISSING-999"},
		})
		if recMixed.Code != http.StatusForbidden {
			t.Fatalf("foreign shop + missing status=%d, want 403", recMixed.Code)
		}
	})

	// 3. Missing code -> 404.
	t.Run("MissingCodeReturns404", func(t *testing.T) {
		rec := postBatch("/labels/batch", sellerATok, map[string]any{
			"tracking_codes": []string{"GHN-SHOPA-1", "GHN-DOES-NOT-EXIST"},
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("missing code status=%d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	// 4. G5 attack: buyer JWT, moderator JWT, seller without shop_id -> 403; service tokens & anonymous -> 401.
	t.Run("UnauthorizedRolesAndServiceTokenRejected", func(t *testing.T) {
		body := map[string]any{"tracking_codes": []string{"GHN-SHOPA-1"}}
		for _, tc := range []struct {
			name string
			tok  string
			want int
		}{
			{"anonymous", "", http.StatusUnauthorized},
			{"service_token_other_key", svcTok, http.StatusUnauthorized},
			{"service_token_same_key", sameKeySvcTok, http.StatusUnauthorized},
			{"buyer_even_if_order_owner", buyerOwnerTok, http.StatusForbidden},
			{"moderator_no_orders_or_carriers_read", modTok, http.StatusForbidden},
			{"seller_empty_shop_id", sellerEmptyShopTok, http.StatusForbidden},
		} {
			rec := postBatch("/labels/batch", tc.tok, body)
			if rec.Code != tc.want {
				t.Fatalf("%s: status=%d, want %d (body=%s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		}

		// Direct handler invocation with injected service claims must also reject service token.
		rDirect := chi.NewRouter()
		rDirect.Post("/labels/batch", bh.ServeHTTP)
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/labels/batch", bytes.NewReader(raw))
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
			Role:    auth.ServiceRole,
			Service: "order-service",
		}))
		rec := httptest.NewRecorder()
		rDirect.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Fatalf("direct handler service claims: status=%d, want 401 or 403", rec.Code)
		}
	})

	// 5. Valid seller batch (4 labels of shop A) returns multi-page A6 PDF with exact headers & Vietnamese diacritics.
	// Also verifies no batch HTML output even if ?format=html is supplied, and verifies gateway alias path works.
	t.Run("ValidSellerBatchReturnsA6VietnamesePDFAndExactHeaders", func(t *testing.T) {
		for _, path := range []string{"/labels/batch", "/api/v1/shipping/labels/batch?format=html"} {
			rec := postBatch(path, sellerATok, map[string]any{
				"tracking_codes": []string{"GHN-SHOPA-1", "GHN-SHOPA-2", "GHN-SHOPA-3", "GHN-SHOPA-4"},
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s status=%d, want 200 (body=%s)", path, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
				t.Fatalf("POST %s Content-Type=%q, want application/pdf", path, got)
			}
			if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="shipping-labels-batch.pdf"` {
				t.Fatalf("POST %s Content-Disposition=%q, want inline; filename=\"shipping-labels-batch.pdf\"", path, got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("POST %s X-Content-Type-Options=%q, want nosniff", path, got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("POST %s Cache-Control=%q, want private, no-store", path, got)
			}

			pdfBytes := rec.Body.Bytes()
			if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
				t.Fatalf("POST %s body does not start with %%PDF-", path)
			}
			pages := countPDFPages(pdfBytes)
			if pages != 4 {
				t.Fatalf("POST %s page count=%d, want 4", path, pages)
			}
			if !bytes.Contains(pdfBytes, []byte("/FontFile2")) {
				t.Fatalf("POST %s PDF missing embedded TrueType font (/FontFile2)", path)
			}
			extracted := extractPDFUnicodeText(pdfBytes)
			for _, want := range []string{
				"GHN-SHOPA-1", "Nguyễn Văn Ánh", "Phường Hoàn Kiếm", "Thành phố Hà Nội", "250.000 VND",
				"GHN-SHOPA-2", "Trần Thị Bích Ngọc", "Phường Hải Châu", "Thành phố Đà Nẵng",
				"GHN-SHOPA-3", "Lê Hoàng Phúc", "Phường Bến Thành", "Thành phố Hồ Chí Minh",
				"GHN-SHOPA-4", "Phạm Minh Đức", "Phường Hồng Bàng", "Thành phố Hải Phòng", "420.000 VND",
				"Xưởng Áo Thun Việt", "Phường Sài Gòn",
			} {
				if !strings.Contains(extracted, want) {
					t.Fatalf("POST %s extracted PDF text missing %q", path, want)
				}
			}
		}
	})

	// 6. Authorized staff (orders.read or carriers.read) can print labels across mixed shops A + B.
	t.Run("AuthorizedStaffCanPrintCrossShopBatch", func(t *testing.T) {
		body := map[string]any{"tracking_codes": []string{"GHN-SHOPA-1", "GHN-SHOPB-1"}}
		for _, tc := range []struct {
			name string
			tok  string
		}{
			{"super_admin", superAdminTok},
			{"admin", adminTok},
			{"ops", opsTok},
			{"cskh", cskhTok},
			{"accountant", accountantTok},
		} {
			rec := postBatch("/labels/batch", tc.tok, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("staff %s status=%d, want 200 (body=%s)", tc.name, rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != "application/pdf" {
				t.Fatalf("staff %s Content-Type=%q, want application/pdf", tc.name, rec.Header().Get("Content-Type"))
			}
			if gotPages := countPDFPages(rec.Body.Bytes()); gotPages != 2 {
				t.Fatalf("staff %s pages=%d, want 2", tc.name, gotPages)
			}
		}
	})
}

var (
	reTestPDFPage    = regexp.MustCompile(`/Type\s*/Page\b`)
	reTestPDFStream  = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	reTestBFChar     = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	reTestBFRange    = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	reTestHexPair    = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reTestRangeLine  = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	reTestHexText    = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*Tj|\[([^\]]*)\]\s*TJ`)
	reTestHexElement = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
)

func countPDFPages(data []byte) int {
	return len(reTestPDFPage.FindAll(data, -1))
}

func extractPDFUnicodeText(data []byte) string {
	cmap := make(map[uint16]string)
	var contentStreams []string
	for _, m := range reTestPDFStream.FindAllSubmatch(data, -1) {
		raw := m[1]
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			if dec, rErr := io.ReadAll(zr); rErr == nil {
				raw = dec
			}
			_ = zr.Close()
		}
		s := string(raw)
		if strings.Contains(s, "begincmap") {
			for _, block := range reTestBFChar.FindAllStringSubmatch(s, -1) {
				for _, pair := range reTestHexPair.FindAllStringSubmatch(block[1], -1) {
					cid, err := strconv.ParseUint(pair[1], 16, 16)
					if err != nil {
						continue
					}
					if b, dErr := hex.DecodeString(pair[2]); dErr == nil && len(b)%2 == 0 {
						u16 := make([]uint16, len(b)/2)
						for i := 0; i < len(b); i += 2 {
							u16[i/2] = uint16(b[i])<<8 | uint16(b[i+1])
						}
						cmap[uint16(cid)] = string(utf16.Decode(u16))
					}
				}
			}
			for _, block := range reTestBFRange.FindAllStringSubmatch(s, -1) {
				for _, rng := range reTestRangeLine.FindAllStringSubmatch(block[1], -1) {
					sCID, e1 := strconv.ParseUint(rng[1], 16, 16)
					eCID, e2 := strconv.ParseUint(rng[2], 16, 16)
					base, e3 := strconv.ParseUint(rng[3], 16, 32)
					if e1 != nil || e2 != nil || e3 != nil {
						continue
					}
					for cid := sCID; cid <= eCID; cid++ {
						cmap[uint16(cid)] = string(rune(base + (cid - sCID)))
					}
				}
			}
		} else if strings.Contains(s, "BT") {
			contentStreams = append(contentStreams, s)
		}
	}

	decodeHex := func(h string) string {
		b, err := hex.DecodeString(h)
		if err != nil || len(b)%2 != 0 {
			return ""
		}
		var sb strings.Builder
		for i := 0; i < len(b); i += 2 {
			cid := uint16(b[i])<<8 | uint16(b[i+1])
			sb.WriteString(cmap[cid])
		}
		return sb.String()
	}

	var out strings.Builder
	for _, cs := range contentStreams {
		for _, m := range reTestHexText.FindAllStringSubmatch(cs, -1) {
			if m[1] != "" {
				out.WriteString(decodeHex(m[1]))
				out.WriteByte('\n')
			} else if m[2] != "" {
				for _, tok := range reTestHexElement.FindAllStringSubmatch(m[2], -1) {
					out.WriteString(decodeHex(tok[1]))
				}
				out.WriteByte('\n')
			}
		}
	}
	return out.String()
}
