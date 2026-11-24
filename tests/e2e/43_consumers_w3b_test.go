package e2e

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func w3bNATSAddr(rawURL string) string {
	addr := strings.TrimSpace(rawURL)
	if addr == "" {
		addr = "nats://nats:4222"
	}
	addr = strings.TrimPrefix(addr, "nats://")
	if !strings.Contains(addr, ":") {
		addr += ":4222"
	}
	return addr
}

// w3bNATSRequest performs a single NATS request/reply over raw TCP (used for JetStream $JS.API.* queries).
func w3bNATSRequest(natsURL, subject string, payload []byte) ([]byte, error) {
	addr := w3bNATSAddr(natsURL)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial NATS %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	infoLine, err := rw.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read NATS INFO from %s: %w", addr, err)
	}
	if !strings.HasPrefix(infoLine, "INFO ") {
		return nil, fmt.Errorf("expected NATS INFO from %s, got %q", addr, infoLine)
	}

	inbox := "_INBOX.w3b." + strings.ReplaceAll(uuid.NewString(), "-", "")
	cmds := fmt.Sprintf(
		"CONNECT {\"verbose\":false,\"pedantic\":false,\"protocol\":1,\"headers\":true,\"no_responders\":true}\r\n"+
			"SUB %s 1\r\n"+
			"PUB %s %s %d\r\n%s\r\n",
		inbox, subject, inbox, len(payload), string(payload),
	)
	if _, err := rw.WriteString(cmds); err != nil {
		return nil, fmt.Errorf("write NATS PUB %s: %w", subject, err)
	}
	if err := rw.Flush(); err != nil {
		return nil, fmt.Errorf("flush NATS PUB %s: %w", subject, err)
	}

	for {
		line, readErr := rw.ReadString('\n')
		if readErr != nil {
			return nil, fmt.Errorf("read NATS reply for %s: %w", subject, readErr)
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "PING":
			_, _ = rw.WriteString("PONG\r\n")
			_ = rw.Flush()
		case trimmed == "PONG" || trimmed == "+OK" || trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "-ERR"):
			return nil, fmt.Errorf("NATS error for %s: %s", subject, trimmed)
		case strings.HasPrefix(trimmed, "HMSG "):
			fields := strings.Fields(trimmed)
			if len(fields) < 5 {
				return nil, fmt.Errorf("unexpected HMSG %q", trimmed)
			}
			nBytes, _ := strconv.Atoi(fields[len(fields)-1])
			buf := make([]byte, nBytes+2)
			if _, err := io.ReadFull(rw, buf); err != nil {
				return nil, fmt.Errorf("read HMSG body: %w", err)
			}
			return nil, fmt.Errorf("NATS HMSG response on %s: %s", subject, strings.TrimSpace(string(buf[:nBytes])))
		case strings.HasPrefix(trimmed, "MSG "):
			fields := strings.Fields(trimmed)
			if len(fields) < 4 {
				return nil, fmt.Errorf("unexpected MSG %q", trimmed)
			}
			nBytes, _ := strconv.Atoi(fields[len(fields)-1])
			buf := make([]byte, nBytes+2)
			if _, err := io.ReadFull(rw, buf); err != nil {
				return nil, fmt.Errorf("read MSG body: %w", err)
			}
			return buf[:nBytes], nil
		}
	}
}

