// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "github.com/dagucloud/dagu/internal/cmn/collections"

// ParallelConfig contains the configuration for parallel execution of a step.
// MVP version supports basic parallel execution with max_concurrent control.
type ParallelConfig struct {
	// Variable is the name of a variable that contains the json array of items to process in parallel.
	Variable string `json:"variable,omitempty"`

	// Items is the array of items to process in parallel.
	// Can be a direct array or a reference to a variable containing an array.
	Items []ParallelItem `json:"items,omitempty"`

	// MaxConcurrent is the maximum number of parallel executions.
	// Default is 10 if not specified.
	MaxConcurrent int `json:"max_concurrent,omitempty"`
}

// DefaultMaxConcurrent is the default maximum concurrent executions for parallel steps.
const DefaultMaxConcurrent = 10

// MaxExpansionConcurrency is the maximum concurrency accepted by expansion constructs.
const MaxExpansionConcurrency = 1000

// ParallelItemVariable is the special variable name that represents the current item in parallel execution
const ParallelItemVariable = "ITEM"

// ParallelItem represents a single item to be processed in parallel.
// It can be either a simple value or a set of parameters.
type ParallelItem struct {
	// Value is used for simple string items or variable references
	// E.g. "item1", "item2", "${ITEM_VAR}"
	Value string `json:"value,omitempty"`

	// Params is used for key-value pairs that will be passed as parameters
	// E.g. {"SOURCE": "s3://customers", "TYPE": "csv"}
	// Uses DeterministicMap to ensure consistent JSON marshaling for hashing
	Params collections.DeterministicMap `json:"params,omitempty"`
}
