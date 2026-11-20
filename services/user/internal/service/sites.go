package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/site"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) resolveSiteShopID(ctx context.Context, requestedShopID string) (string, *auth.Claims, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return "", nil, err
	}
	reqShopID := strings.TrimSpace(requestedShopID)
	if isAdminRole(claims.Role) {
		if reqShopID == "" {
			return "", nil, status.Error(codes.InvalidArgument, "shop_id is required")
		}
		if !isValidUUID(reqShopID) {
			return "", nil, status.Error(codes.NotFound, "shop not found")
		}
		return reqShopID, claims, nil
	}
	if strings.EqualFold(claims.Role, RoleSeller) {
		ownShopID := strings.TrimSpace(claims.ShopID)
		if ownShopID == "" {
			return "", nil, status.Error(codes.PermissionDenied, "seller account has no shop")
		}
		if reqShopID != "" && reqShopID != ownShopID {
			return "", nil, status.Error(codes.PermissionDenied, "cannot access another shop's site")
		}
		if !isValidUUID(ownShopID) {
			return "", nil, status.Error(codes.NotFound, "shop not found")
		}
		return ownShopID, claims, nil
	}
	return "", nil, status.Error(codes.PermissionDenied, "seller or admin role required")
}

func (s *Server) sitePrimaryHost(subdomain, dbHost string) string {
	if h := strings.TrimSpace(dbHost); h != "" {
		return h
	}
	if sub := strings.TrimSpace(subdomain); sub != "" {
		return sub + "." + s.opts.SiteRootDomain
	}
	return ""
}

func toProtoSiteFields(
	id, shopID, shopName, subdomain, primaryHost, st string,
	configJSON []byte,
	publishedVersionID *string,
	publishedVersionNumber int32,
	publishedAt *time.Time,
	suspendedReason string,
	createdAt, updatedAt time.Time,
) *userv1.Site {
	out := &userv1.Site{
		Id:                     id,
		ShopId:                 shopID,
		ShopName:               shopName,
		Subdomain:              subdomain,
		PrimaryHost:            primaryHost,
		Status:                 siteStatusToProto(st),
		ConfigJson:             string(configJSON),
		PublishedVersionNumber: publishedVersionNumber,
		SuspendedReason:        suspendedReason,
		CreatedAt:              timestamppb.New(createdAt),
		UpdatedAt:              timestamppb.New(updatedAt),
	}
	if publishedVersionID != nil {
		out.PublishedVersionId = *publishedVersionID
	}
	if publishedAt != nil {
		out.PublishedAt = timestamppb.New(*publishedAt)
	}
	return out
}

// GetSiteByHost is PUBLIC (no JWT). Returns ONLY published config of a PUBLISHED site
// whose shop is ACTIVE, with public shop fields only. Never leaks draft_config.
func (s *Server) GetSiteByHost(ctx context.Context, req *userv1.GetSiteByHostRequest) (*userv1.GetSiteByHostResponse, error) {
	if strings.TrimSpace(req.GetHost()) == "" {
		return nil, status.Error(codes.InvalidArgument, "host is required")
	}
	host, err := site.NormalizeHost(req.GetHost())
	if err != nil {
		return nil, status.Error(codes.NotFound, "site not found")
	}

	row, err := s.store.GetPublishedSiteByHost(ctx, host)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site by host")
	}

	return &userv1.GetSiteByHostResponse{
		Site: toProtoSiteFields(
			row.ID, row.ShopID, row.ShopName, row.Subdomain,
			s.sitePrimaryHost(row.Subdomain, row.PrimaryHost),
			row.Status, row.PublishedConfig,
			row.PublishedVersionID, row.PublishedVersionNumber, row.PublishedAt,
			"", row.CreatedAt, row.UpdatedAt,
		),
		Shop: &userv1.PublicShop{
			Id:          row.ShopID,
			Name:        row.ShopName,
			Description: row.ShopDescription,
			Status:      shopStatusToProto(row.ShopStatus),
		},
	}, nil
}