// w3bNATSPublishRaw publishes raw bytes to a JetStream subject with an explicit Nats-Msg-Id and waits for PubAck.
func w3bNATSPublishRaw(t *testing.T, natsURL, subject, msgID string, body []byte) uint64 {
	t.Helper()
	addr := w3bNATSAddr(natsURL)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial NATS %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	infoLine, err := rw.ReadString('\n')
	if err != nil || !strings.HasPrefix(infoLine, "INFO ") {
		t.Fatalf("expected NATS INFO from %s, got %q err=%v", addr, infoLine, err)
	}

	inbox := "_INBOX.w3bpub." + strings.ReplaceAll(uuid.NewString(), "-", "")
	hdr := fmt.Sprintf("NATS/1.0\r\nNats-Msg-Id: %s\r\n\r\n", msgID)
	totalLen := len(hdr) + len(body)
	cmds := fmt.Sprintf(
		"CONNECT {\"verbose\":false,\"pedantic\":false,\"protocol\":1,\"headers\":true,\"no_responders\":true}\r\n"+
			"SUB %s 1\r\n"+
			"HPUB %s %s %d %d\r\n%s%s\r\n",
		inbox, subject, inbox, len(hdr), totalLen, hdr, string(body),
	)
	if _, err := rw.WriteString(cmds); err != nil {
		t.Fatalf("write HPUB %s: %v", subject, err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatalf("flush HPUB %s: %v", subject, err)
	}

	for {
		line, readErr := rw.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read PubAck %s: %v", subject, readErr)
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "PING":
			_, _ = rw.WriteString("PONG\r\n")
			_ = rw.Flush()
		case trimmed == "PONG" || trimmed == "+OK" || trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "-ERR"):
			t.Fatalf("NATS error publishing %s: %s", subject, trimmed)
		case strings.HasPrefix(trimmed, "MSG "):
			fields := strings.Fields(trimmed)
			nBytes, _ := strconv.Atoi(fields[len(fields)-1])
			buf := make([]byte, nBytes+2)
			if _, err := io.ReadFull(rw, buf); err != nil {
				t.Fatalf("read PubAck body: %v", err)
			}
			var ack struct {
				Stream string `json:"stream"`
				Seq    uint64 `json:"seq"`
				Error  *struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"error"`
			}
			if err := json.Unmarshal(buf[:nBytes], &ack); err != nil {
				t.Fatalf("decode PubAck %q: %v", string(buf[:nBytes]), err)
			}
			if ack.Error != nil {
				t.Fatalf("JetStream PubAck error for %s: %+v", subject, ack.Error)
			}
			return ack.Seq
		}
	}
}

type w3bJSStreamInfo struct {
	Config struct {
		Name     string   `json:"name"`
		Subjects []string `json:"subjects"`
	} `json:"config"`
	State struct {
		Messages uint64 `json:"messages"`
		FirstSeq uint64 `json:"first_seq"`
		LastSeq  uint64 `json:"last_seq"`
	} `json:"state"`
	Error *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error"`
}

type w3bJSConsumerInfo struct {
	Name   string `json:"name"`
	Config struct {
		DurableName  string `json:"durable_name"`
		DeliverGroup string `json:"deliver_group"`
		MaxDeliver   int    `json:"max_deliver"`
	} `json:"config"`
	NumPending uint64 `json:"num_pending"`
	Error      *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error"`
}

type w3bJSStoredMsg struct {
	Subject string
	Seq     uint64
	Headers string
	Data    []byte
}

func w3bNATSGetStreamInfo(natsURL, stream string) (*w3bJSStreamInfo, error) {
	raw, err := w3bNATSRequest(natsURL, "$JS.API.STREAM.INFO."+stream, nil)
	if err != nil {
		return nil, err
	}
	var out w3bJSStreamInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode stream info %s: %w", stream, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("stream info %s error: %+v", stream, out.Error)
	}
	return &out, nil
}

func w3bNATSGetConsumerInfo(natsURL, stream, consumer string) (*w3bJSConsumerInfo, error) {
	raw, err := w3bNATSRequest(natsURL, "$JS.API.CONSUMER.INFO."+stream+"."+consumer, nil)
	if err != nil {
		return nil, err
	}
	var out w3bJSConsumerInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode consumer info %s/%s: %w", stream, consumer, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("consumer info %s/%s error: %+v", stream, consumer, out.Error)
	}
	return &out, nil
}

func w3bNATSGetMsg(natsURL, stream string, req map[string]any) (*w3bJSStoredMsg, error) {
	body, _ := json.Marshal(req)
	raw, err := w3bNATSRequest(natsURL, "$JS.API.STREAM.MSG.GET."+stream, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Message *struct {
			Subject string `json:"subject"`
			Seq     uint64 `json:"seq"`
			Hdrs    string `json:"hdrs"`
			Data    string `json:"data"`
		} `json:"message"`
		Error *struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode stream msg get %s: %w", stream, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("stream msg get %s error %d: %s", stream, out.Error.Code, out.Error.Description)
	}
	if out.Message == nil {
		return nil, fmt.Errorf("stream msg get %s returned nil message", stream)
	}
	data, err := base64.StdEncoding.DecodeString(out.Message.Data)
	if err != nil {
		return nil, fmt.Errorf("decode msg data: %w", err)
	}
	var hdrs string
	if out.Message.Hdrs != "" {
		if hBytes, hErr := base64.StdEncoding.DecodeString(out.Message.Hdrs); hErr == nil {
			hdrs = string(hBytes)
		}
	}
	return &w3bJSStoredMsg{
		Subject: out.Message.Subject,
		Seq:     out.Message.Seq,
		Headers: hdrs,
		Data:    data,
	}, nil
}

