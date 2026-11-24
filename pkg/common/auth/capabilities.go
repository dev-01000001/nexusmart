package auth

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Canonical user and internal staff roles (UC-A11, F9/F10).
const (
	RoleBuyer      = "buyer"
	RoleSeller     = "seller"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "super_admin"
	RoleModerator  = "moderator"
	RoleCSKH       = "cskh"
	RoleAccountant = "accountant"
	RoleOps        = "ops"
)

// Canonical admin/internal RBAC capabilities mapped in capabilities.json (F9).
const (
	CapUsersRead             = "users.read"
	CapUsersStatusWrite      = "users.status.write"
	CapUsersRoleWrite        = "users.role.write"
	CapStaffCreate           = "staff.create"
	CapShopsRead             = "shops.read"
	CapShopsReview           = "shops.review"
	CapShopsSuspendRequest   = "shops.suspend.request"
	CapShopsSuspendDecide    = "shops.suspend.decide"
	CapShopsUnsuspend        = "shops.unsuspend"
	CapShopsApprovalsRead    = "shops.approvals.read"
	CapShopsBankRead         = "shops.bank.read"
	CapShopsBankReview       = "shops.bank.review"
	CapAuditLogsRead         = "audit_logs.read"
	CapCategoriesRead        = "categories.read"
	CapCategoriesWrite       = "categories.write"
	CapFeesCategoryRead      = "fees.category.read"
	CapFeesCategoryWrite     = "fees.category.write"
	CapProductsRead          = "products.read"
	CapProductsReview        = "products.review"
	CapBannedKeywordsRead    = "banned_keywords.read"
	CapBannedKeywordsWrite   = "banned_keywords.write"
	CapVouchersRead          = "vouchers.read"
	CapVouchersWrite         = "vouchers.write"
	CapFlashSalesRead        = "flash_sales.read"
	CapFlashSalesWrite       = "flash_sales.write"
	CapFlashSalesItemApprove = "flash_sales.items.approve"
	CapCoinsCredit           = "coins.credit"
	CapOrdersRead            = "orders.read"
	CapOrdersReviewResolve   = "orders.review.resolve"
	CapReportsRead           = "reports.read"
	CapReturnsRead           = "returns.read"
	CapReturnsResolve        = "returns.resolve"
	CapFeesPaymentRead       = "fees.payment.read"
	CapFeesPaymentWrite      = "fees.payment.write"
	CapLedgerRead            = "ledger.read"
	CapReconciliationWrite   = "reconciliation.write"
	CapEscrowRelease         = "escrow.release"
	CapRefundsRead           = "refunds.read"
	CapRefundsCreate         = "refunds.create"
	CapRefundsApprove        = "refunds.approve"
	CapRefundsSettle         = "refunds.settle"
	CapCarriersRead          = "carriers.read"
	CapCarriersWrite         = "carriers.write"
	CapPaymentMethodsRead    = "payment_methods.read"
	CapPaymentMethodsWrite   = "payment_methods.write"
	CapApprovalsRead         = "approvals.read"
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type capabilityMatrixFile struct {
	Roles        []string            `json:"roles"`
	Capabilities map[string][]string `json:"capabilities"`
}

var (
	staffRolesList     []string
	staffRolesSet      map[string]bool
	capabilityRolesMap map[string][]string
	capabilityAllowSet map[string]map[string]bool
)

func init() {
	var parsed capabilityMatrixFile
	if err := json.Unmarshal(capabilitiesJSON, &parsed); err != nil {
		panic(fmt.Sprintf("auth: parse embedded capabilities.json: %v", err))
	}

	staffRolesList = make([]string, 0, len(parsed.Roles))
	staffRolesSet = make(map[string]bool, len(parsed.Roles))
	for _, r := range parsed.Roles {
		norm := strings.ToLower(strings.TrimSpace(r))
		if norm == "" || staffRolesSet[norm] {
			continue
		}
		staffRolesList = append(staffRolesList, norm)
		staffRolesSet[norm] = true
	}

	capabilityRolesMap = make(map[string][]string, len(parsed.Capabilities))
	capabilityAllowSet = make(map[string]map[string]bool, len(parsed.Capabilities))
	for capName, roles := range parsed.Capabilities {
		cleanCap := strings.TrimSpace(capName)
		if cleanCap == "" {
			panic("auth: empty capability name in capabilities.json")
		}
		roleList := make([]string, 0, len(roles))
		roleSet := make(map[string]bool, len(roles))
		for _, r := range roles {
			norm := strings.ToLower(strings.TrimSpace(r))
			if !staffRolesSet[norm] {
				panic(fmt.Sprintf("auth: capability %q references unknown role %q", cleanCap, r))
			}
			if roleSet[norm] {
				continue
			}
			roleList = append(roleList, norm)
			roleSet[norm] = true
		}
		capabilityRolesMap[cleanCap] = roleList
		capabilityAllowSet[cleanCap] = roleSet
	}
}

// StaffRoles returns a copy of all internal/admin role identifiers from capabilities.json.
func StaffRoles() []string {
	out := make([]string, len(staffRolesList))
	copy(out, staffRolesList)
	return out
}

// IsStaffRole reports whether role is one of the internal/admin roles.
func IsStaffRole(role string) bool {
	return staffRolesSet[strings.ToLower(strings.TrimSpace(role))]
}

// RoleRank returns the authority rank used for F10 hierarchy enforcement:
// super_admin (40) > admin (30) > specialist staff (20) > buyer/seller (10).
func RoleRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleSuperAdmin:
		return 40
	case RoleAdmin:
		return 30
	case RoleModerator, RoleCSKH, RoleAccountant, RoleOps:
		return 20
	case RoleSeller, RoleBuyer:
		return 10
	default:
		return 0
	}
}

// CanActOnTargetRole enforces F10: an actor may only mutate accounts whose role rank is strictly
// lower than the actor's own rank (prevents peer/upward actions, e.g. cskh locking staff or admin locking admin).
func CanActOnTargetRole(actorRole, targetRole string) bool {
	ar := RoleRank(actorRole)
	tr := RoleRank(targetRole)
	return ar > 0 && tr > 0 && ar > tr
}

// RoleHasCapability reports whether the given role grants capability cap.
func RoleHasCapability(role, capName string) bool {
	allowed, ok := capabilityAllowSet[capName]
	if !ok {
		return false
	}
	return allowed[strings.ToLower(strings.TrimSpace(role))]
}

// HasCapability reports whether an authenticated end-user JWT grants capability cap (F9).
// Internal service tokens (claims.Service != "") are always rejected so service tokens
// cannot accidentally satisfy end-user admin RBAC checks.
func HasCapability(claims *Claims, capName string) bool {
	if claims == nil || claims.Service != "" {
		return false
	}
	return RoleHasCapability(claims.Role, capName)
}

// CapabilityRoles returns a copy of the roles permitted for capability cap (empty if unknown).
func CapabilityRoles(capName string) []string {
	roles, ok := capabilityRolesMap[capName]
	if !ok {
		return nil
	}
	out := make([]string, len(roles))
	copy(out, roles)
	return out
}

// AllCapabilities returns a sorted list of all defined capabilities in capabilities.json.
func AllCapabilities() []string {
	out := make([]string, 0, len(capabilityRolesMap))
	for c := range capabilityRolesMap {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// RequireCapabilityHTTP enforces a capability from capabilities.json on HTTP handlers
// after HTTPMiddleware has populated Claims.
func RequireCapabilityHTTP(capName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
				return
			}
			if !HasCapability(claims, capName) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
