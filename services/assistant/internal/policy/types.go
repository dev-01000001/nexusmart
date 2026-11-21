package policy

import "context"

// Chunk represents a single retrievable policy section with a stable citation link.
type Chunk struct {
	ID         string  `json:"id"`
	DocSlug    string  `json:"doc_slug"`
	DocTitle   string  `json:"doc_title"`
	Section    string  `json:"section"`
	Citation   string  `json:"citation"`
	Content    string  `json:"content"`
	Score      float64 `json:"score,omitempty"`
}

// Retriever retrieves relevant policy sections from the embedded/configured repo policy corpus.
type Retriever interface {
	Search(ctx context.Context, query string, topK int) ([]Chunk, error)
	Reload(ctx context.Context) error
}
