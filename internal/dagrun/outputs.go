// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

// DAGRunOutputs represents the full outputs file structure with metadata.
type DAGRunOutputs struct {
	Metadata OutputsMetadata   `json:"metadata"`
	Outputs  map[string]string `json:"outputs"`
}

// OutputsMetadata contains execution context for the outputs.
type OutputsMetadata struct {
	DAGName     string `json:"dagName"`
	DAGRunID    string `json:"dagRunId"`
	AttemptID   string `json:"attemptId"`
	Status      string `json:"status"`
	CompletedAt string `json:"completedAt"`
	Params      string `json:"params,omitempty"` // JSON-serialized parameters
}
