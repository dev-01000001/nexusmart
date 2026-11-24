use super::*;
use std::sync::atomic::{AtomicUsize, Ordering};

use axum::{http::StatusCode, routing::post, Json, Router};
use chrono::TimeZone;

use crate::config::{DEV_ONLY_ZALOPAY_MOCK_KEY1, DEV_ONLY_ZALOPAY_MOCK_KEY2};
use crate::domain::ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS;
use crate::vietqr::CircuitState;

// Purely synthetic unit-test placeholders (NEVER any real or public ZaloPay sandbox keys).
const SYNTH_KEY1: &str = "synthetic-unit-test-key1-00000001";
const SYNTH_KEY2: &str = "synthetic-unit-test-key2-00000002";
const SYNTH_APP_ID: i32 = 100_001;

#[test]
fn test_mac_formulas_match_independent_sha256_reference_vectors() {
    // 1. Create Order: app_id|app_trans_id|app_user|amount|app_time|embed_data|item
    let create_mac = compute_create_order_mac(
        SYNTH_KEY1,
        SYNTH_APP_ID,
        "261124_order0001",
        "buyer_01",
        250_000,
        1_795_478_400_000,
        r#"{"redirecturl":"https://nexusmart.local/result"}"#,
        "[]",
    );
    assert_eq!(
        create_mac,
        "a333c55d99f638fab05f8a47e89a9954aeebc3be571989188408043e543d1f63"
    );

    // 2. Query Order: app_id|app_trans_id|key1
    let query_mac = compute_query_order_mac(SYNTH_KEY1, SYNTH_APP_ID, "261124_order0001");
    assert_eq!(
        query_mac,
        "8393a3462206ca7677224e5cc7f3e305c1785bdb96c4c7e363da55539001c583"
    );

    // 3a. Refund without refund_fee_amount: app_id|zp_trans_id|amount|description|timestamp
    let refund_mac_no_fee = compute_refund_mac(
        SYNTH_KEY1,
        SYNTH_APP_ID,
        "261124000000999",
        250_000,
        None,
        "Order cancelled",
        1_795_478_500_000,
    );
    assert_eq!(
        refund_mac_no_fee,
        "e41c367cc5c15d75e649666a040382063069f334bb35d50b162bfb860939cd5f"
    );

    // 3b. Refund with refund_fee_amount: app_id|zp_trans_id|amount|refund_fee_amount|description|timestamp
    let refund_mac_with_fee = compute_refund_mac(
        SYNTH_KEY1,
        SYNTH_APP_ID,
        "261124000000999",
        250_000,
        Some(5_000),
        "Order cancelled",
        1_795_478_500_000,
    );
    assert_eq!(
        refund_mac_with_fee,
        "543059d7b40353e9922088ed2018793d129c35bd9b1151348c8c16301f78ccc9"
    );
    assert_ne!(refund_mac_no_fee, refund_mac_with_fee);

    // 4. Query Refund: app_id|m_refund_id|timestamp
    let query_refund_mac = compute_query_refund_mac(
        SYNTH_KEY1,
        SYNTH_APP_ID,
        "261124_100001_ref0001",
        1_795_478_600_000,
    );
    assert_eq!(
        query_refund_mac,
        "2abbb74d90065b1ef7248b472ab0763e4846f5bd8b1913c427b13155c3031d56"
    );

    // 5. Callback: raw_data with key2
    let cb_data = r#"{"app_id":100001,"app_trans_id":"261124_order0001","app_time":1795478400000,"app_user":"buyer_01","amount":250000,"embed_data":"{}","item":"[]","zp_trans_id":261124000000999,"server_time":1795478415000,"channel":38,"merchant_user_id":"muser_01","user_fee_amount":0,"discount_amount":0}"#;
    let cb_mac = compute_callback_mac(SYNTH_KEY2, cb_data);
    assert_eq!(
        cb_mac,
        "0584fc2987b90614881f72f5100b7fe37bb2be3fba23386fac63d67138d19937"
    );
}

