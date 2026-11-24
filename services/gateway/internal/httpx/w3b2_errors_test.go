package httpx

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapGRPCError_W3B2CanonicalDomainCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "ShopSuspended_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is currently suspended"),
			wantStatus: http.StatusConflict,
			wantCode:   "SHOP_SUSPENDED",
		},
		{
			name:       "PaymentMethodDisabled_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "PAYMENT_METHOD_DISABLED: ZALOPAY is currently disabled"),
			wantStatus: http.StatusConflict,
			wantCode:   "PAYMENT_METHOD_DISABLED",
		},
		{
			name:       "OrderAmountOutOfRange_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "ORDER_AMOUNT_OUT_OF_RANGE: total must be within method bounds"),
			wantStatus: http.StatusConflict,
			wantCode:   "ORDER_AMOUNT_OUT_OF_RANGE",
		},
		{
			name:       "VersionConflict_FailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "VERSION_CONFLICT: expected_version 1 does not match 2"),
			wantStatus: http.StatusConflict,
			wantCode:   "VERSION_CONFLICT",
		},
		{
			name:       "MakerCheckerConflict_PermissionDenied",
			err:        status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: checker must be different from maker"),
			wantStatus: http.StatusForbidden,
			wantCode:   "MAKER_CHECKER_CONFLICT",
		},
		{
			name:       "MakerCheckerConflictExact_PermissionDenied",
			err:        status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT"),
			wantStatus: http.StatusForbidden,
			wantCode:   "MAKER_CHECKER_CONFLICT",
		},
		{
			name:       "PlainPermissionDeniedUnchanged",
			err:        status.Error(codes.PermissionDenied, "insufficient permissions"),
			wantStatus: http.StatusForbidden,
			wantCode:   "PERMISSION_DENIED",
		},
		{
			name:       "UnrelatedMentionDoesNotHijackPermissionDenied",
			err:        status.Error(codes.PermissionDenied, "caller failed check near MAKER_CHECKER_CONFLICT"),
			wantStatus: http.StatusForbidden,
			wantCode:   "PERMISSION_DENIED",
		},
		{
			name:       "WrongGRPCCodeDoesNotMapMakerChecker",
			err:        status.Error(codes.FailedPrecondition, "MAKER_CHECKER_CONFLICT: wrong code"),
			wantStatus: http.StatusConflict,
			wantCode:   "FAILED_PRECONDITION",
		},
		{
			name:       "UnrelatedMentionDoesNotHijackFailedPrecondition",
			err:        status.Error(codes.FailedPrecondition, "reason mentions SHOP_SUSPENDED in middle"),
			wantStatus: http.StatusConflict,
			wantCode:   "FAILED_PRECONDITION",
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