func w3bNATSGetLastMsgBySubject(natsURL, stream, subject string) (*w3bJSStoredMsg, error) {
	return w3bNATSGetMsg(natsURL, stream, map[string]any{"last_by_subj": subject})
}

// w3bNATSFindDLQSince scans NEXUSMART_DLQ messages starting from seq >= startSeq matching dlqSubject
// and predicate, skipping unrelated messages produced by other tests and returning the advanced cursor
// so callers polling in a loop never re-scan already-checked non-matching messages.
// Returns (nil, nextSeq, nil) when end-of-stream (JetStream 404) is reached without a match.
func w3bNATSFindDLQSince(natsURL, dlqSubject string, startSeq uint64, match func(*w3bJSStoredMsg) bool) (*w3bJSStoredMsg, uint64, error) {
	seq := startSeq
	if seq == 0 {
		seq = 1
	}
	for step := 0; step < 500; step++ {
		msg, err := w3bNATSGetMsg(natsURL, "NEXUSMART_DLQ", map[string]any{
			"seq":          seq,
			"next_by_subj": dlqSubject,
		})
		if err != nil {
			if strings.Contains(err.Error(), "error 404:") {
				return nil, seq, nil
			}
			return nil, seq, err
		}
		seq = msg.Seq + 1
		if match(msg) {
			return msg, seq, nil
		}
	}
	return nil, seq, fmt.Errorf("scanned 500 DLQ messages on %s without match", dlqSubject)
}

func w3b43TraceID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func w3b43DLQNextSeq(t *testing.T, natsURL string) uint64 {
	t.Helper()
	info, err := w3bNATSGetStreamInfo(natsURL, "NEXUSMART_DLQ")
	if err != nil {
		t.Fatalf("get NEXUSMART_DLQ info: %v", err)
	}
	return info.State.LastSeq + 1
}

type w3b43NotificationDLQEnvelope struct {
	EventID         string `json:"event_id"`
	OriginalSubject string `json:"original_subject"`
	Error           string `json:"error"`
	Attempts        int    `json:"attempts"`
	FailedAt        string `json:"failed_at"`
	OriginalPayload string `json:"original_payload"`
}

func w3b43WaitConsumerDrained(t *testing.T, env *testEnv, durable string) {
	t.Helper()
	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "consumer "+durable+" NumPending==0", func() (bool, string) {
		ci, err := w3bNATSGetConsumerInfo(env.cfg.NATSURL, "NEXUSMART", durable)
		if err != nil {
			return false, err.Error()
		}
		return ci.NumPending == 0, fmt.Sprintf("num_pending=%d", ci.NumPending)
	})
}

