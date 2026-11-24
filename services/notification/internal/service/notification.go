package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
	"github.com/nexusmart/core/services/notification/internal/userclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultWebBaseURL = "http://localhost:3000"

var (
	ErrInvalidArgument          = errors.New("invalid argument")
	ErrMalformedPayload         = errors.New("malformed event payload")
	ErrOrderParticipantNotFound = errors.New("order participant not found")
)

var vietnamZone = time.FixedZone("GMT+7", 7*3600)

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
	webBaseURL   string

	shopCacheMu sync.RWMutex
	shopCache   map[string]store.ShopOwner
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
		webBaseURL:   defaultWebBaseURL,
		shopCache:    make(map[string]store.ShopOwner),
	}
}

// SetWebBaseURL overrides the web base URL used in transactional email links (e.g. password reset).
func (s *NotificationService) SetWebBaseURL(raw string) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed != "" {
		s.webBaseURL = trimmed
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
// External network I/O (UserService gRPC lookup & Mailpit SMTP) executes strictly OUTSIDE
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

	// Enrich order-dependent events from cached order_participants OUTSIDE any open Postgres tx.
	if plan.lookupOrderID != "" {
		part, found, lookupErr := s.repo.GetOrderParticipant(ctx, plan.lookupOrderID)
		if lookupErr != nil {
			return false, fmt.Errorf("lookup order participant: %w", lookupErr)
		}
		if plan.applyParticipant != nil {
			plan.applyParticipant(part, found)
		}
		if found {
			for i := range plan.deliveries {
				d := &plan.deliveries[i]
				if d.role == "buyer" && d.userID == "" {
					d.userID = part.BuyerID
				}
				if d.role == "seller" && d.shopID == "" {
					d.shopID = part.ShopID
				}
			}
		}
	}

	// Resolve seller owner IDs via cache / shop_owners / UserService.GetShop OUTSIDE Postgres tx,
	// and fail fast (without recording processed_events) if required participants are not ready yet.
	var resolvedShops []store.ShopOwner
	for i := range plan.deliveries {
		d := &plan.deliveries[i]
		if d.role == "buyer" {
			if d.userID == "" && d.requireUser {
				return false, fmt.Errorf("%w: missing buyer for order %s", ErrOrderParticipantNotFound, plan.lookupOrderID)
			}
			continue
		}
		if d.role == "seller" {
			if d.userID != "" {
				continue
			}
			if d.shopID == "" {
				if d.requireUser {
					return false, fmt.Errorf("%w: missing shop for order %s", ErrOrderParticipantNotFound, plan.lookupOrderID)
				}
				continue
			}
			owner, fromRemote, shopErr := s.resolveShopOwner(ctx, d.shopID)
			if shopErr != nil {
				return false, fmt.Errorf("resolve shop owner %s: %w", d.shopID, shopErr)
			}
			if owner.OwnerUserID != "" {
				d.userID = owner.OwnerUserID
				if fromRemote {
					resolvedShops = append(resolvedShops, owner)
				}
			}
		}
	}

	// Resolve external email addresses strictly OUTSIDE any open Postgres transaction.
	resolvedEmails := make(map[string]string)
	if plan.cacheContact != nil && plan.cacheContact.UserID != "" && strings.TrimSpace(plan.cacheContact.Email) != "" {
		resolvedEmails[plan.cacheContact.UserID] = strings.TrimSpace(plan.cacheContact.Email)
	}
	var resolvedContacts []store.UserContact
	for i := range plan.deliveries {
		d := &plan.deliveries[i]
		if d.userID == "" || d.email == nil {
			continue
		}
		if strings.TrimSpace(d.email.To) != "" {
			continue
		}
		if _, ok := resolvedEmails[d.userID]; ok {
			continue
		}
		if !d.forceEmail {
			prefs, pErr := s.repo.GetPreferences(ctx, d.userID)
			if pErr != nil {
				return false, fmt.Errorf("get preferences for %s: %w", d.userID, pErr)
			}
			if !shouldSendEmail(EnforcePreferenceGuard(prefs), d.category) {
				continue
			}
		}
		contact, found, cErr := s.repo.GetUserContact(ctx, d.userID)
		if cErr != nil {
			return false, fmt.Errorf("get user contact %s: %w", d.userID, cErr)
		}
		if found && strings.TrimSpace(contact.Email) != "" {
			resolvedEmails[d.userID] = strings.TrimSpace(contact.Email)
			continue
		}
		if s.userResolver != nil {
			email, fullName, uErr := s.userResolver.GetUserContact(ctx, d.userID)
			if uErr != nil {
				if isPermanentLookupError(uErr) {
					s.logger.WarnContext(ctx, "user contact not found in UserService",
						slog.String("user_id", d.userID),
						slog.String("error", uErr.Error()),
					)
					continue
				}
				return false, fmt.Errorf("resolve user email %s: %w", d.userID, uErr)
			}
			if email != "" {
				resolvedEmails[d.userID] = email
				resolvedContacts = append(resolvedContacts, store.UserContact{
					UserID:   d.userID,
					Email:    email,
					FullName: fullName,
				})
			}
		}
	}

	// Short DB-only transaction: atomic processed_events dedup + cache upserts + in-app notifications + email dedup claims.
	type outboundEmail struct {
		userID string
		msg    mailer.Message
	}
	var emailsToSend []outboundEmail
	duplicate, err := s.repo.ProcessEventTx(ctx, env.EventID, eventType, func(tx store.TxStore) error {
		if plan.cacheContact != nil {
			if err := tx.UpsertUserContact(ctx, *plan.cacheContact); err != nil {
				return fmt.Errorf("cache user contact: %w", err)
			}
		}
		for _, c := range resolvedContacts {
			if err := tx.UpsertUserContact(ctx, c); err != nil {
				return fmt.Errorf("cache resolved user contact: %w", err)
			}
		}
		if plan.cacheOrder != nil {
			if err := tx.UpsertOrderParticipant(ctx, *plan.cacheOrder); err != nil {
				return fmt.Errorf("cache order participant: %w", err)
			}
		}
		if plan.cacheShop != nil {
			if err := tx.UpsertShopOwner(ctx, *plan.cacheShop); err != nil {
				return fmt.Errorf("cache shop owner: %w", err)
			}
		}
		for _, so := range resolvedShops {
			if err := tx.UpsertShopOwner(ctx, so); err != nil {
				return fmt.Errorf("cache resolved shop owner: %w", err)
			}
		}

		for i := range plan.deliveries {
			d := &plan.deliveries[i]
			if d.userID == "" {
				continue
			}
			prefs, prefErr := tx.GetPreferences(ctx, d.userID)
			if prefErr != nil {
				return prefErr
			}
			prefs = EnforcePreferenceGuard(prefs)

			// Persist in-app notification idempotently (skip if already saved before an earlier SMTP retry).
			if d.inApp != nil && shouldSendInApp(prefs, d.inApp.Category) {
				exists, existErr := tx.HasNotificationForEvent(ctx, env.EventID, d.userID)
				if existErr != nil {
					return existErr
				}
				if !exists {
					n := *d.inApp
					n.UserID = d.userID
					n.EventID = env.EventID
					if d.resolvePayLink {
						n.Link = resolvePaymentOrderLink(ctx, tx, d.payOrderID, d.payOrderGroupID)
					}
					if _, insertErr := tx.InsertNotification(ctx, n); insertErr != nil {
						return insertErr
					}
				}
			}

			if d.email != nil && (d.forceEmail || shouldSendEmail(prefs, d.category)) {
				alreadySent, sentErr := tx.HasEmailDedup(ctx, sentEmailDedupKey(env.EventID, d.userID))
				if sentErr != nil {
					return sentErr
				}
				if alreadySent {
					continue
				}
				to := strings.TrimSpace(d.email.To)
				if to == "" {
					to = resolvedEmails[d.userID]
				}
				if to == "" {
					if contact, found, cErr := tx.GetUserContact(ctx, d.userID); cErr == nil && found {
						to = strings.TrimSpace(contact.Email)
					}
				}
				if to != "" {
					if d.emailDedupKey != "" {
						ownerEventID, dedupErr := tx.ClaimEmailDedup(ctx, d.emailDedupKey, env.EventID)
						if dedupErr != nil {
							return dedupErr
						}
						if ownerEventID != env.EventID {
							continue
						}
					}
					msg := *d.email
					msg.To = to
					emailsToSend = append(emailsToSend, outboundEmail{userID: d.userID, msg: msg})
				}
			}
		}

		return nil
	})
	if err != nil || duplicate {
		return duplicate, err
	}

	// Update in-memory shop owner cache after DB commit succeeds.
	if plan.cacheShop != nil {
		s.putCachedShopOwner(*plan.cacheShop)
	}
	for _, so := range resolvedShops {
		s.putCachedShopOwner(so)
	}

	// Dispatch SMTP emails strictly OUTSIDE the Postgres transaction.
	// Record per-(event_id, user_id) sent marker immediately after each successful send so
	// retrying a failed later recipient never duplicates emails already sent to earlier recipients.
	if s.mailer != nil {
		for _, item := range emailsToSend {
			if sendErr := s.mailer.Send(ctx, item.msg); sendErr != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				delErr := s.repo.DeleteProcessedEvent(cleanupCtx, env.EventID)
				cleanupCancel()
				if delErr != nil {
					s.logger.WarnContext(ctx, "failed to clear processed_event after SMTP error",
						slog.String("event_id", env.EventID),
						slog.String("error", delErr.Error()),
					)
				}
				return false, fmt.Errorf("send email: %w", sendErr)
			}
			dedupCtx, dedupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			recErr := s.repo.RecordEmailDedup(dedupCtx, sentEmailDedupKey(env.EventID, item.userID), env.EventID)
			dedupCancel()
			if recErr != nil {
				s.logger.WarnContext(ctx, "failed to record sent email dedup",
					slog.String("event_id", env.EventID),
					slog.String("user_id", item.userID),
					slog.String("error", recErr.Error()),
				)
			}
		}
	}

	return false, nil
}