// GetSiteByShop is PUBLIC (no JWT). Returns ONLY published config of a PUBLISHED site
// whose shop is ACTIVE, with public shop fields only.
func (s *Server) GetSiteByShop(ctx context.Context, req *userv1.GetSiteByShopRequest) (*userv1.GetSiteByShopResponse, error) {
	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "site not found")
	}

	row, err := s.store.GetPublishedSiteByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site by shop")
	}

	return &userv1.GetSiteByShopResponse{
		Site: toProtoSiteFields(
			row.ID, row.ShopID, row.ShopName, row.Subdomain,
			s.sitePrimaryHost(row.Subdomain, row.PrimaryHost),
			row.Status, row.PublishedConfig,
			row.PublishedVersionID, row.PublishedVersionNumber, row.PublishedAt,
			"", row.CreatedAt, row.UpdatedAt,
		),
		Shop: &userv1.PublicShop{
			Id:          row.ShopID,
			Name:        row.ShopName,
			Description: row.ShopDescription,
			Status:      shopStatusToProto(row.ShopStatus),
		},
	}, nil
}

// GetSiteDraft returns the shop's site with its draft_config. Restricted to owning seller or admin.
func (s *Server) GetSiteDraft(ctx context.Context, req *userv1.GetSiteDraftRequest) (*userv1.GetSiteDraftResponse, error) {
	shopID, _, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	row, err := s.store.GetSiteDetailByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site draft")
	}

	return &userv1.GetSiteDraftResponse{
		Site: toProtoSiteFields(
			row.ID, row.ShopID, row.ShopName, row.Subdomain,
			s.sitePrimaryHost(row.Subdomain, row.PrimaryHost),
			row.Status, row.DraftConfig,
			row.PublishedVersionID, row.PublishedVersionNumber, row.PublishedAt,
			row.SuspendedReason, row.CreatedAt, row.UpdatedAt,
		),
	}, nil
}

// SaveSiteDraft validates subdomain and config_json, upserts the site draft + subdomain domain
// (never mutating status or published_version_id), and records an audit log.
func (s *Server) SaveSiteDraft(ctx context.Context, req *userv1.SaveSiteDraftRequest) (*userv1.SaveSiteDraftResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	if _, err := s.store.GetShopByID(ctx, shopID); err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop")
	}

	sub, err := site.ValidateSubdomain(req.GetSubdomain())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	_, canonicalJSON, err := site.ValidateAndCanonicalizeConfig([]byte(req.GetConfigJson()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	primaryHost := sub + "." + s.opts.SiteRootDomain

	// Early uniqueness check (concurrent races are also caught by unique index 23505 below).
	if other, lookupErr := s.store.GetSiteBySubdomain(ctx, sub); lookupErr == nil && other.ShopID != shopID {
		return nil, status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: subdomain is already in use")
	} else if lookupErr != nil && !isNotFoundErr(lookupErr) {
		return nil, status.Error(codes.Internal, "failed to check subdomain uniqueness")
	}

	var detail db.GetSiteDetailByIDRow
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		existing, txErr := q.GetSiteByShopIDForUpdate(ctx, shopID)
		isCreate := false
		if txErr != nil {
			if !isNotFoundErr(txErr) {
				return txErr
			}
			isCreate = true
		}

		if otherDom, domErr := q.GetDomainByHost(ctx, primaryHost); domErr == nil {
			if isCreate || otherDom.SiteID != existing.ID {
				return status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: domain host is already in use")
			}
		} else if !isNotFoundErr(domErr) {
			return domErr
		}

		var (
			saved      db.Site
			beforeJSON = "{}"
		)
		if isCreate {
			saved, txErr = q.CreateSite(ctx, db.CreateSiteParams{
				ID:          uuid.NewString(),
				ShopID:      shopID,
				Lower:       sub,
				DraftConfig: canonicalJSON,
			})
		} else {
			beforeJSON = marshalAuditJSON(auditSiteSnapshot(existing))
			saved, txErr = q.UpdateSiteDraft(ctx, db.UpdateSiteDraftParams{
				ID:          existing.ID,
				Lower:       sub,
				DraftConfig: canonicalJSON,
			})
		}
		if txErr != nil {
			return txErr
		}

		if _, txErr = q.UpsertSiteSubdomainDomain(ctx, db.UpsertSiteSubdomainDomainParams{
			ID:     uuid.NewString(),
			SiteID: saved.ID,
			Lower:  primaryHost,
		}); txErr != nil {
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.save_draft",
			TargetType: "site",
			TargetID:   saved.ID,
			BeforeJSON: beforeJSON,
			AfterJSON:  marshalAuditJSON(auditSiteSnapshot(saved)),
			Reason:     "",
			IPAddress:  peerIPFromContext(ctx),
		}); txErr != nil {
			return txErr
		}

		detail, txErr = q.GetSiteDetailByID(ctx, saved.ID)
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if isUniqueViolation(err) {
			return nil, status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: subdomain is already in use")
		}
		return nil, status.Error(codes.Internal, "failed to save site draft")
	}

	return &userv1.SaveSiteDraftResponse{
		Site: toProtoSiteFields(
			detail.ID, detail.ShopID, detail.ShopName, detail.Subdomain,
			s.sitePrimaryHost(detail.Subdomain, detail.PrimaryHost),
			detail.Status, detail.DraftConfig,
			detail.PublishedVersionID, detail.PublishedVersionNumber, detail.PublishedAt,
			detail.SuspendedReason, detail.CreatedAt, detail.UpdatedAt,
		),
	}, nil
}

