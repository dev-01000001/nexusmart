package rrf

import "sort"

const (
	DefaultK             = 60.0
	DefaultKeywordWeight = 1.0
	DefaultVectorWeight  = 0.8
)

// ScoredID holds a document ID and its fused Reciprocal Rank Fusion score.
type ScoredID struct {
	ID    string
	Score float64
}

type entry struct {
	id      string
	score   float64
	kwRank  int
	vecRank int
}

// Fuse merges ranked keyword and vector document ID lists using weighted Reciprocal Rank Fusion.
// Higher scores appear first, with deterministic tie-breaking.
func Fuse(keywordIDs, vectorIDs []string, k, kwWeight, vecWeight float64) []ScoredID {
	if k <= 0 {
		k = DefaultK
	}
	if len(keywordIDs) == 0 && len(vectorIDs) == 0 {
		return []ScoredID{}
	}

	const noRank = int(^uint(0) >> 1)
	byID := make(map[string]*entry, len(keywordIDs)+len(vectorIDs))

	for i, id := range keywordIDs {
		if id == "" {
			continue
		}
		e, ok := byID[id]
		if !ok {
			e = &entry{id: id, kwRank: noRank, vecRank: noRank}
			byID[id] = e
		}
		if e.kwRank == noRank {
			rank := i + 1
			e.kwRank = rank
			e.score += kwWeight / (k + float64(rank))
		}
	}

	for i, id := range vectorIDs {
		if id == "" {
			continue
		}
		e, ok := byID[id]
		if !ok {
			e = &entry{id: id, kwRank: noRank, vecRank: noRank}
			byID[id] = e
		}
		if e.vecRank == noRank {
			rank := i + 1
			e.vecRank = rank
			e.score += vecWeight / (k + float64(rank))
		}
	}

	list := make([]*entry, 0, len(byID))
	for _, e := range byID {
		list = append(list, e)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		if list[i].kwRank != list[j].kwRank {
			return list[i].kwRank < list[j].kwRank
		}
		if list[i].vecRank != list[j].vecRank {
			return list[i].vecRank < list[j].vecRank
		}
		return list[i].id < list[j].id
	})

	out := make([]ScoredID, len(list))
	for i, e := range list {
		out[i] = ScoredID{ID: e.id, Score: e.score}
	}
	return out
}
