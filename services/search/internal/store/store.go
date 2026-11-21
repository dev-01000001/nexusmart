package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when a document does not exist in the search store.
var ErrNotFound = errors.New("document not found")

// ProductDocument represents an indexed product document with its pgvector embedding.
type ProductDocument struct {
	ProductID        string
	ShopID           string
	CategoryID       string
	CategorySlug     string
	CategoryName     string
	Name             string
	Description      string
	Images           []string
	Status           string
	MinPriceVND      int64
	MaxPriceVND      int64
	TotalStock       int32
	InStock          bool
	Version          int64
	Embedding        []float32
	ProductCreatedAt time.Time
	ProductUpdatedAt time.Time
	IndexedAt        time.Time
}

// VectorSearchFilter filters cosine similarity search results.
type VectorSearchFilter struct {
	ShopID           string
	Category         string // matches category_slug OR category_id
	MinPriceVND      int64
	MaxPriceVND      int64
	InStockOnly      bool
	ExcludeProductID string
	Limit            int
}

// HasFilter reports whether any selective filter predicate is set.
func (f VectorSearchFilter) HasFilter() bool {
	return strings.TrimSpace(f.ShopID) != "" ||
		strings.TrimSpace(f.Category) != "" ||
		f.MinPriceVND > 0 ||
		f.MaxPriceVND > 0 ||
		f.InStockOnly ||
		strings.TrimSpace(f.ExcludeProductID) != ""
}

// VectorHit pairs a product document with its cosine similarity score.
type VectorHit struct {
	Doc   ProductDocument
	Score float64
}

// DeadLetterRecord represents a persisted poison event record.
type DeadLetterRecord struct {
	ID           string
	EventID      string
	Subject      string
	Payload      []byte
	ErrorMessage string
	Attempts     int
	CreatedAt    time.Time
}

// Store abstracts Postgres/pgvector persistence and in-memory test storage.
type Store interface {
	UpsertDocument(ctx context.Context, doc ProductDocument) (applied bool, err error)
	UpsertDocumentsBatch(ctx context.Context, docs []ProductDocument) error
	DeleteDocument(ctx context.Context, productID string) error
	GetDocument(ctx context.Context, productID string) (*ProductDocument, error)
	GetDocumentVersion(ctx context.Context, productID string) (version int64, exists bool, err error)
	VectorSearch(ctx context.Context, queryEmbedding []float32, filter VectorSearchFilter) ([]VectorHit, error)
	CountDocuments(ctx context.Context) (int64, error)
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	TryMarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
	Ping(ctx context.Context) error
}

// FormatVector encodes a float32 slice into pgvector text literal syntax "[v1,v2,...]".
func FormatVector(vec []float32) string {
	var b strings.Builder
	b.Grow(len(vec)*10 + 2)
	b.WriteByte('[')
	var numBuf [32]byte
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			f = 0
		}
		b.Write(strconv.AppendFloat(numBuf[:0], f, 'f', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ParseVector decodes a pgvector text representation "[v1,v2,...]" into []float32.
func ParseVector(raw string) ([]float32, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return nil, fmt.Errorf("invalid vector format: %q", raw)
	}
	inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if inner == "" {
		return []float32{}, nil
	}
	parts := strings.Split(inner, ",")
	out := make([]float32, len(parts))
	for i, part := range parts {
		val, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector element %d: %w", i, err)
		}
		out[i] = float32(val)
	}
	return out, nil
}
