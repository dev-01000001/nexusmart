package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/cloudflare"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/site"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	customDomainExpiryDuration    = 7 * 24 * time.Hour
	customDomainClaimHoldDuration = 48 * time.Hour
	cfCleanupTimeout              = 5 * time.Second
	pollerActorID                 = "system:domain-poller"
	pollerActorRole               = "system"
)

var (
	errDomainGone     = errors.New("domain deleted concurrently")
	errDomainRemoving = errors.New("domain is being removed")
)

func normalizeCFDomainStatus(st string) string {
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case cloudflare.StatusActive:
		return DomainStatusActive
	case cloudflare.StatusFailed:
		return DomainStatusFailed
	default:
		return DomainStatusPending
	}
}

func tlsStatusForDomainStatus(st string) string {
	switch st {
	case DomainStatusActive:
		return "ACTIVE"
	case DomainStatusFailed:
		return "FAILED"
	default:
		return "PENDING"
	}
}

func marshalVerificationRecords(records []cloudflare.VerificationRecord) ([]byte, error) {
	if records == nil {
		records = []cloudflare.VerificationRecord{}
	}
	return json.Marshal(records)
}

func strPtrOrNil(v string) *string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// domainBlocksOtherSite reports whether an existing domain row blocks another site from claiming
// the same host at now. Only ACTIVE, REMOVING, non-custom rows, and PENDING rows within the
// 48-hour claim hold block another site; FAILED and stale (>48h) PENDING rows can be reclaimed.
func domainBlocksOtherSite(dom db.Domain, now time.Time) bool {
	if dom.Kind != DomainKindCustom {
		return true
	}
	switch dom.Status {
	case DomainStatusActive, DomainStatusRemoving:
		return true
	case DomainStatusFailed:
		return false
	case DomainStatusPending:
		return now.Sub(dom.CreatedAt) <= customDomainClaimHoldDuration
	default:
		return true
	}
}

// canReplaceOwnCustomDomain reports whether the same site may re-add its own FAILED custom host
// by replacing the old row in place without triggering domain_limit.
func canReplaceOwnCustomDomain(dom db.Domain, hostname string) bool {
	return dom.Kind == DomainKindCustom &&
		strings.EqualFold(dom.Host, hostname) &&
		dom.Status == DomainStatusFailed
}

// ListSiteDomains lists all domains (subdomain + optional custom domain) for a shop's site.
func (s *Server) ListSiteDomains(ctx context.Context, req *userv1.ListSiteDomainsRequest) (*userv1.ListSiteDomainsResponse, error) {
	shopID, _, err := s.resolveSiteShopID(ctx, req.GetShopId(), auth.CapSitesRead)
	if err != nil {
		return nil, err
	}

	st, err := s.store.GetSiteByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site")
	}

	domains, err := s.store.ListDomainsBySiteID(ctx, st.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list site domains")
	}

	out := make([]*userv1.Domain, 0, len(domains))
	for _, d := range domains {
		out = append(out, toProtoDomain(d))
	}
	return &userv1.ListSiteDomainsResponse{Domains: out}, nil
}

