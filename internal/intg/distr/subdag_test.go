// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestActionOutputsFromDistributedWorker(t *testing.T) {
	t.Run("childWorker", func(t *testing.T) {
		actionDir := writeActionOutputBundle(t, `
name: notify-action-child
worker_selector:
  type: test-worker
steps:
  - id: publish
    action: outputs.write
    with:
      values:
        messageId: msg-123
        worker: remote-worker
`)

		f := newTestFixture(t, `
type: graph
steps:
  - id: call_action
    action: `+strconv.Quote("source:"+actionDir+"@local")+`

  - id: audit
    depends: [call_action]
    action: log.write
    with:
      message: "message=${call_action.outputs.messageId} worker=${call_action.outputs.worker}"
`, withLabels(map[string]string{"type": "test-worker"}), withLogPersistence())

		f.dagWrapper.Agent().RunSuccess(t)
		status, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)

		callAction := requireNodeByID(t, status, "call_action")
		require.NotNil(t, callAction.OutputsValue)
		require.JSONEq(t, `{"messageId":"msg-123","worker":"remote-worker"}`, *callAction.OutputsValue)

		audit := requireNodeByID(t, status, "audit")
		auditLog, err := os.ReadFile(audit.Stdout)
		require.NoError(t, err)
		require.Contains(t, string(auditLog), "message=msg-123 worker=remote-worker")
	})

}

