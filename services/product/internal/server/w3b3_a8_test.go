package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestW3B3_A8_ListStockHistory(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	baseTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	now := baseTime
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	modCtx := ctxWithRole("mod-1", auth.RoleModerator, "")
	acctCtx := ctxWithRole("acct-1", auth.RoleAccountant, "")
	buyerCtx := ctxWithRole("buyer-1", auth.RoleBuyer, "")
	sellerACtx := ctxWithRole("seller-a", auth.RoleSeller, "shop-a")
	sellerBCtx := ctxWithRole("seller-b", auth.RoleSeller, "shop-b")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Apparel", Slug: "apparel-hist", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	prodAResp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo Thun Shop A",
		Skus: []*productv1.SKUInput{
			{SkuCode: "A-SKU-1", Name: "Size M", PriceVnd: 150000, Stock: 20},
			{SkuCode: "A-SKU-2", Name: "Size L", PriceVnd: 160000, Stock: 10},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct A: %v", err)
	}
	prodAID := prodAResp.GetProduct().GetId()
	skuA1 := prodAResp.GetProduct().GetSkus()[0]
	skuA2 := prodAResp.GetProduct().GetSkus()[1]
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodAID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve A: %v", err)
	}

	prodBResp, err := srv.CreateProduct(sellerBCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Quần Shop B",
		Skus:       []*productv1.SKUInput{{SkuCode: "B-SKU-1", Name: "Default", PriceVnd: 200000, Stock: 15}},
	})
	if err != nil {
		t.Fatalf("CreateProduct B: %v", err)
	}
	prodBID := prodBResp.GetProduct().GetId()
	skuB1 := prodBResp.GetProduct().GetSkus()[0]

	// Record 4 stock events for shop-a at distinct timestamps + 1 for shop-b.
	now = baseTime.Add(1 * time.Minute)
	if _, err := srv.UpdateStock(sellerACtx, &productv1.UpdateStockRequest{
		SkuId: skuA1.GetId(), NewStock: 25, ExpectedVersion: skuA1.GetVersion(),
	}); err != nil {
		t.Fatalf("UpdateStock A1: %v", err)
	}

	now = baseTime.Add(2 * time.Minute)
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-hist-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuA1.GetId(), Quantity: 3}},
	}); err != nil {
		t.Fatalf("ReserveStock A1: %v", err)
	}

	now = baseTime.Add(3 * time.Minute)
	if _, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{
		OrderId: "ord-hist-1",
	}); err != nil {
		t.Fatalf("ConfirmStock A1: %v", err)
	}

	now = baseTime.Add(4 * time.Minute)
	if _, err := srv.UpdateStock(sellerACtx, &productv1.UpdateStockRequest{
		SkuId: skuA2.GetId(), NewStock: 12, ExpectedVersion: skuA2.GetVersion(),
	}); err != nil {
		t.Fatalf("UpdateStock A2: %v", err)
	}

	now = baseTime.Add(5 * time.Minute)
	if _, err := srv.UpdateStock(sellerBCtx, &productv1.UpdateStockRequest{
		SkuId: skuB1.GetId(), NewStock: 30, ExpectedVersion: skuB1.GetVersion(),
	}); err != nil {
		t.Fatalf("UpdateStock B1: %v", err)
	}

	// 1. Seller A lists own history (all 4 entries, ordered created_at DESC, id DESC) with pagination.
	page1, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{
		Page: 1, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListStockHistory seller A page 1: %v", err)
	}
	if page1.GetTotal() != 4 || len(page1.GetItems()) != 2 {
		t.Fatalf("seller A page 1: total=%d len=%d, want 4 and 2", page1.GetTotal(), len(page1.GetItems()))
	}
	if page1.GetItems()[0].GetSkuId() != skuA2.GetId() || page1.GetItems()[0].GetChangeType() != "MANUAL_UPDATE" ||
		page1.GetItems()[0].GetProductName() != "Áo Thun Shop A" || page1.GetItems()[0].GetSkuCode() != "A-SKU-2" {
		t.Fatalf("unexpected newest history item: %+v", page1.GetItems()[0])
	}
	if page1.GetItems()[1].GetSkuId() != skuA1.GetId() || page1.GetItems()[1].GetChangeType() != "CONFIRM" ||
		page1.GetItems()[1].GetDelta() != -3 {
		t.Fatalf("unexpected second history item: %+v", page1.GetItems()[1])
	}

	page2, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{
		Page: 2, PageSize: 2,
	})
	if err != nil || page2.GetTotal() != 4 || len(page2.GetItems()) != 2 {
		t.Fatalf("seller A page 2: resp=%+v err=%v", page2, err)
	}
	if page2.GetItems()[0].GetChangeType() != "RESERVE" || page2.GetItems()[1].GetChangeType() != "MANUAL_UPDATE" {
		t.Fatalf("unexpected page 2 order: %+v", page2.GetItems())
	}

	// 2. Filter by sku_id, change_type, and time window.
	filtered, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{
		ProductId:  prodAID,
		SkuId:      skuA1.GetId(),
		ChangeType: "confirm",
		FromTime:   timestamppb.New(baseTime.Add(2 * time.Minute)),
		ToTime:     timestamppb.New(baseTime.Add(4 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("ListStockHistory filtered: %v", err)
	}
	if filtered.GetTotal() != 1 || len(filtered.GetItems()) != 1 || filtered.GetItems()[0].GetOrderId() != "ord-hist-1" {
		t.Fatalf("filtered history: total=%d items=%+v", filtered.GetTotal(), filtered.GetItems())
	}

	// 3. Attack: Seller B querying Seller A's sku_id / product_id / shop_id -> PermissionDenied.
	if _, err := srv.ListStockHistory(sellerBCtx, &productv1.ListStockHistoryRequest{SkuId: skuA1.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B querying seller A sku_id: want PermissionDenied, got %v", err)
	}
	if _, err := srv.ListStockHistory(sellerBCtx, &productv1.ListStockHistoryRequest{ProductId: prodAID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B querying seller A product_id: want PermissionDenied, got %v", err)
	}
	if _, err := srv.ListStockHistory(sellerBCtx, &productv1.ListStockHistoryRequest{ShopId: "shop-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B querying seller A shop_id: want PermissionDenied, got %v", err)
	}

	// 4. RBAC: anonymous -> Unauthenticated; buyer & accountant (no products.read) -> PermissionDenied;
	// moderator (has products.read) -> allowed and can filter across shops.
	if _, err := srv.ListStockHistory(context.Background(), &productv1.ListStockHistoryRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListStockHistory: want Unauthenticated, got %v", err)
	}
	if _, err := srv.ListStockHistory(buyerCtx, &productv1.ListStockHistoryRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListStockHistory: want PermissionDenied, got %v", err)
	}
	if _, err := srv.ListStockHistory(acctCtx, &productv1.ListStockHistoryRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("accountant ListStockHistory: want PermissionDenied, got %v", err)
	}
	modResp, err := srv.ListStockHistory(modCtx, &productv1.ListStockHistoryRequest{ShopId: "shop-b"})
	if err != nil || modResp.GetTotal() != 1 || modResp.GetItems()[0].GetProductId() != prodBID {
		t.Fatalf("moderator ListStockHistory shop-b: resp=%+v err=%v", modResp, err)
	}

	// 5. Validation errors: unknown change_type, inverted time range.
	if _, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{ChangeType: "BOGUS"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid change_type: want InvalidArgument, got %v", err)
	}
	if _, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{
		FromTime: timestamppb.New(baseTime.Add(10 * time.Minute)),
		ToTime:   timestamppb.New(baseTime.Add(1 * time.Minute)),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("inverted time window: want InvalidArgument, got %v", err)
	}
}

func TestW3B3_A8_CSVImport_WholeFileRejection(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	sellerCtx := ctxWithRole("seller-a", auth.RoleSeller, "shop-a")
	buyerCtx := ctxWithRole("buyer-1", auth.RoleBuyer, "")
	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")

	// Non-seller rejected.
	if _, err := srv.CreateProductImportJob(buyerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "a.csv", CsvContent: []byte("product_name,sku_code,category_id,price_vnd,stock\nA,S1,c1,1000,1\n"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreateProductImportJob: want PermissionDenied, got %v", err)
	}
	if _, err := srv.CreateProductImportJob(adminCtx, &productv1.CreateProductImportJobRequest{
		Filename: "a.csv", CsvContent: []byte("product_name,sku_code,category_id,price_vnd,stock\nA,S1,c1,1000,1\n"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin CreateProductImportJob: want PermissionDenied, got %v", err)
	}

	// Invalid UTF-8 -> IMPORT_INVALID_UTF8.
	_, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "badutf8.csv",
		CsvContent: []byte{0xff, 0xfe, 0x00, 0x01},
	})
	if status.Code(err) != codes.InvalidArgument || !containsStr(err.Error(), "IMPORT_INVALID_UTF8") {
		t.Fatalf("invalid utf8: want InvalidArgument IMPORT_INVALID_UTF8, got %v", err)
	}

	// Bad header -> IMPORT_INVALID_HEADER.
	_, err = srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "badhdr.csv",
		CsvContent: []byte("foo,bar,baz\n1,2,3\n"),
	})
	if status.Code(err) != codes.InvalidArgument || !containsStr(err.Error(), "IMPORT_INVALID_HEADER") {
		t.Fatalf("bad header: want InvalidArgument IMPORT_INVALID_HEADER, got %v", err)
	}

	// Header-only (0 rows) -> IMPORT_INVALID_HEADER.
	_, err = srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "emptyrows.csv",
		CsvContent: []byte("product_name,sku_code,category_id,price_vnd,stock\n"),
	})
	if status.Code(err) != codes.InvalidArgument || !containsStr(err.Error(), "IMPORT_INVALID_HEADER") {
		t.Fatalf("header only: want InvalidArgument IMPORT_INVALID_HEADER, got %v", err)
	}

	// 1,001 rows -> IMPORT_TOO_MANY_ROWS.
	var sb []byte
	sb = append(sb, "product_name,sku_code,category_id,price_vnd,stock\n"...)
	for i := 1; i <= 1001; i++ {
		sb = append(sb, "P,S,c,1000,1\n"...)
	}
	_, err = srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "toomany.csv",
		CsvContent: sb,
	})
	if status.Code(err) != codes.InvalidArgument || !containsStr(err.Error(), "IMPORT_TOO_MANY_ROWS") {
		t.Fatalf("1001 rows: want InvalidArgument IMPORT_TOO_MANY_ROWS, got %v", err)
	}

	// > 1 MiB -> IMPORT_FILE_TOO_LARGE.
	oversized := make([]byte, (1<<20)+10)
	for i := range oversized {
		oversized[i] = 'a'
	}
	_, err = srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "big.csv",
		CsvContent: oversized,
	})
	if status.Code(err) != codes.InvalidArgument || !containsStr(err.Error(), "IMPORT_FILE_TOO_LARGE") {
		t.Fatalf("> 1 MiB: want InvalidArgument IMPORT_FILE_TOO_LARGE, got %v", err)
	}

	// Suspended shop -> SHOP_SUSPENDED.
	if _, _, err := st.UpsertShopSuspensionState(context.Background(), "shop-susp", true, 1); err != nil {
		t.Fatalf("suspend shop: %v", err)
	}
	suspCtx := ctxWithRole("seller-susp", auth.RoleSeller, "shop-susp")
	_, err = srv.CreateProductImportJob(suspCtx, &productv1.CreateProductImportJobRequest{
		Filename:   "ok.csv",
		CsvContent: []byte("product_name,sku_code,category_id,price_vnd,stock\nA,S1,c1,1000,1\n"),
	})
	if status.Code(err) != codes.FailedPrecondition || !containsStr(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("suspended shop import: want FailedPrecondition SHOP_SUSPENDED, got %v", err)
	}
}