func (s *NotificationService) resolveShopOwner(ctx context.Context, shopID string) (store.ShopOwner, bool, error) {
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return store.ShopOwner{}, false, nil
	}
	if cached, ok := s.getCachedShopOwner(shopID); ok {
		return cached, false, nil
	}
	stored, found, err := s.repo.GetShopOwner(ctx, shopID)
	if err != nil {
		return store.ShopOwner{}, false, err
	}
	if found && stored.OwnerUserID != "" {
		s.putCachedShopOwner(stored)
		return stored, false, nil
	}
	if s.userResolver == nil {
		return store.ShopOwner{}, false, nil
	}
	ownerID, shopName, err := s.userResolver.GetShopOwner(ctx, shopID)
	if err != nil {
		if isPermanentLookupError(err) {
			s.logger.WarnContext(ctx, "shop not found in UserService",
				slog.String("shop_id", shopID),
				slog.String("error", err.Error()),
			)
			return store.ShopOwner{}, false, nil
		}
		return store.ShopOwner{}, false, err
	}
	if ownerID == "" {
		return store.ShopOwner{}, false, nil
	}
	return store.ShopOwner{
		ShopID:      shopID,
		OwnerUserID: ownerID,
		ShopName:    shopName,
	}, true, nil
}

func (s *NotificationService) getCachedShopOwner(shopID string) (store.ShopOwner, bool) {
	s.shopCacheMu.RLock()
	defer s.shopCacheMu.RUnlock()
	so, ok := s.shopCache[shopID]
	return so, ok && so.OwnerUserID != ""
}

func (s *NotificationService) putCachedShopOwner(so store.ShopOwner) {
	if so.ShopID == "" || so.OwnerUserID == "" {
		return
	}
	s.shopCacheMu.Lock()
	defer s.shopCacheMu.Unlock()
	if strings.TrimSpace(so.ShopName) == "" {
		if existing, ok := s.shopCache[so.ShopID]; ok && existing.ShopName != "" {
			so.ShopName = existing.ShopName
		}
	}
	s.shopCache[so.ShopID] = so
}