func writeActionOutputBundle(t *testing.T, actionYAML string) string {
	t.Helper()
	actionDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "dagu-action.yaml"), []byte(`
apiVersion: v1alpha1
name: notify-action
dag: workflow.yaml
outputs:
  type: object
  additionalProperties: false
  required: [messageId, worker]
  properties:
    messageId:
      type: string
    worker:
      type: string
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(actionDir, "workflow.yaml"), []byte(actionYAML), 0o600))
	return actionDir
}

func requireNodeByID(t *testing.T, status ir.DAGRunStatus, id string) *ir.Node {
	t.Helper()
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		if node.Step.ID == id || node.Step.Name == id {
			return node
		}
	}
	require.Failf(t, "missing node", "node %q not found", id)
	return nil
}

func TestSubDAG_LocalCallsDistributed(t *testing.T) {
	t.Run("localParentCallsDistributedChild", func(t *testing.T) {
		f := newTestFixture(t, `
steps:
  - name: run-local-on-worker
    action: dag.run
    with:
      dag: local-sub
    output: RESULT

---
name: local-sub
worker_selector:
  type: test-worker
steps:
  - name: worker-task
    run: echo "Hello from worker"
    output: MESSAGE
`, withLabels(map[string]string{"type": "test-worker"}))

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)
		f.dagWrapper.AssertLatestStatus(t, ir.Succeeded)
	})
}

func TestSubDAG_FileWorkerSelectorEnv(t *testing.T) {
	f := newTestFixture(t, `
steps:
  - name: run-child
    action: dag.run
    with:
      dag: env-selected-child
`, withLabels(map[string]string{"host": "serverA"}))

	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "env-selected-child", []byte(`
name: env-selected-child
env:
  TARGET_HOST: serverA
worker_selector:
  host: ${TARGET_HOST}
steps:
  - name: child-task
    run: echo "child executed on selected worker"
`))

	agent := f.dagWrapper.Agent()
	agent.RunSuccess(t)

	parentStatus := agent.Status(f.coord.Context)
	require.Len(t, parentStatus.Nodes, 1)
	require.Len(t, parentStatus.Nodes[0].SubRuns, 1)

	subRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID
	subAttempt, err := f.coord.DAGRunRepository.FindSubAttempt(
		f.coord.Context,
		ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID),
		subRunID,
	)
	require.NoError(t, err)

	childStatus, err := subAttempt.ReadStatus(f.coord.Context)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, childStatus.Status)
	require.Equal(t, "worker-1", childStatus.WorkerID)
}

func TestSubDAG_CallStepWorkerSelector(t *testing.T) {
	t.Run("immediateParentDispatchesChildUsingCallStepSelector", func(t *testing.T) {
		f := newTestFixture(t, `
steps:
  - name: run-child-on-selected-worker
    action: dag.run
    with:
      dag: selected-child
    worker_selector:
      host: serverA

---
name: selected-child
steps:
  - name: child-task
    run: echo "child executed on selected worker"
`, withLabels(map[string]string{"host": "serverA"}))
		defer f.cleanup()

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)

		parentStatus := agent.Status(f.coord.Context)
		require.Len(t, parentStatus.Nodes, 1)
		require.Len(t, parentStatus.Nodes[0].SubRuns, 1)

		subRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID
		subAttempt, err := f.coord.DAGRunRepository.FindSubAttempt(
			f.coord.Context,
			ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID),
			subRunID,
		)
		require.NoError(t, err)

		childStatus, err := subAttempt.ReadStatus(f.coord.Context)
		require.NoError(t, err)
		require.NotNil(t, childStatus)
		require.Equal(t, ir.Succeeded, childStatus.Status)
		require.Equal(t, "worker-1", childStatus.WorkerID)
	})
}

func TestSubDAG_FailurePropagation(t *testing.T) {
	t.Run("childFailurePropagatesToParent", func(t *testing.T) {
		f := newTestFixture(t, `
steps:
  - name: run-local-on-worker
    action: dag.run
    with:
      dag: local-sub

---
name: local-sub
worker_selector:
  type: test-worker
steps:
  - name: worker-task
    run: |
      echo "Start task"
      exit 1
`, withLabels(map[string]string{"type": "test-worker"}))

		agent := f.dagWrapper.Agent()

		err := agent.Run(agent.Context)
		require.Error(t, err)

		f.dagWrapper.AssertLatestStatus(t, ir.Failed)

		st, statusErr := f.latestStatus()
		require.NoError(t, statusErr)
		require.Len(t, st.Nodes, 1)

		node := st.Nodes[0]
		require.Equal(t, "run-local-on-worker", node.Step.Name)
		require.Equal(t, ir.NodeFailed, node.Status)
		require.Len(t, node.SubRuns, 1)
	})
}

func TestSubDAG_UnmetChildPreconditionContinuesOnSkipped(t *testing.T) {
	f := newTestFixture(t, `
steps:
  - id: call_child
    action: dag.run
    with:
      dag: guarded-child
    continue_on: skipped
  - id: after_child
    depends: [call_child]
    run: echo "continued"

---
name: guarded-child
worker_selector:
  type: test-worker
preconditions:
  - condition: "blocked"
    expected: "ready"
steps:
  - id: should_not_run
    run: echo "should not run"
`, withLabels(map[string]string{"type": "test-worker"}))

	f.dagWrapper.Agent().RunSuccess(t)
	parentStatus, err := f.latestStatus()
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, parentStatus.Status)

	callChild := requireNodeByID(t, parentStatus, "call_child")
	require.Equal(t, ir.NodeSkipped, callChild.Status)
	require.Len(t, callChild.SubRuns, 1)
	require.Equal(t, ir.NodeSucceeded, requireNodeByID(t, parentStatus, "after_child").Status)

	subAttempt, err := f.coord.DAGRunRepository.FindSubAttempt(
		f.coord.Context,
		ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID),
		callChild.SubRuns[0].DAGRunID,
	)
	require.NoError(t, err)

	childStatus, err := subAttempt.ReadStatus(f.coord.Context)
	require.NoError(t, err)
	require.Equal(t, ir.Aborted, childStatus.Status)
	require.Equal(t, ir.NodeNotStarted, requireNodeByID(t, *childStatus, "should_not_run").Status)
}

func TestSubDAG_NoMatchingWorker(t *testing.T) {
	t.Run("failsWhenNoWorkerMatchesSelector", func(t *testing.T) {
		f := newTestFixture(t, `
steps:
  - name: run-on-nonexistent-worker
    action: dag.run
    with:
      dag: local-sub
    output: RESULT

---

name: local-sub
worker_selector:
  type: nonexistent-worker
steps:
  - name: worker-task
    run: echo "Should not run"
    output: MESSAGE
`, withWorkerCount(0))

		agent := f.dagWrapper.Agent()

		ctx, cancel := context.WithTimeout(f.coord.Context, 5*time.Second)
		defer cancel()
		err := agent.Run(ctx)
		require.Error(t, err)

		st := agent.Status(f.coord.Context)
		require.NotEqual(t, ir.Succeeded, st.Status)
	})
}

func TestSubDAG_DifferentWorkers(t *testing.T) {
	t.Run("parentAndChildOnDifferentWorkers", func(t *testing.T) {
		childYAML := `
name: child-remote
worker_selector:
  type: child
steps:
  - name: child-step
    run: echo "child executed"
`
		f := newTestFixture(t, `
name: parent-remote
worker_selector:
  type: parent
steps:
  - action: dag.run
    with:
      dag: child-remote
`, withLabels(map[string]string{"type": "parent"}))
		defer f.cleanup()

		f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "child-remote", []byte(childYAML))

		childWorker := f.setupWorker("child-worker", map[string]string{"type": "child"}, "")
		_ = childWorker

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 25*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
	})
}

func TestSubDAG_SharedNothingNestedNamedFileDependencies(t *testing.T) {
	const (
		stageDependency = "stage-input.txt"
		stageContent    = "dependency for stage worker"
		leafDependency  = "leaf-input.txt"
		leafContent     = "dependency for leaf worker"
	)

	f := newTestFixture(t, `
name: shared-nothing-root
worker_selector:
  layer: root
steps:
  - id: call_stage
    action: dag.run
    with:
      dag: shared-nothing-stage
`, withWorkerCount(0), withLogPersistence())
	defer f.cleanup()

	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "shared-nothing-stage", []byte(`
name: shared-nothing-stage
worker_selector:
  layer: stage
steps:
  - id: read_stage_dependency
    dependencies:
      - `+stageDependency+`
    action: file.read
    with:
      path: `+stageDependency+`
    output: CONTENT
  - id: call_leaf
    depends: [read_stage_dependency]
    action: dag.run
    with:
      dag: shared-nothing-leaf
`))
	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "shared-nothing-leaf", []byte(`
name: shared-nothing-leaf
worker_selector:
  layer: leaf
steps:
  - id: read_leaf_dependency
    dependencies:
      - `+leafDependency+`
    action: file.read
    with:
      path: `+leafDependency+`
    output: CONTENT
`))
	require.NoError(t, os.WriteFile(filepath.Join(f.coord.Config.Paths.DAGsDir, stageDependency), []byte(stageContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(f.coord.Config.Paths.DAGsDir, leafDependency), []byte(leafContent), 0o600))

	f.workers = append(f.workers,
		f.setupWorkerMode("root-worker", map[string]string{"layer": "root"}, "", true, nil),
		f.setupWorkerMode("stage-worker", map[string]string{"layer": "stage"}, "", true, nil),
		f.setupWorkerMode("leaf-worker", map[string]string{"layer": "leaf"}, "", true, nil),
	)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	rootStatus := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	require.Equal(t, "root-worker", rootStatus.WorkerID)
	rootCall := requireNodeByID(t, rootStatus, "call_stage")
	require.Len(t, rootCall.SubRuns, 1)

	rootRef := ir.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
	stageStatus := readDistributedSubAttemptStatus(t, f, rootRef, rootCall.SubRuns[0].DAGRunID)
	require.Equal(t, ir.Succeeded, stageStatus.Status)
	require.Equal(t, "stage-worker", stageStatus.WorkerID)
	require.Equal(t, rootRef, stageStatus.Root)
	require.Equal(t, rootRef, stageStatus.Parent)
	require.Equal(t, stageContent, nodeOutputValue(t, requireNodeByID(t, *stageStatus, "read_stage_dependency"), "CONTENT"))

	stageCall := requireNodeByID(t, *stageStatus, "call_leaf")
	require.Len(t, stageCall.SubRuns, 1)
	leafStatus := readDistributedSubAttemptStatus(t, f, rootRef, stageCall.SubRuns[0].DAGRunID)
	require.Equal(t, ir.Succeeded, leafStatus.Status)
	require.Equal(t, "leaf-worker", leafStatus.WorkerID)
	require.Equal(t, rootRef, leafStatus.Root)
	require.Equal(t, ir.NewDAGRunRef(stageStatus.Name, stageStatus.DAGRunID), leafStatus.Parent)
	require.Equal(t, leafContent, nodeOutputValue(t, requireNodeByID(t, *leafStatus, "read_leaf_dependency"), "CONTENT"))
}

func TestSubDAG_DispatchedParentRunsInlineNamedChild(t *testing.T) {
	f := newTestFixture(t, `
name: nested-inline-root
worker_selector:
  layer: root
steps:
  - id: call_parent
    action: dag.run
    with:
      dag: nested-inline-parent
`, withWorkerCount(0), withLogPersistence())
	defer f.cleanup()

	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "nested-inline-parent", []byte(`
name: nested-inline-parent
worker_selector:
  layer: parent
steps:
  - id: call_child
    action: dag.run
    with:
      dag: nested-inline-child
`))
	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "nested-inline-child", []byte(`
name: nested-inline-child
steps:
  - id: complete
    run: echo inline child completed
`))

	f.workers = append(f.workers,
		f.setupWorkerMode("root-worker", map[string]string{"layer": "root"}, "", true, nil),
		f.setupWorkerMode("parent-worker", map[string]string{"layer": "parent"}, "", true, nil),
	)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	rootStatus := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	require.Equal(t, "root-worker", rootStatus.WorkerID)
	rootCall := requireNodeByID(t, rootStatus, "call_parent")
	require.Len(t, rootCall.SubRuns, 1)

	rootRef := ir.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
	parentStatus := readDistributedSubAttemptStatus(t, f, rootRef, rootCall.SubRuns[0].DAGRunID)
	require.Equal(t, ir.Succeeded, parentStatus.Status)
	require.Equal(t, "parent-worker", parentStatus.WorkerID)
	parentCall := requireNodeByID(t, *parentStatus, "call_child")
	require.Len(t, parentCall.SubRuns, 1)

	childStatus := readDistributedSubAttemptStatus(t, f, rootRef, parentCall.SubRuns[0].DAGRunID)
	require.Equal(t, ir.Succeeded, childStatus.Status)
	require.Equal(t, "parent-worker", childStatus.WorkerID)
	require.Equal(t, ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID), childStatus.Parent)
}

func TestSubDAG_DispatchedParentDefersSourceLessNamedChildDependencies(t *testing.T) {
	const (
		childDependency = "nested-inline-child.txt"
		childContent    = "dependency packaged by the coordinator"
	)

	f := newTestFixture(t, `
name: nested-dependency-root
worker_selector:
  layer: root
steps:
  - id: call_parent
    action: dag.run
    with:
      dag: nested-dependency-parent
`, withWorkerCount(0), withLogPersistence())
	defer f.cleanup()

	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "nested-dependency-parent", []byte(`
name: nested-dependency-parent
worker_selector:
  layer: parent
steps:
  - id: call_child
    action: dag.run
    with:
      dag: nested-dependency-child
`))
	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "nested-dependency-child", []byte(`
name: nested-dependency-child
steps:
  - id: read_dependency
    dependencies:
      - `+childDependency+`
    action: file.read
    with:
      path: `+childDependency+`
    output: CONTENT
`))
	require.NoError(t, os.WriteFile(filepath.Join(f.coord.Config.Paths.DAGsDir, childDependency), []byte(childContent), 0o600))

	f.workers = append(f.workers,
		f.setupWorkerMode("root-worker", map[string]string{"layer": "root"}, "", true, nil),
		f.setupWorkerMode("parent-worker", map[string]string{"layer": "parent"}, "", true, nil),
	)

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	rootStatus := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	rootCall := requireNodeByID(t, rootStatus, "call_parent")
	require.Len(t, rootCall.SubRuns, 1)

	rootRef := ir.NewDAGRunRef(rootStatus.Name, rootStatus.DAGRunID)
	parentStatus := readDistributedSubAttemptStatus(t, f, rootRef, rootCall.SubRuns[0].DAGRunID)
	parentCall := requireNodeByID(t, *parentStatus, "call_child")
	require.Len(t, parentCall.SubRuns, 1)

	childStatus := readDistributedSubAttemptStatus(t, f, rootRef, parentCall.SubRuns[0].DAGRunID)
	require.Equal(t, ir.Succeeded, childStatus.Status)
	require.Equal(t, ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID), childStatus.Parent)
	require.Equal(t, childContent, nodeOutputValue(t, requireNodeByID(t, *childStatus, "read_dependency"), "CONTENT"))
}

func TestSubDAG_InSameFile(t *testing.T) {
	t.Run("parentAndChildInSameYAMLFile", func(t *testing.T) {
		f := newTestFixture(t, `
steps:
  - action: dag.run
    with:
      dag: dotest
params:
  - URL: default_value
---
name: dotest
worker_selector:
  foo: bar
steps:
  - name: task
    run: echo "Sub-DAG executed"
`, withLabels(map[string]string{"foo": "bar"}))
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.start())

		status := f.waitForStatus(ir.Succeeded, 20*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
	})
}

func TestSubDAG_ParentWithInlineChildOnWorker(t *testing.T) {
	t.Run("parentDispatchedToWorkerWithInlineSubDAG", func(t *testing.T) {
		// The parent DAG has a worker_selector so the entire multi-document
		// YAML is sent to the worker. The worker loads it with
		// WithName(task.Target), which previously overrode ALL document
		// names (including inline sub-DAGs), causing LocalDAGs lookup to
		// fail with "file does not exist".
		//
		// The inline child also has a worker_selector so it dispatches
		// through the coordinator because workers execute task payloads
		// without a local DAGRunRepository for subprocess-based sub-DAG execution.
		f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - name: call-child
    action: dag.run
    with:
      dag: inline-child
---
name: inline-child
worker_selector:
  test: "true"
steps:
  - name: task
    run: echo "inline child executed"
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 25*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
		f.assertAllNodesSucceeded(status)
	})
}

func TestSubDAG_PassEnvDistributed(t *testing.T) {
	readChildResult := func(t *testing.T, f *testFixture) string {
		t.Helper()
		parentStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, parentStatus.Status)
		require.Len(t, parentStatus.Nodes, 1)
		require.Len(t, parentStatus.Nodes[0].SubRuns, 1)

		subRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID
		subAttempt, err := f.coord.DAGRunRepository.FindSubAttempt(
			f.coord.Context,
			ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID),
			subRunID,
		)
		require.NoError(t, err)

		childStatus, err := subAttempt.ReadStatus(f.coord.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, childStatus.Status)
		require.Len(t, childStatus.Nodes, 1)
		return nodeOutputValue(t, childStatus.Nodes[0], "RESULT")
	}

	t.Run("selective", func(t *testing.T) {
		f := newTestFixture(t, `
env:
  - TODAY: "2026-03-05"
  - NOT_LISTED: not-requested
steps:
  - name: run-child
    action: dag.run
    with:
      dag: env-child
    pass_env: [TODAY, GH_USER]

---
name: env-child
worker_selector:
  type: test-worker
steps:
  - name: report
    run: echo "TODAY=${TODAY} GH=${GH_USER} NL=${NOT_LISTED}"
    output: RESULT
`, withLabels(map[string]string{"type": "test-worker"}))
		defer f.cleanup()

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)

		require.Equal(t, "TODAY=2026-03-05 GH= NL=", readChildResult(t, f))
	})

	t.Run("all", func(t *testing.T) {
		f := newTestFixture(t, `
env:
  - TODAY: "2026-03-05"
  - NOT_LISTED: not-requested
steps:
  - name: run-child
    action: dag.run
    with:
      dag: env-child
    pass_env: true

---
name: env-child
worker_selector:
  type: test-worker
steps:
  - name: report
    run: echo "TODAY=${TODAY} NL=${NOT_LISTED}"
    output: RESULT
`, withLabels(map[string]string{"type": "test-worker"}))
		defer f.cleanup()

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)

		require.Equal(t, "TODAY=2026-03-05 NL=not-requested", readChildResult(t, f))
	})

	t.Run("defaultNoInheritance", func(t *testing.T) {
		f := newTestFixture(t, `
env:
  - TODAY: "2026-03-05"
steps:
  - name: run-child
    action: dag.run
    with:
      dag: env-child

---
name: env-child
worker_selector:
  type: test-worker
steps:
  - name: report
    run: echo "TODAY=${TODAY}"
    output: RESULT
`, withLabels(map[string]string{"type": "test-worker"}))
		defer f.cleanup()

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)

		require.Equal(t, "TODAY=", readChildResult(t, f))
	})

	t.Run("parallel", func(t *testing.T) {
		f := newTestFixture(t, `
env:
  - TODAY: "2026-03-05"
steps:
  - name: run-children
    action: dag.run
    with:
      dag: env-child
      params: "ITEM_ID=${ITEM.id}"
    pass_env: [TODAY]
    parallel:
      items:
        - id: a
        - id: b

---
name: env-child
worker_selector:
  type: test-worker
params:
  - ITEM_ID
steps:
  - name: report
    run: echo "${ITEM_ID}:${TODAY}"
    output: RESULT
`, withLabels(map[string]string{"type": "test-worker"}))
		defer f.cleanup()

		agent := f.dagWrapper.Agent()
		agent.RunSuccess(t)

		parentStatus, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, parentStatus.Status)
		require.Len(t, parentStatus.Nodes, 1)
		require.Len(t, parentStatus.Nodes[0].SubRuns, 2)

		rootRef := ir.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID)
		results := map[string]string{}
		for _, sub := range parentStatus.Nodes[0].SubRuns {
			childStatus := readDistributedSubAttemptStatus(t, f, rootRef, sub.DAGRunID)
			require.Equal(t, ir.Succeeded, childStatus.Status)
			result := nodeOutputValue(t, childStatus.Nodes[0], "RESULT")
			results[strings.Split(result, ":")[0]] = result
		}
		require.Equal(t, "a:2026-03-05", results["a"])
		require.Equal(t, "b:2026-03-05", results["b"])
	})
}
