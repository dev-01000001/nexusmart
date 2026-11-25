package retriever

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/policy"
)

// ErrProductNotFound is returned when a product ID does not exist in the catalog.
var ErrProductNotFound = errors.New("product not found")

const (
	memBM25K1 = 1.5
	memBM25B  = 0.75
	memRRFK   = 60.0
)

type indexedProduct struct {
	hit        ProductHit
	tf         map[string]int
	docLen     int
	vec        []float32
	normCat    string
	normCatID  string
	normSearch string
}

// InMemoryRetriever provides deterministic Vietnamese accent-insensitive hybrid search
// (BM25 + 256-dim hash cosine RRF) with strict shop_id isolation and indirect injection sanitization.
type InMemoryRetriever struct {
	mu        sync.RWMutex
	products  []indexedProduct
	byID      map[string]int
	idf       map[string]float64
	avgDocLen float64
	guardrail guardrail.Engine
}

// NewInMemoryRetriever indexes products into an in-memory hybrid retriever.
func NewInMemoryRetriever(products []ProductHit, g guardrail.Engine) *InMemoryRetriever {
	indexed := make([]indexedProduct, 0, len(products))
	byID := make(map[string]int, len(products))
	df := make(map[string]int, 256)
	totalLen := 0

	for _, raw := range products {
		p := cloneProductHit(raw)
		indexText := buildProductIndexText(p)
		tokens := policy.TokenizeVietnamese(indexText)
		tf := make(map[string]int, len(tokens))
		for _, tok := range tokens {
			tf[tok]++
		}
		for tok := range tf {
			df[tok]++
		}
		totalLen += len(tokens)
		byID[p.ID] = len(indexed)
		indexed = append(indexed, indexedProduct{
			hit:        p,
			tf:         tf,
			docLen:     len(tokens),
			vec:        policy.HashEmbed256(indexText),
			normCat:    policy.NormalizeVietnamese(p.Category),
			normCatID:  policy.NormalizeVietnamese(p.CategoryID),
			normSearch: policy.NormalizeVietnamese(indexText),
		})
	}

	n := float64(len(indexed))
	idf := make(map[string]float64, len(df))
	if n > 0 {
		for tok, freq := range df {
			idf[tok] = math.Log(1.0 + (n-float64(freq)+0.5)/(float64(freq)+0.5))
		}
	}
	avgLen := 1.0
	if n > 0 && totalLen > 0 {
		avgLen = float64(totalLen) / n
	}

	return &InMemoryRetriever{
		products:  indexed,
		byID:      byID,
		idf:       idf,
		avgDocLen: avgLen,
		guardrail: g,
	}
}

// Search performs filtered, shop-isolated hybrid search and sanitizes all returned descriptions.
func (m *InMemoryRetriever) Search(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 10
	}

	shopFilter := strings.TrimSpace(q.ShopID)
	catFilter := policy.NormalizeVietnamese(q.Category)
	normQuery := policy.NormalizeVietnamese(q.Query)

	m.mu.RLock()
	products := m.products
	idf := m.idf
	avgLen := m.avgDocLen
	m.mu.RUnlock()

	filteredIdx := make([]int, 0, len(products))
	for i, ip := range products {
		if !matchesFilters(ip, shopFilter, catFilter, q.MinPriceVND, q.MaxPriceVND) {
			continue
		}
		filteredIdx = append(filteredIdx, i)
	}

	if len(filteredIdx) == 0 {
		return []ProductHit{}, 0, nil
	}

	type scored struct {
		idx    int
		bm25   float64
		cosine float64
		rrf    float64
	}

	var ranked []scored
	if normQuery == "" {
		ranked = make([]scored, 0, len(filteredIdx))
		for _, idx := range filteredIdx {
			ranked = append(ranked, scored{idx: idx, rrf: 1.0})
		}
	} else {
		rawTokens := policy.TokenizeVietnamese(normQuery)
		meaningful := extractMeaningfulProductTokens(rawTokens)
		queryVec := policy.HashEmbed256(normQuery)

		items := make([]scored, len(filteredIdx))
		bm25Order := make([]int, 0, len(filteredIdx))
		vecOrder := make([]int, 0, len(filteredIdx))

		for pos, idx := range filteredIdx {
			ip := products[idx]
			b := scoreMemBM25(meaningful, ip.tf, ip.docLen, avgLen, idf)
			c := policy.CosineSimilarity(queryVec, ip.vec)
			items[pos] = scored{idx: idx, bm25: b, cosine: c}
			if b > 0 {
				bm25Order = append(bm25Order, pos)
			}
			if c >= 0.16 {
				vecOrder = append(vecOrder, pos)
			}
		}

		sort.Slice(bm25Order, func(i, j int) bool {
			a, b := items[bm25Order[i]], items[bm25Order[j]]
			if a.bm25 != b.bm25 {
				return a.bm25 > b.bm25
			}
			return products[a.idx].hit.ID < products[b.idx].hit.ID
		})
		for rank, pos := range bm25Order {
			items[pos].rrf += 1.0 / (memRRFK + float64(rank+1))
		}

		sort.Slice(vecOrder, func(i, j int) bool {
			a, b := items[vecOrder[i]], items[vecOrder[j]]
			if a.cosine != b.cosine {
				return a.cosine > b.cosine
			}
			return products[a.idx].hit.ID < products[b.idx].hit.ID
		})
		for rank, pos := range vecOrder {
			items[pos].rrf += 1.0 / (memRRFK + float64(rank+1))
		}

		ranked = make([]scored, 0, len(items))
		for _, s := range items {
			ip := products[s.idx]
			if !passesRelevanceGate(meaningful, ip, s.bm25, s.cosine) {
				continue
			}
			ranked = append(ranked, s)
		}
	}

	sortMode := strings.ToLower(strings.TrimSpace(q.Sort))
	sort.Slice(ranked, func(i, j int) bool {
		pi := products[ranked[i].idx].hit
		pj := products[ranked[j].idx].hit
		switch sortMode {
		case "price_asc":
			if pi.MinPriceVND != pj.MinPriceVND {
				return pi.MinPriceVND < pj.MinPriceVND
			}
		case "price_desc":
			if pi.MinPriceVND != pj.MinPriceVND {
				return pi.MinPriceVND > pj.MinPriceVND
			}
		}
		if ranked[i].rrf != ranked[j].rrf {
			return ranked[i].rrf > ranked[j].rrf
		}
		if ranked[i].bm25 != ranked[j].bm25 {
			return ranked[i].bm25 > ranked[j].bm25
		}
		return pi.ID < pj.ID
	})

	total := int64(len(ranked))
	start := int((q.Page - 1) * q.PageSize)
	if start >= len(ranked) {
		return []ProductHit{}, total, nil
	}
	end := start + int(q.PageSize)
	if end > len(ranked) {
		end = len(ranked)
	}

	out := make([]ProductHit, 0, end-start)
	for _, s := range ranked[start:end] {
		hit := cloneProductHit(products[s.idx].hit)
		hit.Score = math.Round(s.rrf*10000) / 10000
		hit = sanitizeHit(hit, m.guardrail)
		out = append(out, hit)
	}
	return out, total, nil
}