func TestW3B3_A8_CSVImport_TwoStepValidationModerationAndSecurity(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	baseTime := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return baseTime })

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerACtx := ctxWithRole("seller-a", auth.RoleSeller, "shop-a")
	sellerBCtx := ctxWithRole("seller-b", auth.RoleSeller, "shop-b")

	// Categories: clean cat, prohibited root + prohibited child.
	cleanCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Clean Fashion", Slug: "clean-fashion", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory clean: %v", err)
	}
	cleanCatID := cleanCat.GetCategory().GetId()

	prohibParent, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Restricted Root", Slug: "restricted-root", CommissionRateBps: 500, IsProhibited: true,
	})
	if err != nil {
		t.Fatalf("CreateCategory prohib parent: %v", err)
	}
	prohibChild, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Restricted Child", Slug: "restricted-child", ParentId: prohibParent.GetCategory().GetId(), CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory prohib child: %v", err)
	}

	// Banned keywords: BLOCK ("ma túy") and REVIEW ("hàng xách tay").
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "ma túy", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword BLOCK: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "hàng xách tay", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword REVIEW: %v", err)
	}

	// Existing SKU in shop-a ("EXIST-A") and shop-b ("EXIST-B").
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: cleanCatID, Name: "Existing A",
		Skus: []*productv1.SKUInput{{SkuCode: "EXIST-A", PriceVnd: 100000, Stock: 5}},
	}); err != nil {
		t.Fatalf("CreateProduct existing A: %v", err)
	}
	if _, err := srv.CreateProduct(sellerBCtx, &productv1.CreateProductRequest{
		CategoryId: cleanCatID, Name: "Existing B",
		Skus: []*productv1.SKUInput{{SkuCode: "EXIST-B", PriceVnd: 100000, Stock: 5}},
	}); err != nil {
		t.Fatalf("CreateProduct existing B: %v", err)
	}

	// UTF-8 BOM + 11 data rows (3 valid: clean, REVIEW-flagged, cross-shop SKU reuse; 8 invalid).
	csvBody := "\xef\xbb\xbfproduct_name,sku_code,category_id,price_vnd,stock,weight_grams,description,color,size\n" +
		"Áo Sơ Mi Linen,IMP-OK-1," + cleanCatID + ",250000,10,300,Vải thoáng mát,Trắng,M\n" +
		"Nước Hoa hàng xách tay,IMP-REV-2," + cleanCatID + ",500000,5,200,Chính hãng,Đen,L\n" +
		"Quần Jean Nam,EXIST-B," + cleanCatID + ",350000,8,450,Form slim,Xanh,32\n" +
		"Sản phẩm ma túy cấm,IMP-BLK-4," + cleanCatID + ",100000,2,100,Mô tả,,\n" +
		"Hàng trong danh mục cấm,IMP-PRH-5," + prohibChild.GetCategory().GetId() + ",100000,2,100,Mô tả,,\n" +
		"Danh mục lạ,IMP-NOCAT-6,cat-missing,100000,2,100,Mô tả,,\n" +
		"=CMD|' /C calc'!A0,IMP-NEG-7," + cleanCatID + ",-5000,2,100,Giá âm,,\n" +
		"Tồn âm,IMP-STK-8," + cleanCatID + ",100000,-1,100,Tồn âm,,\n" +
		"Trùng SKU file,IMP-OK-1," + cleanCatID + ",100000,2,100,Trùng trong file,,\n" +
		"Trùng SKU DB,EXIST-A," + cleanCatID + ",100000,2,100,Trùng trong DB,,\n" +
		"Cân nặng âm,IMP-WT-11," + cleanCatID + ",100000,2,-50,Cân nặng âm,,\n"

	createResp, err := srv.CreateProductImportJob(sellerACtx, &productv1.CreateProductImportJobRequest{
		Filename:   "batch.csv",
		CsvContent: []byte(csvBody),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob: %v", err)
	}
	jobID := createResp.GetJob().GetId()
	if createResp.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_QUEUED || createResp.GetJob().GetTotalRows() != 11 {
		t.Fatalf("unexpected initial job: %+v", createResp.GetJob())
	}

	// Confirm while still QUEUED -> FailedPrecondition IMPORT_JOB_NOT_READY.
	if _, err := srv.ConfirmProductImportJob(sellerACtx, &productv1.ConfirmProductImportJobRequest{JobId: jobID}); status.Code(err) != codes.FailedPrecondition || !containsStr(err.Error(), "IMPORT_JOB_NOT_READY") {
		t.Fatalf("confirm while QUEUED: want FailedPrecondition IMPORT_JOB_NOT_READY, got %v", err)
	}

	// Seller B cannot read/list/confirm Seller A's job.
	if _, err := srv.GetProductImportJob(sellerBCtx, &productv1.GetProductImportJobRequest{JobId: jobID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B GetProductImportJob: want PermissionDenied, got %v", err)
	}
	if _, err := srv.ListProductImportRows(sellerBCtx, &productv1.ListProductImportRowsRequest{JobId: jobID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B ListProductImportRows: want PermissionDenied, got %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerBCtx, &productv1.ConfirmProductImportJobRequest{JobId: jobID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B ConfirmProductImportJob: want PermissionDenied, got %v", err)
	}

	// Run background worker Step 1 (validate -> READY).
	worked, err := srv.ProcessNextImportJob(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessNextImportJob step 1: worked=%v err=%v", worked, err)
	}

	readyResp, err := srv.GetProductImportJob(sellerACtx, &productv1.GetProductImportJobRequest{JobId: jobID})
	if err != nil {
		t.Fatalf("GetProductImportJob ready: %v", err)
	}
	if readyResp.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY ||
		readyResp.GetJob().GetValidRows() != 3 || readyResp.GetJob().GetInvalidRows() != 8 {
		t.Fatalf("unexpected READY job: %+v", readyResp.GetJob())
	}

	// List errors only -> exactly 8 rows, raw_row_json preserves formula string.
	errRows, err := srv.ListProductImportRows(sellerACtx, &productv1.ListProductImportRowsRequest{
		JobId: jobID, ErrorsOnly: true, Page: 1, PageSize: 50,
	})
	if err != nil || errRows.GetTotal() != 8 || len(errRows.GetRows()) != 8 {
		t.Fatalf("ListProductImportRows errors_only: total=%d len=%d err=%v", errRows.GetTotal(), len(errRows.GetRows()), err)
	}
	var sawBlock, sawProhib, sawFormula bool
	for _, r := range errRows.GetRows() {
		if r.GetErrorCode() == "BANNED_CONTENT" {
			sawBlock = true
		}
		if r.GetErrorCode() == "PROHIBITED_CATEGORY" {
			sawProhib = true
		}
		if r.GetRowNumber() == 7 && containsStr(r.GetRawRowJson(), "=CMD|") {
			sawFormula = true
		}
	}
	if !sawBlock || !sawProhib || !sawFormula {
		t.Fatalf("missing expected error details: sawBlock=%v sawProhib=%v sawFormula=%v", sawBlock, sawProhib, sawFormula)
	}

	// Step 2: Confirm job -> CONFIRMING -> worker creates 3 PENDING products -> COMPLETED.
	conf1, err := srv.ConfirmProductImportJob(sellerACtx, &productv1.ConfirmProductImportJobRequest{JobId: jobID})
	if err != nil || conf1.GetAlreadyConfirmed() || conf1.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_CONFIRMING {
		t.Fatalf("ConfirmProductImportJob #1: resp=%+v err=%v", conf1, err)
	}
	conf2, err := srv.ConfirmProductImportJob(sellerACtx, &productv1.ConfirmProductImportJobRequest{JobId: jobID})
	if err != nil || !conf2.GetAlreadyConfirmed() {
		t.Fatalf("ConfirmProductImportJob #2: want already_confirmed=true, got resp=%+v err=%v", conf2, err)
	}

	worked, err = srv.ProcessNextImportJob(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessNextImportJob step 2: worked=%v err=%v", worked, err)
	}

	doneResp, err := srv.GetProductImportJob(sellerACtx, &productv1.GetProductImportJobRequest{JobId: jobID})
	if err != nil {
		t.Fatalf("GetProductImportJob completed: %v", err)
	}
	if doneResp.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
		doneResp.GetJob().GetCreatedRows() != 3 || doneResp.GetJob().GetFailedRows() != 0 {
		t.Fatalf("unexpected COMPLETED job: %+v", doneResp.GetJob())
	}

	// raw_csv must be cleared to nil when job finishes.
	rawJob, err := st.GetProductImportJobByID(context.Background(), jobID)
	if err != nil || len(rawJob.RawCSV) != 0 {
		t.Fatalf("expected raw_csv cleared to nil after completion, got len=%d err=%v", len(rawJob.RawCSV), err)
	}

	// Verify created products are PENDING and REVIEW-flagged product retains flagged_keywords.
	listed, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{ShopId: "shop-a", Page: 1, PageSize: 50})
	if err != nil || listed.GetTotal() != 4 { // 1 pre-existing + 3 imported
		t.Fatalf("seller A products after import: total=%d err=%v", listed.GetTotal(), err)
	}
	var sawFlaggedPending bool
	for _, p := range listed.GetProducts() {
		if p.GetName() == "Nước Hoa hàng xách tay" {
			if p.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING || len(p.GetFlaggedKeywords()) == 0 {
				t.Fatalf("REVIEW imported product must be PENDING with flagged_keywords: %+v", p)
			}
			sawFlaggedPending = true
		}
		if p.GetName() == "Sản phẩm ma túy cấm" || p.GetName() == "Hàng trong danh mục cấm" {
			t.Fatalf("INVALID BLOCK/prohibited row must NEVER be created as product: %+v", p)
		}
	}
	if !sawFlaggedPending {
		t.Fatalf("expected REVIEW-flagged imported product in PENDING state")
	}

	// All-invalid job -> ConfirmProductImportJob must fail with IMPORT_JOB_NOT_READY.
	invCSV := "product_name,sku_code,category_id,price_vnd,stock\nBad,INV-1," + cleanCatID + ",-10,1\n"
	invCreate, err := srv.CreateProductImportJob(sellerACtx, &productv1.CreateProductImportJobRequest{
		Filename: "allbad.csv", CsvContent: []byte(invCSV),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob allbad: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("ProcessNextImportJob allbad: %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerACtx, &productv1.ConfirmProductImportJobRequest{JobId: invCreate.GetJob().GetId()}); status.Code(err) != codes.FailedPrecondition || !containsStr(err.Error(), "IMPORT_JOB_NOT_READY") {
		t.Fatalf("confirm 0-valid job: want FailedPrecondition IMPORT_JOB_NOT_READY, got %v", err)
	}
}

func TestW3B3_A8_CSVImport_ConcurrentConfirmAndLeaseExpiryRace(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	clockVal := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	srv.SetClock(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clockVal
	})
	advanceClock := func(d time.Duration) {
		clockMu.Lock()
		clockVal = clockVal.Add(d)
		clockMu.Unlock()
	}

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerCtx := ctxWithRole("seller-race", auth.RoleSeller, "shop-race")
	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Race Cat", Slug: "race-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	buildCSV := func(prefix string, n int) []byte {
		var b []byte
		b = append(b, "product_name,sku_code,category_id,price_vnd,stock,weight_grams\n"...)
		for i := 1; i <= n; i++ {
			b = append(b, fmt.Sprintf("SP %s %d,%s-%03d,%s,%d,10,200\n", prefix, i, prefix, i, catID, 100000+i*1000)...)
		}
		return b
	}

	// 1. Concurrent ConfirmProductImportJob on a 50-row READY job -> exactly 50 products, 0 duplicates.
	cResp, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "race50.csv", CsvContent: buildCSV("RC1", 50),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob race50: %v", err)
	}
	job1ID := cResp.GetJob().GetId()
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate race50: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var firstConfirmCount, alreadyConfirmCount atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, cErr := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: job1ID})
			if cErr != nil {
				t.Errorf("concurrent ConfirmProductImportJob err: %v", cErr)
				return
			}
			if resp.GetAlreadyConfirmed() {
				alreadyConfirmCount.Add(1)
			} else {
				firstConfirmCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if firstConfirmCount.Load() != 1 || alreadyConfirmCount.Load() != 3 {
		t.Fatalf("concurrent confirm counts: first=%d already=%d, want 1 and 3", firstConfirmCount.Load(), alreadyConfirmCount.Load())
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("confirm process race50: %v", err)
	}
	j1After, err := srv.GetProductImportJob(sellerCtx, &productv1.GetProductImportJobRequest{JobId: job1ID})
	if err != nil || j1After.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
		j1After.GetJob().GetCreatedRows() != 50 || j1After.GetJob().GetFailedRows() != 0 {
		t.Fatalf("job1 after confirm: %+v err=%v", j1After, err)
	}
	p1List, err := srv.ListProducts(sellerCtx, &productv1.ListProductsRequest{ShopId: "shop-race", Page: 1, PageSize: 100})
	if err != nil || p1List.GetTotal() != 50 {
		t.Fatalf("expected exactly 50 products after job1, got total=%d err=%v", p1List.GetTotal(), err)
	}

	// 2. Lease expiry mid-confirm: worker 1 stalls at row 21, lease expires, worker 2 takes over and finishes 21..50,
	//    then worker 1 resumes with stale claim_token and is fenced out -> exact 50 more products (total 100), 0 duplicates!
	cResp2, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "lease50.csv", CsvContent: buildCSV("RC2", 50),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob lease50: %v", err)
	}
	job2ID := cResp2.GetJob().GetId()
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate lease50: %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: job2ID}); err != nil {
		t.Fatalf("confirm lease50: %v", err)
	}

	srv.SetImportLease(5 * time.Second)
	paused := make(chan struct{})
	resume := make(chan struct{})
	var w1PausedOnce sync.Once
	srv.SetImportBeforeConfirmRowHook(func(jID string, rowNum int32) {
		if jID == job2ID && rowNum == 21 {
			w1PausedOnce.Do(func() {
				close(paused)
				<-resume
			})
		}
	})

	w1Done := make(chan error, 1)
	go func() {
		_, wErr := srv.ProcessNextImportJob(context.Background())
		w1Done <- wErr
	}()

	<-paused
	// Expire worker 1's lease and let worker 2 claim and finish the job.
	advanceClock(10 * time.Second)
	srv.SetImportBeforeConfirmRowHook(nil)
	worked2, err := srv.ProcessNextImportJob(context.Background())
	if err != nil || !worked2 {
		t.Fatalf("worker 2 takeover: worked=%v err=%v", worked2, err)
	}

	// Now unblock worker 1: it must detect lost lease and NOT duplicate any product.
	close(resume)
	if err := <-w1Done; err != nil {
		t.Fatalf("worker 1 after lost lease returned unexpected error: %v", err)
	}

	j2After, err := srv.GetProductImportJob(sellerCtx, &productv1.GetProductImportJobRequest{JobId: job2ID})
	if err != nil || j2After.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
		j2After.GetJob().GetCreatedRows() != 50 || j2After.GetJob().GetFailedRows() != 0 {
		t.Fatalf("job2 after lease takeover: %+v err=%v", j2After, err)
	}
	p2List, err := srv.ListProducts(sellerCtx, &productv1.ListProductsRequest{ShopId: "shop-race", Page: 1, PageSize: 100})
	if err != nil || p2List.GetTotal() != 100 {
		t.Fatalf("expected exactly 100 total products (50 + 50, 0 duplicates), got total=%d err=%v", p2List.GetTotal(), err)
	}
}

