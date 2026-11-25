package policy

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/nexusmart/core/services/assistant/policies"
)

// HashEmbedDim is the fixed dimensionality of the deterministic mock embedding vector.
const HashEmbedDim = 256

const (
	bm25K1 = 1.5
	bm25B  = 0.75
	rrfK   = 60.0
)

type indexedChunk struct {
	chunk   Chunk
	tf      map[string]int
	docLen  int
	vec     []float32
	normTxt string
}

// RAG implements hybrid Vietnamese policy retrieval (BM25 + 256-dim hash cosine + RRF)
// over embedded or disk-configured Markdown policy documents.
type RAG struct {
	policiesDir string
	embedMode   string

	mu        sync.RWMutex
	chunks    []indexedChunk
	idf       map[string]float64
	avgDocLen float64
}

// NewRAG initializes a policy RAG retriever from embedded policies (or policiesDir when non-empty).
func NewRAG(policiesDir string, embedMode string) (*RAG, error) {
	mode := strings.ToLower(strings.TrimSpace(embedMode))
	if mode == "" {
		mode = "mock"
	}
	r := &RAG{
		policiesDir: strings.TrimSpace(policiesDir),
		embedMode:   mode,
	}
	if err := r.Reload(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// Reload reloads and re-indexes all policy Markdown documents safely under a write lock.
func (r *RAG) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var fsys fs.FS = policies.Files
	if r.policiesDir != "" {
		info, err := os.Stat(r.policiesDir)
		if err != nil {
			return fmt.Errorf("stat policies dir %q: %w", r.policiesDir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("policies path %q is not a directory", r.policiesDir)
		}
		fsys = os.DirFS(r.policiesDir)
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("read policy directory: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return errors.New("no policy markdown files found")
	}

	var allChunks []indexedChunk
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("read policy file %s: %w", name, err)
		}
		slug := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		parsed := chunkMarkdown(slug, string(raw))
		for _, ch := range parsed {
			indexText := ch.DocTitle + " " + ch.Section + " " + ch.Section + " " + ch.Content
			tokens := TokenizeVietnamese(indexText)
			tf := make(map[string]int, len(tokens)*2)
			for i, tok := range tokens {
				tf[tok]++
				if i+1 < len(tokens) {
					tf[tok+"_"+tokens[i+1]]++
				}
			}
			allChunks = append(allChunks, indexedChunk{
				chunk:   ch,
				tf:      tf,
				docLen:  len(tokens),
				vec:     HashEmbed256(indexText),
				normTxt: NormalizeVietnamese(indexText),
			})
		}
	}

	if len(allChunks) == 0 {
		return errors.New("policy corpus produced zero chunks")
	}

	df := make(map[string]int, 512)
	totalLen := 0
	for _, ic := range allChunks {
		totalLen += ic.docLen
		for tok := range ic.tf {
			df[tok]++
		}
	}

	n := float64(len(allChunks))
	idf := make(map[string]float64, len(df))
	for tok, freq := range df {
		idf[tok] = math.Log(1.0 + (n-float64(freq)+0.5)/(float64(freq)+0.5))
	}
	avgLen := float64(totalLen) / n
	if avgLen <= 0 {
		avgLen = 1
	}

	r.mu.Lock()
	r.chunks = allChunks
	r.idf = idf
	r.avgDocLen = avgLen
	r.mu.Unlock()
	return nil
}

// Search executes hybrid Vietnamese accent-insensitive BM25 + 256-dim hash cosine RRF retrieval.
func (r *RAG) Search(ctx context.Context, query string, topK int) ([]Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 4
	}
	normQuery := NormalizeVietnamese(query)
	if normQuery == "" {
		return []Chunk{}, nil
	}

	rawQueryTokens := TokenizeVietnamese(normQuery)
	queryTokens := filterPolicyQueryTokens(rawQueryTokens)
	queryBigrams := buildBigrams(rawQueryTokens)
	queryVec := HashEmbed256(normQuery)

	r.mu.RLock()
	chunks := r.chunks
	idf := r.idf
	avgLen := r.avgDocLen
	r.mu.RUnlock()

	if len(chunks) == 0 {
		return []Chunk{}, nil
	}

	type scored struct {
		idx         int
		bm25        float64
		cosine      float64
		rrf         float64
		bigramHits  int
		unigramHits int
		maxIDF      float64
	}

	items := make([]scored, len(chunks))
	bm25Order := make([]int, 0, len(chunks))
	vecOrder := make([]int, 0, len(chunks))

	for i, ic := range chunks {
		b, uniHits, maxTokIDF := scoreBM25WithStats(queryTokens, ic.tf, ic.docLen, avgLen, idf)
		biScore, biHits, _ := scoreBM25WithStats(queryBigrams, ic.tf, ic.docLen, avgLen, idf)
		totalBM25 := b + 1.5*biScore
		c := CosineSimilarity(queryVec, ic.vec)
		items[i] = scored{
			idx:         i,
			bm25:        totalBM25,
			cosine:      c,
			bigramHits:  biHits,
			unigramHits: uniHits,
			maxIDF:      maxTokIDF,
		}
		if totalBM25 > 0 {
			bm25Order = append(bm25Order, i)
		}
		if c >= 0.16 {
			vecOrder = append(vecOrder, i)
		}
	}

	sort.Slice(bm25Order, func(i, j int) bool {
		a, b := items[bm25Order[i]], items[bm25Order[j]]
		if a.bm25 != b.bm25 {
			return a.bm25 > b.bm25
		}
		return chunks[a.idx].chunk.ID < chunks[b.idx].chunk.ID
	})
	for rank, idx := range bm25Order {
		items[idx].rrf += 1.0 / (rrfK + float64(rank+1))
	}

	sort.Slice(vecOrder, func(i, j int) bool {
		a, b := items[vecOrder[i]], items[vecOrder[j]]
		if a.cosine != b.cosine {
			return a.cosine > b.cosine
		}
		return chunks[a.idx].chunk.ID < chunks[b.idx].chunk.ID
	})
	for rank, idx := range vecOrder {
		items[idx].rrf += 1.0 / (rrfK + float64(rank+1))
	}

	candidates := make([]scored, 0, len(chunks))
	for _, s := range items {
		if len(chunks) < 5 {
			if s.bm25 > 0 || s.cosine >= 0.24 {
				candidates = append(candidates, s)
			}
			continue
		}
		if len(queryTokens) <= 2 {
			if s.bm25 > 0 || s.cosine >= 0.30 {
				candidates = append(candidates, s)
			}
			continue
		}
		// Multi-word Vietnamese queries must match at least one compound bigram
		// (avoiding false positives from homonymous single syllables like du/thoi/ngay/nang)
		// or exhibit strong vector cosine similarity / very high lexical overlap.
		ratio := float64(s.unigramHits) / float64(len(queryTokens))
		if s.bigramHits > 0 || (ratio >= 0.65 && s.maxIDF >= 1.8) || s.cosine >= 0.35 {
			candidates = append(candidates, s)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rrf != candidates[j].rrf {
			return candidates[i].rrf > candidates[j].rrf
		}
		if candidates[i].bm25 != candidates[j].bm25 {
			return candidates[i].bm25 > candidates[j].bm25
		}
		if candidates[i].cosine != candidates[j].cosine {
			return candidates[i].cosine > candidates[j].cosine
		}
		return chunks[candidates[i].idx].chunk.ID < chunks[candidates[j].idx].chunk.ID
	})

	if len(candidates) > topK {
		candidates = candidates[:topK]
	}

	out := make([]Chunk, 0, len(candidates))
	for _, cand := range candidates {
		ch := chunks[cand.idx].chunk
		ch.Score = math.Round(cand.rrf*10000) / 10000
		out = append(out, ch)
	}
	return out, nil
}

