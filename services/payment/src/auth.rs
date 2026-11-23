//! gRPC caller authentication. Identity comes only from a verified HS256 JWT in the
//! `authorization` metadata; plain `x-user-id` / `x-user-role` / `x-shop-id` are never trusted.
//!
//! ponytail: one shared HS256 service key (same scheme as pkg/common/auth in Go); any holder can
//! mint any service identity. Upgrade path: mTLS between services, or asymmetric per-service JWT
//! (EdDSA) verified via JWKS.

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hmac::{Hmac, Mac};
use serde::Deserialize;
use sha2::Sha256;
use tonic::metadata::MetadataMap;
use tonic::Status;

use crate::domain::PaymentWithAllocations;
use crate::error::PaymentError;

/// RPCs on which an internal service token (order saga / expiry watchdog / checkout forward
/// recovery) is accepted.
const SERVICE_METHODS: &[&str] = &[
    "CreatePayment",
    "GetPayment",
    "CancelPayment",
    "CreateRefund",
    "GetFeeConfig",
];

/// Authenticated caller as seen by handlers.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Caller {
    pub role: String,
    pub user_id: String,
    pub shop_id: String,
    pub is_service: bool,
}

impl Caller {
    pub fn is_admin(&self) -> bool {
        matches!(self.role.as_str(), "admin" | "super_admin")
    }

    pub fn is_admin_or_service(&self) -> bool {
        self.is_admin() || self.is_service
    }
}

/// Caller-scoped visibility for a payment record and its shop allocations.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PaymentViewScope {
    /// Admin, super_admin, or verified internal service token: full payment and allocations.
    Full,
    /// Buyer owner (including a seller purchasing as a buyer): all allocations with
    /// `commission_vnd = 0`; buyer QR/provider_ref and full amount preserved.
    BuyerOwner,
    /// Seller who only owns one or more shop allocations (not the buyer owner):
    /// restricted to `shop_id`, with buyer QR/provider_ref redacted and shop-scoped amount.
    SellerAllocation { shop_id: String },
}

/// Enforces object-level read access for a payment and resolves the caller's visibility scope
/// (admin/super_admin/service token, buyer owner including seller-as-buyer, or seller with a
/// matching shop allocation). Shared by gRPC `GetPayment` and HTTP SSE.
pub fn resolve_payment_read_scope(
    caller: &Caller,
    pwa: &PaymentWithAllocations,
) -> crate::error::Result<PaymentViewScope> {
    if caller.is_admin_or_service() {
        return Ok(PaymentViewScope::Full);
    }
    let owns_as_buyer = !caller.user_id.is_empty() && pwa.payment.user_id == caller.user_id;
    match caller.role.as_str() {
        "buyer" if owns_as_buyer => Ok(PaymentViewScope::BuyerOwner),
        "seller" => {
            if owns_as_buyer {
                return Ok(PaymentViewScope::BuyerOwner);
            }
            if !caller.shop_id.is_empty()
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
        _ => Err(PaymentError::Forbidden(
            "cannot access another user's payment".to_string(),
        )),
    }
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
        let token = authorization
            .map(str::trim)
            .and_then(|v| {
                v.strip_prefix("Bearer ")
                    .or_else(|| v.strip_prefix("bearer "))
            })
            .map(str::trim)
            .filter(|t| !t.is_empty())
            .ok_or_else(|| PaymentError::Unauthorized("authentication required".to_string()))?;

        if let Some(c) = verify_hs256(token, &self.user_key).filter(|c| c.service.is_empty()) {
            return to_caller(c, false);
        }
        if let Some(c) = verify_hs256(token, &self.service_key).filter(|c| !c.service.is_empty()) {
            if !SERVICE_METHODS.contains(&method) {
                return Err(PaymentError::Forbidden(
                    "service identity not allowed for this RPC".to_string(),
                ));
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
        ] {
            let err = auth().authenticate(&m, method).unwrap_err();
            assert_eq!(err.code(), tonic::Code::Unauthenticated, "{method}");
        }
    }

    #[test]
    fn user_jwt_wins_over_metadata() {
        let tok = sign_hs256(
            USER_KEY,
            json!({"sub":"seller-a","role":"seller","shop_id":"shop-a","exp":exp_in(60)}),
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
        ] {
            let ok = a
                .authenticate(&md(&[bearer(svc(SVC_KEY, exp_in(60)))]), method)
                .unwrap();
            assert_eq!(ok.role, "admin", "{method}");
            assert!(ok.is_service, "{method}");
        }

        for method in [
            "GetLedgerSummary",
            "ReleaseEscrow",
            "ListRefunds",
            "SubmitRefundBankAccount",
            "SettleRefund",
            "ListReconciliationQueue",
            "ResolveReconciliationItem",
            "SetFeeConfig",
            "ListSellerLedger",
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
    fn require_payment_read_access_enforces_ownership_and_roles() {
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
                idempotency_key: None,
                expires_at: None,
                paid_at: None,
                trace_id: "trace-1".into(),
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

        let admin = Caller {
            role: "admin".into(),
            user_id: "admin-1".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&admin, &pwa).unwrap(),
            PaymentViewScope::Full
        );

        let super_admin = Caller {
            role: "super_admin".into(),
            user_id: "sadmin-1".into(),
            ..Default::default()
        };
        assert_eq!(
            resolve_payment_read_scope(&super_admin, &pwa).unwrap(),
            PaymentViewScope::Full
        );

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
}
