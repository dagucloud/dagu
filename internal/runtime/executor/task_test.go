// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareDAGWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts", "handler.sh"), []byte("handler"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "foreach.yaml"), []byte("foreach"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "child.txt"), []byte("child"), 0o644))

	dagData := []byte("name: root\nsteps:\n  - run: echo root\n")
	dag := &ir.DAG{
		Name:       "root",
		SourceFile: filepath.Join(root, "dag.yaml"),
		YamlData:   dagData,
		Steps: []ir.Step{
			{Dependencies: []string{"root.txt"}},
			{Foreach: &ir.ForeachConfig{Steps: []ir.Step{{Dependencies: []string{"config/**"}}}}},
		},
		HandlerOn: ir.HandlerOn{
			Failure: &ir.Step{Dependencies: []string{"scripts/handler.sh"}},
		},
		LocalDAGs: map[string]*ir.DAG{
			"child": {Steps: []ir.Step{{Dependencies: []string{"child.txt"}}}},
		},
	}

	seed, err := executor.PrepareDAGWorkspace(dag)
	require.NoError(t, err)
	require.NotNil(t, seed)
	assert.Equal(t, "dag.yaml", seed.Descriptor.DAGPath)

	dest := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, workspacebundle.Extract(seed.Archive, dest, seed.Descriptor, workspacebundle.DefaultLimits()))
	assert.FileExists(t, filepath.Join(dest, "root.txt"))
	assert.FileExists(t, filepath.Join(dest, "scripts", "handler.sh"))
	assert.FileExists(t, filepath.Join(dest, "config", "foreach.yaml"))
	assert.FileExists(t, filepath.Join(dest, "child.txt"))
	actualDAG, err := os.ReadFile(filepath.Join(dest, "dag.yaml"))
	require.NoError(t, err)
	assert.Equal(t, dagData, actualDAG)

	require.NoError(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("updated"), 0o644))
	updatedSeed, err := executor.PrepareDAGWorkspace(dag)
	require.NoError(t, err)
	require.NotNil(t, updatedSeed)
	assert.NotEqual(t, seed.Descriptor.Digest, updatedSeed.Descriptor.Digest)
}

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
			dispatch.DispatchOperationStart,
			runID,
			executor.WithTaskParams(params),
			executor.WithWorkerSelector(selector),
		)

		assert.NotNil(t, task)
		assert.Equal(t, "test-dag", task.RootDAGRunName)
		assert.Equal(t, runID, task.RootDAGRunID)
		assert.Equal(t, dispatch.DispatchOperationStart, task.Operation)
		assert.Equal(t, runID, task.DAGRunID)
		assert.Equal(t, "test-dag", task.Target)
		assert.Equal(t, params, task.Params)
		assert.Equal(t, selector, task.WorkerSelector)
		assert.Equal(t, yamlDefinition, task.Definition)
		// Parent fields should be empty when no options provided
		assert.Empty(t, task.ParentDAGRunName)
		assert.Empty(t, task.ParentDAGRunID)
	})

	t.Run("WithRootDagRunOption", func(t *testing.T) {
		t.Parallel()

		dag := &ir.DAG{
			Name: "sub-dag",
		}

		rootRef := ir.DAGRunRef{
			Name: "root-dag",
			ID:   "root-run-123",
		}

		task := executor.CreateTask(
			dag.Name,
			string(dag.YamlData),
			dispatch.DispatchOperationRetry,
			"child-run-456",
			executor.WithRootDagRun(rootRef),
		)

		assert.Equal(t, "root-dag", task.RootDAGRunName)
		assert.Equal(t, "root-run-123", task.RootDAGRunID)
		assert.Equal(t, "child-run-456", task.DAGRunID)
		assert.Equal(t, "sub-dag", task.Target)
	})

	t.Run("WithParentDagRunOption", func(t *testing.T) {
		t.Parallel()

		parentRef := ir.DAGRunRef{
			Name: "parent-dag",
			ID:   "parent-run-789",
		}

		task := executor.CreateTask(
			"sub-dag",
			`name: sub-dag`,
			dispatch.DispatchOperationStart,
			"child-run-456",
			executor.WithParentDagRun(parentRef),
		)

		assert.Equal(t, "parent-dag", task.ParentDAGRunName)
		assert.Equal(t, "parent-run-789", task.ParentDAGRunID)
		assert.Equal(t, "sub-dag", task.RootDAGRunName)
		assert.Equal(t, "child-run-456", task.RootDAGRunID)
	})

	t.Run("WithMultipleOptions", func(t *testing.T) {
		t.Parallel()

		rootRef := ir.DAGRunRef{
			Name: "root-dag",
			ID:   "root-run-123",
		}
		parentRef := ir.DAGRunRef{
			Name: "parent-dag",
			ID:   "parent-run-456",
		}

		task := executor.CreateTask(
			"grandsub-dag",
			`name: grandsub-dag`,
			dispatch.DispatchOperationStart,
			"grandchild-run-789",
			executor.WithTaskParams("nested=true"),
			executor.WithWorkerSelector(map[string]string{"env": "prod"}),
			executor.WithRootDagRun(rootRef),
			executor.WithParentDagRun(parentRef),
		)

		assert.Equal(t, "root-dag", task.RootDAGRunName)
		assert.Equal(t, "root-run-123", task.RootDAGRunID)
		assert.Equal(t, "parent-dag", task.ParentDAGRunName)
		assert.Equal(t, "parent-run-456", task.ParentDAGRunID)
		assert.Equal(t, "grandchild-run-789", task.DAGRunID)
		assert.Equal(t, "grandsub-dag", task.Target)
		assert.Equal(t, "nested=true", task.Params)
		assert.Equal(t, map[string]string{"env": "prod"}, task.WorkerSelector)
	})

	t.Run("EmptyWorkerSelector", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			dispatch.DispatchOperationStart,
			"run-123",
		)

		assert.Nil(t, task.WorkerSelector)
	})

	t.Run("OptionsWithEmptyRefs", func(t *testing.T) {
		t.Parallel()

		// Test that empty refs don't modify the task
		emptyRootRef := ir.DAGRunRef{}
		emptyParentRef := ir.DAGRunRef{Name: "", ID: ""}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			dispatch.DispatchOperationStart,
			"run-123",
			executor.WithRootDagRun(emptyRootRef),
			executor.WithParentDagRun(emptyParentRef),
		)

		// Should use DAG name and runID as root values
		assert.Equal(t, "test-dag", task.RootDAGRunName)
		assert.Equal(t, "run-123", task.RootDAGRunID)
		// Parent fields should remain empty
		assert.Empty(t, task.ParentDAGRunName)
		assert.Empty(t, task.ParentDAGRunID)
	})

	t.Run("PartiallyEmptyRefs", func(t *testing.T) {
		t.Parallel()

		// Test refs with only one field set
		partialRootRef := ir.DAGRunRef{Name: "root-dag", ID: ""}
		partialParentRef := ir.DAGRunRef{Name: "", ID: "parent-id"}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			dispatch.DispatchOperationStart,
			"run-123",
			executor.WithRootDagRun(partialRootRef),
			executor.WithParentDagRun(partialParentRef),
		)

		// Partial refs should not modify the task
		assert.Equal(t, "test-dag", task.RootDAGRunName)
		assert.Equal(t, "run-123", task.RootDAGRunID)
		assert.Empty(t, task.ParentDAGRunName)
		assert.Empty(t, task.ParentDAGRunID)
	})

	t.Run("CustomTaskOption", func(t *testing.T) {
		t.Parallel()

		// Create a custom task option
		withStep := func(step string) executor.TaskOption {
			return func(task *dispatch.DispatchTask) {
				task.Step = step
			}
		}

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			dispatch.DispatchOperationRetry,
			"run-123",
			withStep("step-2"),
		)

		assert.Equal(t, "step-2", task.Step)
		assert.Equal(t, dispatch.DispatchOperationRetry, task.Operation)
	})

	t.Run("WithLabelsOption", func(t *testing.T) {
		t.Parallel()

		task := executor.CreateTask(
			"test-dag",
			`name: test-dag`,
			dispatch.DispatchOperationStart,
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
			dispatch.DispatchOperationStart,
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
			dispatch.DispatchOperationStart,
			"run-123",
			executor.WithExternalStepRetry(true),
		)

		assert.True(t, task.ExternalStepRetry)
	})

	t.Run("AllOperationTypes", func(t *testing.T) {
		t.Parallel()

		operations := []dispatch.DispatchOperation{
			dispatch.DispatchOperationUnspecified,
			dispatch.DispatchOperationStart,
			dispatch.DispatchOperationRetry,
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

		task := &dispatch.DispatchTask{}
		ref := ir.DAGRunRef{Name: "root", ID: "123"}

		executor.WithRootDagRun(ref)(task)

		assert.Equal(t, "root", task.RootDAGRunName)
		assert.Equal(t, "123", task.RootDAGRunID)
	})

	t.Run("WithParentDagRun", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		ref := ir.DAGRunRef{Name: "parent", ID: "456"}

		executor.WithParentDagRun(ref)(task)

		assert.Equal(t, "parent", task.ParentDAGRunName)
		assert.Equal(t, "456", task.ParentDAGRunID)
	})

	t.Run("WithTaskParams", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithTaskParams("key1=value1 key2=value2")(task)

		assert.Equal(t, "key1=value1 key2=value2", task.Params)
	})

	t.Run("WithWorkerSelector", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		selector := map[string]string{
			"gpu":    "true",
			"region": "us-west-2",
		}

		executor.WithWorkerSelector(selector)(task)

		assert.Equal(t, selector, task.WorkerSelector)
	})

	t.Run("WithTargetWorkerID", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		executor.WithTargetWorkerID("worker-a")(task)

		assert.Equal(t, "worker-a", task.TargetWorkerID)
	})

	t.Run("WithStep", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithStep("step-name")(task)

		assert.Equal(t, "step-name", task.Step)
	})

	t.Run("WithIncludeDownstream", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithIncludeDownstream(true)(task)

		assert.True(t, task.IncludeDownstream)
	})

	t.Run("WithLabels", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithLabels("env=prod,team=backend")(task)

		assert.Equal(t, "env=prod,team=backend", task.Labels)
	})

	t.Run("WithLabelsEmpty", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithLabels("")(task)

		assert.Empty(t, task.Labels)
	})

	t.Run("WithTags", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithTags("env=prod,team=backend")(task)

		assert.Equal(t, "env=prod,team=backend", task.Labels)
	})

	t.Run("WithTagsEmpty", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		executor.WithTags("")(task)

		assert.Empty(t, task.Labels)
	})

	t.Run("WithPreviousStatus", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		status := &ir.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			ProcGroup: "shared-queue",
			Status:    ir.Running,
			Nodes: []*ir.Node{
				{Step: ir.Step{Name: "step1"}, Status: ir.NodeSucceeded},
				{Step: ir.Step{Name: "step2"}, Status: ir.NodeFailed},
			},
		}

		executor.WithPreviousStatus(status)(task)

		assert.Same(t, status, task.PreviousStatus)
		assert.Equal(t, "shared-queue", task.QueueName)
	})

	t.Run("WithPreviousStatusNil", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}

		// Should not panic with nil status
		executor.WithPreviousStatus(nil)(task)

		assert.Nil(t, task.PreviousStatus)
	})

	t.Run("WithExternalStepRetry", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		executor.WithExternalStepRetry(true)(task)

		assert.True(t, task.ExternalStepRetry)
	})

	t.Run("WithSourceFile", func(t *testing.T) {
		t.Parallel()

		task := &dispatch.DispatchTask{}
		executor.WithSourceFile("/dags/test-dag.yaml")(task)

		assert.Equal(t, "/dags/test-dag.yaml", task.SourceFile)
	})

}
