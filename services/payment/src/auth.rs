//! gRPC and internal HTTP caller authentication + capability-based RBAC (F9).
//! Identity comes only from a verified HS256 JWT in the `authorization` metadata/header;
//! plain `x-user-id` / `x-user-role` / `x-shop-id` are never trusted.
//!
//! Staff role capabilities are loaded at compile time from the single shared matrix
//! `pkg/common/auth/capabilities.json` (F9). Service tokens satisfy ZERO staff capabilities
//! and are only accepted on explicitly listed `SERVICE_METHODS`.
//!
//! ponytail: one shared HS256 service key (same scheme as pkg/common/auth in Go); any holder can
//! mint any service identity. Upgrade path: mTLS between services, or asymmetric per-service JWT
//! (EdDSA) verified via JWKS.

use std::collections::{HashMap, HashSet};
use std::sync::OnceLock;

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hmac::{Hmac, Mac};
use serde::Deserialize;
use sha2::Sha256;
use tonic::metadata::MetadataMap;
use tonic::Status;

use crate::domain::PaymentWithAllocations;
use crate::error::PaymentError;

/// Shared single-source-of-truth RBAC matrix (also consumed by Go `pkg/common/auth`).
const CAPABILITIES_JSON: &str = include_str!("../../../pkg/common/auth/capabilities.json");

// Payment-service capability identifiers (exact keys in `pkg/common/auth/capabilities.json`).
pub const CAP_FEES_PAYMENT_READ: &str = "fees.payment.read";
pub const CAP_FEES_PAYMENT_WRITE: &str = "fees.payment.write";
pub const CAP_LEDGER_READ: &str = "ledger.read";
pub const CAP_RECONCILIATION_WRITE: &str = "reconciliation.write";
pub const CAP_ESCROW_RELEASE: &str = "escrow.release";
pub const CAP_REFUNDS_READ: &str = "refunds.read";
pub const CAP_REFUNDS_CREATE: &str = "refunds.create";
pub const CAP_REFUNDS_APPROVE: &str = "refunds.approve";
pub const CAP_REFUNDS_SETTLE: &str = "refunds.settle";
pub const CAP_PAYMENT_METHODS_READ: &str = "payment_methods.read";
pub const CAP_PAYMENT_METHODS_WRITE: &str = "payment_methods.write";

/// Per-RPC allowlist of internal services authorized to authenticate with a service token
/// (verified via `claims.service` signed by `SERVICE_JWT_SECRET`) — p4 Item M.
/// Audited across all `services/*`: only `order-service` calls `PaymentService` with a service
/// token (gateway forwards end-user JWTs only; other services do not hold a `PaymentClient`).
const SERVICE_METHOD_ALLOWLIST: &[(&str, &[&str])] = &[
    ("CreatePayment", &["order-service"]),
    ("GetPayment", &["order-service"]),
    ("CancelPayment", &["order-service"]),
    ("CreateRefund", &["order-service"]),
    ("GetFeeConfig", &["order-service"]),
    ("GetPaymentMethodConfigs", &["order-service"]),
    ("AdjustCODShippingFee", &["order-service"]),
];

#[derive(Deserialize)]
struct RawCapabilitiesFile {
    roles: Vec<String>,
    capabilities: HashMap<String, Vec<String>>,
}

struct CapabilityMatrix {
    roles: Vec<String>,
    role_set: HashSet<String>,
    capabilities: HashMap<String, HashSet<String>>,
}

impl CapabilityMatrix {
    fn parse(raw_json: &str) -> Self {
        let parsed: RawCapabilitiesFile = serde_json::from_str(raw_json)
            .expect("pkg/common/auth/capabilities.json must be valid JSON");
        assert!(
            !parsed.roles.is_empty(),
            "capabilities.json must declare at least one staff role"
        );
        let mut roles = Vec::with_capacity(parsed.roles.len());
        let mut role_set = HashSet::with_capacity(parsed.roles.len());
        for role in parsed.roles {
            let norm = role.trim().to_ascii_lowercase();
            assert!(!norm.is_empty(), "capabilities.json contains empty role");
            if role_set.insert(norm.clone()) {
                roles.push(norm);
            }
        }
        let mut capabilities = HashMap::with_capacity(parsed.capabilities.len());
        for (cap, cap_roles) in parsed.capabilities {
            let cap_norm = cap.trim().to_string();
            assert!(
                !cap_norm.is_empty(),
                "capabilities.json contains empty capability key"
            );
            let mut allowed = HashSet::with_capacity(cap_roles.len());
            for role in cap_roles {
                let norm = role.trim().to_ascii_lowercase();
                assert!(
                    role_set.contains(&norm),
                    "capabilities.json: capability '{cap_norm}' references unknown role '{norm}'"
                );
                allowed.insert(norm);
            }
            capabilities.insert(cap_norm, allowed);
        }
        Self {
            roles,
            role_set,
            capabilities,
        }
    }
}