// AddCustomDomain validates a custom hostname, registers it with Cloudflare for SaaS,
// persists the domain row (max 1 CUSTOM domain per site, globally unique host), and writes an audit log.
func (s *Server) AddCustomDomain(ctx context.Context, req *userv1.AddCustomDomainRequest) (*userv1.AddCustomDomainResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId(), auth.CapSitesDomainsWrite)
	if err != nil {
		return nil, err
	}

	shop, err := s.store.GetShopByID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop")
	}
	if shop.Status == ShopStatusSuspended {
		return nil, status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: suspended shop cannot add a custom domain")
	}

	hostname, err := site.ValidateCustomHostname(req.GetHostname(), s.opts.SiteRootDomain, s.opts.CFMode == "mock")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	st, err := s.store.GetSiteByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site")
	}
	if st.Status == SiteStatusSuspended {
		return nil, status.Error(codes.FailedPrecondition, "SITE_SUSPENDED: suspended site cannot add a custom domain")
	}

	// Pre-checks before opening a locked transaction or calling Cloudflare.
	now := s.now()
	if ownCustom, err := s.store.GetSiteCustomDomain(ctx, st.ID); err == nil {
		if !canReplaceOwnCustomDomain(ownCustom, hostname) {
			return nil, status.Error(codes.FailedPrecondition, "domain_limit: site already has a custom domain")
		}
	} else if !isNotFoundErr(err) {
		return nil, status.Error(codes.Internal, "failed to check site custom domain")
	}
	if existing, err := s.store.GetDomainByHost(ctx, hostname); err == nil {
		if existing.SiteID == st.ID {
			if !canReplaceOwnCustomDomain(existing, hostname) {
				if existing.Kind == DomainKindCustom {
					return nil, status.Error(codes.FailedPrecondition, "domain_limit: site already has a custom domain")
				}
				return nil, status.Error(codes.AlreadyExists, "domain_taken: hostname is already registered")
			}
		} else if domainBlocksOtherSite(existing, now) {
			return nil, status.Error(codes.AlreadyExists, "domain_taken: hostname is already registered")
		}
	} else if !isNotFoundErr(err) {
		return nil, status.Error(codes.Internal, "failed to check domain uniqueness")
	}

	cleanupCF := func(cfHostnameID string) {
		id := strings.TrimSpace(cfHostnameID)
		if id == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cfCleanupTimeout)
		defer cancel()
		if delErr := s.cf.DeleteCustomHostname(cleanupCtx, id); delErr != nil {
			s.log.WarnContext(ctx, "cloudflare cleanup custom hostname failed after db error",
				slog.String("hostname", hostname),
				slog.String("cf_custom_hostname_id", id),
				slog.String("error", cloudflare.SanitizeError(delErr)),
			)
		}
	}

	var (
		created     db.Domain
		createdCFID string
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		lockedShop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}
		if lockedShop.Status == ShopStatusSuspended {
			return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: suspended shop cannot add a custom domain")
		}

		lockedSite, txErr := q.GetSiteByShopIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site not found")
			}
			return txErr
		}
		if lockedSite.Status == SiteStatusSuspended {
			return status.Error(codes.FailedPrecondition, "SITE_SUSPENDED: suspended site cannot add a custom domain")
		}

		if txErr = q.LockDomainHost(ctx, hostname); txErr != nil {
			return txErr
		}

		txNow := s.now()
		if ownCustom, txErr := q.GetSiteCustomDomain(ctx, lockedSite.ID); txErr == nil {
			if !canReplaceOwnCustomDomain(ownCustom, hostname) {
				return status.Error(codes.FailedPrecondition, "domain_limit: site already has a custom domain")
			}
		} else if !isNotFoundErr(txErr) {
			return txErr
		}

		var reclaimed *db.Domain
		if existing, txErr := q.GetDomainByHostForUpdate(ctx, hostname); txErr == nil {
			if existing.SiteID == lockedSite.ID {
				if !canReplaceOwnCustomDomain(existing, hostname) {
					if existing.Kind == DomainKindCustom {
						return status.Error(codes.FailedPrecondition, "domain_limit: site already has a custom domain")
					}
					return status.Error(codes.AlreadyExists, "domain_taken: hostname is already registered")
				}
			} else if domainBlocksOtherSite(existing, txNow) {
				return status.Error(codes.AlreadyExists, "domain_taken: hostname is already registered")
			}

			if existing.CfCustomHostnameID != nil {
				if oldCFID := strings.TrimSpace(*existing.CfCustomHostnameID); oldCFID != "" {
					if delErr := s.cf.DeleteCustomHostname(ctx, oldCFID); delErr != nil {
						s.log.WarnContext(ctx, "cloudflare delete stale custom hostname failed before reclaim",
							slog.String("shop_id", shopID),
							slog.String("site_id", lockedSite.ID),
							slog.String("stale_domain_id", existing.ID),
							slog.String("stale_site_id", existing.SiteID),
							slog.String("hostname", hostname),
							slog.String("error", cloudflare.SanitizeError(delErr)),
						)
						return status.Error(codes.Unavailable, "failed to remove stale custom domain from DNS provider")
					}
				}
			}
			if txErr = q.DeleteDomain(ctx, existing.ID); txErr != nil {
				return txErr
			}
			reclaimedCopy := existing
			reclaimed = &reclaimedCopy
		} else if !isNotFoundErr(txErr) {
			return txErr
		}

		cfRes, cfErr := s.cf.CreateCustomHostname(ctx, hostname)
		if cfErr != nil {
			s.log.WarnContext(ctx, "cloudflare create custom hostname failed",
				slog.String("shop_id", shopID),
				slog.String("site_id", lockedSite.ID),
				slog.String("hostname", hostname),
				slog.String("error", cloudflare.SanitizeError(cfErr)),
			)
			return status.Error(codes.Unavailable, "failed to register custom domain with DNS provider")
		}
		createdCFID = strings.TrimSpace(cfRes.ID)

		verJSON, txErr := marshalVerificationRecords(cfRes.Verification)
		if txErr != nil {
			return status.Error(codes.Internal, "failed to encode verification records")
		}

		domStatus := normalizeCFDomainStatus(cfRes.Status)
		var (
			verifiedAt *time.Time
			lastErr    *string
			isPrimary  bool
			tlsStatus  = tlsStatusForDomainStatus(domStatus)
		)
		if domStatus == DomainStatusActive {
			isPrimary = true
			verifiedAt = &txNow
			tlsStatus = "ACTIVE"
		} else {
			lastErr = strPtrOrNil(cfRes.LastError)
		}
		cfID := strPtrOrNil(cfRes.ID)

		if domStatus == DomainStatusActive {
			if txErr = q.SetSiteDomainPrimary(ctx, db.SetSiteDomainPrimaryParams{
				SiteID:    lockedSite.ID,
				Kind:      DomainKindSubdomain,
				IsPrimary: false,
			}); txErr != nil {
				return txErr
			}
		}

		created, txErr = q.CreateCustomDomain(ctx, db.CreateCustomDomainParams{
			ID:                 uuid.NewString(),
			SiteID:             lockedSite.ID,
			Lower:              hostname,
			IsPrimary:          isPrimary,
			VerifiedAt:         verifiedAt,
			TlsStatus:          tlsStatus,
			Status:             domStatus,
			CfCustomHostnameID: cfID,
			Verification:       verJSON,
			LastCheckedAt:      &txNow,
			LastError:          lastErr,
		})
		if txErr != nil {
			return txErr
		}

		beforeJSON := "{}"
		if reclaimed != nil {
			beforeJSON = marshalAuditJSON(auditDomainSnapshot(*reclaimed))
		}
		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.domain.add",
			TargetType: "domain",
			TargetID:   created.ID,
			BeforeJSON: beforeJSON,
			AfterJSON:  marshalAuditJSON(auditDomainSnapshot(created)),
			Reason:     "",
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		cleanupCF(createdCFID)
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if isUniqueViolation(err) {
			if uniqueConstraintName(err) == "idx_domains_site_custom" {
				return nil, status.Error(codes.FailedPrecondition, "domain_limit: site already has a custom domain")
			}
			return nil, status.Error(codes.AlreadyExists, "domain_taken: hostname is already registered")
		}
		return nil, status.Error(codes.Internal, "failed to save custom domain")
	}

	return &userv1.AddCustomDomainResponse{Domain: toProtoDomain(created)}, nil
}

