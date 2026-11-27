package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type notifW2BPreferencesDTO struct {
	UserID         string `json:"user_id"`
	OrderInApp     bool   `json:"order_in_app"`
	OrderEmail     bool   `json:"order_email"`
	OrderPush      bool   `json:"order_push"`
	PromotionInApp bool   `json:"promotion_in_app"`
	PromotionEmail bool   `json:"promotion_email"`
	PromotionPush  bool   `json:"promotion_push"`
	SystemInApp    bool   `json:"system_in_app"`
	SystemEmail    bool   `json:"system_email"`
	SystemPush     bool   `json:"system_push"`
}

type notifW2BMarkReadRequest struct {
	NotificationIDs []string `json:"notification_ids,omitempty"`
	MarkAll         bool     `json:"mark_all,omitempty"`
}

type notifW2BMarkReadResponse struct {
	UpdatedCount int32 `json:"updated_count"`
	UnreadCount  int64 `json:"unread_count"`
}

type notifW2BEnvelope struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	TraceID    string `json:"trace_id"`
	OccurredAt string `json:"occurred_at"`
	Payload    any    `json:"payload"`
}

var notifW2BSixDigitsRe = regexp.MustCompile(`\d{6}`)

func notifW2BNATSAddr() string {
	raw := strings.TrimSpace(os.Getenv("NATS_URL"))
	if raw == "" {
		raw = "nats://nats:4222"
	}
	raw = strings.TrimPrefix(raw, "nats://")
	if !strings.Contains(raw, ":") {
		raw += ":4222"
	}
	return raw
}

// notifW2BPublishJetStream publishes a canonical event directly to NATS JetStream (stream NEXUSMART)
// using raw stdlib TCP NATS protocol (HPUB with Nats-Msg-Id) and asserts a valid JetStream PubAck.
func notifW2BPublishJetStream(t *testing.T, subject, eventID string, payload any) {
	t.Helper()

	body, err := json.Marshal(notifW2BEnvelope{
		EventID:    eventID,
		EventType:  subject,
		TraceID:    "",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("marshal event %s: %v", subject, err)
	}

	addr := notifW2BNATSAddr()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial NATS %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	// Consume server INFO greeting.
	infoLine, err := rw.ReadString('\n')
	if err != nil || !strings.HasPrefix(infoLine, "INFO ") {
		t.Fatalf("expected NATS INFO from %s, got %q err=%v", addr, infoLine, err)
	}

	inbox := "_INBOX.notifw2b." + strings.ReplaceAll(uuid.NewString(), "-", "")
	hdr := fmt.Sprintf("NATS/1.0\r\nNats-Msg-Id: %s\r\n\r\n", eventID)
	totalLen := len(hdr) + len(body)

	cmds := fmt.Sprintf(
		"CONNECT {\"verbose\":false,\"pedantic\":false,\"protocol\":1,\"headers\":true,\"no_responders\":true}\r\n"+
			"SUB %s 1\r\n"+
			"HPUB %s %s %d %d\r\n%s%s\r\n",
		inbox, subject, inbox, len(hdr), totalLen, hdr, string(body),
	)
	if _, err := rw.WriteString(cmds); err != nil {
		t.Fatalf("write NATS HPUB %s: %v", subject, err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatalf("flush NATS HPUB %s: %v", subject, err)
	}

	for {
		line, readErr := rw.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read JetStream PubAck for %s: %v", subject, readErr)
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
		case strings.HasPrefix(trimmed, "HMSG "):
			fields := strings.Fields(trimmed)
			if len(fields) < 5 {
				t.Fatalf("unexpected NATS HMSG header %q", trimmed)
			}
			nBytes, _ := strconv.Atoi(fields[len(fields)-1])
			buf := make([]byte, nBytes+2)
			if _, err := io.ReadFull(rw, buf); err != nil {
				t.Fatalf("read HMSG body for %s: %v", subject, err)
			}
			t.Fatalf("JetStream rejected subject %q (stream NEXUSMART missing subject?): %s", subject, strings.TrimSpace(string(buf[:nBytes])))
		case strings.HasPrefix(trimmed, "MSG "):
			fields := strings.Fields(trimmed)
			if len(fields) < 4 {
				t.Fatalf("unexpected NATS MSG header %q", trimmed)
			}
			nBytes, _ := strconv.Atoi(fields[len(fields)-1])
			buf := make([]byte, nBytes+2)
			if _, err := io.ReadFull(rw, buf); err != nil {
				t.Fatalf("read JetStream PubAck body for %s: %v", subject, err)
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
				t.Fatalf("decode JetStream PubAck %q: %v", string(buf[:nBytes]), err)
			}
			if ack.Error != nil {
				t.Fatalf("JetStream PubAck error for %s: %+v", subject, ack.Error)
			}
			if ack.Stream != "NEXUSMART" {
				t.Fatalf("expected JetStream stream NEXUSMART for %s, got %q", subject, ack.Stream)
			}
			return
		}
	}
}

func notifW2BListNotifications(t *testing.T, env *testEnv, token, query string) NotificationsListResponse {
	t.Helper()
	path := "/api/v1/notifications"
	if query != "" {
		path += "?" + query
	}
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out NotificationsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	return out
}

func notifW2BWaitNotification(
	t *testing.T,
	env *testEnv,
	token string,
	desc string,
	match func(NotificationDTO) bool,
) NotificationDTO {
	t.Helper()
	var matched NotificationDTO
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, desc, func() (bool, string) {
		out := notifW2BListNotifications(t, env, token, "page=1&page_size=50")
		for _, n := range out.Items {
			if match(n) {
				matched = n
				return true, ""
			}
		}
		return false, fmt.Sprintf("checked %d notifications (unread=%d)", len(out.Items), out.UnreadCount)
	})
	return matched
}