// TestE2E_W3B_NATSStreamSubjectsAndConsumerConfig verifies E1/E2 & contract-r0 without restarting containers:
// - NEXUSMART stream union-merged review.> alongside all prior canonical subjects.
// - NEXUSMART_DLQ covers dlq.>.
// - All 6 durable consumers across the 5 consumer services have expected DeliverGroup, MaxDeliver >= 6, and NumPending == 0.
func TestE2E_W3B_NATSStreamSubjectsAndConsumerConfig(t *testing.T) {
	env := getEnv()

	streamInfo, err := w3bNATSGetStreamInfo(env.cfg.NATSURL, "NEXUSMART")
	if err != nil {
		t.Fatalf("get NEXUSMART stream info: %v", err)
	}
	subjSet := make(map[string]bool, len(streamInfo.Config.Subjects))
	for _, s := range streamInfo.Config.Subjects {
		subjSet[strings.TrimSpace(s)] = true
	}
	for _, want := range []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "promotion.>", "review.>"} {
		if !subjSet[want] {
			t.Fatalf("NEXUSMART stream missing required subject %q; got %v", want, streamInfo.Config.Subjects)
		}
	}

	dlqInfo, err := w3bNATSGetStreamInfo(env.cfg.NATSURL, "NEXUSMART_DLQ")
	if err != nil {
		t.Fatalf("get NEXUSMART_DLQ stream info: %v", err)
	}
	hasDLQWildcard := false
	for _, s := range dlqInfo.Config.Subjects {
		if strings.TrimSpace(s) == "dlq.>" {
			hasDLQWildcard = true
			break
		}
	}
	if !hasDLQWildcard {
		t.Fatalf("NEXUSMART_DLQ stream missing dlq.> subject; got %v", dlqInfo.Config.Subjects)
	}

	expectedConsumers := []struct {
		durable      string
		deliverGroup string
		minMaxDeliv  int
	}{
		{"promotion-order-completed", "promotion-order-completed-workers", 6},
		{"shipping-order-cancelled", "shipping-workers", 6},
		{"notification-events-v1", "notification-workers", 6},
		{"order-payment-consumer", "order-payment-workers", 6},
		{"order-shipment-consumer", "order-shipment-workers", 6},
		{"payment-order-events", "", 6}, // Rust pull consumer
	}
	for _, ec := range expectedConsumers {
		t.Run("Consumer_"+ec.durable, func(t *testing.T) {
			ci, err := w3bNATSGetConsumerInfo(env.cfg.NATSURL, "NEXUSMART", ec.durable)
			if err != nil {
				t.Fatalf("ConsumerInfo %s: %v", ec.durable, err)
			}
			if ci.Config.DeliverGroup != ec.deliverGroup {
				t.Fatalf("E1 %s deliver_group = %q, want %q", ec.durable, ci.Config.DeliverGroup, ec.deliverGroup)
			}
			if ci.Config.MaxDeliver < ec.minMaxDeliv {
				t.Fatalf("E1 %s max_deliver = %d, want >= %d", ec.durable, ci.Config.MaxDeliver, ec.minMaxDeliv)
			}
			w3b43WaitConsumerDrained(t, env, ec.durable)
		})
	}
}