// CheckCustomDomain queries Cloudflare for the latest verification/SSL status of a custom domain,
// applies status/primary transitions atomically, and writes an audit log.
func (s *Server) CheckCustomDomain(ctx context.Context, req *userv1.CheckCustomDomainRequest) (*userv1.CheckCustomDomainResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId(), auth.CapSitesDomainsWrite)
	if err != nil {
		return nil, err
	}

	domainID := strings.TrimSpace(req.GetDomainId())
	if domainID == "" {
		return nil, status.Error(codes.InvalidArgument, "domain_id is required")
	}
	if !isValidUUID(domainID) {
		return nil, status.Error(codes.NotFound, "domain not found")
	}

	st, err := s.store.GetSiteByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site")
	}

	dom, err := s.store.GetDomainByID(ctx, domainID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "domain not found")
		}
		return nil, status.Error(codes.Internal, "failed to load domain")
	}
	if dom.SiteID != st.ID {
		return nil, status.Error(codes.NotFound, "domain not found")
	}
	if dom.Kind != DomainKindCustom {
		return nil, status.Error(codes.FailedPrecondition, "only custom domains can be checked")
	}
	if dom.Status == DomainStatusRemoving {
		return nil, status.Error(codes.FailedPrecondition, "domain is being removed")
	}

	var cfID string
	if dom.CfCustomHostnameID != nil {
		cfID = *dom.CfCustomHostnameID
	}
	cfRes, cfErr := s.cf.GetCustomHostname(ctx, cfID, dom.Host)
	if cfErr != nil {
		sanitized := cloudflare.SanitizeError(cfErr)
		now := s.now()
		if _, updErr := s.store.UpdateDomainError(ctx, db.UpdateDomainErrorParams{
			ID:            dom.ID,
			LastCheckedAt: &now,
			LastError:     strPtrOrNil(sanitized),
		}); updErr != nil {
			s.log.WarnContext(ctx, "failed to record custom domain check error",
				slog.String("domain_id", dom.ID),
				slog.String("error", updErr.Error()),
			)
		}
		s.log.WarnContext(ctx, "cloudflare get custom hostname failed",
			slog.String("shop_id", shopID),
			slog.String("domain_id", dom.ID),
			slog.String("host", dom.Host),
			slog.String("error", sanitized),
		)
		return nil, status.Error(codes.Unavailable, "failed to check custom domain status with DNS provider")
	}

	updated, err := s.applyCustomDomainResult(ctx, st.ID, dom.ID, cfRes, claims.UserID(), claims.Role, true)
	if err != nil {
		if errors.Is(err, errDomainGone) {
			return nil, status.Error(codes.NotFound, "domain not found")
		}
		if errors.Is(err, errDomainRemoving) {
			return nil, status.Error(codes.FailedPrecondition, "domain is being removed")
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to update custom domain status")
	}

	return &userv1.CheckCustomDomainResponse{Domain: toProtoDomain(updated)}, nil
}

