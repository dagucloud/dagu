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

func TestResourceAuditDetailsIdentifiesStepLog(t *testing.T) {
	details := resourceAuditDetails("dagu://runs/nightly/run-1/steps/build/logs")

	require.Equal(t, "dag_run_step_log", details["resource_type"])
	require.Equal(t, "nightly/run-1/build", details["resource_id"])
}

func TestResourceAuditDetailsIdentifiesLegacyWikiURI(t *testing.T) {
	details := resourceAuditDetails("dagu://docs/operations/runbooks%2Frestart")

	require.Equal(t, "document", details["resource_type"])
	require.Equal(t, "operations/runbooks/restart", details["resource_id"])
}

func TestResourceAuditDetailsPreservesInvalidLegacyWikiURI(t *testing.T) {
	const uri = "dagu://docs/operations/runbooks/restart"
	details := resourceAuditDetails(uri)

	require.Equal(t, "resource", details["resource_type"])
	require.Equal(t, uri, details["resource_id"])
}

func TestDocumentChangeAuditMetadataUsesWorkspaceAndContentSize(t *testing.T) {
	metadata := changeAuditMetadata(changeInput{
		Mode:      changeModeApply,
		Type:      changeTypeUpsertWikiPage,
		Workspace: "operations",
		Path:      "runbooks/restart",
		Content:   "# Restart",
	})

	require.Equal(t, "doc", metadata.ResourceType)
	require.Equal(t, "runbooks/restart", metadata.ResourceID)
	require.Equal(t, "operations", metadata.Workspace)
	require.Equal(t, len("# Restart"), metadata.Attributes["content_bytes"])
}

func TestDAGChangeAuditMetadata(t *testing.T) {
	tests := []struct {
		name    string
		input   changeInput
		wantNew string
	}{
		{
			name: "rename",
			input: changeInput{
				Mode:    changeModeApply,
				Type:    changeTypeRenameDAG,
				Name:    "old",
				NewName: "new",
			},
			wantNew: "new",
		},
		{
			name: "delete",
			input: changeInput{
				Mode: changeModeApply,
				Type: changeTypeDeleteDAG,
				Name: "old",
			},
		},
		{
			name: "set profile",
			input: changeInput{
				Mode:    changeModeApply,
				Type:    changeTypeSetDAGProfile,
				Name:    "old",
				Profile: "fudosan",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := changeAuditMetadata(test.input)

			require.Equal(t, "dag", metadata.ResourceType)
			require.Equal(t, test.input.Name, metadata.ResourceID)
			require.Equal(t, test.input.Name, metadata.Attributes["dag_name"])
			if test.wantNew != "" {
				require.Equal(t, test.wantNew, metadata.Attributes["new_dag_name"])
			}
			if test.input.Profile != "" {
				require.Equal(t, test.input.Profile, metadata.Attributes["profile"])
			}
		})
	}
}
