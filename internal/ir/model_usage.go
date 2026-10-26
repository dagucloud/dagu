// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"cmp"
	"slices"
)

// ModelUsage is what calls to one model used: how many there were, their
// tokens, and the cost the provider reported for them.
type ModelUsage struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Calls    int64  `json:"calls"`
	// InputTokens counts the prompts, CachedInputTokens and
	// CacheWriteTokens included. OutputTokens counts the completions,
	// ReasoningTokens included.
	InputTokens       int64 `json:"inputTokens,omitempty"`
	CachedInputTokens int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens  int64 `json:"cacheWriteTokens,omitempty"`
	OutputTokens      int64 `json:"outputTokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoningTokens,omitempty"`
	// Cost sums what the provider reported CostReportedCalls of the calls
	// cost, in US dollars. A call whose cost went unreported adds nothing,
	// so Cost is the whole cost only when CostReportedCalls equals Calls.
	Cost              float64 `json:"cost,omitempty"`
	CostReportedCalls int64   `json:"costReportedCalls,omitempty"`
}

// Add adds other's calls, tokens, and cost to u.
func (u *ModelUsage) Add(other ModelUsage) {
	u.Calls += other.Calls
	u.InputTokens += other.InputTokens
	u.CachedInputTokens += other.CachedInputTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	u.OutputTokens += other.OutputTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.Cost += other.Cost
	u.CostReportedCalls += other.CostReportedCalls
}

// StepModelUsage is what a step's calls to models used, over every attempt
// of the step in its run.
type StepModelUsage struct {
	// Models is the usage of each model the step called.
	Models []ModelUsage `json:"models,omitempty"`
}

// CloneStepModelUsage returns a copy of usage that shares nothing with it.
func CloneStepModelUsage(usage *StepModelUsage) *StepModelUsage {
	if usage == nil {
		return nil
	}
	return &StepModelUsage{Models: slices.Clone(usage.Models)}
}

// MergeModelUsage adds usage to into, one entry per provider and model, and
// returns the entries ordered by provider and model.
func MergeModelUsage(into []ModelUsage, usage ...ModelUsage) []ModelUsage {
	for _, entry := range usage {
		i := slices.IndexFunc(into, func(existing ModelUsage) bool {
			return existing.Provider == entry.Provider && existing.Model == entry.Model
		})
		if i < 0 {
			into = append(into, entry)
			continue
		}
		into[i].Add(entry)
	}
	slices.SortFunc(into, func(a, b ModelUsage) int {
		return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Model, b.Model))
	})
	return into
}

// ModelUsageTotal sums what every step of the run, its handlers included,
// used, by model. It is nil when no step reports any usage.
func (st *DAGRunStatus) ModelUsageTotal() []ModelUsage {
	var total []ModelUsage
	for _, node := range st.NodesInRunOrder() {
		if node.ModelUsage != nil {
			total = MergeModelUsage(total, node.ModelUsage.Models...)
		}
	}
	return total
}
