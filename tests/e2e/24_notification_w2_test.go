package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type notifW2PreferencesDTO struct {
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

type notifW2PreferencesResponse struct {
	Preferences notifW2PreferencesDTO `json:"preferences"`
}

type notifW2ReturnRequestBody struct {
	Reason       string   `json:"reason"`
	EvidenceURLs []string `json:"evidence_urls"`
}

type notifW2ReturnResponse struct {
	Order           OrderDTO `json:"order"`
	ReturnRequestID string   `json:"return_request_id"`
}

type notifW2ForgotPasswordRequest struct {
	Email string `json:"email"`
}

var notifW2ResetTokenRe = regexp.MustCompile(`/dat-lai-mat-khau\?token=([^\s&"']+)`)

func notifW2ListNotifications(t *testing.T, env *testEnv, token string) []NotificationDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=50", token, nil, nil, http.StatusOK)
	var out NotificationsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	return out.Items
}

func notifW2WaitNotification(
	t *testing.T,
	env *testEnv,
	token string,
	desc string,
	match func(NotificationDTO) bool,
) NotificationDTO {
	t.Helper()
	var matched NotificationDTO
	env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, desc, func() (bool, string) {
		items := notifW2ListNotifications(t, env, token)
		for _, n := range items {
			if match(n) {
				matched = n
				return true, ""
			}
		}
		return false, fmt.Sprintf("checked %d notifications", len(items))
	})
	return matched
}

func notifW2CountMailpitMatches(t *testing.T, env *testEnv, toEmail string, match func(*MailpitMessage) bool) int {
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

func notifW2CreateCODOrder(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
) OrderDTO {
	t.Helper()
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, sku.WeightGrams, sku.PriceVND)
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "notif-w2-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID: buyer.AddressID,
		FromCart:  false,
		Items: []CheckoutItemInputDTO{{
			SKUID:    sku.ID,
			Quantity: 1,
		}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{{
			ShopID:      seller.ShopID,
			CarrierCode: quote.CarrierCode,
			ServiceCode: quote.ServiceCode,
		}},
	}, http.StatusCreated, http.StatusOK)

	var out CheckoutResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode COD checkout: %v", err)
	}
	if len(out.Orders) != 1 {
		t.Fatalf("expected 1 COD sub-order, got %d", len(out.Orders))
	}
	return out.Orders[0]
}

func notifW2SendShippingWebhook(
	t *testing.T,
	env *testEnv,
	carrierCode, trackingCode, orderID, rawStatus, desc string,
) {
	t.Helper()
	path := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(carrierCode))
	resp, err := env.client.PostSignedWebhook(env.ctx(t), path, ShippingWebhookPayload{
		EventID:      "notif-w2-shp-" + uuid.NewString(),
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s returned HTTP %d: %s", path, resp.StatusCode, string(resp.Body))
	}
}

