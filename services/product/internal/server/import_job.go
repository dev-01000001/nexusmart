package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/moderation"
	"github.com/nexusmart/core/services/product/internal/store"
)

const maxImportFilenameRunes = 255

var (
	utf8BOM              = []byte{0xef, 0xbb, 0xbf}
	requiredCSVColumns   = []string{"product_name", "sku_code", "category_id", "price_vnd", "stock"}
	csvHeaderAliasLookup = map[string]string{
		"name":            "product_name",
		"variant_name":    "sku_name",
		"image":           "image_url",
		"image_urls":      "images",
		"attributes_json": "attributes",
	}
)

// SetImportLimits overrides the CSV byte and row caps (defaults: 1 MiB, 1,000 rows).
func (s *Server) SetImportLimits(maxBytes, maxRows int) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	if maxBytes > 0 {
		s.importMaxBytes = maxBytes
	}
	if maxRows > 0 {
		s.importMaxRows = maxRows
	}
}

// SetImportLease overrides the background import worker lease duration (default 30s).
func (s *Server) SetImportLease(d time.Duration) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	if d > 0 {
		s.importLease = d
	}
}

// SetImportBeforeConfirmRowHook installs an optional test hook invoked before each row confirmation TX.
func (s *Server) SetImportBeforeConfirmRowHook(fn func(jobID string, rowNum int32)) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	s.importBeforeConfirmRow = fn
}

func (s *Server) getImportLimits() (int, int) {
	s.importMu.RLock()
	defer s.importMu.RUnlock()
	maxBytes := s.importMaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultProductImportMaxBytes
	}
	maxRows := s.importMaxRows
	if maxRows <= 0 {
		maxRows = DefaultProductImportMaxRows
	}
	return maxBytes, maxRows
}

func (s *Server) getImportLease() time.Duration {
	s.importMu.RLock()
	defer s.importMu.RUnlock()
	if s.importLease > 0 {
		return s.importLease
	}
	return DefaultProductImportLease
}

func (s *Server) getImportBeforeConfirmRowHook() func(jobID string, rowNum int32) {
	s.importMu.RLock()
	defer s.importMu.RUnlock()
	return s.importBeforeConfirmRow
}

func (s *Server) triggerImportWorker() {
	select {
	case s.importTrigger <- struct{}{}:
	default:
	}
}

// --- RPC Handlers (UC-S13, G6) ---

// CreateProductImportJob validates the uploaded CSV structure/limits synchronously and enqueues a
// two-step create-only import job for background validation (UC-S13, G6).
func (s *Server) CreateProductImportJob(ctx context.Context, req *productv1.CreateProductImportJobRequest) (*productv1.CreateProductImportJobResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasRole(claims, auth.RoleSeller) {
		return nil, status.Error(codes.PermissionDenied, "seller role required")
	}
	shopID := strings.TrimSpace(claims.ShopID)
	if shopID == "" {
		return nil, status.Error(codes.PermissionDenied, "seller token missing shop_id")
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	suspended, err := s.store.IsShopSuspended(ctx, shopID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check shop suspension: %v", err)
	}
	if suspended {
		return nil, status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot import products")
	}

	maxBytes, maxRows := s.getImportLimits()
	parsed, parseErr := parseImportCSV(req.GetCsvContent(), maxBytes, maxRows)
	if parseErr != nil {
		return nil, parseErr
	}

	filename := strings.TrimSpace(req.GetFilename())
	if filename == "" {
		filename = "import.csv"
	}
	if err := checkText("filename", filename, math.MaxInt); err != nil {
		return nil, err
	}
	if r := []rune(filename); len(r) > maxImportFilenameRunes {
		filename = string(r[:maxImportFilenameRunes])
	}

	var created store.ProductImportJob
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
			return lockErr
		}
		shopSuspended, suspErr := q.IsShopSuspended(ctx, shopID)
		if suspErr != nil {
			return suspErr
		}
		if shopSuspended {
			return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot import products")
		}
		active, activeErr := q.HasActiveProductImportJob(ctx, shopID, "")
		if activeErr != nil {
			return activeErr
		}
		if active {
			return status.Error(codes.FailedPrecondition, "IMPORT_JOB_IN_PROGRESS: another import job of this shop is still being processed")
		}
		j, createErr := q.CreateProductImportJob(ctx, store.ProductImportJob{
			ID:        uuid.NewString(),
			ShopID:    shopID,
			CreatedBy: claims.UserID(),
			Filename:  filename,
			RawCSV:    parsed.cleanBytes,
			Status:    store.ImportJobStatusQueued,
			TotalRows: int32(len(parsed.rows)),
			CreatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		created = j
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "create product import job: %v", err)
	}

	s.triggerImportWorker()
	return &productv1.CreateProductImportJobResponse{
		Job: toProtoImportJob(created),
	}, nil
}

