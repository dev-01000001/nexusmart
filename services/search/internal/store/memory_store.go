package store

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is a thread-safe in-memory Store implementation with exact cosine similarity
// for fast unit tests without external dependencies.
type MemoryStore struct {
	mu          sync.RWMutex
	docs        map[string]ProductDocument
	processed   map[string]string
	deadLetters []DeadLetterRecord
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		docs:      make(map[string]ProductDocument),
		processed: make(map[string]string),
	}
}

func (m *MemoryStore) UpsertDocument(_ context.Context, doc ProductDocument) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.upsertLocked(doc), nil
}

func (m *MemoryStore) UpsertDocumentsBatch(_ context.Context, docs []ProductDocument) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, doc := range docs {
		m.upsertLocked(doc)
	}
	return nil
}

func (m *MemoryStore) upsertLocked(doc ProductDocument) bool {
	if existing, ok := m.docs[doc.ProductID]; ok {
		if doc.Version > 0 && doc.Version < existing.Version {
			return false
		}
		if existing.Version > doc.Version {
			doc.Version = existing.Version
		}
	}
	cloned := cloneDoc(doc)
	if cloned.IndexedAt.IsZero() {
		cloned.IndexedAt = time.Now().UTC()
	}
	m.docs[doc.ProductID] = cloned
	return true
}

func (m *MemoryStore) DeleteDocument(_ context.Context, productID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, productID)
	return nil
}

func (m *MemoryStore) GetDocument(_ context.Context, productID string) (*ProductDocument, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	doc, ok := m.docs[productID]
	if !ok {
		return nil, ErrNotFound
	}
	cloned := cloneDoc(doc)
	return &cloned, nil
}

func (m *MemoryStore) GetDocumentVersion(_ context.Context, productID string) (int64, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	doc, ok := m.docs[productID]
	if !ok {
		return 0, false, nil
	}
	return doc.Version, true, nil
}

func (m *MemoryStore) VectorSearch(_ context.Context, queryEmbedding []float32, filter VectorSearchFilter) ([]VectorHit, error) {
	if len(queryEmbedding) == 0 {
		return []VectorHit{}, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultVectorLimit
	}
	if limit > maxVectorLimit {
		limit = maxVectorLimit
	}

	shopID := strings.TrimSpace(filter.ShopID)
	category := strings.TrimSpace(filter.Category)
	excludeID := strings.TrimSpace(filter.ExcludeProductID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	hits := make([]VectorHit, 0, len(m.docs))
	for _, doc := range m.docs {
		if excludeID != "" && doc.ProductID == excludeID {
			continue
		}
		if shopID != "" && doc.ShopID != shopID {
			continue
		}
		if category != "" && doc.CategorySlug != category && doc.CategoryID != category {
			continue
		}
		if filter.MinPriceVND > 0 && doc.MinPriceVND < filter.MinPriceVND {
			continue
		}
		if filter.MaxPriceVND > 0 && doc.MinPriceVND > filter.MaxPriceVND {
			continue
		}
		if filter.InStockOnly && !doc.InStock {
			continue
		}
		score := cosineSimilarity(queryEmbedding, doc.Embedding)
		hits = append(hits, VectorHit{
			Doc:   cloneDoc(doc),
			Score: score,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Doc.ProductID < hits[j].Doc.ProductID
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (m *MemoryStore) CountDocuments(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.docs)), nil
}

func (m *MemoryStore) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.processed[eventID]
	return ok, nil
}

func (m *MemoryStore) TryMarkEventProcessed(_ context.Context, eventID, subject string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.processed[eventID]; exists {
		return false, nil
	}
	m.processed[eventID] = subject
	return true, nil
}

func (m *MemoryStore) InsertDeadLetter(_ context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	m.deadLetters = append(m.deadLetters, DeadLetterRecord{
		ID:           uuid.NewString(),
		EventID:      eventID,
		Subject:      subject,
		Payload:      cp,
		ErrorMessage: errMsg,
		Attempts:     attempts,
		CreatedAt:    time.Now().UTC(),
	})
	return nil
}

// DeadLetters returns a snapshot copy of all recorded dead-letter events (for tests).
func (m *MemoryStore) DeadLetters() []DeadLetterRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DeadLetterRecord, len(m.deadLetters))
	copy(out, m.deadLetters)
	return out
}

func (m *MemoryStore) Ping(_ context.Context) error {
	return nil
}

func cloneDoc(doc ProductDocument) ProductDocument {
	cp := doc
	if doc.Images != nil {
		cp.Images = make([]string, len(doc.Images))
		copy(cp.Images, doc.Images)
	} else {
		cp.Images = []string{}
	}
	if doc.Embedding != nil {
		cp.Embedding = make([]float32, len(doc.Embedding))
		copy(cp.Embedding, doc.Embedding)
	}
	return cp
}

func cosineSimilarity(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