// SimilarProducts returns products similar to productID within shopID scope.
func (m *InMemoryRetriever) SimilarProducts(ctx context.Context, productID, shopID string, limit int32) ([]ProductHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 6
	}
	shopFilter := strings.TrimSpace(shopID)

	m.mu.RLock()
	seedIdx, ok := m.byID[strings.TrimSpace(productID)]
	if !ok {
		m.mu.RUnlock()
		return []ProductHit{}, nil
	}
	seed := m.products[seedIdx]
	products := m.products
	m.mu.RUnlock()

	type scored struct {
		idx   int
		score float64
	}
	var cands []scored
	for i, ip := range products {
		if ip.hit.ID == seed.hit.ID {
			continue
		}
		if shopFilter != "" && ip.hit.ShopID != shopFilter {
			continue
		}
		sim := policy.CosineSimilarity(seed.vec, ip.vec)
		if seed.normCat != "" && ip.normCat == seed.normCat {
			sim += 0.25
		}
		cands = append(cands, scored{idx: i, score: sim})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return products[cands[i].idx].hit.ID < products[cands[j].idx].hit.ID
	})
	if int32(len(cands)) > limit {
		cands = cands[:limit]
	}

	out := make([]ProductHit, 0, len(cands))
	for _, c := range cands {
		hit := cloneProductHit(products[c.idx].hit)
		hit.Score = math.Round(c.score*10000) / 10000
		hit = sanitizeHit(hit, m.guardrail)
		out = append(out, hit)
	}
	return out, nil
}

// GetProduct looks up a product by ID and sanitizes its description.
func (m *InMemoryRetriever) GetProduct(ctx context.Context, productID string) (*ProductHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	idx, ok := m.byID[strings.TrimSpace(productID)]
	if !ok {
		m.mu.RUnlock()
		return nil, ErrProductNotFound
	}
	hit := cloneProductHit(m.products[idx].hit)
	m.mu.RUnlock()

	hit = sanitizeHit(hit, m.guardrail)
	return &hit, nil
}

func matchesFilters(ip indexedProduct, shopID, normCat string, minPrice, maxPrice int64) bool {
	if shopID != "" && ip.hit.ShopID != shopID {
		return false
	}
	if normCat != "" {
		catMatch := ip.normCat == normCat ||
			ip.normCatID == normCat ||
			strings.Contains(ip.normCat, normCat) ||
			strings.Contains(ip.normCatID, normCat) ||
			strings.Contains(ip.normSearch, normCat)
		if !catMatch {
			return false
		}
	}
	pMin := ip.hit.MinPriceVND
	pMax := ip.hit.MaxPriceVND
	if pMax == 0 {
		pMax = pMin
	}
	if minPrice > 0 && pMax > 0 && pMax < minPrice {
		return false
	}
	if maxPrice > 0 && pMin > maxPrice {
		return false
	}
	return true
}

