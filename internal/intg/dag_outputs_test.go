// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// Root outputs project from the status a real run stores (Spec 106).
func TestDAGOutputsProjectFromRealRun(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	testDAG := th.DAG(t, `
outputs:
  summary: ${diagnose.outputs.summary}
  findings: ${diagnose.outputs.findings}
  skipped: ${later.outputs.value}
  report: ${publish.outputs.report}
  count: ${publish.outputs.count}
steps:
  - id: diagnose
    run: |
      printf 'summary=all good\n' >> "$DAGU_OUTPUT_FILE"
      printf 'findings<<JSON\n{"a":[1,2]}\nJSON\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: summary
      - name: findings
        type: json
  - id: later
    depends: diagnose
    preconditions:
      - condition: "1"
        expected: "2"
    run: printf 'value=x\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: value
  - id: publish
    action: outputs.write
    with:
      values:
        report:
          total: 3
        count: 3
`)
	testDAG.Agent().RunSuccess(t)
	testDAG.AssertLatestStatus(t, ir.Succeeded)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
	require.NoError(t, err)
	got := ir.ProjectDAGOutputs(testDAG.DAG, &status)
	require.Len(t, got, 4)
	require.JSONEq(t, `"all good"`, string(got["summary"]))
	require.JSONEq(t, `{"a":[1,2]}`, string(got["findings"]))
	require.JSONEq(t, `{"total":3}`, string(got["report"]))
	require.JSONEq(t, `3`, string(got["count"]))
}