// GetProductImportJob returns a product import job owned by the caller's shop (or any shop for staff with products.read).
func (s *Server) GetProductImportJob(ctx context.Context, req *productv1.GetProductImportJobRequest) (*productv1.GetProductImportJobResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	job, err := s.store.GetProductImportJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "import job not found")
		}
		return nil, status.Errorf(codes.Internal, "get import job: %v", err)
	}
	if err := authorizeImportJobRead(claims, job.ShopID); err != nil {
		return nil, err
	}
	return &productv1.GetProductImportJobResponse{
		Job: toProtoImportJob(job),
	}, nil
}

// ListProductImportRows returns paginated validation/creation rows for an import job.
func (s *Server) ListProductImportRows(ctx context.Context, req *productv1.ListProductImportRowsRequest) (*productv1.ListProductImportRowsResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	job, err := s.store.GetProductImportJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "import job not found")
		}
		return nil, status.Errorf(codes.Internal, "get import job: %v", err)
	}
	if err := authorizeImportJobRead(claims, job.ShopID); err != nil {
		return nil, err
	}

	statusFilter := ""
	if req.GetStatus() != productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_UNSPECIFIED {
		statusFilter = fromProtoImportRowStatus(req.GetStatus())
		if statusFilter == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid import row status")
		}
	}

	page := req.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxImportRowsPageSize {
		pageSize = maxImportRowsPageSize
	}
	offset64 := int64(page-1) * int64(pageSize)
	if offset64 < 0 || offset64 > math.MaxInt32 {
		return nil, status.Error(codes.InvalidArgument, "pagination offset out of range")
	}

	rows, total, err := s.store.ListProductImportRows(ctx, store.ListProductImportRowsFilter{
		JobID:      jobID,
		Status:     statusFilter,
		ErrorsOnly: req.GetErrorsOnly(),
		Limit:      pageSize,
		Offset:     int32(offset64),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list import rows: %v", err)
	}

	out := make([]*productv1.ProductImportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProtoImportRow(r))
	}
	return &productv1.ListProductImportRowsResponse{
		Rows:  out,
		Total: total,
	}, nil
}

// ConfirmProductImportJob atomically transitions a READY job (with >= 1 valid row) to CONFIRMING under FOR UPDATE.
// Repeated or concurrent clicks when already CONFIRMING/COMPLETED return already_confirmed=true without duplicating products (G6).
func (s *Server) ConfirmProductImportJob(ctx context.Context, req *productv1.ConfirmProductImportJobRequest) (*productv1.ConfirmProductImportJobResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasRole(claims, auth.RoleSeller) {
		return nil, status.Error(codes.PermissionDenied, "seller role required")
	}
	shopID := strings.TrimSpace(claims.ShopID)
	if shopID == "" {
		return nil, status.Error(codes.PermissionDenied, "seller token missing shop_id")
	}
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	var (
		resJob           store.ProductImportJob
		alreadyConfirmed bool
	)
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
			return lockErr
		}
		suspended, suspErr := q.IsShopSuspended(ctx, shopID)
		if suspErr != nil {
			return suspErr
		}
		if suspended {
			return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot confirm product import")
		}

		cur, getErr := q.GetProductImportJobByIDForUpdate(ctx, jobID)
		if getErr != nil {
			return getErr
		}
		if cur.ShopID != shopID {
			return status.Error(codes.PermissionDenied, "cannot confirm import job belonging to another shop")
		}

		switch cur.Status {
		case store.ImportJobStatusConfirming, store.ImportJobStatusCompleted:
			resJob = cur
			alreadyConfirmed = true
			return nil
		case store.ImportJobStatusReady:
			if cur.ValidRows <= 0 {
				return status.Error(codes.FailedPrecondition, "IMPORT_JOB_NOT_READY: import job has no valid rows to confirm")
			}
			active, activeErr := q.HasActiveProductImportJob(ctx, shopID, cur.ID)
			if activeErr != nil {
				return activeErr
			}
			if active {
				return status.Error(codes.FailedPrecondition, "IMPORT_JOB_IN_PROGRESS: another import job of this shop is still being processed")
			}
			upd, transitioned, trErr := q.TransitionProductImportJobConfirming(ctx, jobID)
			if trErr != nil {
				return trErr
			}
			if !transitioned {
				return status.Error(codes.FailedPrecondition, "IMPORT_JOB_NOT_READY: import job is not ready for confirmation")
			}
			resJob = upd
			return nil
		default:
			return status.Errorf(codes.FailedPrecondition, "IMPORT_JOB_NOT_READY: import job is in %s status", cur.Status)
		}
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "import job not found")
		}
		return nil, status.Errorf(codes.Internal, "confirm import job: %v", err)
	}

	if !alreadyConfirmed {
		s.triggerImportWorker()
	}
	return &productv1.ConfirmProductImportJobResponse{
		Job:              toProtoImportJob(resJob),
		AlreadyConfirmed: alreadyConfirmed,
	}, nil
}

func authorizeImportJobRead(claims *auth.Claims, jobShopID string) error {
	if auth.HasCapability(claims, auth.CapProductsRead) {
		return nil
	}
	if auth.HasRole(claims, auth.RoleSeller) && strings.TrimSpace(claims.ShopID) != "" && claims.ShopID == jobShopID {
		return nil
	}
	return status.Error(codes.PermissionDenied, "cannot access import job belonging to another shop")
}

