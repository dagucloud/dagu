// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Validation reads no inventory, so every case runs offline.
func TestNodeTargetingValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		wantError string
	}{
		{"valid.yaml", ""},
		{"unknown_field.yaml", `unknown field "rank_by"`},
		{"missing_labels.yaml", "field 'nodes.labels': is required"},
		{"max_concurrent_range.yaml", "must be an integer from 1 to 1000"},
		{"invalid_term.yaml", `invalid label filter "env==prod"`},
		{"wrong_action.yaml", "is allowed only on dag.run and on command steps"},
		{"with_worker_selector.yaml", "cannot be combined with worker_selector"},
		{"ssh_host.yaml", "field 'with.host': cannot be set on a step with nodes"},
		{"sftp_one_destination.yaml", "field 'with.destination': must use ${node.name}"},
		{"node_required_ssh.yaml", "cannot be combined with ssh"},
		{"node_required_nodes_step.yaml", "a node: required DAG runs on one node and cannot target more nodes"},
		{"node_value.yaml", `must be "required"`},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			if tc.wantError == "" {
				result.ExpectExitCode(0)
				return
			}
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantError)
		})
	}
}