// PublishSite creates a new immutable site_versions snapshot from the current draft_config,
// activates it as published_version_id, sets status=PUBLISHED, and writes an audit log.
func (s *Server) PublishSite(ctx context.Context, req *userv1.PublishSiteRequest) (*userv1.PublishSiteResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}
	if !isValidUUID(claims.UserID()) {
		return nil, status.Error(codes.Unauthenticated, "invalid actor user id")
	}

	note := strings.TrimSpace(req.GetNote())
	if strings.ContainsRune(note, 0) || utf8.RuneCountInString(note) > 200 {
		return nil, status.Error(codes.InvalidArgument, "note must be at most 200 characters")
	}

	shop, err := s.store.GetShopByID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop")
	}
	if shop.Status != ShopStatusActive {
		return nil, status.Error(codes.FailedPrecondition, "SHOP_NOT_ACTIVE: shop must be ACTIVE to publish site")
	}

	var (
		detail  db.GetSiteDetailByIDRow
		version db.SiteVersion
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		existing, txErr := q.GetSiteByShopIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site draft not found")
			}
			return txErr
		}
		if existing.Status == SiteStatusSuspended {
			return status.Error(codes.FailedPrecondition, "SITE_SUSPENDED: suspended site cannot be published")
		}

		sub, vErr := site.ValidateSubdomain(existing.Subdomain)
		if vErr != nil {
			return status.Error(codes.InvalidArgument, vErr.Error())
		}
		_, canonicalJSON, vErr := site.ValidateAndCanonicalizeConfig(existing.DraftConfig)
		if vErr != nil {
			return status.Error(codes.InvalidArgument, vErr.Error())
		}

		if _, txErr = q.UpsertSiteSubdomainDomain(ctx, db.UpsertSiteSubdomainDomainParams{
			ID:     uuid.NewString(),
			SiteID: existing.ID,
			Lower:  sub + "." + s.opts.SiteRootDomain,
		}); txErr != nil {
			return txErr
		}

		maxVer, txErr := q.GetMaxSiteVersionNumber(ctx, existing.ID)
		if txErr != nil {
			return txErr
		}

		version, txErr = q.CreateSiteVersion(ctx, db.CreateSiteVersionParams{
			ID:            uuid.NewString(),
			SiteID:        existing.ID,
			VersionNumber: maxVer + 1,
			Config:        canonicalJSON,
			PublishedBy:   claims.UserID(),
			Note:          note,
		})
		if txErr != nil {
			return txErr
		}

		verID := version.ID
		updated, txErr := q.UpdateSitePublished(ctx, db.UpdateSitePublishedParams{
			ID:                 existing.ID,
			PublishedVersionID: &verID,
		})
		if txErr != nil {
			return txErr
		}

		afterSnap := auditSiteSnapshot(updated)
		afterSnap["version_number"] = version.VersionNumber
		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.publish",
			TargetType: "site",
			TargetID:   existing.ID,
			BeforeJSON: marshalAuditJSON(auditSiteSnapshot(existing)),
			AfterJSON:  marshalAuditJSON(afterSnap),
			Reason:     note,
			IPAddress:  peerIPFromContext(ctx),
		}); txErr != nil {
			return txErr
		}

		detail, txErr = q.GetSiteDetailByID(ctx, updated.ID)
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if isUniqueViolation(err) {
			return nil, status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: subdomain is already in use")
		}
		return nil, status.Error(codes.Internal, "failed to publish site")
	}

	return &userv1.PublishSiteResponse{
		Site: toProtoSiteFields(
			detail.ID, detail.ShopID, detail.ShopName, detail.Subdomain,
			s.sitePrimaryHost(detail.Subdomain, detail.PrimaryHost),
			detail.Status, detail.DraftConfig,
			detail.PublishedVersionID, detail.PublishedVersionNumber, detail.PublishedAt,
			detail.SuspendedReason, detail.CreatedAt, detail.UpdatedAt,
		),
		Version: toProtoSiteVersion(version),
	}, nil
}

