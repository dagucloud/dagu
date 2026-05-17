// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestDaguActionRunsSourceBundleDAG(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	actionDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "dagu-action.yaml"), []byte(`
apiVersion: v1alpha1
name: echo-action
dag: action.yaml
inputs:
  type: object
  additionalProperties: false
  required: [TEXT]
  properties:
    TEXT:
      type: string
outputs:
  type: object
  additionalProperties: false
  required: [RESULT]
  properties:
    RESULT:
      type: string
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "action.yaml"), []byte(`
name: source-action-child
params:
  - TEXT
steps:
  - run: echo "action says ${TEXT}"
    output: RESULT
`), 0o600))

	dag := th.DAG(t, `
type: graph
steps:
  - id: call_action
    action: `+strconv.Quote("source:"+actionDir+"@local")+`
    with:
      TEXT: hello
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	require.Equal(t, "source-action-child", status.Nodes[0].SubRuns[0].DAGName)
	require.NotEmpty(t, status.Nodes[0].SubRuns[0].DAGRunID)

	stdout, err := os.ReadFile(status.Nodes[0].Stdout)
	require.NoError(t, err)
	require.JSONEq(t, `{"RESULT":"action says hello"}`, string(stdout))
}

func TestDaguActionPublishesCallerOutputs(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	actionDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "dagu-action.yaml"), []byte(`
apiVersion: v1alpha1
name: notify-action
dag: action.yaml
outputs:
  type: object
  additionalProperties: false
  required: [messageId, status]
  properties:
    messageId:
      type: string
    status:
      type: string
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "action.yaml"), []byte(`
name: notify-action-child
steps:
  - id: publish
    action: outputs.write
    with:
      values:
        messageId: msg-123
        status: sent
`), 0o600))

	dag := th.DAG(t, `
type: graph
steps:
  - id: call_action
    action: `+strconv.Quote("source:"+actionDir+"@local")+`

  - id: audit
    depends: [call_action]
    action: log.write
    with:
      message: "message=${call_action.outputs.messageId} status=${call_action.outputs.status}"
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, status.Nodes, 2)
	require.NotNil(t, status.Nodes[0].OutputsValue)
	require.JSONEq(t, `{"messageId":"msg-123","status":"sent"}`, *status.Nodes[0].OutputsValue)

	stdout, err := os.ReadFile(status.Nodes[1].Stdout)
	require.NoError(t, err)
	require.Contains(t, string(stdout), "message=msg-123 status=sent")
}
