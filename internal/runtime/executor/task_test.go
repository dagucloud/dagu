// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAG_CreateTask(t *testing.T) {
	t.Parallel()

	t.Run("BasicTaskCreation", func(t *testing.T) {
		t.Parallel()

		dagName := "test-dag"
		yamlDefinition := `
name: test-dag
steps:
  - name: step1
    run: echo hello
`
		runID := "run-123"
		params := "param1=value1"
		selector := map[string]string{
			"gpu":    "true",
			"region": "us-east-1",
		}

		task := executor.CreateTask(
			dagName,
			yamlDefinition,
			coordinatorv1.Operation_OPERATION_START,
			runID,
			executor.WithTaskParams(params),
			executor.WithWorkerSelector(selector),
		)

		assert.NotNil(t, task)
		assert.Equal(t, "test-dag", task.RootDagRunName)
		assert.Equal(t, runID, task.RootDagRunId)
		assert.Equal(t, coordinatorv1.Operation_OPERATION_START, task.Operation)
		assert.Equal(t, runID, task.DagRunId)
		assert.Equal(t, "test-dag", task.Target)
		assert.Equal(t, params, task.Params)
		assert.Equal(t, selector, task.WorkerSelector)
		assert.Equal(t, yamlDefinition, task.Definition)
		// Parent fields should be empty when no options provided
		assert.Empty(t, task.ParentDagRunName)
		assert.Empty(t, task.ParentDagRunId)
	})

	t.Run("WithRootDagRunOption", func(t *testing.T) {
		t.Parallel()

		dag := &core.DAG{
			Name: "sub-dag",
		}

		rootRef := exec.DAGRunRef{
			Name: "root-dag",
			ID:   "root-run-123",
		}

		task := executor.CreateTask(
			dag.Name,
			string(dag.YamlData),
			coordinatorv1.Operation_OPERATION_RETRY,
			"child-run-456",
			executor.WithRootDagRun(rootRef),
		)

		assert.Equal(t, "root-dag", task.RootDagRunName)
		assert.Equal(t, "root-run-123", task.RootDagRunId)
		assert.Equal(t, "child-run-456", task.DagRunId)
		assert.Equal(t, "sub-dag", task.Target)
	})

	t.Run("WithParentDagRunOption", func(t *testing.T) {
		t.Parallel()

		parentRef := exec.DAGRunRef{
			Name: "parent-dag",
			ID:   "parent-run-789",
		}

		task := executor.CreateTask(
			"sub-dag",
			`name: sub-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"child-run-456",
			executor.WithParentDagRun(parentRef),
		)

		assert.Equal(t, "parent-dag", task.ParentDagRunName)
		assert.Equal(t, "parent-run-789", task.ParentDagRunId)
		assert.Equal(t, "sub-dag", task.RootDagRunName)
		assert.Equal(t, "child-run-456", task.RootDagRunId)
	})

	t.Run("WithMultipleOptions", func(t *testing.T) {
		t.Parallel()

		rootRef := exec.DAGRunRef{
			Name: "root-dag",
			ID:   "root-run-123",
		}
		parentRef := exec.DAGRunRef{
			Name: "parent-dag",
			ID:   "parent-run-456",
		}

		task := executor.CreateTask(
			"grandsub-dag",
			`name: grandsub-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"grandchild-run-789",
			executor.WithTaskParams("nested=true"),
			executor.WithWorkerSelector(map[string]string{"env": "prod"}),
			executor.WithRootDagRun(rootRef),
			executor.WithParentDagRun(parentRef),
		)

		assert.Equal(t, "root-dag", task.RootDagRunName)
		assert.Equal(t, "root-run-123", task.RootDagRunId)
		assert.Equal(t, "parent-dag", task.ParentDagRunName)
		assert.Equal(t, "parent-run-456", task.ParentDagRunId)
		assert.Equal(t, "grandchild-run-789", task.DagRunId)
		assert.Equal(t, "grandsub-dag", task.Target)
		assert.Equal(t, "nested=true", task.Params)
		assert.Equal(t, map[string]string{"env": "prod"}, task.WorkerSelector)
	})

	t.Run("EmptyWorkerSelector", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
		)

		assert.Nil(t, task.WorkerSelector)
	})

	t.Run("OptionsWithEmptyRefs", func(t *testing.T) {
		t.Parallel()

		// Test that empty refs don't modify the task
		emptyRootRef := exec.DAGRunRef{}
		emptyParentRef := exec.DAGRunRef{Name: "", ID: ""}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
			executor.WithRootDagRun(emptyRootRef),
			executor.WithParentDagRun(emptyParentRef),
		)

		// Should use DAG name and runID as root values
		assert.Equal(t, "test-dag", task.RootDagRunName)
		assert.Equal(t, "run-123", task.RootDagRunId)
		// Parent fields should remain empty
		assert.Empty(t, task.ParentDagRunName)
		assert.Empty(t, task.ParentDagRunId)
	})

	t.Run("PartiallyEmptyRefs", func(t *testing.T) {
		t.Parallel()

		// Test refs with only one field set
		partialRootRef := exec.DAGRunRef{Name: "root-dag", ID: ""}
		partialParentRef := exec.DAGRunRef{Name: "", ID: "parent-id"}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
			executor.WithRootDagRun(partialRootRef),
			executor.WithParentDagRun(partialParentRef),
		)

		// Partial refs should not modify the task
		assert.Equal(t, "test-dag", task.RootDagRunName)
		assert.Equal(t, "run-123", task.RootDagRunId)
		assert.Empty(t, task.ParentDagRunName)
		assert.Empty(t, task.ParentDagRunId)
	})

	t.Run("CustomTaskOption", func(t *testing.T) {
		t.Parallel()

		// Create a custom task option
		withStep := func(step string) executor.TaskOption {
			return func(task *coordinatorv1.Task) {
				task.Step = step
			}
		}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_RETRY,
			"run-123",
			withStep("step-2"),
		)

		assert.Equal(t, "step-2", task.Step)
		assert.Equal(t, coordinatorv1.Operation_OPERATION_RETRY, task.Operation)
	})

	t.Run("WithLabelsOption", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
			executor.WithLabels("env=prod,region=us-east-1"),
		)

		assert.Equal(t, "env=prod,region=us-east-1", task.Labels)
	})

	t.Run("WithScheduleTimeOption", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
			executor.WithScheduleTime("2026-03-13T10:00:00Z"),
		)

		assert.Equal(t, "2026-03-13T10:00:00Z", task.ScheduleTime)
	})

	t.Run("WithExternalStepRetryOption", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			coordinatorv1.Operation_OPERATION_START,
			"run-123",
			executor.WithExternalStepRetry(true),
		)

		assert.True(t, task.ExternalStepRetry)
	})

	t.Run("AllOperationTypes", func(t *testing.T) {
		t.Parallel()

		operations := []coordinatorv1.Operation{
			coordinatorv1.Operation_OPERATION_UNSPECIFIED,
			coordinatorv1.Operation_OPERATION_START,
			coordinatorv1.Operation_OPERATION_RETRY,
		}

		for _, op := range operations {
			task := executor.CreateTask(
				"test-dag",
				`name: test-dag`,
				op,
				"run-123",
			)
			assert.Equal(t, op, task.Operation)
		}
	})
}