func isPermanentLookupError(err error) bool {
	code := status.Code(err)
	return code == codes.NotFound || code == codes.InvalidArgument
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

type recipientDelivery struct {
	role            string // "buyer", "seller", or "user"
	userID          string
	shopID          string
	requireUser     bool
	category        string
	inApp           *store.Notification
	email           *mailer.Message
	forceEmail      bool
	emailDedupKey   string
	resolvePayLink  bool
	payOrderID      string
	payOrderGroupID string
}

type eventPlan struct {
	lookupOrderID    string
	applyParticipant func(part store.OrderParticipant, found bool)
	cacheContact     *store.UserContact
	cacheOrder       *store.OrderParticipant
	cacheShop        *store.ShopOwner
	deliveries       []recipientDelivery
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
			IsResend  bool   `json:"is_resend"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" || p.Email == "" {
			return nil, fmt.Errorf("%w: invalid user.registered payload", ErrMalformedPayload)
		}
		otpMsg := mailer.FormatOTPEmail(p.Email, p.FullName, p.OTP, p.ExpiresAt)
		var welcomeNotif *store.Notification
		if !p.IsResend {
			welcomeNotif = &store.Notification{
				Category:  store.CategorySystem,
				Title:     "Chào mừng đến với NexusMart",
				Body:      "Tài khoản của bạn đã được đăng ký. Vui lòng kiểm tra email để nhập mã xác minh OTP.",
				Link:      "/dang-ky",
				CreatedAt: now,
			}
		}
		return &eventPlan{
			cacheContact: &store.UserContact{
				UserID:   p.UserID,
				Email:    p.Email,
				FullName: p.FullName,
			},
			deliveries: []recipientDelivery{{
				role:       "user",
				userID:     p.UserID,
				category:   store.CategorySystem,
				forceEmail: true, // Security/OTP verification email always sends
				inApp:      welcomeNotif,
				email:      &otpMsg,
			}},
		}, nil

	case "user.password_reset_requested":
		var p struct {
			UserID     string `json:"user_id"`
			Email      string `json:"email"`
			FullName   string `json:"full_name"`
			ResetToken string `json:"reset_token"`
			ExpiresAt  string `json:"expires_at"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil ||
			strings.TrimSpace(p.UserID) == "" ||
			strings.TrimSpace(p.Email) == "" ||
			strings.TrimSpace(p.ResetToken) == "" {
			return nil, fmt.Errorf("%w: invalid user.password_reset_requested payload", ErrMalformedPayload)
		}
		resetLink := fmt.Sprintf("%s/dat-lai-mat-khau?token=%s",
			strings.TrimRight(s.webBaseURL, "/"),
			url.QueryEscape(strings.TrimSpace(p.ResetToken)),
		)
		resetMsg := mailer.FormatPasswordResetEmail(p.Email, p.FullName, resetLink, p.ExpiresAt)
		return &eventPlan{
			cacheContact: &store.UserContact{
				UserID:   p.UserID,
				Email:    p.Email,
				FullName: p.FullName,
			},
			deliveries: []recipientDelivery{{
				role:       "user",
				userID:     p.UserID,
				category:   store.CategorySystem,
				forceEmail: true, // Security password reset email always sends
				// Security: reset_token and resetLink are NEVER stored in the notifications table.
				inApp: &store.Notification{
					Category:  store.CategorySystem,
					Title:     "Yêu cầu đặt lại mật khẩu",
					Body:      "Hệ thống đã nhận được yêu cầu đặt lại mật khẩu cho tài khoản của bạn. Vui lòng kiểm tra email để tiếp tục. Nếu không phải bạn thực hiện, hãy bảo vệ tài khoản ngay.",
					Link:      "/tai-khoan",
					CreatedAt: now,
				},
				email: &resetMsg,
			}},
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
		decision := strings.ToUpper(strings.TrimSpace(p.Decision))
		decisionLabel := shopReviewDecisionLabel(decision)
		title := fmt.Sprintf("Kết quả duyệt gian hàng %s", p.ShopName)
		link := "/tai-khoan?tab=gian-hang"
		var body string
		switch decision {
		case "APPROVE", "APPROVED":
			link = "/seller"
			body = fmt.Sprintf("Gian hàng %s đã được phê duyệt. Bạn có thể quản lý sản phẩm và đơn hàng trên Kênh Người Bán.", p.ShopName)
		case "REJECT", "REJECTED":
			body = fmt.Sprintf("Hồ sơ gian hàng %s chưa được duyệt.", p.ShopName)
		case "REQUEST_INFO":
			body = fmt.Sprintf("Hồ sơ gian hàng %s cần bổ sung thông tin.", p.ShopName)
		default:
			body = fmt.Sprintf("Gian hàng %s có cập nhật trạng thái duyệt: %s.", p.ShopName, decisionLabel)
		}
		if reason := strings.TrimSpace(p.Reason); reason != "" {
			if decision == "REQUEST_INFO" {
				body += fmt.Sprintf(" Yêu cầu: %s", reason)
			} else {
				body += fmt.Sprintf(" Lý do: %s", reason)
			}
		}
		var cacheShop *store.ShopOwner
		if strings.TrimSpace(p.ShopID) != "" {
			cacheShop = &store.ShopOwner{
				ShopID:      strings.TrimSpace(p.ShopID),
				OwnerUserID: strings.TrimSpace(p.OwnerUserID),
				ShopName:    strings.TrimSpace(p.ShopName),
			}
		}
		return &eventPlan{
			cacheShop: cacheShop,
			deliveries: []recipientDelivery{{
				role:     "user",
				userID:   p.OwnerUserID,
				category: store.CategorySystem,
				inApp: &store.Notification{
					Category:  store.CategorySystem,
					Title:     title,
					Body:      body,
					Link:      link,
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Kết quả duyệt gian hàng %s: %s", p.ShopName, decisionLabel),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "user.shop_status_changed":
		return buildShopStatusChangedPlan(env.Payload, env.OccurredAt, now)

	case "user.shop_bank_account_otp":
		var p struct {
			UserID              string `json:"user_id"`
			ShopID              string `json:"shop_id"`
			Email               string `json:"email"`
			FullName            string `json:"full_name"`
			OTP                 string `json:"otp"`
			ExpiresAt           string `json:"expires_at"`
			BankCode            string `json:"bank_code"`
			AccountNumberMasked string `json:"account_number_masked"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil ||
			strings.TrimSpace(p.UserID) == "" ||
			strings.TrimSpace(p.Email) == "" ||
			strings.TrimSpace(p.OTP) == "" {
			return nil, fmt.Errorf("%w: invalid user.shop_bank_account_otp payload", ErrMalformedPayload)
		}
		userID := strings.TrimSpace(p.UserID)
		emailAddr := strings.TrimSpace(p.Email)
		otpMsg := mailer.FormatShopBankAccountOTPEmail(
			emailAddr,
			p.FullName,
			p.OTP,
			p.ExpiresAt,
			p.BankCode,
			p.AccountNumberMasked,
		)
		var cacheShop *store.ShopOwner
		if shopID := strings.TrimSpace(p.ShopID); shopID != "" {
			cacheShop = &store.ShopOwner{
				ShopID:      shopID,
				OwnerUserID: userID,
			}
		}
		return &eventPlan{
			cacheContact: &store.UserContact{
				UserID:   userID,
				Email:    emailAddr,
				FullName: strings.TrimSpace(p.FullName),
			},
			cacheShop: cacheShop,
			deliveries: []recipientDelivery{{
				role:       "user",
				userID:     userID,
				category:   store.CategorySystem,
				forceEmail: true, // Security OTP email always sends; never stored in-app or logged
				inApp:      nil,
				email:      &otpMsg,
			}},
		}, nil

	case "user.phone_otp_requested":
		var p struct {
			UserID      string `json:"user_id"`
			Email       string `json:"email"`
			FullName    string `json:"full_name"`
			PhoneMasked string `json:"phone_masked"`
			OTP         string `json:"otp"`
			ExpiresAt   string `json:"expires_at"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil ||
			strings.TrimSpace(p.UserID) == "" ||
			strings.TrimSpace(p.Email) == "" ||
			strings.TrimSpace(p.OTP) == "" {
			return nil, fmt.Errorf("%w: invalid user.phone_otp_requested payload", ErrMalformedPayload)
		}
		userID := strings.TrimSpace(p.UserID)
		emailAddr := strings.TrimSpace(p.Email)
		otpMsg := mailer.FormatPhoneOTPEmail(
			emailAddr,
			p.FullName,
			p.PhoneMasked,
			p.OTP,
			p.ExpiresAt,
		)
		return &eventPlan{
			cacheContact: &store.UserContact{
				UserID:   userID,
				Email:    emailAddr,
				FullName: strings.TrimSpace(p.FullName),
			},
			deliveries: []recipientDelivery{{
				role:       "user",
				userID:     userID,
				category:   store.CategorySystem,
				forceEmail: true, // Security SMS-mock OTP email always sends; never stored in-app or logged
				inApp:      nil,
				email:      &otpMsg,
			}},
		}, nil

	case "product.reviewed":
		var p struct {
			ProductID   string `json:"product_id"`
			ProductName string `json:"product_name"`
			ShopID      string `json:"shop_id"`
			Decision    string `json:"decision"`
			Reason      string `json:"reason"`
			OccurredAt  string `json:"occurred_at"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil ||
			strings.TrimSpace(p.ProductID) == "" ||
			strings.TrimSpace(p.ShopID) == "" {
			return nil, fmt.Errorf("%w: invalid product.reviewed payload", ErrMalformedPayload)
		}
		productID := strings.TrimSpace(p.ProductID)
		shopID := strings.TrimSpace(p.ShopID)
		productName := strings.TrimSpace(p.ProductName)
		if productName == "" {
			productName = productID
		}
		if env.OccurredAt == "" && strings.TrimSpace(p.OccurredAt) != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(p.OccurredAt)); parseErr == nil {
				now = parsed.UTC()
			}
		}
		decision := strings.ToUpper(strings.TrimSpace(p.Decision))
		reason := strings.TrimSpace(p.Reason)
		var title, body, emailSubject string
		switch decision {
		case "APPROVE", "APPROVED":
			title = fmt.Sprintf("Sản phẩm %s đã được duyệt", productName)
			body = fmt.Sprintf("Sản phẩm %s đã được phê duyệt và sẵn sàng hiển thị trên NexusMart.", productName)
			if reason != "" {
				body += fmt.Sprintf(" Ghi chú: %s.", strings.TrimSuffix(reason, "."))
			}
			emailSubject = fmt.Sprintf("NexusMart - Sản phẩm %s đã được duyệt", productName)
		case "REJECT", "REJECTED":
			title = fmt.Sprintf("Sản phẩm %s bị từ chối duyệt", productName)
			body = fmt.Sprintf("Sản phẩm %s chưa được phê duyệt.", productName)
			if reason != "" {
				body += fmt.Sprintf(" Lý do: %s.", strings.TrimSuffix(reason, "."))
			}
			body += " Vui lòng kiểm tra, chỉnh sửa thông tin sản phẩm và gửi duyệt lại."
			emailSubject = fmt.Sprintf("NexusMart - Sản phẩm %s bị từ chối duyệt", productName)
		default:
			title = fmt.Sprintf("Cập nhật kiểm duyệt sản phẩm %s", productName)
			body = fmt.Sprintf("Sản phẩm %s vừa có cập nhật trạng thái kiểm duyệt.", productName)
			if reason != "" {
				body += fmt.Sprintf(" Ghi chú: %s.", strings.TrimSuffix(reason, "."))
			}
			emailSubject = fmt.Sprintf("NexusMart - Cập nhật kiểm duyệt sản phẩm %s", productName)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:     "seller",
				shopID:   shopID,
				category: store.CategorySystem,
				inApp: &store.Notification{
					Category:  store.CategorySystem,
					Title:     title,
					Body:      body,
					Link:      sellerProductLink(productID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: emailSubject,
					Body:    body + "\n",
				},
			}},
		}, nil

	case "promotion.voucher_expiring":
		var p struct {
			UserID    string `json:"user_id"`
			VoucherID string `json:"voucher_id"`
			Code      string `json:"code"`
			EndsAt    string `json:"ends_at"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil ||
			strings.TrimSpace(p.UserID) == "" ||
			strings.TrimSpace(p.VoucherID) == "" ||
			strings.TrimSpace(p.Code) == "" {
			return nil, fmt.Errorf("%w: invalid promotion.voucher_expiring payload", ErrMalformedPayload)
		}
		userID := strings.TrimSpace(p.UserID)
		voucherID := strings.TrimSpace(p.VoucherID)
		code := strings.TrimSpace(p.Code)
		dedupKey := voucherExpiringDedupKey(userID, voucherID)
		title := fmt.Sprintf("Voucher %s sắp hết hạn", code)
		var body string
		if endsAt := formatVietnamTime(p.EndsAt); endsAt != "" {
			body = fmt.Sprintf("Mã giảm giá %s trong ví của bạn sẽ hết hạn vào %s. Hãy sử dụng trước khi ưu đãi kết thúc.", code, endsAt)
		} else {
			body = fmt.Sprintf("Mã giảm giá %s trong ví của bạn sắp hết hạn trong vòng 24 giờ tới. Hãy sử dụng trước khi ưu đãi kết thúc.", code)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:          "user",
				userID:        userID,
				category:      store.CategoryPromotion,
				emailDedupKey: dedupKey,
				inApp: &store.Notification{
					Category:  store.CategoryPromotion,
					Title:     title,
					Body:      body,
					Link:      "/tai-khoan?tab=voucher",
					DedupKey:  dedupKey,
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Voucher %s sắp hết hạn", code),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "order.created":
		var p struct {
			CheckoutGroupID string `json:"checkout_group_id"`
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
		amount := FormatVND(p.TotalPayableVND)
		var title, body, emailSubject string
		if isCOD(p.PaymentMethod) {
			title = "Đặt hàng thành công, thanh toán khi nhận hàng"
			body = fmt.Sprintf("Đặt hàng thành công, thanh toán khi nhận hàng cho đơn %s với tổng thanh toán %s.", code, amount)
			emailSubject = fmt.Sprintf("NexusMart - Đặt hàng thành công %s (Thanh toán khi nhận hàng)", code)
		} else {
			title = fmt.Sprintf("Đặt hàng thành công %s", code)
			methodLabel := paymentMethodLabel(p.PaymentMethod)
			if p.TotalPayableVND > 0 {
				body = fmt.Sprintf("Đơn hàng %s đã được tạo (%s) với tổng thanh toán %s. Vui lòng hoàn tất thanh toán để xác nhận đơn.", code, methodLabel, amount)
			} else {
				body = fmt.Sprintf("Đơn hàng %s đã được tạo (%s) với tổng thanh toán %s.", code, methodLabel, amount)
			}
			emailSubject = fmt.Sprintf("NexusMart - Đặt hàng thành công %s", code)
		}
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:         p.OrderID,
				BuyerID:         p.BuyerID,
				ShopID:          p.ShopID,
				OrderCode:       p.OrderCode,
				CheckoutGroupID: p.CheckoutGroupID,
			},
			deliveries: []recipientDelivery{{
				role:     "buyer",
				userID:   p.BuyerID,
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     title,
					Body:      body,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: emailSubject,
					Body:    body + "\n",
				},
			}},
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
		amount := FormatVND(p.TotalPayableVND)
		var buyerBody, sellerBody string
		if isCOD(p.PaymentMethod) {
			buyerBody = fmt.Sprintf("Đơn hàng %s đã được xác nhận (thanh toán khi nhận hàng: %s) và đang chờ người bán chuẩn bị.", code, amount)
			sellerBody = fmt.Sprintf("Gian hàng có đơn hàng mới %s (thanh toán khi nhận hàng: %s). Vui lòng xác nhận và chuẩn bị hàng.", code, amount)
		} else {
			methodLabel := paymentMethodLabel(p.PaymentMethod)
			buyerBody = fmt.Sprintf("Đơn hàng %s đã được xác nhận (%s, tổng thanh toán %s) và đang chờ người bán chuẩn bị.", code, methodLabel, amount)
			sellerBody = fmt.Sprintf("Gian hàng có đơn hàng mới %s (%s, tổng thanh toán %s). Vui lòng xác nhận và chuẩn bị hàng.", code, methodLabel, amount)
		}
		deliveries := []recipientDelivery{{
			role:     "buyer",
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đã được xác nhận", code),
				Body:      buyerBody,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Xác nhận đơn hàng %s", code),
				Body:    buyerBody + "\n",
			},
		}}
		if strings.TrimSpace(p.ShopID) != "" {
			deliveries = append(deliveries, recipientDelivery{
				role:     "seller",
				shopID:   strings.TrimSpace(p.ShopID),
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng mới %s", code),
					Body:      sellerBody,
					Link:      sellerOrderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng mới %s", code),
					Body:    sellerBody + "\n",
				},
			})
		}
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			deliveries: deliveries,
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
		reasonSuffix := ""
		if reason := localizeReason(p.Reason); reason != "" {
			reasonSuffix = fmt.Sprintf(" Lý do: %s", reason)
		}
		buyerBody := fmt.Sprintf("Đơn hàng %s đã bị hủy.%s", code, reasonSuffix)
		sellerBody := fmt.Sprintf("Đơn hàng %s của gian hàng đã bị hủy.%s", code, reasonSuffix)
		deliveries := []recipientDelivery{{
			role:     "buyer",
			userID:   p.BuyerID,
			category: store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     fmt.Sprintf("Đơn hàng %s đã hủy", code),
				Body:      buyerBody,
				Link:      orderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Hủy đơn hàng %s", code),
				Body:    buyerBody + "\n",
			},
		}}
		if strings.TrimSpace(p.ShopID) != "" {
			deliveries = append(deliveries, recipientDelivery{
				role:     "seller",
				shopID:   strings.TrimSpace(p.ShopID),
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng %s đã bị hủy", code),
					Body:      sellerBody,
					Link:      sellerOrderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng %s đã bị hủy", code),
					Body:    sellerBody + "\n",
				},
			})
		}
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			deliveries: deliveries,
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
		body := fmt.Sprintf("Đơn hàng %s đã được bàn giao cho %s (mã vận đơn: %s).", code, carrierLabel(p.CarrierCode), p.TrackingCode)
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			deliveries: []recipientDelivery{{
				role:     "buyer",
				userID:   p.BuyerID,
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng %s đang được giao", code),
					Body:      body,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng %s đang được giao", code),
					Body:    body + "\n",
				},
			}},
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
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			deliveries: []recipientDelivery{{
				role:          "buyer",
				userID:        p.BuyerID,
				category:      store.CategoryOrder,
				emailDedupKey: deliveredEmailDedupKey(p.OrderID),
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng %s đã giao thành công", code),
					Body:      body,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng %s đã giao thành công", code),
					Body:    body + "\n",
				},
			}},
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
		var body string
		if p.CoinsReward > 0 {
			body = fmt.Sprintf("Đơn hàng %s đã hoàn tất. Bạn nhận được %s xu thưởng.", code, FormatInt(p.CoinsReward))
		} else {
			body = fmt.Sprintf("Đơn hàng %s đã hoàn tất. Cảm ơn bạn đã mua sắm tại NexusMart.", code)
		}
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:   p.OrderID,
				BuyerID:   p.BuyerID,
				ShopID:    p.ShopID,
				OrderCode: p.OrderCode,
			},
			deliveries: []recipientDelivery{{
				role:     "buyer",
				userID:   p.BuyerID,
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng %s hoàn tất", code),
					Body:      body,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng %s đã hoàn tất", code),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "order.returned":
		var p struct {
			OrderID         string `json:"order_id"`
			OrderCode       string `json:"order_code"`
			CheckoutGroupID string `json:"checkout_group_id"`
			BuyerID         string `json:"buyer_id"`
			ShopID          string `json:"shop_id"`
			Reason          string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.OrderID == "" || p.BuyerID == "" {
			return nil, fmt.Errorf("%w: invalid order.returned payload", ErrMalformedPayload)
		}
		code := orderLabel(p.OrderCode, p.OrderID)
		body := fmt.Sprintf("Đơn hàng %s đã được chuyển sang trạng thái hoàn hàng.", code)
		if reason := localizeReason(p.Reason); reason != "" {
			body += fmt.Sprintf(" Lý do: %s", reason)
		}
		return &eventPlan{
			cacheOrder: &store.OrderParticipant{
				OrderID:         p.OrderID,
				BuyerID:         p.BuyerID,
				ShopID:          p.ShopID,
				OrderCode:       p.OrderCode,
				CheckoutGroupID: p.CheckoutGroupID,
			},
			deliveries: []recipientDelivery{{
				role:     "buyer",
				userID:   p.BuyerID,
				category: store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     fmt.Sprintf("Đơn hàng %s đã hoàn hàng", code),
					Body:      body,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đơn hàng %s đã hoàn hàng", code),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "order.return_updated":
		return buildReturnUpdatedPlan(env.Payload, now)

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
		amount := FormatVND(p.AmountVND)
		txSuffix := ""
		if txID := strings.TrimSpace(p.ProviderTxID); txID != "" {
			txSuffix = fmt.Sprintf(" (mã giao dịch: %s)", txID)
		}
		var title, body, emailSubject string
		if isCOD(p.Method) {
			title = "Đã thu tiền thanh toán khi nhận hàng"
			body = fmt.Sprintf("Đơn hàng của bạn đã được giao và thu tiền thành công số tiền %s qua hình thức thanh toán khi nhận hàng%s.", amount, txSuffix)
			emailSubject = fmt.Sprintf("NexusMart - Đã thu tiền đơn hàng (%s)", amount)
		} else {
			title = "Thanh toán thành công"
			body = fmt.Sprintf("Thanh toán %s qua %s thành công%s.", amount, paymentMethodLabel(p.Method), txSuffix)
			emailSubject = fmt.Sprintf("NexusMart - Thanh toán thành công (%s)", amount)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:            "buyer",
				userID:          p.UserID,
				category:        store.CategoryOrder,
				resolvePayLink:  true,
				payOrderID:      p.OrderID,
				payOrderGroupID: p.OrderGroupID,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     title,
					Body:      body,
					Link:      "/don-hang",
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: emailSubject,
					Body:    body + "\n",
				},
			}},
		}, nil

	case "payment.failed":
		var p struct {
			PaymentID    string `json:"payment_id"`
			OrderID      string `json:"order_id"`
			OrderGroupID string `json:"order_group_id"`
			UserID       string `json:"user_id"`
			Reason       string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.failed payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Thanh toán cho giao dịch %s không thành công.", p.PaymentID)
		if reason := localizeReason(p.Reason); reason != "" {
			body += fmt.Sprintf(" Lý do: %s", reason)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:            "buyer",
				userID:          p.UserID,
				category:        store.CategoryOrder,
				resolvePayLink:  true,
				payOrderID:      p.OrderID,
				payOrderGroupID: p.OrderGroupID,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     "Thanh toán thất bại",
					Body:      body,
					Link:      "/don-hang",
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: "NexusMart - Thanh toán thất bại",
					Body:    body + "\n",
				},
			}},
		}, nil

	case "payment.expired":
		var p struct {
			PaymentID    string `json:"payment_id"`
			OrderID      string `json:"order_id"`
			OrderGroupID string `json:"order_group_id"`
			UserID       string `json:"user_id"`
			Reason       string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.expired payload", ErrMalformedPayload)
		}
		body := fmt.Sprintf("Phiên thanh toán %s đã hết hạn và đơn hàng đã được hủy tự động.", p.PaymentID)
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:            "buyer",
				userID:          p.UserID,
				category:        store.CategoryOrder,
				resolvePayLink:  true,
				payOrderID:      p.OrderID,
				payOrderGroupID: p.OrderGroupID,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     "Thanh toán hết hạn",
					Body:      body,
					Link:      "/don-hang",
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: "NexusMart - Thanh toán đã hết hạn",
					Body:    body + "\n",
				},
			}},
		}, nil

	case "payment.refunded":
		var p struct {
			RefundID     string `json:"refund_id"`
			PaymentID    string `json:"payment_id"`
			OrderID      string `json:"order_id"`
			OrderGroupID string `json:"order_group_id"`
			UserID       string `json:"user_id"`
			AmountVND    int64  `json:"amount_vnd"`
			Reason       string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.refunded payload", ErrMalformedPayload)
		}
		amount := FormatVND(p.AmountVND)
		body := fmt.Sprintf("Khoản hoàn tiền %s (mã hoàn tiền: %s) đã được xử lý.", amount, p.RefundID)
		if reason := localizeReason(p.Reason); reason != "" {
			body += fmt.Sprintf(" Lý do: %s", reason)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:            "buyer",
				userID:          p.UserID,
				category:        store.CategoryOrder,
				resolvePayLink:  true,
				payOrderID:      p.OrderID,
				payOrderGroupID: p.OrderGroupID,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     "Hoàn tiền thành công",
					Body:      body,
					Link:      "/don-hang",
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Hoàn tiền thành công (%s)", amount),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "payment.refund_settled":
		var p struct {
			RefundID         string `json:"refund_id"`
			PaymentID        string `json:"payment_id"`
			OrderID          string `json:"order_id"`
			OrderGroupID     string `json:"order_group_id"`
			UserID           string `json:"user_id"`
			AmountVND        int64  `json:"amount_vnd"`
			ProviderRefundID string `json:"provider_refund_id"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.UserID == "" {
			return nil, fmt.Errorf("%w: invalid payment.refund_settled payload", ErrMalformedPayload)
		}
		amount := FormatVND(p.AmountVND)
		var body string
		if refID := strings.TrimSpace(p.RefundID); refID != "" {
			body = fmt.Sprintf("Khoản hoàn tiền %s (mã hoàn tiền: %s) đã được chuyển khoản thành công vào tài khoản nhận của bạn.", amount, refID)
		} else {
			body = fmt.Sprintf("Khoản hoàn tiền %s đã được chuyển khoản thành công vào tài khoản nhận của bạn.", amount)
		}
		if provRef := strings.TrimSpace(p.ProviderRefundID); provRef != "" {
			body += fmt.Sprintf(" Mã giao dịch chuyển khoản: %s.", provRef)
		}
		return &eventPlan{
			deliveries: []recipientDelivery{{
				role:            "buyer",
				userID:          p.UserID,
				category:        store.CategoryOrder,
				resolvePayLink:  true,
				payOrderID:      p.OrderID,
				payOrderGroupID: p.OrderGroupID,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     "Hoàn tiền đã được chuyển khoản",
					Body:      body,
					Link:      "/don-hang",
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: fmt.Sprintf("NexusMart - Đã chuyển khoản hoàn tiền (%s)", amount),
					Body:    body + "\n",
				},
			}},
		}, nil

	case "shipment.updated":
		return buildShipmentUpdatedPlan(env.Payload, now)

	case "shipment.create_failed":
		return buildShipmentCreateFailedPlan(env.Payload, env.OccurredAt, now)

	default:
		// Unknown future subject: acknowledge safely without poisoning DLQ.
		return &eventPlan{}, nil
	}
}

type shopStatusChangedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id"`
	ShopName    string `json:"shop_name"`
	FromStatus  string `json:"from_status"`
	ToStatus    string `json:"to_status"`
	Reason      string `json:"reason"`
	ActorID     string `json:"actor_id"`
	ApproverID  string `json:"approver_id"`
	Version     int64  `json:"version"`
	OccurredAt  string `json:"occurred_at"`
}

func buildShopStatusChangedPlan(raw json.RawMessage, envOccurredAt string, now time.Time) (*eventPlan, error) {
	var p shopStatusChangedPayload
	if err := json.Unmarshal(raw, &p); err != nil ||
		strings.TrimSpace(p.ShopID) == "" ||
		strings.TrimSpace(p.OwnerUserID) == "" {
		return nil, fmt.Errorf("%w: invalid user.shop_status_changed payload", ErrMalformedPayload)
	}
	if p.Version <= 0 {
		return nil, fmt.Errorf("%w: invalid user.shop_status_changed version %d", ErrMalformedPayload, p.Version)
	}
	shopID := strings.TrimSpace(p.ShopID)
	ownerUserID := strings.TrimSpace(p.OwnerUserID)
	rawShopName := strings.TrimSpace(p.ShopName)
	shopName := rawShopName
	if shopName == "" {
		shopName = shopID
	}
	if envOccurredAt == "" && strings.TrimSpace(p.OccurredAt) != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(p.OccurredAt)); parseErr == nil {
			now = parsed.UTC()
		}
	}

	fromStatus := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(p.FromStatus)), "SHOP_STATUS_")
	toStatus := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(p.ToStatus)), "SHOP_STATUS_")
	reason := strings.TrimSpace(p.Reason)

	cacheShop := &store.ShopOwner{
		ShopID:      shopID,
		OwnerUserID: ownerUserID,
		ShopName:    rawShopName,
	}

	var title, body, emailSubject string
	switch toStatus {
	case "SUSPENDED":
		title = fmt.Sprintf("Gian hàng %s đã bị tạm khóa", shopName)
		body = fmt.Sprintf("Gian hàng %s đã bị tạm khóa.", shopName)
		if reason != "" {
			body += fmt.Sprintf(" Lý do: %s.", strings.TrimSuffix(reason, "."))
		}
		body += " Các đơn hàng đang xử lý vẫn tiếp tục được thực hiện bình thường, tuy nhiên gian hàng tạm thời không thể nhận đơn hàng mới hoặc tạo/chỉnh sửa sản phẩm."
		emailSubject = fmt.Sprintf("NexusMart - Gian hàng %s đã bị tạm khóa", shopName)
	case "ACTIVE":
		if fromStatus != "" && fromStatus != "SUSPENDED" {
			return &eventPlan{cacheShop: cacheShop}, nil
		}
		title = fmt.Sprintf("Gian hàng %s đã được mở khóa", shopName)
		body = fmt.Sprintf("Gian hàng %s đã được mở khóa và hoạt động trở lại trên NexusMart.", shopName)
		if reason != "" {
			body += fmt.Sprintf(" Ghi chú: %s.", strings.TrimSuffix(reason, "."))
		}
		body += " Sản phẩm đủ điều kiện sẽ hiển thị trở lại và gian hàng có thể tiếp tục nhận đơn hàng mới."
		emailSubject = fmt.Sprintf("NexusMart - Gian hàng %s đã được mở khóa", shopName)
	default:
		return nil, fmt.Errorf("%w: invalid user.shop_status_changed to_status %q", ErrMalformedPayload, p.ToStatus)
	}

	return &eventPlan{
		cacheShop: cacheShop,
		deliveries: []recipientDelivery{{
			role:     "user",
			userID:   ownerUserID,
			category: store.CategorySystem,
			inApp: &store.Notification{
				Category:  store.CategorySystem,
				Title:     title,
				Body:      body,
				Link:      "/seller",
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: emailSubject,
				Body:    body + "\n",
			},
		}},
	}, nil
}

