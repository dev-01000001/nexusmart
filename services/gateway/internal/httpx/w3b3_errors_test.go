package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapGRPCError_W3B3CanonicalDomainCodes(t *testing.T) {
	// Covers ALL 27 canonical domain error codes in docs/contracts.md §7 (line 698)
	// with their exact gRPC status code and HTTP status code.
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		// 400 Bad Request (InvalidArgument) — 6 codes
		{"BannedContent", status.Error(codes.InvalidArgument, "BANNED_CONTENT: matched banned keyword"), http.StatusBadRequest, "BANNED_CONTENT"},
		{"ProhibitedCategory", status.Error(codes.InvalidArgument, "PROHIBITED_CATEGORY: category is prohibited"), http.StatusBadRequest, "PROHIBITED_CATEGORY"},
		{"ImportInvalidHeader", status.Error(codes.InvalidArgument, "IMPORT_INVALID_HEADER: missing column price_vnd"), http.StatusBadRequest, "IMPORT_INVALID_HEADER"},
		{"ImportTooManyRows", status.Error(codes.InvalidArgument, "IMPORT_TOO_MANY_ROWS: max 1000 rows"), http.StatusBadRequest, "IMPORT_TOO_MANY_ROWS"},
		{"ImportFileTooLarge", status.Error(codes.InvalidArgument, "IMPORT_FILE_TOO_LARGE: exceeds 1 MiB"), http.StatusBadRequest, "IMPORT_FILE_TOO_LARGE"},
		{"ImportInvalidUTF8", status.Error(codes.InvalidArgument, "IMPORT_INVALID_UTF8: invalid utf-8 byte"), http.StatusBadRequest, "IMPORT_INVALID_UTF8"},

		// 403 Forbidden (PermissionDenied) — 1 code
		{"MakerCheckerConflict", status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: maker cannot approve own request"), http.StatusForbidden, "MAKER_CHECKER_CONFLICT"},

		// 404 Not Found (NotFound) — 1 code
		{"OrderLookupNotFound", status.Error(codes.NotFound, "ORDER_LOOKUP_NOT_FOUND: order not found"), http.StatusNotFound, "ORDER_LOOKUP_NOT_FOUND"},
		{"PlainNotFoundUnchanged", status.Error(codes.NotFound, "order not found"), http.StatusNotFound, "NOT_FOUND"},

		// 409 Conflict (FailedPrecondition) — 19 codes
		{"SKUInactive", status.Error(codes.FailedPrecondition, "SKU_INACTIVE: sku is not active"), http.StatusConflict, "SKU_INACTIVE"},
		{"CODPhoneNotVerified", status.Error(codes.FailedPrecondition, "COD_PHONE_NOT_VERIFIED: verify phone before COD"), http.StatusConflict, "COD_PHONE_NOT_VERIFIED"},
		{"CODAreaNotSupported", status.Error(codes.FailedPrecondition, "COD_AREA_NOT_SUPPORTED: ward does not support COD"), http.StatusConflict, "COD_AREA_NOT_SUPPORTED"},
		{"CODRejectedLimitExceeded", status.Error(codes.FailedPrecondition, "COD_REJECTED_LIMIT_EXCEEDED: too many rejected COD orders"), http.StatusConflict, "COD_REJECTED_LIMIT_EXCEEDED"},
		{"OwnerNameChanged", status.Error(codes.FailedPrecondition, "OWNER_NAME_CHANGED: shop owner name changed"), http.StatusConflict, "OWNER_NAME_CHANGED"},
		{"ShopSuspended", status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended"), http.StatusConflict, "SHOP_SUSPENDED"},
		{"PaymentMethodDisabled", status.Error(codes.FailedPrecondition, "PAYMENT_METHOD_DISABLED: method disabled"), http.StatusConflict, "PAYMENT_METHOD_DISABLED"},
		{"OrderAmountOutOfRange", status.Error(codes.FailedPrecondition, "ORDER_AMOUNT_OUT_OF_RANGE: exceeds max_order_vnd"), http.StatusConflict, "ORDER_AMOUNT_OUT_OF_RANGE"},
		{"VersionConflict", status.Error(codes.FailedPrecondition, "VERSION_CONFLICT: expected_version mismatch"), http.StatusConflict, "VERSION_CONFLICT"},
		{"AddressUpdateNotAllowed", status.Error(codes.FailedPrecondition, "ADDRESS_UPDATE_NOT_ALLOWED: status is SHIPPING"), http.StatusConflict, "ADDRESS_UPDATE_NOT_ALLOWED"},
		{"AddressUpdateShipmentInProgress", status.Error(codes.FailedPrecondition, "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS: shipment job queued"), http.StatusConflict, "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS"},
		{"AddressUpdateCarrierPickedUp", status.Error(codes.FailedPrecondition, "ADDRESS_UPDATE_CARRIER_PICKED_UP: carrier picked up"), http.StatusConflict, "ADDRESS_UPDATE_CARRIER_PICKED_UP"},
		{"AddressFeeIncreasePrepaid", status.Error(codes.FailedPrecondition, "ADDRESS_FEE_INCREASE_PREPAID: prepaid surcharge not supported"), http.StatusConflict, "ADDRESS_FEE_INCREASE_PREPAID"},
		{"AddressFeeConfirmationRequired", status.Error(codes.FailedPrecondition, "ADDRESS_FEE_CONFIRMATION_REQUIRED: accept_fee_increase required"), http.StatusConflict, "ADDRESS_FEE_CONFIRMATION_REQUIRED"},
		{"AddressFeeChanged", status.Error(codes.FailedPrecondition, `ADDRESS_FEE_CHANGED: {"old_shipping_fee_vnd":20000,"new_shipping_fee_vnd":35000,"fee_delta_vnd":15000,"new_total_payable_vnd":265000}`), http.StatusConflict, "ADDRESS_FEE_CHANGED"},
		{"CODAlreadyCollected", status.Error(codes.FailedPrecondition, "COD_ALREADY_COLLECTED: cod already collected"), http.StatusConflict, "COD_ALREADY_COLLECTED"},
		{"ImportJobNotReady", status.Error(codes.FailedPrecondition, "IMPORT_JOB_NOT_READY: job status is VALIDATING"), http.StatusConflict, "IMPORT_JOB_NOT_READY"},
		{"ImportJobInProgress", status.Error(codes.FailedPrecondition, "IMPORT_JOB_IN_PROGRESS: shop already has an active import job"), http.StatusConflict, "IMPORT_JOB_IN_PROGRESS"},
		{"ReindexAlreadyRunning", status.Error(codes.FailedPrecondition, "REINDEX_ALREADY_RUNNING: another reindex holds lock"), http.StatusConflict, "REINDEX_ALREADY_RUNNING"},

		// Negative checks: wrong gRPC code or non-prefix mention must NOT hijack code.
		{"WrongCodeForOrderLookup", status.Error(codes.InvalidArgument, "ORDER_LOOKUP_NOT_FOUND: wrong code"), http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"WrongCodeForImportHeader", status.Error(codes.FailedPrecondition, "IMPORT_INVALID_HEADER: wrong code"), http.StatusConflict, "FAILED_PRECONDITION"},
		{"WrongCodeForImportJobInProgress", status.Error(codes.InvalidArgument, "IMPORT_JOB_IN_PROGRESS: wrong code"), http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"MiddleMentionNotExtracted", status.Error(codes.FailedPrecondition, "cannot proceed near REINDEX_ALREADY_RUNNING"), http.StatusConflict, "FAILED_PRECONDITION"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotCode, gotMsg := MapGRPCError(tc.err)
			if gotStatus != tc.wantStatus || gotCode != tc.wantCode {
				t.Fatalf("MapGRPCError(%v) = (%d, %q, %q), want (%d, %q)",
					tc.err, gotStatus, gotCode, gotMsg, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

func TestWriteGRPCError_AddressFeeChangedIncludesQuoteObject(t *testing.T) {
	// G-b: Canonical single format emitted by order service: `ADDRESS_FEE_CHANGED: {json}`.
	// Response must carry structured `quote` AND omit raw JSON blob from `error.message`.
	err := status.Error(codes.FailedPrecondition, `ADDRESS_FEE_CHANGED: {"old_shipping_fee_vnd":20000,"new_shipping_fee_vnd":35000,"fee_delta_vnd":15000,"new_total_payable_vnd":265000}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/ord-1/address", nil)
	WriteGRPCError(rec, req, err)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error ErrorBody        `json:"error"`
		Quote *AddressFeeQuote `json:"quote"`
	}
	if uErr := json.Unmarshal(rec.Body.Bytes(), &payload); uErr != nil {
		t.Fatalf("unmarshal response %q: %v", rec.Body.String(), uErr)
	}
	if payload.Error.Code != "ADDRESS_FEE_CHANGED" {
		t.Fatalf("error.code = %q, want ADDRESS_FEE_CHANGED", payload.Error.Code)
	}
	if strings.Contains(payload.Error.Message, "{") || strings.Contains(payload.Error.Message, "old_shipping_fee_vnd") {
		t.Fatalf("error.message must not leak raw JSON quote blob: %q", payload.Error.Message)
	}
	if payload.Error.Message == "" {
		t.Fatalf("error.message must be non-empty human-readable message")
	}
	if payload.Quote == nil {
		t.Fatalf("expected non-nil quote object in 409 ADDRESS_FEE_CHANGED response: %s", rec.Body.String())
	}
	if payload.Quote.OldShippingFeeVND != 20000 ||
		payload.Quote.NewShippingFeeVND != 35000 ||
		payload.Quote.FeeDeltaVND != 15000 ||
		payload.Quote.NewTotalPayableVND != 265000 {
		t.Fatalf("quote = %+v, want {20000 35000 15000 265000}", payload.Quote)
	}

	// Non-JSON message must not fabricate a quote via legacy KV regex fallback.
	recKV := httptest.NewRecorder()
	WriteGRPCError(recKV, req, status.Error(codes.FailedPrecondition, "ADDRESS_FEE_CHANGED: old_shipping_fee_vnd=20000 new_shipping_fee_vnd=35000"))
	var payloadKV struct {
		Error ErrorBody        `json:"error"`
		Quote *AddressFeeQuote `json:"quote"`
	}
	if uErr := json.Unmarshal(recKV.Body.Bytes(), &payloadKV); uErr != nil {
		t.Fatalf("unmarshal KV response: %v", uErr)
	}
	if payloadKV.Error.Code != "ADDRESS_FEE_CHANGED" || payloadKV.Quote != nil {
		t.Fatalf("non-JSON message should not parse quote via KV regex, got %+v", payloadKV)
	}
}
