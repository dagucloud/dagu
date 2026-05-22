// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestMergeToolOutputAuditDetailsHandlesStructOutput(t *testing.T) {
	details := map[string]any{}
	output := struct {
		DAGRunID string `json:"dagRunId"`
		RunURI   string `json:"runUri"`
		Applied  bool   `json:"applied"`
		Valid    bool   `json:"valid"`
	}{
		DAGRunID: "run-1",
		RunURI:   "dagu://runs/run-1",
		Applied:  true,
		Valid:    true,
	}

	mergeToolOutputAuditDetails(details, output)

	require.Equal(t, "run-1", details["dag_run_id"])
	require.Equal(t, "dagu://runs/run-1", details["run_uri"])
	require.Equal(t, true, details["applied"])
	require.Equal(t, true, details["valid"])
}

func TestSanitizeAuditStringTruncatesRunes(t *testing.T) {
	got := sanitizeAuditString(" あいう ", 2)

	require.Equal(t, "あい", got)
	require.True(t, utf8.ValidString(got))
}