func TestNotificationW2(t *testing.T) {
	env := getEnv()
	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "notifw2-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "Ao So Mi NotifW2 "+env.runID, 250000, 30, 400)

	buyer := env.registerVerifyAndLogin(t, "notifw2-buyer")
	env.addAddress(t, buyer)

	var deliveredOrder OrderDTO

	t.Run("SellerReceivesNewOrderCancelAndUCB23Preferences", func(t *testing.T) {
		// 1. Place COD order -> seller receives in-app notification (/seller/don-hang/<id>) and Vietnamese email.
		ord := notifW2CreateCODOrder(t, env, buyer, seller, sku)
		wantSellerLink := "/seller/don-hang/" + ord.ID

		sellerNotif := notifW2WaitNotification(t, env, seller.AccessToken, "seller new order notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == wantSellerLink && strings.Contains(n.Title+" "+n.Body, ord.OrderCode)
		})
		if strings.Contains(sellerNotif.Body, "COD") || !strings.Contains(sellerNotif.Body, "₫") {
			t.Errorf("seller new order notification must be localized Vietnamese with VND, got %+v", sellerNotif)
		}

		sellerEmail, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, ord.OrderCode, env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("seller new order email missing in Mailpit: %v", err)
		}
		if strings.Contains(sellerEmail.Subject, "Order Confirmed") {
			t.Errorf("seller new order email subject must be Vietnamese, got %q", sellerEmail.Subject)
		}

		// 2. UC-B23 server-enforced guard: attempting to turn off all channels keeps order_in_app & system_in_app true.
		prefBuyer := env.registerVerifyAndLogin(t, "notifw2-prefs")
		putResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/notifications/preferences", prefBuyer.AccessToken, nil, notifW2PreferencesDTO{
			OrderInApp:     false,
			OrderEmail:     false,
			OrderPush:      false,
			PromotionInApp: false,
			PromotionEmail: false,
			PromotionPush:  false,
			SystemInApp:    false,
			SystemEmail:    false,
			SystemPush:     false,
		}, http.StatusOK)
		var putOut notifW2PreferencesResponse
		if err := DecodeJSON(putResp, &putOut); err != nil {
			t.Fatalf("decode PUT preferences: %v", err)
		}
		if !putOut.Preferences.OrderInApp || !putOut.Preferences.SystemInApp {
			t.Fatalf("UC-B23 violated on PUT preferences: %+v", putOut.Preferences)
		}
		if putOut.Preferences.OrderEmail || putOut.Preferences.PromotionInApp {
			t.Fatalf("expected optional preferences to save false, got %+v", putOut.Preferences)
		}
		getResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications/preferences", prefBuyer.AccessToken, nil, nil, http.StatusOK)
		var getOut notifW2PreferencesResponse
		if err := DecodeJSON(getResp, &getOut); err != nil {
			t.Fatalf("decode GET preferences: %v", err)
		}
		if !getOut.Preferences.OrderInApp || !getOut.Preferences.SystemInApp {
			t.Fatalf("UC-B23 violated on GET preferences: %+v", getOut.Preferences)
		}

		// 3. Buyer cancels COD order -> seller receives cancellation in-app + email.
		cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord.ID))
		env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
			"reason": "Doi y mua hang",
		}, http.StatusOK)

		notifW2WaitNotification(t, env, seller.AccessToken, "seller order cancelled notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == wantSellerLink && strings.Contains(strings.ToLower(n.Title+" "+n.Body), "hủy")
		})
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, "đã bị hủy", env.cfg.PollTimeout); err != nil {
			t.Fatalf("seller order cancelled email missing in Mailpit: %v", err)
		}
	})

	t.Run("BuyerShipmentEmailsDedupAndSellerDeliveryFailure", func(t *testing.T) {
		// 1. Ship COD order -> delivering email + delivered single deduped email.
		ord := notifW2CreateCODOrder(t, env, buyer, seller, sku)
		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: ord.CarrierCode,
			ServiceCode: ord.ServiceCode,
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil {
			t.Fatalf("decode seller ship: %v", err)
		}
		carrierCode := ord.CarrierCode
		if carrierCode == "" {
			carrierCode = "ghn"
		}

		// delivering webhook -> buyer receives Vietnamese delivering email.
		notifW2SendShippingWebhook(t, env, carrierCode, shipOut.TrackingCode, ord.ID, "delivering", "Shipper dang giao")
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), buyer.Email, "đang được vận chuyển", env.cfg.PollTimeout); err != nil {
			t.Fatalf("buyer delivering email missing in Mailpit: %v", err)
		}

		// delivered webhook -> order becomes DELIVERED; buyer receives EXACTLY 1 delivered email (not 2).
		notifW2SendShippingWebhook(t, env, carrierCode, shipOut.TrackingCode, ord.ID, "delivered", "Giao hang thanh cong")
		env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "order DELIVERED", func() (bool, string) {
			cur := env.getOrder(t, buyer.AccessToken, ord.ID)
			return StatusEquals(cur.Status, "DELIVERED"), cur.Status
		})
		// Wait briefly for both shipment.updated=delivered and order.delivered to settle in notification consumer.
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), buyer.Email, shipOut.TrackingCode, env.cfg.PollTimeout); err != nil {
			t.Fatalf("buyer delivered email missing in Mailpit: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		delivCount := notifW2CountMailpitMatches(t, env, buyer.Email, func(m *MailpitMessage) bool {
			hay := strings.ToLower(m.Subject + "\n" + m.Text)
			hasRef := strings.Contains(hay, strings.ToLower(shipOut.TrackingCode)) || strings.Contains(hay, strings.ToLower(ord.OrderCode))
			hasDelivered := strings.Contains(hay, "giao thành công") || strings.Contains(hay, "giao hàng thành công")
			return hasRef && hasDelivered
		})
		if delivCount != 1 {
			t.Fatalf("expected exactly 1 delivered email for order %s (dedup between order.delivered & shipment.updated=delivered), got %d", ord.OrderCode, delivCount)
		}
		deliveredOrder = ord

		// 2. Second shipped order fails delivery -> seller receives failed delivery in-app + email.
		ordFail := notifW2CreateCODOrder(t, env, buyer, seller, sku)
		shipFailPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordFail.ID))
		shipFailResp := env.mustDoJSON(t, http.MethodPost, shipFailPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: ordFail.CarrierCode,
			ServiceCode: ordFail.ServiceCode,
		}, http.StatusOK)
		var shipFailOut SellerShipResponse
		if err := DecodeJSON(shipFailResp, &shipFailOut); err != nil {
			t.Fatalf("decode seller ship (fail order): %v", err)
		}
		notifW2SendShippingWebhook(t, env, carrierCode, shipFailOut.TrackingCode, ordFail.ID, "failed", "Khach khong nghe may")

		wantFailSellerLink := "/seller/don-hang/" + ordFail.ID
		notifW2WaitNotification(t, env, seller.AccessToken, "seller failed delivery notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == wantFailSellerLink && strings.Contains(strings.ToLower(n.Title+" "+n.Body), "thất bại")
		})
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, "giao hàng thất bại", env.cfg.PollTimeout); err != nil {
			t.Fatalf("seller failed delivery email missing in Mailpit: %v", err)
		}
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), buyer.Email, "giao hàng thất bại", env.cfg.PollTimeout); err != nil {
			t.Fatalf("buyer failed delivery email missing in Mailpit: %v", err)
		}
	})

	t.Run("SellerReceivesReturnRequestNotification", func(t *testing.T) {
		if deliveredOrder.ID == "" {
			t.Fatalf("no delivered order from previous subtest")
		}
		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(deliveredOrder.ID))
		resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, retPath, buyer.AccessToken, nil, notifW2ReturnRequestBody{
			Reason:       "San pham bi loi duong may",
			EvidenceURLs: []string{"https://sandbox.nexusmart.local/evidence/return-1.jpg"},
		})
		if err != nil {
			t.Fatalf("POST %s failed: %v", retPath, err)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s returned HTTP %d: %s", retPath, resp.StatusCode, string(resp.Body))
		}
		var retOut notifW2ReturnResponse
		_ = DecodeJSON(resp, &retOut)
		if retOut.ReturnRequestID == "" {
			t.Fatalf("POST %s did not return return_request_id; body=%s", retPath, string(resp.Body))
		}

		wantSellerLink := "/seller/don-hang/" + deliveredOrder.ID
		wantBuyerLink := "/don-hang/" + deliveredOrder.ID

		notifW2WaitNotification(t, env, seller.AccessToken, "seller return request notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == wantSellerLink && strings.Contains(strings.ToLower(n.Title+" "+n.Body), "trả hàng")
		})
		notifW2WaitNotification(t, env, buyer.AccessToken, "buyer return request notification", func(n NotificationDTO) bool {
			return n.Category == "ORDER" && n.Link == wantBuyerLink && strings.Contains(strings.ToLower(n.Title+" "+n.Body), "trả hàng")
		})
		if _, err := env.mailpit.WaitForEmail(env.ctx(t), seller.Email, "yêu cầu trả hàng", env.cfg.PollTimeout); err != nil {
			t.Fatalf("seller return request email missing in Mailpit: %v", err)
		}
	})

	t.Run("PasswordResetEmailDeliveredToMailpit", func(t *testing.T) {
		resetUser := env.registerVerifyAndLogin(t, "notifw2-reset")
		resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/auth/forgot-password", "", nil, notifW2ForgotPasswordRequest{
			Email: resetUser.Email,
		})
		if err != nil {
			t.Fatalf("POST /api/v1/auth/forgot-password failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/v1/auth/forgot-password returned HTTP %d: %s", resp.StatusCode, string(resp.Body))
		}

		msg, err := env.mailpit.WaitForEmail(env.ctx(t), resetUser.Email, "/dat-lai-mat-khau?token=", env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("password reset email missing in Mailpit: %v", err)
		}
		matches := notifW2ResetTokenRe.FindStringSubmatch(msg.Text + "\n" + msg.HTML)
		if len(matches) != 2 || matches[1] == "" {
			t.Fatalf("password reset email did not contain /dat-lai-mat-khau?token=... link")
		}
		token := matches[1]
		if strings.Contains(msg.Subject, token) {
			t.Fatalf("SECURITY: password reset token leaked into email Subject")
		}

		// Ensure reset token is NEVER persisted in notifications table.
		items := notifW2ListNotifications(t, env, resetUser.AccessToken)
		for _, n := range items {
			if strings.Contains(n.Title, token) || strings.Contains(n.Body, token) || strings.Contains(n.Link, token) {
				t.Fatalf("SECURITY: password reset token found in notification %s", n.ID)
			}
		}
	})
}
