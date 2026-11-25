package searchcfg

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nexusmart/core/pkg/common/textnorm"
)

//go:embed synonyms.json
var synonymsRaw []byte

// LoadSynonyms returns a fresh normalized copy of the embedded Vietnamese synonym dictionary.
func LoadSynonyms() (map[string][]string, error) {
	var raw map[string][]string
	if err := json.Unmarshal(synonymsRaw, &raw); err != nil {
		return nil, fmt.Errorf("parse embedded synonyms.json: %w", err)
	}
	out := make(map[string][]string, len(raw))
	for k, vals := range raw {
		nk := textnorm.NormalizeSearchText(k)
		if nk == "" {
			continue
		}
		seen := make(map[string]struct{}, len(vals))
		clean := make([]string, 0, len(vals))
		for _, v := range vals {
			nv := textnorm.NormalizeSearchText(v)
			if nv == "" || nv == nk {
				continue
			}
			if _, ok := seen[nv]; ok {
				continue
			}
			seen[nv] = struct{}{}
			clean = append(clean, nv)
		}
		if len(clean) > 0 {
			out[nk] = clean
		}
	}
	return out, nil
}

// MustLoadSynonyms loads the embedded synonym dictionary or panics on malformed static JSON.
func MustLoadSynonyms() map[string][]string {
	m, err := LoadSynonyms()
	if err != nil {
		panic(err)
	}
	return m
}

// SynonymGroups partitions synonyms into canonical concept group identifiers for deterministic embedding hashing.
func SynonymGroups(synonyms map[string][]string) map[string]string {
	parent := make(map[string]string, len(synonyms)*2)
	var find func(string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok {
			parent[x] = x
			return x
		}
		if p != x {
			parent[x] = find(p)
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}

	for k, vals := range synonyms {
		nk := strings.TrimSpace(k)
		if nk == "" {
			continue
		}
		find(nk)
		for _, v := range vals {
			nv := strings.TrimSpace(v)
			if nv == "" {
				continue
			}
			union(nk, nv)
		}
	}

	groups := make(map[string]string, len(parent))
	for term := range parent {
		groups[term] = "syn:" + find(term)
	}
	return groups
}