static CAPABILITY_MATRIX: OnceLock<CapabilityMatrix> = OnceLock::new();

fn capability_matrix() -> &'static CapabilityMatrix {
    CAPABILITY_MATRIX.get_or_init(|| CapabilityMatrix::parse(CAPABILITIES_JSON))
}

/// Returns all declared staff roles from `capabilities.json` in declaration order.
pub fn staff_roles() -> &'static [String] {
    &capability_matrix().roles
}

/// Returns true when `role` is a recognized staff role declared in `capabilities.json`.
pub fn is_staff_role(role: &str) -> bool {
    let norm = role.trim().to_ascii_lowercase();
    capability_matrix().role_set.contains(&norm)
}

/// Returns true when `role` is granted `capability` in `capabilities.json`.
pub fn role_has_capability(role: &str, capability: &str) -> bool {
    let norm_role = role.trim().to_ascii_lowercase();
    let norm_cap = capability.trim();
    if norm_role.is_empty() || norm_cap.is_empty() {
        return false;
    }
    capability_matrix()
        .capabilities
        .get(norm_cap)
        .is_some_and(|roles| roles.contains(&norm_role))
}

/// Authenticated caller as seen by gRPC and HTTP handlers.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Caller {
    pub role: String,
    pub user_id: String,
    pub shop_id: String,
    pub is_service: bool,
    pub service: String,
    pub exp: Option<i64>,
}

impl Caller {
    /// Returns true iff caller is an authenticated `admin` or `super_admin` end-user (never a
    /// service token, never narrower staff roles like `cskh`/`accountant`/`ops`/`moderator`).
    /// Used for non-§4.11 payment-lifecycle ownership overrides (`CreatePayment`, `CancelPayment`).
    pub fn is_admin_role(&self) -> bool {
        !self.is_service && matches!(self.role.as_str(), "admin" | "super_admin")
    }

    /// Checks whether this caller holds `capability` in `capabilities.json`.
    /// Service tokens NEVER satisfy staff capabilities (F9).
    pub fn has_capability(&self, capability: &str) -> bool {
        !self.is_service && role_has_capability(&self.role, capability)
    }

    /// Checks whether this caller holds at least one capability in `capabilities`.
    /// Service tokens NEVER satisfy staff capabilities (F9).
    pub fn has_any_capability(&self, capabilities: &[&str]) -> bool {
        !self.is_service
            && capabilities
                .iter()
                .any(|cap| role_has_capability(&self.role, cap))
    }

    /// Returns true when this caller is an authenticated staff user (not a service token).
    pub fn is_staff(&self) -> bool {
        !self.is_service && is_staff_role(&self.role)
    }
}

/// Caller-scoped visibility for a payment record and its shop allocations.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PaymentViewScope {
    /// Staff with payment/refund read capability (`refunds.read` or `ledger.read`), or verified
    /// internal service token: full payment and allocations.
    Full,
    /// Buyer owner (including a seller purchasing as a buyer): all allocations with
    /// `commission_vnd = 0`; buyer QR/pay_url/provider_ref and full amount preserved.
    BuyerOwner,
    /// Seller who only owns one or more shop allocations (not the buyer owner):
    /// restricted to `shop_id`, with buyer QR/pay_url/provider_ref redacted and shop-scoped amount.
    SellerAllocation { shop_id: String },
}

/// Enforces object-level read access for a payment and resolves the caller's visibility scope
/// (staff with `refunds.read`/`ledger.read`, verified service token, payment owner regardless of
/// role — including `seller` or `moderator` shopping as a buyer — or seller with a matching shop
/// allocation). Shared by gRPC `GetPayment` and HTTP SSE.
pub fn resolve_payment_read_scope(
    caller: &Caller,
    pwa: &PaymentWithAllocations,
) -> crate::error::Result<PaymentViewScope> {
    if caller.is_service || caller.has_any_capability(&[CAP_REFUNDS_READ, CAP_LEDGER_READ]) {
        return Ok(PaymentViewScope::Full);
    }
    if !caller.user_id.is_empty() && pwa.payment.user_id == caller.user_id {
        return Ok(PaymentViewScope::BuyerOwner);
    }
    if caller.role == "seller"
        && !caller.shop_id.is_empty()
        && pwa.allocations.iter().any(|a| a.shop_id == caller.shop_id)
    {
        return Ok(PaymentViewScope::SellerAllocation {
            shop_id: caller.shop_id.clone(),
        });
    }
    Err(PaymentError::Forbidden(
        "cannot access another user's payment".to_string(),
    ))
}