#[test]
fn test_verify_callback_edge_cases_bitflip_empty_case_and_tamper() {
    let raw_data = r#"{"app_id":100001,"app_trans_id":"261124_order0001","app_time":1795478400000,"app_user":"buyer_01","amount":250000,"embed_data":"{}","item":"[]","zp_trans_id":261124000000999,"server_time":1795478415000,"channel":38,"merchant_user_id":"muser_01","user_fee_amount":0,"discount_amount":0}"#;
    let valid_lower = compute_callback_mac(SYNTH_KEY2, raw_data);
    let valid_upper = valid_lower.to_ascii_uppercase();
    let valid_mixed: String = valid_lower
        .chars()
        .enumerate()
        .map(|(i, c)| {
            if i % 2 == 0 {
                c.to_ascii_uppercase()
            } else {
                c
            }
        })
        .collect();

    // Lowercase, uppercase, and mixed-case valid 64-hex MACs must all verify.
    assert!(verify_callback(raw_data, &valid_lower, SYNTH_KEY2).is_ok());
    assert!(verify_callback(raw_data, &valid_upper, SYNTH_KEY2).is_ok());
    assert!(verify_callback(raw_data, &valid_mixed, SYNTH_KEY2).is_ok());

    // Flip 1 byte in MAC (last hex nibble changed) -> rejected.
    let mut flipped_mac = valid_lower.clone();
    let last = flipped_mac.pop().unwrap();
    flipped_mac.push(if last == '0' { '1' } else { '0' });
    assert!(matches!(
        verify_callback(raw_data, &flipped_mac, SYNTH_KEY2),
        Err(PaymentError::Unauthorized(_))
    ));

    // Flip 1 byte in first position of MAC -> rejected.
    let mut flipped_first = valid_lower.clone();
    let replacement = if flipped_first.starts_with('a') {
        "b"
    } else {
        "a"
    };
    flipped_first.replace_range(0..1, replacement);
    assert!(matches!(
        verify_callback(raw_data, &flipped_first, SYNTH_KEY2),
        Err(PaymentError::Unauthorized(_))
    ));

    // Flip 1 byte in raw_data (amount 250000 -> 250001) -> rejected.
    let tampered_data = raw_data.replacen("250000", "250001", 1);
    assert!(matches!(
        verify_callback(&tampered_data, &valid_lower, SYNTH_KEY2),
        Err(PaymentError::Unauthorized(_))
    ));

    // Empty / whitespace / malformed / truncated MAC -> rejected.
    for bad_mac in [
        "",
        "   ",
        "not-hex-at-all",
        &valid_lower[..62],
        &format!("{valid_lower}00"),
        &format!("sha256={valid_lower}"),
    ] {
        assert!(
            matches!(
                verify_callback(raw_data, bad_mac, SYNTH_KEY2),
                Err(PaymentError::Unauthorized(_))
            ),
            "expected Unauthorized for bad_mac={bad_mac:?}"
        );
    }

    // Empty key2 -> rejected even if MAC were computed with empty string.
    let empty_key_mac = compute_callback_mac("", raw_data);
    assert!(matches!(
        verify_callback(raw_data, &empty_key_mac, ""),
        Err(PaymentError::Unauthorized(_))
    ));
    assert!(matches!(
        verify_callback(raw_data, &empty_key_mac, "   "),
        Err(PaymentError::Unauthorized(_))
    ));
}