// applyCustomDomainResult locks the site, validates the custom domain is still active/checkable,
// applies the Cloudflare verification state and primary-host transition, and writes an audit log.
func (s *Server) applyCustomDomainResult(
	ctx context.Context,
	siteID, domainID string,
	cfRes cloudflare.CustomHostnameStatus,
	actorID, actorRole string,
	auditAlways bool,
) (db.Domain, error) {
	var updated db.Domain
	err := s.store.WithTx(ctx, func(q db.Querier) error {
		if _, txErr := q.GetSiteByIDForUpdate(ctx, siteID); txErr != nil {
			if isNotFoundErr(txErr) {
				return errDomainGone
			}
			return txErr
		}

		current, txErr := q.GetDomainByID(ctx, domainID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return errDomainGone
			}
			return txErr
		}
		if current.SiteID != siteID || current.Kind != DomainKindCustom {
			return errDomainGone
		}
		if current.Status == DomainStatusRemoving {
			return errDomainRemoving
		}

		verJSON := current.Verification
		if len(cfRes.Verification) > 0 {
			verJSON, txErr = marshalVerificationRecords(cfRes.Verification)
			if txErr != nil {
				return txErr
			}
		} else if len(verJSON) == 0 {
			verJSON = []byte("[]")
		}

		now := s.now()
		newStatus := normalizeCFDomainStatus(cfRes.Status)

		switch newStatus {
		case DomainStatusActive:
			if txErr = q.SetSiteDomainPrimary(ctx, db.SetSiteDomainPrimaryParams{
				SiteID:    siteID,
				Kind:      DomainKindSubdomain,
				IsPrimary: false,
			}); txErr != nil {
				return txErr
			}
			updated, txErr = q.UpdateDomainVerificationState(ctx, db.UpdateDomainVerificationStateParams{
				ID:            current.ID,
				Status:        DomainStatusActive,
				IsPrimary:     true,
				VerifiedAt:    &now,
				TlsStatus:     "ACTIVE",
				Verification:  verJSON,
				LastCheckedAt: &now,
				LastError:     nil,
			})
			if txErr != nil {
				if isNotFoundErr(txErr) {
					return errDomainGone
				}
				return txErr
			}

		case DomainStatusFailed:
			lastErr := strPtrOrNil(cfRes.LastError)
			if lastErr == nil {
				msg := "custom domain verification failed"
				lastErr = &msg
			}
			updated, txErr = q.UpdateDomainVerificationState(ctx, db.UpdateDomainVerificationStateParams{
				ID:            current.ID,
				Status:        DomainStatusFailed,
				IsPrimary:     false,
				VerifiedAt:    nil,
				TlsStatus:     "FAILED",
				Verification:  verJSON,
				LastCheckedAt: &now,
				LastError:     lastErr,
			})
			if txErr != nil {
				if isNotFoundErr(txErr) {
					return errDomainGone
				}
				return txErr
			}
			if current.IsPrimary {
				if txErr = q.SetSiteDomainPrimary(ctx, db.SetSiteDomainPrimaryParams{
					SiteID:    siteID,
					Kind:      DomainKindSubdomain,
					IsPrimary: true,
				}); txErr != nil {
					return txErr
				}
			}

		default: // DomainStatusPending
			updated, txErr = q.UpdateDomainVerificationState(ctx, db.UpdateDomainVerificationStateParams{
				ID:            current.ID,
				Status:        DomainStatusPending,
				IsPrimary:     false,
				VerifiedAt:    nil,
				TlsStatus:     "PENDING",
				Verification:  verJSON,
				LastCheckedAt: &now,
				LastError:     strPtrOrNil(cfRes.LastError),
			})
			if txErr != nil {
				if isNotFoundErr(txErr) {
					return errDomainGone
				}
				return txErr
			}
			if current.IsPrimary {
				if txErr = q.SetSiteDomainPrimary(ctx, db.SetSiteDomainPrimaryParams{
					SiteID:    siteID,
					Kind:      DomainKindSubdomain,
					IsPrimary: true,
				}); txErr != nil {
					return txErr
				}
			}
		}

		statusChanged := current.Status != updated.Status || current.IsPrimary != updated.IsPrimary
		if auditAlways || statusChanged {
			action := "site.domain.check"
			if current.Status != DomainStatusActive && updated.Status == DomainStatusActive {
				action = "site.domain.activated"
			}
			claims, _ := auth.ClaimsFromContext(ctx)
			_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
				ID:         uuid.NewString(),
				ActorID:    actorID,
				ActorRole:  actorRole,
				Action:     action,
				TargetType: "domain",
				TargetID:   updated.ID,
				BeforeJSON: marshalAuditJSON(auditDomainSnapshot(current)),
				AfterJSON:  marshalAuditJSON(auditDomainSnapshot(updated)),
				Reason:     "",
				IPAddress:  auditIPFromContext(ctx, claims, ""),
			})
			if txErr != nil {
				return txErr
			}
		}

		return nil
	})
	return updated, err
}