// --- Background Import Worker (Lease + FOR UPDATE SKIP LOCKED) ---

// RunImportWorker polls and processes queued/confirming import jobs until ctx is canceled.
func (s *Server) RunImportWorker(ctx context.Context, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		for {
			if ctx.Err() != nil {
				return
			}
			worked, err := s.runImportWorkerStep(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.logger.WarnContext(ctx, "product import worker iteration failed", slog.String("error", err.Error()))
				break
			}
			if !worked {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-s.importTrigger:
		case <-ticker.C:
		}
	}
}

func (s *Server) runImportWorkerStep(ctx context.Context) (worked bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(ctx, "product import worker step panicked",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)
			worked = false
			err = fmt.Errorf("product import worker panic: %v", r)
		}
	}()
	return s.ProcessNextImportJob(ctx)
}

// ProcessNextImportJob claims at most one claimable import job (QUEUED, expired VALIDATING, or CONFIRMING)
// using FOR UPDATE SKIP LOCKED and processes it to READY or COMPLETED.
func (s *Server) ProcessNextImportJob(ctx context.Context) (bool, error) {
	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	claimToken := uuid.NewString()
	lease := s.getImportLease()

	job, ok, err := s.store.ClaimNextProductImportJob(ctx, claimToken, lease)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	return true, s.processClaimedImportJob(ctx, job, claimToken)
}

func (s *Server) processClaimedImportJob(ctx context.Context, job store.ProductImportJob, claimToken string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(ctx, "product import job panicked",
				slog.String("job_id", job.ID),
				slog.String("status", job.Status),
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)
			failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			failCtx = store.WithAsOf(failCtx, s.now())
			if _, _, failErr := s.store.MarkProductImportJobFailed(
				failCtx, job.ID, claimToken, "INTERNAL_ERROR: unexpected worker error",
			); failErr != nil {
				s.logger.ErrorContext(ctx, "failed to mark panicked import job as FAILED",
					slog.String("job_id", job.ID),
					slog.String("error", failErr.Error()),
				)
				err = fmt.Errorf("import job %s panicked (%v); mark failed: %w", job.ID, r, failErr)
				return
			}
			err = fmt.Errorf("import job %s panicked: %v", job.ID, r)
		}
	}()

	switch job.Status {
	case store.ImportJobStatusValidating:
		return s.processValidatingImportJob(ctx, job, claimToken)
	case store.ImportJobStatusConfirming:
		return s.processConfirmingImportJob(ctx, job, claimToken)
	default:
		return nil
	}
}

func (s *Server) processValidatingImportJob(ctx context.Context, job store.ProductImportJob, claimToken string) error {
	if len(job.RawCSV) == 0 {
		_, _, err := s.store.MarkProductImportJobFailed(ctx, job.ID, claimToken, "missing raw CSV payload")
		return err
	}

	maxBytes, maxRows := s.getImportLimits()
	parsed, parseErr := parseImportCSV(job.RawCSV, maxBytes, maxRows)
	if parseErr != nil {
		_, _, err := s.store.MarkProductImportJobFailed(ctx, job.ID, claimToken, parseErr.Error())
		return err
	}

	cats, err := s.store.ListCategories(ctx, "")
	if err != nil {
		return fmt.Errorf("list categories for import validation: %w", err)
	}
	catByID := make(map[string]store.Category, len(cats))
	for _, c := range cats {
		catByID[c.ID] = c
	}

	prohibitedIDs, err := s.store.ListProhibitedCategoryIDs(ctx)
	if err != nil {
		return fmt.Errorf("list prohibited categories for import validation: %w", err)
	}
	prohibitedSet := make(map[string]bool, len(prohibitedIDs))
	for _, id := range prohibitedIDs {
		prohibitedSet[id] = true
	}

	rules, err := s.loadCompiledModerationRules(ctx, s.store)
	if err != nil {
		return fmt.Errorf("load moderation rules for import validation: %w", err)
	}

	candidateSKUCodes := make([]string, 0, len(parsed.rows))
	skuSetForQuery := make(map[string]struct{}, len(parsed.rows))
	for _, r := range parsed.rows {
		code := strings.TrimSpace(r.fields["sku_code"])
		if code == "" {
			continue
		}
		if _, dup := skuSetForQuery[code]; !dup {
			skuSetForQuery[code] = struct{}{}
			candidateSKUCodes = append(candidateSKUCodes, code)
		}
	}
	existingShopSKUs, err := s.store.ListExistingShopSKUCodes(ctx, job.ShopID, candidateSKUCodes)
	if err != nil {
		return fmt.Errorf("check existing shop sku codes: %w", err)
	}

	seenSKUsInFile := make(map[string]bool, len(parsed.rows))
	validatedRows := make([]store.ProductImportRow, 0, len(parsed.rows))
	var validCount, invalidCount int32

	for _, csvRow := range parsed.rows {
		rowModel := validateImportCSVRow(job.ID, csvRow, catByID, prohibitedSet, rules, seenSKUsInFile, existingShopSKUs)
		if rowModel.Status == store.ImportRowStatusValid {
			validCount++
		} else {
			invalidCount++
		}
		validatedRows = append(validatedRows, rowModel)
	}

	return s.store.WithTx(ctx, func(q store.Queries) error {
		cur, getErr := q.GetProductImportJobByIDForUpdate(ctx, job.ID)
		if getErr != nil {
			return getErr
		}
		if cur.Status != store.ImportJobStatusValidating || cur.ClaimToken != claimToken {
			return nil
		}
		if delErr := q.DeleteProductImportRowsByJob(ctx, job.ID); delErr != nil {
			return delErr
		}
		for _, r := range validatedRows {
			if insErr := q.InsertProductImportRow(ctx, r); insErr != nil {
				return insErr
			}
		}
		_, _, markErr := q.MarkProductImportJobReady(ctx, job.ID, claimToken, int32(len(validatedRows)), validCount, invalidCount)
		return markErr
	})
}