// TestW3B3_A8_CSVImport_RowTextLimitsAndOneActiveJob: text Postgres cannot store or that CreateProduct refuses makes
// only its own row INVALID, the filename is cut on a rune boundary, and a shop has at most one active job.
func TestW3B3_A8_CSVImport_RowTextLimitsAndOneActiveJob(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	sellerCtx := ctxWithRole("seller-lim", auth.RoleSeller, "shop-lim")
	cat, err := srv.CreateCategory(ctxWithRole("admin-1", auth.RoleAdmin, ""), &productv1.CreateCategoryRequest{
		Name: "Limit Cat", Slug: "limit-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()
	row := func(name, sku, attrs, images string) string {
		return fmt.Sprintf("%s,%s,%s,100000,5,\"%s\",%s\n", name, sku, catID, strings.ReplaceAll(attrs, `"`, `""`), images)
	}
	manyImages := strings.TrimSuffix(strings.Repeat("/products/a.jpg|", maxProductImages+1), "|")
	csvBody := "product_name,sku_code,category_id,price_vnd,stock,attributes,images\n" +
		row("SP tốt", "LIM-1", `{"color":"Đen"}`, "") +
		row("SP NUL", "LIM-2", `{"color":"a\u0000b"}`, "") +
		row("SP mã dài", strings.Repeat("x", maxSKUCodeRunes+1), `{}`, "") +
		row(strings.Repeat("ă", maxProductNameRunes+1), "LIM-4", `{}`, "") +
		row("SP nhiều ảnh", "LIM-5", `{}`, manyImages) +
		row(strings.Repeat("ă", maxProductNameRunes), "LIM-6", `{}`, "")

	created, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: strings.Repeat("ế", 300) + ".csv", CsvContent: []byte(csvBody),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob: %v", err)
	}
	jobID := created.GetJob().GetId()
	if got := created.GetJob().GetFilename(); !utf8.ValidString(got) || utf8.RuneCountInString(got) != maxImportFilenameRunes {
		t.Fatalf("filename not cut on a rune boundary: valid=%v runes=%d", utf8.ValidString(got), utf8.RuneCountInString(got))
	}

	_, err = srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{Filename: "b.csv", CsvContent: []byte(csvBody)})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "IMPORT_JOB_IN_PROGRESS") {
		t.Fatalf("second active job: want IMPORT_JOB_IN_PROGRESS, got %v", err)
	}
	if _, err := srv.CreateProductImportJob(ctxWithRole("seller-oth", auth.RoleSeller, "shop-oth"), &productv1.CreateProductImportJobRequest{
		Filename: "c.csv", CsvContent: []byte(csvBody),
	}); err != nil {
		t.Fatalf("another shop must not be blocked: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
			t.Fatalf("ProcessNextImportJob: %v", err)
		}
	}
	rows, err := srv.ListProductImportRows(sellerCtx, &productv1.ListProductImportRowsRequest{JobId: jobID, PageSize: 50})
	if err != nil {
		t.Fatalf("ListProductImportRows: %v", err)
	}
	wantCodes := []string{"", "INVALID_SKU_TEXT", "INVALID_SKU_TEXT", "INVALID_PRODUCT_TEXT", "INVALID_PRODUCT_TEXT", ""}
	for i, r := range rows.GetRows() {
		if r.GetErrorCode() != wantCodes[i] {
			t.Errorf("row %d error_code = %q (%s), want %q", i+1, r.GetErrorCode(), r.GetErrorMessage(), wantCodes[i])
		}
	}

	// READY is not active: the shop may upload a corrected file without confirming the first one.
	if _, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{Filename: "d.csv", CsvContent: []byte(csvBody)}); err != nil {
		t.Fatalf("upload after READY: %v", err)
	}

	// CreateProduct goes through the same check.
	_, err = srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "SP NUL", Skus: []*productv1.SKUInput{{SkuCode: "N-1", PriceVnd: 1000, Attributes: map[string]string{"color": "a\x00b"}}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateProduct with NUL attribute: want InvalidArgument, got %v", err)
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}

