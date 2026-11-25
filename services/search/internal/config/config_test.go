package config

import "testing"

func TestConfigValidate_MockDefaultAndOpenAIKeyRequired(t *testing.T) {
	cfg := Load()
	if cfg.EmbeddingMode != EmbeddingModeMock {
		t.Fatalf("default EmbeddingMode = %q, want %q", cfg.EmbeddingMode, EmbeddingModeMock)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default mock config should validate, got: %v", err)
	}

	// G12: EMBEDDING_MODE=openai without EMBEDDING_API_KEY must refuse startup.
	badOpenAI := cfg
	badOpenAI.EmbeddingMode = EmbeddingModeOpenAI
	badOpenAI.EmbeddingAPIKey = "   "
	if err := badOpenAI.Validate(); err == nil {
		t.Fatalf("expected validation error when EMBEDDING_MODE=openai and EMBEDDING_API_KEY is empty")
	}

	goodOpenAI := badOpenAI
	goodOpenAI.EmbeddingAPIKey = "sk-test-local-only"
	if err := goodOpenAI.Validate(); err != nil {
		t.Fatalf("expected openai config with key to validate, got: %v", err)
	}

	badMode := cfg
	badMode.EmbeddingMode = "unknown-provider"
	if err := badMode.Validate(); err == nil {
		t.Fatalf("expected error for unsupported EMBEDDING_MODE")
	}

	badDim := cfg
	badDim.EmbeddingDim = 768
	if err := badDim.Validate(); err == nil {
		t.Fatalf("expected error for mismatched EMBEDDING_DIM")
	}

	if cfg.VectorMinSimilarity != DefaultVectorMinSimilarity {
		t.Fatalf("default VectorMinSimilarity = %v, want %v", cfg.VectorMinSimilarity, DefaultVectorMinSimilarity)
	}
	badSim := cfg
	badSim.VectorMinSimilarity = 1.5
	if err := badSim.Validate(); err == nil {
		t.Fatalf("expected error for out-of-range SEARCH_VECTOR_MIN_SIMILARITY")
	}
}