#[test]
fn test_server_time_normalization_and_replay_window_299s_vs_301s_both_units() {
    let now = Utc.with_ymd_and_hms(2026, 11, 24, 15, 0, 0).unwrap();
    let now_sec = now.timestamp();
    let now_ms = now.timestamp_millis();

    // Unix SECONDS: -299s, +299s, -300s within window; -301s, +301s outside window.
    let sec_past_299 =
        check_server_time_window(now_sec - 299, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS).unwrap();
    assert!(sec_past_299.within_window);
    assert_eq!(sec_past_299.drift_secs, 299);
    assert_eq!(sec_past_299.normalized_at.timestamp(), now_sec - 299);

    let sec_future_299 =
        check_server_time_window(now_sec + 299, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS).unwrap();
    assert!(sec_future_299.within_window);
    assert_eq!(sec_future_299.drift_secs, 299);

    let sec_exact_300 =
        check_server_time_window(now_sec - 300, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS).unwrap();
    assert!(sec_exact_300.within_window);
    assert_eq!(sec_exact_300.drift_secs, 300);

    let sec_past_301 =
        check_server_time_window(now_sec - 301, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS).unwrap();
    assert!(!sec_past_301.within_window);
    assert_eq!(sec_past_301.drift_secs, 301);

    let sec_future_301 =
        check_server_time_window(now_sec + 301, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS).unwrap();
    assert!(!sec_future_301.within_window);
    assert_eq!(sec_future_301.drift_secs, 301);

    // Unix MILLISECONDS (> 1e11): -299_000ms, +299_000ms, -300_000ms within;
    // -300_001ms, -301_000ms, +301_000ms outside window.
    let ms_past_299 =
        check_server_time_window(now_ms - 299_000, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
            .unwrap();
    assert!(ms_past_299.within_window);
    assert_eq!(ms_past_299.drift_secs, 299);
    assert_eq!(
        ms_past_299.normalized_at.timestamp_millis(),
        now_ms - 299_000
    );

    let ms_future_299 =
        check_server_time_window(now_ms + 299_000, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
            .unwrap();
    assert!(ms_future_299.within_window);
    assert_eq!(ms_future_299.drift_secs, 299);

    let ms_past_300_plus_1ms =
        check_server_time_window(now_ms - 300_001, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
            .unwrap();
    assert!(
        !ms_past_300_plus_1ms.within_window,
        "300s + 1ms must NOT be rounded down into the 300s window"
    );

    let ms_past_301 =
        check_server_time_window(now_ms - 301_000, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
            .unwrap();
    assert!(!ms_past_301.within_window);
    assert_eq!(ms_past_301.drift_secs, 301);

    let ms_future_301 =
        check_server_time_window(now_ms + 301_000, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
            .unwrap();
    assert!(!ms_future_301.within_window);
    assert_eq!(ms_future_301.drift_secs, 301);

    // Non-positive timestamps rejected.
    assert!(normalize_server_time(0).is_err());
    assert!(normalize_server_time(-1).is_err());
}

#[test]
fn test_app_trans_id_and_m_refund_id_use_gmt_plus_7_boundary() {
    let payment_id = Uuid::parse_str("11111111-2222-3333-4444-555555555555").unwrap();
    let refund_id = Uuid::parse_str("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee").unwrap();

    // 2026-11-24 16:59:59 UTC == 2026-11-24 23:59:59 +07:00 -> prefix 261124_
    let before_vn_midnight = Utc.with_ymd_and_hms(2026, 11, 24, 16, 59, 59).unwrap();
    let trans_before = generate_app_trans_id(payment_id, before_vn_midnight);
    assert!(trans_before.starts_with("261124_"));
    assert!(trans_before.len() <= MAX_APP_TRANS_ID_LEN);

    // 2026-11-24 17:00:00 UTC == 2026-11-25 00:00:00 +07:00 -> prefix 261125_
    let at_vn_midnight = Utc.with_ymd_and_hms(2026, 11, 24, 17, 0, 0).unwrap();
    let trans_after = generate_app_trans_id(payment_id, at_vn_midnight);
    assert!(trans_after.starts_with("261125_"));
    assert!(trans_after.len() <= MAX_APP_TRANS_ID_LEN);

    let m_refund = generate_m_refund_id(SYNTH_APP_ID, refund_id, at_vn_midnight);
    assert!(m_refund.starts_with("261125_100001_"));
    assert_eq!(m_refund, "261125_100001_aaaaaaaabbbbcccc");
}

#[test]
fn test_f8_validate_zalopay_pay_url_enforces_host_allowlist_and_mock_origin() {
    // Valid ZaloPay HTTPS hosts (both modes).
    for good in [
        "https://sbgateway.zalopay.vn/openinapp?order=tok-1",
        "https://qcgateway.zalopay.vn/pay/v2/abc",
        "https://zalopay.vn/checkout",
    ] {
        assert_eq!(
            validate_zalopay_pay_url(good, ZaloPayMode::Sandbox).unwrap(),
            good
        );
        assert_eq!(
            validate_zalopay_pay_url(good, ZaloPayMode::Mock).unwrap(),
            good
        );
    }

    // Same-origin relative mock path allowed ONLY in Mock mode, rejected in Sandbox.
    let mock_rel = "/thanh-toan/mock-zalopay?app_trans_id=261124_abc&token=tok";
    assert_eq!(
        validate_zalopay_pay_url(mock_rel, ZaloPayMode::Mock).unwrap(),
        mock_rel
    );
    assert!(validate_zalopay_pay_url(mock_rel, ZaloPayMode::Sandbox).is_err());

    // Disallowed schemes, spoofing hosts, userinfo, traversal, protocol-relative, empty.
    for bad in [
        "",
        "   ",
        "javascript:alert(1)",
        "data:text/html,<script>alert(1)</script>",
        "http://sbgateway.zalopay.vn/openinapp?order=tok",
        "https://evil.com/pay",
        "https://evilzalopay.vn/pay",
        "https://zalopay.vn.evil.com/pay",
        "https://zalopay.vn@evil.com/pay",
        "//evil.com/thanh-toan/mock-zalopay",
        "/thanh-toan/../evil",
        "/thanh-toan/mock\\evil",
        "/other-path/redirect",
        "https://sbgateway.zalopay.vn/pay\r\nX-Inject: 1",
    ] {
        assert!(
            validate_zalopay_pay_url(bad, ZaloPayMode::Mock).is_err(),
            "mock should reject {bad:?}"
        );
        assert!(
            validate_zalopay_pay_url(bad, ZaloPayMode::Sandbox).is_err(),
            "sandbox should reject {bad:?}"
        );
    }
}

#[test]
fn test_mock_adapter_uses_dev_only_keys_when_env_keys_empty() {
    let adapter = ZaloPayAdapter::new_mock(MockZaloPay::new());
    assert_eq!(adapter.effective_key1(), DEV_ONLY_ZALOPAY_MOCK_KEY1);
    assert_eq!(adapter.effective_key2(), DEV_ONLY_ZALOPAY_MOCK_KEY2);
    assert_eq!(adapter.effective_app_id().unwrap(), 1);

    let now = Utc.with_ymd_and_hms(2026, 11, 24, 10, 0, 0).unwrap();
    let payload = ZaloPayCallbackData {
        app_id: 1,
        app_trans_id: "261124_mock001".to_string(),
        app_time: now.timestamp_millis(),
        app_user: "buyer-1".to_string(),
        amount: 120_000,
        embed_data: "{}".to_string(),
        item: "[]".to_string(),
        zp_trans_id: 123_456_789_012,
        server_time: now.timestamp_millis(),
        channel: 38,
        merchant_user_id: "mu-1".to_string(),
        user_fee_amount: 0,
        discount_amount: 0,
    };
    let signed = MockZaloPay::build_signed_callback(DEV_ONLY_ZALOPAY_MOCK_KEY2, &payload).unwrap();
    let verified = adapter
        .verify_callback_request(&signed, now, ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS)
        .expect("dev-only mock key2 must verify callback in mock mode");
    assert_eq!(verified.data.app_trans_id, "261124_mock001");
}

#[test]
fn test_debug_redaction_never_leaks_keys_mac_or_raw_callback_body() {
    let raw_data = r#"{"app_id":100001,"app_trans_id":"261124_secretorder","amount":500000,"zp_trans_id":999,"server_time":1795478415000}"#;
    let mac = compute_callback_mac(SYNTH_KEY2, raw_data);
    let cb_req = ZaloPayCallbackRequest {
        data: raw_data.to_string(),
        mac: mac.clone(),
        callback_type: 1,
    };
    let cb_dbg = format!("{cb_req:?}");
    assert!(
        !cb_dbg.contains("261124_secretorder"),
        "leaked raw data: {cb_dbg}"
    );
    assert!(!cb_dbg.contains(&mac), "leaked full mac: {cb_dbg}");
    assert!(cb_dbg.contains("[REDACTED]"));

    let cfg = ZaloPayConfig::from_lookup(|k| match k {
        "ZALOPAY_MODE" => Some("sandbox".to_string()),
        "ZALOPAY_APP_ID" => Some(SYNTH_APP_ID.to_string()),
        "ZALOPAY_KEY1" => Some(SYNTH_KEY1.to_string()),
        "ZALOPAY_KEY2" => Some(SYNTH_KEY2.to_string()),
        _ => None,
    })
    .unwrap();
    let adapter = ZaloPayAdapter::try_from_zalopay_config(&cfg).unwrap();
    let adapter_dbg = format!("{adapter:?}");
    assert!(!adapter_dbg.contains(SYNTH_KEY1));
    assert!(!adapter_dbg.contains(SYNTH_KEY2));
    assert!(adapter_dbg.contains("[REDACTED]"));
}

#[tokio::test]
async fn test_mock_zalopay_deterministic_and_injectable_outcomes_1_2_3_and_callback() {
    let mock = MockZaloPay::new();
    let adapter = ZaloPayAdapter::new_mock(mock.clone());
    let now = Utc.with_ymd_and_hms(2026, 11, 24, 10, 0, 0).unwrap();
    let payment_id = Uuid::parse_str("11111111-2222-3333-4444-555555555555").unwrap();
    let app_trans_id = generate_app_trans_id(payment_id, now);

    let req = ZaloPayCreateOrderInput {
        app_trans_id: app_trans_id.clone(),
        app_user: "buyer-01".to_string(),
        amount_vnd: 350_000,
        description: "Thanh toan don hang".to_string(),
        embed_data: None,
        item: None,
        callback_url: None,
        expire_duration_seconds: Some(900),
        bank_code: None,
        sub_app_id: None,
        app_time: Some(now),
    };

    let r1 = adapter.create_order(&req).await.unwrap();
    let r2 = adapter.create_order(&req).await.unwrap();
    assert_eq!(r1.status(), ZaloPayReturnCode::Success);
    assert_eq!(r1.order_url, r2.order_url);
    assert_eq!(r1.zp_trans_token, r2.zp_trans_token);
    assert!(r1.order_url.starts_with("/thanh-toan/mock-zalopay?"));

    // Default newly-created unpaid mock order queries as return_code = 3, is_processing = false.
    let q_default = adapter.query_order(&app_trans_id).await.unwrap();
    assert_eq!(q_default.return_code, 3);
    assert_eq!(q_default.status(), ZaloPayReturnCode::Processing);
    assert!(
        !q_default.is_processing,
        "unpaid mock order must report is_processing=false"
    );

    // Inject return_code = 1 (success), 2 (fail), 3 (processing).
    mock.set_order_outcome(&app_trans_id, MockBehavior::return_code(1));
    let q1 = adapter.query_order(&app_trans_id).await.unwrap();
    assert_eq!(q1.return_code, 1);
    assert_eq!(q1.amount, 350_000);
    assert_eq!(
        q1.zp_trans_id,
        MockZaloPay::deterministic_zp_trans_id(&app_trans_id)
    );
    assert!(q1.normalized_server_time().unwrap().is_some());

    mock.set_order_outcome(&app_trans_id, MockBehavior::return_code(2));
    let q2 = adapter.query_order(&app_trans_id).await.unwrap();
    assert_eq!(q2.return_code, 2);
    assert_eq!(q2.status(), ZaloPayReturnCode::Fail);

    mock.set_order_outcome(&app_trans_id, MockBehavior::unpaid());
    let q_unpaid = adapter.query_order(&app_trans_id).await.unwrap();
    assert_eq!(q_unpaid.return_code, 3);
    assert!(!q_unpaid.is_processing);

    mock.set_order_outcome(&app_trans_id, MockBehavior::processing());
    let q3 = adapter.query_order(&app_trans_id).await.unwrap();
    assert_eq!(q3.return_code, 3);
    assert!(q3.is_processing);

    // Refund + query_refund with injected 3 -> 1 transition and 2 failure.
    let m_refund_id = generate_m_refund_id(1, Uuid::new_v4(), now);
    mock.set_refund_outcome(&m_refund_id, MockBehavior::return_code(3));
    let ref_res = adapter
        .refund(&ZaloPayRefundInput {
            m_refund_id: m_refund_id.clone(),
            zp_trans_id: q1.zp_trans_id.to_string(),
            amount_vnd: 350_000,
            refund_fee_amount_vnd: None,
            description: "Buyer cancelled".to_string(),
            timestamp: Some(now),
        })
        .await
        .unwrap();
    assert_eq!(ref_res.return_code, 3);
    assert_eq!(
        ref_res.refund_id,
        MockZaloPay::deterministic_refund_id(&m_refund_id)
    );

    // Query refund sees processing (3), then after override sees success (1), then fail (2).
    let qr3 = adapter.query_refund(&m_refund_id, Some(now)).await.unwrap();
    assert_eq!(qr3.return_code, 3);

    mock.set_query_refund_outcome(&m_refund_id, MockBehavior::return_code(1));
    let qr1 = adapter.query_refund(&m_refund_id, Some(now)).await.unwrap();
    assert_eq!(qr1.return_code, 1);

    mock.set_query_refund_outcome(&m_refund_id, MockBehavior::return_code(2));
    let qr2 = adapter.query_refund(&m_refund_id, Some(now)).await.unwrap();
    assert_eq!(qr2.return_code, 2);

    // Signed mock callback verifies cleanly with verify_and_parse_callback.
    let cb_payload = ZaloPayCallbackData {
        app_id: 1,
        app_trans_id: app_trans_id.clone(),
        app_time: now.timestamp_millis(),
        app_user: "buyer-01".to_string(),
        amount: 350_000,
        embed_data: "{}".to_string(),
        item: "[]".to_string(),
        zp_trans_id: q1.zp_trans_id,
        server_time: now.timestamp_millis(),
        channel: 38,
        merchant_user_id: "zlp-user-1".to_string(),
        user_fee_amount: 0,
        discount_amount: 0,
    };
    let signed = MockZaloPay::build_signed_callback(SYNTH_KEY2, &cb_payload).unwrap();
    let verified = verify_and_parse_callback(
        &signed,
        SYNTH_KEY2,
        now + chrono::Duration::seconds(10),
        ZALOPAY_CALLBACK_REPLAY_WINDOW_SECS,
    )
    .unwrap();
    assert_eq!(verified.data.app_trans_id, app_trans_id);
    assert_eq!(verified.data.amount, 350_000);
    assert!(verified.server_time.within_window);
}

#[tokio::test]
async fn test_mock_retry_recovery_and_circuit_breaker_open_halfopen_close() {
    let mock = MockZaloPay::new();
    let cb = CircuitBreaker::new(3, Duration::from_millis(30));
    let adapter = ZaloPayAdapter::new_mock(mock.clone())
        .with_circuit_breaker(cb.clone())
        .with_mock_backoff(Duration::from_millis(1));

    // 1. Two transient network errors followed by success (max_retries=3 -> 4 attempts) -> succeeds & resets CB.
    mock.push_behavior(
        MockOperation::QueryOrder,
        MockBehavior::network_error("transient timeout 1"),
    );
    mock.push_behavior(
        MockOperation::QueryOrder,
        MockBehavior::network_error("transient timeout 2"),
    );
    mock.push_behavior(MockOperation::QueryOrder, MockBehavior::return_code(1));

    let ok = adapter.query_order("261124_retry_ok").await.unwrap();
    assert_eq!(ok.return_code, 1);
    assert_eq!(cb.state(), CircuitState::Closed);

    // 2. Persistent network errors exhaust retries and trip the circuit breaker Open.
    for i in 0..4 {
        mock.push_behavior(
            MockOperation::QueryOrder,
            MockBehavior::network_error(format!("down-{i}")),
        );
    }
    let err = adapter.query_order("261124_trip_cb").await.unwrap_err();
    assert!(matches!(err, PaymentError::Provider(_)));
    assert_eq!(cb.state(), CircuitState::Open);

    // 3. Immediate next call fails fast with CircuitOpen without hitting mock.
    let fast_fail = adapter.query_order("261124_fast_fail").await.unwrap_err();
    assert!(matches!(fast_fail, PaymentError::CircuitOpen));

    // 4. After open_duration elapses, HalfOpen probe succeeds and closes the circuit again.
    tokio::time::sleep(Duration::from_millis(40)).await;
    mock.push_behavior(MockOperation::QueryOrder, MockBehavior::return_code(1));
    let recovered = adapter.query_order("261124_recover").await.unwrap();
    assert_eq!(recovered.return_code, 1);
    assert_eq!(cb.state(), CircuitState::Closed);
}

#[tokio::test]
async fn test_sandbox_http_wire_format_retry_5xx_nonretry_4xx_and_timeout() {
    // Local loopback axum server only (NEVER calls real ZaloPay sandbox).
    let create_attempts = Arc::new(AtomicUsize::new(0));
    let create_attempts_srv = create_attempts.clone();
    let query_attempts = Arc::new(AtomicUsize::new(0));
    let query_attempts_srv = query_attempts.clone();
    let refund_attempts = Arc::new(AtomicUsize::new(0));
    let refund_attempts_srv = refund_attempts.clone();

    let app = Router::new()
        .route(
            "/v2/create",
            post(move |Json(body): Json<serde_json::Value>| {
                let counter = create_attempts_srv.clone();
                async move {
                    let n = counter.fetch_add(1, Ordering::SeqCst);
                    // Verify exact wire MAC on the request body.
                    let expected_mac = compute_create_order_mac(
                        SYNTH_KEY1,
                        body["app_id"].as_i64().unwrap() as i32,
                        body["app_trans_id"].as_str().unwrap(),
                        body["app_user"].as_str().unwrap(),
                        body["amount"].as_i64().unwrap(),
                        body["app_time"].as_i64().unwrap(),
                        body["embed_data"].as_str().unwrap(),
                        body["item"].as_str().unwrap(),
                    );
                    assert_eq!(body["mac"].as_str().unwrap(), expected_mac);
                    assert_eq!(body["expire_duration_seconds"].as_i64(), Some(900));

                    if n == 0 {
                        return (
                            StatusCode::SERVICE_UNAVAILABLE,
                            Json(serde_json::json!({"error": "temporary"})),
                        );
                    }
                    (
                        StatusCode::OK,
                        Json(serde_json::json!({
                            "return_code": 1,
                            "return_message": "success",
                            "sub_return_code": 1,
                            "sub_return_message": "",
                            "zp_trans_token": "tok-1",
                            "order_url": "https://sbgateway.zalopay.vn/openinapp?order=tok-1",
                            "order_token": "otok-1",
                            "qr_code": "qr-1"
                        })),
                    )
                }
            }),
        )
        .route(
            "/v2/query",
            post(move |Json(body): Json<serde_json::Value>| {
                let counter = query_attempts_srv.clone();
                async move {
                    counter.fetch_add(1, Ordering::SeqCst);
                    let expected_mac = compute_query_order_mac(
                        SYNTH_KEY1,
                        body["app_id"].as_i64().unwrap() as i32,
                        body["app_trans_id"].as_str().unwrap(),
                    );
                    assert_eq!(body["mac"].as_str().unwrap(), expected_mac);
                    // Non-429 4xx must NOT be retried.
                    (
                        StatusCode::BAD_REQUEST,
                        Json(serde_json::json!({"error": "bad request"})),
                    )
                }
            }),
        )
        .route(
            "/v2/refund",
            post(move |Json(body): Json<serde_json::Value>| {
                let counter = refund_attempts_srv.clone();
                async move {
                    counter.fetch_add(1, Ordering::SeqCst);
                    let expected_mac = compute_refund_mac(
                        SYNTH_KEY1,
                        body["app_id"].as_i64().unwrap() as i32,
                        body["zp_trans_id"].as_str().unwrap(),
                        body["amount"].as_i64().unwrap(),
                        body.get("refund_fee_amount").and_then(|v| v.as_i64()),
                        body["description"].as_str().unwrap(),
                        body["timestamp"].as_i64().unwrap(),
                    );
                    assert_eq!(body["mac"].as_str().unwrap(), expected_mac);
                    (
                        StatusCode::OK,
                        Json(serde_json::json!({
                            "return_code": 3,
                            "return_message": "processing",
                            "sub_return_code": 3,
                            "sub_return_message": "",
                            "refund_id": 261124000012345_i64
                        })),
                    )
                }
            }),
        )
        .route(
            "/v2/query_refund",
            post(|Json(body): Json<serde_json::Value>| async move {
                let expected_mac = compute_query_refund_mac(
                    SYNTH_KEY1,
                    body["app_id"].as_i64().unwrap() as i32,
                    body["m_refund_id"].as_str().unwrap(),
                    body["timestamp"].as_i64().unwrap(),
                );
                assert_eq!(body["mac"].as_str().unwrap(), expected_mac);
                (
                    StatusCode::OK,
                    Json(serde_json::json!({
                        "return_code": 1,
                        "return_message": "success",
                        "sub_return_code": 1,
                        "sub_return_message": ""
                    })),
                )
            }),
        );

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });

    let cfg = ZaloPayConfig::from_lookup(|k| match k {
        "ZALOPAY_MODE" => Some("sandbox".to_string()),
        "ZALOPAY_BASE_URL" => Some(format!("http://{addr}")),
        "ZALOPAY_APP_ID" => Some(SYNTH_APP_ID.to_string()),
        "ZALOPAY_KEY1" => Some(SYNTH_KEY1.to_string()),
        "ZALOPAY_KEY2" => Some(SYNTH_KEY2.to_string()),
        "ZALOPAY_HTTP_TIMEOUT_SECS" => Some("5".to_string()),
        "EXTERNAL_MAX_RETRIES" => Some("1".to_string()),
        _ => None,
    })
    .unwrap();

    let adapter = ZaloPayAdapter::try_from_zalopay_config(&cfg).unwrap();
    let now = Utc.with_ymd_and_hms(2026, 11, 24, 12, 0, 0).unwrap();

    // 1. /v2/create returns 503 then 200 -> retried once and succeeds.
    let created = adapter
        .create_order(&ZaloPayCreateOrderInput {
            app_trans_id: "261124_http001".to_string(),
            app_user: "buyer-http".to_string(),
            amount_vnd: 120_000,
            description: "HTTP wire test".to_string(),
            embed_data: None,
            item: None,
            callback_url: None,
            expire_duration_seconds: Some(900),
            bank_code: None,
            sub_app_id: None,
            app_time: Some(now),
        })
        .await
        .unwrap();
    assert_eq!(created.return_code, 1);
    assert_eq!(create_attempts.load(Ordering::SeqCst), 2);
    assert!(created
        .order_url
        .starts_with("https://sbgateway.zalopay.vn/"));

    // 2. /v2/query returns 400 -> NOT retried (exactly 1 attempt).
    let q_err = adapter.query_order("261124_http001").await.unwrap_err();
    assert!(matches!(q_err, PaymentError::Provider(ref m) if m.contains("HTTP 400")));
    assert_eq!(query_attempts.load(Ordering::SeqCst), 1);

    // 3. /v2/refund + /v2/query_refund verify wire MACs and parse responses.
    let ref_resp = adapter
        .refund(&ZaloPayRefundInput {
            m_refund_id: "261124_100001_httpref1".to_string(),
            zp_trans_id: "261124000000999".to_string(),
            amount_vnd: 120_000,
            refund_fee_amount_vnd: None,
            description: "Refund wire test".to_string(),
            timestamp: Some(now),
        })
        .await
        .unwrap();
    assert_eq!(ref_resp.return_code, 3);
    assert_eq!(ref_resp.refund_id, 261_124_000_012_345);
    assert_eq!(refund_attempts.load(Ordering::SeqCst), 1);

    let qr_resp = adapter
        .query_refund("261124_100001_httpref1", Some(now))
        .await
        .unwrap();
    assert_eq!(qr_resp.return_code, 1);

    server.abort();
}

#[tokio::test]
async fn test_sandbox_http_timeout_retries_and_trips_circuit_breaker() {
    // Local loopback server that intentionally stalls beyond client timeout.
    let attempts = Arc::new(AtomicUsize::new(0));
    let attempts_srv = attempts.clone();

    let app = Router::new().route(
        "/v2/query",
        post(move || {
            let counter = attempts_srv.clone();
            async move {
                counter.fetch_add(1, Ordering::SeqCst);
                tokio::time::sleep(Duration::from_millis(250)).await;
                (
                    StatusCode::OK,
                    Json(serde_json::json!({"return_code": 1, "return_message": "late"})),
                )
            }
        }),
    );

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });

    let mut cfg = ZaloPayConfig::from_lookup(|k| match k {
        "ZALOPAY_MODE" => Some("sandbox".to_string()),
        "ZALOPAY_BASE_URL" => Some(format!("http://{addr}")),
        "ZALOPAY_APP_ID" => Some(SYNTH_APP_ID.to_string()),
        "ZALOPAY_KEY1" => Some(SYNTH_KEY1.to_string()),
        "ZALOPAY_KEY2" => Some(SYNTH_KEY2.to_string()),
        "EXTERNAL_MAX_RETRIES" => Some("1".to_string()),
        _ => None,
    })
    .unwrap();
    cfg.http_timeout = Duration::from_millis(60);

    let cb = CircuitBreaker::new(2, Duration::from_secs(30));
    let adapter = ZaloPayAdapter::try_from_zalopay_config(&cfg)
        .unwrap()
        .with_circuit_breaker(cb.clone());

    let err = adapter.query_order("261124_timeout").await.unwrap_err();
    assert!(matches!(err, PaymentError::Provider(_)), "got: {err:?}");
    assert_eq!(attempts.load(Ordering::SeqCst), 2, "1 initial + 1 retry");
    assert_eq!(cb.state(), CircuitState::Open);

    // Next call must fail fast with CircuitOpen without hitting the slow server.
    let fast_err = adapter.query_order("261124_timeout").await.unwrap_err();
    assert!(matches!(fast_err, PaymentError::CircuitOpen));
    assert_eq!(attempts.load(Ordering::SeqCst), 2);

    server.abort();
}