// TestW3B3_P1_P2_SellerListProducts_SKUCodeKeywordAndLowStock_Memory verifies:
//   - P1: seller own-shop keyword matches non-INACTIVE sku_code case-insensitively with LIKE-escaped %/_;
//     public and admin callers keep name/description-only behavior.
//   - P2: low_stock=true returns only products with >=1 active SKU (APPROVED/OUT_OF_STOCK product, non-INACTIVE SKU)
//     having available_stock (stock - reserved) <= 5 (incl. out-of-stock), combinable with category/keyword/status,
//     accurate total/pagination, honored for seller/admin, ignored for public.
func TestW3B3_P1_P2_SellerListProducts_SKUCodeKeywordAndLowStock_Memory(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	buyerCtx := ctxWithRole("buyer-1", auth.RoleBuyer, "")
	sellerACtx := ctxWithRole("seller-p12-a", auth.RoleSeller, "shop-p12-a")
	sellerBCtx := ctxWithRole("seller-p12-b", auth.RoleSeller, "shop-p12-b")

	cat1Resp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "P12 Shirts", Slug: "p12-shirts", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory 1: %v", err)
	}
	cat1ID := cat1Resp.GetCategory().GetId()
	cat2Resp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "P12 Pants", Slug: "p12-pants", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory 2: %v", err)
	}
	cat2ID := cat2Resp.GetCategory().GetId()

	createAndApprove := func(ctx context.Context, catID, name string, skus []*productv1.SKUInput, approve bool) *productv1.Product {
		t.Helper()
		resp, cErr := srv.CreateProduct(ctx, &productv1.CreateProductRequest{
			CategoryId: catID, Name: name, Skus: skus,
		})
		if cErr != nil {
			t.Fatalf("CreateProduct %s: %v", name, cErr)
		}
		if approve {
			rev, aErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
				ProductId: resp.GetProduct().GetId(),
				Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
			})
			if aErr != nil {
				t.Fatalf("APPROVE %s: %v", name, aErr)
			}
			return rev.GetProduct()
		}
		return resp.GetProduct()
	}

	// Boundary 5 -> INCLUDED in low_stock
	pAvail5 := createAndApprove(sellerACtx, cat1ID, "Ao Bien Nam", []*productv1.SKUInput{
		{SkuCode: "AT-LOW-5", Name: "M", PriceVnd: 100000, Stock: 5},
	}, true)
	// Boundary 6 -> EXCLUDED from low_stock
	pAvail6 := createAndApprove(sellerACtx, cat1ID, "Ao Bien Sau", []*productv1.SKUInput{
		{SkuCode: "AT-OK-6", Name: "M", PriceVnd: 100000, Stock: 6},
	}, true)
	// Reserved: stock 10 - reserved 5 = 5 -> INCLUDED in low_stock
	pResLow := createAndApprove(sellerACtx, cat1ID, "Ao Giu Ton Thap", []*productv1.SKUInput{
		{SkuCode: "AT-RES-5", Name: "L", PriceVnd: 120000, Stock: 10},
	}, true)
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-p12-res5",
		Items:   []*productv1.StockReservationItem{{SkuId: pResLow.GetSkus()[0].GetId(), Quantity: 5}},
	}); err != nil {
		t.Fatalf("ReserveStock 5: %v", err)
	}
	// Reserved: stock 10 - reserved 4 = 6 -> EXCLUDED from low_stock
	pResOK := createAndApprove(sellerACtx, cat1ID, "Ao Giu Ton Du", []*productv1.SKUInput{
		{SkuCode: "AT-RES-6", Name: "L", PriceVnd: 120000, Stock: 10},
	}, true)
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-p12-res4",
		Items:   []*productv1.StockReservationItem{{SkuId: pResOK.GetSkus()[0].GetId(), Quantity: 4}},
	}); err != nil {
		t.Fatalf("ReserveStock 4: %v", err)
	}
	// Out-of-stock product (cat2) -> INCLUDED in low_stock
	pOOS := createAndApprove(sellerACtx, cat2ID, "Quan Het Hang", []*productv1.SKUInput{
		{SkuCode: "QN-OOS-0", Name: "XL", PriceVnd: 150000, Stock: 0},
	}, true)
	// Special LIKE chars % and _ in sku_code (stock 20 -> not low_stock)
	pWild := createAndApprove(sellerACtx, cat2ID, "Quan Dac Biet", []*productv1.SKUInput{
		{SkuCode: "SPEC_50%OFF", Name: "S", PriceVnd: 200000, Stock: 20},
	}, true)
	// APPROVED product with 1 active SKU (stock 20) + 1 INACTIVE SKU (stock 0) -> EXCLUDED from low_stock & INACTIVE sku_code not matched
	pWithInactive := createAndApprove(sellerACtx, cat1ID, "Ao Co SKU Tat", []*productv1.SKUInput{
		{SkuCode: "LIVE-20", Name: "Live", PriceVnd: 110000, Stock: 20},
		{SkuCode: "DEAD-ZERO", Name: "Dead", PriceVnd: 110000, Stock: 0},
	}, true)
	var liveSKUID string
	for _, sk := range pWithInactive.GetSkus() {
		if sk.GetSkuCode() == "LIVE-20" {
			liveSKUID = sk.GetId()
		}
	}
	if liveSKUID == "" {
		t.Fatalf("missing LIVE-20 sku")
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id: pWithInactive.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: liveSKUID, SkuCode: "LIVE-20", Name: "Live", PriceVnd: 110000, Stock: 20},
		},
	}); err != nil {
		t.Fatalf("deactivate DEAD-ZERO sku: %v", err)
	}
	// Non-selling products (PENDING / HIDDEN) with stock=2 -> EXCLUDED from low_stock
	_ = createAndApprove(sellerACtx, cat1ID, "Ao Cho Duyet Ton It", []*productv1.SKUInput{
		{SkuCode: "PEND-LOW-2", Name: "S", PriceVnd: 100000, Stock: 2},
	}, false)
	pHidden := createAndApprove(sellerACtx, cat1ID, "Ao Dang An Ton It", []*productv1.SKUInput{
		{SkuCode: "HID-LOW-2", Name: "S", PriceVnd: 100000, Stock: 2},
	}, true)
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id: pHidden.GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("hide product: %v", err)
	}
	// Another shop's product with unique sku_code
	_ = createAndApprove(sellerBCtx, cat1ID, "Ao Shop Khac", []*productv1.SKUInput{
		{SkuCode: "OTHER-SHOP-SKU", Name: "M", PriceVnd: 100000, Stock: 2},
	}, true)

	// --- P1 assertions ---
	// Seller matches own sku_code case-insensitively
	resSKU, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Keyword: "at-low-5", Page: 1, PageSize: 20,
	})
	if err != nil || resSKU.GetTotal() != 1 || resSKU.GetProducts()[0].GetId() != pAvail5.GetId() {
		t.Fatalf("P1 seller sku_code search: total=%d products=%+v err=%v", resSKU.GetTotal(), resSKU.GetProducts(), err)
	}
	// Public caller MUST NOT match sku_code
	pubSKU, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		Keyword: "at-low-5", Page: 1, PageSize: 20,
	})
	if err != nil || pubSKU.GetTotal() != 0 {
		t.Fatalf("P1 public must not match sku_code: total=%d err=%v", pubSKU.GetTotal(), err)
	}
	// Admin caller keeps name/description-only behavior (does not match sku_code)
	admSKU, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Keyword: "at-low-5", Page: 1, PageSize: 20,
	})
	if err != nil || admSKU.GetTotal() != 0 {
		t.Fatalf("P1 admin must keep name/desc-only search: total=%d err=%v", admSKU.GetTotal(), err)
	}
	// Seller cannot match INACTIVE sku_code or another shop's sku_code
	deadSKU, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Keyword: "DEAD-ZERO", Page: 1, PageSize: 20,
	})
	if err != nil || deadSKU.GetTotal() != 0 {
		t.Fatalf("P1 inactive sku_code must not match: total=%d err=%v", deadSKU.GetTotal(), err)
	}
	otherSKU, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Keyword: "OTHER-SHOP-SKU", Page: 1, PageSize: 20,
	})
	if err != nil || otherSKU.GetTotal() != 0 {
		t.Fatalf("P1 another shop sku_code must not match: total=%d err=%v", otherSKU.GetTotal(), err)
	}
	// Wildcards % and _ are escaped literally (must match ONLY pWild, NOT every product!)
	for _, wildKW := range []string{"%", "_", "50%off", "spec_50"} {
		wRes, wErr := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
			ShopId: "shop-p12-a", Keyword: wildKW, Page: 1, PageSize: 20,
		})
		if wErr != nil || wRes.GetTotal() != 1 || wRes.GetProducts()[0].GetId() != pWild.GetId() {
			t.Fatalf("P1 escaped LIKE keyword=%q: want only pWild, got total=%d products=%+v err=%v", wildKW, wRes.GetTotal(), wRes.GetProducts(), wErr)
		}
	}

	// --- P2 assertions ---
	// Expected low_stock products for shop-p12-a: ONLY pAvail5, pResLow, pOOS (total = 3).
	lowP1, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", LowStock: true, Page: 1, PageSize: 2,
	})
	if err != nil || lowP1.GetTotal() != 3 || len(lowP1.GetProducts()) != 2 {
		t.Fatalf("P2 page 1: want total=3 len=2, got total=%d len=%d err=%v", lowP1.GetTotal(), len(lowP1.GetProducts()), err)
	}
	lowP2, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", LowStock: true, Page: 2, PageSize: 2,
	})
	if err != nil || lowP2.GetTotal() != 3 || len(lowP2.GetProducts()) != 1 {
		t.Fatalf("P2 page 2: want total=3 len=1, got total=%d len=%d err=%v", lowP2.GetTotal(), len(lowP2.GetProducts()), err)
	}
	gotLowIDs := map[string]bool{}
	for _, p := range append(lowP1.GetProducts(), lowP2.GetProducts()...) {
		gotLowIDs[p.GetId()] = true
	}
	for _, wantID := range []string{pAvail5.GetId(), pResLow.GetId(), pOOS.GetId()} {
		if !gotLowIDs[wantID] {
			t.Fatalf("P2 missing expected low_stock product %s; got=%v", wantID, gotLowIDs)
		}
	}
	if gotLowIDs[pAvail6.GetId()] || gotLowIDs[pResOK.GetId()] || gotLowIDs[pWithInactive.GetId()] || gotLowIDs[pHidden.GetId()] {
		t.Fatalf("P2 included non-low-stock or non-selling product: %v", gotLowIDs)
	}

	// Combined filters: low_stock + category_id, low_stock + status, low_stock + keyword (sku_code)
	combCat, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", CategoryId: cat2ID, LowStock: true, Page: 1, PageSize: 20,
	})
	if err != nil || combCat.GetTotal() != 1 || combCat.GetProducts()[0].GetId() != pOOS.GetId() {
		t.Fatalf("P2 low_stock+category_id: total=%d err=%v", combCat.GetTotal(), err)
	}
	combStatus, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, LowStock: true, Page: 1, PageSize: 20,
	})
	if err != nil || combStatus.GetTotal() != 2 {
		t.Fatalf("P2 low_stock+status=APPROVED: want total=2, got total=%d err=%v", combStatus.GetTotal(), err)
	}
	combKW, err := srv.ListProducts(sellerACtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", Keyword: "at-res-5", LowStock: true, Page: 1, PageSize: 20,
	})
	if err != nil || combKW.GetTotal() != 1 || combKW.GetProducts()[0].GetId() != pResLow.GetId() {
		t.Fatalf("P2 low_stock+keyword(sku_code): total=%d err=%v", combKW.GetTotal(), err)
	}

	// Admin caller honors low_stock=true; public caller ignores low_stock=true.
	admLow, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", LowStock: true, Page: 1, PageSize: 20,
	})
	if err != nil || admLow.GetTotal() != 3 {
		t.Fatalf("P2 admin low_stock=true: want total=3, got total=%d err=%v", admLow.GetTotal(), err)
	}
	pubLow, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		ShopId: "shop-p12-a", LowStock: true, Page: 1, PageSize: 20,
	})
	if err != nil || pubLow.GetTotal() != 7 { // all 7 public APPROVED/OUT_OF_STOCK products of shop-p12-a
		t.Fatalf("P2 public must ignore low_stock=true: want total=7, got total=%d err=%v", pubLow.GetTotal(), err)
	}
}