func TestTaskOption_Functions(t *testing.T) {
	t.Parallel()

	t.Run("WithRootDagRun", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		ref := exec.DAGRunRef{Name: "root", ID: "123"}

		executor.WithRootDagRun(ref)(task)

		assert.Equal(t, "root", task.RootDagRunName)
		assert.Equal(t, "123", task.RootDagRunId)
	})

	t.Run("WithParentDagRun", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		ref := exec.DAGRunRef{Name: "parent", ID: "456"}

		executor.WithParentDagRun(ref)(task)

		assert.Equal(t, "parent", task.ParentDagRunName)
		assert.Equal(t, "456", task.ParentDagRunId)
	})

	t.Run("WithTaskParams", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithTaskParams("key1=value1 key2=value2")(task)

		assert.Equal(t, "key1=value1 key2=value2", task.Params)
	})

	t.Run("WithWorkerSelector", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		selector := map[string]string{
			"gpu":    "true",
			"region": "us-west-2",
		}

		executor.WithWorkerSelector(selector)(task)

		assert.Equal(t, selector, task.WorkerSelector)
	})

	t.Run("WithStep", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithStep("step-name")(task)

		assert.Equal(t, "step-name", task.Step)
	})

	t.Run("WithLabels", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithLabels("env=prod,team=backend")(task)

		assert.Equal(t, "env=prod,team=backend", task.Labels)
	})

	t.Run("WithLabelsEmpty", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithLabels("")(task)

		assert.Empty(t, task.Labels)
	})

	t.Run("WithTags", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithTags("env=prod,team=backend")(task)

		assert.Equal(t, "env=prod,team=backend", task.Labels)
	})

	t.Run("WithTagsEmpty", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		executor.WithTags("")(task)

		assert.Empty(t, task.Labels)
	})

	t.Run("WithPreviousStatus", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		status := &exec.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			ProcGroup: "shared-queue",
			Status:    core.Running,
			Nodes: []*exec.Node{
				{Step: core.Step{Name: "step1"}, Status: core.NodeSucceeded},
				{Step: core.Step{Name: "step2"}, Status: core.NodeFailed},
			},
		}

		executor.WithPreviousStatus(status)(task)

		assert.NotNil(t, task.PreviousStatus)
		// Verify via JSON conversion
		s, convErr := convert.ProtoToDAGRunStatus(task.PreviousStatus)
		require.NoError(t, convErr)
		assert.NotNil(t, s)
		assert.Equal(t, "test-dag", s.Name)
		assert.Equal(t, "run-123", s.DAGRunID)
		assert.Equal(t, core.Running, s.Status)
		assert.Len(t, s.Nodes, 2)
		assert.Equal(t, "shared-queue", task.QueueName)
	})

	t.Run("WithPreviousStatusNil", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}

		// Should not panic with nil status
		executor.WithPreviousStatus(nil)(task)

		assert.Nil(t, task.PreviousStatus)
	})

	t.Run("WithExternalStepRetry", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		executor.WithExternalStepRetry(true)(task)

		assert.True(t, task.ExternalStepRetry)
	})

	t.Run("WithSourceFile", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		executor.WithSourceFile("/dags/test-dag.yaml")(task)

		assert.Equal(t, "/dags/test-dag.yaml", task.SourceFile)
	})

	t.Run("WithAgentSnapshot", func(t *testing.T) {
		t.Parallel()

		task := &coordinatorv1.Task{}
		snapshot := []byte("agent-snapshot")
		want := append([]byte(nil), snapshot...)
		executor.WithAgentSnapshot(snapshot)(task)
		snapshot[0] = 'X'

		assert.Equal(t, want, task.AgentSnapshot)
		assert.NotEqual(t, snapshot, task.AgentSnapshot)
	})
}
