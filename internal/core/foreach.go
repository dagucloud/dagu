// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

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