// TestW3B3_P4_CSVImport_AttributesNullAndWorkerPanicRecovery verifies audit 2.1 fixes:
//   - attributes=null with color/size never panics and treats attributes as an empty map;
//   - RunImportWorker recovers panics, logs stack, marks the panicked job FAILED (INTERNAL_ERROR) after one attempt,
//     never crash-loops on it, and stays alive to process subsequent jobs.
func TestW3B3_P4_CSVImport_AttributesNullAndWorkerPanicRecovery(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	clockVal := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	srv.SetClock(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clockVal
	})

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerCtx := ctxWithRole("seller-p4", auth.RoleSeller, "shop-p4")
	sellerOtherCtx := ctxWithRole("seller-p4-other", auth.RoleSeller, "shop-p4-other")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "P4 Cat", Slug: "p4-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	// 1. attributes=null with color and size must NOT panic and must create product cleanly.
	csvNull := fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock,attributes,color,size\nSP Null Attr,SKU-NULL-1,%s,100000,5,null,Đỏ,M\n", catID)
	jNull, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "null-attr.csv", CsvContent: []byte(csvNull),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob null attr: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("ProcessNextImportJob validate null attr: %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: jNull.GetJob().GetId()}); err != nil {
		t.Fatalf("ConfirmProductImportJob null attr: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("ProcessNextImportJob confirm null attr: %v", err)
	}
	gotNullJob, err := srv.GetProductImportJob(sellerCtx, &productv1.GetProductImportJobRequest{JobId: jNull.GetJob().GetId()})
	if err != nil || gotNullJob.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
		gotNullJob.GetJob().GetCreatedRows() != 1 {
		t.Fatalf("null-attr job expected COMPLETED created=1, got %+v err=%v", gotNullJob, err)
	}
	// Also ensure decodeImportRowExtras handles JSON null raw_row_json safely without panicking.
	if _, err := decodeImportRowExtras([]byte("null")); err != nil {
		t.Fatalf("decodeImportRowExtras(null): %v", err)
	}

	// 2. Simulated row panic inside RunImportWorker -> recovered, job transitions to FAILED (INTERNAL_ERROR)
	//    after 1 attempt (not retried after lease expiry), and worker stays alive to process the next job.
	csvPanic := fmt.Sprintf(
		"product_name,sku_code,category_id,price_vnd,stock\nRow 1,PANIC-1,%s,100000,5\nRow 2,PANIC-2,%s,100000,5\n",
		catID, catID,
	)
	jPanic, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "panic.csv", CsvContent: []byte(csvPanic),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob panic: %v", err)
	}
	panicJobID := jPanic.GetJob().GetId()
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate panic job: %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: panicJobID}); err != nil {
		t.Fatalf("confirm panic job: %v", err)
	}

	var panicCount atomic.Int32
	srv.SetImportBeforeConfirmRowHook(func(jID string, rowNum int32) {
		if jID == panicJobID && rowNum == 2 {
			panicCount.Add(1)
			panic("simulated poison row panic")
		}
	})

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		srv.RunImportWorker(workerCtx, 10*time.Millisecond)
	}()

	// Wait for panicJobID to become FAILED with INTERNAL_ERROR.
	deadline := time.Now().Add(3 * time.Second)
	var failedJob *productv1.ProductImportJob
	for time.Now().Before(deadline) {
		resp, gErr := srv.GetProductImportJob(sellerCtx, &productv1.GetProductImportJobRequest{JobId: panicJobID})
		if gErr == nil && resp.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_FAILED {
			failedJob = resp.GetJob()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if failedJob == nil {
		cancelWorker()
		<-workerDone
		t.Fatalf("panicked job did not transition to FAILED")
	}
	if !strings.Contains(failedJob.GetErrorMessage(), "INTERNAL_ERROR") {
		cancelWorker()
		<-workerDone
		t.Fatalf("panicked job error_message = %q, want INTERNAL_ERROR", failedJob.GetErrorMessage())
	}

	// Advance clock past lease (60s) and submit a healthy job for another shop to prove:
	// (a) the panicked job is NOT re-claimed/re-panicked; (b) RunImportWorker is still alive and working.
	clockMu.Lock()
	clockVal = clockVal.Add(60 * time.Second)
	clockMu.Unlock()

	jHealthy, err := srv.CreateProductImportJob(sellerOtherCtx, &productv1.CreateProductImportJobRequest{
		Filename: "healthy.csv", CsvContent: []byte(fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock\nHealthy SP,HLT-1,%s,100000,5\n", catID)),
	})
	if err != nil {
		cancelWorker()
		<-workerDone
		t.Fatalf("CreateProductImportJob healthy: %v", err)
	}
	healthyReady := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, gErr := srv.GetProductImportJob(sellerOtherCtx, &productv1.GetProductImportJobRequest{JobId: jHealthy.GetJob().GetId()})
		if gErr == nil && resp.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY {
			healthyReady = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelWorker()
	<-workerDone

	if !healthyReady {
		t.Fatalf("RunImportWorker did not stay alive to process healthy job after panic")
	}
	if got := panicCount.Load(); got != 1 {
		t.Fatalf("panicked job hook invoked %d times, want exactly 1 (must stop FAILED after first attempt)", got)
	}
}

// TestW3B3_P5_CSVImport_ConfirmRejectsWhenAnotherJobActive verifies audit 2.2:
// ConfirmProductImportJob checks HasActiveProductImportJob excluding self under the shop lock
// and returns FailedPrecondition IMPORT_JOB_IN_PROGRESS when another job of the shop is active.
func TestW3B3_P5_CSVImport_ConfirmRejectsWhenAnotherJobActive(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	sellerCtx := ctxWithRole("seller-p5", auth.RoleSeller, "shop-p5")
	cat, err := srv.CreateCategory(ctxWithRole("admin-1", auth.RoleAdmin, ""), &productv1.CreateCategoryRequest{
		Name: "P5 Cat", Slug: "p5-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()
	csv1 := fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock\nSP A,P5-A,%s,100000,5\n", catID)
	csv2 := fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock\nSP B,P5-B,%s,100000,5\n", catID)

	j1, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{Filename: "1.csv", CsvContent: []byte(csv1)})
	if err != nil {
		t.Fatalf("create j1: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate j1: %v", err)
	}
	j2, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{Filename: "2.csv", CsvContent: []byte(csv2)})
	if err != nil {
		t.Fatalf("create j2: %v", err)
	}
	// While j2 is QUEUED, confirming j1 (READY) must also fail with IMPORT_JOB_IN_PROGRESS.
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: j1.GetJob().GetId()}); status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "IMPORT_JOB_IN_PROGRESS") {
		t.Fatalf("confirm j1 while j2 QUEUED: want FailedPrecondition IMPORT_JOB_IN_PROGRESS, got %v", err)
	}

	// Validate j2 -> READY; now both j1 and j2 are READY.
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate j2: %v", err)
	}
	// Confirm j1 -> succeeds (CONFIRMING).
	c1, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: j1.GetJob().GetId()})
	if err != nil || c1.GetAlreadyConfirmed() {
		t.Fatalf("confirm j1: resp=%+v err=%v", c1, err)
	}
	// Idempotent repeat on j1 itself MUST succeed (already_confirmed=true, not blocked by self).
	c1Again, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: j1.GetJob().GetId()})
	if err != nil || !c1Again.GetAlreadyConfirmed() {
		t.Fatalf("confirm j1 repeat: want already_confirmed=true, got resp=%+v err=%v", c1Again, err)
	}
	// Confirm j2 while j1 is CONFIRMING -> FailedPrecondition IMPORT_JOB_IN_PROGRESS.
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: j2.GetJob().GetId()}); status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "IMPORT_JOB_IN_PROGRESS") {
		t.Fatalf("confirm j2 while j1 CONFIRMING: want FailedPrecondition IMPORT_JOB_IN_PROGRESS, got %v", err)
	}
	// After j1 finishes COMPLETED, confirming j2 succeeds.
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("process j1: %v", err)
	}
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: j2.GetJob().GetId()}); err != nil {
		t.Fatalf("confirm j2 after j1 COMPLETED: %v", err)
	}
}

