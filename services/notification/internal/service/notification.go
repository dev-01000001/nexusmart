package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
	"github.com/nexusmart/core/services/notification/internal/userclient"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrMalformedPayload = errors.New("malformed event payload")
)

// Envelope mirrors docs/contracts.md §5 canonical NATS JetStream event format.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// NotificationService manages in-app notifications, preferences (UC-B23), and event processing.
type NotificationService struct {
	repo         store.Repository
	mailer       mailer.Sender
	userResolver userclient.Resolver
	logger       *slog.Logger
}

// NewNotificationService constructs a NotificationService.
func NewNotificationService(
	repo store.Repository,
	mailSender mailer.Sender,
	userResolver userclient.Resolver,
	logger *slog.Logger,
) *NotificationService {
	if logger == nil {
		logger = slog.Default()
	}
	return &NotificationService{
		repo:         repo,
		mailer:       mailSender,
		userResolver: userResolver,
		logger:       logger,
	}
}

// ListNotifications returns paginated notifications and unread count for userID.
func (s *NotificationService) ListNotifications(ctx context.Context, userID, category string, unreadOnly bool, page, pageSize int32) ([]store.Notification, int64, int64, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, 0, 0, fmt.Errorf("%w: user_id is required", ErrInvalidArgument)
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize
	return s.repo.ListNotifications(ctx, userID, category, unreadOnly, pageSize, offset)
}

// MarkRead marks specific notification IDs or all unread notifications as read for userID.
func (s *NotificationService) MarkRead(ctx context.Context, userID string, ids []string, markAll bool) (int32, int64, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, 0, fmt.Errorf("%w: user_id is required", ErrInvalidArgument)
	}
	return s.repo.MarkRead(ctx, userID, ids, markAll)
}

// GetPreferences returns the user's preferences with UC-B23 invariants applied.
func (s *NotificationService) GetPreferences(ctx context.Context, userID string) (store.Preferences, error) {
	if strings.TrimSpace(userID) == "" {
		return store.Preferences{}, fmt.Errorf("%w: user_id is required", ErrInvalidArgument)
	}
	prefs, err := s.repo.GetPreferences(ctx, userID)
	if err != nil {
		return store.Preferences{}, err
	}
	return EnforcePreferenceGuard(prefs), nil
}

// UpdatePreferences persists user preferences while enforcing UC-B23:
// order_in_app and system_in_app cannot be disabled.
func (s *NotificationService) UpdatePreferences(ctx context.Context, prefs store.Preferences) (store.Preferences, error) {
	if strings.TrimSpace(prefs.UserID) == "" {
		return store.Preferences{}, fmt.Errorf("%w: user_id is required", ErrInvalidArgument)
	}
	guarded := EnforcePreferenceGuard(prefs)
	return s.repo.UpsertPreferences(ctx, guarded)
}

// EnforcePreferenceGuard enforces UC-B23: order & security/system in-app notifications cannot be disabled.
func EnforcePreferenceGuard(p store.Preferences) store.Preferences {
	p.OrderInApp = true
	p.SystemInApp = true
	return p
}