type returnUpdatedPayload struct {
	ReturnRequestID      string `json:"return_request_id"`
	OrderID              string `json:"order_id"`
	OrderCode            string `json:"order_code"`
	CheckoutGroupID      string `json:"checkout_group_id"`
	BuyerID              string `json:"buyer_id"`
	ShopID               string `json:"shop_id"`
	Status               string `json:"status"`
	PreviousStatus       string `json:"previous_status"`
	Reason               string `json:"reason"`
	ReturnTrackingCode   string `json:"return_tracking_code"`
	DeadlineAt           string `json:"deadline_at"`
	ActorRole            string `json:"actor_role"`
	RefundApprovalStatus string `json:"refund_approval_status"`
}

func buildReturnUpdatedPlan(raw json.RawMessage, now time.Time) (*eventPlan, error) {
	var p returnUpdatedPayload
	if err := json.Unmarshal(raw, &p); err != nil || strings.TrimSpace(p.OrderID) == "" {
		return nil, fmt.Errorf("%w: invalid order.return_updated payload", ErrMalformedPayload)
	}
	p.OrderID = strings.TrimSpace(p.OrderID)
	p.BuyerID = strings.TrimSpace(p.BuyerID)
	p.ShopID = strings.TrimSpace(p.ShopID)
	p.OrderCode = strings.TrimSpace(p.OrderCode)

	plan := &eventPlan{}
	if p.BuyerID != "" {
		plan.cacheOrder = &store.OrderParticipant{
			OrderID:         p.OrderID,
			BuyerID:         p.BuyerID,
			ShopID:          p.ShopID,
			OrderCode:       p.OrderCode,
			CheckoutGroupID: strings.TrimSpace(p.CheckoutGroupID),
		}
	}

	buildDeliveries := func(orderCode string) []recipientDelivery {
		code := orderLabel(orderCode, p.OrderID)
		buyerTitle, buyerBody, buyerSubj, sellerTitle, sellerBody, sellerSubj := formatReturnMessages(p, code)
		return []recipientDelivery{
			{
				role:        "buyer",
				userID:      p.BuyerID,
				requireUser: true,
				category:    store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     buyerTitle,
					Body:      buyerBody,
					Link:      orderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: buyerSubj,
					Body:    buyerBody + "\n",
				},
			},
			{
				role:        "seller",
				shopID:      p.ShopID,
				requireUser: true,
				category:    store.CategoryOrder,
				inApp: &store.Notification{
					Category:  store.CategoryOrder,
					Title:     sellerTitle,
					Body:      sellerBody,
					Link:      sellerOrderLink(p.OrderID),
					CreatedAt: now,
				},
				email: &mailer.Message{
					Subject: sellerSubj,
					Body:    sellerBody + "\n",
				},
			},
		}
	}

	plan.deliveries = buildDeliveries(p.OrderCode)
	if p.BuyerID == "" || p.ShopID == "" || p.OrderCode == "" {
		plan.lookupOrderID = p.OrderID
		plan.applyParticipant = func(part store.OrderParticipant, found bool) {
			if !found {
				return
			}
			if p.BuyerID == "" {
				p.BuyerID = part.BuyerID
			}
			if p.ShopID == "" {
				p.ShopID = part.ShopID
			}
			if p.OrderCode == "" {
				p.OrderCode = part.OrderCode
			}
			plan.deliveries = buildDeliveries(p.OrderCode)
		}
	}
	return plan, nil
}