// RemoveCustomDomain marks a custom domain REMOVING, immediately restores the site's subdomain
// as primary, then deletes the custom hostname from Cloudflare before removing the DB row.
// If Cloudflare deletion fails, the row remains REMOVING so the background poller can retry.
func (s *Server) RemoveCustomDomain(ctx context.Context, req *userv1.RemoveCustomDomainRequest) (*userv1.RemoveCustomDomainResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId(), auth.CapSitesDomainsWrite)
	if err != nil {
		return nil, err
	}

	domainID := strings.TrimSpace(req.GetDomainId())
	if domainID == "" {
		return nil, status.Error(codes.InvalidArgument, "domain_id is required")
	}
	if !isValidUUID(domainID) {
		return nil, status.Error(codes.NotFound, "domain not found")
	}

	if _, err := s.store.GetSiteByShopID(ctx, shopID); err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site")
	}

	var (
		targetID string
		cfID     string
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		lockedSite, txErr := q.GetSiteByShopIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site not found")
			}
			return txErr
		}

		dom, txErr := q.GetDomainByID(ctx, domainID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "domain not found")
			}
			return txErr
		}
		if dom.SiteID != lockedSite.ID {
			return status.Error(codes.NotFound, "domain not found")
		}
		if dom.Kind != DomainKindCustom {
			return status.Error(codes.FailedPrecondition, "only custom domains can be removed")
		}

		removing, txErr := q.MarkCustomDomainRemoving(ctx, dom.ID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "domain not found")
			}
			return txErr
		}
		if txErr = q.SetSiteDomainPrimary(ctx, db.SetSiteDomainPrimaryParams{
			SiteID:    lockedSite.ID,
			Kind:      DomainKindSubdomain,
			IsPrimary: true,
		}); txErr != nil {
			return txErr
		}

		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.domain.remove",
			TargetType: "domain",
			TargetID:   dom.ID,
			BeforeJSON: marshalAuditJSON(auditDomainSnapshot(dom)),
			AfterJSON:  marshalAuditJSON(auditDomainSnapshot(removing)),
			Reason:     "",
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		if txErr != nil {
			return txErr
		}

		targetID = dom.ID
		if dom.CfCustomHostnameID != nil {
			cfID = strings.TrimSpace(*dom.CfCustomHostnameID)
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to prepare custom domain removal")
	}

	if cfID != "" {
		if cfErr := s.cf.DeleteCustomHostname(ctx, cfID); cfErr != nil {
			sanitized := cloudflare.SanitizeError(cfErr)
			now := s.now()
			if _, updErr := s.store.UpdateDomainError(ctx, db.UpdateDomainErrorParams{
				ID:            targetID,
				LastCheckedAt: &now,
				LastError:     strPtrOrNil(sanitized),
			}); updErr != nil {
				s.log.WarnContext(ctx, "failed to record custom domain delete error",
					slog.String("domain_id", targetID),
					slog.String("error", updErr.Error()),
				)
			}
			s.log.WarnContext(ctx, "cloudflare delete custom hostname failed; marked REMOVING for retry",
				slog.String("shop_id", shopID),
				slog.String("domain_id", targetID),
				slog.String("error", sanitized),
			)
			return nil, status.Error(codes.Unavailable, "failed to delete custom domain from DNS provider; marked REMOVING for retry")
		}
	}

	if err := s.store.DeleteDomain(ctx, targetID); err != nil {
		return nil, status.Error(codes.Internal, "failed to delete custom domain record")
	}

	return &userv1.RemoveCustomDomainResponse{}, nil
}

