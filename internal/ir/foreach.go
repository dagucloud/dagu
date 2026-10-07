// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// ForeachConfig contains the configuration for inline item-body iteration.
type ForeachConfig struct {
	// Items is the static list of items to iterate.
	Items []any `json:"items,omitempty"`

	// ItemsExpr is a value-resolved expression that must produce a JSON array.
	ItemsExpr string `json:"itemsExpr,omitempty"`

	// As is the item alias exposed under the foreach namespace.
	As string `json:"as,omitempty"`

	// Key is an optional item key expression.
	Key string `json:"key,omitempty"`

	// MaxConcurrent is the maximum number of item bodies running at once.
	MaxConcurrent int `json:"maxConcurrent,omitempty"`

	// Steps is the item body graph.
	Steps []Step `json:"steps,omitempty"`

	// Collect maps output names to value-resolved expressions.
	Collect map[string]string `json:"collect,omitempty"`
}

// ForeachItems lists the items a foreach step expanded to. It is recorded
// before any item body runs, so a reader knows the total ahead of the results.
type ForeachItems struct {
	Total int              `json:"total"`
	Items []ForeachItemRef `json:"items"`
}

// ForeachItemRef identifies one expanded item.
type ForeachItemRef struct {
	Index int    `json:"index"`
	Key   string `json:"key"`
}

// ForeachItemStatus records the body run of one foreach item. It is kept
// beside the item's body logs, outside the run status, so that a foreach over
// many items does not grow every status snapshot.
type ForeachItemStatus struct {
	Index      int                     `json:"index"`
	Key        string                  `json:"key"`
	Status     NodeStatus              `json:"status"`
	Error      string                  `json:"error,omitempty"`
	StartedAt  string                  `json:"startedAt,omitempty"`
	FinishedAt string                  `json:"finishedAt,omitempty"`
	Steps      []ForeachBodyStepStatus `json:"steps"`
}

// ForeachBodyStepStatus records one body step of a foreach item. Stdout and
// Stderr are file names relative to the item's directory.
type ForeachBodyStepStatus struct {
	Name       string     `json:"name"`
	ID         string     `json:"id,omitempty"`
	Status     NodeStatus `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  string     `json:"startedAt,omitempty"`
	FinishedAt string     `json:"finishedAt,omitempty"`
	RetryCount int        `json:"retryCount,omitempty"`
	Stdout     string     `json:"stdout,omitempty"`
	Stderr     string     `json:"stderr,omitempty"`
	// Foreach marks a body step that is itself a foreach, whose items live
	// in a nested directory.
	Foreach bool `json:"foreach,omitempty"`
}