func formatReturnMessages(p returnUpdatedPayload, code string) (buyerTitle, buyerBody, buyerSubj, sellerTitle, sellerBody, sellerSubj string) {
	st := normalizeReturnStatus(p.Status)
	approvalStatus := normalizeRefundApprovalStatus(p.RefundApprovalStatus)
	isRefundRejected := strings.EqualFold(strings.TrimSpace(p.Reason), "refund_rejected") || approvalStatus == "REJECTED"
	tracking := strings.TrimSpace(p.ReturnTrackingCode)
	reason := localizeReason(p.Reason)
	deadline := formatVietnamTime(p.DeadlineAt)

	switch st {
	case "PENDING":
		buyerTitle = fmt.Sprintf("Đã gửi yêu cầu trả hàng đơn %s", code)
		buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được gửi đến người bán và đang chờ phản hồi.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Đã gửi yêu cầu trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Yêu cầu trả hàng mới cho đơn %s", code)
		sellerBody = fmt.Sprintf("Người mua đã gửi yêu cầu trả hàng cho đơn %s. Vui lòng xem xét và phản hồi đúng hạn.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Yêu cầu trả hàng mới đơn %s", code)

	case "AWAITING_SHIPMENT":
		buyerTitle = fmt.Sprintf("Yêu cầu trả hàng đơn %s đã được chấp nhận", code)
		buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp nhận. Vui lòng gửi hàng trả về cho người bán đúng hạn.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Chấp nhận trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Đơn %s đang chờ người mua gửi hàng trả", code)
		sellerBody = fmt.Sprintf("Yêu cầu trả hàng đơn %s đã chuyển sang trạng thái chờ người mua gửi hàng trả về kho.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Chờ hàng trả về đơn %s", code)

	case "IN_TRANSIT":
		buyerTitle = fmt.Sprintf("Hàng trả đơn %s đang trên đường về người bán", code)
		buyerBody = fmt.Sprintf("Kiện hàng trả của đơn %s đang được vận chuyển về kho người bán.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Hàng trả đơn %s đang vận chuyển", code)

		sellerTitle = fmt.Sprintf("Hàng trả đơn %s đang trên đường về kho", code)
		sellerBody = fmt.Sprintf("Kiện hàng trả của đơn %s đang trên đường về kho của bạn.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Hàng trả đơn %s đang về kho", code)

	case "RECEIVED":
		buyerTitle = fmt.Sprintf("Người bán đã nhận hàng trả đơn %s", code)
		buyerBody = fmt.Sprintf("Kiện hàng trả của đơn %s đã về tới kho người bán và đang chờ kiểm tra hoàn tiền.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Đã nhận hàng trả đơn %s", code)

		sellerTitle = fmt.Sprintf("Hàng trả đơn %s đã về kho", code)
		sellerBody = fmt.Sprintf("Kiện hàng trả của đơn %s đã về kho của bạn. Vui lòng kiểm tra và xác nhận đúng hạn.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Hàng trả đơn %s đã về kho", code)

	case "REJECTED":
		buyerTitle = fmt.Sprintf("Yêu cầu trả hàng đơn %s bị từ chối", code)
		buyerBody = fmt.Sprintf("Người bán đã từ chối yêu cầu trả hàng cho đơn %s. Bạn có thể mở khiếu nại nếu chưa đồng ý.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Từ chối trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Đã từ chối yêu cầu trả hàng đơn %s", code)
		sellerBody = fmt.Sprintf("Bạn đã từ chối yêu cầu trả hàng cho đơn %s.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Cập nhật trả hàng đơn %s", code)

	case "ESCALATED":
		if isRefundRejected {
			buyerTitle = fmt.Sprintf("Khoản hoàn tiền đơn %s cần được xem xét lại", code)
			buyerBody = fmt.Sprintf("Khoản hoàn tiền cho yêu cầu trả hàng đơn %s hiện chưa được hoàn tiền và cần được xem xét lại. Đội ngũ NexusMart sẽ kiểm tra lại và liên hệ với bạn trong thời gian sớm nhất.", code)
			buyerSubj = fmt.Sprintf("NexusMart - Khoản hoàn tiền đơn %s cần được xem xét lại", code)

			sellerTitle = fmt.Sprintf("Khoản hoàn tiền trả hàng đơn %s cần xem xét lại", code)
			sellerBody = fmt.Sprintf("Khoản hoàn tiền cho yêu cầu trả hàng đơn %s hiện chưa được hoàn tiền và cần được xem xét lại. Đội ngũ NexusMart sẽ kiểm tra lại và thông báo tới bạn khi có kết quả.", code)
			sellerSubj = fmt.Sprintf("NexusMart - Khoản hoàn tiền đơn %s cần xem xét lại", code)
		} else {
			buyerTitle = fmt.Sprintf("Yêu cầu trả hàng đơn %s đang được khiếu nại", code)
			buyerBody = fmt.Sprintf("Khiếu nại trả hàng cho đơn %s đã được chuyển tới quản trị viên NexusMart xem xét phân xử.", code)
			buyerSubj = fmt.Sprintf("NexusMart - Khiếu nại trả hàng đơn %s", code)

			sellerTitle = fmt.Sprintf("Đơn %s có khiếu nại trả hàng", code)
			sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã chuyển sang trạng thái khiếu nại để quản trị viên NexusMart phân xử.", code)
			sellerSubj = fmt.Sprintf("NexusMart - Khiếu nại trả hàng đơn %s", code)
		}

	case "EVIDENCE_REQUESTED":
		buyerTitle = fmt.Sprintf("Yêu cầu bổ sung bằng chứng trả hàng đơn %s", code)
		buyerBody = fmt.Sprintf("Quản trị viên yêu cầu bạn bổ sung bằng chứng cho khiếu nại trả hàng đơn %s.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Yêu cầu bổ sung bằng chứng đơn %s", code)

		sellerTitle = fmt.Sprintf("Khiếu nại đơn %s đang chờ bổ sung bằng chứng", code)
		sellerBody = fmt.Sprintf("Quản trị viên đã yêu cầu người mua bổ sung bằng chứng cho khiếu nại trả hàng đơn %s.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Cập nhật khiếu nại đơn %s", code)

	case "REFUNDING":
		switch approvalStatus {
		case "PENDING_APPROVAL":
			buyerTitle = fmt.Sprintf("Đơn %s đang chờ phê duyệt hoàn tiền", code)
			buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp thuận và khoản hoàn đang chờ phê duyệt hoàn tiền.", code)
			buyerSubj = fmt.Sprintf("NexusMart - Chờ phê duyệt hoàn tiền đơn %s", code)

			sellerTitle = fmt.Sprintf("Đơn %s đang chờ phê duyệt hoàn tiền trả hàng", code)
			sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp thuận và khoản hoàn đang chờ phê duyệt hoàn tiền cho người mua.", code)
			sellerSubj = fmt.Sprintf("NexusMart - Chờ phê duyệt hoàn tiền trả hàng đơn %s", code)
		case "APPROVED":
			buyerTitle = fmt.Sprintf("Đơn %s đang được hoàn tiền", code)
			buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp thuận. Hệ thống đang tiến hành hoàn tiền cho bạn.", code)
			buyerSubj = fmt.Sprintf("NexusMart - Đang hoàn tiền đơn %s", code)

			sellerTitle = fmt.Sprintf("Đơn %s đang xử lý hoàn tiền trả hàng", code)
			sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đang được xử lý hoàn tiền cho người mua.", code)
			sellerSubj = fmt.Sprintf("NexusMart - Đang hoàn tiền trả hàng đơn %s", code)
		default:
			buyerTitle = fmt.Sprintf("Đơn %s đang xử lý hoàn tiền", code)
			buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp thuận, khoản hoàn đang được xử lý.", code)
			buyerSubj = fmt.Sprintf("NexusMart - Đang xử lý hoàn tiền đơn %s", code)

			sellerTitle = fmt.Sprintf("Đơn %s đang xử lý hoàn tiền trả hàng", code)
			sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được chấp thuận, khoản hoàn đang được xử lý cho người mua.", code)
			sellerSubj = fmt.Sprintf("NexusMart - Đang xử lý hoàn tiền trả hàng đơn %s", code)
		}

	case "REFUNDED":
		buyerTitle = fmt.Sprintf("Đơn %s đã hoàn tất trả hàng và hoàn tiền", code)
		buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã được hoàn tiền. Vui lòng kiểm tra thông tin tài khoản nhận hoàn tiền.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Hoàn tất trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Đơn %s đã hoàn tất trả hàng và hoàn tiền", code)
		sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã hoàn tất quá trình hoàn tiền.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Hoàn tất trả hàng đơn %s", code)

	case "CLOSED":
		buyerTitle = fmt.Sprintf("Yêu cầu trả hàng đơn %s đã đóng", code)
		buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã đóng và đơn hàng chuyển sang trạng thái hoàn tất.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Đóng yêu cầu trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Yêu cầu trả hàng đơn %s đã đóng", code)
		sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s đã đóng và đơn hàng đã hoàn tất.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Đóng yêu cầu trả hàng đơn %s", code)

	default:
		buyerTitle = fmt.Sprintf("Cập nhật yêu cầu trả hàng đơn %s", code)
		buyerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s vừa có cập nhật trạng thái mới.", code)
		buyerSubj = fmt.Sprintf("NexusMart - Cập nhật trả hàng đơn %s", code)

		sellerTitle = fmt.Sprintf("Cập nhật yêu cầu trả hàng đơn %s", code)
		sellerBody = fmt.Sprintf("Yêu cầu trả hàng cho đơn %s vừa có cập nhật trạng thái mới.", code)
		sellerSubj = fmt.Sprintf("NexusMart - Cập nhật trả hàng đơn %s", code)
	}

	var details string
	if tracking != "" {
		details += fmt.Sprintf(" Mã vận đơn trả hàng: %s.", tracking)
	}
	if reason != "" && !(st == "ESCALATED" && isRefundRejected) {
		details += fmt.Sprintf(" Lý do: %s.", strings.TrimSuffix(reason, "."))
	}
	if deadline != "" && (st == "PENDING" || st == "AWAITING_SHIPMENT" || st == "RECEIVED" || st == "REJECTED" || st == "EVIDENCE_REQUESTED") {
		details += fmt.Sprintf(" Hạn xử lý: %s.", deadline)
	}
	buyerBody += details
	sellerBody += details
	return buyerTitle, buyerBody, buyerSubj, sellerTitle, sellerBody, sellerSubj
}

