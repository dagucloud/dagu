// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

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
		f.dagWrapper.AssertLatestStatus(t, core.Succeeded)
	})
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
		subAttempt, err := f.coord.DAGRunStore.FindSubAttempt(
			f.coord.Context,
			exec.NewDAGRunRef(parentStatus.Name, parentStatus.DAGRunID),
			subRunID,
		)
		require.NoError(t, err)

		childStatus, err := subAttempt.ReadStatus(f.coord.Context)
		require.NoError(t, err)
		require.NotNil(t, childStatus)
		require.Equal(t, core.Succeeded, childStatus.Status)
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

		f.dagWrapper.AssertLatestStatus(t, core.Failed)

		st, statusErr := f.latestStatus()
		require.NoError(t, statusErr)
		require.Len(t, st.Nodes, 1)

		node := st.Nodes[0]
		require.Equal(t, "run-local-on-worker", node.Step.Name)
		require.Equal(t, core.NodeFailed, node.Status)
		require.Len(t, node.SubRuns, 1)
	})
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
		require.NotEqual(t, core.Succeeded, st.Status)
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

		childWorker := f.setupSharedNothingWorker("child-worker", map[string]string{"type": "child"}, "")
		_ = childWorker

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(core.Succeeded, 25*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
	})
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

		status := f.waitForStatus(core.Succeeded, 20*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
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
		// through the coordinator (shared-nothing workers don't have a
		// local DAGRunStore for subprocess-based sub-DAG execution).
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

		status := f.waitForStatus(core.Succeeded, 25*time.Second)

		require.Equal(t, core.Succeeded, status.Status)
		f.assertAllNodesSucceeded(status)
	})
}
