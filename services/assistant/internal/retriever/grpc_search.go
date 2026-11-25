package retriever

import (
	"context"
	"strings"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func dialGRPC(addr, serviceName string) (*grpc.ClientConn, error) {
	if serviceName == "" {
		serviceName = "assistant"
	}
	return grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(serviceName)),
	)
}

// stripOutgoingAuth ensures public catalog search RPCs never forward user or service
// authorization headers (prevents recording search history on behalf of users and avoids
// public RPC rejection of service tokens).
func stripOutgoingAuth(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || len(md.Get("authorization")) == 0 {
		return ctx
	}
	cloned := md.Copy()
	cloned.Delete("authorization")
	return metadata.NewOutgoingContext(ctx, cloned)
}

func (r *ChainedRetriever) searchViaSearchGRPC(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error) {
	req := &searchv1.SearchProductsRequest{
		Q:           q.Query,
		ShopId:      q.ShopID,
		MinPriceVnd: q.MinPriceVND,
		MaxPriceVnd: q.MaxPriceVND,
		InStockOnly: false,
		SortBy:      normalizeSearchSortBy(q.Sort),
		Mode:        searchv1.SearchMode_SEARCH_MODE_HYBRID,
		Page:        q.Page,
		PageSize:    q.PageSize,
	}
	if cat := strings.TrimSpace(q.Category); cat != "" {
		if looksLikeUUID(cat) {
			req.CategoryId = cat
		} else {
			req.CategorySlug = cat
		}
	}
	resp, err := r.searchClient.SearchProducts(stripOutgoingAuth(ctx), req)
	if err != nil {
		return nil, 0, err
	}
	hits := make([]ProductHit, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		hits = append(hits, searchProductHitToProductHit(it))
	}
	return hits, resp.GetTotal(), nil
}

func (r *ChainedRetriever) similarViaSearchGRPC(ctx context.Context, productID, shopID string, limit int32) ([]ProductHit, error) {
	resp, err := r.searchClient.SimilarProducts(stripOutgoingAuth(ctx), &searchv1.SimilarProductsRequest{
		ProductId: productID,
		ShopId:    shopID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	hits := make([]ProductHit, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		hits = append(hits, searchProductHitToProductHit(it))
	}
	return hits, nil
}

func (r *ChainedRetriever) searchViaProductGRPC(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error) {
	resp, err := r.productClient.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  q.Category,
		Keyword:     q.Query,
		ShopId:      q.ShopID,
		Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
		MinPriceVnd: q.MinPriceVND,
		MaxPriceVnd: q.MaxPriceVND,
		SortBy:      q.Sort,
		Page:        q.Page,
		PageSize:    q.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	hits := make([]ProductHit, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		hits = append(hits, productProtoToProductHit(p))
	}
	return hits, resp.GetTotal(), nil
}

func (r *ChainedRetriever) getProductViaProductGRPC(ctx context.Context, productID string) (*ProductHit, error) {
	resp, err := r.productClient.GetProduct(ctx, &productv1.GetProductRequest{Id: productID})
	if err != nil {
		return nil, err
	}
	if resp.GetProduct() == nil {
		return nil, ErrProductNotFound
	}
	hit := productProtoToProductHit(resp.GetProduct())
	return &hit, nil
}

func searchProductHitToProductHit(it *searchv1.SearchProductHit) ProductHit {
	if it == nil {
		return ProductHit{}
	}
	cat := strings.TrimSpace(it.GetCategoryName())
	if cat == "" {
		cat = strings.TrimSpace(it.GetCategorySlug())
	}
	return ProductHit{
		ID:          it.GetId(),
		ShopID:      it.GetShopId(),
		ShopName:    it.GetShopName(),
		CategoryID:  it.GetCategoryId(),
		Category:    cat,
		Name:        it.GetName(),
		Description: it.GetDescriptionSnippet(),
		MinPriceVND: it.GetMinPriceVnd(),
		MaxPriceVND: it.GetMaxPriceVnd(),
		ImageURL:    strings.TrimSpace(it.GetThumbnailUrl()),
		InStock:     it.GetInStock(),
		Score:       it.GetScore(),
	}
}

func productProtoToProductHit(p *productv1.Product) ProductHit {
	if p == nil {
		return ProductHit{}
	}
	img := ""
	if imgs := p.GetImages(); len(imgs) > 0 {
		img = imgs[0]
	}
	skus := make([]SKUHit, 0, len(p.GetSkus()))
	inStock := false
	for _, s := range p.GetSkus() {
		if s == nil {
			continue
		}
		if s.GetAvailableStock() > 0 {
			inStock = true
		}
		attrs := make(map[string]string, len(s.GetAttributes()))
		for k, v := range s.GetAttributes() {
			attrs[k] = v
		}
		skus = append(skus, SKUHit{
			ID:             s.GetId(),
			SKUCode:        s.GetSkuCode(),
			Name:           s.GetName(),
			PriceVND:       s.GetPriceVnd(),
			AvailableStock: s.GetAvailableStock(),
			Attributes:     attrs,
		})
	}
	if len(skus) == 0 && p.GetStatus() == productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		inStock = true
	}
	return ProductHit{
		ID:          p.GetId(),
		ShopID:      p.GetShopId(),
		CategoryID:  p.GetCategoryId(),
		Name:        p.GetName(),
		Description: p.GetDescription(),
		MinPriceVND: p.GetMinPriceVnd(),
		MaxPriceVND: p.GetMaxPriceVnd(),
		ImageURL:    img,
		InStock:     inStock,
		SKUs:        skus,
	}
}