type shipmentUpdatedPayload struct {
	ShipmentID      string `json:"shipment_id"`
	OrderID         string `json:"order_id"`
	ShopID          string `json:"shop_id"`
	BuyerID         string `json:"buyer_id"`
	CarrierCode     string `json:"carrier_code"`
	TrackingCode    string `json:"tracking_code"`
	Status          string `json:"status"`
	Description     string `json:"description"`
	Direction       string `json:"direction"`
	ReturnRequestID string `json:"return_request_id"`
	RedeliveryCount int32  `json:"redelivery_count"`
}

func buildShipmentUpdatedPlan(raw json.RawMessage, now time.Time) (*eventPlan, error) {
	var p shipmentUpdatedPayload
	if err := json.Unmarshal(raw, &p); err != nil || strings.TrimSpace(p.OrderID) == "" {
		return nil, fmt.Errorf("%w: invalid shipment.updated payload", ErrMalformedPayload)
	}
	p.OrderID = strings.TrimSpace(p.OrderID)
	p.BuyerID = strings.TrimSpace(p.BuyerID)
	p.ShopID = strings.TrimSpace(p.ShopID)
	p.TrackingCode = strings.TrimSpace(p.TrackingCode)

	dir := strings.ToUpper(strings.TrimSpace(p.Direction))
	if dir == "" {
		dir = "FORWARD"
	}
	st := strings.ToLower(strings.TrimSpace(p.Status))
	needsSeller := st == "failed" || st == "returning" || st == "returned"

	buildDeliveries := func(orderCode string) []recipientDelivery {
		orderRef := orderLabel(orderCode, p.OrderID)
		trackingRef := p.TrackingCode
		if trackingRef == "" {
			trackingRef = orderRef
		}
		carrierSuffix := ""
		if strings.TrimSpace(p.CarrierCode) != "" {
			carrierSuffix = fmt.Sprintf(" (%s)", carrierLabel(p.CarrierCode))
		}
		descSuffix := ""
		if desc := strings.TrimSpace(p.Description); desc != "" {
			descSuffix = " " + desc
		}

		if dir == "RETURN" {
			return buildReturnShipmentDeliveries(p, st, orderRef, trackingRef, carrierSuffix, descSuffix, needsSeller, now)
		}
		return buildForwardShipmentDeliveries(p, st, orderRef, trackingRef, carrierSuffix, descSuffix, needsSeller, now)
	}

	plan := &eventPlan{
		lookupOrderID: p.OrderID,
		deliveries:    buildDeliveries(""),
	}
	plan.applyParticipant = func(part store.OrderParticipant, found bool) {
		if !found {
			return
		}
		if p.BuyerID == "" {
			p.BuyerID = part.BuyerID
		}
		if p.ShopID == "" {
			p.ShopID = part.ShopID
		}
		plan.deliveries = buildDeliveries(part.OrderCode)
	}
	return plan, nil
}