// RollbackSite restores published_version_id and draft_config to an existing immutable version
// belonging to the same site, rejecting suspended sites, and writes an audit log.
func (s *Server) RollbackSite(ctx context.Context, req *userv1.RollbackSiteRequest) (*userv1.RollbackSiteResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	versionID := strings.TrimSpace(req.GetVersionId())
	if versionID == "" {
		return nil, status.Error(codes.InvalidArgument, "version_id is required")
	}
	if !isValidUUID(versionID) {
		return nil, status.Error(codes.NotFound, "site version not found")
	}

	note := strings.TrimSpace(req.GetNote())
	if strings.ContainsRune(note, 0) || utf8.RuneCountInString(note) > 200 {
		return nil, status.Error(codes.InvalidArgument, "note must be at most 200 characters")
	}

	var (
		detail  db.GetSiteDetailByIDRow
		version db.SiteVersion
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		existing, txErr := q.GetSiteByShopIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site not found")
			}
			return txErr
		}
		if existing.Status == SiteStatusSuspended {
			return status.Error(codes.FailedPrecondition, "SITE_SUSPENDED: suspended site cannot be rolled back")
		}

		version, txErr = q.GetSiteVersionByID(ctx, versionID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site version not found")
			}
			return txErr
		}
		if version.SiteID != existing.ID {
			return status.Error(codes.NotFound, "site version not found")
		}

		verID := version.ID
		updated, txErr := q.UpdateSiteRollback(ctx, db.UpdateSiteRollbackParams{
			ID:                 existing.ID,
			PublishedVersionID: &verID,
			DraftConfig:        version.Config,
		})
		if txErr != nil {
			return txErr
		}

		afterSnap := auditSiteSnapshot(updated)
		afterSnap["restored_version_number"] = version.VersionNumber
		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.rollback",
			TargetType: "site",
			TargetID:   existing.ID,
			BeforeJSON: marshalAuditJSON(auditSiteSnapshot(existing)),
			AfterJSON:  marshalAuditJSON(afterSnap),
			Reason:     note,
			IPAddress:  peerIPFromContext(ctx),
		}); txErr != nil {
			return txErr
		}

		detail, txErr = q.GetSiteDetailByID(ctx, updated.ID)
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to rollback site")
	}

	return &userv1.RollbackSiteResponse{
		Site: toProtoSiteFields(
			detail.ID, detail.ShopID, detail.ShopName, detail.Subdomain,
			s.sitePrimaryHost(detail.Subdomain, detail.PrimaryHost),
			detail.Status, detail.DraftConfig,
			detail.PublishedVersionID, detail.PublishedVersionNumber, detail.PublishedAt,
			detail.SuspendedReason, detail.CreatedAt, detail.UpdatedAt,
		),
		Version: toProtoSiteVersion(version),
	}, nil
}

// ListSiteVersions lists immutable published versions for the shop's site (newest first).
func (s *Server) ListSiteVersions(ctx context.Context, req *userv1.ListSiteVersionsRequest) (*userv1.ListSiteVersionsResponse, error) {
	shopID, _, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	existing, err := s.store.GetSiteByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "site not found")
		}
		return nil, status.Error(codes.Internal, "failed to load site")
	}

	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())
	versions, err := s.store.ListSiteVersions(ctx, db.ListSiteVersionsParams{
		SiteID:     existing.ID,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list site versions")
	}
	total, err := s.store.CountSiteVersions(ctx, existing.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count site versions")
	}

	out := make([]*userv1.SiteVersion, 0, len(versions))
	for _, v := range versions {
		out = append(out, toProtoSiteVersion(v))
	}
	return &userv1.ListSiteVersionsResponse{
		Versions: out,
		Total:    total,
	}, nil
}