// ProcessEvent idempotently handles a canonical NEXUSMART event.
// External network I/O (UserService gRPC email lookup & Mailpit SMTP) executes strictly OUTSIDE
// the short Postgres idempotency transaction so DB connections are never held across network calls.
func (s *NotificationService) ProcessEvent(ctx context.Context, subject string, env Envelope) (bool, error) {
	if strings.TrimSpace(env.EventID) == "" {
		return false, fmt.Errorf("%w: missing event_id", ErrMalformedPayload)
	}
	eventType := strings.TrimSpace(env.EventType)
	if eventType == "" {
		eventType = subject
	}

	plan, err := s.buildNotificationPlan(eventType, env)
	if err != nil {
		return false, err
	}

	// Fast-path dedup check before doing any external gRPC/SMTP work.
	if processed, checkErr := s.repo.IsEventProcessed(ctx, env.EventID); checkErr == nil && processed {
		return true, nil
	}

	// Resolve target user & external email contact OUTSIDE any open Postgres transaction.
	targetUserID := plan.userID
	if targetUserID == "" && plan.lookupOrderID != "" {
		if part, found, lookupErr := s.repo.GetOrderParticipant(ctx, plan.lookupOrderID); lookupErr == nil && found {
			targetUserID = part.BuyerID
		}
	}

	var (
		resolvedEmail   string
		resolvedContact *store.UserContact
	)
	if plan.email != nil {
		resolvedEmail = strings.TrimSpace(plan.email.To)
		if resolvedEmail == "" && targetUserID != "" {
			if contact, found, cErr := s.repo.GetUserContact(ctx, targetUserID); cErr == nil && found && contact.Email != "" {
				resolvedEmail = contact.Email
			} else if s.userResolver != nil {
				email, fullName, uErr := s.userResolver.GetUserContact(ctx, targetUserID)
				if uErr != nil {
					s.logger.WarnContext(ctx, "failed to resolve user email from UserService",
						slog.String("user_id", targetUserID),
						slog.String("error", uErr.Error()),
					)
				} else if email != "" {
					resolvedEmail = email
					resolvedContact = &store.UserContact{
						UserID:   targetUserID,
						Email:    email,
						FullName: fullName,
					}
				}
			}
		}
	}

	// Short DB-only transaction: atomic processed_events dedup + contact/order cache + in-app notification.
	var emailToSend *mailer.Message
	duplicate, err := s.repo.ProcessEventTx(ctx, env.EventID, eventType, func(tx store.TxStore) error {
		if plan.cacheContact != nil {
			if err := tx.UpsertUserContact(ctx, *plan.cacheContact); err != nil {
				return fmt.Errorf("cache user contact: %w", err)
			}
		}
		if resolvedContact != nil {
			if err := tx.UpsertUserContact(ctx, *resolvedContact); err != nil {
				return fmt.Errorf("cache resolved user contact: %w", err)
			}
		}
		if plan.cacheOrder != nil {
			if err := tx.UpsertOrderParticipant(ctx, *plan.cacheOrder); err != nil {
				return fmt.Errorf("cache order participant: %w", err)
			}
		}

		userID := targetUserID
		if userID == "" && plan.lookupOrderID != "" {
			if part, found, lookupErr := tx.GetOrderParticipant(ctx, plan.lookupOrderID); lookupErr == nil && found {
				userID = part.BuyerID
			}
		}
		if userID == "" {
			return nil
		}

		prefs, prefErr := tx.GetPreferences(ctx, userID)
		if prefErr != nil {
			return prefErr
		}
		prefs = EnforcePreferenceGuard(prefs)

		// Persist in-app notification idempotently (skip if already saved before an earlier SMTP retry).
		if plan.inApp != nil && shouldSendInApp(prefs, plan.inApp.Category) {
			exists, existErr := tx.HasNotificationForEvent(ctx, env.EventID, userID)
			if existErr != nil {
				return existErr
			}
			if !exists {
				n := *plan.inApp
				n.UserID = userID
				n.EventID = env.EventID
				if _, insertErr := tx.InsertNotification(ctx, n); insertErr != nil {
					return insertErr
				}
			}
		}

		if plan.email != nil && (plan.forceEmail || shouldSendEmail(prefs, plan.category)) {
			to := resolvedEmail
			if to == "" {
				if contact, found, cErr := tx.GetUserContact(ctx, userID); cErr == nil && found {
					to = contact.Email
				}
			}
			if to != "" {
				msg := *plan.email
				msg.To = to
				emailToSend = &msg
			}
		}

		return nil
	})
	if err != nil || duplicate {
		return duplicate, err
	}

	// Dispatch SMTP email strictly OUTSIDE the Postgres transaction.
	// If SMTP fails transiently, release the processed_events lock so JetStream can retry
	// (the HasNotificationForEvent check above guarantees no duplicate in-app notification on retry).
	if emailToSend != nil && s.mailer != nil {
		if sendErr := s.mailer.Send(ctx, *emailToSend); sendErr != nil {
			if delErr := s.repo.DeleteProcessedEvent(ctx, env.EventID); delErr != nil {
				s.logger.WarnContext(ctx, "failed to clear processed_event after SMTP error",
					slog.String("event_id", env.EventID),
					slog.String("error", delErr.Error()),
				)
			}
			return false, fmt.Errorf("send email: %w", sendErr)
		}
	}

	return false, nil
}

func shouldSendInApp(p store.Preferences, category string) bool {
	switch category {
	case store.CategoryOrder:
		return true // UC-B23: order in-app cannot be disabled
	case store.CategorySystem:
		return true // UC-B23: system/security in-app cannot be disabled
	case store.CategoryPromotion:
		return p.PromotionInApp
	default:
		return true
	}
}

func shouldSendEmail(p store.Preferences, category string) bool {
	switch category {
	case store.CategoryOrder:
		return p.OrderEmail
	case store.CategorySystem:
		return p.SystemEmail
	case store.CategoryPromotion:
		return p.PromotionEmail
	default:
		return false
	}
}

type eventPlan struct {
	userID        string
	lookupOrderID string
	category      string
	inApp         *store.Notification
	email         *mailer.Message
	forceEmail    bool
	cacheContact  *store.UserContact
	cacheOrder    *store.OrderParticipant
}