func (s *Server) processConfirmingImportJob(ctx context.Context, job store.ProductImportJob, claimToken string) error {
	rows, err := s.store.ListValidProductImportRows(ctx, job.ID)
	if err != nil {
		return fmt.Errorf("list valid import rows: %w", err)
	}

	lease := s.getImportLease()
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if hook := s.getImportBeforeConfirmRowHook(); hook != nil {
			hook(job.ID, row.RowNumber)
		}

		leaseLost, rowErr := s.confirmSingleImportRow(ctx, job, row, claimToken, lease)
		if rowErr != nil {
			return rowErr
		}
		if leaseLost {
			return nil
		}
	}

	return s.store.WithTx(ctx, func(q store.Queries) error {
		cur, getErr := q.GetProductImportJobByIDForUpdate(ctx, job.ID)
		if getErr != nil {
			return getErr
		}
		if cur.Status != store.ImportJobStatusConfirming || cur.ClaimToken != claimToken {
			return nil
		}
		counts, cntErr := q.GetProductImportRowOutcomeCounts(ctx, job.ID)
		if cntErr != nil {
			return cntErr
		}
		if counts.RemainingValid > 0 {
			return nil
		}
		_, _, compErr := q.MarkProductImportJobCompleted(ctx, job.ID, claimToken, counts.Created, counts.Failed)
		return compErr
	})
}