func buildForwardShipmentDeliveries(
	p shipmentUpdatedPayload,
	st, orderRef, trackingRef, carrierSuffix, descSuffix string,
	needsSeller bool,
	now time.Time,
) []recipientDelivery {
	statusLabel := shipmentStatusLabel(st)
	var buyerBody string
	if st == "failed" {
		buyerBody = fmt.Sprintf("Vận đơn %s%s giao hàng không thành công. Bạn có thể yêu cầu giao lại trong chi tiết đơn hàng.%s", trackingRef, carrierSuffix, descSuffix)
	} else {
		buyerBody = fmt.Sprintf("Vận đơn %s%s cập nhật trạng thái: %s.%s", trackingRef, carrierSuffix, statusLabel, descSuffix)
	}

	var buyerEmail *mailer.Message
	var dedupKey string
	switch st {
	case "delivering":
		buyerEmail = &mailer.Message{
			Subject: fmt.Sprintf("NexusMart - Đơn hàng đang được vận chuyển (%s)", trackingRef),
			Body:    buyerBody + "\n",
		}
	case "delivered":
		dedupKey = deliveredEmailDedupKey(p.OrderID)
		buyerEmail = &mailer.Message{
			Subject: fmt.Sprintf("NexusMart - Giao hàng thành công (%s)", trackingRef),
			Body:    buyerBody + "\n",
		}
	case "failed":
		buyerEmail = &mailer.Message{
			Subject: fmt.Sprintf("NexusMart - Giao hàng thất bại (%s)", trackingRef),
			Body:    buyerBody + "\n",
		}
	}

	deliveries := []recipientDelivery{{
		role:          "buyer",
		userID:        p.BuyerID,
		requireUser:   true,
		category:      store.CategoryOrder,
		emailDedupKey: dedupKey,
		inApp: &store.Notification{
			Category:  store.CategoryOrder,
			Title:     fmt.Sprintf("Cập nhật vận chuyển: %s", statusLabel),
			Body:      buyerBody,
			Link:      orderLink(p.OrderID),
			CreatedAt: now,
		},
		email: buyerEmail,
	}}

	if needsSeller {
		var sellerTitle, sellerBody, sellerSubj string
		switch st {
		case "failed":
			sellerTitle = fmt.Sprintf("Giao hàng thất bại cho đơn %s", orderRef)
			sellerBody = fmt.Sprintf("Đơn hàng %s (vận đơn %s%s) giao hàng không thành công.%s", orderRef, trackingRef, carrierSuffix, descSuffix)
			sellerSubj = fmt.Sprintf("NexusMart - Giao hàng thất bại đơn %s", orderRef)
		case "returning":
			sellerTitle = fmt.Sprintf("Đơn hàng %s đang hoàn về kho", orderRef)
			sellerBody = fmt.Sprintf("Đơn hàng %s (vận đơn %s%s) đang trên đường hoàn về kho của bạn.%s", orderRef, trackingRef, carrierSuffix, descSuffix)
			sellerSubj = fmt.Sprintf("NexusMart - Hàng đang hoàn về kho (%s)", orderRef)
		case "returned":
			sellerTitle = fmt.Sprintf("Hàng hoàn đơn %s đã về kho", orderRef)
			sellerBody = fmt.Sprintf("Đơn hàng %s (vận đơn %s%s) đã được hoàn về kho của bạn.%s", orderRef, trackingRef, carrierSuffix, descSuffix)
			sellerSubj = fmt.Sprintf("NexusMart - Hàng hoàn đã về kho (%s)", orderRef)
		}
		deliveries = append(deliveries, recipientDelivery{
			role:        "seller",
			shopID:      p.ShopID,
			requireUser: true,
			category:    store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     sellerTitle,
				Body:      sellerBody,
				Link:      sellerOrderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: sellerSubj,
				Body:    sellerBody + "\n",
			},
		})
	}
	return deliveries
}

func buildReturnShipmentDeliveries(
	p shipmentUpdatedPayload,
	st, orderRef, trackingRef, carrierSuffix, descSuffix string,
	needsSeller bool,
	now time.Time,
) []recipientDelivery {
	statusLabel := returnShipmentStatusLabel(st)
	buyerBody := fmt.Sprintf("Vận đơn trả hàng %s%s của đơn %s cập nhật trạng thái: %s.%s", trackingRef, carrierSuffix, orderRef, statusLabel, descSuffix)

	var buyerEmail *mailer.Message
	if st == "failed" {
		buyerEmail = &mailer.Message{
			Subject: fmt.Sprintf("NexusMart - Vận đơn trả hàng gặp sự cố (%s)", trackingRef),
			Body:    buyerBody + "\n",
		}
	}

	deliveries := []recipientDelivery{{
		role:        "buyer",
		userID:      p.BuyerID,
		requireUser: true,
		category:    store.CategoryOrder,
		inApp: &store.Notification{
			Category:  store.CategoryOrder,
			Title:     fmt.Sprintf("Cập nhật vận đơn trả hàng: %s", statusLabel),
			Body:      buyerBody,
			Link:      orderLink(p.OrderID),
			CreatedAt: now,
		},
		email: buyerEmail,
	}}

	if needsSeller {
		sellerTitle := fmt.Sprintf("Cập nhật vận đơn trả hàng đơn %s: %s", orderRef, statusLabel)
		sellerBody := fmt.Sprintf("Vận đơn trả hàng %s%s của đơn %s cập nhật trạng thái: %s.%s", trackingRef, carrierSuffix, orderRef, statusLabel, descSuffix)
		deliveries = append(deliveries, recipientDelivery{
			role:        "seller",
			shopID:      p.ShopID,
			requireUser: true,
			category:    store.CategoryOrder,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     sellerTitle,
				Body:      sellerBody,
				Link:      sellerOrderLink(p.OrderID),
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Vận đơn trả hàng đơn %s: %s", orderRef, statusLabel),
				Body:    sellerBody + "\n",
			},
		})
	}
	return deliveries
}

type shipmentCreateFailedPayload struct {
	JobID        string `json:"job_id"`
	OrderID      string `json:"order_id"`
	ShopID       string `json:"shop_id"`
	CarrierCode  string `json:"carrier_code"`
	ServiceCode  string `json:"service_code"`
	Attempts     int32  `json:"attempts"`
	ErrorMessage string `json:"error_message"`
	OccurredAt   string `json:"occurred_at"`
}