// TestW3B3_P6_ProductSlugMax200_SharedValidationAndAutoTruncation verifies audit 2.3:
//   - Explicit slug > 200 normalized chars fails with InvalidArgument on CreateProduct/UpdateProduct
//     and INVALID_SLUG at CSV validation; <= 200 normalized chars succeeds.
//   - Auto-generated slug from long product name is truncated to <= 200 chars (on dash boundary when possible)
//     without error, including duplicate-name collision suffixes.
func TestW3B3_P6_ProductSlugMax200_SharedValidationAndAutoTruncation(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	sellerCtx := ctxWithRole("seller-p6", auth.RoleSeller, "shop-p6")
	cat, err := srv.CreateCategory(ctxWithRole("admin-1", auth.RoleAdmin, ""), &productv1.CreateCategoryRequest{
		Name: "P6 Cat", Slug: "p6-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	slug200 := strings.Repeat("a", 200)
	slug201 := strings.Repeat("a", 201)

	// 1. CreateProduct rejects explicit slug > 200 chars, accepts exact 200 chars (and raw >200 normalizing to <=200).
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "SP Slug Dai", Slug: slug201,
		Skus: []*productv1.SKUInput{{SkuCode: "P6-BAD-1", PriceVnd: 100000, Stock: 5}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateProduct slug 201: want InvalidArgument, got %v", err)
	}
	p200, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "SP Slug 200", Slug: "  " + slug200 + "!!!",
		Skus: []*productv1.SKUInput{{SkuCode: "P6-OK-200", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil || len(p200.GetProduct().GetSlug()) != 200 {
		t.Fatalf("CreateProduct slug 200: slugLen=%d err=%v", len(p200.GetProduct().GetSlug()), err)
	}

	// 2. UpdateProduct rejects explicit slug > 200 chars.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p200.GetProduct().GetId(), Slug: slug201,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct slug 201: want InvalidArgument, got %v", err)
	}

	// 3. Auto-generated slug from long Vietnamese name with spaces: cut <= 200 on dash boundary, no error.
	longName := strings.TrimSpace(strings.Repeat("Áo thun cotton nam cao cấp ", 12)) // > 300 chars
	pAuto1, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: longName,
		Skus: []*productv1.SKUInput{{SkuCode: "P6-AUTO-1", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct auto-slug #1: %v", err)
	}
	s1 := pAuto1.GetProduct().GetSlug()
	if s1 == "" || len(s1) > 200 || strings.HasSuffix(s1, "-") {
		t.Fatalf("auto-slug #1 invalid: len=%d slug=%q", len(s1), s1)
	}
	// Check cut happened cleanly at word boundary (every segment is one of the full words).
	if !strings.HasSuffix(s1, "-cap") && !strings.HasSuffix(s1, "-cao") && !strings.HasSuffix(s1, "-nam") &&
		!strings.HasSuffix(s1, "-cotton") && !strings.HasSuffix(s1, "-thun") && !strings.HasSuffix(s1, "-ao") {
		t.Fatalf("auto-slug #1 split mid-word instead of dash boundary: %q", s1)
	}
	// Second product with identical long name gets -<id[:8]> suffix and STILL fits <= 200 chars!
	pAuto2, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: longName,
		Skus: []*productv1.SKUInput{{SkuCode: "P6-AUTO-2", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct auto-slug #2: %v", err)
	}
	s2 := pAuto2.GetProduct().GetSlug()
	if s2 == "" || len(s2) > 200 || s2 == s1 {
		t.Fatalf("auto-slug #2 invalid: len=%d slug=%q (s1=%q)", len(s2), s2, s1)
	}
	if !isAutoGeneratedSlug(pAuto1.GetProduct().GetId(), longName, s1) ||
		!isAutoGeneratedSlug(pAuto2.GetProduct().GetId(), longName, s2) {
		t.Fatalf("isAutoGeneratedSlug failed for truncated slugs: s1=%q s2=%q", s1, s2)
	}

	// Single-word 300-char name without spaces -> truncated cleanly to exact 200 chars.
	pSingleWord, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: strings.Repeat("b", 300),
		Skus: []*productv1.SKUInput{{SkuCode: "P6-SINGLE", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil || len(pSingleWord.GetProduct().GetSlug()) != 200 {
		t.Fatalf("single-word auto-slug: len=%d err=%v", len(pSingleWord.GetProduct().GetSlug()), err)
	}

	// 4. CSV import: explicit slug > 200 -> INVALID with error_code INVALID_SLUG at validation stage;
	//    long name with empty slug -> VALID and created with auto-truncated slug <= 200.
	csvBody := fmt.Sprintf(
		"product_name,sku_code,category_id,price_vnd,stock,slug\nSP CSV Bad Slug,P6-CSV-BAD,%s,100000,5,%s\n%s,P6-CSV-AUTO,%s,100000,5,\n",
		catID, slug201, longName, catID,
	)
	jResp, err := srv.CreateProductImportJob(sellerCtx, &productv1.CreateProductImportJobRequest{
		Filename: "p6-slug.csv", CsvContent: []byte(csvBody),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob p6: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("validate p6 csv: %v", err)
	}
	rows, err := srv.ListProductImportRows(sellerCtx, &productv1.ListProductImportRowsRequest{
		JobId: jResp.GetJob().GetId(), Page: 1, PageSize: 10,
	})
	if err != nil || len(rows.GetRows()) != 2 {
		t.Fatalf("list p6 rows: len=%d err=%v", len(rows.GetRows()), err)
	}
	if rows.GetRows()[0].GetStatus() != productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID ||
		rows.GetRows()[0].GetErrorCode() != "INVALID_SLUG" {
		t.Fatalf("CSV overlong slug row: status=%v code=%q, want INVALID/INVALID_SLUG",
			rows.GetRows()[0].GetStatus(), rows.GetRows()[0].GetErrorCode())
	}
	if rows.GetRows()[1].GetStatus() != productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_VALID {
		t.Fatalf("CSV long-name auto-slug row: status=%v code=%q msg=%q, want VALID",
			rows.GetRows()[1].GetStatus(), rows.GetRows()[1].GetErrorCode(), rows.GetRows()[1].GetErrorMessage())
	}
	if _, err := srv.ConfirmProductImportJob(sellerCtx, &productv1.ConfirmProductImportJobRequest{JobId: jResp.GetJob().GetId()}); err != nil {
		t.Fatalf("confirm p6 csv: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("process p6 csv: %v", err)
	}
	doneRows, err := srv.ListProductImportRows(sellerCtx, &productv1.ListProductImportRowsRequest{
		JobId: jResp.GetJob().GetId(), Page: 1, PageSize: 10,
	})
	if err != nil || doneRows.GetRows()[1].GetStatus() != productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_CREATED {
		t.Fatalf("CSV long-name row after confirm: %+v err=%v", doneRows.GetRows(), err)
	}
	createdProd, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: doneRows.GetRows()[1].GetCreatedProductId()})
	if err != nil || len(createdProd.GetProduct().GetSlug()) == 0 || len(createdProd.GetProduct().GetSlug()) > 200 {
		t.Fatalf("CSV created auto-slug invalid: slug=%q err=%v", createdProd.GetProduct().GetSlug(), err)
	}
}