func (s *Server) confirmSingleImportRow(
	ctx context.Context,
	job store.ProductImportJob,
	row store.ProductImportRow,
	claimToken string,
	lease time.Duration,
) (bool, error) {
	now := s.now()
	ctx = store.WithAsOf(ctx, now)
	var leaseLost bool

	err := s.store.WithTx(ctx, func(q store.Queries) error {
		// Consistent lock order: shop_suspension -> product_import_jobs -> product_import_rows -> products -> skus.
		if lockErr := q.LockShopSuspension(ctx, job.ShopID); lockErr != nil {
			return lockErr
		}
		curJob, getJobErr := q.GetProductImportJobByIDForUpdate(ctx, job.ID)
		if getJobErr != nil {
			return getJobErr
		}
		if curJob.Status != store.ImportJobStatusConfirming || curJob.ClaimToken != claimToken {
			leaseLost = true
			return nil
		}

		curRow, getRowErr := q.GetProductImportRowByIDForUpdate(ctx, row.ID)
		if getRowErr != nil {
			return getRowErr
		}
		if curRow.Status != store.ImportRowStatusValid || curRow.CreatedProductID != "" {
			return nil
		}

		shopSuspended, suspErr := q.IsShopSuspended(ctx, job.ShopID)
		if suspErr != nil {
			return suspErr
		}
		if shopSuspended {
			return newImportRowFailure("SHOP_SUSPENDED", "shop is suspended and cannot create products")
		}

		cat, catErr := q.GetCategoryByID(ctx, curRow.CategoryID)
		if catErr != nil {
			if errors.Is(catErr, store.ErrNotFound) {
				return newImportRowFailure("CATEGORY_NOT_FOUND", "category not found")
			}
			return catErr
		}

		// Re-check SKU uniqueness within shop inside TX to guard against concurrent imports or CreateProduct calls.
		existingSKUs, skuLookupErr := q.ListExistingShopSKUCodes(ctx, job.ShopID, []string{curRow.SKUCode})
		if skuLookupErr != nil {
			return skuLookupErr
		}
		if existingSKUs[curRow.SKUCode] {
			return newImportRowFailure("DUPLICATE_SKU_IN_SHOP", "sku_code already exists in shop")
		}

		extras, extraErr := decodeImportRowExtras(curRow.RawRowJSON)
		if extraErr != nil {
			return newImportRowFailure("INVALID_ATTRIBUTES", extraErr.Error())
		}
		validatedExplicitSlug, slugErr := validateExplicitSlug(extras.rawSlug)
		if slugErr != nil {
			return newImportRowFailure("INVALID_SLUG", status.Convert(slugErr).Message())
		}
		extras.explicitSlug = validatedExplicitSlug

		modRes, modErr := s.checkProductModeration(ctx, q, curRow.CategoryID, curRow.ProductName, extras.description, productModerationSlug{
			Name:    curRow.ProductName,
			Slug:    extras.explicitSlug,
			RawSlug: extras.rawSlug,
		}, []moderationSKU{{Name: extras.skuName, Attributes: extras.attributes}})
		if modErr != nil {
			if st, ok := status.FromError(modErr); ok && st.Code() == codes.InvalidArgument {
				errCode := "BANNED_CONTENT"
				if strings.HasPrefix(st.Message(), "PROHIBITED_CATEGORY") {
					errCode = "PROHIBITED_CATEGORY"
				}
				return newImportRowFailure(errCode, st.Message())
			}
			return modErr
		}

		prodID := uuid.NewString()
		prodSlug := extras.explicitSlug
		if prodSlug != "" {
			if _, lookupErr := q.GetProductBySlug(ctx, prodSlug); lookupErr == nil {
				return newImportRowFailure("DUPLICATE_SLUG", "product slug already exists")
			} else if !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		} else {
			base := slugify(curRow.ProductName, "prod-"+prodID[:8])
			prodSlug = base
			if _, lookupErr := q.GetProductBySlug(ctx, prodSlug); lookupErr == nil {
				prodSlug = slugWithSuffix(base, prodID[:8])
			} else if !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		}

		p, createErr := q.CreateProduct(ctx, store.Product{
			ID:              prodID,
			ShopID:          job.ShopID,
			CategoryID:      curRow.CategoryID,
			Name:            curRow.ProductName,
			Slug:            prodSlug,
			Description:     extras.description,
			Images:          extras.images,
			Status:          store.ProductStatusPending,
			MinPriceVND:     curRow.PriceVND,
			MaxPriceVND:     curRow.PriceVND,
			FlaggedKeywords: modRes.FlaggedTerms,
			ShopSuspended:   false,
		})
		if createErr != nil {
			if errors.Is(createErr, store.ErrAlreadyExists) {
				return newImportRowFailure("DUPLICATE_SLUG", "product slug or sku_code already exists")
			}
			return createErr
		}

		skuStatus := store.SKUStatusActive
		if curRow.Stock == 0 {
			skuStatus = store.SKUStatusOutOfStock
		}
		sk, skuErr := q.CreateSKU(ctx, store.SKU{
			ID:                uuid.NewString(),
			ProductID:         p.ID,
			ShopID:            job.ShopID,
			ProductName:       p.Name,
			SKUCode:           curRow.SKUCode,
			Name:              extras.skuName,
			Attributes:        extras.attributes,
			PriceVND:          curRow.PriceVND,
			MinPrice30dVND:    curRow.PriceVND,
			Stock:             curRow.Stock,
			WeightGrams:       curRow.WeightGrams,
			ImageURL:          extras.imageURL,
			CategoryID:        curRow.CategoryID,
			CommissionRateBps: cat.CommissionRateBps,
			Status:            skuStatus,
			ProductStatus:     p.Status,
			ShopSuspended:     false,
		})
		if skuErr != nil {
			return skuErr
		}
		if phErr := q.InsertSKUPriceHistory(ctx, store.SKUPriceHistoryEntry{
			ID:         uuid.NewString(),
			SKUID:      sk.ID,
			PriceVND:   sk.PriceVND,
			ActorID:    job.CreatedBy,
			Reason:     "csv import create sku",
			RecordedAt: now,
		}); phErr != nil {
			return phErr
		}

		updated, updErr := q.UpdateProductImportRowOutcome(ctx, curRow.ID, store.ImportRowStatusCreated, "", "", p.ID)
		if updErr != nil {
			return updErr
		}
		if updated {
			ok, progErr := q.UpdateProductImportJobProgress(ctx, job.ID, claimToken, 1, 0, lease)
			if progErr != nil {
				return progErr
			}
			if !ok {
				leaseLost = true
			}
		}
		return nil
	})
	if err == nil {
		return leaseLost, nil
	}

	// A row that can never be saved must not be retried forever: the row TX rolled back, so record the failure in
	// its own TX and move on. Infrastructure errors are returned and the job is retried once its lease expires.
	var failure *importRowFailure
	if !errors.As(err, &failure) {
		if !store.IsPermanentDataError(err) {
			return false, err
		}
		s.logger.ErrorContext(ctx, "product import row rejected by the database",
			slog.String("job_id", job.ID), slog.Int("row_number", int(row.RowNumber)), slog.String("error", err.Error()))
		failure = &importRowFailure{code: "INTERNAL_ERROR", msg: "row could not be saved"}
	}
	return s.failImportRow(ctx, job.ID, row.ID, claimToken, lease, failure)
}

// importRowFailure is a permanent, seller-visible reason why one import row cannot be created.
type importRowFailure struct{ code, msg string }

func newImportRowFailure(code, msg string) error { return &importRowFailure{code: code, msg: msg} }

func (f *importRowFailure) Error() string { return f.code + ": " + f.msg }