func buildShipmentCreateFailedPlan(raw json.RawMessage, envOccurredAt string, now time.Time) (*eventPlan, error) {
	var p shipmentCreateFailedPayload
	if err := json.Unmarshal(raw, &p); err != nil || strings.TrimSpace(p.OrderID) == "" {
		return nil, fmt.Errorf("%w: invalid shipment.create_failed payload", ErrMalformedPayload)
	}
	p.JobID = strings.TrimSpace(p.JobID)
	p.OrderID = strings.TrimSpace(p.OrderID)
	p.ShopID = strings.TrimSpace(p.ShopID)
	p.CarrierCode = strings.TrimSpace(p.CarrierCode)
	if envOccurredAt == "" && strings.TrimSpace(p.OccurredAt) != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(p.OccurredAt)); parseErr == nil {
			now = parsed.UTC()
		}
	}

	dedupKey := shipmentCreateFailedDedupKey(p.JobID)
	buildDeliveries := func(orderCode string) []recipientDelivery {
		orderRef := orderLabel(orderCode, p.OrderID)
		carrier := carrierLabel(p.CarrierCode)
		title := fmt.Sprintf("Tạo vận đơn thất bại cho đơn %s", orderRef)
		var body string
		if p.Attempts > 0 {
			body = fmt.Sprintf("Hệ thống không thể tạo vận đơn qua %s cho đơn hàng %s sau %d lần thử.", carrier, orderRef, p.Attempts)
		} else {
			body = fmt.Sprintf("Hệ thống không thể tạo vận đơn qua %s cho đơn hàng %s.", carrier, orderRef)
		}
		body += " Vui lòng kiểm tra và bấm tạo lại vận đơn trong chi tiết đơn hàng."

		return []recipientDelivery{{
			role:          "seller",
			shopID:        p.ShopID,
			requireUser:   true,
			category:      store.CategoryOrder,
			emailDedupKey: dedupKey,
			inApp: &store.Notification{
				Category:  store.CategoryOrder,
				Title:     title,
				Body:      body,
				Link:      sellerOrderLink(p.OrderID),
				DedupKey:  dedupKey,
				CreatedAt: now,
			},
			email: &mailer.Message{
				Subject: fmt.Sprintf("NexusMart - Tạo vận đơn thất bại đơn %s", orderRef),
				Body:    body + "\n",
			},
		}}
	}

	plan := &eventPlan{
		lookupOrderID: p.OrderID,
		deliveries:    buildDeliveries(""),
	}
	plan.applyParticipant = func(part store.OrderParticipant, found bool) {
		if !found {
			return
		}
		if p.ShopID == "" {
			p.ShopID = part.ShopID
		}
		plan.deliveries = buildDeliveries(part.OrderCode)
	}
	return plan, nil
}

func deliveredEmailDedupKey(orderID string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return ""
	}
	return "order_delivered:" + orderID
}

func voucherExpiringDedupKey(userID, voucherID string) string {
	userID = strings.TrimSpace(userID)
	voucherID = strings.TrimSpace(voucherID)
	if userID == "" || voucherID == "" {
		return ""
	}
	return "voucher_expiring:" + userID + ":" + voucherID
}

func shipmentCreateFailedDedupKey(jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	return "shipment_create_failed:" + jobID
}

func sentEmailDedupKey(eventID, userID string) string {
	return "event_sent:" + strings.TrimSpace(eventID) + ":" + strings.TrimSpace(userID)
}

func resolvePaymentOrderLink(ctx context.Context, tx store.TxStore, orderID, orderGroupID string) string {
	orderID = strings.TrimSpace(orderID)
	orderGroupID = strings.TrimSpace(orderGroupID)

	// 1. Distinct order_id and order_group_id means the event already carries a real sub-order ID.
	if orderID != "" && orderGroupID != "" && orderID != orderGroupID {
		return orderLink(orderID)
	}

	// 2. If orderID matches a known sub-order directly, link straight to it.
	if orderID != "" {
		if _, found, err := tx.GetOrderParticipant(ctx, orderID); err == nil && found {
			return orderLink(orderID)
		}
	}

	// 3. Look up sub-orders belonging to checkout_group_id (orderGroupID, or orderID when group ID is absent).
	groupLookup := orderGroupID
	if groupLookup == "" {
		groupLookup = orderID
	}
	if groupLookup != "" {
		if parts, err := tx.ListOrderParticipantsByGroup(ctx, groupLookup); err == nil && len(parts) > 0 {
			if len(parts) == 1 {
				return orderLink(parts[0].OrderID)
			}
			// Multi-shop checkout group: link to the orders list rather than a 404 checkout_group_id URL.
			return "/don-hang"
		}
	}

	// 4. Never emit /don-hang/<checkout_group_id> when orderID is missing or known to be checkout_group_id.
	if orderID == "" || (orderGroupID != "" && orderID == orderGroupID) {
		return "/don-hang"
	}

	// 5. Fallback for sub-order refund arriving before order.created is cached (orderGroupID == "").
	return orderLink(orderID)
}

// FormatInt formats an integer with Vietnamese dot thousands separators (e.g., 256500 -> "256.500").
func FormatInt(n int64) string {
	neg := n < 0
	var abs uint64
	if neg {
		abs = uint64(-(n + 1)) + 1
	} else {
		abs = uint64(n)
	}
	digits := strconv.FormatUint(abs, 10)
	if len(digits) <= 3 {
		if neg {
			return "-" + digits
		}
		return digits
	}
	var b strings.Builder
	b.Grow(len(digits) + (len(digits)-1)/3 + 1)
	if neg {
		b.WriteByte('-')
	}
	rem := len(digits) % 3
	if rem == 0 {
		rem = 3
	}
	b.WriteString(digits[:rem])
	for i := rem; i < len(digits); i += 3 {
		b.WriteByte('.')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// FormatVND formats an integer VND amount as Vietnamese currency (e.g., 256500 -> "256.500₫").
func FormatVND(amount int64) string {
	return FormatInt(amount) + "₫"
}

func formatVietnamTime(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.In(vietnamZone).Format("15:04 ngày 02/01/2006 (GMT+7)")
	}
	return trimmed
}

func isCOD(method string) bool {
	return strings.EqualFold(strings.TrimSpace(method), "COD")
}

func paymentMethodLabel(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "COD":
		return "thanh toán khi nhận hàng"
	case "VIETQR":
		return "chuyển khoản VietQR"
	case "ZALOPAY":
		return "ví ZaloPay"
	default:
		return "thanh toán trực tuyến"
	}
}

func shopReviewDecisionLabel(decision string) string {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVE", "APPROVED":
		return "Đã duyệt"
	case "REJECT", "REJECTED":
		return "Từ chối"
	case "REQUEST_INFO":
		return "Yêu cầu bổ sung thông tin"
	default:
		return "Đang cập nhật"
	}
}

func normalizeReturnStatus(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	return strings.TrimPrefix(s, "RETURN_STATUS_")
}

func normalizeRefundApprovalStatus(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	return strings.TrimPrefix(s, "REFUND_STATUS_")
}

func shipmentStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "CREATED":
		return "Đã tạo vận đơn"
	case "PICKING":
		return "Đang lấy hàng"
	case "DELIVERING":
		return "Đang vận chuyển"
	case "DELIVERED":
		return "Giao thành công"
	case "FAILED":
		return "Giao thất bại"
	case "RETURNING":
		return "Đang hoàn hàng"
	case "RETURNED":
		return "Đã hoàn về kho"
	case "CANCELLED":
		return "Đã hủy vận đơn"
	default:
		return "Đang cập nhật"
	}
}

func returnShipmentStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "CREATED":
		return "Đã tạo vận đơn trả hàng"
	case "PICKING":
		return "Đang lấy hàng trả"
	case "DELIVERING":
		return "Hàng trả đang vận chuyển về người bán"
	case "DELIVERED":
		return "Hàng trả đã về kho người bán"
	case "FAILED":
		return "Lấy hoặc giao hàng trả không thành công"
	case "RETURNING":
		return "Hàng trả đang được hoàn lại người gửi"
	case "RETURNED":
		return "Hàng trả đã hoàn lại người gửi"
	case "CANCELLED":
		return "Đã hủy vận đơn trả hàng"
	default:
		return "Đang cập nhật vận đơn trả hàng"
	}
}

func carrierLabel(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "GHN":
		return "Giao Hàng Nhanh"
	case "GHTK":
		return "Giao Hàng Tiết Kiệm"
	case "SPX":
		return "SPX Express"
	case "MOCK", "MOCK_EXPRESS", "NEXUS_EXPRESS":
		return "Vận chuyển tiêu chuẩn NexusMart"
	default:
		if trimmed := strings.TrimSpace(code); trimmed != "" {
			return trimmed
		}
		return "đơn vị vận chuyển"
	}
}

func localizeReason(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	switch strings.ToLower(trimmed) {
	case "payment_timeout_expired", "payment expired", "payment expired after timeout", "expired":
		return "Quá thời hạn thanh toán"
	case "cancelled", "canceled":
		return "Giao dịch đã bị hủy"
	case "payment_failed", "failed":
		return "Giao dịch không thành công"
	case "late_payment":
		return "Thanh toán muộn sau khi đơn đã hủy hoặc đã có giao dịch mới"
	case "payment initialization failed":
		return "Khởi tạo thanh toán không thành công"
	case "seller_confirm_timeout":
		return "Người bán quá thời hạn xác nhận đơn hàng"
	case "order_returned":
		return "Đơn hàng đã hoàn trả"
	case "return_approved":
		return "Yêu cầu trả hàng đã được chấp thuận"
	case "refund_rejected":
		return "Khoản hoàn tiền cần được xem xét lại"
	case "return_timeout", "return_ship_timeout":
		return "Quá thời hạn gửi hàng trả"
	case "seller_response_timeout":
		return "Người bán quá thời hạn phản hồi yêu cầu trả hàng"
	case "seller_inspect_timeout":
		return "Quá thời hạn kiểm tra hàng trả"
	case "evidence_timeout":
		return "Quá thời hạn bổ sung bằng chứng"
	case "buyer_escalate_timeout":
		return "Quá thời hạn mở khiếu nại"
	case "delivery_failed":
		return "Giao hàng không thành công"
	case "buyer_cancelled":
		return "Người mua hủy đơn hàng"
	case "seller_cancelled":
		return "Người bán hủy đơn hàng"
	case "admin_cancelled":
		return "Quản trị viên hủy đơn hàng"
	default:
		return trimmed
	}
}

func orderLabel(code, id string) string {
	if strings.TrimSpace(code) != "" {
		return code
	}
	return id
}

func orderLink(orderID string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return "/don-hang"
	}
	return "/don-hang/" + orderID
}

func sellerOrderLink(orderID string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return "/seller/don-hang"
	}
	return "/seller/don-hang/" + orderID
}

func sellerProductLink(productID string) string {
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return "/seller/san-pham"
	}
	return "/seller/san-pham/" + productID
}