// ListSites lists all sites for admin moderation with optional status/query filter.
func (s *Server) ListSites(ctx context.Context, req *userv1.ListSitesRequest) (*userv1.ListSitesResponse, error) {
	if _, err := s.requireAdminClaims(ctx); err != nil {
		return nil, err
	}

	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())
	st := siteStatusFromProto(req.GetStatus())
	query := strings.TrimSpace(req.GetQuery())

	rows, err := s.store.ListSites(ctx, db.ListSitesParams{
		Status:     st,
		Query:      query,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list sites")
	}
	total, err := s.store.CountSites(ctx, db.CountSitesParams{
		Status: st,
		Query:  query,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count sites")
	}

	out := make([]*userv1.Site, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProtoSiteFields(
			row.ID, row.ShopID, row.ShopName, row.Subdomain,
			s.sitePrimaryHost(row.Subdomain, row.PrimaryHost),
			row.Status, row.DraftConfig,
			row.PublishedVersionID, row.PublishedVersionNumber, row.PublishedAt,
			row.SuspendedReason, row.CreatedAt, row.UpdatedAt,
		))
	}
	return &userv1.ListSitesResponse{
		Sites: out,
		Total: total,
	}, nil
}

// SuspendSite suspends or unsuspends a site (admin/super_admin only) and writes an audit log.
func (s *Server) SuspendSite(ctx context.Context, req *userv1.SuspendSiteRequest) (*userv1.SuspendSiteResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}

	siteID := strings.TrimSpace(req.GetSiteId())
	if siteID == "" {
		return nil, status.Error(codes.InvalidArgument, "site_id is required")
	}
	if !isValidUUID(siteID) {
		return nil, status.Error(codes.NotFound, "site not found")
	}

	reason := strings.TrimSpace(req.GetReason())
	if strings.ContainsRune(reason, 0) || utf8.RuneCountInString(reason) > 500 {
		return nil, status.Error(codes.InvalidArgument, "reason must be at most 500 characters")
	}

	var detail db.GetSiteDetailByIDRow
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		existing, txErr := q.GetSiteByIDForUpdate(ctx, siteID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "site not found")
			}
			return txErr
		}

		var (
			newStatus string
			newReason string
			action    string
		)
		if req.GetSuspend() {
			newStatus = SiteStatusSuspended
			newReason = reason
			action = "admin.site.suspend"
		} else {
			if existing.PublishedVersionID != nil && *existing.PublishedVersionID != "" {
				newStatus = SiteStatusPublished
			} else {
				newStatus = SiteStatusDraft
			}
			newReason = ""
			action = "admin.site.unsuspend"
		}

		updated, txErr := q.UpdateSiteStatus(ctx, db.UpdateSiteStatusParams{
			ID:              existing.ID,
			Status:          newStatus,
			SuspendedReason: newReason,
		})
		if txErr != nil {
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     action,
			TargetType: "site",
			TargetID:   existing.ID,
			BeforeJSON: marshalAuditJSON(auditSiteSnapshot(existing)),
			AfterJSON:  marshalAuditJSON(auditSiteSnapshot(updated)),
			Reason:     reason,
			IPAddress:  peerIPFromContext(ctx),
		}); txErr != nil {
			return txErr
		}

		detail, txErr = q.GetSiteDetailByID(ctx, updated.ID)
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to update site suspension status")
	}

	return &userv1.SuspendSiteResponse{
		Site: toProtoSiteFields(
			detail.ID, detail.ShopID, detail.ShopName, detail.Subdomain,
			s.sitePrimaryHost(detail.Subdomain, detail.PrimaryHost),
			detail.Status, detail.DraftConfig,
			detail.PublishedVersionID, detail.PublishedVersionNumber, detail.PublishedAt,
			detail.SuspendedReason, detail.CreatedAt, detail.UpdatedAt,
		),
	}, nil
}