/// Enforces object-level read access for a payment. Shared by gRPC `GetPayment` and HTTP SSE.
pub fn require_payment_read_access(
    caller: &Caller,
    pwa: &PaymentWithAllocations,
) -> crate::error::Result<()> {
    resolve_payment_read_scope(caller, pwa).map(|_| ())
}

/// Computes a seller's payable portion across their own allocations in a payment:
/// `sum(subtotal_vnd + shipping_fee_vnd - platform_discount_vnd)` clamped to `>= 0`.
pub fn seller_scoped_amount_vnd(pwa: &PaymentWithAllocations, shop_id: &str) -> i64 {
    pwa.allocations
        .iter()
        .filter(|a| a.shop_id == shop_id)
        .fold(0_i64, |acc, a| {
            let alloc_payable = a
                .subtotal_vnd
                .saturating_add(a.shipping_fee_vnd)
                .saturating_sub(a.platform_discount_vnd);
            acc.saturating_add(alloc_payable)
        })
        .max(0)
}

/// Resolves a safe top-level `order_id` for a seller-allocation-only view without leaking
/// another shop's sub-order ID.
pub fn seller_scoped_order_id(pwa: &PaymentWithAllocations, shop_id: &str) -> String {
    if pwa.payment.order_id == pwa.payment.order_group_id
        || pwa
            .allocations
            .iter()
            .any(|a| a.shop_id == shop_id && a.order_id == pwa.payment.order_id)
    {
        return pwa.payment.order_id.clone();
    }
    pwa.allocations
        .iter()
        .find(|a| a.shop_id == shop_id)
        .map(|a| a.order_id.clone())
        .unwrap_or_default()
}

#[derive(Deserialize)]
struct Claims {
    #[serde(default)]
    sub: String,
    #[serde(default)]
    role: String,
    #[serde(default)]
    shop_id: String,
    #[serde(default)]
    service: String,
    exp: i64,
}

/// Verifies end-user JWTs (user key) and internal service tokens (separate service key).
#[derive(Clone)]
pub struct GrpcAuth {
    user_key: Vec<u8>,
    service_key: Vec<u8>,
}

impl GrpcAuth {
    pub fn new(user_secret: &str, service_secret: &str) -> anyhow::Result<Self> {
        if user_secret.trim().is_empty() || service_secret.trim().is_empty() {
            anyhow::bail!("JWT_SECRET and SERVICE_JWT_SECRET must both be set");
        }
        // Eagerly validate the embedded capability matrix at startup.
        let _ = capability_matrix();
        Ok(Self {
            user_key: user_secret.as_bytes().to_vec(),
            service_key: service_secret.as_bytes().to_vec(),
        })
    }

    /// Authenticates from a raw `Authorization` header value (`Bearer <jwt>`) for `method`.
    pub fn authenticate_header(
        &self,
        authorization: Option<&str>,
        method: &str,
    ) -> crate::error::Result<Caller> {
        let token = extract_bearer(authorization)
            .ok_or_else(|| PaymentError::Unauthorized("authentication required".to_string()))?;

        if let Some(c) = verify_hs256(token, &self.user_key).filter(|c| c.service.trim().is_empty())
        {
            return to_caller(c, false);
        }
        if let Some(c) =
            verify_hs256(token, &self.service_key).filter(|c| !c.service.trim().is_empty())
        {
            let Some((_, allowed_services)) = SERVICE_METHOD_ALLOWLIST
                .iter()
                .find(|(rpc, _)| *rpc == method)
            else {
                return Err(PaymentError::Forbidden(
                    "service identity not allowed for this RPC".to_string(),
                ));
            };
            let caller_service = c.service.trim();
            if !allowed_services.contains(&caller_service) {
                return Err(PaymentError::Forbidden(format!(
                    "service '{caller_service}' is not allowed for RPC '{method}'"
                )));
            }
            return to_caller(c, true);
        }
        Err(PaymentError::Unauthorized(
            "invalid access token".to_string(),
        ))
    }