// TestE2E_W3B_ConsumerDuplicateEventIdempotency verifies DB-level event/job deduplication
// by publishing duplicate events with distinct Nats-Msg-Id headers (bypassing JetStream msg-id dedup).
func TestE2E_W3B_ConsumerDuplicateEventIdempotency(t *testing.T) {
	env := getEnv()

	// 1. Promotion duplicate order.completed with identical event_id -> credits coins strictly ONCE
	//    and produces ZERO DLQ entries across payment/promotion/notification.
	t.Run("PromotionOrderCompletedDuplicateEventID", func(t *testing.T) {
		_, seller1, _ := env.ensureBaseMarketplace(t)
		buyer := env.registerVerifyAndLogin(t, "w3b43-dup-promo")
		dlqStartSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)

		// Canonical order.completed payload (services/order/internal/events/types.go & docs/contracts.md §5).
		// Coin reward formula:
		//   - order emits coins_reward = ComputeOrderRewardCoins(ord, 100 bps) = floor((R - V_plat - X) * 100 / 10000)
		//     (for subtotal_vnd=500000, platform discounts=0, coins_used=0 -> 5000 coins).
		//   - promotion consumer (services/promotion/internal/events/consumer.go:467 -> store.ProcessOrderCompletedEvent)
		//     credits payload.CoinsReward directly once per processed_events(event_id, subject) & coin_transactions(reference_id=order_id).
		const (
			subtotalVND   int64 = 500000
			commissionVND int64 = 25000
			wantCoins     int64 = 5000
		)
		evtID := "w3b43-evt-promo-" + uuid.NewString()
		ordID := uuid.NewString()
		raw, _ := json.Marshal(map[string]any{
			"event_id":    evtID,
			"event_type":  "order.completed",
			"trace_id":    w3b43TraceID(),
			"occurred_at": time.Now().UTC().Format(time.RFC3339),
			"payload": map[string]any{
				"order_id":          ordID,
				"order_code":        "ORD-W3B43-DUP",
				"buyer_id":          buyer.UserID,
				"shop_id":           seller1.ShopID,
				"subtotal_vnd":      subtotalVND,
				"commission_vnd":    commissionVND,
				"payment_fee_vnd":   int64(0),
				"total_payable_vnd": subtotalVND,
				"coins_reward":      wantCoins,
			},
		})
		// Publish twice with DIFFERENT Nats-Msg-Id so JetStream delivers both to promotion/payment/notification.
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "order.completed", "msg-1-"+evtID, raw)
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "order.completed", "msg-2-"+evtID, raw)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "promotion coins credited once", func() (bool, string) {
			bal := env.getCoinBalance(t, buyer.AccessToken)
			return bal >= wantCoins, fmt.Sprintf("balance=%d want=%d", bal, wantCoins)
		})
		w3b43WaitConsumerDrained(t, env, "promotion-order-completed")
		w3b43WaitConsumerDrained(t, env, "payment-order-events")
		w3b43WaitConsumerDrained(t, env, "notification-events-v1")

		if bal := env.getCoinBalance(t, buyer.AccessToken); bal != wantCoins {
			t.Fatalf("E1/E2: duplicate order.completed credited unexpected coins: got %d, want %d", bal, wantCoins)
		}

		// Assert ZERO DLQ messages were emitted for this valid order.completed event_id across all consumers.
		dlqHit, _, dlqErr := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.>", dlqStartSeq, func(cand *w3bJSStoredMsg) bool {
			return strings.Contains(string(cand.Data), evtID) || strings.Contains(cand.Headers, evtID)
		})
		if dlqErr != nil {
			t.Fatalf("scan NEXUSMART_DLQ for %s: %v", evtID, dlqErr)
		}
		if dlqHit != nil {
			t.Fatalf("valid canonical order.completed event %s unexpectedly routed to DLQ subject=%s hdrs=%q data=%s",
				evtID, dlqHit.Subject, dlqHit.Headers, string(dlqHit.Data))
		}
	})

	// 2. Notification shipment.create_failed deduplicates by job_id while allowing a new job_id to notify again.
	t.Run("NotificationShipmentCreateFailedJobIDDedup", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "w3b43-dup-notif")
		// Wait until notification caches seller's shop_id from shop.reviewed before publishing synthetic event.
		w3b43WaitConsumerDrained(t, env, "notification-events-v1")

		fakeOrderID := uuid.NewString()
		wantLink := "/seller/don-hang/" + fakeOrderID
		job1 := "job-1-" + uuid.NewString()
		job2 := "job-2-" + uuid.NewString()

		publishFail := func(eventID, jobID string) {
			raw, _ := json.Marshal(map[string]any{
				"event_id":    eventID,
				"event_type":  "shipment.create_failed",
				"trace_id":    w3b43TraceID(),
				"occurred_at": time.Now().UTC().Format(time.RFC3339),
				"payload": map[string]any{
					"job_id":        jobID,
					"order_id":      fakeOrderID,
					"shop_id":       seller.ShopID,
					"carrier_code":  "mock_express",
					"service_code":  "STANDARD",
					"attempts":      4,
					"error_message": "carrier 503",
					"occurred_at":   time.Now().UTC().Format(time.RFC3339),
				},
			})
			w3bNATSPublishRaw(t, env.cfg.NATSURL, "shipment.create_failed", "msg-"+eventID, raw)
		}

		countSellerNotifs := func() int {
			t.Helper()
			resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=50", seller.AccessToken, nil, nil, http.StatusOK)
			var out NotificationsListResponse
			if err := DecodeJSON(resp, &out); err != nil {
				t.Fatalf("decode notifications: %v", err)
			}
			cnt := 0
			for _, n := range out.Items {
				if n.Link == wantLink {
					cnt++
				}
			}
			return cnt
		}

		// Two events with DIFFERENT event_id but SAME job_id -> strictly 1 notification.
		publishFail("evt-j1-a-"+uuid.NewString(), job1)
		publishFail("evt-j1-b-"+uuid.NewString(), job1)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "1 notification for job1", func() (bool, string) {
			c := countSellerNotifs()
			return c >= 1, fmt.Sprintf("count=%d", c)
		})
		w3b43WaitConsumerDrained(t, env, "notification-events-v1")
		if c := countSellerNotifs(); c != 1 {
			t.Fatalf("same job_id should deduplicate to 1 notification, got %d", c)
		}

		// Third event with a NEW job_id for the same order -> creates 2nd notification.
		publishFail("evt-j2-"+uuid.NewString(), job2)
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "2 notifications after new job2", func() (bool, string) {
			c := countSellerNotifs()
			return c == 2, fmt.Sprintf("count=%d", c)
		})
	})
}

