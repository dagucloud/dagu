// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"sort"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
)

const (
	dagNameSuggestionLimit = 3
	// dagNameSuggestionScan bounds how many DAG names are considered; the
	// list API caps perPage at this value.
	dagNameSuggestionScan = 1000
)

// didYouMeanDetails wraps close DAG-name matches as error details, or
// returns nil when there is nothing to suggest.
func (svc *Service) didYouMeanDetails(ctx context.Context, name string) map[string]any {
	suggestions := svc.dagNameSuggestions(ctx, name)
	if len(suggestions) == 0 {
		return nil
	}
	return map[string]any{"didYouMean": suggestions}
}

// dagNameSuggestions returns up to three existing DAG names that closely
// resemble the requested name, for did-you-mean error details. Lookup
// failures return no suggestions; the caller already reports the primary
// error.
func (svc *Service) dagNameSuggestions(ctx context.Context, name string) []string {
	if svc.api == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	raw, err := svc.api.GetDAGsListDataIncludingAltDirs(ctx, "perPage="+strconv.Itoa(dagNameSuggestionScan))
	if err != nil {
		return nil
	}
	var dags []daguapi.DAGFile
	switch data := raw.(type) {
	case daguapi.ListDAGs200JSONResponse:
		dags = data.Dags
	case *daguapi.ListDAGs200JSONResponse:
		dags = data.Dags
	default:
		return nil
	}
	candidates := make([]string, 0, len(dags))
	for _, dag := range dags {
		candidate := dag.FileName
		if candidate == "" {
			candidate = dag.Dag.Name
		}
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	return rankNameSuggestions(name, candidates)
}

// rankNameSuggestions orders candidates by similarity to name and keeps the
// closest matches: substring matches first, then names within a small edit
// distance.
func rankNameSuggestions(name string, candidates []string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}

	type scored struct {
		name  string
		score int
	}
	maxDistance := max(2, len(name)/3)
	ranked := make([]scored, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		lower := strings.ToLower(candidate)
		if lower == name {
			continue
		}
		score := editDistance(name, lower)
		if strings.Contains(lower, name) || strings.Contains(name, lower) {
			score = min(score, 1)
		}
		if score > maxDistance {
			continue
		}
		ranked = append(ranked, scored{name: candidate, score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score < ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	out := make([]string, 0, dagNameSuggestionLimit)
	for _, entry := range ranked {
		out = append(out, entry.name)
		if len(out) == dagNameSuggestionLimit {
			break
		}
	}
	return out
}

// editDistance computes the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}