    /// Authenticates the caller of `method` (bare RPC name, e.g. "GetPayment").
    #[allow(clippy::result_large_err)]
    pub fn authenticate(&self, md: &MetadataMap, method: &str) -> Result<Caller, Status> {
        let header = md.get("authorization").and_then(|v| v.to_str().ok());
        self.authenticate_header(header, method)
            .map_err(|e| e.to_tonic_status())
    }

    /// Authenticates when an `authorization` header is present and non-empty; returns `Ok(None)`
    /// for unauthenticated public callers (used by `GetPaymentMethodConfigs`).
    #[allow(clippy::result_large_err)]
    pub fn authenticate_optional(
        &self,
        md: &MetadataMap,
        method: &str,
    ) -> Result<Option<Caller>, Status> {
        let Some(raw) = md.get("authorization") else {
            return Ok(None);
        };
        let header = raw
            .to_str()
            .map_err(|_| Status::unauthenticated("invalid authorization metadata"))?;
        if header.trim().is_empty() {
            return Ok(None);
        }
        self.authenticate_header(Some(header), method)
            .map(Some)
            .map_err(|e| e.to_tonic_status())
    }
}

fn extract_bearer(raw: Option<&str>) -> Option<&str> {
    raw.map(str::trim)
        .and_then(|v| {
            v.strip_prefix("Bearer ")
                .or_else(|| v.strip_prefix("bearer "))
        })
        .map(str::trim)
        .filter(|t| !t.is_empty())
}

fn to_caller(c: Claims, is_service: bool) -> crate::error::Result<Caller> {
    let role = c.role.trim().to_ascii_lowercase();
    if role.is_empty() {
        return Err(PaymentError::Unauthorized("token has no role".to_string()));
    }
    Ok(Caller {
        role,
        user_id: c.sub.trim().to_string(),
        shop_id: c.shop_id.trim().to_string(),
        is_service,
        service: if is_service {
            c.service.trim().to_string()
        } else {
            String::new()
        },
        exp: Some(c.exp),
    })
}

/// Returns the claims of a well-formed, correctly signed, unexpired HS256 JWT.
fn verify_hs256(token: &str, key: &[u8]) -> Option<Claims> {
    let mut parts = token.split('.');
    let (header, payload, sig) = (parts.next()?, parts.next()?, parts.next()?);
    if parts.next().is_some() {
        return None;
    }
    let hdr: serde_json::Value =
        serde_json::from_slice(&URL_SAFE_NO_PAD.decode(header).ok()?).ok()?;
    if hdr.get("alg")?.as_str()? != "HS256" {
        return None;
    }
    let mut mac = Hmac::<Sha256>::new_from_slice(key).ok()?;
    mac.update(header.as_bytes());
    mac.update(b".");
    mac.update(payload.as_bytes());
    mac.verify_slice(&URL_SAFE_NO_PAD.decode(sig).ok()?).ok()?;
    let claims: Claims = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(payload).ok()?).ok()?;
    if claims.exp <= chrono::Utc::now().timestamp() || claims.sub.trim().is_empty() {
        return None;
    }
    Some(claims)
}

