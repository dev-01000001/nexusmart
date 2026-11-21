package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/site"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BootstrapSiteTemplates idempotently upserts all built-in platform site templates into the database.
func BootstrapSiteTemplates(ctx context.Context, store db.Store, log *slog.Logger) error {
	templates := site.BuiltInTemplates()
	for _, tmpl := range templates {
		cfgJSON, err := tmpl.ConfigJSON()
		if err != nil {
			return fmt.Errorf("validate built-in site template %q: %w", tmpl.ID, err)
		}
		tags := tmpl.Tags
		if tags == nil {
			tags = []string{}
		}
		if _, err := store.UpsertSiteTemplate(ctx, db.UpsertSiteTemplateParams{
			ID:              tmpl.ID,
			Slug:            tmpl.Slug,
			Name:            tmpl.Name,
			Category:        tmpl.Category,
			Archetype:       tmpl.Archetype,
			Description:     tmpl.Description,
			PreviewImageUrl: tmpl.PreviewImageURL,
			Tags:            tags,
			Config:          cfgJSON,
			IsActive:        true,
			SortOrder:       tmpl.SortOrder,
		}); err != nil {
			return fmt.Errorf("upsert built-in site template %q: %w", tmpl.ID, err)
		}
	}
	if log != nil {
		log.InfoContext(ctx, "bootstrapped platform site templates", slog.Int("count", len(templates)))
	}
	return nil
}

func toProtoSiteTemplate(t db.SiteTemplate) *userv1.SiteTemplate {
	tags := make([]string, len(t.Tags))
	copy(tags, t.Tags)
	out := &userv1.SiteTemplate{
		Id:              t.ID,
		Slug:            t.Slug,
		Name:            t.Name,
		Category:        t.Category,
		Archetype:       t.Archetype,
		Description:     t.Description,
		PreviewImageUrl: t.PreviewImageUrl,
		Tags:            tags,
		ConfigJson:      string(t.Config),
		IsActive:        t.IsActive,
		SortOrder:       t.SortOrder,
	}
	if !t.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(t.CreatedAt)
	}
	if !t.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(t.UpdatedAt)
	}
	return out
}