func (s *Server) failImportRow(
	ctx context.Context,
	jobID, rowID, claimToken string,
	lease time.Duration,
	failure *importRowFailure,
) (bool, error) {
	var leaseLost bool
	err := s.store.WithTx(ctx, func(q store.Queries) error {
		curJob, getErr := q.GetProductImportJobByIDForUpdate(ctx, jobID)
		if getErr != nil {
			return getErr
		}
		if curJob.Status != store.ImportJobStatusConfirming || curJob.ClaimToken != claimToken {
			leaseLost = true
			return nil
		}
		updated, updErr := q.UpdateProductImportRowOutcome(ctx, rowID, store.ImportRowStatusFailed, failure.code, failure.msg, "")
		if updErr != nil || !updated {
			return updErr
		}
		ok, progErr := q.UpdateProductImportJobProgress(ctx, jobID, claimToken, 0, 1, lease)
		leaseLost = !ok
		return progErr
	})
	return leaseLost, err
}

// --- CSV Parsing & Row Validation Helpers ---

type parsedCSVFile struct {
	cleanBytes []byte
	rows       []parsedCSVRow
}

type parsedCSVRow struct {
	rowNumber int32
	fields    map[string]string
}

func parseImportCSV(raw []byte, maxBytes, maxRows int) (*parsedCSVFile, error) {
	if len(raw) == 0 {
		return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_HEADER: csv_content is required")
	}
	if len(raw) > maxBytes {
		return nil, status.Errorf(codes.InvalidArgument, "IMPORT_FILE_TOO_LARGE: CSV file exceeds maximum size of %d bytes", maxBytes)
	}
	if !utf8.Valid(raw) {
		return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_UTF8: CSV file must be valid UTF-8")
	}
	clean := bytes.TrimPrefix(raw, utf8BOM)
	if bytes.IndexByte(clean, 0) >= 0 {
		return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_UTF8: CSV file contains invalid NUL byte")
	}

	reader := csv.NewReader(bytes.NewReader(clean))
	reader.FieldsPerRecord = 0
	reader.LazyQuotes = false

	headerRec, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_HEADER: CSV file is empty")
		}
		return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_UTF8: malformed CSV header: %v", err)
	}
	if len(headerRec) == 0 {
		return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_HEADER: CSV header is empty")
	}

	headers := make([]string, len(headerRec))
	seenHeaders := make(map[string]bool, len(headerRec))
	for i, col := range headerRec {
		norm := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(col, "\ufeff")))
		if norm == "" {
			return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_HEADER: empty column header at index %d", i)
		}
		if alias, ok := csvHeaderAliasLookup[norm]; ok {
			norm = alias
		}
		if seenHeaders[norm] {
			return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_HEADER: duplicate column header %q", norm)
		}
		seenHeaders[norm] = true
		headers[i] = norm
	}

	var missing []string
	for _, reqCol := range requiredCSVColumns {
		if !seenHeaders[reqCol] {
			missing = append(missing, reqCol)
		}
	}
	if len(missing) > 0 {
		return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_HEADER: missing required columns: %s", strings.Join(missing, ", "))
	}

	rows := make([]parsedCSVRow, 0)
	var rowNum int32
	for {
		rec, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if errors.Is(readErr, csv.ErrFieldCount) {
				return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_HEADER: row %d has mismatched column count", rowNum+1)
			}
			return nil, status.Errorf(codes.InvalidArgument, "IMPORT_INVALID_UTF8: malformed CSV at row %d: %v", rowNum+1, readErr)
		}
		if isBlankCSVRecord(rec) {
			continue
		}
		rowNum++
		if int(rowNum) > maxRows {
			return nil, status.Errorf(codes.InvalidArgument, "IMPORT_TOO_MANY_ROWS: CSV exceeds maximum of %d data rows", maxRows)
		}
		fields := make(map[string]string, len(headers))
		for i, h := range headers {
			fields[h] = rec[i]
		}
		rows = append(rows, parsedCSVRow{
			rowNumber: rowNum,
			fields:    fields,
		})
	}

	if len(rows) == 0 {
		return nil, status.Error(codes.InvalidArgument, "IMPORT_INVALID_HEADER: CSV file contains no data rows")
	}

	cleanCopy := append([]byte(nil), clean...)
	return &parsedCSVFile{
		cleanBytes: cleanCopy,
		rows:       rows,
	}, nil
}