func passesRelevanceGate(meaningful []string, ip indexedProduct, bm25, cosine float64) bool {
	if len(meaningful) == 0 {
		return bm25 > 0 || cosine >= 0.30
	}
	matched := 0
	for _, tok := range meaningful {
		if ip.tf[tok] > 0 {
			matched++
		}
	}
	if matched == 0 {
		return cosine >= 0.40
	}
	ratio := float64(matched) / float64(len(meaningful))
	switch {
	case len(meaningful) == 1:
		return matched == 1
	case len(meaningful) <= 4:
		return ratio >= 0.50 || cosine >= 0.45
	default:
		return (matched >= 2 && ratio >= 0.35) || cosine >= 0.45
	}
}

func buildProductIndexText(p ProductHit) string {
	var b strings.Builder
	// Weight product name twice so name matches dominate over incidental description text.
	b.WriteString(p.Name)
	b.WriteByte(' ')
	b.WriteString(p.Name)
	b.WriteByte(' ')
	b.WriteString(p.Category)
	b.WriteByte(' ')
	b.WriteString(p.CategoryID)
	b.WriteByte(' ')
	b.WriteString(p.Description)
	for _, sku := range p.SKUs {
		b.WriteByte(' ')
		b.WriteString(sku.Name)
		b.WriteByte(' ')
		b.WriteString(sku.SKUCode)
		for _, v := range sku.Attributes {
			b.WriteByte(' ')
			b.WriteString(v)
		}
	}
	return b.String()
}

func cloneProductHit(p ProductHit) ProductHit {
	cp := p
	if len(p.SKUs) > 0 {
		cp.SKUs = make([]SKUHit, len(p.SKUs))
		for i, s := range p.SKUs {
			skuCopy := s
			if len(s.Attributes) > 0 {
				skuCopy.Attributes = make(map[string]string, len(s.Attributes))
				for k, v := range s.Attributes {
					skuCopy.Attributes[k] = v
				}
			}
			cp.SKUs[i] = skuCopy
		}
	}
	return cp
}

func scoreMemBM25(tokens []string, tf map[string]int, docLen int, avgDocLen float64, idf map[string]float64) float64 {
	if len(tokens) == 0 || docLen == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(tokens))
	var score float64
	for _, tok := range tokens {
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		freq := float64(tf[tok])
		if freq == 0 {
			continue
		}
		tokenIDF := idf[tok]
		if tokenIDF <= 0 {
			tokenIDF = 0.2
		}
		denom := freq + memBM25K1*(1.0-memBM25B+memBM25B*(float64(docLen)/avgDocLen))
		score += tokenIDF * ((freq * (memBM25K1 + 1.0)) / denom)
	}
	return score
}

var productQueryStopWords = map[string]struct{}{
	"mua": {}, "tim": {}, "cho": {}, "toi": {}, "minh": {}, "shop": {}, "gian": {}, "hang": {},
	"co": {}, "khong": {}, "nao": {}, "loai": {}, "gia": {}, "duoi": {}, "tren": {}, "tu": {},
	"den": {}, "khoang": {}, "tam": {}, "re": {}, "dep": {}, "tot": {}, "nhat": {}, "ban": {},
	"can": {}, "muon": {}, "xem": {}, "san": {}, "pham": {}, "vnd": {}, "dong": {}, "trieu": {},
	"nghin": {}, "ngan": {}, "k": {}, "d": {}, "la": {}, "cua": {}, "va": {}, "hoac": {},
	"voi": {}, "de": {}, "mac": {}, "dung": {}, "nhe": {}, "a": {}, "oi": {}, "gi": {},
	"bao": {}, "nhieu": {}, "em": {}, "anh": {}, "chi": {}, "goi": {}, "y": {}, "tuong": {},
	"mau": {}, "size": {}, "cai": {}, "chiec": {}, "bo": {}, "mot": {}, "vai": {}, "giup": {},
	"di": {}, "choi": {}, "lam": {}, "nha": {}, "ngay": {}, "he": {}, "thu": {}, "muahe": {},
}

func extractMeaningfulProductTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if _, stop := productQueryStopWords[tok]; stop {
			continue
		}
		if isPriceOrPureNumberToken(tok) {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	if len(out) == 0 {
		return tokens
	}
	return out
}

func isPriceOrPureNumberToken(tok string) bool {
	if tok == "" {
		return true
	}
	digits := 0
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c >= '0' && c <= '9' {
			digits++
			continue
		}
		// Allow trailing price unit suffix like "300k", "250d", "20tr".
		rest := tok[i:]
		if digits > 0 && (rest == "k" || rest == "d" || rest == "tr" || rest == "vnd") {
			return true
		}
		return false
	}
	return digits == len(tok)
}