// NormalizeVietnamese folds Vietnamese diacritics (both NFC precomposed and NFD combining marks)
// and đ/Đ -> d, lowercases text, and collapses whitespace into single spaces.
func NormalizeVietnamese(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		folded := foldVietnameseRune(r)
		if unicode.IsSpace(folded) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(folded)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

// TokenizeVietnamese splits Vietnamese text into accent-folded lowercase alphanumeric tokens.
func TokenizeVietnamese(s string) []string {
	norm := NormalizeVietnamese(s)
	if norm == "" {
		return nil
	}
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range norm {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// HashEmbed256 produces a deterministic L2-normalized 256-dim vector for Vietnamese/English text.
func HashEmbed256(text string) []float32 {
	vec := make([]float64, HashEmbedDim)
	tokens := TokenizeVietnamese(text)
	if len(tokens) == 0 {
		return make([]float32, HashEmbedDim)
	}

	addFeature := func(feat string, weight float64) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feat))
		sum := h.Sum64()
		bucket := sum % HashEmbedDim
		sign := 1.0
		if (sum>>8)&1 == 1 {
			sign = -1.0
		}
		vec[bucket] += sign * weight
	}

	for i, tok := range tokens {
		addFeature("u:"+tok, 1.5)
		if i+1 < len(tokens) {
			addFeature("b:"+tok+"_"+tokens[i+1], 2.0)
		}
		if len(tok) >= 3 {
			for j := 0; j <= len(tok)-3; j++ {
				addFeature("c:"+tok[j:j+3], 0.45)
			}
		}
	}

	var normSq float64
	for _, v := range vec {
		normSq += v * v
	}
	out := make([]float32, HashEmbedDim)
	if normSq == 0 {
		return out
	}
	invNorm := 1.0 / math.Sqrt(normSq)
	for i, v := range vec {
		out[i] = float32(v * invNorm)
	}
	return out
}