func notifW2BCountMailpitMatches(t *testing.T, env *testEnv, toEmail string, match func(*MailpitMessage) bool) int {
	t.Helper()
	list, err := env.mailpit.ListMessages(env.ctx(t), 100)
	if err != nil {
		t.Fatalf("list mailpit messages: %v", err)
	}
	target := strings.ToLower(strings.TrimSpace(toEmail))
	count := 0
	for _, summary := range list.Messages {
		if !hasRecipient(summary.To, target) {
			continue
		}
		full, getErr := env.mailpit.GetMessage(env.ctx(t), summary.ID)
		if getErr != nil {
			continue
		}
		if match(full) {
			count++
		}
	}
	return count
}

func TestNotificationW2B(t *testing.T) {
	env := getEnv()

	t.Run("ProductReviewedApproveRejectViaNATS", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "notifw2b-prod")
		before := notifW2BListNotifications(t, env, seller.AccessToken, "page=1&page_size=5")

		prodID := "prod-w2b-" + uuid.NewString()
		prodName := "Áo Len Cổ Lọ W2B " + env.runID
		rejectReason := "Thiếu ảnh chụp tem mác sản phẩm thực tế"
		wantLink := "/seller/san-pham/" + prodID

		// 1. REJECT product.reviewed -> seller gets SYSTEM in-app + Vietnamese email with reason & product link.
		notifW2BPublishJetStream(t, "product.reviewed", "evt-w2b-prod-rej-"+uuid.NewString(), map[string]any{
			"product_id":   prodID,
			"product_name": prodName,
			"shop_id":      seller.ShopID,
			"decision":     "REJECT",
			"reason":       rejectReason,
			"occurred_at":  time.Now().UTC().Format(time.RFC3339),
		})

		rejNotif := notifW2BWaitNotification(t, env, seller.AccessToken, "seller rejected product notification", func(n NotificationDTO) bool {
			return n.Category == "SYSTEM" && n.Link == wantLink && strings.Contains(n.Body, rejectReason)
		})
		if strings.Contains(rejNotif.Title+" "+rejNotif.Body, "REJECT") {
			t.Errorf("product.reviewed REJECT notification leaked raw enum: %+v", rejNotif)
		}
		rejEmail, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, rejectReason, env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("seller rejected product email missing in Mailpit: %v", err)
		}
		if strings.Contains(rejEmail.Subject+"\n"+rejEmail.Text, "REJECT") {
			t.Errorf("product.reviewed REJECT email leaked raw enum: subj=%q", rejEmail.Subject)
		}

		// 2. APPROVE product.reviewed -> seller gets approved SYSTEM in-app + email, unread_count increments by 2 total.
		notifW2BPublishJetStream(t, "product.reviewed", "evt-w2b-prod-app-"+uuid.NewString(), map[string]any{
			"product_id":   prodID,
			"product_name": prodName,
			"shop_id":      seller.ShopID,
			"decision":     "APPROVE",
			"reason":       "",
			"occurred_at":  time.Now().UTC().Format(time.RFC3339),
		})

		appNotif := notifW2BWaitNotification(t, env, seller.AccessToken, "seller approved product notification", func(n NotificationDTO) bool {
			return n.Category == "SYSTEM" && n.Link == wantLink && strings.Contains(strings.ToLower(n.Title+" "+n.Body), "đã được duyệt")
		})
		if strings.Contains(appNotif.Title+" "+appNotif.Body, "APPROVE") {
			t.Errorf("product.reviewed APPROVE notification leaked raw enum: %+v", appNotif)
		}
		appEmail, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, "đã được phê duyệt", env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("seller approved product email missing in Mailpit: %v", err)
		}
		if strings.Contains(appEmail.Subject+"\n"+appEmail.Text, "APPROVE") {
			t.Errorf("product.reviewed APPROVE email leaked raw enum: subj=%q", appEmail.Subject)
		}

		after := notifW2BListNotifications(t, env, seller.AccessToken, "page=1&page_size=5")
		if after.UnreadCount != before.UnreadCount+2 {
			t.Fatalf("expected seller header unread_count to increase by 2 (before=%d after=%d)", before.UnreadCount, after.UnreadCount)
		}
	})

	t.Run("PromotionVoucherExpiringPreferencesDedupAndHeaderUnread", func(t *testing.T) {
		buyerA := env.registerVerifyAndLogin(t, "notifw2b-promo-a")
		buyerB := env.registerVerifyAndLogin(t, "notifw2b-promo-b")

		// buyerB disables all PROMOTION channels.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/notifications/preferences", buyerB.AccessToken, nil, notifW2BPreferencesDTO{
			OrderInApp:     true,
			OrderEmail:     true,
			OrderPush:      true,
			PromotionInApp: false,
			PromotionEmail: false,
			PromotionPush:  false,
			SystemInApp:    true,
			SystemEmail:    true,
			SystemPush:     true,
		}, http.StatusOK)

		voucherID := "vch-w2b-" + uuid.NewString()
		voucherCode := env.uniqueCode("EXP24H")
		endsAt := time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339)

		// Publish TWO promotion.voucher_expiring events with DIFFERENT event_ids for the SAME (buyerA, voucherID).
		promoPayloadA := map[string]any{
			"user_id":    buyerA.UserID,
			"voucher_id": voucherID,
			"code":       voucherCode,
			"ends_at":    endsAt,
		}
		notifW2BPublishJetStream(t, "promotion.voucher_expiring", "evt-w2b-vch-a1-"+uuid.NewString(), promoPayloadA)
		notifW2BPublishJetStream(t, "promotion.voucher_expiring", "evt-w2b-vch-a2-"+uuid.NewString(), promoPayloadA)

		// Publish promotion.voucher_expiring for buyerB + sentinel order.created to prove consumer processed past it.
		notifW2BPublishJetStream(t, "promotion.voucher_expiring", "evt-w2b-vch-b1-"+uuid.NewString(), map[string]any{
			"user_id":    buyerB.UserID,
			"voucher_id": voucherID,
			"code":       voucherCode,
			"ends_at":    endsAt,
		})
		sentinelOrderID := "ord-sentinel-" + uuid.NewString()
		notifW2BPublishJetStream(t, "order.created", "evt-w2b-sentinel-"+uuid.NewString(), map[string]any{
			"checkout_group_id": "grp-sentinel-" + uuid.NewString(),
			"order_id":          sentinelOrderID,
			"order_code":        "NM-SENTINEL-" + env.runID,
			"buyer_id":          buyerB.UserID,
			"shop_id":           "shop-sentinel",
			"payment_method":    "COD",
			"total_payable_vnd": 100000,
		})

		// Wait for buyerA PROMOTION notification and email, plus buyerB sentinel notification.
		promoNotif := notifW2BWaitNotification(t, env, buyerA.AccessToken, "buyerA PROMOTION voucher expiring notification", func(n NotificationDTO) bool {
			return n.Category == "PROMOTION" && strings.Contains(n.Title+" "+n.Body, voucherCode)
		})
		if promoNotif.Link != "/tai-khoan?tab=voucher" {
			t.Errorf("voucher expiring link = %q, want /tai-khoan?tab=voucher", promoNotif.Link)
		}
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), buyerA.Email, voucherCode, env.cfg.PollTimeout); err != nil {
			t.Fatalf("buyerA voucher expiring email missing in Mailpit: %v", err)
		}
		// Per merged sitelink.go, order.created without storefront_shop_id/published site carries empty buyer link "".
		notifW2BWaitNotification(t, env, buyerB.AccessToken, "buyerB sentinel order notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == "" && strings.Contains(n.Title+" "+n.Body, "NM-SENTINEL-"+env.runID)
		})
		// Brief settle for duplicate evtA2 to finish being ignored.
		time.Sleep(800 * time.Millisecond)

		// Verify logical dedup on buyerA: EXACTLY 1 PROMOTION notification and EXACTLY 1 email!
		promoListA := notifW2BListNotifications(t, env, buyerA.AccessToken, "category=PROMOTION&page=1&page_size=20")
		if promoListA.Total != 1 || len(promoListA.Items) != 1 {
			t.Fatalf("expected exactly 1 deduped PROMOTION notification for buyerA, got total=%d items=%+v", promoListA.Total, promoListA.Items)
		}
		mailCountA := notifW2BCountMailpitMatches(t, env, buyerA.Email, func(m *MailpitMessage) bool {
			return strings.Contains(m.Subject+"\n"+m.Text, voucherCode)
		})
		if mailCountA != 1 {
			t.Fatalf("expected exactly 1 deduped PROMOTION email for buyerA, got %d", mailCountA)
		}

		// Verify buyerB preferences respected: 0 PROMOTION notifications and 0 emails for voucherCode!
		promoListB := notifW2BListNotifications(t, env, buyerB.AccessToken, "category=PROMOTION&page=1&page_size=20")
		if promoListB.Total != 0 || len(promoListB.Items) != 0 {
			t.Fatalf("buyerB disabled promotion_in_app; expected 0 PROMOTION notifications, got %+v", promoListB)
		}
		mailCountB := notifW2BCountMailpitMatches(t, env, buyerB.Email, func(m *MailpitMessage) bool {
			return strings.Contains(m.Subject+"\n"+m.Text, voucherCode)
		})
		if mailCountB != 0 {
			t.Fatalf("buyerB disabled promotion_email; expected 0 PROMOTION emails, got %d", mailCountB)
		}

		// Verify header unread_count badge contract on buyerA (welcome SYSTEM + expiring PROMOTION = 2 unread).
		headerResp := notifW2BListNotifications(t, env, buyerA.AccessToken, "page=1&page_size=5")
		if headerResp.UnreadCount != 2 {
			t.Fatalf("expected buyerA header unread_count=2, got %d (items=%+v)", headerResp.UnreadCount, headerResp.Items)
		}
		// Category-filtered list must still report global unread_count=2 for header consistency.
		if promoListA.UnreadCount != 2 {
			t.Fatalf("expected category-filtered unread_count=2, got %d", promoListA.UnreadCount)
		}
		unreadOnlyResp := notifW2BListNotifications(t, env, buyerA.AccessToken, "unread_only=true&page=1&page_size=1")
		if unreadOnlyResp.UnreadCount != 2 || len(unreadOnlyResp.Items) != 1 {
			t.Fatalf("expected unread_only page_size=1 unread_count=2 len=1, got unread=%d len=%d", unreadOnlyResp.UnreadCount, len(unreadOnlyResp.Items))
		}

		// Mark 1 read -> unread_count decrements to 1.
		mark1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/notifications/read", buyerA.AccessToken, nil, notifW2BMarkReadRequest{
			NotificationIDs: []string{promoNotif.ID},
		}, http.StatusOK)
		var mark1 notifW2BMarkReadResponse
		if err := DecodeJSON(mark1Resp, &mark1); err != nil {
			t.Fatalf("decode mark read single: %v", err)
		}
		if mark1.UpdatedCount != 1 || mark1.UnreadCount != 1 {
			t.Fatalf("expected updated_count=1 unread_count=1, got %+v", mark1)
		}
		afterMark1 := notifW2BListNotifications(t, env, buyerA.AccessToken, "page=1&page_size=5")
		if afterMark1.UnreadCount != 1 {
			t.Fatalf("expected header unread_count=1 after marking 1 read, got %d", afterMark1.UnreadCount)
		}

		// Mark all read -> unread_count becomes 0.
		markAllResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/notifications/read", buyerA.AccessToken, nil, notifW2BMarkReadRequest{
			MarkAll: true,
		}, http.StatusOK)
		var markAll notifW2BMarkReadResponse
		if err := DecodeJSON(markAllResp, &markAll); err != nil {
			t.Fatalf("decode mark all read: %v", err)
		}
		if markAll.UnreadCount != 0 {
			t.Fatalf("expected unread_count=0 after mark_all, got %+v", markAll)
		}
		afterMarkAll := notifW2BListNotifications(t, env, buyerA.AccessToken, "page=1&page_size=5")
		if afterMarkAll.UnreadCount != 0 {
			t.Fatalf("expected header unread_count=0 after mark_all, got %d", afterMarkAll.UnreadCount)
		}
	})

	t.Run("ShopBankAccountOTPEmailOnlyAndSecurityViaNATS", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "notifw2b-bank")

		// Disable system_email to prove security bank OTP email is still forced.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/notifications/preferences", seller.AccessToken, nil, notifW2BPreferencesDTO{
			OrderInApp:     true,
			OrderEmail:     true,
			OrderPush:      true,
			PromotionInApp: true,
			PromotionEmail: true,
			PromotionPush:  true,
			SystemInApp:    true,
			SystemEmail:    false,
			SystemPush:     false,
		}, http.StatusOK)

		before := notifW2BListNotifications(t, env, seller.AccessToken, "page=1&page_size=50")
		otpCode := fmt.Sprintf("%06d", 500000+(time.Now().UnixNano()%499999))
		maskedAcct := "******7741"

		notifW2BPublishJetStream(t, "user.shop_bank_account_otp", "evt-w2b-bank-otp-"+uuid.NewString(), map[string]any{
			"user_id":               seller.UserID,
			"shop_id":               seller.ShopID,
			"email":                 seller.Email,
			"full_name":             "Chủ Shop W2B",
			"otp":                   otpCode,
			"expires_at":            time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
			"bank_code":             "VCB",
			"account_number_masked": maskedAcct,
		})

		msg, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, maskedAcct, env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("shop bank OTP email missing in Mailpit: %v", err)
		}
		if strings.Contains(msg.Subject, otpCode) || notifW2BSixDigitsRe.MatchString(msg.Subject) {
			t.Fatalf("SECURITY: bank OTP leaked into email Subject: %q", msg.Subject)
		}
		if !strings.Contains(msg.Text, otpCode) || !strings.Contains(msg.Text, "VCB") {
			t.Fatalf("bank OTP email body missing OTP or bank_code: %q", msg.Text)
		}
		if extracted := ExtractOTP(msg.Text); extracted != otpCode {
			t.Fatalf("ExtractOTP got %q, want %q", extracted, otpCode)
		}

		// Verify email-only: no new in-app notification created and OTP never persisted in notifications table.
		after := notifW2BListNotifications(t, env, seller.AccessToken, "page=1&page_size=50")
		if after.Total != before.Total {
			t.Fatalf("user.shop_bank_account_otp must be email-only; notifications total changed before=%d after=%d", before.Total, after.Total)
		}
		for _, n := range after.Items {
			if strings.Contains(n.Title+n.Body+n.Link, otpCode) {
				t.Fatalf("SECURITY: bank OTP leaked into in-app notification %s", n.ID)
			}
		}
	})

	t.Run("CrossServiceAdminProductReview", func(t *testing.T) {
		cat := env.ensureCategory(t)
		seller := env.createApprovedSeller(t, "notifw2b-xprod")
		adminTok := env.adminLogin(t)

		// Real cross-service product review: POST /api/v1/seller/products -> POST /api/v1/admin/products/{id}/review.
		skuCode := env.uniqueCode("SKU2B")
		createResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller.AccessToken, nil, CreateProductRequest{
			CategoryID:  cat.ID,
			Name:        "Sản phẩm kiểm duyệt W2B " + env.runID,
			Description: "Cross-service product.reviewed test",
			Images:      []string{"/products/ao-thun-cotton-trang.png"},
			SKUs: []SKUInputDTO{{
				SKUCode:     skuCode,
				Name:        "Mặc định",
				PriceVND:    180000,
				Stock:       10,
				WeightGrams: 300,
			}},
		}, http.StatusCreated, http.StatusOK)
		var prodOut ProductResponse
		if err := DecodeJSON(createResp, &prodOut); err != nil {
			t.Fatalf("decode create product: %v", err)
		}

		xsvcReason := "Ảnh mô tả chưa rõ chất liệu " + env.runID
		reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(prodOut.Product.ID))
		env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
			Decision: "REJECT",
			Reason:   xsvcReason,
		}, http.StatusOK)

		wantProdLink := "/seller/san-pham/" + prodOut.Product.ID
		notifW2BWaitNotification(t, env, seller.AccessToken, "seller product.reviewed notification from AdminReviewProduct", func(n NotificationDTO) bool {
			return n.Category == "SYSTEM" && n.Link == wantProdLink && strings.Contains(n.Body, xsvcReason)
		})
	})

	t.Run("CrossServiceSellerBankAccountOTP", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "notifw2b-xbank")

		// Real cross-service seller bank account OTP: POST /api/v1/seller/bank-account.
		bankResp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/seller/bank-account", seller.AccessToken, nil, map[string]string{
			"bank_code":           "VCB",
			"bank_name":           "Vietcombank",
			"account_number":      "0123456789012",
			"account_holder_name": seller.User.FullName,
		})
		if err != nil {
			t.Fatalf("POST /api/v1/seller/bank-account failed: %v", err)
		}
		if bankResp.StatusCode != http.StatusCreated && bankResp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/v1/seller/bank-account returned HTTP %d: %s", bankResp.StatusCode, string(bankResp.Body))
		}
		bankEmail, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, "VCB", env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("bank account OTP email missing in Mailpit: %v", err)
		}
		otp := ExtractOTP(bankEmail.Text)
		if otp == "" {
			t.Fatalf("could not extract 6-digit OTP from bank OTP email body: %q", bankEmail.Text)
		}
		if strings.Contains(bankEmail.Subject, otp) || notifW2BSixDigitsRe.MatchString(bankEmail.Subject) {
			t.Fatalf("SECURITY: bank OTP leaked into email Subject: %q", bankEmail.Subject)
		}
		items := notifW2BListNotifications(t, env, seller.AccessToken, "page=1&page_size=50").Items
		for _, n := range items {
			if strings.Contains(n.Title+n.Body+n.Link, otp) {
				t.Fatalf("SECURITY: bank OTP leaked into in-app notification %s", n.ID)
			}
		}
	})
}
