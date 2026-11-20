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

use crate::error::PaymentError;

/// RPCs on which an internal service token (order saga / expiry watchdog) is accepted.
const SERVICE_METHODS: &[&str] = &[
    "GetPayment",
    "CancelPayment",
    "CreateRefund",
    "ReleaseEscrow",
];

/// Authenticated caller as seen by gRPC and HTTP handlers.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Caller {
    pub role: String,
    pub user_id: String,
    pub shop_id: String,
}

impl Caller {
    pub fn is_admin(&self) -> bool {
        matches!(self.role.as_str(), "admin" | "super_admin")
    }
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

    /// Authenticates the caller of `method` (bare RPC name, e.g. "GetPayment").
    #[allow(clippy::result_large_err)]
    pub fn authenticate(&self, md: &MetadataMap, method: &str) -> Result<Caller, Status> {
        let token = extract_bearer(md.get("authorization").and_then(|v| v.to_str().ok()))
            .ok_or_else(|| Status::unauthenticated("authentication required"))?;

        if let Some(c) = verify_hs256(token, &self.user_key).filter(|c| c.service.trim().is_empty())
        {
            return to_caller(c);
        }
        if let Some(c) =
            verify_hs256(token, &self.service_key).filter(|c| !c.service.trim().is_empty())
        {
            if !SERVICE_METHODS.contains(&method) {
                return Err(Status::permission_denied(
                    "service identity not allowed for this RPC",
                ));
            }
            return to_caller(c);
        }
        Err(Status::unauthenticated("invalid access token"))
    }

    /// Authenticates an HTTP caller using a verified end-user Bearer JWT.
    /// Plain X-User-* headers are never trusted; internal service tokens are rejected on HTTP routes.
    pub fn authenticate_http_user(
        &self,
        auth_header: Option<&str>,
    ) -> crate::error::Result<Caller> {
        let token = extract_bearer(auth_header)
            .ok_or_else(|| PaymentError::Unauthorized("authentication required".to_string()))?;

        if let Some(c) = verify_hs256(token, &self.user_key).filter(|c| c.service.trim().is_empty())
        {
            return to_caller(c).map_err(|s| PaymentError::Unauthorized(s.message().to_string()));
        }
        if verify_hs256(token, &self.service_key)
            .filter(|c| !c.service.trim().is_empty())
            .is_some()
        {
            return Err(PaymentError::Forbidden(
                "service identity not allowed for this HTTP endpoint".to_string(),
            ));
        }
        Err(PaymentError::Unauthorized(
            "invalid access token".to_string(),
        ))
    }

    /// Authenticates an HTTP caller and enforces `admin` or `super_admin` role.
    pub fn authenticate_http_admin(
        &self,
        auth_header: Option<&str>,
    ) -> crate::error::Result<Caller> {
        let caller = self.authenticate_http_user(auth_header)?;
        if !caller.is_admin() {
            return Err(PaymentError::Forbidden(
                "forbidden: admin role required".to_string(),
            ));
        }
        Ok(caller)
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

#[allow(clippy::result_large_err)]
fn to_caller(c: Claims) -> Result<Caller, Status> {
    let role = c.role.trim().to_ascii_lowercase();
    if role.is_empty() {
        return Err(Status::unauthenticated("token has no role"));
    }
    Ok(Caller {
        role,
        user_id: c.sub.trim().to_string(),
        shop_id: c.shop_id.trim().to_string(),
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
        for method in ["GetLedgerSummary", "GetSellerBalance", "GetPayment"] {
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
                shop_id: "shop-a".into()
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
        let ok = a
            .authenticate(&md(&[bearer(svc(SVC_KEY, exp_in(60)))]), "ReleaseEscrow")
            .unwrap();
        assert_eq!(ok.role, "admin");

        for method in [
            "CreatePayment",
            "GetLedgerSummary",
            "GetSellerBalance",
            "RunReconciliation",
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
    fn http_auth_enforces_user_jwt_and_admin_role() {
        let a = auth();
        assert!(matches!(
            a.authenticate_http_user(None),
            Err(PaymentError::Unauthorized(_))
        ));
        assert!(matches!(
            a.authenticate_http_admin(Some("Bearer invalid.token.sig")),
            Err(PaymentError::Unauthorized(_))
        ));

        let svc_tok = sign_hs256(
            SVC_KEY,
            json!({"sub":"order-service","role":"admin","service":"order-service","exp":exp_in(60)}),
        );
        assert!(matches!(
            a.authenticate_http_admin(Some(&format!("Bearer {svc_tok}"))),
            Err(PaymentError::Forbidden(_))
        ));

        let buyer_tok = sign_hs256(
            USER_KEY,
            json!({"sub":"buyer-1","role":"buyer","exp":exp_in(60)}),
        );
        let buyer = a
            .authenticate_http_user(Some(&format!("Bearer {buyer_tok}")))
            .unwrap();
        assert_eq!(buyer.user_id, "buyer-1");
        assert!(matches!(
            a.authenticate_http_admin(Some(&format!("Bearer {buyer_tok}"))),
            Err(PaymentError::Forbidden(_))
        ));

        let admin_tok = sign_hs256(
            USER_KEY,
            json!({"sub":"admin-1","role":"admin","exp":exp_in(60)}),
        );
        let admin = a
            .authenticate_http_admin(Some(&format!("Bearer {admin_tok}")))
            .unwrap();
        assert!(admin.is_admin());
    }

    #[test]
    fn rejects_empty_secrets() {
        assert!(GrpcAuth::new("", SVC_KEY).is_err());
        assert!(GrpcAuth::new(USER_KEY, " ").is_err());
    }
}