// CosineSimilarity computes cosine similarity between two equal-length float32 vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func chunkMarkdown(slug, content string) []Chunk {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	docTitle := slug
	var chunks []Chunk

	var curSection string
	var curBody strings.Builder

	flush := func() {
		body := strings.TrimSpace(curBody.String())
		if curSection == "" || body == "" {
			curBody.Reset()
			return
		}
		anchor := slugifyAnchor(curSection)
		citation := fmt.Sprintf("/policies/%s#%s", slug, anchor)
		chunks = append(chunks, Chunk{
			ID:       fmt.Sprintf("%s#%s", slug, anchor),
			DocSlug:  slug,
			DocTitle: docTitle,
			Section:  curSection,
			Citation: citation,
			Content:  body,
		})
		curBody.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			docTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			curSection = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if curSection != "" {
			curBody.WriteString(line)
			curBody.WriteByte('\n')
		}
	}
	flush()

	// Fallback if a custom policy markdown has no ## headings.
	if len(chunks) == 0 {
		body := strings.TrimSpace(content)
		if body != "" {
			anchor := slugifyAnchor(docTitle)
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s#%s", slug, anchor),
				DocSlug:  slug,
				DocTitle: docTitle,
				Section:  docTitle,
				Citation: fmt.Sprintf("/policies/%s#%s", slug, anchor),
				Content:  body,
			})
		}
	}
	return chunks
}

func slugifyAnchor(heading string) string {
	norm := NormalizeVietnamese(heading)
	var b strings.Builder
	lastDash := true
	for _, r := range norm {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "section"
	}
	return out
}

func buildBigrams(tokens []string) []string {
	if len(tokens) < 2 {
		return nil
	}
	out := make([]string, 0, len(tokens)-1)
	for i := 0; i+1 < len(tokens); i++ {
		out = append(out, tokens[i]+"_"+tokens[i+1])
	}
	return out
}

func scoreBM25WithStats(queryTokens []string, tf map[string]int, docLen int, avgDocLen float64, idf map[string]float64) (float64, int, float64) {
	if len(queryTokens) == 0 || docLen == 0 {
		return 0, 0, 0
	}
	seen := make(map[string]struct{}, len(queryTokens))
	var total float64
	var hits int
	var maxIDF float64
	for _, tok := range queryTokens {
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		freq := float64(tf[tok])
		if freq == 0 {
			continue
		}
		hits++
		tokenIDF := idf[tok]
		if tokenIDF <= 0 {
			tokenIDF = 0.1
		}
		if tokenIDF > maxIDF {
			maxIDF = tokenIDF
		}
		denom := freq + bm25K1*(1.0-bm25B+bm25B*(float64(docLen)/avgDocLen))
		total += tokenIDF * ((freq * (bm25K1 + 1.0)) / denom)
	}
	return total, hits, maxIDF
}

var policyStopWords = map[string]struct{}{
	"la": {}, "cua": {}, "va": {}, "co": {}, "duoc": {}, "khong": {}, "cho": {}, "toi": {},
	"minh": {}, "em": {}, "anh": {}, "chi": {}, "hoi": {}, "ve": {}, "nhu": {}, "the": {},
	"nao": {}, "gi": {}, "bao": {}, "nhieu": {}, "thi": {}, "sao": {}, "a": {}, "oi": {},
	"nhe": {}, "voi": {}, "de": {}, "trong": {}, "tren": {}, "san": {}, "nexusmart": {},
	"chinh": {}, "sach": {}, "quy": {}, "dinh": {}, "cac": {}, "mot": {}, "khi": {},
	"da": {}, "se": {}, "dang": {}, "tai": {}, "tu": {}, "den": {}, "o": {}, "nay": {},
	"mai": {}, "hom": {}, "cung": {}, "nhung": {}, "hay": {}, "neu": {}, "ma": {}, "vi": {},
}

func filterPolicyQueryTokens(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if _, isStop := policyStopWords[tok]; !isStop {
			filtered = append(filtered, tok)
		}
	}
	if len(filtered) == 0 {
		return tokens
	}
	return filtered
}

func foldVietnameseRune(r rune) rune {
	switch r {
	case 'à', 'á', 'ạ', 'ả', 'ã', 'â', 'ầ', 'ấ', 'ậ', 'ẩ', 'ẫ', 'ă', 'ằ', 'ắ', 'ặ', 'ẳ', 'ẵ', 'ä', 'å':
		return 'a'
	case 'è', 'é', 'ẹ', 'ẻ', 'ẽ', 'ê', 'ề', 'ế', 'ệ', 'ể', 'ễ', 'ë':
		return 'e'
	case 'ì', 'í', 'ị', 'ỉ', 'ĩ', 'î', 'ï':
		return 'i'
	case 'ò', 'ó', 'ọ', 'ỏ', 'õ', 'ô', 'ồ', 'ố', 'ộ', 'ổ', 'ỗ', 'ơ', 'ờ', 'ớ', 'ợ', 'ở', 'ỡ', 'ö', 'ø':
		return 'o'
	case 'ù', 'ú', 'ụ', 'ủ', 'ũ', 'ư', 'ừ', 'ứ', 'ự', 'ử', 'ữ', 'û', 'ü':
		return 'u'
	case 'ỳ', 'ý', 'ỵ', 'ỷ', 'ỹ', 'ÿ':
		return 'y'
	case 'đ', 'ð':
		return 'd'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	default:
		return r
	}
}