// TestE2E_W3B_ConsumerPoisonEventsDLQAndOTPRedaction publishes malformed/poison messages directly onto
// NEXUSMART and verifies each service routes them to NEXUSMART_DLQ cleanly with NumPending returning to 0
// and sensitive OTP fields redacted.
func TestE2E_W3B_ConsumerPoisonEventsDLQAndOTPRedaction(t *testing.T) {
	env := getEnv()

	// 1. Notification: malformed JSON-valid user.phone_otp_requested (missing email) -> OTP redacted in DLQ envelope.
	t.Run("Notification_PhoneOTPValidJSONMalformed_RedactsOTPInDLQ", func(t *testing.T) {
		scanSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)
		eid := "w3b43-otp-malformed-" + uuid.NewString()
		const secretOTP = "839201"
		raw, _ := json.Marshal(map[string]any{
			"event_id":    eid,
			"event_type":  "user.phone_otp_requested",
			"trace_id":    w3b43TraceID(),
			"occurred_at": time.Now().UTC().Format(time.RFC3339),
			"payload": map[string]any{
				"user_id":      "u-poison",
				"email":        "",
				"phone_masked": "09***999",
				"otp":          secretOTP,
			},
		})
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "user.phone_otp_requested", eid, raw)

		var dlqMsg *w3bJSStoredMsg
		var dlqEnv w3b43NotificationDLQEnvelope
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "dlq.user.phone_otp_requested (valid JSON)", func() (bool, string) {
			m, nextSeq, err := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.user.phone_otp_requested", scanSeq, func(cand *w3bJSStoredMsg) bool {
				var e w3b43NotificationDLQEnvelope
				if uErr := json.Unmarshal(cand.Data, &e); uErr != nil {
					return false
				}
				return e.EventID == eid && e.OriginalSubject == "user.phone_otp_requested"
			})
			scanSeq = nextSeq
			if err != nil {
				return false, err.Error()
			}
			if m == nil {
				return false, fmt.Sprintf("no matching DLQ message yet (nextSeq=%d)", scanSeq)
			}
			_ = json.Unmarshal(m.Data, &dlqEnv)
			dlqMsg = m
			return true, ""
		})
		if strings.Contains(string(dlqMsg.Data), secretOTP) || strings.Contains(dlqMsg.Headers, secretOTP) {
			t.Fatalf("E19/DLQ: secret OTP %s leaked in DLQ body/headers: hdrs=%q data=%s", secretOTP, dlqMsg.Headers, string(dlqMsg.Data))
		}
		if !strings.Contains(dlqEnv.OriginalPayload, "[REDACTED]") {
			t.Fatalf("E19/DLQ: expected [REDACTED] in notification DLQ original_payload, got %+v", dlqEnv)
		}
		w3b43WaitConsumerDrained(t, env, "notification-events-v1")
	})

	// 2. Notification: invalid JSON on user.phone_otp_requested -> DLQ envelope original_payload == `{"redacted":true}`, zero OTP leak.
	t.Run("Notification_PhoneOTPInvalidJSON_RedactedMarkerInDLQ", func(t *testing.T) {
		scanSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)
		const secretOTP = "741928"
		raw := []byte(`{"otp":"` + secretOTP + `",corrupt-json`)
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "user.phone_otp_requested", "w3b43-otp-badjson-"+uuid.NewString(), raw)

		var dlqMsg *w3bJSStoredMsg
		var dlqEnv w3b43NotificationDLQEnvelope
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "dlq.user.phone_otp_requested (invalid JSON)", func() (bool, string) {
			m, nextSeq, err := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.user.phone_otp_requested", scanSeq, func(cand *w3bJSStoredMsg) bool {
				var e w3b43NotificationDLQEnvelope
				if uErr := json.Unmarshal(cand.Data, &e); uErr != nil {
					return false
				}
				return e.OriginalSubject == "user.phone_otp_requested" &&
					e.EventID == "" &&
					strings.Contains(strings.ToLower(e.Error), "malformed") &&
					strings.TrimSpace(e.OriginalPayload) == `{"redacted":true}`
			})
			scanSeq = nextSeq
			if err != nil {
				return false, err.Error()
			}
			if m == nil {
				return false, fmt.Sprintf("no matching redacted DLQ envelope yet (nextSeq=%d)", scanSeq)
			}
			_ = json.Unmarshal(m.Data, &dlqEnv)
			dlqMsg = m
			return true, ""
		})
		if dlqEnv.OriginalPayload != `{"redacted":true}` {
			t.Fatalf("E19/DLQ: original_payload = %q, want %q", dlqEnv.OriginalPayload, `{"redacted":true}`)
		}
		if strings.Contains(string(dlqMsg.Data), secretOTP) || strings.Contains(dlqMsg.Headers, secretOTP) {
			t.Fatalf("E19/DLQ: invalid JSON OTP leaked secret %s: hdrs=%q data=%s", secretOTP, dlqMsg.Headers, string(dlqMsg.Data))
		}
		w3b43WaitConsumerDrained(t, env, "notification-events-v1")
	})

	// 3. Payment: order.delivered missing order_id (with complete canonical envelope incl. trace_id) -> routed to dlq.order.delivered with X-DLQ-Error "missing order_id".
	t.Run("Payment_OrderDeliveredMissingOrderID_RoutesToDLQ", func(t *testing.T) {
		scanSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)
		eid := "w3b43-pay-missing-oid-" + uuid.NewString()
		raw, _ := json.Marshal(map[string]any{
			"event_id":    eid,
			"event_type":  "order.delivered",
			"trace_id":    w3b43TraceID(),
			"occurred_at": time.Now().UTC().Format(time.RFC3339),
			"payload": map[string]any{
				"carrier_code":  "ghn",
				"tracking_code": "TRK-NO-OID",
			},
		})
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "order.delivered", eid, raw)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "payment dlq.order.delivered", func() (bool, string) {
			m, nextSeq, err := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.order.delivered", scanSeq, func(cand *w3bJSStoredMsg) bool {
				return strings.Contains(string(cand.Data), eid) &&
					strings.Contains(cand.Headers, "X-DLQ-Original-Subject") &&
					strings.Contains(cand.Headers, "missing order_id")
			})
			scanSeq = nextSeq
			if err != nil {
				return false, err.Error()
			}
			if m == nil {
				return false, fmt.Sprintf("no matching payment DLQ message yet (nextSeq=%d)", scanSeq)
			}
			return true, ""
		})
		w3b43WaitConsumerDrained(t, env, "payment-order-events")
	})

	// 4. Promotion: malformed JSON on order.completed -> routed to dlq.order.completed with X-DLQ-Original-Subject.
	t.Run("Promotion_MalformedOrderCompleted_RoutesToDLQ", func(t *testing.T) {
		scanSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)
		marker := "w3b43-promo-poison-" + uuid.NewString()
		raw := []byte("{malformed-json-" + marker)
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "order.completed", marker, raw)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "promotion dlq.order.completed", func() (bool, string) {
			m, nextSeq, err := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.order.completed", scanSeq, func(cand *w3bJSStoredMsg) bool {
				return strings.Contains(string(cand.Data), marker) &&
					strings.Contains(cand.Headers, "X-DLQ-Original-Subject")
			})
			scanSeq = nextSeq
			if err != nil {
				return false, err.Error()
			}
			if m == nil {
				return false, fmt.Sprintf("no matching promotion DLQ message yet (nextSeq=%d)", scanSeq)
			}
			return true, ""
		})
		w3b43WaitConsumerDrained(t, env, "promotion-order-completed")
	})

	// 5. Shipping: malformed JSON on order.cancelled -> routed to dlq.order.cancelled with X-DLQ-Original-Subject.
	t.Run("Shipping_MalformedOrderCancelled_RoutesToDLQ", func(t *testing.T) {
		scanSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)
		marker := "w3b43-ship-poison-" + uuid.NewString()
		raw := []byte("{malformed-json-" + marker)
		w3bNATSPublishRaw(t, env.cfg.NATSURL, "order.cancelled", marker, raw)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "shipping dlq.order.cancelled", func() (bool, string) {
			m, nextSeq, err := w3bNATSFindDLQSince(env.cfg.NATSURL, "dlq.order.cancelled", scanSeq, func(cand *w3bJSStoredMsg) bool {
				return strings.Contains(string(cand.Data), marker) &&
					strings.Contains(cand.Headers, "X-DLQ-Original-Subject")
			})
			scanSeq = nextSeq
			if err != nil {
				return false, err.Error()
			}
			if m == nil {
				return false, fmt.Sprintf("no matching shipping DLQ message yet (nextSeq=%d)", scanSeq)
			}
			return true, ""
		})
		w3b43WaitConsumerDrained(t, env, "shipping-order-cancelled")
	})
}