func (s *NotificationService) buildNotificationPlan(eventType string, env Envelope) (*eventPlan, error) {
	now := time.Now().UTC()
	if env.OccurredAt != "" {
		if parsed, err := time.Parse(time.RFC3339, env.OccurredAt); err == nil {
			now = parsed.UTC()
		}
	}

	switch eventType {
	case "user.registered":
		var p struct {
			UserID    string `json:"user_id"`
			Email     string `json:"email"`
			FullName  string `json:"full_name"`
			OTP       string `json:"otp"`
			ExpiresAt string `json:"expires_at"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" || p.Email == "" {
			return nil, fmt.Errorf("%w: invalid user.registered payload", ErrMalformedPayload)
		}
		otpMsg := mailer.FormatOTPEmail(p.Email, p.FullName, p.OTP, p.ExpiresAt)
		return &eventPlan{
			userID:     p.UserID,
			category:   store.CategorySystem,
			forceEmail: true, // Security/OTP verification email always sends
			cacheContact: &store.UserContact{
				UserID:   p.UserID,
				Email:    p.Email,
				FullName: p.FullName,
			},
			inApp: &store.Notification{
				Category:  store.CategorySystem,
				Title:     "Chào mừng đến với NexusMart",
				Body:      "Tài khoản của bạn đã được đăng ký. Vui lòng kiểm tra email để nhập mã xác thực OTP.",
				Link:      "/verify-otp",
				CreatedAt: now,
			},
			email: &otpMsg,
		}, nil

	case "user.shop_reviewed":
		var p struct {
			ShopID      string `json:"shop_id"`
			OwnerUserID string `json:"owner_user_id"`
			ShopName    string `json:"shop_name"`
			Decision    string `json:"decision"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OwnerUserID == "" {
			return nil, fmt.Errorf("%w: invalid user.shop_reviewed payload", ErrMalformedPayload)
		}
		title := fmt.Sprintf("Kết quả duyệt gian hàng %s", p.ShopName)
		body := fmt.Sprintf("Gian hàng %s đã nhận quyết định: %s.", p.ShopName, p.Decision)
		if strings.TrimSpace(p.Reason) != "" {
			body += fmt.Sprintf(" Lý do: %s", p.Reason)
		}
		return &eventPlan{
			userID:   p.OwnerUserID,
			category: store.CategorySystem,
			inApp: &store.Notification{
				Category:  store.CategorySystem,
				Title:     title,
				Body:      body,
				Link:      "/seller/dashboard",
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Shop review update (%s): %s", p.ShopName, p.Decision),
				Body:    body + "\n",
			},
		}, nil

	case "order.created":
		var p struct {
			OrderID         string `json:"order_id"`
			OrderCode       string `json:"order_code"`
			BuyerID         string `json:"buyer_id"`
			ShopID          string `json:"shop_id"`
			PaymentMethod   string `json:"payment_method"`
			TotalPayableVND int64  `json:"total_payable_vnd"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.created payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã được tạo (%s) với tổng thanh toán %d VND.", code, p.PaymentMethod, p.TotalPayableVND)
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đặt hàng thành công %s", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Created %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "order.confirmed":
		var p struct {
			OrderID         string `json:"order_id"`
			OrderCode       string `json:"order_code"`
			BuyerID         string `json:"buyer_id"`
			ShopID          string `json:"shop_id"`
			PaymentMethod   string `json:"payment_method"`
			TotalPayableVND int64  `json:"total_payable_vnd"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.confirmed payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã được xác nhận (%d VND) và đang chờ người bán chuẩn bị.", code, p.TotalPayableVND)
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đã được xác nhận", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Confirmed %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "order.cancelled":
		var p struct {
			OrderID   string `json:"order_id"`
			OrderCode string `json:"order_code"`
			BuyerID   string `json:"buyer_id"`
			ShopID    string `json:"shop_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.cancelled payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã bị hủy.", code)
		if strings.TrimSpace(p.Reason) != "" {
			body += fmt.Sprintf(" Lý do: %s", p.Reason)
		}
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đã hủy", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Cancelled %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "order.shipped":
		var p struct {
			OrderID      string `json:"order_id"`
			OrderCode    string `json:"order_code"`
			BuyerID      string `json:"buyer_id"`
			ShopID       string `json:"shop_id"`
			CarrierCode  string `json:"carrier_code"`
			TrackingCode string `json:"tracking_code"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.shipped payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã được giao cho đơn vị vận chuyển %s (mã vận đơn: %s).", code, p.CarrierCode, p.TrackingCode)
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đang được giao", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Shipped %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "order.delivered":
		var p struct {
			OrderID      string `json:"order_id"`
			OrderCode    string `json:"order_code"`
			BuyerID      string `json:"buyer_id"`
			ShopID       string `json:"shop_id"`
			TrackingCode string `json:"tracking_code"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.delivered payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s (mã vận đơn %s) đã giao thành công. Vui lòng kiểm tra và xác nhận đã nhận hàng.", code, p.TrackingCode)
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đã giao thành công", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Delivered %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "order.completed":
		var p struct {
			OrderID     string `json:"order_id"`
			OrderCode   string `json:"order_code"`
			BuyerID     string `json:"buyer_id"`
			ShopID      string `json:"shop_id"`
			CoinsReward int64  `json:"coins_reward"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.completed payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã hoàn tất. Bạn nhận được %d xu thưởng.", code, p.CoinsReward)
		return &eventPlan{
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s hoàn tất", code),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Order Completed %s", code),
				Body:    body + "\n",
			},
		}, nil

	case "payment.succeeded":
		var p struct {
			PaymentID    string `json:"payment_id"`
			OrderID      string `json:"order_id"`
			OrderGroupID string `json:"order_group_id"`
			UserID       string `json:"user_id"`
			AmountVND    int64  `json:"amount_vnd"`
			Method       string `json:"method"`
			ProviderTxID string `json:"provider_tx_id"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.succeeded payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Thanh toán %d VND qua %s thành công (mã giao dịch: %s).", p.AmountVND, p.Method, p.ProviderTxID)
		return &eventPlan{
			userID:   p.UserID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     "Thanh toán thành công",
				Body:      body,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Payment Succeeded (%d VND)", p.AmountVND),
				Body:    body + "\n",
			},
		}, nil

	case "payment.failed":
		var p struct {
			PaymentID string `json:"payment_id"`
			OrderID   string `json:"order_id"`
			UserID    string `json:"user_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.failed payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Thanh toán thất bại cho giao dịch %s. Lý do: %s", p.PaymentID, p.Reason)
		return &eventPlan{
			userID:   p.UserID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     "Thanh toán thất bại",
				Body:      body,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: "NexusMart - Payment Failed",
				Body:    body + "\n",
			},
		}, nil

	case "payment.expired":
		var p struct {
			PaymentID string `json:"payment_id"`
			OrderID   string `json:"order_id"`
			UserID    string `json:"user_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.expired payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Phiên thanh toán %s đã hết hạn và đơn hàng đã được hủy tự động.", p.PaymentID)
		return &eventPlan{
			userID:   p.UserID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     "Thanh toán hết hạn",
				Body:      body,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: "NexusMart - Payment Expired",
				Body:    body + "\n",
			},
		}, nil

	case "payment.refunded":
		var p struct {
			RefundID  string `json:"refund_id"`
			PaymentID string `json:"payment_id"`
			OrderID   string `json:"order_id"`
			UserID    string `json:"user_id"`
			AmountVND int64  `json:"amount_vnd"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.refunded payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Khoản hoàn tiền %d VND (mã %s) đã được xử lý.", p.AmountVND, p.RefundID)
		if strings.TrimSpace(p.Reason) != "" {
			body += fmt.Sprintf(" Lý do: %s", p.Reason)
		}
		return &eventPlan{
			userID:   p.UserID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     "Hoàn tiền thành công",
				Body:      body,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Payment Refunded (%d VND)", p.AmountVND),
				Body:    body + "\n",
			},
		}, nil

	case "shipment.updated":
		var p struct {
			ShipmentID   string `json:"shipment_id"`
			OrderID      string `json:"order_id"`
			ShopID       string `json:"shop_id"`
			BuyerID      string `json:"buyer_id"`
			CarrierCode  string `json:"carrier_code"`
			TrackingCode string `json:"tracking_code"`
			Status       string `json:"status"`
			Description  string `json:"description"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" {
			return nil, fmt.Errorf("%w: invalid shipment.updated payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Vận đơn %s cập nhật trạng thái: %s.", p.TrackingCode, p.Status)
		if strings.TrimSpace(p.Description) != "" {
			body += " " + p.Description
		}
		return &eventPlan{
			userID:        p.BuyerID,
			lookupOrderID: p.OrderID,
			category:      store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Cập nhật vận chuyển (%s)", p.Status),
				Body:      body,
				Link:      "/orders/" + p.OrderID,
				CreatedAt: now,
			},
		}, nil

	default:
		// Unknown future subject: acknowledge safely without poisoning DLQ.
		return &eventPlan{}, nil
	}
}

func orderLabel(code, id string) string {
	if strings.TrimSpace(code) != "" {
		return code
	}
	return id
}

func orderLink(orderID string) string {
	if strings.TrimSpace(orderID) == "" {
		return "/orders"
	}
	return "/orders/" + orderID
}
