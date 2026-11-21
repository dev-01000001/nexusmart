package rrf

import (
	"math"
	"testing"
)

func TestFuseCombinesAndBoostsOverlap(t *testing.T) {
	kw := []string{"p1", "p2", "p3"}
	vec := []string{"p2", "p4", "p1"}

	got := Fuse(kw, vec, DefaultK, DefaultKeywordWeight, DefaultVectorWeight)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}

	// p2 is rank 2 in kw (1/62) + rank 1 in vec (0.8/61) = 0.0292438...
	// p1 is rank 1 in kw (1/61) + rank 3 in vec (0.8/63) = 0.0290919...
	if got[0].ID != "p2" || got[1].ID != "p1" {
		t.Fatalf("unexpected top 2 order: %+v", got)
	}
	wantP2 := 1.0/62.0 + 0.8/61.0
	if math.Abs(got[0].Score-wantP2) > 1e-12 {
		t.Fatalf("p2 score = %.12f, want %.12f", got[0].Score, wantP2)
	}
	// p3 (1/63 = 0.01587) > p4 (0.8/62 = 0.01290)
	if got[2].ID != "p3" || got[3].ID != "p4" {
		t.Fatalf("unexpected tail order: %+v", got)
	}
}

func TestFuseDeterministicTieBreakAndDedupe(t *testing.T) {
	kw := []string{"a", "a", "b"}
	vec := []string{"c", "d"}

	first := Fuse(kw, vec, 60, 1.0, 1.0)
	for i := 0; i < 10; i++ {
		again := Fuse(kw, vec, 60, 1.0, 1.0)
		if len(again) != len(first) {
			t.Fatalf("length mismatch on iteration %d", i)
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("non-deterministic result at idx %d: %+v vs %+v", j, again[j], first[j])
			}
		}
	}
	// a (kw rank 1) and c (vec rank 1) have identical score with equal weights; keyword rank wins tie-break.
	if first[0].ID != "a" || first[1].ID != "c" {
		t.Fatalf("expected keyword tie-break winner 'a' before 'c', got %+v", first)
	}
}

func TestFuseEmptyInputs(t *testing.T) {
	got := Fuse(nil, nil, 0, 1.0, 0.8)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %+v", got)
	}
}
