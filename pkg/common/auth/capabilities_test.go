package auth

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
)

func TestCapabilityMatrixExhaustive(t *testing.T) {
	// Every capability in capabilities.json must have a corresponding Go constant and vice versa,
	// and must be tested explicitly across all 6 staff roles + buyer + seller (F9).
	expected := map[string][]string{
		CapUsersRead:             {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps},
		CapUsersStatusWrite:      {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleOps},
		CapUsersRoleWrite:        {RoleSuperAdmin},
		CapStaffCreate:           {RoleSuperAdmin},
		CapShopsRead:             {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps},
		CapShopsReview:           {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapShopsSuspendRequest:   {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapShopsSuspendDecide:    {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapShopsUnsuspend:        {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapShopsApprovalsRead:    {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapShopsBankRead:         {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapShopsBankReview:       {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapAuditLogsRead:         {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps},
		CapCategoriesRead:        {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleAccountant, RoleOps},
		CapCategoriesWrite:       {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapFeesCategoryRead:      {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapFeesCategoryWrite:     {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapProductsRead:          {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleOps},
		CapProductsReview:        {RoleSuperAdmin, RoleAdmin, RoleModerator},
		CapBannedKeywordsRead:    {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapBannedKeywordsWrite:   {RoleSuperAdmin, RoleAdmin, RoleModerator},
		CapVouchersRead:          {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleAccountant, RoleOps},
		CapVouchersWrite:         {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapFlashSalesRead:        {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapFlashSalesWrite:       {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapFlashSalesItemApprove: {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleOps},
		CapCoinsCredit:           {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapOrdersRead:            {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleAccountant, RoleOps},
		CapOrdersReviewResolve:   {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapReportsRead:           {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapReturnsRead:           {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleAccountant, RoleOps},
		CapReturnsResolve:        {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleOps},
		CapFeesPaymentRead:       {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapFeesPaymentWrite:      {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapLedgerRead:            {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapReconciliationWrite:   {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapEscrowRelease:         {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapRefundsRead:           {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleAccountant, RoleOps},
		CapRefundsCreate:         {RoleSuperAdmin, RoleAdmin, RoleCSKH, RoleAccountant},
		CapRefundsApprove:        {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapRefundsSettle:         {RoleSuperAdmin, RoleAdmin, RoleAccountant},
		CapCarriersRead:          {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapCarriersWrite:         {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapPaymentMethodsRead:    {RoleSuperAdmin, RoleAdmin, RoleAccountant, RoleOps},
		CapPaymentMethodsWrite:   {RoleSuperAdmin, RoleAdmin, RoleOps},
		CapApprovalsRead:         {RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps},
		CapSearchReindex:         {RoleSuperAdmin, RoleAdmin, RoleOps},
	}

	allCaps := AllCapabilities()
	if len(allCaps) != len(expected) {
		t.Fatalf("capabilities.json count=%d does not match expected test table count=%d", len(allCaps), len(expected))
	}

	allRoles := []string{
		RoleSuperAdmin,
		RoleAdmin,
		RoleModerator,
		RoleCSKH,
		RoleAccountant,
		RoleOps,
		RoleBuyer,
		RoleSeller,
		"",
		"unknown",
	}

	for _, capName := range allCaps {
		wantRoles, ok := expected[capName]
		if !ok {
			t.Fatalf("capability %q present in capabilities.json but missing from expected test table", capName)
		}
		gotRoles := CapabilityRoles(capName)
		wantSorted := append([]string(nil), wantRoles...)
		gotSorted := append([]string(nil), gotRoles...)
		sort.Strings(wantSorted)
		sort.Strings(gotSorted)
		if !reflect.DeepEqual(gotSorted, wantSorted) {
			t.Errorf("capability %q roles = %v, want %v", capName, gotSorted, wantSorted)
		}

		allowSet := make(map[string]bool, len(wantRoles))
		for _, r := range wantRoles {
			allowSet[r] = true
		}

		for _, role := range allRoles {
			want := allowSet[role]
			claims := &Claims{Role: role}
			if got := HasCapability(claims, capName); got != want {
				t.Errorf("HasCapability(role=%q, cap=%q) = %v, want %v", role, capName, got, want)
			}
			if got := RoleHasCapability(role, capName); got != want {
				t.Errorf("RoleHasCapability(role=%q, cap=%q) = %v, want %v", role, capName, got, want)
			}
		}

		// Service tokens MUST NEVER satisfy end-user capabilities even though ServiceRole == "admin".
		svcClaims := &Claims{Role: ServiceRole, Service: "order-service"}
		if HasCapability(svcClaims, capName) {
			t.Errorf("service token must not satisfy end-user capability %q", capName)
		}
	}

	// Nil claims and unknown capabilities always fail closed.
	if HasCapability(nil, CapUsersRead) {
		t.Error("nil claims must return false")
	}
	if HasCapability(&Claims{Role: RoleSuperAdmin}, "nonexistent.capability") {
		t.Error("unknown capability must return false even for super_admin")
	}
}

func TestStaffRolesAndRoleHierarchyF10(t *testing.T) {
	wantStaff := []string{RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps}
	if got := StaffRoles(); !reflect.DeepEqual(got, wantStaff) {
		t.Fatalf("StaffRoles() = %v, want %v", got, wantStaff)
	}
	for _, r := range wantStaff {
		if !IsStaffRole(r) {
			t.Errorf("IsStaffRole(%q) = false, want true", r)
		}
	}
	for _, r := range []string{RoleBuyer, RoleSeller, "", "guest"} {
		if IsStaffRole(r) {
			t.Errorf("IsStaffRole(%q) = true, want false", r)
		}
	}

	// F10 hierarchy: actor must have strictly higher rank than target.
	cases := []struct {
		actor  string
		target string
		want   bool
	}{
		{RoleSuperAdmin, RoleAdmin, true},
		{RoleSuperAdmin, RoleModerator, true},
		{RoleSuperAdmin, RoleBuyer, true},
		{RoleSuperAdmin, RoleSuperAdmin, false},
		{RoleAdmin, RoleModerator, true},
		{RoleAdmin, RoleCSKH, true},
		{RoleAdmin, RoleBuyer, true},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleSuperAdmin, false},
		{RoleCSKH, RoleBuyer, true},
		{RoleCSKH, RoleSeller, true},
		{RoleCSKH, RoleModerator, false},
		{RoleCSKH, RoleCSKH, false},
		{RoleCSKH, RoleAccountant, false},
		{RoleCSKH, RoleOps, false},
		{RoleCSKH, RoleAdmin, false},
		{RoleCSKH, RoleSuperAdmin, false},
		{RoleOps, RoleBuyer, true},
		{RoleOps, RoleSeller, true},
		{RoleOps, RoleCSKH, false},
		{RoleOps, RoleAdmin, false},
		{RoleBuyer, RoleBuyer, false},
		{"unknown", RoleBuyer, false},
	}
	for _, tc := range cases {
		if got := CanActOnTargetRole(tc.actor, tc.target); got != tc.want {
			t.Errorf("CanActOnTargetRole(%q, %q) = %v, want %v", tc.actor, tc.target, got, tc.want)
		}
	}
}

func TestRequireCapabilityHTTP(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := RequireCapabilityHTTP(CapRefundsApprove)(next)

	// Unauthenticated -> 401.
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	// CSKH cannot approve refunds -> 403.
	reqCSKH := httptest.NewRequest(http.MethodPost, "/", nil)
	reqCSKH = reqCSKH.WithContext(WithClaims(reqCSKH.Context(), &Claims{Role: RoleCSKH}))
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, reqCSKH)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cskh status = %d, want 403", rec.Code)
	}

	// Accountant can approve refunds -> 200.
	reqAcc := httptest.NewRequest(http.MethodPost, "/", nil)
	reqAcc = reqAcc.WithContext(WithClaims(reqAcc.Context(), &Claims{Role: RoleAccountant}))
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, reqAcc)
	if rec.Code != http.StatusOK {
		t.Fatalf("accountant status = %d, want 200", rec.Code)
	}
}
