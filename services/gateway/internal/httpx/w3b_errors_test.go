package httpx

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapGRPCError_W3BCanonicalDomainCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "BannedContent_InvalidArgument",
			err:        status.Error(codes.InvalidArgument, "BANNED_CONTENT: matched prohibited term"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "BANNED_CONTENT",
		},
		{
			name:       "ProhibitedCategory_InvalidArgument",
			err:        status.Error(codes.InvalidArgument, "PROHIBITED_CATEGORY: category is prohibited"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "PROHIBITED_CATEGORY",
		},
		{
			name:       "SKUInactive_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "SKU_INACTIVE: sku-1 is not active"),
			wantStatus: http.StatusConflict,
			wantCode:   "SKU_INACTIVE",
		},
		{
			name:       "CODPhoneNotVerified_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "COD_PHONE_NOT_VERIFIED: verify phone before COD"),
			wantStatus: http.StatusConflict,
			wantCode:   "COD_PHONE_NOT_VERIFIED",
		},
		{
			name:       "CODAreaNotSupported_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "COD_AREA_NOT_SUPPORTED: destination ward does not support COD"),
			wantStatus: http.StatusConflict,
			wantCode:   "COD_AREA_NOT_SUPPORTED",
		},
		{
			name:       "CODRejectedLimitExceeded_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "COD_REJECTED_LIMIT_EXCEEDED: rejection rate too high"),
			wantStatus: http.StatusConflict,
			wantCode:   "COD_REJECTED_LIMIT_EXCEEDED",
		},
		{
			name:       "OwnerNameChanged_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "OWNER_NAME_CHANGED: owner full_name changed"),
			wantStatus: http.StatusConflict,
			wantCode:   "OWNER_NAME_CHANGED",
		},
		{
			name:       "PlainInvalidArgumentUnchanged",
			err:        status.Error(codes.InvalidArgument, "rating must be 1..5"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_ARGUMENT",
		},
		{
			name:       "CheckoutFailedRemainsFailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "CHECKOUT_FAILED (EXPIRED): retry with new key"),
			wantStatus: http.StatusConflict,
			wantCode:   "FAILED_PRECONDITION",
		},
		{
			name:       "UnrelatedMentionDoesNotHijackCode",
			err:        status.Error(codes.InvalidArgument, "note must not mention BANNED_CONTENT literally"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_ARGUMENT",
		},
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