// RunDomainPoller runs PollCustomDomainsOnce on s.opts.CFPollInterval until ctx is cancelled.
func (s *Server) RunDomainPoller(ctx context.Context) {
	interval := s.opts.CFPollInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollCustomDomainsOnce(ctx)
		}
	}
}

// PollCustomDomainsOnce executes a single reconciliation pass for PENDING and REMOVING custom domains.
func (s *Server) PollCustomDomainsOnce(ctx context.Context) {
	pending, err := s.store.ListCustomDomainsByStatus(ctx, DomainStatusPending)
	if err != nil {
		s.log.WarnContext(ctx, "domain poller failed to list PENDING custom domains", slog.String("error", err.Error()))
	} else {
		for _, dom := range pending {
			if ctx.Err() != nil {
				return
			}
			if s.now().Sub(dom.CreatedAt) > customDomainExpiryDuration {
				expired := cloudflare.CustomHostnameStatus{
					Hostname:  dom.Host,
					Status:    cloudflare.StatusFailed,
					LastError: "verification expired after 7 days",
				}
				if _, applyErr := s.applyCustomDomainResult(ctx, dom.SiteID, dom.ID, expired, pollerActorID, pollerActorRole, false); applyErr != nil &&
					!errors.Is(applyErr, errDomainGone) && !errors.Is(applyErr, errDomainRemoving) {
					s.log.WarnContext(ctx, "domain poller failed to expire PENDING custom domain",
						slog.String("domain_id", dom.ID),
						slog.String("error", applyErr.Error()),
					)
				}
				continue
			}

			var cfID string
			if dom.CfCustomHostnameID != nil {
				cfID = *dom.CfCustomHostnameID
			}
			cfRes, cfErr := s.cf.GetCustomHostname(ctx, cfID, dom.Host)
			if cfErr != nil {
				sanitized := cloudflare.SanitizeError(cfErr)
				now := s.now()
				if _, updErr := s.store.UpdateDomainError(ctx, db.UpdateDomainErrorParams{
					ID:            dom.ID,
					LastCheckedAt: &now,
					LastError:     strPtrOrNil(sanitized),
				}); updErr != nil {
					s.log.WarnContext(ctx, "domain poller failed to record error for PENDING custom domain",
						slog.String("domain_id", dom.ID),
						slog.String("error", updErr.Error()),
					)
				}
				s.log.WarnContext(ctx, "domain poller cloudflare check failed",
					slog.String("domain_id", dom.ID),
					slog.String("host", dom.Host),
					slog.String("error", sanitized),
				)
				continue
			}

			if _, applyErr := s.applyCustomDomainResult(ctx, dom.SiteID, dom.ID, cfRes, pollerActorID, pollerActorRole, false); applyErr != nil &&
				!errors.Is(applyErr, errDomainGone) && !errors.Is(applyErr, errDomainRemoving) {
				s.log.WarnContext(ctx, "domain poller failed to apply status update",
					slog.String("domain_id", dom.ID),
					slog.String("error", applyErr.Error()),
				)
			}
		}
	}

	removing, err := s.store.ListCustomDomainsByStatus(ctx, DomainStatusRemoving)
	if err != nil {
		s.log.WarnContext(ctx, "domain poller failed to list REMOVING custom domains", slog.String("error", err.Error()))
		return
	}
	for _, dom := range removing {
		if ctx.Err() != nil {
			return
		}
		var cfID string
		if dom.CfCustomHostnameID != nil {
			cfID = strings.TrimSpace(*dom.CfCustomHostnameID)
		}
		if cfID != "" {
			if cfErr := s.cf.DeleteCustomHostname(ctx, cfID); cfErr != nil {
				sanitized := cloudflare.SanitizeError(cfErr)
				now := s.now()
				if _, updErr := s.store.UpdateDomainError(ctx, db.UpdateDomainErrorParams{
					ID:            dom.ID,
					LastCheckedAt: &now,
					LastError:     strPtrOrNil(sanitized),
				}); updErr != nil {
					s.log.WarnContext(ctx, "domain poller failed to record error for REMOVING custom domain",
						slog.String("domain_id", dom.ID),
						slog.String("error", updErr.Error()),
					)
				}
				s.log.WarnContext(ctx, "domain poller cloudflare delete retry failed",
					slog.String("domain_id", dom.ID),
					slog.String("host", dom.Host),
					slog.String("error", sanitized),
				)
				continue
			}
		}
		if delErr := s.store.DeleteDomain(ctx, dom.ID); delErr != nil {
			s.log.WarnContext(ctx, "domain poller failed to delete REMOVING custom domain row",
				slog.String("domain_id", dom.ID),
				slog.String("error", delErr.Error()),
			)
		}
	}
}