func isBlankCSVRecord(rec []string) bool {
	for _, cell := range rec {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func validateImportCSVRow(
	jobID string,
	csvRow parsedCSVRow,
	catByID map[string]store.Category,
	prohibitedSet map[string]bool,
	rules []moderation.CompiledRule,
	seenSKUsInFile map[string]bool,
	existingShopSKUs map[string]bool,
) store.ProductImportRow {
	rawJSON, _ := json.Marshal(csvRow.fields)
	if len(rawJSON) == 0 {
		rawJSON = []byte("{}")
	}

	productName := strings.TrimSpace(csvRow.fields["product_name"])
	skuCode := strings.TrimSpace(csvRow.fields["sku_code"])
	categoryID := strings.TrimSpace(csvRow.fields["category_id"])
	rawPrice := strings.TrimSpace(csvRow.fields["price_vnd"])
	rawStock := strings.TrimSpace(csvRow.fields["stock"])
	rawWeight := strings.TrimSpace(csvRow.fields["weight_grams"])
	desc := strings.TrimSpace(csvRow.fields["description"])
	rawSlug := strings.TrimSpace(csvRow.fields["slug"])

	var (
		firstErrCode string
		errMsgs      []string
		priceVND     int64
		stock        int32
		weightGrams  int32
	)
	addErr := func(code, msg string) {
		if firstErrCode == "" {
			firstErrCode = code
		}
		errMsgs = append(errMsgs, msg)
	}

	if productName == "" {
		addErr("INVALID_PRODUCT_NAME", "product_name is required")
	}

	if skuCode == "" {
		addErr("INVALID_SKU_CODE", "sku_code is required")
	} else {
		if seenSKUsInFile[skuCode] {
			addErr("DUPLICATE_SKU_IN_FILE", fmt.Sprintf("duplicate sku_code %q in CSV file", skuCode))
		} else {
			seenSKUsInFile[skuCode] = true
		}
		if existingShopSKUs[skuCode] {
			addErr("DUPLICATE_SKU_IN_SHOP", fmt.Sprintf("sku_code %q already exists in shop", skuCode))
		}
	}

	if categoryID == "" {
		addErr("INVALID_CATEGORY", "category_id is required")
	} else if _, exists := catByID[categoryID]; !exists {
		addErr("CATEGORY_NOT_FOUND", fmt.Sprintf("category %q not found", categoryID))
	} else if prohibitedSet[categoryID] {
		addErr("PROHIBITED_CATEGORY", "products cannot be listed in a prohibited category")
	}

	if pVal, err := strconv.ParseInt(rawPrice, 10, 64); err != nil {
		addErr("INVALID_PRICE", "price_vnd must be a positive integer")
	} else {
		priceVND = pVal
		if pVal <= 0 {
			addErr("INVALID_PRICE", "price_vnd must be > 0")
		}
	}

	if sVal, err := strconv.ParseInt(rawStock, 10, 32); err != nil {
		addErr("INVALID_STOCK", "stock must be an integer >= 0")
	} else {
		stock = int32(sVal)
		if sVal < 0 {
			addErr("INVALID_STOCK", "stock must be >= 0")
		}
	}

	if rawWeight != "" {
		if wVal, err := strconv.ParseInt(rawWeight, 10, 32); err != nil {
			addErr("INVALID_WEIGHT", "weight_grams must be an integer >= 0")
		} else {
			weightGrams = int32(wVal)
			if wVal < 0 {
				addErr("INVALID_WEIGHT", "weight_grams must be >= 0")
			}
		}
	}

	explicitSlug, slugErr := validateExplicitSlug(rawSlug)
	if slugErr != nil {
		addErr("INVALID_SLUG", status.Convert(slugErr).Message())
	}

	extras, extraErr := decodeImportRowExtrasFromMap(csvRow.fields)
	if extraErr != nil {
		addErr("INVALID_ATTRIBUTES", extraErr.Error())
	} else if err := validateProductText(productName, desc, extras.images); err != nil {
		addErr("INVALID_PRODUCT_TEXT", status.Convert(err).Message())
	} else if err := validateSKUText(skuCode, extras.skuName, extras.imageURL, extras.attributes); err != nil {
		addErr("INVALID_SKU_TEXT", status.Convert(err).Message())
	} else if len(rules) > 0 {
		modRes := moderation.MatchCompiled(rules, collectProductModerationFields(productName, desc, productModerationSlug{
			Name:    productName,
			Slug:    explicitSlug,
			RawSlug: rawSlug,
		}, []moderationSKU{{Name: extras.skuName, Attributes: extras.attributes}})...)
		if modRes.Blocked {
			addErr("BANNED_CONTENT", fmt.Sprintf("product contains banned keywords: %s", strings.Join(modRes.BlockedTerms, ", ")))
		}
	}

	rowStatus := store.ImportRowStatusValid
	if firstErrCode != "" {
		rowStatus = store.ImportRowStatusInvalid
	}

	return store.ProductImportRow{
		ID:           uuid.NewString(),
		JobID:        jobID,
		RowNumber:    csvRow.rowNumber,
		Status:       rowStatus,
		ErrorCode:    firstErrCode,
		ErrorMessage: strings.Join(errMsgs, "; "),
		ProductName:  productName,
		SKUCode:      skuCode,
		CategoryID:   categoryID,
		PriceVND:     priceVND,
		Stock:        stock,
		WeightGrams:  weightGrams,
		RawRowJSON:   rawJSON,
	}
}

type importRowExtras struct {
	description  string
	skuName      string
	rawSlug      string
	explicitSlug string
	imageURL     string
	images       []string
	attributes   map[string]string
}

func decodeImportRowExtras(rawJSON []byte) (importRowExtras, error) {
	fields := map[string]string{}
	if len(rawJSON) > 0 {
		if err := json.Unmarshal(rawJSON, &fields); err != nil {
			return importRowExtras{}, fmt.Errorf("invalid raw_row_json: %w", err)
		}
		if fields == nil {
			fields = map[string]string{}
		}
	}
	return decodeImportRowExtrasFromMap(fields)
}

func decodeImportRowExtrasFromMap(fields map[string]string) (importRowExtras, error) {
	skuName := strings.TrimSpace(fields["sku_name"])
	if skuName == "" {
		skuName = "Default"
	}
	rawSlug := strings.TrimSpace(fields["slug"])
	explicitSlug := ""
	if rawSlug != "" {
		explicitSlug = normalizeSlug(rawSlug)
	}

	attrs := map[string]string{}
	if rawAttrs := strings.TrimSpace(fields["attributes"]); rawAttrs != "" {
		if err := json.Unmarshal([]byte(rawAttrs), &attrs); err != nil {
			return importRowExtras{}, fmt.Errorf("attributes must be a valid JSON object")
		}
		if attrs == nil {
			attrs = map[string]string{}
		}
	}
	if color := strings.TrimSpace(fields["color"]); color != "" {
		attrs["color"] = color
	}
	if size := strings.TrimSpace(fields["size"]); size != "" {
		attrs["size"] = size
	}

	imageURL := strings.TrimSpace(fields["image_url"])
	var images []string
	if rawImages := strings.TrimSpace(fields["images"]); rawImages != "" {
		for _, part := range strings.Split(rawImages, "|") {
			if u := strings.TrimSpace(part); u != "" {
				images = append(images, u)
			}
		}
	}
	if imageURL != "" && len(images) == 0 {
		images = []string{imageURL}
	} else if imageURL == "" && len(images) > 0 {
		imageURL = images[0]
	}
	if images == nil {
		images = []string{}
	}

	return importRowExtras{
		description:  strings.TrimSpace(fields["description"]),
		skuName:      skuName,
		rawSlug:      rawSlug,
		explicitSlug: explicitSlug,
		imageURL:     imageURL,
		images:       images,
		attributes:   attrs,
	}, nil
}

func toProtoImportJob(j store.ProductImportJob) *productv1.ProductImportJob {
	msg := &productv1.ProductImportJob{
		Id:           j.ID,
		ShopId:       j.ShopID,
		CreatedBy:    j.CreatedBy,
		Filename:     j.Filename,
		Status:       toProtoImportJobStatus(j.Status),
		TotalRows:    j.TotalRows,
		ValidRows:    j.ValidRows,
		InvalidRows:  j.InvalidRows,
		CreatedRows:  j.CreatedRows,
		FailedRows:   j.FailedRows,
		ErrorMessage: j.ErrorMessage,
		CreatedAt:    timestamppb.New(j.CreatedAt),
		UpdatedAt:    timestamppb.New(j.UpdatedAt),
	}
	if j.ConfirmedAt != nil && !j.ConfirmedAt.IsZero() {
		msg.ConfirmedAt = timestamppb.New(*j.ConfirmedAt)
	}
	if j.FinishedAt != nil && !j.FinishedAt.IsZero() {
		msg.FinishedAt = timestamppb.New(*j.FinishedAt)
	}
	return msg
}

func toProtoImportRow(r store.ProductImportRow) *productv1.ProductImportRow {
	return &productv1.ProductImportRow{
		Id:               r.ID,
		JobId:            r.JobID,
		RowNumber:        r.RowNumber,
		Status:           toProtoImportRowStatus(r.Status),
		ErrorCode:        r.ErrorCode,
		ErrorMessage:     r.ErrorMessage,
		ProductName:      r.ProductName,
		SkuCode:          r.SKUCode,
		CategoryId:       r.CategoryID,
		PriceVnd:         r.PriceVND,
		Stock:            r.Stock,
		WeightGrams:      r.WeightGrams,
		CreatedProductId: r.CreatedProductID,
		RawRowJson:       string(r.RawRowJSON),
	}
}

func toProtoImportJobStatus(st string) productv1.ProductImportJobStatus {
	switch st {
	case store.ImportJobStatusQueued:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_QUEUED
	case store.ImportJobStatusValidating:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_VALIDATING
	case store.ImportJobStatusReady:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY
	case store.ImportJobStatusConfirming:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_CONFIRMING
	case store.ImportJobStatusCompleted:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED
	case store.ImportJobStatusFailed:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_FAILED
	default:
		return productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_UNSPECIFIED
	}
}

func toProtoImportRowStatus(st string) productv1.ProductImportRowStatus {
	switch st {
	case store.ImportRowStatusPending:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_PENDING
	case store.ImportRowStatusValid:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_VALID
	case store.ImportRowStatusInvalid:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID
	case store.ImportRowStatusCreated:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_CREATED
	case store.ImportRowStatusFailed:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED
	default:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_UNSPECIFIED
	}
}

func fromProtoImportRowStatus(st productv1.ProductImportRowStatus) string {
	switch st {
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_PENDING:
		return store.ImportRowStatusPending
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_VALID:
		return store.ImportRowStatusValid
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID:
		return store.ImportRowStatusInvalid
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_CREATED:
		return store.ImportRowStatusCreated
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED:
		return store.ImportRowStatusFailed
	default:
		return ""
	}
}