/// Test helper: signs `claims` as an HS256 JWT.
#[cfg(test)]
pub(crate) fn sign_hs256(key: &str, claims: serde_json::Value) -> String {
    let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
    let payload = URL_SAFE_NO_PAD.encode(claims.to_string());
    let mut mac = Hmac::<Sha256>::new_from_slice(key.as_bytes()).unwrap();
    mac.update(format!("{header}.{payload}").as_bytes());
    let sig = URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes());
    format!("{header}.{payload}.{sig}")
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tonic::metadata::MetadataValue;

    const USER_KEY: &str = "pay-test-user-key";
    const SVC_KEY: &str = "pay-test-service-key";

    fn auth() -> GrpcAuth {
        GrpcAuth::new(USER_KEY, SVC_KEY).unwrap()
    }

    fn exp_in(secs: i64) -> i64 {
        chrono::Utc::now().timestamp() + secs
    }

    fn md(pairs: &[(&'static str, String)]) -> MetadataMap {
        let mut m = MetadataMap::new();
        for (k, v) in pairs {
            m.insert(*k, MetadataValue::try_from(v.as_str()).unwrap());
        }
        m
    }

    fn bearer(tok: String) -> (&'static str, String) {
        ("authorization", format!("Bearer {tok}"))
    }

    #[test]
    fn spoofed_metadata_without_jwt_is_unauthenticated() {
        let m = md(&[
            ("x-user-id", "attacker".into()),
            ("x-user-role", "admin".into()),
            ("x-shop-id", "shop-b".into()),
        ]);
        for method in [
            "GetLedgerSummary",
            "GetSellerBalance",
            "GetPayment",
            "GetFeeConfig",
            "SetFeeConfig",
            "ListSellerLedger",
            "ImportCodReconciliation",
            "ListReconciliationRuns",
            "UpdatePaymentMethodConfig",
            "DecideRefundApproval",
            "AdjustCODShippingFee",
        ] {
            let err = auth().authenticate(&m, method).unwrap_err();
            assert_eq!(err.code(), tonic::Code::Unauthenticated, "{method}");
        }
    }

    #[test]
    fn user_jwt_wins_over_metadata() {
        let exp = exp_in(60);
        let tok = sign_hs256(
            USER_KEY,
            json!({"sub":"seller-a","role":"seller","shop_id":"shop-a","exp":exp}),
        );
        let m = md(&[
            bearer(tok),
            ("x-shop-id", "shop-b".into()),
            ("x-user-role", "admin".into()),
        ]);
        let c = auth().authenticate(&m, "GetSellerBalance").unwrap();
        assert_eq!(
            c,
            Caller {
                role: "seller".into(),
                user_id: "seller-a".into(),
                shop_id: "shop-a".into(),
                is_service: false,
                service: String::new(),
                exp: Some(exp),
            }
        );
    }

    #[test]
    fn service_tokens_are_verified_and_scoped() {
        let svc = |key: &str, exp: i64| {
            sign_hs256(
                key,
                json!({"sub":"order-service","role":"admin","service":"order-service","exp":exp}),
            )
        };
        let a = auth();
        for method in [
            "CreatePayment",
            "GetPayment",
            "CancelPayment",
            "CreateRefund",
            "GetFeeConfig",
            "GetPaymentMethodConfigs",
            "AdjustCODShippingFee",
        ] {
            let ok = a
                .authenticate(&md(&[bearer(svc(SVC_KEY, exp_in(60)))]), method)
                .unwrap();
            assert_eq!(ok.role, "admin", "{method}");
            assert!(ok.is_service, "{method}");
            // Service token must NEVER satisfy any staff capability even if its role claim is "admin".
            assert!(!ok.has_capability(CAP_PAYMENT_METHODS_READ));
            assert!(!ok.has_capability(CAP_PAYMENT_METHODS_WRITE));
            assert!(!ok.has_capability(CAP_REFUNDS_APPROVE));
            assert!(!ok.has_capability(CAP_LEDGER_READ));
            assert!(!ok.is_staff());
        }

        for method in [
            "GetLedgerSummary",
            "GetSellerBalance",
            "ReleaseEscrow",
            "RunReconciliation",
            "ListRefunds",
            "SubmitRefundBankAccount",
            "SettleRefund",
            "ListReconciliationQueue",
            "ResolveReconciliationItem",
            "SetFeeConfig",
            "ListSellerLedger",
            "ImportCodReconciliation",
            "ListReconciliationRuns",
            "ListReconciliationReports",
            "UpdatePaymentMethodConfig",
            "DecideRefundApproval",
        ] {
            let denied = a
                .authenticate(&md(&[bearer(svc(SVC_KEY, exp_in(60)))]), method)
                .unwrap_err();
            assert_eq!(denied.code(), tonic::Code::PermissionDenied, "{method}");
        }

        for (name, tok) in [
            ("wrong key", svc("guessed-key", exp_in(60))),
            ("expired", svc(SVC_KEY, exp_in(-5))),
            ("user key", svc(USER_KEY, exp_in(60))),
            ("garbage", "a.b.c".to_string()),
        ] {
            let err = a
                .authenticate(&md(&[bearer(tok)]), "GetPayment")
                .unwrap_err();
            assert_eq!(err.code(), tonic::Code::Unauthenticated, "{name}");
        }
    }

    #[test]
    fn authenticate_optional_handles_public_valid_and_invalid_tokens() {
        let a = auth();
        // Missing header -> public caller (Ok(None)).
        assert_eq!(
            a.authenticate_optional(&MetadataMap::new(), "GetPaymentMethodConfigs")
                .unwrap(),
            None
        );

        // Valid admin JWT -> Ok(Some(caller)).
        let ops_tok = sign_hs256(
            USER_KEY,
            json!({"sub":"ops-1","role":"ops","exp":exp_in(60)}),
        );
        let ops = a
            .authenticate_optional(&md(&[bearer(ops_tok)]), "GetPaymentMethodConfigs")
            .unwrap()
            .expect("authenticated caller");
        assert!(ops.has_capability(CAP_PAYMENT_METHODS_READ));
        assert!(ops.has_capability(CAP_PAYMENT_METHODS_WRITE));

        // Valid service token -> Ok(Some(service caller)), zero capabilities.
        let svc_tok = sign_hs256(
            SVC_KEY,
            json!({"sub":"order-service","role":"admin","service":"order-service","exp":exp_in(60)}),
        );
        let svc = a
            .authenticate_optional(&md(&[bearer(svc_tok)]), "GetPaymentMethodConfigs")
            .unwrap()
            .expect("service caller");
        assert!(svc.is_service);
        assert!(!svc.has_capability(CAP_PAYMENT_METHODS_READ));

        // Invalid token -> rejected as Unauthenticated (never silently downgraded to public).
        let err = a
            .authenticate_optional(
                &md(&[bearer("bad.tok.val".into())]),
                "GetPaymentMethodConfigs",
            )
            .unwrap_err();
        assert_eq!(err.code(), tonic::Code::Unauthenticated);
    }

    #[test]
    fn f9_capability_matrix_matches_shared_json_across_all_6_staff_roles_and_non_staff() {
        // Verify exact 6 staff roles from pkg/common/auth/capabilities.json.
        let expected_roles = [
            "super_admin",
            "admin",
            "moderator",
            "cskh",
            "accountant",
            "ops",
        ];
        assert_eq!(staff_roles(), expected_roles);

        // Parse capabilities.json independently to ensure zero drift.
        let raw: serde_json::Value =
            serde_json::from_str(CAPABILITIES_JSON).expect("valid capabilities.json");
        let caps_obj = raw["capabilities"].as_object().expect("capabilities map");

        // Table of all 11 payment capabilities × expected allowed roles.
        let expected_payment_matrix: &[(&str, &[&str])] = &[
            (
                CAP_FEES_PAYMENT_READ,
                &["super_admin", "admin", "accountant", "ops"],
            ),
            (
                CAP_FEES_PAYMENT_WRITE,
                &["super_admin", "admin", "accountant"],
            ),
            (
                CAP_LEDGER_READ,
                &["super_admin", "admin", "accountant", "ops"],
            ),
            (
                CAP_RECONCILIATION_WRITE,
                &["super_admin", "admin", "accountant"],
            ),
            (CAP_ESCROW_RELEASE, &["super_admin", "admin", "accountant"]),
            (
                CAP_REFUNDS_READ,
                &["super_admin", "admin", "cskh", "accountant", "ops"],
            ),
            (
                CAP_REFUNDS_CREATE,
                &["super_admin", "admin", "cskh", "accountant"],
            ),
            (CAP_REFUNDS_APPROVE, &["super_admin", "admin", "accountant"]),
            (CAP_REFUNDS_SETTLE, &["super_admin", "admin", "accountant"]),
            (
                CAP_PAYMENT_METHODS_READ,
                &["super_admin", "admin", "accountant", "ops"],
            ),
            (CAP_PAYMENT_METHODS_WRITE, &["super_admin", "admin", "ops"]),
        ];

        for &(cap, allowed_roles) in expected_payment_matrix {
            // Confirm capabilities.json itself matches our expected table.
            let json_roles: HashSet<&str> = caps_obj
                .get(cap)
                .unwrap_or_else(|| panic!("missing capability {cap} in capabilities.json"))
                .as_array()
                .expect("array of roles")
                .iter()
                .map(|v| v.as_str().expect("role string"))
                .collect();
            let expected_set: HashSet<&str> = allowed_roles.iter().copied().collect();
            assert_eq!(
                json_roles, expected_set,
                "drift in capabilities.json for {cap}"
            );

            // Check every one of the 6 staff roles (positive & negative).
            for role in expected_roles {
                let caller = Caller {
                    role: role.to_string(),
                    user_id: format!("u-{role}"),
                    shop_id: String::new(),
                    is_service: false,
                    service: String::new(),
                    exp: Some(exp_in(60)),
                };
                let should_allow = expected_set.contains(role);
                assert_eq!(
                    caller.has_capability(cap),
                    should_allow,
                    "role={role} cap={cap} expected={should_allow}"
                );
                assert_eq!(role_has_capability(role, cap), should_allow);
                assert!(caller.is_staff());
            }

            // Non-staff roles (buyer, seller, unknown) and service tokens MUST always be denied.
            for non_staff in ["buyer", "seller", "", "unknown_role"] {
                let caller = Caller {
                    role: non_staff.to_string(),
                    user_id: "u-non-staff".to_string(),
                    shop_id: "shop-1".to_string(),
                    is_service: false,
                    service: String::new(),
                    exp: Some(exp_in(60)),
                };
                assert!(
                    !caller.has_capability(cap),
                    "non-staff role={non_staff} must be denied {cap}"
                );
                assert!(!caller.is_staff());
            }

            // Service token carrying role="super_admin" MUST STILL be denied every capability (F9).
            let svc_caller = Caller {
                role: "super_admin".to_string(),
                user_id: "order-service".to_string(),
                shop_id: String::new(),
                is_service: true,
                service: "order-service".to_string(),
                exp: Some(exp_in(60)),
            };
            assert!(
                !svc_caller.has_capability(cap),
                "service token must NEVER satisfy {cap}"
            );
            assert!(!svc_caller.is_staff());
        }
    }

    #[test]
    fn rejects_empty_secrets() {
        assert!(GrpcAuth::new("", SVC_KEY).is_err());
        assert!(GrpcAuth::new(USER_KEY, " ").is_err());
    }

    #[test]
    fn authenticate_header_rejects_service_token_on_sse() {
        let a = auth();
        let buyer_tok = sign_hs256(
            USER_KEY,
            json!({"sub":"buyer-1","role":"buyer","exp":exp_in(60)}),
        );
        let caller = a
            .authenticate_header(Some(&format!("Bearer {buyer_tok}")), "PaymentEventsSse")
            .unwrap();
        assert_eq!(caller.role, "buyer");
        assert_eq!(caller.user_id, "buyer-1");

        let svc_tok = sign_hs256(
            SVC_KEY,
            json!({"sub":"order-service","role":"admin","service":"order-service","exp":exp_in(60)}),
        );
        let err = a
            .authenticate_header(Some(&format!("Bearer {svc_tok}")), "PaymentEventsSse")
            .unwrap_err();
        assert!(matches!(err, PaymentError::Forbidden(_)));

        let missing = a.authenticate_header(None, "PaymentEventsSse").unwrap_err();
        assert!(matches!(missing, PaymentError::Unauthorized(_)));
    }

    #[test]
    fn require_payment_read_access_enforces_ownership_and_capabilities() {
        use crate::domain::{PaymentRecord, ShopAllocationRecord};
        use chrono::Utc;
        use uuid::Uuid;

        let now = Utc::now();
        let payment_id = Uuid::new_v4();
        let pwa = PaymentWithAllocations {
            payment: PaymentRecord {
                id: payment_id,
                order_group_id: "grp-1".into(),
                order_id: "ord-1".into(),
                user_id: "buyer-owner".into(),
                method: "VIETQR".into(),
                provider: "mock".into(),
                amount_vnd: 250_000,
                status: "pending".into(),
                provider_tx_id: None,
                provider_order_code: "NM-1".into(),
                qr_code: None,
                qr_url: None,
                pay_url: None,
                idempotency_key: None,
                expires_at: None,
                paid_at: None,
                trace_id: "trace-1".into(),
                sales_channel: "MARKETPLACE".into(),
                storefront_shop_id: String::new(),
                created_at: now,
                updated_at: now,
            },
            allocations: vec![ShopAllocationRecord {
                id: Uuid::new_v4(),
                payment_id,
                order_id: "ord-1".into(),
                shop_id: "shop-a".into(),
                subtotal_vnd: 220_000,
                commission_vnd: 11_000,
                shipping_fee_vnd: 30_000,
                platform_discount_vnd: 0,
                escrow_status: "pending".into(),
                settled_at: None,
                released_at: None,
                tracking_code: None,
                carrier_code: None,
                order_code: None,
                cod_reconciled_at: None,
                cod_reconciliation_run_id: None,
                sales_channel: "MARKETPLACE".into(),
                storefront_shop_id: String::new(),
                created_at: now,
            }],
        };

        let buyer_owner = Caller {
            role: "buyer".into(),
            user_id: "buyer-owner".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&buyer_owner, &pwa).unwrap(),
            PaymentViewScope::BuyerOwner
        );

        let other_buyer = Caller {
            role: "buyer".into(),
            user_id: "buyer-other".into(),
            ..Default::default()
        };
        assert!(matches!(
            require_payment_read_access(&other_buyer, &pwa),
            Err(PaymentError::Forbidden(_))
        ));

        let seller_with_alloc = Caller {
            role: "seller".into(),
            user_id: "seller-u1".into(),
            shop_id: "shop-a".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&seller_with_alloc, &pwa).unwrap(),
            PaymentViewScope::SellerAllocation {
                shop_id: "shop-a".into(),
            }
        );

        let seller_without_alloc = Caller {
            role: "seller".into(),
            user_id: "seller-u2".into(),
            shop_id: "shop-b".into(),
            ..Default::default()
        };
        assert!(matches!(
            require_payment_read_access(&seller_without_alloc, &pwa),
            Err(PaymentError::Forbidden(_))
        ));

        let seller_as_buyer = Caller {
            role: "seller".into(),
            user_id: "buyer-owner".into(),
            shop_id: "shop-other".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&seller_as_buyer, &pwa).unwrap(),
            PaymentViewScope::BuyerOwner
        );

        // Staff with `refunds.read` or `ledger.read` get Full scope.
        for staff_role in ["super_admin", "admin", "cskh", "accountant", "ops"] {
            let staff = Caller {
                role: staff_role.into(),
                user_id: format!("u-{staff_role}"),
                ..Default::default()
            };
            assert_eq!(
                resolve_payment_read_scope(&staff, &pwa).unwrap(),
                PaymentViewScope::Full,
                "role={staff_role}"
            );
        }

        // `moderator` lacks both `refunds.read` and `ledger.read`:
        // - Accessing ANOTHER user's payment -> Forbidden
        // - Accessing OWN payment (moderator shopping as buyer) -> BuyerOwner (p3 item 3)
        let moderator_other = Caller {
            role: "moderator".into(),
            user_id: "mod-1".into(),
            ..Default::default()
        };
        assert!(matches!(
            require_payment_read_access(&moderator_other, &pwa),
            Err(PaymentError::Forbidden(_))
        ));

        let moderator_owner = Caller {
            role: "moderator".into(),
            user_id: "buyer-owner".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&moderator_owner, &pwa).unwrap(),
            PaymentViewScope::BuyerOwner
        );

        // Any other non-capability role owning the payment also gets BuyerOwner,
        // while non-owners of every non-capability role are denied.
        for role in ["moderator", "buyer", "seller", "support_readonly", ""] {
            let own = Caller {
                role: role.into(),
                user_id: "buyer-owner".into(),
                shop_id: "shop-unrelated".into(),
                ..Default::default()
            };
            assert_eq!(
                resolve_payment_read_scope(&own, &pwa).unwrap(),
                PaymentViewScope::BuyerOwner,
                "owner with role={role} must get BuyerOwner"
            );

            let stranger = Caller {
                role: role.into(),
                user_id: "stranger-99".into(),
                shop_id: "shop-unrelated".into(),
                ..Default::default()
            };
            assert!(
                matches!(
                    require_payment_read_access(&stranger, &pwa),
                    Err(PaymentError::Forbidden(_))
                ),
                "non-owner with role={role} must be Forbidden"
            );
        }

        let service_caller = Caller {
            role: "service".into(),
            user_id: "order-service".into(),
            is_service: true,
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&service_caller, &pwa).unwrap(),
            PaymentViewScope::Full
        );

        assert!(matches!(
            require_payment_read_access(&Caller::default(), &pwa),
            Err(PaymentError::Forbidden(_))
        ));
    }

    #[test]
    fn p4_item_m_service_tokens_enforce_per_rpc_caller_allowlist() {
        let a = auth();
        let make_svc = |svc_name: &str| {
            sign_hs256(
                SVC_KEY,
                json!({
                    "sub": svc_name,
                    "role": "admin",
                    "service": svc_name,
                    "exp": exp_in(60),
                }),
            )
        };

        for method in [
            "CreatePayment",
            "GetPayment",
            "CancelPayment",
            "CreateRefund",
            "GetFeeConfig",
            "GetPaymentMethodConfigs",
            "AdjustCODShippingFee",
        ] {
            // Only `order-service` is authorized to call PaymentService internal RPCs.
            let ok = a
                .authenticate(&md(&[bearer(make_svc("order-service"))]), method)
                .unwrap_or_else(|e| panic!("order-service must be allowed on {method}: {e}"));
            assert!(ok.is_service);

            // Other services sharing SERVICE_JWT_SECRET MUST be rejected with PermissionDenied.
            for other_svc in [
                "shipping-service",
                "product-service",
                "notification-service",
                "promotion-service",
                "user-service",
                "cart-service",
                "gateway",
            ] {
                let err = a
                    .authenticate(&md(&[bearer(make_svc(other_svc))]), method)
                    .unwrap_err();
                assert_eq!(
                    err.code(),
                    tonic::Code::PermissionDenied,
                    "service={other_svc} must be denied on {method}"
                );
            }
        }
    }
}