func builtInDBSiteTemplates() ([]db.SiteTemplate, error) {
	now := time.Now().UTC()
	builtIn := site.BuiltInTemplates()
	out := make([]db.SiteTemplate, 0, len(builtIn))
	for _, tmpl := range builtIn {
		cfgJSON, err := tmpl.ConfigJSON()
		if err != nil {
			return nil, err
		}
		tags := make([]string, len(tmpl.Tags))
		copy(tags, tmpl.Tags)
		out = append(out, db.SiteTemplate{
			ID:              tmpl.ID,
			Slug:            tmpl.Slug,
			Name:            tmpl.Name,
			Category:        tmpl.Category,
			Archetype:       tmpl.Archetype,
			Description:     tmpl.Description,
			PreviewImageUrl: tmpl.PreviewImageURL,
			Tags:            tags,
			Config:          cfgJSON,
			IsActive:        true,
			SortOrder:       tmpl.SortOrder,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	return out, nil
}

func (s *Server) loadActiveTemplate(ctx context.Context, idOrSlug string) (db.SiteTemplate, error) {
	row, err := s.store.GetSiteTemplateByID(ctx, idOrSlug)
	if err == nil {
		return row, nil
	}
	if !isNotFoundErr(err) {
		return db.SiteTemplate{}, status.Error(codes.Internal, "failed to load site template")
	}

	// Fallback to built-in templates only when the backing table has no active rows yet (e.g. unbootstrapped tests).
	activeRows, listErr := s.store.ListActiveSiteTemplates(ctx)
	if listErr == nil && len(activeRows) == 0 {
		builtIn, bErr := builtInDBSiteTemplates()
		if bErr == nil {
			for _, t := range builtIn {
				if t.ID == idOrSlug || strings.EqualFold(t.Slug, idOrSlug) {
					return t, nil
				}
			}
		}
	}
	return db.SiteTemplate{}, status.Error(codes.NotFound, "site template not found")
}

// ListSiteTemplates is PUBLIC (no JWT). Returns all active platform site templates ordered by sort_order.
func (s *Server) ListSiteTemplates(ctx context.Context, _ *userv1.ListSiteTemplatesRequest) (*userv1.ListSiteTemplatesResponse, error) {
	rows, err := s.store.ListActiveSiteTemplates(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list site templates")
	}
	if len(rows) == 0 {
		rows, err = builtInDBSiteTemplates()
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to load built-in site templates")
		}
	}

	out := make([]*userv1.SiteTemplate, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProtoSiteTemplate(r))
	}
	return &userv1.ListSiteTemplatesResponse{
		Templates: out,
	}, nil
}

// GetSiteTemplate is PUBLIC (no JWT). Looks up an active platform site template by id or slug.
func (s *Server) GetSiteTemplate(ctx context.Context, req *userv1.GetSiteTemplateRequest) (*userv1.GetSiteTemplateResponse, error) {
	idOrSlug := strings.TrimSpace(req.GetId())
	if idOrSlug == "" {
		return nil, status.Error(codes.InvalidArgument, "template id is required")
	}

	tmpl, err := s.loadActiveTemplate(ctx, idOrSlug)
	if err != nil {
		return nil, err
	}
	return &userv1.GetSiteTemplateResponse{
		Template: toProtoSiteTemplate(tmpl),
	}, nil
}

// ApplySiteTemplate applies an active platform template to the shop's site draft_config while
// preserving brand identity (brand_name, tagline, logo_url) and never mutating live published state.
func (s *Server) ApplySiteTemplate(ctx context.Context, req *userv1.ApplySiteTemplateRequest) (*userv1.ApplySiteTemplateResponse, error) {
	shopID, claims, err := s.resolveSiteShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	templateID := strings.TrimSpace(req.GetTemplateId())
	if templateID == "" {
		return nil, status.Error(codes.InvalidArgument, "template_id is required")
	}

	tmpl, err := s.loadActiveTemplate(ctx, templateID)
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

	var validatedReqSub string
	if rawSub := strings.TrimSpace(req.GetSubdomain()); rawSub != "" {
		validatedReqSub, err = site.ValidateSubdomain(rawSub)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
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

		var existingDraft []byte
		if !isCreate {
			existingDraft = existing.DraftConfig
		}

		mergedJSON, applyErr := site.ApplyTemplateToConfig(tmpl.Config, existingDraft, shop.Name, shop.Description)
		if applyErr != nil {
			return status.Error(codes.InvalidArgument, applyErr.Error())
		}

		var (
			saved      db.Site
			beforeJSON = "{}"
		)
		if isCreate {
			sub, subErr := s.chooseInitialSubdomain(ctx, q, validatedReqSub, shop.Name, shop.ID)
			if subErr != nil {
				return subErr
			}
			saved, txErr = q.CreateSite(ctx, db.CreateSiteParams{
				ID:          uuid.NewString(),
				ShopID:      shopID,
				Lower:       sub,
				DraftConfig: mergedJSON,
			})
			if txErr != nil {
				return txErr
			}
			if _, txErr = q.UpsertSiteSubdomainDomain(ctx, db.UpsertSiteSubdomainDomainParams{
				ID:     uuid.NewString(),
				SiteID: saved.ID,
				Lower:  sub + "." + s.opts.SiteRootDomain,
			}); txErr != nil {
				return txErr
			}
		} else {
			beforeJSON = marshalAuditJSON(auditSiteSnapshot(existing))
			saved, txErr = q.UpdateSiteDraft(ctx, db.UpdateSiteDraftParams{
				ID:          existing.ID,
				Lower:       existing.Subdomain,
				DraftConfig: mergedJSON,
			})
			if txErr != nil {
				return txErr
			}
		}

		tmplID := tmpl.ID
		if txErr = q.UpdateSiteTemplateID(ctx, db.UpdateSiteTemplateIDParams{
			ID:         saved.ID,
			TemplateID: &tmplID,
		}); txErr != nil {
			return txErr
		}

		afterSnap := auditSiteSnapshot(saved)
		afterSnap["template_id"] = tmpl.ID
		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "site.apply_template",
			TargetType: "site",
			TargetID:   saved.ID,
			BeforeJSON: beforeJSON,
			AfterJSON:  marshalAuditJSON(afterSnap),
			Reason:     tmpl.ID,
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
		return nil, status.Error(codes.Internal, "failed to apply site template")
	}

	return &userv1.ApplySiteTemplateResponse{
		Site: toProtoSiteFields(
			detail.ID, detail.ShopID, detail.ShopName, detail.Subdomain,
			s.sitePrimaryHost(detail.Subdomain, detail.PrimaryHost),
			detail.Status, detail.DraftConfig,
			detail.PublishedVersionID, detail.PublishedVersionNumber, detail.PublishedAt,
			detail.SuspendedReason, detail.CreatedAt, detail.UpdatedAt,
		),
		Template: toProtoSiteTemplate(tmpl),
	}, nil
}

func (s *Server) chooseInitialSubdomain(ctx context.Context, q db.Querier, requestedSub, shopName, shopID string) (string, error) {
	if requestedSub != "" {
		if _, err := q.GetSiteBySubdomain(ctx, requestedSub); err == nil {
			return "", status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: subdomain is already in use")
		} else if !isNotFoundErr(err) {
			return "", err
		}
		if _, err := q.GetDomainByHost(ctx, requestedSub+"."+s.opts.SiteRootDomain); err == nil {
			return "", status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: domain host is already in use")
		} else if !isNotFoundErr(err) {
			return "", err
		}
		return requestedSub, nil
	}

	candidates := deriveSubdomainCandidates(shopName, shopID)
	for _, cand := range candidates {
		validSub, err := site.ValidateSubdomain(cand)
		if err != nil {
			continue
		}
		if _, err := q.GetSiteBySubdomain(ctx, validSub); err == nil {
			continue
		} else if !isNotFoundErr(err) {
			return "", err
		}
		if _, err := q.GetDomainByHost(ctx, validSub+"."+s.opts.SiteRootDomain); err == nil {
			continue
		} else if !isNotFoundErr(err) {
			return "", err
		}
		return validSub, nil
	}
	return "", status.Error(codes.AlreadyExists, "SUBDOMAIN_TAKEN: unable to allocate unique subdomain")
}

func deriveSubdomainCandidates(shopName, shopID string) []string {
	slug := slugifyVietnamese(shopName)
	hexSuffix := strings.ReplaceAll(strings.ToLower(shopID), "-", "")
	if len(hexSuffix) > 8 {
		hexSuffix = hexSuffix[:8]
	}
	if len(hexSuffix) < 4 {
		hexSuffix = "0001"
	}

	var out []string
	if len(slug) >= 3 && len(slug) <= 40 {
		out = append(out, slug)
	}
	if slug != "" {
		base := slug
		maxBase := 40 - 1 - len(hexSuffix)
		if len(base) > maxBase {
			base = strings.TrimRight(base[:maxBase], "-")
		}
		if base != "" {
			out = append(out, base+"-"+hexSuffix)
		}
	}
	out = append(out, "store-"+hexSuffix)
	return out
}

func slugifyVietnamese(s string) string {
	var b strings.Builder
	prevHyphen := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		ch := foldVietnameseRune(r)
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if len(res) > 40 {
		res = strings.TrimRight(res[:40], "-")
	}
	return res
}

func foldVietnameseRune(r rune) rune {
	switch r {
	case 'à', 'á', 'ạ', 'ả', 'ã', 'â', 'ầ', 'ấ', 'ậ', 'ẩ', 'ẫ', 'ă', 'ằ', 'ắ', 'ặ', 'ẳ', 'ẵ':
		return 'a'
	case 'è', 'é', 'ẹ', 'ẻ', 'ẽ', 'ê', 'ề', 'ế', 'ệ', 'ể', 'ễ':
		return 'e'
	case 'ì', 'í', 'ị', 'ỉ', 'ĩ':
		return 'i'
	case 'ò', 'ó', 'ọ', 'ỏ', 'õ', 'ô', 'ồ', 'ố', 'ộ', 'ổ', 'ỗ', 'ơ', 'ờ', 'ớ', 'ợ', 'ở', 'ỡ':
		return 'o'
	case 'ù', 'ú', 'ụ', 'ủ', 'ũ', 'ư', 'ừ', 'ứ', 'ự', 'ử', 'ữ':
		return 'u'
	case 'ỳ', 'ý', 'ỵ', 'ỷ', 'ỹ':
		return 'y'
	case 'đ':
		return 'd'
	default:
		return r
	}
}
